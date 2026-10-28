package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// Role ids (OpenCloud's, and oc-custom's Client and Employee; also in
// users.sh)
const (
	roleAdmin = "71881883-1768-46bd-a24d-a356a2afdf7f"
	roleUser  = "d7beeea8-8ff4-406b-8fb6-ab2dd81e6b11"
)

// IDs from OpenCloud's settings service, the same on every server
const (
	profileBundle = "2a506de7-99bd-4f0d-994e-c38e72c28fd9"
	// "Disable Email Notifications": checked for each recipient when
	// OpenCloud sends, so it mutes all of OpenCloud's e-mails
	settingDisableEmails = "33ffb5d6-cd07-4dc0-afb0-84f7559ae438"
)

var errNoUser = errStatus(http.StatusNotFound, "no such user in OpenCloud")

// openCloud reaches OpenCloud inside its network namespace: its HTTP
// address, and its CLI (which talks to its internal services on 127.0.0.1
// with the read-only config in /etc/opencloud).
type openCloud struct {
	url  string
	http *http.Client
	// newToken makes an app token for user, valid for a minute
	newToken func(ctx context.Context, user string) (string, error)
	// Two CLI runs at a time: each starts a whole OpenCloud client (~3 s)
	cli chan struct{}
	log *slog.Logger
}

func newOpenCloud(base string, insecure bool) *openCloud {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // a test server's own certificate
	}
	o := &openCloud{
		url:  strings.TrimRight(base, "/"),
		http: &http.Client{Transport: tr, Timeout: 30 * time.Second},
		cli:  make(chan struct{}, 2),
		log:  slog.Default(),
	}
	o.newToken = o.cliToken
	return o
}

