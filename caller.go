package hubservice

// Caller identity (Docs/Devices/protocol.md section 4). hub-pro replaces any
// client-supplied `context.hub` and forwards each request with
// `{ "hub": { "caller": {...}, "hubKey": "<hub SPKI>" } }`. Services only take
// requests from their hub connection, so they can trust it.

import (
	"context"
	"encoding/json"

	"github.com/v57/channel-go/channelgo"
)

// Caller is who sent a request, as the hub reports it.
type Caller struct {
	// Key is the caller's service key (SPKI); empty for anonymous callers.
	Key string `json:"key,omitempty"`
	// Device is set when the caller's connection is attached to a device.
	Device *CallerDevice `json:"device,omitempty"`
	// Groups lists every group the key or its device belongs to.
	Groups []string `json:"groups,omitempty"`
	// Owner is true for members of the owners group.
	Owner bool `json:"owner,omitempty"`
	// Via is the merged hub a request came through; Claimed is that hub's
	// unverified description of its own caller.
	Via     string  `json:"via,omitempty"`
	Claimed *Caller `json:"claimed,omitempty"`
}

// CallerDevice is the device a caller's connection is attached to.
type CallerDevice struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// InGroup reports whether the caller belongs to group.
func (c *Caller) InGroup(group string) bool {
	if c == nil {
		return false
	}
	for _, g := range c.Groups {
		if g == group {
			return true
		}
	}
	return false
}

// Request is one call handed to a handler.
type Request struct {
	Body any
	// Caller is nil when the hub sent none (hub-lite, older hub-pro) or the
	// context's hub key isn't the pinned one.
	Caller *Caller
	// HubKey is the forwarding hub's key, when it sent one.
	HubKey string
	// Context is the request's whole context, including `service` and
	// `device` selection.
	Context any
}

// RequestFrom reads the hub's caller identity out of a request context. It
// doesn't check the hub key; PostRequest and StreamRequest do.
func RequestFrom(body any, contextValue any) Request {
	request := Request{Body: body, Context: contextValue}
	values, ok := contextValue.(map[string]any)
	if !ok {
		return request
	}
	hub, ok := values["hub"].(map[string]any)
	if !ok {
		return request
	}
	request.HubKey, _ = hub["hubKey"].(string)
	if raw, ok := hub["caller"]; ok && raw != nil {
		data, err := json.Marshal(raw)
		if err == nil {
			var caller Caller
			if json.Unmarshal(data, &caller) == nil {
				request.Caller = &caller
			}
		}
	}
	return request
}

// request is RequestFrom with the caller kept only when the context names the
// pinned hub key: hub-lite forwards a client's own context.hub unchanged, so
// an unpinned or other hub's caller can't be trusted.
func (s *Service) request(body channelgo.Body) Request {
	request := RequestFrom(body.Body, body.Context)
	if request.HubKey == "" || request.HubKey != s.HubKey() {
		request.Caller = nil
	}
	return request
}

// PostRequest registers a request/response API whose handler sees the
// caller. Caller is set only on hubs whose key is pinned (hub-pro).
func (s *Service) PostRequest(path string, handler func(request Request) (any, error), settings ...ApiSettings) *Service {
	s.addPath(path, settings)
	s.ch.Post(path, func(body channelgo.Body) (any, error) {
		return handler(s.request(body))
	})
	return s
}

// StreamRequest registers a streaming API whose handler sees the caller. Its
// ctx is cancelled when the caller cancels the stream.
func (s *Service) StreamRequest(path string, handler func(ctx context.Context, request Request, emit func(any) error) error, settings ...ApiSettings) *Service {
	s.addPath(path, settings)
	s.ch.Stream(path, func(body channelgo.Body, emit channelgo.StreamEmitter) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if body.Task != nil {
			body.Task.OnCancel(cancel)
		}
		return handler(ctx, s.request(body), emit.Emit)
	})
	return s
}
