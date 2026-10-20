package hubservice

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v57/channel-go/channelgo"
	"github.com/v57/channel-go/server"
)

// vectors are Docs/Devices/vectors.json, copied into testdata.
type vectors struct {
	Keys map[string]struct {
		KeyFile    string `json:"keyFile"`
		PublicKey  string `json:"publicKey"`
		DeviceID   string `json:"deviceId"`
		SafetyCode string `json:"safetyCode"`
	} `json:"keys"`
	Auth struct {
		Expiry int64 `json:"expiry"`
		V0     struct {
			Header string `json:"header"`
		} `json:"v0"`
		V1 struct {
			Header string `json:"header"`
		} `json:"v1"`
	} `json:"auth"`
	Certificate      certificateVector `json:"certificate"`
	ChildCertificate certificateVector `json:"childCertificate"`
	Caller           struct {
		Context map[string]any `json:"context"`
	} `json:"caller"`
}

type certificateVector struct {
	JSON   string `json:"json"`
	Header string `json:"header"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/device-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAuthTokensMatchVectors(t *testing.T) {
	v := loadVectors(t)
	private, err := KeyFromFile(v.Keys["service"].KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Unix(v.Auth.Expiry, 0)
	v0, err := SignToken(private, "", expiry)
	if err != nil || v0 != v.Auth.V0.Header {
		t.Fatalf("v0 token:\n got %s\nwant %s (%v)", v0, v.Auth.V0.Header, err)
	}
	v1, err := SignToken(private, v.Keys["hub"].PublicKey, expiry)
	if err != nil || v1 != v.Auth.V1.Header {
		t.Fatalf("v1 token:\n got %s\nwant %s (%v)", v1, v.Auth.V1.Header, err)
	}
}

func TestHubKeysPinPerAddress(t *testing.T) {
	v := loadVectors(t)
	path := filepath.Join(t.TempDir(), "hubs.json")
	pins := OpenHubKeys(path)
	if err := pins.Pin(1997, v.Keys["hub"].PublicKey); err != nil {
		t.Fatal(err)
	}
	if got := OpenHubKeys(path).Get("ws://127.0.0.1:1997"); got != v.Keys["hub"].PublicKey {
		t.Fatalf("pin by URL = %q", got)
	}
	if got := pins.Get("127.0.0.1:1998"); got != "" {
		t.Fatalf("other address pinned: %q", got)
	}
	data, _ := os.ReadFile(path)
	var stored map[string]string
	if err := json.Unmarshal(data, &stored); err != nil || stored["ws://127.0.0.1:1997"] != v.Keys["hub"].PublicKey {
		t.Fatalf("hubs.json = %s (%v)", data, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("hubs.json mode %v", info.Mode().Perm())
	}
	if err := pins.Pin(1997, "not a key"); err == nil {
		t.Fatal("pinned an invalid key")
	}
	if err := pins.Forget(1997); err != nil || pins.Get(1997) != "" {
		t.Fatalf("forget: %v", err)
	}
}

func TestDefaultHubKeysBesideKeyFile(t *testing.T) {
	dir := t.TempDir()
	useKeyPath(t, filepath.Join(dir, "auth"))
	if got := DefaultHubKeysPath(); got != filepath.Join(dir, "hubs.json") {
		t.Fatalf("hubs.json path = %q", got)
	}
}

// infoHub is a test hub that answers hub/info (unless key is empty) and
// records the headers of every connection.
type infoHub struct {
	mu      sync.Mutex
	headers []map[string]string
	port    int
	updates chan map[string]string
}

func newInfoHub(t *testing.T, key string) *infoHub {
	t.Helper()
	h := &infoHub{updates: make(chan map[string]string, 16)}
	ch := channelgo.NewChannel()
	if key != "" {
		ch.Post("hub/info", func(channelgo.Body) (any, error) {
			return map[string]any{"key": key, "protocol": 1, "pro": true, "mode": "default-allow"}, nil
		})
	}
	ch.Post("hub/service/update", func(body channelgo.Body) (any, error) {
		headers, _ := body.State.(map[string]string)
		h.updates <- headers
		return nil, nil
	})
	srv, err := server.Listen(ch, 0, server.Options{State: func(headers map[string]string) any {
		h.mu.Lock()
		h.headers = append(h.headers, headers)
		h.mu.Unlock()
		return headers
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	_, port, _ := strings.Cut(srv.Addr(), ":")
	h.port = atoi(port)
	return h
}

func atoi(value string) int {
	n := 0
	for _, c := range value {
		n = n*10 + int(c-'0')
	}
	return n
}

func (h *infoHub) next(t *testing.T) map[string]string {
	t.Helper()
	select {
	case headers := <-h.updates:
		return headers
	case <-time.After(5 * time.Second):
		t.Fatal("no connection")
		return nil
	}
}

func TestServiceLearnsHubKeyAndReconnectsWithV1(t *testing.T) {
	v := loadVectors(t)
	hub := newInfoHub(t, v.Keys["hub"].PublicKey)
	private, _ := KeyFromFile(v.Keys["service"].KeyFile)
	pins := filepath.Join(t.TempDir(), "hubs.json")
	s := New(ServiceOptions{Address: hub.port, Key: private, HubKeys: pins})
	s.Start()
	defer s.Stop()

	first := hub.next(t)
	if first["v"] != "0" || !strings.HasPrefix(first["auth"], "key."+v.Keys["service"].PublicKey+".") {
		t.Fatalf("first connect headers: %v", first)
	}
	second := hub.next(t)
	if second["v"] != "1" {
		t.Fatalf("second connect headers: %v", second)
	}
	verifyToken(t, second["auth"], "hub1:"+v.Keys["hub"].PublicKey+":")
	if s.HubKey() != v.Keys["hub"].PublicKey || OpenHubKeys(pins).Get(hub.port) != v.Keys["hub"].PublicKey {
		t.Fatalf("hub key not pinned: %q", s.HubKey())
	}

	// A new service with the same pins starts on v1 straight away.
	again := New(ServiceOptions{Address: hub.port, Key: private, HubKeys: pins})
	again.Start()
	defer again.Stop()
	if headers := hub.next(t); headers["v"] != "1" {
		t.Fatalf("pinned connect headers: %v", headers)
	}
	select {
	case headers := <-hub.updates:
		t.Fatalf("unexpected reconnect: %v", headers)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestServiceStaysOnV0WithoutHubInfo(t *testing.T) {
	v := loadVectors(t)
	hub := newInfoHub(t, "")
	private, _ := KeyFromFile(v.Keys["service"].KeyFile)
	s := New(ServiceOptions{Address: hub.port, Key: private})
	s.Start()
	defer s.Stop()
	if headers := hub.next(t); headers["v"] != "0" {
		t.Fatalf("headers: %v", headers)
	}
	select {
	case headers := <-hub.updates:
		t.Fatalf("unexpected reconnect: %v", headers)
	case <-time.After(300 * time.Millisecond):
	}
	if s.HubKey() != "" {
		t.Fatalf("pinned %q", s.HubKey())
	}
}

func TestHubKeyOptionPinsUpFront(t *testing.T) {
	v := loadVectors(t)
	private, _ := KeyFromFile(v.Keys["service"].KeyFile)
	s := New(ServiceOptions{Address: 7001, Key: private, HubKey: v.Keys["hub"].PublicKey})
	headers := s.authHeaders()
	if headers["v"] != "1" {
		t.Fatalf("headers: %v", headers)
	}
	verifyToken(t, headers["auth"], "hub1:"+v.Keys["hub"].PublicKey+":")
	if err := s.ForgetHubKey(); err != nil || s.authHeaders()["v"] != "0" {
		t.Fatalf("forget: %v", err)
	}
}
