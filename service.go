// Package hubservice lets a Go process connect to a hub (hub-lite/hub-pro),
// host post/stream API endpoints, and call API through the hub.
//
// Port of hub-service (TS reference). Every (re)connect sends a fresh hub-pro
// auth token from the keychain (see keychain.go), bound to the hub's key once
// it is known (see hubkeys.go); hub-lite ignores it.
package hubservice

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/v57/channel-go/channelgo"
	"github.com/v57/channel-go/client"
)

var errNotStarted = errors.New("Service not started")

// ServiceOptions configures a Service.
type ServiceOptions struct {
	// Address is a port (int) or a ws:// URL (string). Default: 1997 or $HUB.
	Address any
	// Name is the profile name shown in the hub.
	Name string
	// Icon is the profile icon: a string or map[string]any{"symbol":..,"text":..}.
	Icon any
	// Key is the service key that signs auth tokens. Default: the keychain
	// (`auth` in the working directory, or $HUBKEY).
	Key ed25519.PrivateKey
	// HubKeys is the `hubs.json` file that pins hub keys per address.
	// Default: beside the keychain's key file; with Key set, pins stay in
	// memory unless this is set.
	HubKeys string
	// HubKey is the hub's key when it is known up front (an invitation or
	// config). It is pinned for Address.
	HubKey string
	// Sign replaces the SDK's signing: its token goes out with `v: 0` on every
	// connect and the hub key is never learned. Default: nil (the SDK signs
	// with Key).
	Sign func() (string, error)
	// Device certifies the service key with this device's key on every
	// connect (the `device` header). On by default; ignored with Sign.
	Device DeviceOptions
	// OnConnect runs after every (re)connect, once the profile and service
	// updates are queued. It runs on the connection's event loop: sends from
	// it work, and a sender kept for later is safe to use from any goroutine.
	OnConnect func(sender *channelgo.Sender)
}

// Profile is sent to the hub as hub/profile/update on connect.
type Profile struct {
	Name string `json:"name,omitempty"`
	Icon any    `json:"icon,omitempty"`
}

// AppHeader identifies an app in the hub.
type AppHeader struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// ServicePermissions is a pro-only wire-parity field; hub-lite ignores it.
type ServicePermissions struct {
	Group  string `json:"group,omitempty"`
	Name   string `json:"name"`
	Allows *bool  `json:"allows,omitempty"`
}

// ApiSettings configures a registered API path (pro-only fields, wire parity).
type ApiSettings struct {
	Permissions *ServicePermissions `json:"permissions,omitempty"`
	Limit       *int                `json:"limit,omitempty"`
}

// ServiceHeader is one entry of the hub/service/update services array.
type ServiceHeader struct {
	Path        string              `json:"path"`
	Permissions *ServicePermissions `json:"permissions,omitempty"`
	Limit       *int                `json:"limit,omitempty"`
}

// serviceUpdate is the hub/service/update payload.
type serviceUpdate struct {
	Services []ServiceHeader `json:"services"`
	Apps     []AppHeader     `json:"apps"`
}

// Service registers API endpoints on a hub and serves calls forwarded by it.
type Service struct {
	ch      *channelgo.Channel
	address any
	profile *Profile
	apps    []AppHeader
	// paths tracks every registered handler path (post, stream, app) in
	// registration-independent order; used to build the services list.
	paths     map[string]struct{}
	settings  map[string]ApiSettings
	sender    *channelgo.Sender
	conn      *client.ClientSender
	epoch     int
	sign      func() (string, error)
	key       ed25519.PrivateKey
	hubKeys   *HubKeys
	device    *deviceCertifier
	onConnect func(*channelgo.Sender)
	mu        sync.Mutex
}

// New creates a Service. Options are optional; the first one wins.
func New(options ...ServiceOptions) *Service {
	opts := ServiceOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	s := &Service{
		ch:        channelgo.NewChannel(),
		apps:      []AppHeader{},
		paths:     map[string]struct{}{},
		settings:  map[string]ApiSettings{},
		sign:      opts.Sign,
		key:       opts.Key,
		onConnect: opts.OnConnect,
	}
	if opts.Address != nil {
		s.address = opts.Address
	} else if env := os.Getenv("HUB"); env != "" {
		s.address = env
	} else {
		s.address = 1997
	}
	switch {
	case opts.HubKeys != "":
		s.hubKeys = OpenHubKeys(opts.HubKeys)
	case opts.Key == nil && opts.Sign == nil:
		s.hubKeys = OpenHubKeys(DefaultHubKeysPath())
	default:
		s.hubKeys = OpenHubKeys("")
	}
	if opts.Sign == nil && !opts.Device.Disabled {
		s.device = newDeviceCertifier(opts.Device, opts.Name)
	}
	if opts.HubKey != "" {
		if err := s.hubKeys.Pin(s.address, opts.HubKey); err != nil {
			fmt.Fprintln(os.Stderr, "hub-service: pin hub key:", err)
		}
	}
	if opts.Name != "" || opts.Icon != nil {
		s.profile = &Profile{Name: opts.Name, Icon: opts.Icon}
	}
	return s
}

