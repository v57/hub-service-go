package hubservice

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func useKeyPath(t *testing.T, path string) {
	t.Helper()
	SetKeyPath(path)
	t.Cleanup(func() { SetKeyPath("") })
}

func TestKeychainGeneratesKeyFileInTSLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth")
	useKeyPath(t, path)
	publicKey, err := PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes) != 92 {
		t.Fatalf("key file: expected 92 bytes, got %d", len(bytes))
	}
	if got := base64.StdEncoding.EncodeToString(bytes[48:]); got != publicKey {
		t.Fatalf("public key %q is not the file's SPKI part %q", publicKey, got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v", info.Mode().Perm())
	}

	// Reloading from disk gives the same identity.
	SetKeyPath(path)
	again, err := PublicKey()
	if err != nil || again != publicKey {
		t.Fatalf("reload: %q, %v", again, err)
	}
}

func TestSignTokenVerifies(t *testing.T) {
	useKeyPath(t, filepath.Join(t.TempDir(), "auth"))
	token, err := Sign()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "key" {
		t.Fatalf("token shape: %q", token)
	}
	publicDer, _ := base64.StdEncoding.DecodeString(parts[1])
	parsed, err := x509.ParsePKIXPublicKey(publicDer)
	if err != nil {
		t.Fatal(err)
	}
	signature, _ := base64.StdEncoding.DecodeString(parts[2])
	if !ed25519.Verify(parsed.(ed25519.PublicKey), []byte(parts[3]), signature) {
		t.Fatal("signature does not verify")
	}
	expiry, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(time.Unix(expiry, 0)); left < 8*time.Second || left > 11*time.Second {
		t.Fatalf("expiry %v from now, want about 10s", left)
	}
}

func TestKeychainReadsHUBKEY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service-key")
	t.Setenv("HUBKEY", path)
	useKeyPath(t, "")
	if _, err := PublicKey(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("key not written to $HUBKEY: %v", err)
	}
}

func TestKeychainRejectsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth")
	if err := os.WriteFile(path, []byte("bm90IGEga2V5"), 0o600); err != nil {
		t.Fatal(err)
	}
	useKeyPath(t, path)
	if _, err := Sign(); err == nil {
		t.Fatal("expected an error for a malformed key file")
	}
}

func TestAuthHeadersUseSignOption(t *testing.T) {
	service := New(ServiceOptions{Sign: func() (string, error) { return "key.a.b.1", nil }})
	headers := service.authHeaders()
	if headers["auth"] != "key.a.b.1" || headers["v"] != "0" {
		t.Fatalf("headers: %v", headers)
	}
}
