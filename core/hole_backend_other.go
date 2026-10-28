//go:build !linux

package core

import "net"

func newForwardingBackend(conn net.PacketConn) forwardingBackend {
	return &portableForwarder{conn: conn}
}

func (f *holeForwarder) quicPacketConn() net.PacketConn { return f }
