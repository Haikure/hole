package core

import (
	"encoding/json"
	"fmt"
	"github.com/pion/stun/v4"
	"gopkg.in/yaml.v3"
	"time"
)

const (
	PreferredIPv6        = "ipv6"
	PreferredICE         = "ice"
	ProfileLegacy        = "legacy-ipv6-quic-v2"
	ProfileICE           = "ice-quic-mux-v1"
	RelayPolicyUDPTCPTLS = "udp-tcp-tls-v1"
	iceALPN              = "hole-ice-mux-v1"
	maxPeerTransports    = 16
	maxPeerChannels      = 64
	maxICECandidates     = 64
)

// ConfigDuration has the same string representation in CLI YAML and mobile JSON.
type ConfigDuration time.Duration

func (d ConfigDuration) Duration() time.Duration      { return time.Duration(d) }
func (d ConfigDuration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }
func (d ConfigDuration) MarshalYAML() (any, error)    { return time.Duration(d).String(), nil }
func (d *ConfigDuration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = ConfigDuration(v)
	return err
}
func (d *ConfigDuration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = ConfigDuration(v)
	return err
}

type TransportConfig struct {
	Preferred           string `yaml:"preferred" json:"preferred"`
	AllowLegacy         bool   `yaml:"allow_legacy" json:"allow_legacy"`
	AllowInsecureSignal bool   `yaml:"allow_insecure_signal" json:"allow_insecure_signal"`
}
type ICEConfig struct {
	STUNURLs            []string       `yaml:"stun_urls" json:"stun_urls"`
	DirectProbeTimeout  ConfigDuration `yaml:"direct_probe_timeout" json:"direct_probe_timeout"`
	GatherTimeout       ConfigDuration `yaml:"gather_timeout" json:"gather_timeout"`
	ConnectivityTimeout ConfigDuration `yaml:"connectivity_timeout" json:"connectivity_timeout"`
	RetryMaxDelay       ConfigDuration `yaml:"retry_max_delay" json:"retry_max_delay"`
	InterfaceAllowlist  []string       `yaml:"interface_allowlist" json:"interface_allowlist"`
	IncludeLoopback     bool           `yaml:"include_loopback" json:"include_loopback"`
	RelayOnly           bool           `yaml:"relay_only" json:"relay_only"`
}
type TURNConfig struct {
	Mode       string         `yaml:"mode" json:"mode"`
	TTL        ConfigDuration `yaml:"ttl" json:"ttl"`
	URLs       []string       `yaml:"urls" json:"urls"`
	Username   string         `yaml:"username" json:"username"`
	Credential string         `yaml:"credential" json:"credential"`
}
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

func (c *Config) normalizeTransport() {
	if c.Transport.Preferred == "" {
		c.Transport.Preferred = PreferredIPv6
	}
	if c.ICE.STUNURLs == nil {
		c.ICE.STUNURLs = []string{"stun:stun.cloudflare.com:3478"}
	} else {
		c.ICE.STUNURLs = append([]string{}, c.ICE.STUNURLs...)
	}
	c.ICE.InterfaceAllowlist = append([]string(nil), c.ICE.InterfaceAllowlist...)
	if len(c.ICE.InterfaceAllowlist) == 0 {
		c.ICE.InterfaceAllowlist = append([]string(nil), c.CandidateInterfaces...)
	}
	if c.ICE.DirectProbeTimeout == 0 {
		c.ICE.DirectProbeTimeout = ConfigDuration(3 * time.Second)
	}
	if c.ICE.GatherTimeout == 0 {
		c.ICE.GatherTimeout = ConfigDuration(6 * time.Second)
	}
	if c.ICE.ConnectivityTimeout == 0 {
		c.ICE.ConnectivityTimeout = ConfigDuration(10 * time.Second)
	}
	if c.ICE.RetryMaxDelay == 0 {
		c.ICE.RetryMaxDelay = ConfigDuration(15 * time.Second)
	}
	c.TURN.URLs = append([]string{}, c.TURN.URLs...)
	if c.TURN.Mode == "" {
		c.TURN.Mode = "worker"
	}
	if c.TURN.TTL == 0 {
		c.TURN.TTL = ConfigDuration(6 * time.Hour)
	}
}
func (c Config) validateTransport() error {
	c.normalizeTransport()
	if c.Transport.Preferred != PreferredICE && c.Transport.Preferred != PreferredIPv6 {
		return fmt.Errorf("transport.preferred 只接受 ice 或 ipv6")
	}
	if c.Transport.Preferred == PreferredICE && len(c.CandidateAddresses) > 0 {
		return fmt.Errorf("candidate_addresses 仅用于 IPv6 直连；选择 ICE 时请移除此项，不自动转换为 NAT 地址映射")
	}
	if len(c.Provide)+len(c.Consume) > maxPeerChannels {
		return fmt.Errorf("每个设备最多启用 %d 条映射", maxPeerChannels)
	}
	for name, d := range map[string]ConfigDuration{"direct_probe_timeout": c.ICE.DirectProbeTimeout, "gather_timeout": c.ICE.GatherTimeout, "connectivity_timeout": c.ICE.ConnectivityTimeout, "retry_max_delay": c.ICE.RetryMaxDelay} {
		if d.Duration() < 100*time.Millisecond || d.Duration() > 2*time.Minute {
			return fmt.Errorf("ice.%s 需要 100ms 到 2m", name)
		}
	}
	if c.TURN.Mode != "worker" && c.TURN.Mode != "manual" && c.TURN.Mode != "off" {
		return fmt.Errorf("turn.mode 需要 worker、manual 或 off")
	}
	if c.TURN.TTL.Duration() < time.Minute || c.TURN.TTL.Duration() > 6*time.Hour {
		return fmt.Errorf("turn.ttl 需要 1m 到 6h")
	}
	if len(c.ICE.STUNURLs) > 8 || len(c.TURN.URLs) > 8 {
		return fmt.Errorf("STUN/TURN URL 每组最多 8 条")
	}
	for _, raw := range c.ICE.STUNURLs {
		u, e := stun.ParseURI(raw)
		if e != nil || u.Scheme != stun.SchemeTypeSTUN {
			return fmt.Errorf("ice.stun_urls 需要有效的 stun: URL")
		}
	}
	for _, raw := range c.TURN.URLs {
		u, e := stun.ParseURI(raw)
		if e != nil || (u.Scheme != stun.SchemeTypeTURN && u.Scheme != stun.SchemeTypeTURNS) {
			return fmt.Errorf("turn.urls 需要有效的 turn:/turns: URL")
		}
		if u.Scheme == stun.SchemeTypeTURNS && u.Proto != stun.ProtoTypeTCP {
			return fmt.Errorf("本版本 turns 使用 TCP/TLS")
		}
	}
	if c.TURN.Mode == "manual" && (len(c.TURN.URLs) == 0 || c.TURN.Username == "" || c.TURN.Credential == "") {
		return fmt.Errorf("手动 TURN 需要 URL、用户名和凭据")
	}
	return nil
}
