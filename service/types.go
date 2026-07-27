// Package service defines the Hub service registration and profile types used
// when a service connects to Hub. Types mirror the Bun hub-service wire format
// so a Go service registers equivalently.
package service

// Permissions declares the RPC access policy for a registered service path.
type Permissions struct {
	// Group scopes the permission under a named permission group. Defaults to
	// the first path segment when empty.
	Group string `json:"group,omitempty"`
	// Name is the permission name required to call the service path.
	Name string `json:"name"`
	// Allows grants the permission by default when true.
	Allows bool `json:"allows,omitempty"`
}

// Settings carries per-path registration settings.
type Settings struct {
	Permissions *Permissions `json:"permissions,omitempty"`
	Limit       int          `json:"limit,omitempty"`
}

// Header is a single service descriptor sent to Hub on connect/reconnect.
type Header struct {
	Path        string       `json:"path"`
	Permissions *Permissions `json:"permissions,omitempty"`
	Limit       int          `json:"limit,omitempty"`
}

// AppHeader registers an app route surfaced by the owning service.
type AppHeader struct {
	Type string `json:"type"` // always "app"
	Name string `json:"name"`
	Path string `json:"path"`
}

// Profile is the optional service display profile pushed to Hub on connect.
type Profile struct {
	Name string `json:"name,omitempty"`
	Icon any    `json:"icon,omitempty"`
}

// UpdateMessage is the body of the `hub/service/update` request.
type UpdateMessage struct {
	Services []Header    `json:"services"`
	Apps     []AppHeader `json:"apps"`
}

// ProfileUpdateMessage is the body of the `hub/profile/update` request.
type ProfileUpdateMessage struct {
	Name string `json:"name,omitempty"`
	Icon any    `json:"icon,omitempty"`
}
