package core

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"syscall"
)

const APIVersion = 1

// Fault is safe to display after the Engine has redacted its current secrets.
type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *Fault) Error() string { return f.Code + ": " + f.Message }

var (
	ErrClosed  = &Fault{Code: "engine_closed", Message: "Engine 已销毁"}
	ErrRunning = &Fault{Code: "already_running", Message: "Engine 正在运行；请使用配置重配接口"}
)

// Event delivery is bounded and nonblocking. A dropped event never changes the
// authoritative Snapshot. Hosts should refresh snapshots after events and on a
// low-frequency timer. 64-bit values are decimal strings in every JSON API.
type Event struct {
	Kind                string `json:"kind"`
	Sequence            uint64 `json:"sequence,string"`
	Generation          uint64 `json:"generation,string"`
	Time                string `json:"time"`
	State               string `json:"state,omitempty"`
	MappingID           string `json:"mapping_id,omitempty"`
	Protocol            string `json:"protocol,omitempty"`
	Message             string `json:"message,omitempty"`
	Error               *Fault `json:"error,omitempty"`
	Peer                string `json:"peer,omitempty"`
	Path                string `json:"path,omitempty"`
	Profile             string `json:"profile,omitempty"`
	Phase               string `json:"phase,omitempty"`
	TransportGeneration uint64 `json:"transport_generation,string,omitempty"`
	SessionID           string `json:"session_id,omitempty"`
	Target              string `json:"target,omitempty"`
	Stage               string `json:"stage,omitempty"`
	Resume              bool   `json:"resume,omitempty"`
}

type MappingSnapshot struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	Protocol        string `json:"protocol,omitempty"`
	State           string `json:"state"`
	TCPSessions     uint64 `json:"tcp_sessions,string"`
	UDPSessions     uint64 `json:"udp_sessions,string"`
	Error           *Fault `json:"error,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	Peer            string `json:"peer,omitempty"`
	Path            string `json:"path,omitempty"`
	TCPReadBytes    uint64 `json:"tcp_read_bytes,string"`
	TCPWrittenBytes uint64 `json:"tcp_written_bytes,string"`
	// ReadBytes/WrittenBytes are protocol-agnostic mapping totals. The legacy
	// TCP fields remain for clients that show TCP replay details.
	ReadBytes    uint64 `json:"read_bytes,string"`
	WrittenBytes uint64 `json:"written_bytes,string"`
	ReplayBytes  uint64 `json:"replay_bytes,string"`
	Profile      string `json:"profile,omitempty"`
}

type Capabilities struct {
	Transport         string `json:"transport"`
	RequiresIPv6      bool   `json:"requires_ipv6"`
	LiveConfiguration bool   `json:"live_configuration"`
	NetworkBinding    bool   `json:"network_binding"`
}

type Snapshot struct {
	APIVersion          int                     `json:"api_version"`
	SessionProtocol     int                     `json:"session_protocol"`
	CoreVersion         string                  `json:"core_version"`
	Configured          bool                    `json:"configured"`
	RunRequested        bool                    `json:"run_requested"`
	EngineState         string                  `json:"engine_state"`
	SignalState         string                  `json:"signal_state"`
	Generation          uint64                  `json:"generation,string"`
	EventsDropped       uint64                  `json:"events_dropped,string"`
	Mappings            []MappingSnapshot       `json:"mappings"`
	Error               *Fault                  `json:"error,omitempty"`
	Capabilities        Capabilities            `json:"capabilities"`
	TransportGeneration uint64                  `json:"transport_generation,string"`
	NetworkChanges      uint64                  `json:"network_changes,string"`
	Reconnects          uint64                  `json:"reconnects,string"`
	StartedAt           string                  `json:"started_at,omitempty"`
	Network             NetworkSnapshot         `json:"network"`
	PeerTransports      []PeerTransportSnapshot `json:"peer_transports"`
}

// CoreVersion can be set by the CLI/AAR build using -ldflags -X.
var CoreVersion = "development"

func classifyError(err error) *Fault {
	var fault *Fault
	if errors.As(err, &fault) {
		copy := *fault
		return &copy
	}
	code := "network_error"
	if errors.Is(err, syscall.EADDRINUSE) {
		code = "address_in_use"
	}
	return &Fault{Code: code, Message: err.Error()}
}

func redact(text string, cfg Config) string {
	for _, secret := range []string{cfg.Password, cfg.Token, cfg.TURN.Credential, cfg.TURN.Username} {
		if secret != "" {
			for _, spelling := range []string{url.QueryEscape(secret), url.PathEscape(secret), secret} {
				text = strings.ReplaceAll(text, spelling, "[redacted]")
			}
		}
	}
	const maxLogBytes = 4096
	if len(text) > maxLogBytes {
		text = string([]rune(text[:maxLogBytes])) + "…"
	}
	return text
}

func displayServer(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid URL]"
	}
	return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.EscapedPath())
}
