//go:build linux

package core

import (
	"net"
	"syscall"

	"github.com/quic-go/quic-go"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

// Preserve real pktinfo, ECN and send-side GSO on the ordinary fixed QUIC
// path. ReadBatch must be supplied explicitly: otherwise x/net unwraps the
// raw descriptor and creates a competing reader behind the dispatcher.
type holeQUICPacketConn struct {
	*holeForwarder
	udp *net.UDPConn
}

func (f *holeForwarder) quicPacketConn() net.PacketConn {
	if udp, ok := f.conn.(*net.UDPConn); ok {
		return &holeQUICPacketConn{f, udp}
	}
	return f
}
func (p *holeQUICPacketConn) SyscallConn() (syscall.RawConn, error) { return p.udp.SyscallConn() }
func (p *holeQUICPacketConn) WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (int, int, error) {
	return p.udp.WriteMsgUDP(b, oob, addr)
}
func (p *holeQUICPacketConn) ReadMsgUDP(b, oob []byte) (int, int, int, *net.UDPAddr, error) {
	frame, err := p.takeNormal()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	n, on, flags, addr := copy(b, frame.data[:frame.n]), copy(oob, frame.oob[:frame.oobn]), frame.flags, net.UDPAddrFromAddrPort(frame.address)
	if n < frame.n {
		flags |= unix.MSG_TRUNC
	}
	if on < frame.oobn {
		flags |= unix.MSG_CTRUNC
	}
	p.free <- frame
	return n, on, flags, addr, nil
}
func (p *holeQUICPacketConn) ReadBatch(messages []ipv4.Message, _ int) (int, error) {
	for i := range messages {
		var frame *holeFrame
		if i == 0 {
			var err error
			frame, err = p.takeNormal()
			if err != nil {
				return 0, err
			}
		} else {
			select {
			case frame = <-p.normal:
			default:
				return i, nil
			}
		}
		m := &messages[i]
		offset := 0
		for _, buffer := range m.Buffers {
			offset += copy(buffer, frame.data[offset:frame.n])
		}
		m.N, m.NN, m.Flags, m.Addr = offset, copy(m.OOB, frame.oob[:frame.oobn]), frame.flags, net.UDPAddrFromAddrPort(frame.address)
		if m.N < frame.n {
			m.Flags |= unix.MSG_TRUNC
		}
		if m.NN < frame.oobn {
			m.Flags |= unix.MSG_CTRUNC
		}
		p.free <- frame
	}
	return len(messages), nil
}

var _ quic.OOBCapablePacketConn = (*holeQUICPacketConn)(nil)
