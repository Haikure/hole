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
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	urlpkg "net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
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

const holePort = 55140

const (
	// punchMagic 的首字节是 '#'（0x23），最高两位为 00，
	// quic-go 据此把打洞包判定为非 QUIC 包并交给 ReadNonQUICPacket，
	// 因此打洞包可以和 QUIC 流量复用同一个 UDP 套接字与同一个四元组。
	punchMagic = "#hole/1"
	// 打洞初期的高频探测间隔，用于抢在对端防火墙状态建立前反复尝试。
	punchInterval = 250 * time.Millisecond
	// 高频探测持续时长，之后降频为保活。
	punchBurst = 30 * time.Second
	// 保活间隔，需要短于光猫 conntrack 的 UDP 表项超时（通常 30 到 180 秒）。
	punchKeepalive = 15 * time.Second
	// 等待对端打洞包确认路径的时长，超时后退化为盲试全部候选地址。
	punchConfirmWait = 3 * time.Second
	// 重新采集本机公网 IPv6 并在变化时上报的间隔。家宽前缀会随重拨变化，
	// 隐私扩展的临时地址也会定期轮换，不上报的话对端会一直打向废地址。
	candidateRefresh = 5 * time.Second
	// 信令 WebSocket 的保活参数。join 之后连接大部分时间没有上行流量，
	// Cloudflare 边缘会把长期空闲的 WebSocket 直接掐断（表现为约 100 分钟
	// 一次的 close 1006 unexpected EOF），必须周期性发 ping 维持。
	// pongWait 要大于 pingPeriod，保证每个周期内至少收到一个 pong 来续读超时。
	signalPingPeriod = 30 * time.Second
	signalPongWait   = 90 * time.Second
)

type Agent struct {
	platform            Platform
	emit                func(Event)
	cfg                 Config
	udp                 net.PacketConn
	publishedCandidates []Candidate
	tlsConfig           *tls.Config
	quicConfig          *quic.Config
	quicServer          *quic.Listener
	quicServerMu        sync.Mutex
	quicTransport       *quic.Transport
	quicConnMu          sync.Mutex
	quicConns           map[string]*quic.Conn
	provide             map[string]Provide
	consume             map[string]Consume
	candidatesMu        sync.RWMutex
	signalMu            sync.Mutex
	punchMu             sync.Mutex
	punchSessions       map[peerMapping]*punchSession
	fingerprint         string
	peerFingerprintMu   sync.Mutex
	peerFingerprints    map[peerMapping]string
	sessions            *sessionManager
	ownsSessions        bool
	workers             sync.WaitGroup
	workerMu            sync.Mutex
	workersClosed       bool
	lifecycleContext    context.Context
	lifecycleCancel     context.CancelFunc
	shutdownOnce        sync.Once
}

type peerMapping struct {
	mappingID string
	device    string
}

// punchSession 跟踪一个映射的打洞进度。对端的第一个打洞包会确认一条
// 真实可用的返回路径，这个源地址比信令里公布的候选地址更可靠：
// 套接字绑定在通配地址上，内核按 RFC 6724 自行挑选源地址，
// 未必等于候选列表里的任何一项。
type punchSession struct {
	cancel context.CancelFunc
	ready  chan struct{}
	once   sync.Once
	addrMu sync.Mutex
	addr   *net.UDPAddr

	// targets 是打洞与拨号共用的目标列表。信令会把对端轮换后的新候选
	// 地址通过 peer_candidates 推送过来，setTargets 热更新后 punchLoop 与
	// dialWithRetry 每轮重新读取，映射无需重建即可指向新地址。
	targetMu       sync.Mutex
	targets        []*net.UDPAddr
	targetsChanged chan struct{}
}

func (s *punchSession) setTargets(targets []*net.UDPAddr) {
	s.targetMu.Lock()
	defer s.targetMu.Unlock()
	s.targets = targets
	select {
	case s.targetsChanged <- struct{}{}:
	default:
	}
}

func (s *punchSession) currentTargets() []*net.UDPAddr {
	s.targetMu.Lock()
	defer s.targetMu.Unlock()
	return s.targets
}

func (s *punchSession) confirm(addr *net.UDPAddr) {
	s.addrMu.Lock()
	isFirst := s.addr == nil
	if isFirst {
		s.addr = addr
	}
	s.addrMu.Unlock()
	if isFirst {
		s.once.Do(func() { close(s.ready) })
	}
}

func (s *punchSession) confirmedAddr() *net.UDPAddr {
	s.addrMu.Lock()
	defer s.addrMu.Unlock()
	return s.addr
}

