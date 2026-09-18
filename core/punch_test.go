package core

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestPunchPacketRoundTrip(t *testing.T) {
	packet := punchPacket("ping", "proxy", "nec")
	kind, mappingID, device, ok := parsePunchPacket(packet)
	if !ok {
		t.Fatalf("解析自己构造的打洞包失败：%q", packet)
	}
	if kind != "ping" || mappingID != "proxy" || device != "nec" {
		t.Fatalf("往返结果不符：kind=%q mapping=%q device=%q", kind, mappingID, device)
	}
}

func TestPunchPacketRejectsForeignData(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("hello"),
		[]byte("#hole/1\nping\nproxy"),
		[]byte("#hole/2\nping\nproxy\nnec"),
	} {
		if _, _, _, ok := parsePunchPacket(data); ok {
			t.Fatalf("不应接受非法数据：%q", data)
		}
	}
}

// quic-go 依据首字节的最高两位把包分流给 ReadNonQUICPacket
// （transport.go 的 handlePacket，判定见 wire.IsLongHeaderPacket / IsPotentialQUICPacket）。
// 打洞包必须落在非 QUIC 一侧，否则会被当成 QUIC 包丢弃或误触发 stateless reset。
func TestPunchPacketIsClassifiedAsNonQUIC(t *testing.T) {
	first := punchPacket("ping", "proxy", "nec")[0]
	if first&0x80 != 0 {
		t.Fatalf("首字节 %#x 会被判定为 QUIC 长包头", first)
	}
	if first&0x40 != 0 {
		t.Fatalf("首字节 %#x 会被判定为潜在 QUIC 包", first)
	}
}

// 打洞的前提是探测包与 QUIC 流量共用同一个 UDP 套接字和同一个四元组：
// 只有这样，探测包在光猫上打开的 conntrack 表项才能让随后的 QUIC 报文通过。
func TestPunchAndQUICShareOneSocket(t *testing.T) {
	transportA, addrA := newTestTransport(t)
	transportB, addrB := newTestTransport(t)

	tlsA, err := makeTLSConfig()
	if err != nil {
		t.Fatalf("生成 A 端 TLS 配置：%v", err)
	}
	tlsB, err := makeTLSConfig()
	if err != nil {
		t.Fatalf("生成 B 端 TLS 配置：%v", err)
	}
	quicConfig := &quic.Config{EnableDatagrams: true, MaxIdleTimeout: 30 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	received := make(chan string, 8)
	go func() {
		buffer := make([]byte, 1500)
		for {
			n, from, err := transportB.ReadNonQUICPacket(ctx, buffer)
			if err != nil {
				return
			}
			kind, mappingID, _, ok := parsePunchPacket(buffer[:n])
			if !ok {
				continue
			}
			if kind == "ping" {
				_, _ = transportB.WriteTo(punchPacket("pong", mappingID, "b"), from)
			}
			select {
			case received <- kind:
			default:
			}
		}
	}()

	// ReadNonQUICPacket 只投递它首次被调用之后到达的包，所以像真实打洞一样重复发送。
	ping := punchPacket("ping", "proxy", "a")
	if err := sendUntil(ctx, t, transportA, addrB, ping, received); err != nil {
		t.Fatalf("B 端未收到打洞包：%v", err)
	}

	listener, err := transportB.Listen(tlsB, quicConfig)
	if err != nil {
		t.Fatalf("B 端监听 QUIC：%v", err)
	}
	defer listener.Close()

	accepted := make(chan *quic.Conn, 1)
	go func() {
		conn, err := listener.Accept(ctx)
		if err == nil {
			accepted <- conn
		}
	}()

	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDial()
	conn, err := transportA.Dial(dialCtx, addrB, tlsA, quicConfig)
	if err != nil {
		t.Fatalf("A 端向同一套接字建立 QUIC 连接失败：%v", err)
	}
	defer conn.CloseWithError(0, "")

	select {
	case <-accepted:
	case <-ctx.Done():
		t.Fatal("B 端未接受 QUIC 连接")
	}

	// QUIC 连接已建立，打洞包仍必须能在同一套接字上继续收发，
	// 保活探测正是靠这一点维持 conntrack 表项。
	drain(received)
	if err := sendUntil(ctx, t, transportA, addrB, ping, received); err != nil {
		t.Fatalf("QUIC 连接建立后打洞包无法继续收发：%v", err)
	}

	if addrA.Port == addrB.Port {
		t.Fatal("测试用的两个套接字端口意外相同")
	}
}

func newTestTransport(t *testing.T) (*quic.Transport, *net.UDPAddr) {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("绑定测试套接字：%v", err)
	}
	transport := &quic.Transport{Conn: pc}
	t.Cleanup(func() {
		_ = transport.Close()
		_ = pc.Close()
	})
	return transport, pc.LocalAddr().(*net.UDPAddr)
}

func sendUntil(ctx context.Context, t *testing.T, transport *quic.Transport, target *net.UDPAddr, packet []byte, got <-chan string) error {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(5 * time.Second)
	for {
		if _, err := transport.WriteTo(packet, target); err != nil {
			return err
		}
		select {
		case <-got:
			return nil
		case <-ticker.C:
		case <-deadline:
			return context.DeadlineExceeded
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func drain(ch <-chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// 配置里写 IPv4 就必须绑 IPv4。把 "tcp" 直接交给 Go 会让通配地址被提升成
// 双栈的 [::]，日志显示的绑定地址与配置不符。
func TestExposeNetworkFollowsConfiguredFamily(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want string
	}{
		{"0.0.0.0", "tcp4"},
		{"127.0.0.1", "tcp4"},
		{"192.168.1.10", "tcp4"},
		{"::1", "tcp6"},
		{"fd00::1", "tcp6"},
		{"::", "tcp"},
	} {
		if got := exposeNetwork("tcp", net.ParseIP(tc.addr)); got != tc.want {
			t.Errorf("exposeNetwork(tcp, %s) = %s，期望 %s", tc.addr, got, tc.want)
		}
	}
}

func TestExposeBindsConfiguredFamily(t *testing.T) {
	bind := tcpAddr(HostPort{Addr: "0.0.0.0", Port: 0})
	listener, err := net.ListenTCP(exposeNetwork("tcp", bind.IP), bind)
	if err != nil {
		t.Fatalf("监听 0.0.0.0：%v", err)
	}
	defer listener.Close()

	got := listener.Addr().(*net.TCPAddr)
	if got.IP.To4() == nil {
		t.Fatalf("配置 0.0.0.0 却绑到了 %s", got)
	}
}
