package core

import (
	"fmt"
	"time"
)

const natPunchVersion = "nat-punch-v1"
const natPunchInterval = 200 * time.Millisecond

// The knobs bound socket use, scan width and time independently of ordinary ICE.
type NATPunchConfig struct {
	Sockets          int            `yaml:"sockets" json:"sockets"`
	ScanWidth        int            `yaml:"scan_width" json:"scan_width"`
	DualSockets      int            `yaml:"dual_sockets" json:"dual_sockets"`
	DualRetrySockets int            `yaml:"dual_retry_sockets" json:"dual_retry_sockets"`
	Timeout          ConfigDuration `yaml:"timeout" json:"timeout"`
	RoundTimeout     ConfigDuration `yaml:"round_timeout" json:"round_timeout"`
}

func (c *NATPunchConfig) normalize() {
	if c.Sockets == 0 {
		c.Sockets = 10
	}
	if c.ScanWidth == 0 {
		c.ScanWidth = 20
	}
	if c.DualSockets == 0 {
		c.DualSockets = 32
	}
	if c.DualRetrySockets == 0 {
		c.DualRetrySockets = 64
	}
	if c.Timeout == 0 {
		c.Timeout = ConfigDuration(10 * time.Second)
	}
	if c.RoundTimeout == 0 {
		c.RoundTimeout = ConfigDuration(2500 * time.Millisecond)
	}
}

func (c NATPunchConfig) validate() error {
	if c.Sockets < 1 || c.Sockets > 64 || c.DualSockets < 1 || c.DualSockets > 64 || c.DualRetrySockets < c.DualSockets || c.DualRetrySockets > 64 || c.ScanWidth < 1 || c.ScanWidth > 256 {
		return fmt.Errorf("ice.punch: socket 数需为 1–64，重试不少于首轮，扫描宽度需为 1–256")
	}
	if c.Timeout.Duration() < time.Second || c.Timeout.Duration() > 30*time.Second || c.RoundTimeout.Duration() < 500*time.Millisecond || c.RoundTimeout > c.Timeout {
		return fmt.Errorf("ice.punch: timeout 需为 1s–30s，round_timeout 需为 500ms 到单轮完整时限")
	}
	return nil
}