func punchPacket(kind, mappingID, device string) []byte {
	return []byte(punchMagic + "\n" + kind + "\n" + mappingID + "\n" + device)
}

func parsePunchPacket(data []byte) (kind, mappingID, device string, ok bool) {
	parts := strings.Split(string(data), "\n")
	if len(parts) != 4 || parts[0] != punchMagic {
		return "", "", "", false
	}
	return parts[1], parts[2], parts[3], true
}

func newAgent(ctx context.Context, cfg Config, platform Platform, emit func(Event)) (*Agent, error) {
	return newAgentWithState(ctx, cfg, platform, emit, nil, nil)
}

func newAgentWithState(ctx context.Context, cfg Config, platform Platform, emit func(Event), sessions *sessionManager, tlsConfig *tls.Config) (*Agent, error) {
	candidates, err := platform.Candidates(ctx, cfg, holePort)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, &Fault{Code: "no_ipv6", Message: "未找到可发布的公网 IPv6 候选地址"}
	}

	pc, err := platform.ListenPacket(ctx, "udp6", net.JoinHostPort("::", fmt.Sprint(holePort)))
	if err != nil {
		return nil, fmt.Errorf("绑定 IPv6 UDP 套接字 *:%d: %w", holePort, err)
	}
	if tlsConfig == nil {
		tlsConfig, err = makeTLSConfig()
		if err != nil {
			pc.Close()
			return nil, fmt.Errorf("初始化 TLS：%w", err)
		}
	}
	logf := func(format string, args ...any) { emit(Event{Kind: "log", Message: fmt.Sprintf(format, args...)}) }
	ownsSessions := sessions == nil
	if sessions == nil {
		sessions = newSessionManager(cfg.SessionTimeout, sessionOptions{platform: platform, logf: logf})
	}
	logf("🌐 公网 IPv6 已就绪：候选地址=%d，UDP 端口=%d", len(candidates), holePort)
	life, cancelLife := context.WithCancel(ctx)
	return &Agent{
		lifecycleContext: life, lifecycleCancel: cancelLife,
		platform: platform, emit: emit,
		cfg:                 cfg,
		udp:                 pc,
		publishedCandidates: candidates,
		tlsConfig:           tlsConfig,
		fingerprint:         certFingerprint(tlsConfig.Certificates[0]),
		quicConfig:          &quic.Config{EnableDatagrams: true, KeepAlivePeriod: 15 * time.Second, MaxIdleTimeout: 2 * time.Minute},
		quicTransport:       &quic.Transport{Conn: pc},
		quicConns:           make(map[string]*quic.Conn),
		peerFingerprints:    make(map[peerMapping]string),
		provide:             provideTable(cfg.Provide),
		consume:             consumeTable(cfg.Consume),
		punchSessions:       make(map[peerMapping]*punchSession),
		sessions:            sessions,
		ownsSessions:        ownsSessions,
	}, nil
}