// Post registers a request/response API endpoint.
func (s *Service) Post(path string, handler func(body any) (any, error), settings ...ApiSettings) *Service {
	s.addPath(path, settings)
	s.ch.Post(path, func(body channelgo.Body) (any, error) {
		return handler(body.Body)
	})
	return s
}

// Stream registers a streaming API endpoint. The handler emits values via emit.
func (s *Service) Stream(path string, handler func(body any, emit func(any) error) error, settings ...ApiSettings) *Service {
	s.addPath(path, settings)
	s.ch.Stream(path, func(body channelgo.Body, emit channelgo.StreamEmitter) error {
		return handler(body.Body, emit.Emit)
	})
	return s
}

// StreamContext registers a streaming API endpoint whose ctx is cancelled
// when the caller cancels the stream, for handlers that wait between values.
func (s *Service) StreamContext(path string, handler func(ctx context.Context, body any, emit func(any) error) error, settings ...ApiSettings) *Service {
	s.addPath(path, settings)
	s.ch.Stream(path, func(body channelgo.Body, emit channelgo.StreamEmitter) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if body.Task != nil {
			body.Task.OnCancel(cancel)
		}
		return handler(ctx, body.Body, emit.Emit)
	})
	return s
}

// App registers an app: its header goes to the hub apps list and its path
// serves the AppInterface as a stream (TS parity: the path is also callable).
func (s *Service) App(app App) *Service {
	s.mu.Lock()
	s.apps = append(s.apps, app.Header)
	s.mu.Unlock()
	s.addPath(app.Header.Path, nil)
	s.ch.Stream(app.Header.Path, func(body channelgo.Body, emit channelgo.StreamEmitter) error {
		return emit.Emit(map[string]any{
			"header": app.Header,
			"body":   app.Body,
			"top":    app.Top,
			"bottom": app.Bottom,
			"data":   app.Data,
		})
	})
	return s
}

// Send calls an API through the hub. Errors if the service is not started.
func (s *Service) Send(path string, body any, contextValue ...any) (any, error) {
	sender, err := s.senderOrError()
	if err != nil {
		return nil, err
	}
	return sender.Send(path, body, contextValue...)
}

// Values opens a stream call through the hub. Errors if not started.
func (s *Service) Values(path string, body any, contextValue ...any) (*channelgo.Values, error) {
	sender, err := s.senderOrError()
	if err != nil {
		return nil, err
	}
	return sender.Values(path, body, contextValue...), nil
}

// Start connects to the hub. On every (re)connect it sends hub/profile/update
// (if a profile is set) and hub/service/update with the current services+apps.
func (s *Service) Start() *Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startLocked()
	return s
}

func (s *Service) startLocked() {
	if s.sender != nil {
		return
	}
	var conn *client.ClientSender
	conn = client.Connect(s.ch, s.address, client.Options{
		Headers: s.authHeaders,
		OnConnect: func(sender *channelgo.Sender) {
			s.sendUpdates(sender)
			if s.onConnect != nil {
				s.onConnect(sender)
			}
			if s.sign == nil && s.HubKey() == "" {
				go s.learnHubKey(conn, sender)
			}
		},
	})
	s.conn = conn
	s.sender = conn.Sender
}

// HubKey is the hub's pinned key for this service's address, or "" while it
// is unknown (the service then connects with v0 tokens).
func (s *Service) HubKey() string {
	return s.hubKeys.Get(s.address)
}

// ForgetHubKey clears the pinned hub key, for a hub whose key changed. The
// next connect learns it again.
func (s *Service) ForgetHubKey() error {
	return s.hubKeys.Forget(s.address)
}

