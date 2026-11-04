package hubservice

// Auth v2's client side (Docs/Devices/protocol.md section 2): before every
// connect the SDK asks hub/info on an anonymous connection with a fresh nonce,
// and sends its token only if the hub proved the key it claims, over that nonce.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/v57/channel-go/channelgo"
	"github.com/v57/channel-go/client"
)

// HubTransport says how a connection to an address travels.
type HubTransport string

const (
	TransportEncrypted    HubTransport = "encrypted"
	TransportLoopback     HubTransport = "loopback"
	TransportOverlay      HubTransport = "overlay"
	TransportLocalNetwork HubTransport = "localNetwork"
	// TransportRefused is plain ws:// beyond loopback, the overlay and the LAN:
	// it isn't dialed unless AllowPlaintext says so.
	TransportRefused HubTransport = "refused"
)

var hubAddressPattern = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*)://(?:[^/?#@]*@)?(\[[^\]]*\]|[^/?#:]*)`)

// HubTransportOf classifies an address before anything is dialed.
func HubTransportOf(address any) HubTransport {
	if _, ok := address.(int); ok {
		return TransportLoopback
	}
	text := strings.TrimSpace(fmt.Sprint(address))
	if _, err := strconv.Atoi(text); err == nil {
		return TransportLoopback
	}
	if !strings.Contains(text, "://") {
		text = "ws://" + text
	}
	match := hubAddressPattern.FindStringSubmatch(text)
	if match == nil {
		return TransportRefused
	}
	scheme := strings.ToLower(match[1])
	host := strings.TrimSuffix(strings.ToLower(match[2]), ".")
	switch scheme {
	case "wss", "https", "quic":
		return TransportEncrypted
	case "ws", "http":
	default:
		return TransportRefused
	}
	if strings.HasPrefix(host, "[") {
		v6 := strings.Trim(host, "[]")
		switch {
		case v6 == "::1":
			return TransportLoopback
		case strings.HasPrefix(v6, "fc"), strings.HasPrefix(v6, "fd"),
			strings.HasPrefix(v6, "fe8"), strings.HasPrefix(v6, "fe9"), strings.HasPrefix(v6, "fea"), strings.HasPrefix(v6, "feb"):
			return TransportLocalNetwork
		}
		return TransportRefused
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		v4 := ip.To4()
		a, b := v4[0], v4[1]
		switch {
		case a == 127:
			return TransportLoopback
		case a == 100 && b >= 64 && b <= 127:
			return TransportOverlay
		case a == 10, a == 172 && b >= 16 && b <= 31, a == 192 && b == 168, a == 169 && b == 254:
			return TransportLocalNetwork
		}
		return TransportRefused
	}
	switch {
	case host == "localhost", strings.HasSuffix(host, ".localhost"):
		return TransportLoopback
	case strings.HasSuffix(host, ".hub.invalid"):
		return TransportOverlay
	case strings.HasSuffix(host, ".local"):
		return TransportLocalNetwork
	}
	return TransportRefused
}

// MakeClientNonce is the nonce for one `hub/info`: 32 fresh random bytes in base64url.
func MakeClientNonce() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// HubInfoSignedData is the string a `hub/info` proof signs.
func HubInfoSignedData(hubKey, nonce string) string {
	return "hubinfo1:" + hubKey + ":" + nonce
}

// HubInfo is an answer to `hub/info` with a nonce.
type HubInfo struct {
	Key       string `json:"key"`
	Proof     string `json:"proof"`
	Challenge string `json:"challenge"`
}

// Proves says whether the answer proves its key over nonce. A relayed answer
// passes too, so it never decides which credentials a hub gets.
func (i HubInfo) Proves(nonce string) bool {
	if i.Key == "" || i.Proof == "" || i.Challenge == "" {
		return false
	}
	public, err := parseSPKI(i.Key)
	if err != nil {
		return false
	}
	signature, err := base64.StdEncoding.DecodeString(i.Proof)
	if err != nil {
		return false
	}
	return ed25519.Verify(public, []byte(HubInfoSignedData(i.Key, nonce)), signature)
}

var errNoProof = errors.New("hub didn't prove its key")

// askHub asks the hub at address for its key and a challenge, on a short
// anonymous connection, and returns the answer only with a valid proof.
func askHub(address any, timeout time.Duration) (HubInfo, error) {
	// The client retries a refused connect forever; a hub that is down
	// fails here at once.
	dial, err := net.DialTimeout("tcp", dialHost(address), timeout)
	if err != nil {
		return HubInfo{}, err
	}
	dial.Close()
	nonce, err := MakeClientNonce()
	if err != nil {
		return HubInfo{}, err
	}
	conn := client.Connect(channelgo.NewChannel(), address, client.Options{})
	defer conn.Stop()
	type result struct {
		info HubInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		value, err := conn.Send("hub/info", map[string]any{"nonce": nonce})
		if err != nil {
			done <- result{err: err}
			return
		}
		var info HubInfo
		data, _ := json.Marshal(value)
		if err := json.Unmarshal(data, &info); err != nil {
			done <- result{err: err}
			return
		}
		if !info.Proves(nonce) {
			done <- result{err: errNoProof}
			return
		}
		done <- result{info: info}
	}()
	select {
	case r := <-done:
		return r.info, r.err
	case <-time.After(timeout):
		return HubInfo{}, errors.New("hub/info timed out")
	}
}
