package protocol

const WireVersion = 1

type Versioned struct {
	Version int `json:"version"`
}

type LocalTarget struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

type NetworkPolicy struct {
	Mode  string   `json:"mode"`
	Group string   `json:"group,omitempty"`
	Keys  []string `json:"keys,omitempty"`
}

type AgentAttestation struct {
	Version       int         `json:"version"`
	AttestationID string      `json:"attestationId"`
	ServiceKey    string      `json:"serviceKey"`
	AgentKey      string      `json:"agentKey"`
	ShareName     string      `json:"shareName"`
	Protocol      string      `json:"protocol"`
	Target        LocalTarget `json:"target"`
	IssuedAt      string      `json:"issuedAt"`
	ExpiresAt     string      `json:"expiresAt"`
	Signature     string      `json:"signature"`
}

type NetworkShare struct {
	Version     int              `json:"version"`
	Name        string           `json:"name"`
	Protocol    string           `json:"protocol"`
	Target      LocalTarget      `json:"target"`
	Policy      *NetworkPolicy   `json:"policy,omitempty"`
	Attestation AgentAttestation `json:"attestation"`
}

type NetworkSharePublication struct {
	Version        int    `json:"version"`
	ResourceID     string `json:"resourceId"`
	Authority      string `json:"authority"`
	ProviderID     string `json:"providerId"`
	Protocol       string `json:"protocol"`
	OverlayAddress string `json:"overlayAddress"`
	Port           uint16 `json:"port"`
}

type AgentProfile struct {
	Version          int      `json:"version"`
	ProfileKey       string   `json:"profileKey"`
	AgentKey         string   `json:"agentKey"`
	WireGuardKey     string   `json:"wireGuardKey"`
	Name             string   `json:"name,omitempty"`
	Platform         string   `json:"platform,omitempty"`
	OverlayAddresses []string `json:"overlayAddresses"`
	ConnectedAt      string   `json:"connectedAt"`
}

type ProfileBinding struct {
	Version          int      `json:"version"`
	BindingID        string   `json:"bindingId"`
	HubNetworkID     string   `json:"hubNetworkId"`
	ProfileKey       string   `json:"profileKey"`
	AgentKey         string   `json:"agentKey"`
	WireGuardKey     string   `json:"wireGuardKey"`
	OverlayAddresses []string `json:"overlayAddresses"`
	IssuedAt         string   `json:"issuedAt"`
	ExpiresAt        string   `json:"expiresAt"`
	Signature        string   `json:"signature"`
}

type ConnectionGrant struct {
	Version            int    `json:"version"`
	GrantID            string `json:"grantId"`
	HubKey             string `json:"hubKey"`
	HubNetworkID       string `json:"hubNetworkId"`
	CallerProfileKey   string `json:"callerProfileKey"`
	CallerWireGuardKey string `json:"callerWireGuardKey"`
	CallerAddress      string `json:"callerAddress"`
	TargetAgentKey     string `json:"targetAgentKey"`
	TargetWireGuardKey string `json:"targetWireGuardKey"`
	ResourceID         string `json:"resourceId"`
	ProviderID         string `json:"providerId"`
	Protocol           string `json:"protocol"`
	DestinationAddress string `json:"destinationAddress"`
	DestinationPort    uint16 `json:"destinationPort"`
	Generation         uint64 `json:"generation"`
	IssuedAt           string `json:"issuedAt"`
	ExpiresAt          string `json:"expiresAt"`
	Signature          string `json:"signature"`
}

type RendezvousTicket struct {
	Version        int      `json:"version"`
	TicketID       string   `json:"ticketId"`
	HubKey         string   `json:"hubKey"`
	HubNetworkID   string   `json:"hubNetworkId"`
	ProfileKey     string   `json:"profileKey"`
	AgentKey       string   `json:"agentKey"`
	NodeID         string   `json:"nodeId"`
	Roles          []string `json:"roles"`
	SessionPurpose string   `json:"sessionPurpose"`
	IssuedAt       string   `json:"issuedAt"`
	ExpiresAt      string   `json:"expiresAt"`
	Signature      string   `json:"signature"`
}

type Candidate struct {
	Version     int    `json:"version"`
	CandidateID string `json:"candidateId"`
	SessionID   string `json:"sessionId"`
	AgentKey    string `json:"agentKey"`
	Kind        string `json:"kind"`
	Transport   string `json:"transport"`
	Address     string `json:"address"`
	Port        uint16 `json:"port"`
	Priority    uint32 `json:"priority"`
	Foundation  string `json:"foundation,omitempty"`
	ExpiresAt   string `json:"expiresAt"`
}

// ObservedEndpoint is transport metadata measured by a rendezvous node. It is
// kept separate from client-declared candidates so callers can distinguish
// node observation from agent claims.
type ObservedEndpoint struct {
	Version   int    `json:"version"`
	AgentKey  string `json:"agentKey"`
	Transport string `json:"transport"`
	Address   string `json:"address"`
	Port      uint16 `json:"port"`
}

type NetworkResource struct {
	Version    int           `json:"version"`
	ResourceID string        `json:"resourceId"`
	OwnerKey   string        `json:"ownerKey"`
	ShareName  string        `json:"shareName"`
	Authority  string        `json:"authority"`
	Policy     NetworkPolicy `json:"policy"`
	Generation uint64        `json:"generation"`
}

type LiveProvider struct {
	Version        int    `json:"version"`
	ProviderID     string `json:"providerId"`
	ResourceID     string `json:"resourceId"`
	ServiceKey     string `json:"serviceKey"`
	AgentKey       string `json:"agentKey"`
	Protocol       string `json:"protocol"`
	OverlayAddress string `json:"overlayAddress"`
	Port           uint16 `json:"port"`
	ConnectedAt    string `json:"connectedAt"`
}

type NetworkError struct {
	Version   int            `json:"version"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type ConnectionStatus struct {
	Version    int           `json:"version"`
	ResourceID string        `json:"resourceId"`
	ProviderID string        `json:"providerId,omitempty"`
	Phase      string        `json:"phase"`
	Direct     bool          `json:"direct"`
	UpdatedAt  string        `json:"updatedAt"`
	Error      *NetworkError `json:"error,omitempty"`
}

type AgentStatus struct {
	Version    int           `json:"version"`
	AgentKey   string        `json:"agentKey"`
	ProfileKey string        `json:"profileKey"`
	Available  bool          `json:"available"`
	Addresses  []string      `json:"addresses"`
	Peers      int           `json:"peers"`
	Shares     int           `json:"shares"`
	UpdatedAt  string        `json:"updatedAt"`
	Error      *NetworkError `json:"error,omitempty"`
}

type SharesReplacePayload struct {
	Shares []NetworkShare `json:"shares"`
}

type SharesReplaceMessage struct {
	Version   int                  `json:"version"`
	Type      string               `json:"type"`
	RequestID string               `json:"requestId,omitempty"`
	Payload   SharesReplacePayload `json:"payload"`
}
