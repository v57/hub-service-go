// Package hubservice provides the Go Hub Service SDK: persistent identity,
// authenticated WebSocket connection lifecycle, service registration, RPC, and
// network-share publication layered on github.com/v57/channel-go.
//
// This is the initial scaffold (HN-070). Identity handling, connection
// lifecycle, RPC helpers, share publication, and conformance tests land in
// subsequent tasks (HN-071 through HN-078).
package hubservice

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/v57/channel-go/channelgo"
	"github.com/v57/channel-go/client"
	"github.com/v57/hub-service-go/identity"
	"github.com/v57/hub-service-go/network"
	"github.com/v57/hub-service-go/service"
)

// errNotStarted is returned by RPC helpers called before Start.
var errNotStarted = errors.New("hubservice: service not started")

// AuthVersion is the Hub auth header version token, matching the Bun SDK.
const AuthVersion = "0"

// DefaultHubAddress is used when no address is configured. Matches the Bun
// default of port 1997.
const DefaultHubAddress = 1997

// Options configures a Service.
type Options struct {
	// Address is the Hub WebSocket address: integer port, "host:port", or full
	// "ws://..." URL. Defaults to DefaultHubAddress.
	Address any
	// Name is the optional service display name pushed on connect.
	Name string
	// Icon is the optional service display icon pushed on connect.
	Icon any
	// IdentityPath is the filesystem path of the persistent identity file. When
	// empty, identity must be supplied via Identity.
	IdentityPath string
	// Identity is an explicit identity. When nil, identity is loaded from
	// IdentityPath, generating a fresh key if absent.
	Identity *identity.Identity
	// Network configures the network-share publisher. When nil, a default
	// Publisher is created and shares declared via Share/PublishShare are
	// published through the local hub-net agent.
	Network *network.Publisher
	// NetworkOptions configures the default network publisher when Network is
	// nil.
	NetworkOptions network.Options
	// OnConnect is invoked after the SDK pushes its profile, service
	// registration, and share republication on every (re)connect. It receives
	// the connected sender so callers can issue follow-up Hub calls (for
	// example, hub-net registering its network agent profile). Errors are
	// logged by the SDK but never abort the connection.
	OnConnect func(sender *channelgo.Sender)
}

// Service is a Hub service connection. Methods on a zero Service are not safe
// to call before Start.
type Service struct {
	mu       sync.Mutex
	channel  *channelgo.Channel
	sender   *client.ClientSender
	identity *identity.Identity
	address  any
	profile  *service.Profile
	network  *network.Publisher

	postPaths   map[string]service.Settings
	streamPaths map[string]service.Settings
	apps        []service.AppHeader
	onConnect   func(sender *channelgo.Sender)
	started     bool
}

// New constructs a Service with the given options, loading or generating
// identity material as configured.
func New(options Options) (*Service, error) {
	id := options.Identity
	if id == nil {
		loaded, err := identity.Load(options.IdentityPath)
		if err != nil {
			return nil, err
		}
		id = loaded
	}
	address := options.Address
	if address == nil {
		address = DefaultHubAddress
	}
	publisher := options.Network
	if publisher == nil {
		publisher = network.New(options.NetworkOptions)
	}
	svc := &Service{
		channel:     channelgo.NewChannel(),
		identity:    id,
		address:     address,
		network:     publisher,
		onConnect:   options.OnConnect,
		postPaths:   make(map[string]service.Settings),
		streamPaths: make(map[string]service.Settings),
	}
	if options.Name != "" || options.Icon != nil {
		svc.profile = &service.Profile{Name: options.Name, Icon: options.Icon}
	}
	return svc, nil
}

// Channel returns the underlying Channel runtime so callers can register post,
// stream, and subscription handlers before Start.
func (s *Service) Channel() *channelgo.Channel { return s.channel }

// Identity exposes the service identity for local-agent challenge proofs.
func (s *Service) Identity() *identity.Identity { return s.identity }

// Sender returns the connected Hub sender. Nil before Start and after Stop.
func (s *Service) Sender() *channelgo.Sender {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sender == nil {
		return nil
	}
	return s.sender.Sender
}

