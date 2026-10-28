package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// The shortest password; deploy/lib.sh (PASSWORD_MIN_LENGTH) and Authelia's
// password_policy have the same
const passwordMinLength = 3

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)

// twoFactorGroup: Authelia asks its members for 2FA (as admins), when its
// configuration has the rule (deploy/authelia/configuration.yml)
const twoFactorGroup = "2fa"

var twoFactorRule = regexp.MustCompile(`(?m)^[^#\n]*group:` + twoFactorGroup + `\b`)

// validName checks a username as users.sh does.
func validName(n string) error {
	if !nameRe.MatchString(n) || len(n) > 255 {
		return errors.New("usernames may only contain letters, digits and _ . @ -")
	}
	// . and .. would be other paths in OpenCloud's API
	if strings.HasPrefix(n, ".") {
		return errors.New("usernames can't start with a dot")
	}
	return nil
}

// authelia changes Authelia's user file as users.sh does (deploy/lib.sh,
// update_users): under users.sh's lock, the whole file built, checked, and
// renamed into place, never appended to; when Authelia rewrites it meanwhile
// (a user changed their password), the change is made again on top.
type authelia struct {
	usersFile string
	lockFile  string
	config    string
	// hash returns Authelia's argon2id digest of a password
	hash func(ctx context.Context, password string) (string, error)
	// cli runs Authelia's CLI with this server's config
	cli func(ctx context.Context, args ...string) (string, error)
}

func newAuthelia(usersFile, lockFile, config string) *authelia {
	a := &authelia{usersFile: usersFile, lockFile: lockFile, config: config}
	a.cli = a.runCLI
	a.hash = a.cliHash
	return a
}

func (a *authelia) runCLI(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "authelia", append([]string{"--config", a.config}, args...)...)
	// Authelia's own container sets its notifier in its entrypoint; the CLI
	// needs one too and sends nothing
	cmd.Env = append(os.Environ(), "AUTHELIA_NOTIFIER_FILESYSTEM_FILENAME=/tmp/cli-notifications.txt")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// cliHash uses Authelia's own hashing, so the digest is what Authelia
// expects. The password is on the tool's command line inside this container
// only (its CLI takes it no other way), as with users.sh.
func (a *authelia) cliHash(ctx context.Context, password string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "authelia", "crypto", "hash", "generate", "argon2",
		"--password="+password).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("authelia crypto hash: %v: %s", err, lastLine(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if d, ok := strings.CutPrefix(strings.TrimSpace(line), "Digest: "); ok && d != "" {
			return d, nil
		}
	}
	return "", errors.New("authelia crypto hash printed no digest")
}

// --- Reading

// autheliaUser is an entry as the API lists it: no password hash.
type autheliaUser struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayname"`
	Email       string `json:"email"`
	Disabled    bool   `json:"disabled"`
	Admin       bool   `json:"admin"` // in the admins group: read-only here
	// In the 2fa group: asked for 2FA (admins always are)
	TwoFactor bool `json:"two_factor"`
}

type fileUser struct {
	DisplayName string   `yaml:"displayname"`
	Email       string   `yaml:"email"`
	Disabled    bool     `yaml:"disabled"`
	Groups      []string `yaml:"groups"`
	Password    *string  `yaml:"password"`
}

func (u fileUser) in(group string) bool {
	for _, g := range u.Groups {
		if g == group {
			return true
		}
	}
	return false
}

func (u fileUser) admin() bool { return u.in("admins") }