// learnHubKey asks the hub for its key after a v0 connect, pins it and
// reconnects so the session uses hub-bound tokens. hub-lite and older hubs
// don't answer hub/info and stay on v0.
func (s *Service) learnHubKey(conn *client.ClientSender, sender *channelgo.Sender) {
	value, err := sender.Send("hub/info", nil)
	if err != nil {
		return
	}
	var info struct {
		Key string `json:"key"`
	}
	data, err := json.Marshal(value)
	if err != nil || json.Unmarshal(data, &info) != nil || info.Key == "" {
		return
	}
	if err := s.hubKeys.Pin(s.address, info.Key); err != nil {
		fmt.Fprintln(os.Stderr, "hub-service: pin hub key:", err)
		return
	}
	s.reconnect(conn)
}

// reconnect replaces conn with a fresh connection, unless the service was
// stopped or restarted meanwhile.
func (s *Service) reconnect(conn *client.ClientSender) {
	s.mu.Lock()
	if s.conn != conn {
		s.mu.Unlock()
		return
	}
	epoch := s.epoch
	s.conn = nil
	s.sender = nil
	s.mu.Unlock()
	conn.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch == epoch {
		s.startLocked()
	}
}

// Sender returns the hub connection's sender, nil before Start and after Stop.
func (s *Service) Sender() *channelgo.Sender {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sender
}

// Stop closes the hub connection. Start connects again.
func (s *Service) Stop() {
	s.mu.Lock()
	sender := s.sender
	s.sender = nil
	s.conn = nil
	s.epoch++
	s.mu.Unlock()
	if sender != nil {
		sender.Stop()
	}
}

// authHeaders signs a fresh token for each connect: bound to the hub (v1)
// once its key is pinned, else v0, plus the device certificate. If signing
// fails the connection goes out without auth: hub-lite accepts it, hub-pro
// treats it as an anonymous connection. Without a certificate it is a plain
// key connection.
func (s *Service) authHeaders() map[string]string {
	if s.sign != nil {
		token, err := s.sign()
		if err != nil {
			fmt.Fprintln(os.Stderr, "hub-service: auth token:", err)
			return nil
		}
		return map[string]string{"auth": token, "v": authVersion}
	}
	private, err := s.privateKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hub-service: auth token:", err)
		return nil
	}
	hubKey := s.HubKey()
	token, err := SignToken(private, hubKey, time.Now().Add(signExpiry))
	if err != nil {
		fmt.Fprintln(os.Stderr, "hub-service: auth token:", err)
		return nil
	}
	version := authVersion
	if hubKey != "" {
		version = authVersionHub
	}
	headers := map[string]string{"auth": token, "v": version}
	if s.device != nil {
		serviceKey, _, _ := strings.Cut(strings.TrimPrefix(token, "key."), ".")
		if certificate, err := s.device.certificate(serviceKey); err != nil {
			fmt.Fprintln(os.Stderr, "hub-service: device certificate:", err)
		} else {
			headers["device"] = certificate
		}
	}
	return headers
}

func (s *Service) privateKey() (ed25519.PrivateKey, error) {
	if s.key != nil {
		return s.key, nil
	}
	return keychainPrivateKey()
}

// update builds the current hub/service/update payload.
func (s *Service) update() serviceUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := make([]string, 0, len(s.paths))
	for path := range s.paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	services := make([]ServiceHeader, 0, len(paths))
	for _, path := range paths {
		settings := s.settings[path]
		services = append(services, ServiceHeader{Path: path, Permissions: settings.Permissions, Limit: settings.Limit})
	}
	return serviceUpdate{Services: services, Apps: s.apps}
}

func (s *Service) senderOrError() (*channelgo.Sender, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sender == nil {
		return nil, errNotStarted
	}
	return s.sender, nil
}

func (s *Service) sendUpdates(sender *channelgo.Sender) {
	if s.profile != nil {
		sender.SendUntracked("hub/profile/update", s.profile)
	}
	sender.SendUntracked("hub/service/update", s.update())
}

func (s *Service) addPath(path string, settings []ApiSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths[path] = struct{}{}
	if len(settings) > 0 {
		st := settings[0]
		if st.Permissions != nil && st.Permissions.Group == "" {
			st.Permissions.Group = strings.Split(path, "/")[0]
		}
		s.settings[path] = st
	}
}

// ValuesNext is a small helper for iterating *channelgo.Values.
func ValuesNext(ctx context.Context, values *channelgo.Values) (any, bool, error) {
	item, err := values.Next(ctx)
	if err != nil {
		return nil, false, err
	}
	return item.Value, item.Done, nil
}
