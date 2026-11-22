package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	urlpkg "net/url"
	"os"
	"strings"
	"sync"
	"time"

	"hole/core/netresolve"
)

type Candidate struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type SignalMessage struct {
	Type            string           `json:"type"`
	Room            string           `json:"room,omitempty"`
	Token           string           `json:"token,omitempty"`
	DeviceID        string           `json:"device_id,omitempty"`
	DeviceName      string           `json:"device_name,omitempty"`
	Provide         []Provide        `json:"provide"`
	Consume         []Consume        `json:"consume"`
	Candidates      []Candidate      `json:"candidates,omitempty"`
	MappingID       string           `json:"mapping_id,omitempty"`
	Provider        string           `json:"provider,omitempty"`
	PeerDevice      string           `json:"peer_device,omitempty"`
	Role            string           `json:"role,omitempty"`
	PeerCandidates  []Candidate      `json:"peer_candidates,omitempty"`
	CertFingerprint string           `json:"cert_fingerprint,omitempty"`
	PeerFingerprint string           `json:"peer_fingerprint,omitempty"`
	LocalExpose     *HostPort        `json:"local_expose,omitempty"`
	Service         *ServiceEndpoint `json:"service,omitempty"`
	Reason          string           `json:"reason,omitempty"`
	Message         string           `json:"message,omitempty"`
	Code            string           `json:"code,omitempty"`
}

const (
	signalPingPeriod = 30 * time.Second
	signalPongWait   = 90 * time.Second
)

// sessionPeer handles authorized application streams on the shared device mux.
// Transport discovery and connection lifecycle belong to iceCoordinator.
type sessionPeer struct {
	provide           map[string]Provide
	peerFingerprints  map[peerMapping]string
	peerFingerprintMu sync.Mutex
	sessions          *sessionManager
	emit              func(Event)
}

type peerMapping struct {
	mappingID string
	device    string
}

// tunnel is the shared consumer session's local mapping and peer identity.
type tunnel struct {
	id       string
	protocol string
	expose   HostPort
	peer     string
}

func sameCandidates(left, right []Candidate) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[Candidate]bool, len(left))
	for _, candidate := range left {
		seen[candidate] = true
	}
	for _, candidate := range right {
		if !seen[candidate] {
			return false
		}
	}
	return true
}

func cfgEscape(s string) string {
	return urlpkg.QueryEscape(s)
}

func loadTrustRoots() *x509.CertPool {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}

	paths := []string{
		os.Getenv("SSL_CERT_FILE"),
		os.Getenv("PREFIX") + "/etc/tls/cert.pem",
		"/data/data/com.termux/files/usr/etc/tls/cert.pem",
		"/etc/ssl/cert.pem",
		"/etc/ssl/certs/ca-certificates.crt",
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil && roots.AppendCertsFromPEM(data) {
			break
		}
	}
	return roots
}

func dialWithFallback(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if net.ParseIP(strings.Trim(address, "[]")) != nil {
		return dialer.DialContext(ctx, network, address)
	}

	conn, err := dialer.DialContext(ctx, network, address)
	if err == nil {
		return conn, nil
	}

	host, port, splitErr := net.SplitHostPort(address)
	if splitErr != nil {
		return nil, err
	}
	fallbackResolver := netresolve.NewFallbackResolver()
	ips, lookupErr := fallbackResolver.LookupIP(ctx, "ip", host)
	if lookupErr != nil {
		return nil, fmt.Errorf("系统 DNS 失败（%v），备用 DNS 也失败：%w", err, lookupErr)
	}
	for _, ip := range ips {
		if network == "tcp4" && ip.To4() == nil {
			continue
		}
		if network == "tcp6" && ip.To4() != nil {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
	}
	return nil, err
}

func tcpAddr(endpoint HostPort) *net.TCPAddr {
	return &net.TCPAddr{IP: net.ParseIP(endpoint.Addr), Port: endpoint.Port}
}

func udpAddr(endpoint HostPort) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(endpoint.Addr), Port: endpoint.Port}
}

// exposeNetwork 按配置里写的地址族选择监听网络。
// 把 "tcp"/"udp" 直接交给 Go 会让 0.0.0.0 这类通配地址被提升成双栈的 [::]
// （见 net.favoriteAddrFamily：listen 且地址是通配时优先 AF_INET6），
// 于是配置写 IPv4 却绑到了 IPv6，与预期不符。
// 例外是 ::，它按惯例就表示双栈通配，保留 Go 的默认行为。
func exposeNetwork(base string, ip net.IP) string {
	if ip == nil || (ip.IsUnspecified() && ip.To4() == nil) {
		return base
	}
	if ip.To4() != nil {
		return base + "4"
	}
	return base + "6"
}

