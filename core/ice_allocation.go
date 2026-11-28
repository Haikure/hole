package core

import (
	"context"
	"net"
	"os"
	"sync"
	"time"

	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/deadline"
)

func (n *iceNetwork) listenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	if n.allocation != nil {
		release, err := n.allocation.acquire(ctx)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	return n.platform.ListenPacket(ctx, network, address)
}

func (n *iceNetwork) guardAllocation(pc net.PacketConn) net.PacketConn {
	if n.allocation == nil {
		return pc
	}
	ctx, cancel := context.WithCancel(n.ctx)
	c := &allocationPacketConn{PacketConn: pc, ctx: ctx, cancel: cancel, gate: n.allocation, destinations: make(map[string]bool), writeDeadline: deadline.New()}
	if udp, ok := pc.(transport.UDPConn); ok {
		return &allocationUDPConn{UDPConn: udp, guard: c}
	}
	return c
}

// A new destination can allocate a NAT mapping even on an existing socket.
// Serialize only that first write with prediction; never retain the device
// allocator while waiting for the remote ICE/QUIC handshake. Known endpoints
// keep carrying traffic while another peer reserves the allocator.
type allocationPacketConn struct {
	net.PacketConn
	ctx           context.Context
	cancel        context.CancelFunc
	gate          *natAllocationGate
	mu            sync.Mutex
	destinations  map[string]bool
	writeDeadline *deadline.Deadline
}

func (c *allocationPacketConn) write(to net.Addr, send func() (int, error)) (int, error) {
	key := ""
	if to != nil {
		key = to.String()
	}
	c.mu.Lock()
	known := c.destinations[key]
	c.mu.Unlock()
	if known {
		return send()
	}
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	// Deadline.Context follows extensions and clearing of an unexpired deadline.
	// Socket deadlines alone cannot interrupt a wait before the socket write.
	writeCtx := c.writeDeadline.Context()
	ctx, cancel := context.WithCancel(writeCtx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer func() { stop(); cancel() }()
	release, err := c.gate.acquire(ctx)
	if err != nil {
		if c.ctx.Err() != nil {
			return 0, c.ctx.Err()
		}
		if writeCtx.Err() != nil {
			return 0, &net.OpError{Op: "write", Net: c.LocalAddr().Network(), Source: c.LocalAddr(), Addr: to, Err: os.ErrDeadlineExceeded}
		}
		return 0, err
	}
	defer release()
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := send()
	if err == nil {
		c.mu.Lock()
		c.destinations[key] = true
		c.mu.Unlock()
	}
	return n, err
}

func (c *allocationPacketConn) WriteTo(b []byte, to net.Addr) (int, error) {
	return c.write(to, func() (int, error) { return c.PacketConn.WriteTo(b, to) })
}

func (c *allocationPacketConn) Close() error {
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.PacketConn.Close()
	c.writeDeadline.Set(time.Time{})
	return err
}

func (c *allocationPacketConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.PacketConn.SetDeadline(t); err != nil {
		return err
	}
	c.writeDeadline.Set(t)
	return nil
}

func (c *allocationPacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.PacketConn.SetWriteDeadline(t); err != nil {
		return err
	}
	c.writeDeadline.Set(t)
	return nil
}

type allocationUDPConn struct {
	transport.UDPConn
	guard *allocationPacketConn
}

func (c *allocationUDPConn) Close() error                       { return c.guard.Close() }
func (c *allocationUDPConn) SetDeadline(t time.Time) error      { return c.guard.SetDeadline(t) }
func (c *allocationUDPConn) SetWriteDeadline(t time.Time) error { return c.guard.SetWriteDeadline(t) }

func (c *allocationUDPConn) Write(b []byte) (int, error) {
	return c.guard.write(c.RemoteAddr(), func() (int, error) { return c.UDPConn.Write(b) })
}

func (c *allocationUDPConn) WriteTo(b []byte, to net.Addr) (int, error) {
	return c.guard.WriteTo(b, to)
}

func (c *allocationUDPConn) WriteToUDP(b []byte, to *net.UDPAddr) (int, error) {
	return c.guard.write(to, func() (int, error) { return c.UDPConn.WriteToUDP(b, to) })
}

func (c *allocationUDPConn) WriteMsgUDP(b, oob []byte, to *net.UDPAddr) (n, oobn int, err error) {
	var destination net.Addr = to
	if to == nil {
		destination = c.RemoteAddr()
	}
	n, err = c.guard.write(destination, func() (int, error) {
		var written int
		written, oobn, err = c.UDPConn.WriteMsgUDP(b, oob, to)
		return written, err
	})
	return n, oobn, err
}
