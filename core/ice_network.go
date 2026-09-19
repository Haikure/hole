package core

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pion/transport/v4"
)

// Implements Pion's network boundary without creating stdnet (which enumerates
// netlink on Android). Every socket/DNS operation stays on the pinned Platform.
type iceNetwork struct {
	ctx      context.Context
	platform Platform
	facts    ICEPlatform
	mu       sync.Mutex
	sockets  []net.PacketConn
}

// rawSocket is the part of *net.UDPConn that quic-go tunes: the DF bit through
// the raw handle, and the kernel buffers.
type rawSocket interface {
	SyscallConn() (syscall.RawConn, error)
	SetReadBuffer(int) error
	SetWriteBuffer(int) error
}

func newICENetwork(ctx context.Context, p Platform) (*iceNetwork, error) {
	facts, ok := p.(ICEPlatform)
	if !ok {
		return nil, &Fault{Code: "ice_network_adapter_missing", Message: "当前平台缺少 ICE 网络适配"}
	}
	return &iceNetwork{ctx: ctx, platform: p, facts: facts}, nil
}
func (n *iceNetwork) ListenPacket(network, address string) (net.PacketConn, error) {
	pc, err := n.platform.ListenPacket(n.ctx, network, address)
	if err == nil {
		n.track(pc)
	}
	return pc, err
}

// track remembers every UDP socket this agent opens so a direct-phase QUIC
// connection can tune all of them.
func (n *iceNetwork) track(pc net.PacketConn) {
	if _, ok := pc.(rawSocket); !ok {
		return
	}
	n.mu.Lock()
	n.sockets = append(n.sockets, pc)
	n.mu.Unlock()
}

// socketsFor returns every tracked socket with the one bound to base last:
// quic-go judges DF support and buffer sizes by the last socket its control
// callback touched, and that should be the one carrying the data.
func (n *iceNetwork) socketsFor(base string) []rawSocket {
	n.mu.Lock()
	defer n.mu.Unlock()
	var selected rawSocket
	result := make([]rawSocket, 0, len(n.sockets))
	for _, pc := range n.sockets {
		socket := pc.(rawSocket)
		if selected == nil && base != "" && pc.LocalAddr() != nil && pc.LocalAddr().String() == base {
			selected = socket
			continue
		}
		result = append(result, socket)
	}
	if selected != nil {
		result = append(result, selected)
	}
	return result
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
	pc, err := l.n.platform.ListenPacket(ctx, network, address)
	if err == nil {
		l.n.track(pc)
	}
	return pc, err
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

// quic-go starts path MTU discovery only after setting the DF bit through the
// PacketConn's SyscallConn, and sizes kernel buffers through SetReadBuffer /
// SetWriteBuffer. The ICE Conn has no socket of its own, so a direct-phase
// transport hands quic-go every UDP socket the agent opened; the selected
// host or server-reflexive candidate lives on one of them.
type mtuProbingConn struct {
	*icePacketConn
	sockets []rawSocket
}

func (c *mtuProbingConn) SyscallConn() (syscall.RawConn, error) {
	raw := make(fanOutRawConn, 0, len(c.sockets))
	for _, socket := range c.sockets {
		if r, err := socket.SyscallConn(); err == nil {
			raw = append(raw, r)
		}
	}
	if len(raw) == 0 {
		return nil, errors.New("ICE transport has no UDP socket")
	}
	return raw, nil
}
func (c *mtuProbingConn) SetReadBuffer(bytes int) error {
	var err error
	for _, socket := range c.sockets {
		if e := socket.SetReadBuffer(bytes); e != nil {
			err = e
		}
	}
	return err
}
func (c *mtuProbingConn) SetWriteBuffer(bytes int) error {
	var err error
	for _, socket := range c.sockets {
		if e := socket.SetWriteBuffer(bytes); e != nil {
			err = e
		}
	}
	return err
}

type fanOutRawConn []syscall.RawConn

// Control applies f to each socket. A socket that was already closed does
// not stop the others from being configured.
func (s fanOutRawConn) Control(f func(fd uintptr)) error {
	for _, raw := range s {
		_ = raw.Control(f)
	}
	return nil
}
func (fanOutRawConn) Read(func(fd uintptr) bool) error  { return errors.ErrUnsupported }
func (fanOutRawConn) Write(func(fd uintptr) bool) error { return errors.ErrUnsupported }