func provideTable(provide []Provide) map[string]Provide {
	result := make(map[string]Provide, len(provide))
	for _, p := range provide {
		result[p.ID] = p
	}
	return result
}

func consumeTable(consume []Consume) map[string]Consume {
	result := make(map[string]Consume, len(consume))
	for _, c := range consume {
		result[c.ID] = c
	}
	return result
}

// CandidatesFromInterfaces applies the same candidate policy to desktop and
// host-provided interface snapshots, without making any OS interface calls.
func CandidatesFromInterfaces(cfg Config, port int, interfaces []InterfaceSnapshot) ([]Candidate, error) {
	seen := make(map[string]bool)
	var result []Candidate
	addCandidate := func(ip net.IP) error {
		if !isPublicIPv6(ip) {
			return fmt.Errorf("%s 不是公网 IPv6 地址", ip)
		}
		key := ip.String()
		if !seen[key] {
			seen[key] = true
			result = append(result, Candidate{IP: key, Port: port})
		}
		return nil
	}

	if len(cfg.CandidateAddresses) > 0 {
		for _, value := range cfg.CandidateAddresses {
			ip := net.ParseIP(strings.TrimSpace(value))
			if ip == nil {
				return nil, fmt.Errorf("候选地址 %q 不是有效 IP", value)
			}
			if err := addCandidate(ip); err != nil {
				return nil, fmt.Errorf("候选地址 %q 无效：%w", value, err)
			}
		}
		return result, nil
	}

	wantedInterfaces := stringSet(cfg.CandidateInterfaces)
	for _, iface := range interfaces {
		if !iface.Up || iface.Loopback {
			continue
		}
		if len(wantedInterfaces) > 0 {
			if !wantedInterfaces[iface.Name] {
				continue
			}
		} else if isDefaultExcludedInterface(iface.Name) {
			continue
		}
		for _, raw := range iface.Addresses {
			ip := net.ParseIP(raw)
			if ip == nil || !isPublicIPv6(ip) {
				continue
			}
			if err := addCandidate(ip); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func addrIP(addr net.Addr) net.IP {
	switch value := addr.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		return nil
	}
}

func isPublicIPv6(ip net.IP) bool {
	ip = ip.To16()
	return ip != nil && ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLoopback() && !ip.IsUnspecified()
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func isDefaultExcludedInterface(name string) bool {
	name = strings.ToLower(name)
	if name == "tailscale0" || name == "tun0" || name == "utun0" || strings.HasPrefix(name, "tailscale") {
		return true
	}
	for _, prefix := range []string{"tun", "utun", "wg", "docker", "veth", "br-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// makeTLSConfig creates one process identity reused across all peer paths.
// Peer fingerprints come from authenticated signaling before channels open.
func makeTLSConfig() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "hole"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, DNSNames: []string{"hole"}}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{holeALPN}, InsecureSkipVerify: true, ClientAuth: tls.RequireAnyClientCert}, nil
}

// certFingerprint 计算叶子证书 DER 的 SHA-256 指纹，随 join 上报信令，
// 再由 transport_ready 下发给对端作为数据面认证的比对基准。
func certFingerprint(cert tls.Certificate) string {
	sum := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(sum[:])
}

// verifyPeerCert 校验入向 QUIC 连接的对端证书是否与信令下发的该映射对端
// 指纹一致。指纹经受密码保护的信令通道分发，伪造证书无法通过比对，
// 任何知道地址和端口的外部连接都会在这里被拒。
func (a *sessionPeer) verifyPeerCert(mappingID string, conn sessionConnection) bool {
	fingerprint := connectionFingerprint(conn)
	if fingerprint == "" {
		a.logf("映射 %s 对端未提供客户端证书，拒绝数据面连接", mappingID)
		return false
	}
	a.peerFingerprintMu.Lock()
	defer a.peerFingerprintMu.Unlock()
	for key, expected := range a.peerFingerprints {
		if key.mappingID == mappingID && expected == fingerprint {
			return true
		}
	}
	a.logf("映射 %s 对端证书指纹不匹配，拒绝数据面连接", mappingID)
	return false
}

func connectionFingerprint(conn sessionConnection) string {
	cs := conn.ConnectionState()
	if len(cs.TLS.PeerCertificates) == 0 {
		return ""
	}
	sum := sha256.Sum256(cs.TLS.PeerCertificates[0].Raw)
	return hex.EncodeToString(sum[:])
}

func (a *sessionPeer) logf(format string, args ...any) {
	if a.emit != nil {
		a.emit(Event{Kind: "log", Message: fmt.Sprintf(format, args...)})
	}
}
