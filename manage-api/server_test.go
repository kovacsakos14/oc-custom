package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const roleClient = "38f0649f-b600-4188-b138-fbd2c773cc43"

// fakeOpenCloud answers what the API asks OpenCloud, for the users in roles
// (name -> role id), and records tokens: each must be revoked once used.
type fakeOpenCloud struct {
	mu      sync.Mutex
	roles   map[string]string
	tokens  map[string]string // token -> user, while valid
	made    int
	emails  map[string]settingValue
	drives  map[string]string // user -> personal space id, once asked for
	revoked int
}

func (f *fakeOpenCloud) newToken(_ context.Context, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.roles[user]; !ok {
		return "", errNoUser
	}
	f.made++
	t := fmt.Sprintf("tok%d", f.made)
	f.tokens[t] = user
	return t, nil
}

func (f *fakeOpenCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/status.php" {
		fmt.Fprint(w, `{"productversion":"7.2.4+custom.9"}`)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	user, tok, ok := r.BasicAuth()
	if !ok || f.tokens[tok] != user {
		http.Error(w, "unauthorized", 401)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/graph/v1.0/me":
		fmt.Fprintf(w, `{"appRoleAssignments":[{"appRoleId":%q}]}`, f.roles[user])
	case r.Method == "DELETE" && r.URL.Path == "/auth-app/tokens":
		if r.URL.Query().Get("token") != tok {
			http.Error(w, "only its own token in this fake", 400)
			return
		}
		delete(f.tokens, tok)
		f.revoked++
	case r.URL.Path == "/api/v0/settings/values-get-by-unique-identifiers":
		v, ok := f.emails[user]
		if !ok {
			http.Error(w, `{"code":"not found"}`, 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"value": map[string]any{"value": v}})
	case r.URL.Path == "/api/v0/settings/values-save":
		var body struct {
			Value settingValue `json:"value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		v := body.Value
		if old, ok := f.emails[user]; ok && v.ID != old.ID {
			http.Error(w, "a second value", 400)
			return
		}
		if v.ID == "" {
			v.ID = "value-" + user
		}
		f.emails[user] = v
		_ = json.NewEncoder(w).Encode(map[string]any{"value": map[string]any{"value": v}})
	case r.Method == "GET" && r.URL.Path == "/graph/v1.0/me/drives":
		f.drives[user] = "drive-" + user
		fmt.Fprintf(w, `{"value":[{"id":"virtual","driveType":"virtual"},{"id":%q,"driveType":"personal"}]}`, f.drives[user])
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 500)
	}
}

const testToken = "the-api-token"

type testServer struct {
	*httptest.Server
	oc       *fakeOpenCloud
	authelia *authelia
}

func newTestServer(t *testing.T, withAuthelia bool) *testServer {
	t.Helper()
	f := &fakeOpenCloud{
		roles:  map[string]string{"admin": roleAdmin, "alice": roleUser, "balabit": roleClient, "boss": roleAdmin},
		tokens: map[string]string{}, emails: map[string]settingValue{}, drives: map[string]string{},
	}
	ocSrv := httptest.NewServer(f)
	t.Cleanup(ocSrv.Close)
	oc := newOpenCloud(ocSrv.URL, false)
	oc.newToken = f.newToken
	hashFile := filepath.Join(t.TempDir(), "token.sha256")
	_, _ = newToken() // the real generator runs
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(testToken)))
	if err := os.WriteFile(hashFile, []byte(sum+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &api{tokenHashFile: hashFile, oc: oc, log: slog.New(slog.NewTextHandler(io.Discard, nil)), failures: newLimiter(10, 10*time.Minute)}
	if withAuthelia {
		a.authelia = testAuthelia(t)
	}
	s := httptest.NewServer(a.routes())
	t.Cleanup(func() {
		s.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.tokens) != 0 {
			t.Errorf("tokens not revoked: %v", f.tokens)
		}
	})
	return &testServer{Server: s, oc: f, authelia: a.authelia}
}

// do sends a request with the API token (token "" for none) and decodes
// the JSON answer into a map.
func (s *testServer) do(t *testing.T, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func TestTokenIsRequired(t *testing.T) {
	s := newTestServer(t, false)
	if code, _ := s.do(t, "GET", "/v1/info", "", ""); code != 401 {
		t.Errorf("no token: %d", code)
	}
	if code, _ := s.do(t, "GET", "/v1/info", "wrong", ""); code != 401 {
		t.Errorf("wrong token: %d", code)
	}
	code, m := s.do(t, "GET", "/v1/info", testToken, "")
	if code != 200 || m["opencloud"] != "7.2.4+custom.9" || m["authelia"] != false {
		t.Errorf("info: %d %v", code, m)
	}
}

func TestFailedAttemptsAreLimited(t *testing.T) {
	s := newTestServer(t, false)
	for range 10 {
		s.do(t, "GET", "/v1/info", "wrong", "")
	}
	// Blocked now, even with the right token
	if code, _ := s.do(t, "GET", "/v1/info", testToken, ""); code != 429 {
		t.Errorf("after 10 failures: %d", code)
	}
}

func TestLimiterForgets(t *testing.T) {
	l := newLimiter(2, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	l.fail("x")
	l.fail("x")
	if !l.blocked("x") || l.blocked("y") {
		t.Fatal("x should be blocked, y not")
	}
	now = now.Add(2 * time.Minute)
	if l.blocked("x") {
		t.Error("still blocked after the window")
	}
}

func TestNoTokenSetUp(t *testing.T) {
	a := &api{tokenHashFile: "/nonexistent", log: slog.New(slog.NewTextHandler(io.Discard, nil)), failures: newLimiter(10, time.Minute)}
	s2 := httptest.NewServer(a.routes())
	defer s2.Close()
	resp, err := http.Get(s2.URL + "/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("without a token file: %d", resp.StatusCode)
	}
}

func TestMuteAndUnmute(t *testing.T) {
	s := newTestServer(t, false)
	code, m := s.do(t, "GET", "/v1/opencloud/users/balabit/emails", testToken, "")
	if code != 200 || m["disabled"] != false || m["value_id"] != "" {
		t.Errorf("never set: %d %v", code, m)
	}
	code, m = s.do(t, "PUT", "/v1/opencloud/users/balabit/emails", testToken, `{"disabled":true}`)
	if code != 200 || m["disabled"] != true || m["value_id"] != "value-balabit" {
		t.Errorf("mute: %d %v", code, m)
	}
	// Saved over the stored value, not a second one (the fake refuses that)
	code, m = s.do(t, "PUT", "/v1/opencloud/users/balabit/emails", testToken, `{"disabled":false}`)
	if code != 200 || m["disabled"] != false {
		t.Errorf("unmute: %d %v", code, m)
	}
	if s.oc.revoked != 3 {
		t.Errorf("%d tokens revoked, want 3", s.oc.revoked)
	}
}

func TestEmailsBody(t *testing.T) {
	s := newTestServer(t, false)
	for body, want := range map[string]int{`{}`: 400, `{"disabled":true,"x":1}`: 400, `not json`: 400,
		`{"disabled":true} {}`: 400, `{"disabled":"` + strings.Repeat("x", maxBody) + `"}`: 413} {
		if code, m := s.do(t, "PUT", "/v1/opencloud/users/balabit/emails", testToken, body); code != want {
			t.Errorf("%.40s: %d %v", body, code, m)
		}
	}
}

func TestAdminsAreLeftAlone(t *testing.T) {
	s := newTestServer(t, true)
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/v1/opencloud/users/admin/emails", `{"disabled":true}`},
		{"GET", "/v1/opencloud/users/admin/emails", ""},
		{"POST", "/v1/opencloud/users/admin/personal-space", ""},
		// In Authelia's admins group
		{"PUT", "/v1/authelia/users/admin", `{"password":"taken-over"}`},
		{"DELETE", "/v1/authelia/users/admin", ""},
		{"POST", "/v1/authelia/users/admin/reset-2fa", ""},
		// OpenCloud's Admin without an entry in admins: still refused
		{"PUT", "/v1/authelia/users/boss", `{"displayname":"Boss","password":"pw1"}`},
	} {
		if code, m := s.do(t, c.method, c.path, testToken, c.body); code != 403 {
			t.Errorf("%s %s: %d %v", c.method, c.path, code, m)
		}
	}
	if _, ok := readUsers(t, s.authelia)["boss"]; ok {
		t.Error("boss was added")
	}
}

func TestPersonalSpace(t *testing.T) {
	s := newTestServer(t, false)
	if code, m := s.do(t, "POST", "/v1/opencloud/users/alice/personal-space", testToken, ""); code != 200 || m["id"] != "drive-alice" {
		t.Errorf("alice: %d %v", code, m)
	}
	if code, _ := s.do(t, "POST", "/v1/opencloud/users/balabit/personal-space", testToken, ""); code != 409 {
		t.Errorf("a Client: %d", code)
	}
	if s.oc.drives["balabit"] != "" {
		t.Error("the Client's drives were asked for")
	}
	if code, _ := s.do(t, "POST", "/v1/opencloud/users/nobody/personal-space", testToken, ""); code != 404 {
		t.Errorf("unknown user: %d", code)
	}
}

func TestAutheliaEndpoints(t *testing.T) {
	s := newTestServer(t, true)
	// A Client in OpenCloud, new in Authelia
	code, m := s.do(t, "PUT", "/v1/authelia/users/balabit", testToken, `{"displayname":"Balabit","email":"b@example.com","password":"pw1"}`)
	if code != 200 || m["created"] != true {
		t.Fatalf("create: %d %v", code, m)
	}
	code, m = s.do(t, "PUT", "/v1/authelia/users/balabit", testToken, `{"disabled":true}`)
	if code != 200 || m["created"] != false {
		t.Errorf("disable: %d %v", code, m)
	}
	// Not in OpenCloud (yet): no role there, so not an admin
	if code, m := s.do(t, "PUT", "/v1/authelia/users/newbie", testToken, `{"displayname":"N","password":"pw1"}`); code != 200 {
		t.Errorf("unknown to OpenCloud: %d %v", code, m)
	}
	req, _ := http.NewRequest("GET", s.URL+"/v1/authelia/users", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var list []autheliaUser
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	got := map[string]autheliaUser{}
	for _, u := range list {
		got[u.Name] = u
	}
	if !got["admin"].Admin || !got["balabit"].Disabled || got["balabit"].Email != "b@example.com" || len(list) != 4 {
		t.Errorf("list: %+v", list)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "argon2") {
		t.Error("the list shows hashes")
	}
	if code, _ := s.do(t, "DELETE", "/v1/authelia/users/balabit", testToken, ""); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := s.do(t, "PUT", "/v1/authelia/users/.hidden", testToken, `{"disabled":true}`); code != 400 {
		t.Errorf("name .hidden: %d", code)
	}
}

func TestWithoutAuthelia(t *testing.T) {
	s := newTestServer(t, false)
	if code, _ := s.do(t, "GET", "/v1/authelia/users", testToken, ""); code != 404 {
		t.Errorf("list: %d", code)
	}
	if code, _ := s.do(t, "PUT", "/v1/authelia/users/alice", testToken, `{"disabled":true}`); code != 404 {
		t.Errorf("put: %d", code)
	}
	if code, _ := s.do(t, "GET", "/v1/nothing", testToken, ""); code != 404 {
		t.Errorf("unknown path: %d", code)
	}
}
