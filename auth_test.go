package hubservice

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
		Expiry int64  `json:"expiry"`
		Nonce  string `json:"nonce"`
		V1     struct {
			Header string `json:"header"`
		} `json:"v1"`
	} `json:"auth"`
	HubInfo struct {
		Nonce      string `json:"nonce"`
		SignedData string `json:"signedData"`
		Proof      string `json:"proof"`
	} `json:"hubInfo"`
	Transport []struct {
		URL       string `json:"url"`
		Transport string `json:"transport"`
	} `json:"transport"`
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
	v1, err := SignToken(private, v.Keys["hub"].PublicKey, v.Auth.Nonce, expiry)
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
	if got := OpenHubKeys(path).Get("ws://localhost:1997"); got != v.Keys["hub"].PublicKey {
		t.Fatalf("pin by URL = %q", got)
	}
	if got := pins.Get("127.0.0.1:1998"); got != "" {
		t.Fatalf("other address pinned: %q", got)
	}
	data, _ := os.ReadFile(path)
	var stored map[string]string
	if err := json.Unmarshal(data, &stored); err != nil || stored["ws://localhost:1997"] != v.Keys["hub"].PublicKey {
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

func TestHubAddressNormalization(t *testing.T) {
	for input, want := range map[any]string{
		1997:                       "ws://localhost:1997",
		"1997":                     "ws://localhost:1997",
		"Hub.Local:1997":           "ws://hub.local:1997",
		"WSS://Hub.Example.com/":   "wss://hub.example.com",
		"ws://10.0.0.2:1997/Path/": "ws://10.0.0.2:1997/Path",
	} {
		if got := HubAddress(input); got != want {
			t.Errorf("HubAddress(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestDefaultHubKeysBesideKeyFile(t *testing.T) {
	dir := t.TempDir()
	useKeyPath(t, filepath.Join(dir, "auth"))
	if got := DefaultHubKeysPath(); got != filepath.Join(dir, "hubs.json") {
		t.Fatalf("hubs.json path = %q", got)
	}
}

func TestHubInfoProofMatchesVectors(t *testing.T) {
	v := loadVectors(t)
	hub := v.Keys["hub"].PublicKey
	if got := HubInfoSignedData(hub, v.HubInfo.Nonce); got != v.HubInfo.SignedData {
		t.Fatalf("signed data: %s", got)
	}
	info := HubInfo{Key: hub, Proof: v.HubInfo.Proof, Challenge: "c"}
	if !info.Proves(v.HubInfo.Nonce) {
		t.Fatal("proof doesn't verify")
	}
	if info.Proves("another-nonce-another-nonce") {
		t.Fatal("proof verifies for another nonce")
	}
	info.Key = v.Keys["service"].PublicKey
	if info.Proves(v.HubInfo.Nonce) {
		t.Fatal("proof verifies for another key")
	}
}

func TestTransportMatchesVectors(t *testing.T) {
	for _, c := range loadVectors(t).Transport {
		if got := HubTransportOf(c.URL); string(got) != c.Transport {
			t.Errorf("%s: %s, want %s", c.URL, got, c.Transport)
		}
	}
}

func TestPublicPlaintextIsNotDialed(t *testing.T) {
	s := New(ServiceOptions{Address: "ws://hub.example.com:1997", Sign: noSign})
	s.Start()
	defer s.Stop()
	if s.Sender() != nil {
		t.Fatal("dialed a public ws:// address")
	}
	if _, err := s.Send("x", nil); err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("send: %v", err)
	}
	allowed := New(ServiceOptions{Address: "ws://127.0.0.1:1", Sign: noSign, AllowPlaintext: true})
	if allowed.allowPlaintext != true {
		t.Fatal("AllowPlaintext ignored")
	}
}

// fakeHub answers hub/info as answer says and records every connection's
// headers. Its tokens are checked by the tests.
type fakeHub struct {
	mu      sync.Mutex
	headers []map[string]string
	port    int
	updates chan map[string]string
}

func newFakeHub(t *testing.T, answer func(nonce, challenge string) map[string]any) *fakeHub {
	t.Helper()
	h := &fakeHub{updates: make(chan map[string]string, 16)}
	ch := channelgo.NewChannel()
	asked := 0
	ch.Post("hub/info", func(body channelgo.Body) (any, error) {
		input, _ := body.Body.(map[string]any)
		nonce, _ := input["nonce"].(string)
		asked++
		return answer(nonce, "challenge-"+strconv.Itoa(asked)), nil
	})
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
	h.port, _ = strconv.Atoi(port)
	return h
}

func (h *fakeHub) next(t *testing.T) map[string]string {
	t.Helper()
	select {
	case headers := <-h.updates:
		return headers
	case <-time.After(5 * time.Second):
		t.Fatal("no connection")
		return nil
	}
}

// credentialsSent lists the auth headers the hub has seen so far.
func (h *fakeHub) credentialsSent() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sent []string
	for _, headers := range h.headers {
		if headers["auth"] != "" || headers["device"] != "" {
			sent = append(sent, headers["auth"])
		}
	}
	return sent
}

func provingHub(t *testing.T, name string) func(nonce, challenge string) map[string]any {
	t.Helper()
	key := loadVectors(t).Keys[name]
	private, err := KeyFromFile(key.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	return func(nonce, challenge string) map[string]any {
		return map[string]any{"key": key.PublicKey, "protocol": 2, "challenge": challenge,
			"proof": base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte(HubInfoSignedData(key.PublicKey, nonce))))}
	}
}

func TestServiceSendsATokenOnlyAfterProof(t *testing.T) {
	v := loadVectors(t)
	hub := newFakeHub(t, provingHub(t, "hub"))
	private, _ := KeyFromFile(v.Keys["service"].KeyFile)
	pins := filepath.Join(t.TempDir(), "hubs.json")
	s := New(ServiceOptions{Address: hub.port, Key: private, HubKeys: pins, Device: DeviceOptions{Disabled: true}})
	// A call made right away waits for the authenticated connection.
	reply := make(chan error, 1)
	s.Start()
	defer s.Stop()
	go func() {
		_, err := s.Send("hub/info", nil)
		reply <- err
	}()

	registered := hub.next(t)
	if registered["v"] != "1" {
		t.Fatalf("headers: %v", registered)
	}
	verifyToken(t, registered["auth"], "hub1:"+v.Keys["hub"].PublicKey+":")
	if err := <-reply; err != nil {
		t.Fatalf("call made before the connect: %v", err)
	}
	if s.HubKey() != v.Keys["hub"].PublicKey || OpenHubKeys(pins).Get(hub.port) != v.Keys["hub"].PublicKey {
		t.Fatalf("hub key not pinned: %q", s.HubKey())
	}
	hub.mu.Lock()
	first := hub.headers[0]
	hub.mu.Unlock()
	if first["auth"] != "" || first["device"] != "" {
		t.Fatalf("the first connection carried credentials: %v", first)
	}
	// A service with the pin also asks first, for its own challenge.
	again := New(ServiceOptions{Address: hub.port, Key: private, HubKeys: pins, Device: DeviceOptions{Disabled: true}})
	again.Start()
	defer again.Stop()
	if headers := hub.next(t); headers["v"] != "1" || strings.Split(headers["auth"], ".")[4] == strings.Split(registered["auth"], ".")[4] {
		t.Fatalf("second token reused a challenge: %v", headers)
	}
}

func TestHubWithoutProofGetsNothing(t *testing.T) {
	v := loadVectors(t)
	for name, answer := range map[string]func(nonce, challenge string) map[string]any{
		"no proof": func(_, challenge string) map[string]any {
			return map[string]any{"key": v.Keys["hub"].PublicKey, "challenge": challenge}
		},
		"another key's proof": func(nonce, challenge string) map[string]any {
			// Claims the real hub's key, signs with its own.
			answer := provingHub(t, "service")(nonce, challenge)
			answer["key"] = v.Keys["hub"].PublicKey
			return answer
		},
	} {
		t.Run(name, func(t *testing.T) {
			hub := newFakeHub(t, answer)
			private, _ := KeyFromFile(v.Keys["service"].KeyFile)
			s := New(ServiceOptions{Address: hub.port, Key: private, Device: DeviceOptions{Disabled: true}})
			s.Start()
			defer s.Stop()
			select {
			case headers := <-hub.updates:
				t.Fatalf("connected: %v", headers)
			case <-time.After(1500 * time.Millisecond):
			}
			if sent := hub.credentialsSent(); len(sent) != 0 {
				t.Fatalf("sent credentials: %v", sent)
			}
			if s.HubKey() != "" {
				t.Fatalf("pinned %q", s.HubKey())
			}
		})
	}
}

func TestHubWithAnotherKeyThanThePinGetsNothing(t *testing.T) {
	v := loadVectors(t)
	// Proves its own key, but this address is pinned to the real hub's.
	hub := newFakeHub(t, provingHub(t, "service"))
	private, _ := KeyFromFile(v.Keys["service"].KeyFile)
	s := New(ServiceOptions{Address: hub.port, Key: private, HubKey: v.Keys["hub"].PublicKey, Device: DeviceOptions{Disabled: true}})
	s.Start()
	defer s.Stop()
	select {
	case headers := <-hub.updates:
		t.Fatalf("connected: %v", headers)
	case <-time.After(1500 * time.Millisecond):
	}
	if sent := hub.credentialsSent(); len(sent) != 0 {
		t.Fatalf("sent credentials: %v", sent)
	}
	if s.HubKey() != v.Keys["hub"].PublicKey {
		t.Fatalf("pin replaced: %q", s.HubKey())
	}
	s.Stop() // ends the waiting handshake rather than hanging
}

func TestHubKeyOptionPinsUpFront(t *testing.T) {
	v := loadVectors(t)
	private, _ := KeyFromFile(v.Keys["service"].KeyFile)
	s := New(ServiceOptions{Address: 7001, Key: private, HubKey: v.Keys["hub"].PublicKey})
	if s.HubKey() != v.Keys["hub"].PublicKey {
		t.Fatalf("hub key: %q", s.HubKey())
	}
	if err := s.ForgetHubKey(); err != nil || s.HubKey() != "" {
		t.Fatalf("forget: %v, %q", err, s.HubKey())
	}
}
