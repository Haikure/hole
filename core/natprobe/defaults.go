// SPDX-License-Identifier: GPL-2.0-or-later
package natprobe

import "time"

// DefaultTimeout includes DNS and all diagnostic probes. Connection attempts
// may impose a shorter parent deadline to leave time for the handshake.
const DefaultTimeout = 20 * time.Second

// DefaultServers returns a fresh list so callers cannot change shared defaults.
func DefaultServers() []string {
	return []string{
		"stun:stun.cloudflare.com:3478",
		"stun:stun.l.google.com:19302",
		"stun:stun.miwifi.com:3478",
		"stun:stun.chat.bilibili.com:3478",
		"stun:stun.hitv.com:3478",
		"stun:stun.voipgate.com:3478",
	}
}