// Post registers an RPC request handler at path and records its registration
// settings so the path is re-registered on every reconnect. Equivalent to the
// Bun service.post API.
func (s *Service) Post(path string, action channelgo.Function, settings service.Settings) {
	s.mu.Lock()
	s.postPaths[path] = settings
	s.mu.Unlock()
	s.channel.Post(path, action)
}

// Stream registers an RPC stream handler at path and records its registration
// settings so the path is re-registered on every reconnect. Equivalent to the
// Bun service.stream API.
func (s *Service) Stream(path string, action channelgo.StreamFunction, settings service.Settings) {
	s.mu.Lock()
	s.streamPaths[path] = settings
	s.mu.Unlock()
	s.channel.Stream(path, action)
}

// App registers an app route surfaced by this service.
func (s *Service) App(name, path string) {
	s.mu.Lock()
	s.apps = append(s.apps, service.AppHeader{Type: "app", Name: name, Path: path})
	s.mu.Unlock()
}

// Start connects to Hub. RPC handlers must be registered on Channel before
// calling Start. On every (re)connect the SDK pushes the configured profile
// and re-registers all post/stream services via hub/service/update.
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("hubservice: already started")
	}
	headers := func() map[string]string {
		token, err := s.identity.AuthToken(authTokenValidFor)
		if err != nil {
			return map[string]string{}
		}
		return map[string]string{"auth": token, "v": AuthVersion}
	}
	onConnect := func(sender *channelgo.Sender) {
		if s.profile != nil {
			_, _ = sender.Send("hub/profile/update", s.profile)
		}
		_, _ = sender.Send("hub/service/update", s.registrationMessage())
		if s.network.HasShares() {
			ctx, cancel := context.WithTimeout(context.Background(), networkRepublishTimeout)
			defer cancel()
			serviceKey, err := s.identity.PublicKeyBase64()
			if err == nil {
				_, _ = s.network.Republish(ctx, serviceKey, s.identity, sender)
			}
		}
		if s.onConnect != nil {
			s.onConnect(sender)
		}
	}
	s.sender = client.Connect(s.channel, s.address, client.Options{
		Headers:   headers,
		OnConnect: onConnect,
	})
	s.started = true
	return nil
}

// registrationMessage builds the hub/service/update body from registered paths.
func (s *Service) registrationMessage() service.UpdateMessage {
	headers := make([]service.Header, 0, len(s.postPaths)+len(s.streamPaths))
	for path, settings := range s.postPaths {
		headers = append(headers, serviceHeader(path, settings))
	}
	for path, settings := range s.streamPaths {
		headers = append(headers, serviceHeader(path, settings))
	}
	apps := append([]service.AppHeader(nil), s.apps...)
	return service.UpdateMessage{Services: headers, Apps: apps}
}

// serviceHeader applies the default-permission-group behavior matching the Bun
// SDK: when permissions omit a group, the group defaults to the first path
// segment.
func serviceHeader(path string, settings service.Settings) service.Header {
	header := service.Header{Path: path, Limit: settings.Limit}
	if settings.Permissions != nil {
		permissions := *settings.Permissions
		if permissions.Group == "" {
			permissions.Group = defaultGroup(path)
		}
		header.Permissions = &permissions
	}
	return header
}

// defaultGroup returns the first path segment, matching the Bun behavior of
// path.split('/')[0].
func defaultGroup(path string) string {
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return path
}

// Stop closes the Hub connection and releases transport resources.
func (s *Service) Stop() {
	s.mu.Lock()
	sender := s.sender
	s.started = false
	s.sender = nil
	s.mu.Unlock()
	if sender != nil {
		sender.Stop()
	}
}

// Send issues a request/response call to path and blocks until a response
// arrives. Returns an error if the Service has not been started. The optional
// contextValue is forwarded as the request context, matching the Bun
// SendOptions.context behavior.
func (s *Service) Send(path string, body any, contextValue ...any) (any, error) {
	sender := s.requireSender()
	if sender == nil {
		return nil, errNotStarted
	}
	return sender.Send(path, body, contextValue...)
}

