package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// As setup.sh writes it (yq), with a comment that must survive
const usersFixture = `users:
  # the server's admin
  admin:
    displayname: Admin
    email: admin@example.com
    password: $argon2id$v=19$m=65536,t=3,p=4$adminsalt$adminhash
    disabled: false
    groups:
      - admins
  alice:
    displayname: Alice
    email: alice@example.com
    password: $argon2id$v=19$m=65536,t=3,p=4$alicesalt$alicehash
    disabled: false
    groups: []
`

const configFixture = `identity_providers:
  oidc:
    authorization_policies:
      opencloud:
        default_policy: one_factor
        rules:
          - policy: two_factor
            subject:
              - group:admins
              - group:2fa
`

func testAuthelia(t *testing.T) *authelia {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "users"), 0o700); err != nil {
		t.Fatal(err)
	}
	users := filepath.Join(dir, "users", "users_database.yml")
	if err := os.WriteFile(users, []byte(usersFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	// The part of deploy/'s configuration that matters here
	config := filepath.Join(dir, "configuration.yml")
	if err := os.WriteFile(config, []byte(configFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newAuthelia(users, filepath.Join(dir, ".users.lock"), config)
	a.hash = func(_ context.Context, pw string) (string, error) { return "$argon2id$hash-of-" + pw, nil }
	a.cli = func(context.Context, ...string) (string, error) { return "", errors.New("no CLI in this test") }
	return a
}

func str(s string) *string { return &s }
func boolp(b bool) *bool   { return &b }

func readUsers(t *testing.T, a *authelia) map[string]fileUser {
	t.Helper()
	u, err := a.read()
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPutCreatesAndKeepsTheRest(t *testing.T) {
	a := testAuthelia(t)
	// A display name YAML would read as a bool must stay a string
	created, err := a.put(context.Background(), "bob", userChange{DisplayName: str("true"), Email: str("bob@example.com"), Password: str("pw1")})
	if err != nil || !created {
		t.Fatalf("put: %v, created %v", err, created)
	}
	u := readUsers(t, a)
	bob := u["bob"]
	if bob.DisplayName != "true" || bob.Email != "bob@example.com" || *bob.Password != "$argon2id$hash-of-pw1" || bob.Disabled || len(bob.Groups) != 0 {
		t.Errorf("bob: %+v", bob)
	}
	if !u["admin"].admin() || *u["alice"].Password != "$argon2id$v=19$m=65536,t=3,p=4$alicesalt$alicehash" {
		t.Errorf("the other entries changed: %+v", u)
	}
	b, _ := os.ReadFile(a.usersFile)
	if !strings.Contains(string(b), "# the server's admin") {
		t.Errorf("the comment is gone:\n%s", b)
	}
	if st, _ := os.Stat(a.usersFile); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", st.Mode().Perm())
	}
	left, _ := filepath.Glob(a.usersFile + ".new.*")
	if len(left) != 0 {
		t.Errorf("left over: %v", left)
	}
}

func TestPutChangesOnlyTheGivenFields(t *testing.T) {
	a := testAuthelia(t)
	created, err := a.put(context.Background(), "alice", userChange{Disabled: boolp(true), Password: str("new-pw")})
	if err != nil || created {
		t.Fatalf("put: %v, created %v", err, created)
	}
	alice := readUsers(t, a)["alice"]
	if !alice.Disabled || *alice.Password != "$argon2id$hash-of-new-pw" || alice.DisplayName != "Alice" || alice.Email != "alice@example.com" {
		t.Errorf("alice: %+v", alice)
	}
}

func TestPutNewNeedsNameAndPassword(t *testing.T) {
	a := testAuthelia(t)
	_, err := a.put(context.Background(), "carol", userChange{DisplayName: str("Carol")})
	var he *httpError
	if !errors.As(err, &he) || he.status != 400 {
		t.Fatalf("want 400, got %v", err)
	}
	if _, ok := readUsers(t, a)["carol"]; ok {
		t.Error("carol was added")
	}
}

func TestAdminsAreRefused(t *testing.T) {
	a := testAuthelia(t)
	before, _ := os.ReadFile(a.usersFile)
	if _, err := a.put(context.Background(), "admin", userChange{Password: str("taken-over")}); err != errAdmin {
		t.Errorf("put admin: %v", err)
	}
	if err := a.remove("admin"); err != errAdmin {
		t.Errorf("remove admin: %v", err)
	}
	if after, _ := os.ReadFile(a.usersFile); string(after) != string(before) {
		t.Error("the file changed")
	}
	if admin, err := a.isAdmin("admin"); !admin || err != nil {
		t.Errorf("isAdmin(admin) = %v, %v", admin, err)
	}
	if admin, _ := a.isAdmin("alice"); admin {
		t.Error("alice is no admin")
	}
}

func TestRemove(t *testing.T) {
	a := testAuthelia(t)
	if err := a.remove("alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := readUsers(t, a)["alice"]; ok {
		t.Error("alice is still there")
	}
	var he *httpError
	if err := a.remove("alice"); !errors.As(err, &he) || he.status != 404 {
		t.Errorf("second remove: %v", err)
	}
}

func TestConcurrentPutsAllLand(t *testing.T) {
	a := testAuthelia(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := a.put(context.Background(), fmt.Sprintf("user%d", i), userChange{DisplayName: str("U"), Password: str("pw1")})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := len(readUsers(t, a)); n != 22 {
		t.Errorf("%d entries, want 22", n)
	}
}

func TestWaitsForUsersShLock(t *testing.T) {
	a := testAuthelia(t)
	f, err := os.OpenFile(a.lockFile, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// What users.sh's flock does
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}()
	start := time.Now()
	if _, err := a.put(context.Background(), "dave", userChange{DisplayName: str("Dave"), Password: str("pw1")}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Error("didn't wait for the lock")
	}
}

func TestReplaceNoticesAChangedFile(t *testing.T) {
	a := testAuthelia(t)
	before, _ := os.ReadFile(a.usersFile)
	// What was read differs from the file now (Authelia rewrote it)
	done, err := a.replace([]byte("users: {}\n"), sha256.Sum256([]byte("something else")))
	if done || err != nil {
		t.Fatalf("done %v, err %v", done, err)
	}
	if after, _ := os.ReadFile(a.usersFile); string(after) != string(before) {
		t.Error("the file was replaced")
	}
	if left, _ := filepath.Glob(a.usersFile + ".new.*"); len(left) != 0 {
		t.Errorf("left over: %v", left)
	}
}

func TestValidUsers(t *testing.T) {
	for in, ok := range map[string]bool{
		"users: {}\n": true,
		"users:\n  a:\n    displayname: A\n    password: x\n": true,
		"users:\n  a:\n    displayname: A\n":                  false,
		"users:\n  a:\n    password: x\n":                     false,
		"other: 1\n":                                          false,
	} {
		if err := validUsers([]byte(in)); (err == nil) != ok {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestReset2FA(t *testing.T) {
	a := testAuthelia(t)
	var calls []string
	a.cli = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[2] == "totp" {
			return "Error: no TOTP configuration for user 'alice'", errors.New("exit 1")
		}
		return "Successfully deleted", nil
	}
	if err := a.reset2FA(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	want := []string{"storage user totp delete -- alice", "storage user webauthn delete --all -- alice"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls %q", calls)
	}
	a.cli = func(context.Context, ...string) (string, error) {
		return "Error: database is locked", errors.New("exit 1")
	}
	if err := a.reset2FA(context.Background(), "alice"); err == nil || !strings.Contains(err.Error(), "database is locked") {
		t.Errorf("want Authelia's error, got %v", err)
	}
}

func TestUserChangeCheck(t *testing.T) {
	for _, c := range []struct {
		c  userChange
		ok bool
	}{
		{userChange{Password: str("abc")}, true},
		{userChange{Password: str("ab")}, false},
		{userChange{DisplayName: str(" ")}, false},
		{userChange{DisplayName: str("A\nB")}, false},
		{userChange{Email: str("no-at")}, false},
		{userChange{Email: str("")}, true}, // clears it
		{userChange{Disabled: boolp(false)}, true},
		{userChange{}, false},
	} {
		if err := c.c.check(); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.c, err)
		}
	}
}

func TestValidName(t *testing.T) {
	for n, ok := range map[string]bool{"alice": true, "a.b@c-d_e": true, ".": false, "..": false, ".x": false, "a/b": false, "": false, "a b": false} {
		if err := validName(n); (err == nil) != ok {
			t.Errorf("%q: %v", n, err)
		}
	}
}

// The encoder keeps a !!str that looks like a number or a bool quoted
func TestScalarQuoting(t *testing.T) {
	doc := &yaml.Node{Kind: yaml.MappingNode}
	setStr(doc, "a", "123")
	setStr(doc, "b", "yes")
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back["a"] != "123" || back["b"] != "yes" {
		t.Errorf("%s -> %#v", out, back)
	}
}

func TestTwoFactorGroup(t *testing.T) {
	a := testAuthelia(t)
	if _, err := a.put(context.Background(), "alice", userChange{TwoFactor: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	if g := readUsers(t, a)["alice"].Groups; len(g) != 1 || g[0] != "2fa" {
		t.Errorf("groups %v", g)
	}
	list, _ := a.list()
	for _, u := range list {
		if u.TwoFactor != (u.Name == "alice") {
			t.Errorf("list: %+v", u)
		}
	}
	// Twice on is still once; off keeps the other groups
	if _, err := a.put(context.Background(), "alice", userChange{TwoFactor: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.put(context.Background(), "alice", userChange{TwoFactor: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	if g := readUsers(t, a)["alice"].Groups; len(g) != 0 {
		t.Errorf("after off: %v", g)
	}
	// A new entry with 2FA from the start
	if _, err := a.put(context.Background(), "erin", userChange{DisplayName: str("Erin"), Password: str("pw1"), TwoFactor: boolp(true)}); err != nil {
		t.Fatal(err)
	}
	if !readUsers(t, a)["erin"].in("2fa") {
		t.Error("erin isn't in 2fa")
	}
}

func TestSetGroupKeepsOthers(t *testing.T) {
	var entry yaml.Node
	if err := yaml.Unmarshal([]byte("groups: [staff, 2fa, other]\n"), &entry); err != nil {
		t.Fatal(err)
	}
	m := entry.Content[0]
	setGroup(m, "2fa", false)
	setGroup(m, "x", true)
	var got []string
	for _, n := range mapValue(m, "groups").Content {
		got = append(got, n.Value)
	}
	if strings.Join(got, ",") != "staff,other,x" {
		t.Errorf("groups %v", got)
	}
}

func TestTwoFactorNeedsTheRule(t *testing.T) {
	a := testAuthelia(t)
	// A configuration from before the 2fa group
	if err := os.WriteFile(a.config, []byte(strings.ReplaceAll(configFixture, "              - group:2fa\n", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(a.usersFile)
	_, err := a.put(context.Background(), "alice", userChange{TwoFactor: boolp(true)})
	var he *httpError
	if !errors.As(err, &he) || he.status != 409 {
		t.Fatalf("want 409, got %v", err)
	}
	if after, _ := os.ReadFile(a.usersFile); string(after) != string(before) {
		t.Error("the file changed")
	}
	// Turning it off needs no rule
	if _, err := a.put(context.Background(), "alice", userChange{TwoFactor: boolp(false)}); err != nil {
		t.Error(err)
	}
	// A commented-out rule doesn't count
	_ = os.WriteFile(a.config, []byte("# subject: group:2fa\n"), 0o600)
	if ok, _ := a.twoFactorConfigured(); ok {
		t.Error("a comment counted as the rule")
	}
}
