// SPDX-License-Identifier: GPL-3.0-only
package natprobe

import "time"

// DefaultTimeout includes DNS and all diagnostic probes. Connection attempts
// may impose a shorter parent deadline to leave time for the handshake.
const DefaultTimeout = 20 * time.Second

// MaxServers bounds NAT classification probe lists across the CLI and clients.
const MaxServers = 16

// DefaultServers returns a fresh list so callers cannot change shared defaults.
func DefaultServers() []string {
	return []string{
		"stun:stun.chat.bilibili.com:3478",
		"stun:stun.cloudflare.com:3478",
		"stun:stun.l.google.com:19302",
		"stun:stun.miwifi.com:3478",
		"stun:stun.hitv.com:3478",
		"stun:stun.voipgate.com:3478",
		"stun:sg1.stun.twilio.com:3478",
		"stun:jp1.stun.twilio.com:3478",
		"stun:stun.nextcloud.com:3478",
		"stun:stunserver2025.stunprotocol.org:3478",
	}
}