func (a *Agent) run(ctx context.Context, serverURL string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() { cancel(); a.shutdown() }()

	// 监听器和打洞收包循环在进程启动时就跑起来，而不是等到收到 mapping_ready。
	// ReadNonQUICPacket 只会投递它被首次调用之后到达的包，晚启动会丢掉对端
	// 最早的几轮打洞探测；监听器晚启动同理会错过已经穿透进来的 QUIC Initial。
	if err := a.ensureProviderListener(ctx); err != nil {
		return fmt.Errorf("启动 QUIC 监听：%w", err)
	}
	a.spawn(func() { a.runPunchReceiver(ctx) })
	a.logf("🚀 hole 已启动：候选地址=%d，UDP 端口=%d", len(a.candidates()), holePort)

	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		started := time.Now()
		err := a.connectAndServe(ctx, serverURL)
		if time.Since(started) >= signalPingPeriod {
			backoff = time.Second
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			a.logf("信令会话结束：%v", err)
		} else {
			a.logf("信令会话结束")
		}
		a.closeSessions()
		event := Event{Kind: "signal", State: "reconnecting"}
		if err != nil {
			event.Error = classifyError(err)
		}
		a.emit(event)

		a.logf("🔁 信令断开，将在 %s 后自动重连", backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (a *Agent) connectAndServe(ctx context.Context, serverURL string) error {
	candidates, err := a.platform.Candidates(ctx, a.cfg, holePort)
	if err != nil {
		return fmt.Errorf("刷新 IPv6 候选地址：%w", err)
	}
	if len(candidates) == 0 {
		return errors.New("刷新后没有可发布的公网 IPv6 候选地址")
	}
	a.setCandidates(candidates)
	a.logf("🌐 已刷新公网 IPv6：候选地址=%d，UDP 端口=%d", len(candidates), holePort)

	url, err := urlpkg.Parse(serverURL)
	if err != nil {
		return err
	}
	query := url.Query()
	query.Set("room", a.cfg.Room)
	url.RawQuery = query.Encode()
	a.emit(Event{Kind: "signal", State: "connecting"})
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = a.platform.DialSignal
	if roots := loadTrustRoots(); roots != nil {
		dialer.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	// password 以 Bearer 头在 WebSocket 升级前由信令服务器校验，没通过就不会
	// 创建房间 DO，防止公网扫描白白消耗 DO 额度。
	header := http.Header{}
	header.Set("Authorization", "Bearer "+a.cfg.Password)
	a.logf("🔌 正在连接信令服务器：%s", displayServer(serverURL))
	conn, response, err := dialer.DialContext(ctx, url.String(), header)
	if err != nil {
		if response != nil {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
				return &Fault{Code: "signal_auth_failed", Message: "信令密码校验失败"}
			}
		}
		return fmt.Errorf("连接信令服务器：%w", err)
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	conn.SetReadLimit(256 * 1024)

	// 空闲保活：读超时靠下面的 pong 处理器续期，ping 由 pingSignal 周期性发送。
	// 没有它，join 之后长期空闲的连接会被 Cloudflare 边缘按空闲超时掐断。
	conn.SetReadDeadline(time.Now().Add(signalPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(signalPongWait))
		return nil
	})

	join := SignalMessage{Type: "join", Room: a.cfg.Room, Token: a.cfg.Token, DeviceID: a.cfg.DeviceName, DeviceName: a.cfg.DeviceName, Provide: a.cfg.Provide, Consume: a.cfg.Consume, Candidates: a.candidates(), CertFingerprint: a.fingerprint}
	if err := a.writeSignal(conn, join); err != nil {
		return fmt.Errorf("发送加入消息：%w", err)
	}
	a.emit(Event{Kind: "signal", State: "joining"})
	a.logf("✅ 信令连接成功，等待房间事件")

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.spawn(func() { a.pingSignal(sessionCtx, conn) })
	a.spawn(func() { a.refreshCandidates(sessionCtx, conn) })
	result := make(chan error, 1)
	a.spawn(func() {
		result <- a.readSignals(sessionCtx, conn)
	})
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		_ = conn.Close()
		return ctx.Err()
	}
}

func (a *Agent) candidates() []Candidate {
	a.candidatesMu.RLock()
	defer a.candidatesMu.RUnlock()
	return a.publishedCandidates
}

func (a *Agent) setCandidates(candidates []Candidate) {
	a.candidatesMu.Lock()
	a.publishedCandidates = candidates
	a.candidatesMu.Unlock()
}

// writeSignal 串行化 WebSocket 写入。gorilla/websocket 只允许一个并发 writer，
// 而 join 与周期性的候选地址上报来自不同 goroutine。
func (a *Agent) writeSignal(conn *websocket.Conn, msg SignalMessage) error {
	a.signalMu.Lock()
	defer a.signalMu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return conn.WriteJSON(msg)
}

// pingSignal 周期性向信令服务器发送 WebSocket ping，刷新 Cloudflare 边缘的空闲
// 计时器，防止长时间无业务消息的连接被掐断；对端回的 pong 会让读超时被续期。
// WriteControl 官方保证可与其他写操作并发调用，因此不经过 signalMu。
func (a *Agent) pingSignal(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(signalPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
			return
		}
	}
}