// cliToken runs `opencloud auth-app create`. With impersonation off it's the
// only way to act as a user; it needs OpenCloud's config and internal
// services, which this container has and the outside doesn't.
func (o *openCloud) cliToken(ctx context.Context, user string) (string, error) {
	select {
	case o.cli <- struct{}{}:
		defer func() { <-o.cli }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "opencloud", "auth-app", "create",
		"--user-name", user, "--expiration", "1m").CombinedOutput()
	if strings.Contains(string(out), "user not found") {
		return "", errNoUser
	}
	if err != nil {
		return "", fmt.Errorf("opencloud auth-app create: %v: %s", err, lastLine(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "token: "); ok && t != "" {
			return t, nil
		}
	}
	return "", errors.New("opencloud auth-app create printed no token")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// userSession is one user's token, for one action.
type userSession struct {
	o     *openCloud
	ctx   context.Context
	user  string
	token string
	role  string // the user's role id
}

// asUser makes a token for user, refuses admins, runs fn and revokes the
// token. If the revocation fails it still expires within a minute.
func (o *openCloud) asUser(ctx context.Context, user string, fn func(*userSession) error) error {
	s, err := o.session(ctx, user)
	if err != nil {
		return err
	}
	defer s.revoke()
	if s.role == roleAdmin {
		return errAdmin
	}
	return fn(s)
}

func (o *openCloud) session(ctx context.Context, user string) (*userSession, error) {
	token, err := o.newToken(ctx, user)
	if err != nil {
		return nil, err
	}
	s := &userSession{o: o, ctx: ctx, user: user, token: token}
	var me struct {
		Roles []struct {
			ID string `json:"appRoleId"`
		} `json:"appRoleAssignments"`
	}
	if err := s.call("GET", "/graph/v1.0/me?%24expand=appRoleAssignments", nil, &me); err != nil {
		s.revoke()
		return nil, err
	}
	if len(me.Roles) > 0 {
		s.role = me.Roles[0].ID
	}
	return s, nil
}

// role returns user's role id in OpenCloud (errNoUser when there's no such
// user).
func (o *openCloud) role(ctx context.Context, user string) (string, error) {
	s, err := o.session(ctx, user)
	if err != nil {
		return "", err
	}
	s.revoke()
	return s.role, nil
}

func (s *userSession) revoke() {
	// Its own context: the request may be over, the token must still go
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rs := &userSession{o: s.o, ctx: ctx, user: s.user, token: s.token}
	if err := rs.call("DELETE", "/auth-app/tokens?token="+url.QueryEscape(s.token), nil, nil); err != nil {
		s.o.log.Warn("couldn't revoke a user token; it expires within a minute", "user", s.user, "error", err)
	}
}

// call sends one request as the session's user; out (if not nil) gets the
// JSON answer.
func (s *userSession) call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(s.ctx, method, s.o.url+path, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.user, s.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.o.http.Do(req)
	if err != nil {
		return errStatus(http.StatusBadGateway, "OpenCloud: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return &openCloudError{status: resp.StatusCode, msg: fmt.Sprintf("OpenCloud: %s %s: HTTP %d %s",
			method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, strings.TrimSpace(string(b)))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// openCloudError is OpenCloud's answer to a request that failed; the API
// answers 502 with it.
type openCloudError struct {
	status int
	msg    string
}

func (e *openCloudError) Error() string { return e.msg }

func (e *openCloudError) As(target any) bool {
	if he, ok := target.(**httpError); ok {
		*he = &httpError{http.StatusBadGateway, e.msg}
		return true
	}
	return false
}

// --- E-mails

// emailSetting is a user's "Disable Email Notifications".
type emailSetting struct {
	Disabled bool `json:"disabled"`
	// The stored value's id, empty when the user never set it
	ValueID string `json:"value_id"`
}

type settingValue struct {
	ID          string `json:"id,omitempty"`
	BundleID    string `json:"bundleId"`
	SettingID   string `json:"settingId"`
	AccountUUID string `json:"accountUuid"`
	Resource    struct {
		Type string `json:"type"`
	} `json:"resource"`
	BoolValue bool `json:"boolValue"`
}

type settingAnswer struct {
	Value struct {
		Value settingValue `json:"value"`
	} `json:"value"`
}

func (s *userSession) emails() (emailSetting, error) {
	var a settingAnswer
	err := s.call("POST", "/api/v0/settings/values-get-by-unique-identifiers",
		map[string]string{"accountUuid": "me", "settingId": settingDisableEmails}, &a)
	var oe *openCloudError
	if errors.As(err, &oe) && oe.status == http.StatusNotFound {
		return emailSetting{}, nil // never set: e-mails are on
	}
	if err != nil {
		return emailSetting{}, err
	}
	return emailSetting{Disabled: a.Value.Value.BoolValue, ValueID: a.Value.Value.ID}, nil
}

// setEmailsDisabled saves the setting over the stored value (saving without
// its id adds a second value, and OpenCloud keeps reading the first).
func (s *userSession) setEmailsDisabled(disabled bool) (emailSetting, error) {
	cur, err := s.emails()
	if err != nil {
		return cur, err
	}
	v := settingValue{ID: cur.ValueID, BundleID: profileBundle, SettingID: settingDisableEmails,
		AccountUUID: "me", BoolValue: disabled}
	v.Resource.Type = "TYPE_USER"
	var a settingAnswer
	if err := s.call("POST", "/api/v0/settings/values-save", map[string]any{"value": v}, &a); err != nil {
		return cur, err
	}
	return emailSetting{Disabled: a.Value.Value.BoolValue, ValueID: a.Value.Value.ID}, nil
}

// --- The personal space

// personalSpace creates a User's personal space (asking for it does) and
// returns its id. Only Users have one: the other types' is disabled or
// missing, and must stay so.
func (s *userSession) personalSpace() (string, error) {
	if s.role != roleUser {
		return "", errStatus(http.StatusConflict, "only a User has a personal space")
	}
	var drives struct {
		Value []struct {
			ID   string `json:"id"`
			Type string `json:"driveType"`
		} `json:"value"`
	}
	if err := s.call("GET", "/graph/v1.0/me/drives", nil, &drives); err != nil {
		return "", err
	}
	for _, d := range drives.Value {
		if d.Type == "personal" {
			return d.ID, nil
		}
	}
	return "", errStatus(http.StatusBadGateway, "OpenCloud made no personal space for %s", s.user)
}

// --- Status

func (o *openCloud) version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", o.url+"/status.php", nil)
	if err != nil {
		return "", err
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var st struct {
		Version string `json:"productversion"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status.php: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return "", fmt.Errorf("status.php: %w", err)
	}
	return st.Version, nil
}

// watch returns an error once OpenCloud hasn't answered for limit. Then the
// container exits and its restart policy starts it again, in OpenCloud's
// current network namespace (after OpenCloud's container restarts, this
// one's namespace no longer has it).
func (o *openCloud) watch(ctx context.Context, limit time.Duration) error {
	last := time.Now()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := o.version(c)
		cancel()
		if err == nil {
			last = time.Now()
		} else if time.Since(last) > limit {
			return fmt.Errorf("OpenCloud hasn't answered for %s (last: %v); exiting so the restart finds it again", limit, err)
		}
	}
}
