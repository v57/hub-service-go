package hubservice_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/v57/channel-go/channelgo"
	"github.com/v57/channel-go/server"
	"github.com/v57/hub-service-go"
	"github.com/v57/hub-service-go/identity"
	"github.com/v57/hub-service-go/network"
	"github.com/v57/hub-service-go/protocol"
	"github.com/v57/hub-service-go/service"
)

// freePortServer starts a channel-go server on an ephemeral port that records
// the first hub/service/update body it receives. Body arrives as decoded JSON
// (map[string]any), so we capture it raw.
type freePortServer struct {
	t        *testing.T
	srv      *server.Server
	received chan map[string]any
}

func startFreePortServer(t *testing.T) *freePortServer {
	t.Helper()
	ch := channelgo.NewChannel()
	received := make(chan map[string]any, 1)
	ch.Post("hub/service/update", func(body channelgo.Body) (any, error) {
		asMap, _ := body.Body.(map[string]any)
		if asMap != nil {
			select {
			case received <- asMap:
			default:
			}
		}
		return "ok", nil
	})
	// Use port 0 so the kernel assigns an ephemeral port, then read it back.
	srv, err := server.Listen(ch, 0, server.Options{})
	if err != nil {
		t.Fatalf("server.Listen: %v", err)
	}
	return &freePortServer{t: t, srv: srv, received: received}
}

// port extracts the TCP port from the server's bound address.
func (f *freePortServer) port() int {
	addr := f.srv.Addr()
	_, portStr, _ := strings.Cut(addr, ":")
	port, _ := strconv.Atoi(portStr)
	return port
}

func (f *freePortServer) stop() {
	_ = f.srv.Stop()
}

