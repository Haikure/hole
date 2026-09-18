package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/stun/v4"
	"github.com/pion/turn/v5"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestICEConfigCloudflareDefaultAndExplicitHostOnly(t *testing.T) {
	cfg := validConfig().normalized()
	if len(cfg.ICE.STUNURLs) != 1 || cfg.ICE.STUNURLs[0] != "stun:stun.cloudflare.com:3478" {
		t.Fatal(cfg.ICE.STUNURLs)
	}
	cfg.ICE.STUNURLs = []string{}
	copy := cfg.normalized()
	if len(copy.ICE.STUNURLs) != 0 {
		t.Fatal("explicit empty STUN list ignored")
	}
	r := engineRequest()
	r.ServerURL = "ws://127.0.0.1/ws"
	r.Config.Transport.Preferred = PreferredICE
	if r.Validate() == nil {
		t.Fatal("ICE accepted non-explicit plaintext signaling")
	}
	r.Config.Transport.AllowInsecureSignal = true
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Config.CandidateAddresses = []string{"2001:db8::1"}
	if r.Validate() == nil {
		t.Fatal("legacy pinned address silently reinterpreted for ICE")
	}
}
func TestICEPacketConnBoundariesDeadlinesAndConcurrentClose(t *testing.T) {
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := net.DialUDP("udp4", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	packet := newICEPacketConn(sender)
	defer packet.Close()
	if _, err = receiver.WriteToUDP([]byte("first-packet"), sender.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	_, _ = receiver.WriteToUDP([]byte("next"), sender.LocalAddr().(*net.UDPAddr))
	short := make([]byte, 3)
	n, _, err := packet.ReadFrom(short)
	if err != nil || n != 3 || string(short) != "fir" {
		t.Fatal(n, err, string(short))
	}
	buf := make([]byte, 20)
	n, _, err = packet.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "next" {
		t.Fatal("datagram boundary lost", err)
	}
	_ = packet.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	if _, _, err = packet.ReadFrom(buf); err == nil {
		t.Fatal("deadline ignored")
	}
	_ = packet.SetReadDeadline(time.Time{})
	done := make(chan error, 1)
	go func() { _, _, err := packet.ReadFrom(buf); done <- err }()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = packet.Close() }()
	}
	wg.Wait()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock packet read")
	}
}

func TestICETURNRelayUsesRealAllocation(t *testing.T)    { runRelayFixture(t, "udp") }
func TestICETURNPlainTCPRelayUsesActualTCP(t *testing.T) { runRelayFixture(t, "tcp") }

func runRelayFixture(t *testing.T, protocol string) {
	server, _ := actualWorkerFixture(t)
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	logger := logging.NewDefaultLoggerFactory()
	logger.DefaultLogLevel = logging.LogLevelDisabled
	logger.Writer = io.Discard
	serverConfig := turn.ServerConfig{Realm: "fixture", LoggerFactory: logger,
		AuthHandler: func(attr *turn.RequestAttributes) (string, []byte, bool) {
			if attr.Username != "user" {
				return "", nil, false
			}
			return attr.Username, turn.GenerateAuthKey("user", "fixture", "secret"), true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: socket, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.IPv4(127, 0, 0, 1), Address: "127.0.0.1"}}},
	}
	relayURL := fmt.Sprintf("turn:%s?transport=udp", socket.LocalAddr())
	if protocol != "udp" {
		socket.Close()
		var listener net.Listener
		var e error
		if protocol == "tls" {
			certificate, err := tls.LoadX509KeyPair(os.Getenv("HOLE_TLS_CERT"), os.Getenv("HOLE_TLS_KEY"))
			if err != nil {
				t.Fatal(err)
			}
			listener, e = tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
		} else {
			listener, e = net.Listen("tcp4", "127.0.0.1:0")
		}
		if e != nil {
			t.Fatal(e)
		}
		serverConfig.PacketConnConfigs = nil
		serverConfig.ListenerConfigs = []turn.ListenerConfig{{Listener: listener, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.IPv4(127, 0, 0, 1), Address: "127.0.0.1"}}}
		scheme := "turn"
		if protocol == "tls" {
			scheme = "turns"
		}
		relayURL = fmt.Sprintf("%s:%s?transport=tcp", scheme, listener.Addr())
	}
	relay, err := turn.NewServer(serverConfig)
	if err != nil {
		socket.Close()
		t.Fatal(err)
	}
	defer relay.Close()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	a, b := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	for _, r := range []*Request{&ra, &rb} {
		r.Config.ICE.RelayOnly = true
		r.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{relayURL}, Username: "user", Credential: "secret"}
	}
	port := unusedTCPPort(t)
	ra.Config.Provide = []Provide{{ID: "relay", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "relay", Expose: HostPort{"127.0.0.1", port}}}
	if err = a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err = b.Start(rb); err != nil {
		t.Fatal(err)
	}
	s := waitSnapshot(t, b, func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].PathType == "relay" && s.PeerTransports[0].ActiveChannels == 1
	})
	if s.PeerTransports[0].RelayProtocol != protocol || s.PeerTransports[0].RelayPolicy != RelayPolicyUDPTCPTLS {
		t.Fatal(s.PeerTransports)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte("through-turn"))
	got := make([]byte, 12)
	if _, err = io.ReadFull(conn, got); err != nil || string(got) != "through-turn" {
		t.Fatal("TURN did not carry QUIC/TCP", err, string(got))
	}
}