func (a *authelia) read() (map[string]fileUser, error) {
	b, err := os.ReadFile(a.usersFile)
	if err != nil {
		return nil, errStatus(http.StatusServiceUnavailable, "Authelia's user file: %v (run setup.sh)", err)
	}
	var f struct {
		Users map[string]fileUser `yaml:"users"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("Authelia's user file: %w", err)
	}
	return f.Users, nil
}

func (a *authelia) list() ([]autheliaUser, error) {
	users, err := a.read()
	if err != nil {
		return nil, err
	}
	out := make([]autheliaUser, 0, len(users))
	for n, u := range users {
		out = append(out, autheliaUser{Name: n, DisplayName: u.DisplayName, Email: u.Email, Disabled: u.Disabled,
			Admin: u.admin(), TwoFactor: u.in(twoFactorGroup)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (a *authelia) isAdmin(user string) (bool, error) {
	users, err := a.read()
	if err != nil {
		return false, err
	}
	u, ok := users[user]
	return ok && u.admin(), nil
}

// --- Changes

// userChange is a PUT's body: the fields to set; a new entry needs
// displayname and password.
type userChange struct {
	DisplayName *string `json:"displayname"`
	Email       *string `json:"email"`
	Disabled    *bool   `json:"disabled"`
	Password    *string `json:"password"`
	// In the 2fa group or not
	TwoFactor *bool `json:"two_factor"`
}

func plainText(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (c userChange) check() error {
	if c.DisplayName != nil && (strings.TrimSpace(*c.DisplayName) == "" || len(*c.DisplayName) > 255 || !plainText(*c.DisplayName)) {
		return errors.New("displayname: up to 255 characters, not empty, no control characters")
	}
	if c.Email != nil && *c.Email != "" &&
		(len(*c.Email) > 254 || !strings.Contains(*c.Email, "@") || strings.ContainsAny(*c.Email, " \t") || !plainText(*c.Email)) {
		return errors.New("email: not an e-mail address")
	}
	if c.Password != nil && (len([]rune(*c.Password)) < passwordMinLength || len(*c.Password) > 1024 || strings.ContainsRune(*c.Password, 0)) {
		return fmt.Errorf("password: at least %d characters", passwordMinLength)
	}
	if c.DisplayName == nil && c.Email == nil && c.Disabled == nil && c.Password == nil && c.TwoFactor == nil {
		return errors.New("nothing to change")
	}
	return nil
}

// put creates user's entry or changes the given fields. It reports whether
// the entry is new.
func (a *authelia) put(ctx context.Context, user string, c userChange) (bool, error) {
	if c.TwoFactor != nil && *c.TwoFactor {
		ok, err := a.twoFactorConfigured()
		if err != nil {
			return false, err
		}
		if !ok {
			// The group alone would change nothing: say so instead
			return false, errStatus(http.StatusConflict, "Authelia's configuration doesn't ask the %s group for 2FA yet (update authelia/configuration.yml)", twoFactorGroup)
		}
	}
	digest := ""
	if c.Password != nil {
		// Before the lock: it takes about a second
		var err error
		if digest, err = a.hash(ctx, *c.Password); err != nil {
			return false, err
		}
	}
	created := false
	err := a.update(func(users *yaml.Node) error {
		entry := mapValue(users, user)
		if entry == nil {
			if c.DisplayName == nil || c.Password == nil {
				return errStatus(http.StatusBadRequest, "a new entry needs displayname and password")
			}
			entry = &yaml.Node{Kind: yaml.MappingNode}
			users.Content = append(users.Content, scalar(user), entry)
			setStr(entry, "displayname", *c.DisplayName)
			setStr(entry, "password", digest)
			setBool(entry, "disabled", false)
			setNode(entry, "groups", &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle})
			created = true
		} else if inAdmins(entry) {
			// Checked again under the lock
			return errAdmin
		}
		if c.DisplayName != nil {
			setStr(entry, "displayname", *c.DisplayName)
		}
		if c.Email != nil {
			setStr(entry, "email", *c.Email)
		}
		if c.Disabled != nil {
			setBool(entry, "disabled", *c.Disabled)
		}
		if c.Password != nil {
			setStr(entry, "password", digest)
		}
		if c.TwoFactor != nil {
			setGroup(entry, twoFactorGroup, *c.TwoFactor)
		}
		return nil
	})
	return created, err
}

// twoFactorConfigured reports whether Authelia's configuration names the
// 2fa group (in its two_factor rule; deploy/ has nothing else that could).
func (a *authelia) twoFactorConfigured() (bool, error) {
	b, err := os.ReadFile(a.config)
	if err != nil {
		return false, fmt.Errorf("Authelia's configuration: %w", err)
	}
	return twoFactorRule.Match(b), nil
}

func (a *authelia) remove(user string) error {
	return a.update(func(users *yaml.Node) error {
		for i := 0; i+1 < len(users.Content); i += 2 {
			if users.Content[i].Value == user {
				if inAdmins(users.Content[i+1]) {
					return errAdmin
				}
				users.Content = append(users.Content[:i], users.Content[i+2:]...)
				return nil
			}
		}
		return errStatus(http.StatusNotFound, "no such user in Authelia")
	})
}

// reset2FA removes the user's TOTP app and security keys.
func (a *authelia) reset2FA(ctx context.Context, user string) error {
	// "--": a name may start with "-"
	if out, err := a.cli(ctx, "storage", "user", "totp", "delete", "--", user); err != nil &&
		!strings.Contains(out, "no TOTP configuration") {
		return fmt.Errorf("Authelia: %s", lastLine(out))
	}
	if out, err := a.cli(ctx, "storage", "user", "webauthn", "delete", "--all", "--", user); err != nil {
		return fmt.Errorf("Authelia: %s", lastLine(out))
	}
	return nil
}

// update applies edit to the users mapping and writes the file (see
// authelia). Changes from different requests wait for each other and for
// users.sh on the lock.
func (a *authelia) update(edit func(users *yaml.Node) error) error {
	unlock, err := a.lock()
	if err != nil {
		return err
	}
	defer unlock()
	for range 3 {
		before, err := os.ReadFile(a.usersFile)
		if err != nil {
			return errStatus(http.StatusServiceUnavailable, "Authelia's user file: %v (run setup.sh)", err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(before, &doc); err != nil {
			return fmt.Errorf("Authelia's user file: %w", err)
		}
		users := usersNode(&doc)
		if users == nil {
			return errors.New("Authelia's user file has no users mapping")
		}
		if err := edit(users); err != nil {
			return err
		}
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return err
		}
		if err := validUsers(buf.Bytes()); err != nil {
			return fmt.Errorf("refusing to write Authelia's user file: %w", err)
		}
		done, err := a.replace(buf.Bytes(), sha256.Sum256(before))
		if err != nil || done {
			return err
		}
		// Changed meanwhile (Authelia): make the change again on top
	}
	return errors.New("Authelia's user file keeps changing; nothing written, try again")
}

// validUsers checks what Authelia needs: a users mapping whose entries have a
// password and a display name (Authelia stops on a broken file).
func validUsers(b []byte) error {
	var f struct {
		Users map[string]fileUser `yaml:"users"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return err
	}
	if f.Users == nil {
		return errors.New("no users mapping")
	}
	for n, u := range f.Users {
		if u.Password == nil || *u.Password == "" || u.DisplayName == "" {
			return fmt.Errorf("%s has no password or display name", n)
		}
	}
	return nil
}

