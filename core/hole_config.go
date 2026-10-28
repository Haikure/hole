package core

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"slices"
	"strings"
)

// HoleRelayVersion versions path planning and the authenticated UDP envelope.
const HoleRelayVersion = "hole-relay-v1"

// Preserve absent order through YAML export: nil migrates the old turn.order,
// while [] explicitly selects the new default order.
func (c RelayConfig) MarshalYAML() (any, error) {
	result := map[string]any{}
	if c.Device != "" {
		result["device"] = c.Device
	}
	if c.Order != nil {
		result["order"] = c.Order
	}
	if c.Serve.Enabled || c.Serve.MaxPairs != 0 || c.Serve.BandwidthMbps != 0 {
		result["serve"] = c.Serve
	}
	return result, nil
}

type RelayConfig struct {
	Device string `yaml:"device,omitempty" json:"device,omitempty"`
	// nil means migrate turn.order; an explicit empty array uses the new defaults.
	Order []string         `yaml:"order" json:"order"`
	Serve RelayServeConfig `yaml:"serve,omitempty" json:"serve,omitempty"`
}
type RelayServeConfig struct {
	Enabled       bool `yaml:"enabled" json:"enabled"`
	MaxPairs      int  `yaml:"max_pairs,omitempty" json:"max_pairs,omitempty"`
	BandwidthMbps int  `yaml:"bandwidth_mbps,omitempty" json:"bandwidth_mbps"`
}

func (c *RelayServeConfig) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("relay.serve 需要对象")
	}
	explicitLimit := false
	for i := 0; i < len(n.Content); i += 2 {
		key := n.Content[i].Value
		if !slices.Contains([]string{"enabled", "max_pairs", "bandwidth_mbps"}, key) {
			return fmt.Errorf("relay.serve 未知字段 %q", key)
		}
		if key == "max_pairs" {
			explicitLimit = true
		}
	}
	type plain RelayServeConfig
	if err := n.Decode((*plain)(c)); err != nil {
		return err
	}
	if explicitLimit && (c.MaxPairs < 1 || c.MaxPairs > 128) {
		return fmt.Errorf("relay.serve.max_pairs 需要 1–128")
	}
	return nil
}

func (c Config) validateHoleRelay() error {
	if c.Relay.Device != "" && (len(c.Relay.Device) > 128 || strings.Trim(c.Relay.Device, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") != "") {
		return fmt.Errorf("relay.device 需要有效的设备名（字母、数字、点、下划线或连字符，最多 128 字节）")
	}
	if c.Relay.Device == c.DeviceName && c.Relay.Device != "" {
		return fmt.Errorf("relay.device 不能是本机")
	}
	seen := map[string]bool{}
	for _, token := range c.Relay.Order {
		if !slices.Contains([]string{"hole", "udp", "tcp", "tls"}, token) || seen[token] {
			return fmt.Errorf("relay.order 只接受不重复的 hole、udp、tcp、tls")
		}
		seen[token] = true
	}
	if c.Relay.Serve.MaxPairs < 0 || c.Relay.Serve.MaxPairs > 128 {
		return fmt.Errorf("relay.serve.max_pairs 需要 1–128，省略时为 64")
	}
	if c.Relay.Serve.BandwidthMbps < 0 {
		return fmt.Errorf("relay.serve.bandwidth_mbps 需要非负整数")
	}
	if c.Relay.Serve.Enabled && c.Transport.FixedEndpoint == nil {
		return fmt.Errorf("relay.serve.enabled 需要 transport.fixed_endpoint")
	}
	if (c.Relay.Device != "" || c.Relay.Serve.Enabled || c.Relay.Order != nil) && c.Transport.Preferred != PreferredICE {
		return fmt.Errorf("Hole 中继需要 auto / ice 传输")
	}
	return nil
}

func (c Config) holeConfigured() bool { return c.Relay.Device != "" || c.Relay.Serve.Enabled }
func (c Config) relayServeLimit() int {
	if c.Relay.Serve.MaxPairs == 0 {
		return 64
	}
	return c.Relay.Serve.MaxPairs
}

// Expand TURN port subphases only on the wire. Explicit relay.order is the
// sole preference source, while absent order preserves old TURN behavior.
func (c Config) deviceRelayOrder(policy string) []string {
	if c.Relay.Order == nil {
		order := relayOrderForConfig(policy, c.TURN)
		if c.Relay.Device != "" {
			order = append([]string{"hole"}, order...)
		}
		return order
	}
	order := c.Relay.Order
	if len(order) == 0 {
		order = []string{"hole", "udp", "tcp", "tls"}
	}
	out := []string{}
	for _, token := range order {
		if token == "hole" {
			if c.Relay.Device != "" {
				out = append(out, token)
			}
			continue
		}
		turn := c.TURN
		turn.Order = []string{token}
		out = append(out, relayOrderForConfig(policy, turn)...)
	}
	return out
}

type relayPathStep struct {
	Device string `json:"device,omitempty"`
	A      string `json:"a"`
	B      string `json:"b"`
}

// One shared Hole step replaces every pair containing that device; TURN-only
// pairs retain their previous order. Names use byte ordering on both runtimes.
func deviceRelayPlan(a, b []string, aDevice, bDevice string) []relayPathStep {
	out := []relayPathStep{}
	seen := map[string]bool{}
	for _, pair := range relayPairs(a, b, true) {
		x, y := "", ""
		if pair[0] >= 0 {
			x = a[pair[0]]
		}
		if pair[1] >= 0 {
			y = b[pair[1]]
		}
		if x == "hole" || y == "hole" {
			names := []string{}
			if x == "hole" {
				names = append(names, aDevice)
			}
			if y == "hole" {
				names = append(names, bDevice)
			}
			slices.Sort(names)
			for _, name := range names {
				if name != "" && !seen[name] {
					seen[name] = true
					out = append(out, relayPathStep{Device: name, A: "hole", B: "hole"})
				}
			}
			continue
		}
		phase := func(t string) string {
			if t == "" {
				return "relay_wait"
			}
			return relayTypePhase(t)
		}
		out = append(out, relayPathStep{A: phase(x), B: phase(y)})
	}
	return out
}

func plannedPhase(m iceSignalMessage, round int, local string) string {
	if round == 0 {
		return "direct"
	}
	if round < 1 || round > len(m.RelayPlan) {
		return ""
	}
	step := m.RelayPlan[round-1]
	if local < m.PeerDevice {
		return step.A
	}
	return step.B
}
