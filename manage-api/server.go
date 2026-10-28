package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// api holds what the handlers use. Authelia is nil on a server without it.
type api struct {
	tokenHashFile string
	oc            *openCloud
	authelia      *authelia
	log           *slog.Logger
	failures      *limiter
}

func serve(c config, log *slog.Logger) error {
	a := &api{
		tokenHashFile: c.TokenHash,
		oc:            newOpenCloud(c.OpenCloudURL, c.OpenCloudInsecure),
		log:           log,
		failures:      newLimiter(10, 10*time.Minute),
	}
	if c.AutheliaUsers != "" {
		a.authelia = newAuthelia(c.AutheliaUsers, c.AutheliaLock, c.AutheliaConfig)
	}
	if _, err := a.tokenHash(); err != nil {
		// Not fatal: setup.sh may write it after the start; until then
		// every request is refused
		log.Warn("no token yet, every request is refused", "error", err)
	}
	srv := &http.Server{
		Addr:              c.Addr,
		Handler:           a.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Authelia's hash and OpenCloud's CLI take a few seconds each
		WriteTimeout: 2 * time.Minute,
		IdleTimeout:  time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	var watchdog <-chan error
	if c.Watchdog > 0 {
		w := make(chan error, 1)
		go func() { w <- a.oc.watch(ctx, c.Watchdog) }()
		watchdog = w
	}
	log.Info("listening", "addr", c.Addr, "opencloud", c.OpenCloudURL, "authelia", a.authelia != nil)
	var err error
	select {
	case err = <-errc:
	case err = <-watchdog:
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// The largest request body: a user's fields, nothing bigger
const maxBody = 64 << 10

func (a *api) routes() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, h func(*http.Request) (any, error)) {
		mux.Handle(pattern, a.authorized(h))
	}
	handle("GET /v1/info", a.info)
	handle("GET /v1/authelia/users", a.autheliaList)
	handle("PUT /v1/authelia/users/{name}", a.autheliaPut)
	handle("DELETE /v1/authelia/users/{name}", a.autheliaDelete)
	handle("POST /v1/authelia/users/{name}/reset-2fa", a.autheliaReset2FA)
	handle("GET /v1/opencloud/users/{name}/emails", a.emailsGet)
	handle("PUT /v1/opencloud/users/{name}/emails", a.emailsPut)
	handle("POST /v1/opencloud/users/{name}/personal-space", a.personalSpace)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, errorBody{"no such endpoint"})
	})
	return mux
}

// httpError is an error with the status the API answers.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func errStatus(status int, format string, args ...any) error {
	return &httpError{status, fmt.Sprintf(format, args...)}
}

var errAdmin = errStatus(http.StatusForbidden, "this API doesn't act on admins; use users.sh on the server")

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// client names who asked, for the rate limit and the log: SWAG's
// X-Real-IP, otherwise the connection's address
func client(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" && net.ParseIP(ip) != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tokenHash reads the stored SHA-256 at each request, so a new token
// (setup.sh --new-manage-token) works without a restart.
func (a *api) tokenHash() ([]byte, error) {
	b, err := os.ReadFile(a.tokenHashFile)
	if err != nil {
		return nil, err
	}
	h, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(h) != sha256.Size {
		return nil, fmt.Errorf("%s isn't a hex SHA-256", a.tokenHashFile)
	}
	return h, nil
}

func (a *api) checkToken(r *http.Request) error {
	want, err := a.tokenHash()
	if err != nil {
		a.log.Error("token", "error", err)
		return errStatus(http.StatusServiceUnavailable, "no token set up on the server (setup.sh)")
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	got := sha256.Sum256([]byte(token))
	if !ok || subtle.ConstantTimeCompare(got[:], want) != 1 {
		return errStatus(http.StatusUnauthorized, "wrong or missing token")
	}
	return nil
}

// authorized checks the token, limits the body, runs h and logs every
// change (not reads) without bodies: no passwords in the log.
func (a *api) authorized(h func(*http.Request) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := client(r)
		if a.failures.blocked(who) {
			writeJSON(w, http.StatusTooManyRequests, errorBody{"too many failed attempts; try again later"})
			return
		}
		if err := a.checkToken(r); err != nil {
			var he *httpError
			if errors.As(err, &he) && he.status == http.StatusUnauthorized {
				a.failures.fail(who)
				a.log.Warn("refused", "client", who, "method", r.Method, "path", r.URL.Path)
			}
			a.answer(w, nil, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		v, err := h(r)
		if r.Method != http.MethodGet {
			attrs := []any{"client", who, "method", r.Method, "path", r.URL.Path}
			if err != nil {
				a.log.Warn("change failed", append(attrs, "error", err.Error())...)
			} else {
				a.log.Info("change", attrs...)
			}
		}
		a.answer(w, v, err)
	})
}

func (a *api) answer(w http.ResponseWriter, v any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, v)
		return
	}
	var he *httpError
	if errors.As(err, &he) {
		writeJSON(w, he.status, errorBody{he.msg})
		return
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorBody{"request too large"})
		return
	}
	a.log.Error("internal", "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{err.Error()})
}

// decode reads a JSON body into v, refusing unknown fields (a typo would
// otherwise change nothing without a word).
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return err
		}
		return errStatus(http.StatusBadRequest, "body: %v", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return errStatus(http.StatusBadRequest, "body: one JSON object only")
	}
	return nil
}