// refreshCandidates 周期性重新采集本机公网 IPv6，变化时上报给信令服务器，
// 由服务器转发给对端，使对端的打洞目标始终指向当前有效的地址。
func (a *Agent) refreshCandidates(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(candidateRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		candidates, err := a.platform.Candidates(ctx, a.cfg, holePort)
		if err != nil || len(candidates) == 0 {
			continue
		}
		if sameCandidates(a.candidates(), candidates) {
			continue
		}
		a.setCandidates(candidates)
		a.logf("🌐 公网 IPv6 已变化：候选地址=%d，正在上报信令服务器", len(candidates))
		if err := a.writeSignal(conn, SignalMessage{Type: "candidate", Candidates: candidates}); err != nil {
			return
		}
		// Keep application sockets, but retire paths bound to the old network.
		// Waiting for QUIC's idle timeout would otherwise stall them for minutes.
		a.sessions.disconnect("", "")
	}
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
	fallbackResolver := &net.Resolver{
		PreferGo: true,
		Dial: func(resolveCtx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(resolveCtx, "udp", "223.5.5.5:53")
		},
	}
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

func (a *Agent) readSignals(ctx context.Context, conn *websocket.Conn) error {
	for {
		var msg SignalMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("读取信令消息：%w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		switch msg.Type {
		case "joined":
			a.emit(Event{Kind: "signal", State: "joined"})
			a.logf("🏠 已加入房间：设备=%q，房间=%q", a.cfg.DeviceName, a.cfg.Room)
		case "mapping_ready":
			if msg.PeerFingerprint == "" {
				a.logf("映射 %s 未携带对端证书指纹（信令服务器或对端过旧？），已拒绝", msg.MappingID)
				continue
			}
			a.setPeerFingerprint(msg.MappingID, msg.PeerDevice, msg.PeerFingerprint)
			a.logf("📡 映射就绪：%s（%s ↔ %s）", msg.MappingID, msg.Role, msg.PeerDevice)
			a.startMapping(ctx, msg)
		case "peer_candidates":
			a.updateSessionCandidates(msg.MappingID, msg.PeerDevice, msg.Candidates)
		case "mapping_closed":
			a.logf("⏹️ 映射已关闭：%s（%s）", msg.MappingID, msg.Reason)
			a.stopMapping(msg.MappingID, msg.PeerDevice)
			a.emit(Event{Kind: "mapping", MappingID: msg.MappingID, State: "waiting_peer"})
		case "error":
			code := msg.Code
			if code == "" {
				code = "signal_error"
			}
			event := Event{Kind: "signal", State: "error", Error: &Fault{Code: code, Message: msg.Message}}
			if msg.MappingID != "" {
				event.Kind, event.MappingID = "mapping", msg.MappingID
			}
			a.emit(event)
			a.logf("信令错误：映射=%s，错误=%s", msg.MappingID, msg.Message)
		default:
		}
	}
}

// startMapping 按 mapping_ready 指派的角色启动映射。信令服务器只按 id 和
// provider 配对，service 的一致性与提供者的有效性由两端在这里各自把关。
// 关键：两端都必须主动向对方发包。只有出向包才会在本侧光猫的
// 有状态 IPv6 防火墙上建立 conntrack 表项，对端后续发来的包才不会被丢弃。
// 旧实现里 provider 只 Listen 不发包，它那侧的洞从未打开，
// consumer 的 QUIC Initial 全部止步于 provider 的光猫。
func (a *Agent) startMapping(ctx context.Context, msg SignalMessage) {
	if msg.Service == nil {
		a.logf("映射 %s 的 mapping_ready 缺少 service 字段，忽略", msg.MappingID)
		return
	}
	switch msg.Role {
	case "provider":
		a.startProvider(ctx, msg)
	case "consumer":
		a.startConsumer(ctx, msg)
	default:
		a.logf("映射 %s 的角色 %q 未知，忽略", msg.MappingID, msg.Role)
	}
}

func (a *Agent) startProvider(ctx context.Context, msg SignalMessage) {
	prov, ok := a.provide[msg.MappingID]
	if !ok {
		a.logf("拒绝未声明的映射：%s（本机没有 provide 这个 id）", msg.MappingID)
		return
	}
	if msg.Service.Protocol != prov.Service.Protocol || msg.Service.Addr != prov.Service.Addr || msg.Service.Port != prov.Service.Port {
		a.logf("映射 %s 的 service 与本机 provide 声明不一致（信令 %s://%s:%d，本机 %s://%s:%d）", msg.MappingID, msg.Service.Protocol, msg.Service.Addr, msg.Service.Port, prov.Service.Protocol, prov.Service.Addr, prov.Service.Port)
		return
	}
	if len(msg.PeerCandidates) == 0 {
		a.logf("映射 %s 没有对端 IPv6 候选地址，请检查对端的全局 IPv6 地址", msg.MappingID)
		return
	}
	a.logf("🔎 正在建立映射：%s，对端=%s，候选地址=%d", prov.ID, msg.PeerDevice, len(msg.PeerCandidates))
	if sess, _ := a.startPunching(ctx, msg.MappingID, msg.PeerDevice, msg.PeerCandidates); sess != nil {
		a.emit(Event{Kind: "mapping", MappingID: msg.MappingID, Protocol: msg.Service.Protocol, State: "connecting", Peer: msg.PeerDevice})
	}
}

// startConsumer 校验映射有效后才开始打洞、连接。consume 只声明 id 和 expose，
// 提供者与 service 由信令服务器按唯一 id 配对得出，这里把关协议有效性与
// 对端候选地址，任何异常都留下明确的日志。
func (a *Agent) startConsumer(ctx context.Context, msg SignalMessage) {
	c, ok := a.consume[msg.MappingID]
	if !ok {
		a.logf("拒绝未声明的映射：%s（本机没有 consume 这个 id）", msg.MappingID)
		return
	}
	if msg.Service.Protocol != "tcp" && msg.Service.Protocol != "udp" {
		a.logf("映射 %s 的协议 %q 无效", msg.MappingID, msg.Service.Protocol)
		return
	}
	if len(msg.PeerCandidates) == 0 {
		a.logf("映射 %s 没有对端 IPv6 候选地址，请检查对端的全局 IPv6 地址", msg.MappingID)
		return
	}
	a.logf("🔎 正在建立映射：%s，对端=%s，候选地址=%d", c.ID, msg.PeerDevice, len(msg.PeerCandidates))
	t := tunnel{id: c.ID, protocol: msg.Service.Protocol, expose: c.Expose, peer: msg.PeerDevice}
	if _, err := a.sessions.consumer(t, msg.PeerFingerprint); err != nil {
		a.logf("映射 %s 初始化本地监听失败：%v", c.ID, err)
		a.emit(Event{Kind: "mapping", MappingID: c.ID, State: "error", Error: classifyError(err)})
		return
	}
	sess, sessCtx := a.startPunching(ctx, msg.MappingID, msg.PeerDevice, msg.PeerCandidates)
	if sess == nil {
		return
	}
	a.emit(Event{Kind: "mapping", MappingID: msg.MappingID, Protocol: msg.Service.Protocol, State: "connecting", Peer: msg.PeerDevice})
	a.spawn(func() { a.dialWithRetry(sessCtx, sess, t) })
}

// startPunching 为一个映射启动（或重启）打洞会话，返回会话及其派生 ctx。
func (a *Agent) startPunching(ctx context.Context, mappingID, peer string, candidates []Candidate) (*punchSession, context.Context) {
	targets := resolveCandidates(candidates)
	if len(targets) == 0 {
		a.logf("映射 %s 的对端候选地址均无法解析，跳过打洞", mappingID)
		return nil, nil
	}

	sessCtx, cancel := context.WithCancel(ctx)
	sess := &punchSession{cancel: cancel, ready: make(chan struct{}), targets: targets, targetsChanged: make(chan struct{}, 1)}
	key := peerMapping{mappingID: mappingID, device: peer}

	a.punchMu.Lock()
	if old, exists := a.punchSessions[key]; exists {
		old.cancel()
	}
	a.punchSessions[key] = sess
	a.punchMu.Unlock()

	a.spawn(func() { a.punchLoop(sessCtx, mappingID, sess) })
	return sess, sessCtx
}

// updateSessionCandidates 处理信令推送的对端新候选地址：热更新存活打洞
// 会话的目标列表，punchLoop 转而探测新地址、dialWithRetry 重试时拨向新
// 地址。对端地址轮换后映射无需重建，也不再依赖信令断开重连来触发
// mapping_ready 重发。
func (a *Agent) updateSessionCandidates(mappingID, peer string, candidates []Candidate) {
	a.punchMu.Lock()
	sess := a.punchSessions[peerMapping{mappingID: mappingID, device: peer}]
	a.punchMu.Unlock()
	if sess == nil {
		return
	}
	targets := resolveCandidates(candidates)
	if len(targets) == 0 {
		a.logf("映射 %s 推送的对端候选地址均无法解析，保留现有目标", mappingID)
		return
	}
	sess.setTargets(targets)
	a.logf("🌐 映射 %s 对端候选地址已更新：目标=%d", mappingID, len(targets))
	if fingerprint := a.peerFingerprint(mappingID, peer); fingerprint != "" {
		a.sessions.disconnect(mappingID, fingerprint)
	}
}

// punchLoop 周期性地向对端所有候选地址发送打洞包。前 punchBurst 时间内高频发送，
// 之后降频为保活，使 conntrack 表项一直存活，对端任何时刻发起连接都能穿透。
// 目标列表每轮从会话读取，信令推送的新地址即时生效。
func (a *Agent) punchLoop(ctx context.Context, mappingID string, sess *punchSession) {
	packet := punchPacket("ping", mappingID, a.cfg.DeviceName)
	burstUntil := time.Now().Add(punchBurst)
	interval := punchInterval
	degraded := false
	a.logf("🔨 映射 %s 开始打洞：目标=%d，间隔=%s", mappingID, len(sess.currentTargets()), interval)

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			a.logf("🛑 映射 %s 打洞已停止", mappingID)
			return
		case <-timer.C:
		case <-sess.targetsChanged:
			burstUntil = time.Now().Add(punchBurst)
			interval = punchInterval
			degraded = false
		}

		for _, target := range sess.currentTargets() {
			if _, err := a.quicTransport.WriteTo(packet, target); err != nil {
				a.logf("映射 %s 向 %s 发送打洞包失败：%v", mappingID, target, err)
			}
		}

		if !degraded && time.Now().After(burstUntil) {
			degraded = true
			interval = punchKeepalive
			a.logf("🕳️ 映射 %s 打洞转入保活模式：间隔=%s", mappingID, interval)
		}
		timer.Reset(interval)
	}
}

