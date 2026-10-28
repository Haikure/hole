package core

import (
	"errors"
	"net"
	"net/netip"
)

// Buffer sizing is safe to delegate without advertising raw/OOB capabilities:
// the QUIC connection must still read through the sole packet dispatcher.
func holeReadBuffer(conn net.PacketConn, n int) error {
	if setter, ok := conn.(interface{ SetReadBuffer(int) error }); ok {
		return setter.SetReadBuffer(n)
	}
	return errors.ErrUnsupported
}
func holeWriteBuffer(conn net.PacketConn, n int) error {
	if setter, ok := conn.(interface{ SetWriteBuffer(int) error }); ok {
		return setter.SetWriteBuffer(n)
	}
	return errors.ErrUnsupported
}

const holeBatchSize = 32

// Backend owns no routing, authorization or policy. The caller retains frame
// ownership until WriteBatch returns, including partial sends and errors.
type forwardingBackend interface {
	ReadBatch([]*holeFrame) (int, error)
	WriteBatch([]*holeFrame) (int, error)
	Name() string
}
type portableForwarder struct{ conn net.PacketConn }

func (b *portableForwarder) Name() string { return "datagram" }
func (b *portableForwarder) ReadBatch(frames []*holeFrame) (int, error) {
	if len(frames) == 0 {
		return 0, nil
	}
	if udp, ok := b.conn.(*net.UDPConn); ok {
		n, addr, err := udp.ReadFromUDPAddrPort(frames[0].data[:])
		if err != nil {
			return 0, err
		}
		frames[0].n, frames[0].address = n, canonicalHoleAddress(addr)
		return 1, nil
	}
	n, addr, err := b.conn.ReadFrom(frames[0].data[:])
	if err != nil {
		return 0, err
	}
	a, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, net.InvalidAddrError("UDP address required")
	}
	frames[0].n = n
	frames[0].address = a.AddrPort()
	return 1, nil
}
func (b *portableForwarder) WriteBatch(frames []*holeFrame) (int, error) {
	for i, f := range frames {
		if udp, ok := b.conn.(*net.UDPConn); ok {
			if _, err := udp.WriteToUDPAddrPort(f.data[:f.n], f.address); err != nil {
				return i, err
			}
			continue
		}
		if _, err := b.conn.WriteTo(f.data[:f.n], net.UDPAddrFromAddrPort(f.address)); err != nil {
			return i, err
		}
	}
	return len(frames), nil
}
func canonicalHoleAddress(a netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(a.Addr().Unmap(), a.Port())
}