func TestICETURNSRelayUsesTrustedTLS(t *testing.T) {
	if os.Getenv("HOLE_TLS_CHILD") == "1" {
		runRelayFixture(t, "tls")
		return
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Local TURN fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, e := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	directory := t.TempDir()
	certFile, keyFile := filepath.Join(directory, "ca.pem"), filepath.Join(directory, "key.pem")
	if e = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); e != nil {
		t.Fatal(e)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestICETURNSRelayUsesTrustedTLS$", "-test.timeout=45s", "-test.v")
	child.Env = append(os.Environ(), "HOLE_TLS_CHILD=1", "HOLE_TLS_CERT="+certFile, "HOLE_TLS_KEY="+keyFile, "SSL_CERT_FILE="+certFile)
	if output, e := child.CombinedOutput(); e != nil {
		t.Fatalf("TLS relay fixture: %v\n%s", e, output)
	}
}

type ipv6OnlyFixture struct {
	DefaultPlatform
	address string
}

func (p ipv6OnlyFixture) InterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error) {
	return []InterfaceSnapshot{{Name: "local-fixture", Up: true, Addresses: []string{p.address}}}, nil
}
func TestICEIPv6OnlyHostConnection(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	facts, err := (DefaultPlatform{}).InterfaceSnapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	address := ""
	for _, f := range facts {
		if !f.Up {
			continue
		}
		for _, raw := range f.Addresses {
			if isPublicIPv6(net.ParseIP(raw)) {
				address = raw
				break
			}
		}
		if address != "" {
			break
		}
	}
	if address == "" {
		t.Skip("no local unicast IPv6 available for the local-only fixture")
	}
	platform := ipv6OnlyFixture{address: address}
	a, b := NewEngine(Options{Platform: platform, RetryNetwork: true}), NewEngine(Options{Platform: platform, RetryNetwork: true})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	ra.Config.ICE.InterfaceAllowlist = []string{"local-fixture"}
	rb.Config.ICE.InterfaceAllowlist = []string{"local-fixture"}
	ra.Config.Provide = []Provide{{ID: "v6", Service: ServiceEndpoint{"tcp", "127.0.0.1", 22}}}
	rb.Config.Consume = []Consume{{ID: "v6", Expose: HostPort{"127.0.0.1", unusedTCPPort(t)}}}
	if e := a.Start(ra); e != nil {
		t.Fatal(e)
	}
	if e := b.Start(rb); e != nil {
		t.Fatal(e)
	}
	waitSnapshot(t, b, func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].AddressFamily == "IPv6"
	})
}

func TestICEServerReflexiveCandidateUsesSTUN(t *testing.T) {
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	logger := logging.NewDefaultLoggerFactory()
	logger.DefaultLogLevel = logging.LogLevelDisabled
	logger.Writer = io.Discard
	server, err := turn.NewServer(turn.ServerConfig{Realm: "fixture", LoggerFactory: logger, PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: socket, RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.IPv4(127, 0, 0, 1), Address: "127.0.0.1"}}}})
	if err != nil {
		socket.Close()
		t.Fatal(err)
	}
	defer server.Close()
	source, err := newICENetwork(context.Background(), DefaultPlatform{})
	if err != nil {
		t.Fatal(err)
	}
	uri, err := stun.ParseURI("stun:" + socket.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ice.NewAgentWithOptions(ice.WithNet(source), ice.WithUrls([]*stun.URI{uri}), ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}), ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost, ice.CandidateTypeServerReflexive}), ice.WithIncludeLoopback(), ice.WithInterfaceFilter(func(name string) bool { return name == "lo" }), ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled), ice.WithLoggerFactory(logger), ice.WithSTUNGatherTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	done := make(chan struct{})
	var once sync.Once
	_ = agent.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			once.Do(func() { close(done) })
		}
	})
	if err = agent.GatherCandidates(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("STUN gather timeout")
	}
	candidates, err := agent.GetLocalCandidates()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range candidates {
		if c.Type() == ice.CandidateTypeServerReflexive {
			return
		}
	}
	t.Fatalf("no srflx candidate collected: %v", candidates)
}