// runPunchReceiver 处理对端发来的打洞包。收到 ping 立刻回 pong，
// 这既是应答也顺带再打一次洞；任意方向的包都能确认一条可用路径。
func (a *Agent) runPunchReceiver(ctx context.Context) {
	buffer := make([]byte, 1500)
	for {
		n, addr, err := a.quicTransport.ReadNonQUICPacket(ctx, buffer)
		if err != nil {
			if ctx.Err() == nil {
				a.logf("打洞收包循环退出：%v", err)
			}
			return
		}
		kind, mappingID, device, ok := parsePunchPacket(buffer[:n])
		if !ok {
			continue
		}
		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}

		if kind == "ping" {
			pong := punchPacket("pong", mappingID, a.cfg.DeviceName)
			if _, err := a.quicTransport.WriteTo(pong, udpAddr); err != nil {
				a.logf("映射 %s 回应打洞包失败：%v", mappingID, err)
			}
		}

		a.punchMu.Lock()
		sess := a.punchSessions[peerMapping{mappingID: mappingID, device: device}]
		a.punchMu.Unlock()
		if sess == nil {
			continue
		}
		if sess.confirmedAddr() == nil {
			a.logf("🎉 映射 %s 打洞成功：对端=%s，路径=%s（%s）", mappingID, device, udpAddr, kind)
		}
		sess.confirm(udpAddr)
	}
}

