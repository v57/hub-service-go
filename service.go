// Package hubservice lets a Go process connect to a hub (hub-lite/hub-pro),
// host post/stream API endpoints, and call API through the hub.
//
// Port of hub-service (TS reference). Every (re)connect sends a fresh hub-pro
// auth token from the keychain (see keychain.go); hub-lite ignores it.
package hubservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

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
	// Sign returns the auth token sent on every connect. Default: Sign
	// (the keychain's `auth` file).
	Sign func() (string, error)
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
	paths    map[string]struct{}
	settings map[string]ApiSettings
	sender   *channelgo.Sender
	sign     func() (string, error)
	mu       sync.Mutex
}

// New creates a Service. Options are optional; the first one wins.
func New(options ...ServiceOptions) *Service {
	opts := ServiceOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	s := &Service{
		ch:       channelgo.NewChannel(),
		apps:     []AppHeader{},
		paths:    map[string]struct{}{},
		settings: map[string]ApiSettings{},
		sign:     opts.Sign,
	}
	if s.sign == nil {
		s.sign = Sign
	}
	if opts.Address != nil {
		s.address = opts.Address
	} else if env := os.Getenv("HUB"); env != "" {
		s.address = env
	} else {
		s.address = 1997
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
	if s.sender != nil {
		return s
	}
	sender := client.Connect(s.ch, s.address, client.Options{
		Headers: s.authHeaders,
		OnConnect: func(sender *channelgo.Sender) {
			s.sendUpdates(sender)
		},
	})
	s.sender = sender.Sender
	return s
}

// authHeaders signs a fresh token for each connect. If signing fails the
// connection goes out without auth: hub-lite accepts it, hub-pro treats it as
// an anonymous connection.
func (s *Service) authHeaders() map[string]string {
	token, err := s.sign()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hub-service: auth token:", err)
		return nil
	}
	return map[string]string{"auth": token, "v": authVersion}
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
