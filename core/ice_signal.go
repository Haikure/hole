package core

type peerMappingRecord struct {
	Key      string          `json:"map_key"`
	ID       string          `json:"mapping_id"`
	Provider string          `json:"provider_id"`
	Consumer string          `json:"consumer_id"`
	Service  ServiceEndpoint `json:"service"`
	Expose   HostPort        `json:"expose"`
}
type iceSignalMessage struct {
	SignalMessage
	SignalVersion       int                 `json:"signal_version,omitempty"`
	LeaseRenewal        bool                `json:"lease_renewal,omitempty"`
	Leases              []transportLease    `json:"leases,omitempty"`
	AuthMode            string              `json:"auth_mode,omitempty"`
	RuntimeID           string              `json:"runtime_id,omitempty"`
	PeerRuntimeID       string              `json:"peer_runtime_id,omitempty"`
	TransportEpoch      uint64              `json:"transport_epoch,string,omitempty"`
	TransportProfiles   []string            `json:"transport_profiles,omitempty"`
	SessionVersions     []int               `json:"session_versions,omitempty"`
	Profile             string              `json:"profile,omitempty"`
	SessionVersion      int                 `json:"session_version,omitempty"`
	RequestID           string              `json:"request_id,omitempty"`
	TransportID         string              `json:"transport_id,omitempty"`
	TransportGeneration uint64              `json:"transport_generation,string,omitempty"`
	ExpectedGeneration  uint64              `json:"expected_generation,string,omitempty"`
	InitiatorID         string              `json:"initiator_id,omitempty"`
	Phase               string              `json:"phase,omitempty"`
	RelayPolicy         string              `json:"relay_policy,omitempty"`
	ToPeerID            string              `json:"to_peer_id,omitempty"`
	Ufrag               string              `json:"ufrag,omitempty"`
	Pwd                 string              `json:"pwd,omitempty"`
	ICECandidate        string              `json:"candidate,omitempty"`
	EndOfCandidates     bool                `json:"end_of_candidates,omitempty"`
	Mappings            []peerMappingRecord `json:"mappings,omitempty"`
	LeaseUntil          int64               `json:"lease_until,string,omitempty"`
	ExpiresAt           int64               `json:"expire_at,string,omitempty"`
	RefreshAt           int64               `json:"refresh_at,string,omitempty"`
	TTLSecs             int                 `json:"ttl,omitempty"`
	ICEServers          []ICEServer         `json:"ice_servers,omitempty"`
	RetryAfterMS        int                 `json:"retry_after_ms,omitempty"`
}

type transportLease struct {
	TransportID string `json:"transport_id"`
	Generation  uint64 `json:"transport_generation,string"`
	Until       int64  `json:"lease_until,string"`
}

type PeerTransportSnapshot struct {
	PeerID      string `json:"peer_id"`
	TransportID string `json:"transport_id"`
	Generation  uint64 `json:"generation,string"`
	Profile     string `json:"profile"`
	State       string `json:"state"`
	Phase       string `json:"phase"`
	PathType    string `json:"path_type,omitempty"`
	LocalType   string `json:"local_type,omitempty"`
	RemoteType  string `json:"remote_type,omitempty"`
	// LocalRelayProtocol is the TURN access protocol used by this process.
	// RelayProtocol is kept as a compatibility alias for older hosts.
	LocalRelayProtocol  string `json:"local_relay_protocol,omitempty"`
	LocalAddress        string `json:"local_address,omitempty"`
	RemoteAddress       string `json:"remote_address,omitempty"`
	RelayProtocol       string `json:"relay_protocol,omitempty"`
	RemoteRelayProtocol string `json:"remote_relay_protocol,omitempty"`
	RelaySide           string `json:"relay_side,omitempty"`
	RelayPolicy         string `json:"relay_policy,omitempty"`
	AddressFamily       string `json:"address_family,omitempty"`
	LocalCandidates     int    `json:"local_candidates"`
	RemoteCandidates    int    `json:"remote_candidates"`
	MappingCount        int    `json:"mapping_count"`
	ActiveChannels      int    `json:"active_channels"`
	ConnectMS           int64  `json:"connect_ms,string"`
	BytesSent           uint64 `json:"bytes_sent,string"`
	BytesReceived       uint64 `json:"bytes_received,string"`
	DroppedDatagrams    uint64 `json:"dropped_datagrams,string"`
	RetryCount          uint64 `json:"retry_count,string"`
	LeaseUntil          int64  `json:"lease_until,string"`
	TURNExpiresAt       int64  `json:"turn_expires_at,string"`
	RelayState          string `json:"relay_state,omitempty"`
	Error               *Fault `json:"error,omitempty"`
	RTTMS               int64  `json:"rtt_ms,string"`
	PendingPhase        string `json:"pending_phase,omitempty"`
}
