package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

var digestRe = regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=4\$([A-Za-z0-9+/]{22})\$([A-Za-z0-9+/]{43})$`)

func TestHashPassword(t *testing.T) {
	d, err := hashPassword(context.Background(), "pw1 ő")
	if err != nil {
		t.Fatal(err)
	}
	m := digestRe.FindStringSubmatch(d)
	if m == nil {
		t.Fatalf("digest %q", d)
	}
	salt, _ := base64.RawStdEncoding.DecodeString(m[1])
	key, _ := base64.RawStdEncoding.DecodeString(m[2])
	if !bytes.Equal(argon2.IDKey([]byte("pw1 ő"), salt, 3, 65536, 4, 32), key) {
		t.Error("the key isn't the password's")
	}
	if d2, _ := hashPassword(context.Background(), "pw1 ő"); d2 == d {
		t.Error("the same salt twice")
	}
}

// Authelia itself accepts the digest (the image's build stage has its CLI).
func TestAutheliaAcceptsTheHash(t *testing.T) {
	if _, err := exec.LookPath("authelia"); err != nil {
		t.Skip("authelia isn't on the PATH")
	}
	d, err := hashPassword(context.Background(), "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	for pw, want := range map[string]string{"correct horse": "The password matches the digest", "wrong": "does not match"} {
		out, _ := exec.Command("authelia", "crypto", "hash", "validate", "--password", pw, "--", d).CombinedOutput()
		if !strings.Contains(string(out), want) {
			t.Errorf("%q: %s", pw, out)
		}
	}
}

func TestPrintHashInput(t *testing.T) {
	for in, ok := range map[string]bool{"pw": true, "pw\n": true, "pw\r\n": true, "": false, "\n": false,
		strings.Repeat("x", 1024): true, strings.Repeat("x", 1025): false} {
		if err := printHash(strings.NewReader(in)); (err == nil) != ok {
			t.Errorf("%d bytes: %v", len(in), err)
		}
	}
}
