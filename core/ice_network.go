package core

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/transport/v4"
)

// Implements Pion's network boundary without creating stdnet (which enumerates
// netlink on Android). Every socket/DNS operation stays on the pinned Platform.
type iceNetwork struct {
	ctx      context.Context
	platform Platform
	facts    ICEPlatform
}

func newICENetwork(ctx context.Context, p Platform) (*iceNetwork, error) {
	facts, ok := p.(ICEPlatform)
	if !ok {
		return nil, &Fault{Code: "ice_network_adapter_missing", Message: "当前平台缺少 ICE 网络适配"}
	}
	return &iceNetwork{ctx: ctx, platform: p, facts: facts}, nil
}
func (n *iceNetwork) ListenPacket(network, address string) (net.PacketConn, error) {
	return n.platform.ListenPacket(n.ctx, network, address)
}
func (n *iceNetwork) ListenUDP(network string, local *net.UDPAddr) (transport.UDPConn, error) {
	if local == nil {
		local = &net.UDPAddr{}
	}
	pc, err := n.ListenPacket(network, local.String())
	if err != nil {
		return nil, err
	}
	conn, ok := pc.(transport.UDPConn)
	if !ok {
		pc.Close()
		return nil, errors.New("platform UDP socket lacks packet operations")
	}
	return conn, nil
}
func (n *iceNetwork) ListenTCP(string, *net.TCPAddr) (transport.TCPListener, error) {
	return nil, transport.ErrNotSupported
}
func (n *iceNetwork) Dial(network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
	defer cancel()
	return n.platform.DialSignal(ctx, network, address)
}
func (n *iceNetwork) DialUDP(network string, local, remote *net.UDPAddr) (transport.UDPConn, error) {
	if local != nil && !local.IP.IsUnspecified() {
		return nil, transport.ErrNotSupported
	}
	conn, err := n.Dial(network, remote.String())
	if err != nil {
		return nil, err
	}
	udp, ok := conn.(transport.UDPConn)
	if !ok {
		conn.Close()
		return nil, transport.ErrNotSupported
	}
	return udp, nil
}
func (n *iceNetwork) DialTCP(network string, local, remote *net.TCPAddr) (transport.TCPConn, error) {
	if local != nil && !local.IP.IsUnspecified() {
		return nil, transport.ErrNotSupported
	}
	conn, err := n.Dial(network, remote.String())
	if err != nil {
		return nil, err
	}
	tcp, ok := conn.(transport.TCPConn)
	if !ok {
		conn.Close()
		return nil, transport.ErrNotSupported
	}
	return tcp, nil
}
func (n *iceNetwork) resolve(network, host string) (net.IP, error) {
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		var err error
		ips, err = n.facts.LookupIP(n.ctx, host)
		if err != nil {
			return nil, err
		}
	}
	// Match net.Resolve{IP,UDP,TCP}Addr: unqualified lookups prefer IPv4,
	// falling back to IPv6 only when there is no A record. Pion TURN resolves
	// with "udp" even when ICE has already opened an IPv4-only UDP socket.
	// Choosing the first AAAA answer there makes Allocate fail before sending.
	if network == "ip" || network == "udp" || network == "tcp" {
		for _, ip := range ips {
			if ip.To4() != nil {
				return ip, nil
			}
		}
	}
	for _, ip := range ips {
		if strings.HasSuffix(network, "4") && ip.To4() == nil {
			continue
		}
		if strings.HasSuffix(network, "6") && ip.To4() != nil {
			continue
		}
		return ip, nil
	}
	return nil, &net.DNSError{Err: "no address in selected family", Name: host, IsNotFound: true}
}
func (n *iceNetwork) ResolveIPAddr(network, address string) (*net.IPAddr, error) {
	ip, e := n.resolve(network, address)
	return &net.IPAddr{IP: ip}, e
}
func (n *iceNetwork) ResolveUDPAddr(network, address string) (*net.UDPAddr, error) {
	host, p, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	port, e := strconv.Atoi(p)
	if e != nil {
		return nil, e
	}
	ip, e := n.resolve(network, host)
	return &net.UDPAddr{IP: ip, Port: port}, e
}
func (n *iceNetwork) ResolveTCPAddr(network, address string) (*net.TCPAddr, error) {
	a, e := n.ResolveUDPAddr(network, address)
	if e != nil {
		return nil, e
	}
	return &net.TCPAddr{IP: a.IP, Port: a.Port}, nil
}
func (n *iceNetwork) Interfaces() ([]*transport.Interface, error) {
	facts, e := n.facts.InterfaceSnapshots(n.ctx)
	if e != nil {
		return nil, e
	}
	result := make([]*transport.Interface, 0, len(facts))
	for i, f := range facts {
		flags := net.Flags(0)
		if f.Up {
			flags |= net.FlagUp | net.FlagRunning
		}
		if f.Loopback {
			flags |= net.FlagLoopback
		}
		iface := transport.NewInterface(net.Interface{Index: i + 1, MTU: 1500, Name: f.Name, Flags: flags})
		for _, raw := range f.Addresses {
			if ip := net.ParseIP(raw); ip != nil {
				iface.AddAddress(&net.IPAddr{IP: ip})
			}
		}
		result = append(result, iface)
	}
	return result, nil
}
func (n *iceNetwork) InterfaceByIndex(index int) (*transport.Interface, error) {
	all, e := n.Interfaces()
	if e != nil {
		return nil, e
	}
	for _, i := range all {
		if i.Index == index {
			return i, nil
		}
	}
	return nil, transport.ErrInterfaceNotFound
}
func (n *iceNetwork) InterfaceByName(name string) (*transport.Interface, error) {
	all, e := n.Interfaces()
	if e != nil {
		return nil, e
	}
	for _, i := range all {
		if i.Name == name {
			return i, nil
		}
	}
	return nil, transport.ErrInterfaceNotFound
}
func (n *iceNetwork) CreateDialer(*net.Dialer) transport.Dialer { return n }
func (n *iceNetwork) CreateListenConfig(*net.ListenConfig) transport.ListenConfig {
	return iceListenConfig{n}
}