// tunnel 是 consumer 建立一条映射所需的完整参数：
// 协议来自 provider 的声明，expose 是本机配置的监听地址。
type tunnel struct {
	id       string
	protocol string
	expose   HostPort
	peer     string
}

// dialWithRetry 反复尝试建立 QUIC 连接直到成功或会话结束。
// 打洞本身是概率性的：两端起始时刻对不齐、探测包丢失、conntrack 建立有先后，
// 都需要靠重试覆盖，所以这里不设尝试次数上限。
// 目标列表每轮从会话读取，信令推送的对端新候选地址即时生效。
func (a *Agent) dialWithRetry(ctx context.Context, sess *punchSession, t tunnel) {
	tlsCfg := a.pinnedTLSConfig(t.id, t.peer)
	if tlsCfg == nil {
		a.logf("映射 %s 缺少对端证书指纹，无法建立数据面连接", t.id)
		return
	}
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return
		}

		// 优先等待打洞确认的真实路径，拿不到再盲试信令公布的候选地址。
		select {
		case <-sess.ready:
		case <-time.After(punchConfirmWait):
			a.logf("映射 %s 第 %d 次尝试：等待打洞确认超时，盲试全部候选地址", t.id, attempt)
		case <-ctx.Done():
			return
		}

		var targets []*net.UDPAddr
		if confirmed := sess.confirmedAddr(); confirmed != nil {
			targets = append(targets, confirmed)
		}
		targets = append(targets, sess.currentTargets()...)

		if conn := a.dialAny(ctx, dedupeAddrs(targets), t, tlsCfg); conn != nil {
			a.quicConnMu.Lock()
			a.quicConns[t.id] = conn
			a.quicConnMu.Unlock()

			// 阻塞直到连接断开，断开后重新打洞重连而不是永久放弃。
			a.runConsumer(ctx, conn, t)

			a.quicConnMu.Lock()
			if a.quicConns[t.id] == conn {
				delete(a.quicConns, t.id)
			}
			a.quicConnMu.Unlock()
			_ = conn.CloseWithError(0, "consumer session ended")

			if ctx.Err() != nil {
				return
			}
			a.logf("🔁 映射 %s 传输已断开，保留应用会话并重新建立", t.id)
			backoff = time.Second
			attempt = 0
			// Prevent a tight loop when transport negotiation is rejected.
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}

		a.logf("⚠️ 映射 %s 第 %d 次尝试失败，%s 后重试", t.id, attempt, backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 15*time.Second {
			backoff *= 2
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
		}
	}
}