// --- Handlers

type infoBody struct {
	OpenCloud string `json:"opencloud"` // its version, from status.php
	Authelia  bool   `json:"authelia"`
	// Authelia asks the 2fa group for 2FA (two_factor can be turned on)
	TwoFactorGroup bool `json:"two_factor_group"`
}

func (a *api) info(r *http.Request) (any, error) {
	v, err := a.oc.version(r.Context())
	if err != nil {
		return nil, errStatus(http.StatusBadGateway, "OpenCloud: %v", err)
	}
	b := infoBody{OpenCloud: v, Authelia: a.authelia != nil}
	if a.authelia != nil {
		if b.TwoFactorGroup, err = a.authelia.twoFactorConfigured(); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (a *api) needAuthelia() error {
	if a.authelia == nil {
		return errStatus(http.StatusNotFound, "no Authelia on this server")
	}
	return nil
}

// name is the path's user name, checked as users.sh checks it.
func name(r *http.Request) (string, error) {
	n := r.PathValue("name")
	if err := validName(n); err != nil {
		return "", errStatus(http.StatusBadRequest, "%v", err)
	}
	return n, nil
}

// notAdmin refuses admins: in Authelia's admins group, or OpenCloud's Admin
// role. A user OpenCloud doesn't know has no role there.
func (a *api) notAdmin(ctx context.Context, user string) error {
	if a.authelia != nil {
		admin, err := a.authelia.isAdmin(user)
		if err != nil {
			return err
		}
		if admin {
			return errAdmin
		}
	}
	role, err := a.oc.role(ctx, user)
	if errors.Is(err, errNoUser) {
		return nil
	}
	if err != nil {
		return errStatus(http.StatusBadGateway, "OpenCloud: %v", err)
	}
	if role == roleAdmin {
		return errAdmin
	}
	return nil
}

func (a *api) autheliaList(r *http.Request) (any, error) {
	if err := a.needAuthelia(); err != nil {
		return nil, err
	}
	return a.authelia.list()
}

func (a *api) autheliaPut(r *http.Request) (any, error) {
	if err := a.needAuthelia(); err != nil {
		return nil, err
	}
	user, err := name(r)
	if err != nil {
		return nil, err
	}
	var req userChange
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := req.check(); err != nil {
		return nil, errStatus(http.StatusBadRequest, "%v", err)
	}
	if err := a.notAdmin(r.Context(), user); err != nil {
		return nil, err
	}
	created, err := a.authelia.put(r.Context(), user, req)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"created": created}, nil
}

func (a *api) autheliaDelete(r *http.Request) (any, error) {
	if err := a.needAuthelia(); err != nil {
		return nil, err
	}
	user, err := name(r)
	if err != nil {
		return nil, err
	}
	if err := a.notAdmin(r.Context(), user); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, a.authelia.remove(user)
}

func (a *api) autheliaReset2FA(r *http.Request) (any, error) {
	if err := a.needAuthelia(); err != nil {
		return nil, err
	}
	user, err := name(r)
	if err != nil {
		return nil, err
	}
	if err := a.notAdmin(r.Context(), user); err != nil {
		return nil, err
	}
	return map[string]bool{"reset": true}, a.authelia.reset2FA(r.Context(), user)
}

func (a *api) emailsGet(r *http.Request) (any, error) {
	user, err := name(r)
	if err != nil {
		return nil, err
	}
	var s emailSetting
	err = a.oc.asUser(r.Context(), user, func(u *userSession) error {
		s, err = u.emails()
		return err
	})
	return s, err
}

func (a *api) emailsPut(r *http.Request) (any, error) {
	user, err := name(r)
	if err != nil {
		return nil, err
	}
	var req struct {
		Disabled *bool `json:"disabled"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Disabled == nil {
		return nil, errStatus(http.StatusBadRequest, "body: disabled (true or false) is required")
	}
	var s emailSetting
	err = a.oc.asUser(r.Context(), user, func(u *userSession) error {
		s, err = u.setEmailsDisabled(*req.Disabled)
		return err
	})
	return s, err
}

func (a *api) personalSpace(r *http.Request) (any, error) {
	user, err := name(r)
	if err != nil {
		return nil, err
	}
	var id string
	err = a.oc.asUser(r.Context(), user, func(u *userSession) error {
		id, err = u.personalSpace()
		return err
	})
	return map[string]string{"id": id}, err
}

// --- Failed attempts

// limiter blocks a client after max failed attempts within window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	now    func() time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, fails: map[string][]time.Time{}, now: time.Now}
}

// recent drops attempts older than the window (call with mu held).
func (l *limiter) recent(who string) []time.Time {
	cut := l.now().Add(-l.window)
	ts := l.fails[who]
	i := 0
	for i < len(ts) && ts[i].Before(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, who)
	} else {
		l.fails[who] = ts
	}
	return ts
}

func (l *limiter) blocked(who string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(who)) >= l.max
}

func (l *limiter) fail(who string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Many addresses: forget the old ones rather than grow without end
	if len(l.fails) > 10000 {
		for k := range l.fails {
			l.recent(k)
		}
	}
	l.fails[who] = append(l.recent(who), l.now())
}
