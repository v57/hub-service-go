package hubservice

// Ed25519 keychain for hub-server auth. Wire parity with hub-service/keychain.ts:
//
//   - The key file (`auth` in the working directory, or $HUBKEY) holds the
//     base64 of the 48-byte PKCS8 DER private key followed by the 44-byte SPKI
//     DER public key.
//   - A token is `key.<pub>.<sig>.<expiry>.<nonce>`: pub is the base64 SPKI
//     public key, expiry the decimal Unix time in seconds, nonce the challenge
//     the hub answered in hub/info, sig the base64 Ed25519 signature of
//     `hub1:<hub SPKI>:<nonce>:<expiry>` (one connection to one hub;
//     Docs/Devices/protocol.md section 2).

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	privateDerLength = 48
	publicDerLength  = 44
	// authVersion is sent as the `v` header next to `auth`.
	authVersion = "1"
	// signExpiry is the protocol's SDK default; a token is for one connect, and
	// hubs accept at most 60 s.
	signExpiry = 30 * time.Second
)

type keychainKey struct {
	private   ed25519.PrivateKey
	publicDer []byte
}

var keychain struct {
	sync.Mutex
	path string
	key  *keychainKey
}

// SetKeyPath overrides the key file location (default: $HUBKEY, else `auth`
// in the working directory).
func SetKeyPath(path string) {
	keychain.Lock()
	defer keychain.Unlock()
	keychain.path = path
	keychain.key = nil
}

// PublicKey returns the service's public key: base64 of the SPKI DER public
// key. Hub Server uses it as the connection identity.
func PublicKey() (string, error) {
	key, err := loadKey()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key.publicDer), nil
}

// SignHub returns a fresh auth token for one connection to the hub with key
// hubKey, over the challenge it answered in `hub/info`.
func SignHub(hubKey, nonce string) (string, error) {
	key, err := loadKey()
	if err != nil {
		return "", err
	}
	return SignToken(key.private, hubKey, nonce, time.Now().Add(signExpiry))
}

// SignToken signs an auth token with a service key: `hub1:<hubKey>:<nonce>:<expiry>`.
func SignToken(private ed25519.PrivateKey, hubKey, nonce string, expiry time.Time) (string, error) {
	publicDer, err := x509.MarshalPKIXPublicKey(private.Public())
	if err != nil {
		return "", err
	}
	data := strconv.FormatInt(expiry.Unix(), 10)
	signature := ed25519.Sign(private, []byte("hub1:"+hubKey+":"+nonce+":"+data))
	return "key." + base64.StdEncoding.EncodeToString(publicDer) + "." +
		base64.StdEncoding.EncodeToString(signature) + "." + data + "." + nonce, nil
}

// KeyFromFile parses key file text (keychain layout) into a private key.
func KeyFromFile(text string) (ed25519.PrivateKey, error) {
	key, err := parseKey(strings.TrimSpace(text))
	if err != nil {
		return nil, err
	}
	return key.private, nil
}

func keychainPrivateKey() (ed25519.PrivateKey, error) {
	key, err := loadKey()
	if err != nil {
		return nil, err
	}
	return key.private, nil
}

func keyPath() string {
	if keychain.path != "" {
		return keychain.path
	}
	if env := os.Getenv("HUBKEY"); env != "" {
		return env
	}
	return "auth"
}

func loadKey() (*keychainKey, error) {
	keychain.Lock()
	defer keychain.Unlock()
	if keychain.key != nil {
		return keychain.key, nil
	}
	path := keyPath()
	text, err := os.ReadFile(path)
	var encoded string
	switch {
	case err == nil:
		encoded = strings.TrimSpace(string(text))
	case errors.Is(err, os.ErrNotExist):
		encoded, err = generateKey()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	key, err := parseKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid key file %q: %w", path, err)
	}
	keychain.key = key
	return key, nil
}

func generateKey() (string, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	privateDer, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return "", err
	}
	publicDer, err := x509.MarshalPKIXPublicKey(private.Public())
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(privateDer, publicDer...)), nil
}

func parseKey(encoded string) (*keychainKey, error) {
	bytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(bytes) != privateDerLength+publicDerLength {
		return nil, fmt.Errorf("expected %d bytes, got %d", privateDerLength+publicDerLength, len(bytes))
	}
	parsed, err := x509.ParsePKCS8PrivateKey(bytes[:privateDerLength])
	if err != nil {
		return nil, err
	}
	private, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an ed25519 key")
	}
	return &keychainKey{private: private, publicDer: bytes[privateDerLength:]}, nil
}