// dialAny 并发拨号所有候选路径，第一个握手成功的胜出，其余立即关闭。
// 旧实现串行遍历、每个候选阻塞 8 秒，一个不可达的地址就会拖垮整轮尝试。
func (a *Agent) dialAny(ctx context.Context, targets []*net.UDPAddr, t tunnel, tlsCfg *tls.Config) *quic.Conn {
	if len(targets) == 0 {
		return nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	type dialResult struct {
		conn *quic.Conn
		addr *net.UDPAddr
	}
	results := make(chan dialResult, len(targets))
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(target *net.UDPAddr) {
			defer wg.Done()
			conn, err := a.quicTransport.Dial(dialCtx, target, tlsCfg, a.quicConfig)
			if err != nil {
				return
			}
			results <- dialResult{conn: conn, addr: target}
		}(target)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	winner, ok := <-results
	if !ok {
		return nil
	}
	cancel()
	for extra := range results {
		_ = extra.conn.CloseWithError(0, "duplicate path")
	}
	a.logf("✅ 映射 %s QUIC 连接已建立：%s", t.id, winner.addr)
	a.emit(Event{Kind: "mapping", MappingID: t.id, State: "active", Peer: t.peer, Path: winner.addr.String()})
	return winner.conn
}

func resolveCandidates(candidates []Candidate) []*net.UDPAddr {
	result := make([]*net.UDPAddr, 0, len(candidates))
	for _, candidate := range candidates {
		addr, err := net.ResolveUDPAddr("udp6", net.JoinHostPort(candidate.IP, fmt.Sprint(candidate.Port)))
		if err != nil {
			continue
		}
		result = append(result, addr)
	}
	return dedupeAddrs(result)
}

func dedupeAddrs(addrs []*net.UDPAddr) []*net.UDPAddr {
	seen := make(map[string]bool, len(addrs))
	result := make([]*net.UDPAddr, 0, len(addrs))
	for _, addr := range addrs {
		key := addr.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, addr)
	}
	return result
}

func (a *Agent) ensureProviderListener(ctx context.Context) error {
	a.quicServerMu.Lock()
	defer a.quicServerMu.Unlock()
	if a.quicServer != nil {
		return nil
	}
	listener, err := a.quicTransport.Listen(a.tlsConfig, a.quicConfig)
	if err != nil {
		return err
	}
	a.logf("👂 QUIC 服务已就绪：UDP %s", a.udp.LocalAddr())
	a.quicServer = listener
	a.spawn(func() { a.acceptProvider(ctx, listener) })
	return nil
}

// stopMapping 终止一个映射的打洞与重试循环，避免对端已经离开后
// 继续无意义地发送保活探测包。
func (a *Agent) stopMapping(mappingID, peer string) {
	a.punchMu.Lock()
	for key, sess := range a.punchSessions {
		if key.mappingID == mappingID && (peer == "" || key.device == peer) {
			sess.cancel()
			delete(a.punchSessions, key)
		}
	}
	a.punchMu.Unlock()

	fingerprints := make(map[string]bool)
	a.peerFingerprintMu.Lock()
	for key, fingerprint := range a.peerFingerprints {
		if key.mappingID == mappingID && (peer == "" || key.device == peer) {
			fingerprints[fingerprint] = true
			delete(a.peerFingerprints, key)
		}
	}
	a.peerFingerprintMu.Unlock()
	if peer == "" {
		a.sessions.disconnect(mappingID, "")
	} else {
		for fingerprint := range fingerprints {
			a.sessions.disconnect(mappingID, fingerprint)
		}
	}

	a.quicConnMu.Lock()
	conn := a.quicConns[mappingID]
	if conn != nil && (peer == "" || fingerprints[connectionFingerprint(conn)]) {
		delete(a.quicConns, mappingID)
	} else {
		conn = nil
	}
	a.quicConnMu.Unlock()
	if conn != nil {
		_ = conn.CloseWithError(0, "mapping closed")
	}
}

// closeSessions 清理一次信令会话期间建立的 QUIC 连接和打洞会话。
// QUIC 监听器不在此关闭：它与进程同生命周期，信令重连不应中断已穿透的入向路径。
func (a *Agent) closeSessions() {
	a.peerFingerprintMu.Lock()
	fingerprints := make(map[peerMapping]string, len(a.peerFingerprints))
	for key, fp := range a.peerFingerprints {
		fingerprints[key] = fp
	}
	a.peerFingerprintMu.Unlock()
	for key, fp := range fingerprints {
		a.sessions.disconnect(key.mappingID, fp)
	}
	a.punchMu.Lock()
	for key, sess := range a.punchSessions {
		sess.cancel()
		delete(a.punchSessions, key)
	}
	a.punchMu.Unlock()

	a.quicConnMu.Lock()
	connections := make([]*quic.Conn, 0, len(a.quicConns))
	for key, conn := range a.quicConns {
		connections = append(connections, conn)
		delete(a.quicConns, key)
	}
	a.quicConnMu.Unlock()
	for _, conn := range connections {
		_ = conn.CloseWithError(0, "signaling session ended")
	}
}