// replace writes b next to the user file and renames it into place if the
// file is still what was read (sum). It reports false when it changed.
func (a *authelia) replace(b []byte, sum [32]byte) (bool, error) {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	tmp := fmt.Sprintf("%s.new.%d%s", a.usersFile, os.Getpid(), hex.EncodeToString(suffix))
	// 0600: the hashes are never readable by others, not even briefly
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp) // gone after the rename; left over otherwise
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	now, err := os.ReadFile(a.usersFile)
	if err != nil {
		return false, err
	}
	if sha256.Sum256(now) != sum {
		return false, nil
	}
	return true, os.Rename(tmp, a.usersFile)
}

// lock takes users.sh's lock (flock on the same file; the lock works across
// containers, the kernel is the host's).
func (a *authelia) lock() (func(), error) {
	f, err := os.OpenFile(a.lockFile, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("users.sh's lock: %w", err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			return nil, errStatus(http.StatusServiceUnavailable, "Authelia's user file is locked (users.sh?); try again")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// --- YAML nodes (changing only what's asked keeps the rest of the file)

func usersNode(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	u := mapValue(doc.Content[0], "users")
	if u == nil || u.Kind != yaml.MappingNode {
		return nil
	}
	return u
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func setNode(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, scalar(key), v)
}

func setStr(m *yaml.Node, key, v string) { setNode(m, key, scalar(v)) }

func setBool(m *yaml.Node, key string, v bool) {
	setNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)})
}

// setGroup adds entry to group or takes it out, keeping its other groups.
func setGroup(entry *yaml.Node, group string, in bool) {
	g := mapValue(entry, "groups")
	if g == nil || g.Kind != yaml.SequenceNode {
		g = &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		setNode(entry, "groups", g)
	}
	kept := g.Content[:0]
	for _, n := range g.Content {
		if n.Value != group {
			kept = append(kept, n)
		}
	}
	g.Content = kept
	if in {
		g.Content = append(g.Content, scalar(group))
	}
}

func inAdmins(entry *yaml.Node) bool {
	g := mapValue(entry, "groups")
	if g == nil {
		return false
	}
	for _, n := range g.Content {
		if n.Value == "admins" {
			return true
		}
	}
	return false
}