type iceListenConfig struct{ n *iceNetwork }

func (l iceListenConfig) Listen(context.Context, string, string) (net.Listener, error) {
	return nil, transport.ErrNotSupported
}
func (l iceListenConfig) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	return l.n.platform.ListenPacket(ctx, network, address)
}

// Pion Conn is datagram-oriented despite its net.Conn-shaped methods. Keep a
// stable address pair for QUIC; the actual nominated ICE pair is telemetry.
// A short ReadFrom buffer consumes/truncates one datagram, never joins packets.
type icePacketConn struct {
	conn          net.Conn
	local, remote net.Addr
	readMu        sync.Mutex
	buffer        []byte
	closed        atomic.Bool
	closeOnce     sync.Once
	closeErr      error
}

func newICEPacketConn(conn net.Conn) *icePacketConn {
	return &icePacketConn{conn: conn, local: conn.LocalAddr(), remote: conn.RemoteAddr(), buffer: make([]byte, 65535)}
}
func (p *icePacketConn) ReadFrom(dst []byte) (int, net.Addr, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	if p.closed.Load() {
		return 0, nil, net.ErrClosed
	}
	n, e := p.conn.Read(p.buffer)
	if p.closed.Load() {
		return 0, nil, net.ErrClosed
	}
	return copy(dst, p.buffer[:n]), p.remote, e
}
func (p *icePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if p.closed.Load() {
		return 0, net.ErrClosed
	}
	if addr == nil || addr.String() != p.remote.String() {
		return 0, net.InvalidAddrError("ICE peer address mismatch")
	}
	return p.conn.Write(b)
}
func (p *icePacketConn) LocalAddr() net.Addr                { return p.local }
func (p *icePacketConn) SetDeadline(t time.Time) error      { return p.conn.SetDeadline(t) }
func (p *icePacketConn) SetReadDeadline(t time.Time) error  { return p.conn.SetReadDeadline(t) }
func (p *icePacketConn) SetWriteDeadline(t time.Time) error { return p.conn.SetWriteDeadline(t) }
func (p *icePacketConn) Close() error {
	p.closeOnce.Do(func() { p.closed.Store(true); p.closeErr = p.conn.Close() })
	return p.closeErr
}