func (a *Agent) acceptProvider(ctx context.Context, listener *quic.Listener) {
	a.logf("QUIC 接收循环已启动")
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return
		}
		a.spawn(func() { a.handleProvider(ctx, conn) })
	}
}

func (a *Agent) handleProvider(ctx context.Context, conn *quic.Conn) {
	a.logf("已接受来自 %s 的 QUIC 连接", conn.RemoteAddr())
	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		a.spawn(func() { a.handleProviderStream(ctx, conn, stream) })
	}
}

func (a *Agent) spawn(work func()) {
	a.workerMu.Lock()
	defer a.workerMu.Unlock()
	if a.workersClosed {
		return
	}
	a.workers.Add(1)
	go func() { defer a.workers.Done(); work() }()
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

// makeTLSConfig 在进程启动时调用一次，证书全程复用。
// 旧实现每次建立映射都现场生成 RSA-2048 密钥，在树莓派、软路由这类弱设备上
// 可能耗时数秒，且两端耗时不同，导致双方开始发打洞包的时刻严重错开。
// 打洞对时序敏感，这里改用 ECDSA P-256，生成开销可以忽略。
// RequireAnyClientCert 使 QUIC 握手阶段就要求对端出示证书：没有证书的
// 连接（公网扫描、随机客户端）在握手时直接失败，指纹校验放在流处理阶段。
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
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"hole-v2"}, InsecureSkipVerify: true, ClientAuth: tls.RequireAnyClientCert}, nil
}

// certFingerprint 计算叶子证书 DER 的 SHA-256 指纹，随 join 上报信令，
// 再由 mapping_ready 下发给对端作为数据面认证的比对基准。
func certFingerprint(cert tls.Certificate) string {
	sum := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(sum[:])
}

func (a *Agent) setPeerFingerprint(mappingID, peer, fingerprint string) {
	a.peerFingerprintMu.Lock()
	a.peerFingerprints[peerMapping{mappingID: mappingID, device: peer}] = fingerprint
	a.peerFingerprintMu.Unlock()
	a.sessions.claimPeer(mappingID, peer, fingerprint)
}

func (a *Agent) peerFingerprint(mappingID, peer string) string {
	a.peerFingerprintMu.Lock()
	defer a.peerFingerprintMu.Unlock()
	return a.peerFingerprints[peerMapping{mappingID: mappingID, device: peer}]
}

// verifyPeerCert 校验入向 QUIC 连接的对端证书是否与信令下发的该映射对端
// 指纹一致。指纹经受密码保护的信令通道分发，伪造证书无法通过比对，
// 任何知道地址和端口的外部连接都会在这里被拒。
func (a *Agent) verifyPeerCert(mappingID string, conn sessionConnection) bool {
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

// pinnedTLSConfig 返回校验对端证书指纹的拨号 TLS 配置：consumer 端在握手
// 阶段就确认对端是信令配对的 provider，防止路径上的 MITM 用自签证书顶替
// 后窃取映射流量。返回 nil 表示该映射没有可用的对端指纹，调用方应拒绝拨号。
func (a *Agent) pinnedTLSConfig(mappingID, peer string) *tls.Config {
	fingerprint := a.peerFingerprint(mappingID, peer)
	if fingerprint == "" {
		return nil
	}
	cfg := a.tlsConfig.Clone()
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("对端未提供证书")
		}
		sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
		if hex.EncodeToString(sum[:]) != fingerprint {
			return errors.New("对端证书指纹与信令下发的不一致")
		}
		return nil
	}
	return cfg
}

func (a *Agent) logf(format string, args ...any) {
	a.emit(Event{Kind: "log", Message: fmt.Sprintf(format, args...)})
}

func (a *Agent) shutdown() {
	a.shutdownOnce.Do(func() {
		if a.lifecycleCancel != nil {
			a.lifecycleCancel()
		}
		a.workerMu.Lock()
		a.workersClosed = true
		a.workerMu.Unlock()
		if a.ownsSessions {
			a.sessions.cancel()
		}
		a.closeSessions()
		_ = a.quicTransport.Close()
		_ = a.udp.Close()
		a.workers.Wait()
		if a.ownsSessions {
			a.sessions.close()
		}
	})
}
