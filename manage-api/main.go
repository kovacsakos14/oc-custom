// manage-api: a small HTTP API for a management tool, so the tool needs
// neither SSH to the server nor OpenCloud's impersonation. It runs in its own
// container next to OpenCloud (deploy/compose.manage.yaml), in OpenCloud's
// network namespace, and does fixed actions only:
//   - Authelia's users (when the server uses Authelia): list, create or
//     change, delete, remove the 2FA devices; Authelia's user file is changed
//     exactly as users.sh does it, under the same lock;
//   - for one OpenCloud user: read or set "Disable Email Notifications", and
//     create a User's personal space. For each it makes an app token for that
//     user with OpenCloud's own CLI, valid for a minute, and revokes it after.
//
// It never acts on admins (Authelia's admins group or OpenCloud's Admin
// role), so its token can't take the server over.
//
//	manage-api [serve]   the API (settings from the environment, see config)
//	manage-api new-token prints a new token and the SHA-256 to store
//	manage-api hash-password  prints Authelia's digest of the password on
//	                     stdin (deploy's users.sh: never on a command line)
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// config is read from the environment; deploy/compose.manage*.yaml set it.
type config struct {
	Addr      string // where the API listens
	TokenHash string // file with the hex SHA-256 of the API's token
	// OpenCloud's own address inside its network namespace
	OpenCloudURL      string
	OpenCloudInsecure bool // a self-signed certificate on OpenCloudURL (tests)
	// Authelia's user file; empty: no Authelia on this server
	AutheliaUsers  string
	AutheliaLock   string // users.sh's lock file
	AutheliaConfig string // for Authelia's CLI (2FA devices)
	// The reverse proxies whose X-Real-IP names the client (SWAG); from
	// anyone else the header is ignored
	Proxies *trustedProxies
	// Exit when OpenCloud hasn't answered for this long (0: never): after
	// OpenCloud's container restarts, this one is in a namespace without it,
	// and its restart policy brings it back into the new one
	Watchdog time.Duration
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func loadConfig() (config, error) {
	c := config{
		Addr:           env("MANAGE_API_ADDR", "0.0.0.0:9300"),
		TokenHash:      env("MANAGE_API_TOKEN_HASH_FILE", "/etc/manage-api/token.sha256"),
		OpenCloudURL:   env("MANAGE_API_OPENCLOUD_URL", "http://127.0.0.1:9200"),
		AutheliaUsers:  env("MANAGE_API_AUTHELIA_USERS", ""),
		AutheliaLock:   env("MANAGE_API_AUTHELIA_LOCK", "/authelia-lock/.users.lock"),
		AutheliaConfig: env("MANAGE_API_AUTHELIA_CONFIG", "/config/configuration.yml"),
	}
	var err error
	if c.OpenCloudInsecure, err = strconv.ParseBool(env("MANAGE_API_OPENCLOUD_INSECURE", "false")); err != nil {
		return c, fmt.Errorf("MANAGE_API_OPENCLOUD_INSECURE: %w", err)
	}
	if c.Proxies, err = parseTrustedProxies(env("MANAGE_API_TRUSTED_PROXIES", "")); err != nil {
		return c, fmt.Errorf("MANAGE_API_TRUSTED_PROXIES: %w", err)
	}
	if c.Watchdog, err = time.ParseDuration(env("MANAGE_API_WATCHDOG", "90s")); err != nil {
		return c, fmt.Errorf("MANAGE_API_WATCHDOG: %w", err)
	}
	return c, nil
}

// newToken returns a token and the hex SHA-256 the API stores instead.
func newToken() (token, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on Linux
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:])
}

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "new-token":
		token, hash := newToken()
		fmt.Printf("token: %s\nsha256: %s\n", token, hash)
	case "hash-password":
		if err := printHash(os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "serve":
		log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
		c, err := loadConfig()
		if err != nil {
			log.Error("settings", "error", err)
			os.Exit(2)
		}
		if err := serve(c, log); err != nil {
			log.Error("stopped", "error", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "Usage: manage-api [serve | new-token | hash-password]")
		os.Exit(2)
	}
}

// printHash prints the digest of the password read from r: all of it, less
// one line end; at most 1024 bytes, as the API takes.
func printHash(r io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(r, 1027))
	if err != nil {
		return err
	}
	pw := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	switch {
	case pw == "":
		return errors.New("no password on stdin")
	case len(pw) > 1024:
		return errors.New("the password is longer than 1024 bytes")
	}
	d, err := hashPassword(context.Background(), pw)
	if err != nil {
		return err
	}
	fmt.Println(d)
	return nil
}