// SendTyped is the generic form of Send, decoding the response into T. It uses
// the channelgo typed helper so the wire JSON is re-decoded into the requested
// type, mirroring channelgo.Send[T].
func SendTyped[T any](s *Service, path string, body any, contextValue ...any) (T, error) {
	var zero T
	sender := s.requireSender()
	if sender == nil {
		return zero, errNotStarted
	}
	return channelgo.Send[T](sender, path, body, contextValue...)
}

// Request issues an asynchronous request/response call. Caller MUST Start the
// Service first; Request on an unstarted Service returns nil.
func (s *Service) Request(path string, body any, contextValue ...any) *channelgo.SendingRequest {
	sender := s.requireSender()
	if sender == nil {
		return nil
	}
	return sender.Request(path, body, contextValue...)
}

// Values issues a streaming request. Caller MUST Start the Service first; Values
// on an unstarted Service returns nil.
func (s *Service) Values(path string, body any, contextValue ...any) *channelgo.Values {
	sender := s.requireSender()
	if sender == nil {
		return nil
	}
	return sender.Values(path, body, contextValue...)
}

// Subscribe registers a subscription for topic and invokes event for every
// delivered event payload. The returned Cancellable stops delivery locally.
// Returns an error if the Service has not been started.
func (s *Service) Subscribe(path string, body any, event channelgo.EventBody, contextValue ...any) (channelgo.Cancellable, error) {
	sender := s.requireSender()
	if sender == nil {
		return nil, errNotStarted
	}
	return sender.Subscribe(path, body, event, contextValue...)
}

// requireSender returns the current connected sender or nil when not started.
func (s *Service) requireSender() *channelgo.Sender {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sender == nil {
		return nil
	}
	return s.sender.Sender
}

// Network exposes the network-share publisher for direct share management.
func (s *Service) Network() *network.Publisher { return s.network }

// ServiceSettings returns an empty Settings value for callers that want the
// zero registration configuration without constructing the struct inline.
func ServiceSettings() service.Settings { return service.Settings{} }

// Share declares a network share before Start. The complete declared set is
// published on connect and republished after every reconnect. Equivalent to the
// Bun service.share API.
func (s *Service) Share(name string, protocol network.Protocol, host string, port uint16, policy *network.Policy) *Service {
	s.network.Declare(network.Share{
		Name:     name,
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Policy:   policy,
	})
	return s
}

// PublishShare publishes a network share immediately. Requires the Service to be
// started. Equivalent to the Bun service.publishShare API.
func (s *Service) PublishShare(ctx context.Context, name string, protocol network.Protocol, host string, port uint16, policy *network.Policy) ([]network.Publication, error) {
	sender := s.requireSender()
	if sender == nil {
		return nil, errNotStarted
	}
	serviceKey, err := s.identity.PublicKeyBase64()
	if err != nil {
		return nil, err
	}
	return s.network.Publish(ctx, network.Share{
		Name:     name,
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Policy:   policy,
	}, serviceKey, s.identity, sender)
}

// UpdateShare updates an already-declared share's policy or runtime port and
// re-publishes. Equivalent to the Bun service.updateShare API.
func (s *Service) UpdateShare(ctx context.Context, name string, policy *network.Policy, port uint16) ([]network.Publication, error) {
	sender := s.requireSender()
	if sender == nil {
		return nil, errNotStarted
	}
	current, ok := s.network.DeclaredShare(name)
	if !ok {
		return nil, fmt.Errorf("hubservice: share %q not found", name)
	}
	if policy != nil {
		current.Policy = policy
	}
	if port != 0 {
		current.Port = port
	}
	serviceKey, err := s.identity.PublicKeyBase64()
	if err != nil {
		return nil, err
	}
	return s.network.Publish(ctx, current, serviceKey, s.identity, sender)
}

// RemoveShare removes a published share. Requires the Service to be started.
func (s *Service) RemoveShare(ctx context.Context, name string) ([]network.Publication, error) {
	sender := s.requireSender()
	if sender == nil {
		return nil, errNotStarted
	}
	serviceKey, err := s.identity.PublicKeyBase64()
	if err != nil {
		return nil, err
	}
	return s.network.Remove(ctx, name, serviceKey, s.identity, sender)
}

// SharePublications returns the latest overlay publications for published
// shares.
func (s *Service) SharePublications() []network.Publication {
	return s.network.Publications()
}
