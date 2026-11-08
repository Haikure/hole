package core

import (
	"net"
	"sync"
	"time"

	"github.com/pion/stun/v4"
	"github.com/pion/transport/v5"
)

type iceCheckPacer struct {
	mu    sync.Mutex
	last  map[string]time.Time
	first chan struct{}
	once  sync.Once
}

func newICECheckPacer() *iceCheckPacer {
	return &iceCheckPacer{last: map[string]time.Time{}, first: make(chan struct{})}
}

func (n *iceNetwork) paced(pc net.PacketConn) net.PacketConn {
	if n.checkPace != nil {
		if udp, ok := pc.(transport.UDPConn); ok {
			return &pacedICEPacket{UDPConn: udp, pace: n.checkPace}
		}
	}
	return pc
}

type pacedICEPacket struct {
	transport.UDPConn
	pace *iceCheckPacer
}

// Trickle candidates can force extra Pion checklist passes. Limit requests
// on each actual socket/target to 200 ms; STUN responses and QUIC are untouched.
func (p *pacedICEPacket) WriteTo(b []byte, addr net.Addr) (int, error) {
	check := false
	if stun.IsMessage(b) {
		m := &stun.Message{Raw: b}
		check = m.Decode() == nil && m.Type == stun.BindingRequest && m.Contains(stun.AttrUsername)
	}
	if check {
		key := p.LocalAddr().String() + "/" + addr.String()
		p.pace.mu.Lock()
		now := time.Now()
		if now.Sub(p.pace.last[key]) < natPunchInterval {
			p.pace.mu.Unlock()
			return len(b), nil
		}
		p.pace.last[key] = now
		p.pace.mu.Unlock()
	}
	n, err := p.UDPConn.WriteTo(b, addr)
	if check && err == nil {
		p.pace.once.Do(func() { close(p.pace.first) })
	}
	return n, err
}

func (p *pacedICEPacket) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return p.WriteTo(b, addr)
}
