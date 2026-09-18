package core

import (
	"context"
	"net"
	"time"
)

// Platform is the external-network boundary. Local application listeners never
// use it. Android network selection can be implemented without replacing the
// session protocols. Implementations must honor cancellation and return sockets
// owned by Core; a platform must not close a borrowed socket descriptor.
type Platform interface {
	Candidates(context.Context, Config, int) ([]Candidate, error)
	ListenPacket(context.Context, string, string) (net.PacketConn, error)
	DialSignal(context.Context, string, string) (net.Conn, error)
	DialTCP(context.Context, string) (*net.TCPConn, error)
	ResolveUDP(context.Context, string) (*net.UDPAddr, error)
	DialUDP(context.Context, *net.UDPAddr) (*net.UDPConn, error)
}

// ICE uses all allowed IPv4/IPv6 addresses, not the legacy public-IPv6 filter.
type ICEPlatform interface {
	InterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error)
	LookupIP(context.Context, string) ([]net.IP, error)
}

func (DefaultPlatform) InterfaceSnapshots(ctx context.Context) ([]InterfaceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []InterfaceSnapshot
	for _, i := range interfaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		s := InterfaceSnapshot{Name: i.Name, Up: i.Flags&net.FlagUp != 0, Loopback: i.Flags&net.FlagLoopback != 0}
		for _, a := range addrs {
			if ip := addrIP(a); ip != nil {
				s.Addresses = append(s.Addresses, ip.String())
			}
		}
		result = append(result, s)
	}
	return result, nil
}
func (DefaultPlatform) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// NetworkSnapshotter pins every external transport in a cycle to one Network.
// Session managers use the live Platform for new upstream sockets; already-open
// application sockets and loopback listeners are never rebound process-wide.
type NetworkSnapshotter interface {
	SnapshotNetwork(context.Context) (Platform, NetworkSnapshot, error)
	NetworkBinding() bool
}

type NetworkSnapshot struct {
	Handle    string   `json:"handle"`
	Transport string   `json:"transport"`
	Interface string   `json:"interface"`
	Addresses []string `json:"addresses"`
	DNS       []string `json:"dns"`
	Available bool     `json:"available"`
	Validated bool     `json:"validated"`
	Metered   bool     `json:"metered"`
}

func (n NetworkSnapshot) clone() NetworkSnapshot {
	n.Addresses = append([]string{}, n.Addresses...)
	n.DNS = append([]string{}, n.DNS...)
	return n
}

// DefaultPlatform retains the desktop socket and DNS behavior. It is not an
// Android Network binding implementation; that adapter lives in mobile/.
type DefaultPlatform struct{}

func (DefaultPlatform) Candidates(ctx context.Context, cfg Config, port int) ([]Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return globalIPv6Candidates(cfg, port)
}

func (DefaultPlatform) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	return (&net.ListenConfig{}).ListenPacket(ctx, network, address)
}

func (DefaultPlatform) DialSignal(ctx context.Context, network, address string) (net.Conn, error) {
	return dialWithFallback(ctx, network, address)
}

func (DefaultPlatform) DialTCP(ctx context.Context, address string) (*net.TCPConn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return conn.(*net.TCPConn), nil
}

func (DefaultPlatform) ResolveUDP(ctx context.Context, address string) (*net.UDPAddr, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) != nil {
		return net.ResolveUDPAddr("udp", address)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	// Match net.ResolveUDPAddr's default IPv4 preference when both families exist.
	selected := addresses[0]
	for _, address := range addresses {
		if address.IP.To4() != nil {
			selected = address
			break
		}
	}
	return net.ResolveUDPAddr("udp", net.JoinHostPort(selected.String(), port))
}

func (DefaultPlatform) DialUDP(ctx context.Context, address *net.UDPAddr) (*net.UDPConn, error) {
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "udp", address.String())
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}

type logFunc func(string, ...any)

func discardLog(string, ...any) {}