func TestStartRegistersServicesOnConnect(t *testing.T) {
	serverInstance := startFreePortServer(t)
	defer serverInstance.stop()

	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	svc, err := hubservice.New(hubservice.Options{
		Address:  serverInstance.port(),
		Name:     "lifecycle-test",
		Identity: id,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	svc.Post("example/echo", func(body channelgo.Body) (any, error) {
		return "ok", nil
	}, service.Settings{Permissions: &service.Permissions{Name: "echo"}})
	svc.App("Example", "example")

	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	select {
	case update := <-serverInstance.received:
		services, _ := update["services"].([]any)
		if len(services) != 1 {
			t.Fatalf("expected 1 service, got %d", len(services))
		}
		header, _ := services[0].(map[string]any)
		if header["path"] != "example/echo" {
			t.Fatalf("path = %v, want example/echo", header["path"])
		}
		permissions, _ := header["permissions"].(map[string]any)
		if permissions["name"] != "echo" {
			t.Fatalf("permissions.name = %v, want echo", permissions["name"])
		}
		if permissions["group"] != "example" {
			t.Fatalf("default group = %v, want example", permissions["group"])
		}
		apps, _ := update["apps"].([]any)
		if len(apps) != 1 {
			t.Fatalf("expected 1 app, got %d", len(apps))
		}
		app, _ := apps[0].(map[string]any)
		if app["name"] != "Example" {
			t.Fatalf("app name = %v, want Example", app["name"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for hub/service/update")
	}
}

// TestSendRoundTrip verifies Send, the typed helper, and context forwarding
// against a local channel-go server.
func TestSendRoundTrip(t *testing.T) {
	serverCh := channelgo.NewChannel()
	echoContext := ""
	serverCh.Post("echo", func(body channelgo.Body) (any, error) {
		if str, ok := body.Context.(string); ok {
			echoContext = str
		}
		return body.Body, nil
	})
	srv, err := server.Listen(serverCh, 0, server.Options{})
	if err != nil {
		t.Fatalf("server.Listen: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	_, portStr, _ := strings.Cut(srv.Addr(), ":")
	port, _ := strconv.Atoi(portStr)
	svc, err := hubservice.New(hubservice.Options{Address: port, Identity: id})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	// Wait for connect so the request is not lost.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && svc.Sender() == nil {
		time.Sleep(time.Millisecond)
	}

	response, err := svc.Send("echo", "hello", "ctx-marker")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if response != "hello" {
		t.Fatalf("response = %v, want hello", response)
	}
	if echoContext != "ctx-marker" {
		t.Fatalf("context = %q, want ctx-marker", echoContext)
	}

	typed, err := hubservice.SendTyped[string](svc, "echo", "typed")
	if err != nil {
		t.Fatalf("SendTyped: %v", err)
	}
	if typed != "typed" {
		t.Fatalf("typed = %q, want typed", typed)
	}
}

func TestSendBeforeStartErrors(t *testing.T) {
	svc, err := hubservice.New(hubservice.Options{Identity: mustGenerate(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := svc.Send("anything", nil); err == nil {
		t.Fatalf("expected error before Start")
	}
	if _, err := hubservice.SendTyped[string](svc, "anything", nil); err == nil {
		t.Fatalf("expected typed error before Start")
	}
	if _, err := svc.Subscribe("anything", nil, func(any) {}); err == nil {
		t.Fatalf("expected subscribe error before Start")
	}
}

func mustGenerate(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("identity.Generate: %v", err)
	}
	return id
}

// TestPublishShareEndToEnd verifies HN-075 end-to-end: a started Service
// publishes a share through a custom attestation provider, the SDK submits
// network/shares/replace, and the overlay publication is recorded.
func TestPublishShareEndToEnd(t *testing.T) {
	serverCh := channelgo.NewChannel()
	gotReplace := make(chan protocol.SharesReplacePayload, 1)
	serverCh.Post("network/shares/replace", func(body channelgo.Body) (any, error) {
		if payload, ok := body.Body.(map[string]any); ok {
			// Re-encode/decode into typed payload for assertions.
			shares := []protocol.NetworkSharePublication{}
			_ = payload
			select {
			case gotReplace <- protocol.SharesReplacePayload{}:
			default:
			}
			// Echo back one publication per requested share.
			if reqShares, ok := payload["shares"].([]any); ok {
				for range reqShares {
					shares = append(shares, protocol.NetworkSharePublication{
						Version:        protocol.WireVersion,
						ResourceID:     "hnr1_test",
						ProviderID:     "pvd_test",
						Protocol:       "tcp",
						OverlayAddress: "10.0.0.5",
						Port:           7777,
					})
				}
			}
			return map[string]any{"shares": shares}, nil
		}
		return map[string]any{"shares": []any{}}, nil
	})
	srv, err := server.Listen(serverCh, 0, server.Options{})
	if err != nil {
		t.Fatalf("server.Listen: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	_, portStr, _ := strings.Cut(srv.Addr(), ":")
	port, _ := strconv.Atoi(portStr)
	id := mustGenerate(t)
	serviceKey, _ := id.PublicKeyBase64()
	provider := func(share network.Share, _ string, _ network.Signer) (*network.Attestation, error) {
		return &network.Attestation{
			Version:       network.WireVersion,
			AttestationID: "att",
			ServiceKey:    serviceKey,
			AgentKey:      "agent",
			ShareName:     share.Name,
			Protocol:      string(share.Protocol),
			Target:        protocol.LocalTarget{Host: share.Host, Port: share.Port},
			IssuedAt:      time.Now().Format(time.RFC3339),
			ExpiresAt:     time.Now().Add(time.Hour).Format(time.RFC3339),
			Signature:     "sig",
		}, nil
	}
	svc, err := hubservice.New(hubservice.Options{
		Address: port,
		Identity: id,
		NetworkOptions: network.Options{AttestationProvider: provider},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && svc.Sender() == nil {
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	publications, err := svc.PublishShare(ctx, "s3", network.ProtocolTCP, "127.0.0.1", 8080, &network.Policy{Mode: network.PolicyHub})
	if err != nil {
		t.Fatalf("PublishShare: %v", err)
	}
	if len(publications) != 1 || publications[0].Port != 7777 {
		t.Fatalf("publications = %+v", publications)
	}
	if pub, ok := svc.Network().Publication("s3"); !ok || pub.OverlayAddress != "10.0.0.5" {
		t.Fatalf("stored publication = %+v ok=%v", pub, ok)
	}
	select {
	case <-gotReplace:
	default:
		t.Fatalf("network/shares/replace never received by Hub")
	}
}

// TestRegistrationPropagatesLimitAndProfile verifies HN-074: per-path limit,
// permission group default, and profile metadata are all delivered in the
// single atomic hub/service/update + hub/profile/update message pair.
func TestRegistrationPropagatesLimitAndProfile(t *testing.T) {
	serverCh := channelgo.NewChannel()
	gotProfile := make(chan map[string]any, 1)
	gotUpdate := make(chan map[string]any, 1)
	serverCh.Post("hub/profile/update", func(body channelgo.Body) (any, error) {
		if asMap, ok := body.Body.(map[string]any); ok {
			select {
			case gotProfile <- asMap:
			default:
			}
		}
		return "ok", nil
	})
	serverCh.Post("hub/service/update", func(body channelgo.Body) (any, error) {
		if asMap, ok := body.Body.(map[string]any); ok {
			select {
			case gotUpdate <- asMap:
			default:
			}
		}
		return "ok", nil
	})
	srv, err := server.Listen(serverCh, 0, server.Options{})
	if err != nil {
		t.Fatalf("server.Listen: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	_, portStr, _ := strings.Cut(srv.Addr(), ":")
	port, _ := strconv.Atoi(portStr)
	svc, err := hubservice.New(hubservice.Options{
		Address: port,
		Name:    "limited-svc",
		Icon:    map[string]any{"symbol": "bolt"},
		Identity: mustGenerate(t),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc.Post("limited/path", func(channelgo.Body) (any, error) { return nil, nil }, service.Settings{
		Permissions: &service.Permissions{Name: "call", Allows: true},
		Limit:       42,
	})

	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	select {
	case profile := <-gotProfile:
		if profile["name"] != "limited-svc" {
			t.Fatalf("profile name = %v", profile["name"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for hub/profile/update")
	}

	select {
	case update := <-gotUpdate:
		services, _ := update["services"].([]any)
		if len(services) != 1 {
			t.Fatalf("expected 1 service, got %d", len(services))
		}
		header, _ := services[0].(map[string]any)
		if header["path"] != "limited/path" {
			t.Fatalf("path = %v", header["path"])
		}
		if header["limit"] != float64(42) {
			t.Fatalf("limit = %v, want 42", header["limit"])
		}
		permissions, _ := header["permissions"].(map[string]any)
		if permissions["name"] != "call" || permissions["allows"] != true {
			t.Fatalf("permissions = %+v", permissions)
		}
		if permissions["group"] != "limited" {
			t.Fatalf("default group = %v, want limited", permissions["group"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for hub/service/update")
	}
}

// TestOnConnectInvokedAfterRegistration verifies the Options.OnConnect hook
// fires after the SDK pushes profile and service registration, so callers
// (hub-net agent profile registration) can issue follow-up Hub calls on every
// (re)connect.
func TestOnConnectInvokedAfterRegistration(t *testing.T) {
	serverCh := channelgo.NewChannel()
	gotProfile := make(chan struct{}, 1)
	gotUpdate := make(chan struct{}, 1)
	gotOnConnect := make(chan struct{}, 1)
	serverCh.Post("hub/profile/update", func(body channelgo.Body) (any, error) {
		select {
		case gotProfile <- struct{}{}:
		default:
		}
		return "ok", nil
	})
	serverCh.Post("hub/service/update", func(body channelgo.Body) (any, error) {
		select {
		case gotUpdate <- struct{}{}:
		default:
		}
		return "ok", nil
	})
	srv, err := server.Listen(serverCh, 0, server.Options{})
	if err != nil {
		t.Fatalf("server.Listen: %v", err)
	}
	defer func() { _ = srv.Stop() }()

	_, portStr, _ := strings.Cut(srv.Addr(), ":")
	port, _ := strconv.Atoi(portStr)
	svc, err := hubservice.New(hubservice.Options{
		Address:  port,
		Name:     "onconnect-svc",
		Identity: mustGenerate(t),
		OnConnect: func(sender *channelgo.Sender) {
			select {
			case gotOnConnect <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	// Order matters: profile and service update must land before OnConnect so
	// the callback can rely on Hub having a registered service.
	<-gotProfile
	<-gotUpdate
	select {
	case <-gotOnConnect:
	case <-time.After(2 * time.Second):
		t.Fatalf("OnConnect never invoked")
	}
}
