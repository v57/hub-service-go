package hubservice

// Hub key pinning (Docs/Devices/protocol.md section 2). A v1 auth token signs
// `hub1:<hub SPKI>:<expiry>`, so it only works on the hub it was made for. The
// SDK learns the hub key from `hub/info` on its first (v0) connection, pins it
// per hub address in `hubs.json` beside the key file, and connects with v1
// from then on. hub-lite doesn't answer `hub/info`, so it stays on v0.

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const hubKeysFile = "hubs.json"

// HubKeys is a `hubs.json` file of pinned hub keys: `{ "<address>": "<hub SPKI>" }`.
// An empty path keeps pins in memory only.
type HubKeys struct {
	path   string
	mu     sync.Mutex
	memory map[string]string
}

var defaultHubKeys sync.Map // path → *HubKeys

// OpenHubKeys returns the pin file at path. Every Service using the same path
// shares one instance, so concurrent pins don't overwrite each other.
func OpenHubKeys(path string) *HubKeys {
	if path == "" {
		return &HubKeys{}
	}
	value, _ := defaultHubKeys.LoadOrStore(path, &HubKeys{path: path})
	return value.(*HubKeys)
}

// DefaultHubKeysPath is `hubs.json` beside the keychain's key file.
func DefaultHubKeysPath() string {
	keychain.Lock()
	defer keychain.Unlock()
	return filepath.Join(filepath.Dir(keyPath()), hubKeysFile)
}

// HubKeysPathFor is `hubs.json` beside a key file.
func HubKeysPathFor(keyFile string) string {
	return filepath.Join(filepath.Dir(keyFile), hubKeysFile)
}

// Get returns the pinned key for a hub address, or "".
func (h *HubKeys) Get(address any) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	pins, _ := h.loadLocked()
	return pins[HubAddress(address)]
}

// Pin stores the key for a hub address.
func (h *HubKeys) Pin(address any, hubKey string) error {
	if _, err := parseSPKI(hubKey); err != nil {
		return fmt.Errorf("hub key: %w", err)
	}
	return h.update(func(pins map[string]string) { pins[HubAddress(address)] = hubKey })
}

// Forget clears the pin for a hub address; the next connect learns it again.
func (h *HubKeys) Forget(address any) error {
	return h.update(func(pins map[string]string) { delete(pins, HubAddress(address)) })
}

func (h *HubKeys) update(change func(map[string]string)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pins, err := h.loadLocked()
	if err != nil {
		return err
	}
	change(pins)
	if h.path == "" {
		h.memory = pins
		return nil
	}
	data, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(h.path, data, 0o600)
}

func (h *HubKeys) loadLocked() (map[string]string, error) {
	if h.path == "" {
		pins := map[string]string{}
		for address, key := range h.memory {
			pins[address] = key
		}
		return pins, nil
	}
	pins := map[string]string{}
	data, err := os.ReadFile(h.path)
	if errors.Is(err, os.ErrNotExist) {
		return pins, nil
	}
	if err != nil {
		return pins, err
	}
	if err := json.Unmarshal(data, &pins); err != nil {
		return map[string]string{}, fmt.Errorf("invalid %s: %w", h.path, err)
	}
	return pins, nil
}

// HubAddress is the form a hub address is pinned under: the WebSocket URL the
// client dials (`1997` and `ws://127.0.0.1:1997` are the same hub).
func HubAddress(address any) string {
	switch value := address.(type) {
	case int:
		return fmt.Sprintf("ws://127.0.0.1:%d", value)
	case string:
		if strings.HasPrefix(value, "ws://") || strings.HasPrefix(value, "wss://") {
			return value
		}
		if strings.Contains(value, ":") {
			return "ws://" + value
		}
		return "ws://127.0.0.1:" + value
	default:
		return fmt.Sprint(value)
	}
}

func parseSPKI(encoded string) (ed25519.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an ed25519 key")
	}
	return key, nil
}

// writeFileAtomic writes a temp file in the same directory and renames it over
// path, so readers never see a partial file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
