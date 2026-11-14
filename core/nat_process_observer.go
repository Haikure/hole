package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"

	"github.com/pion/stun/v4"
	"github.com/pion/transport/v5"
	"hole/core/internal/nat"
)

// The STUN observer delegates every operation to the original connection. It
// neither sends extra probes nor wraps the socket later passed to QUIC.
type natProcessProbeConn struct {
	net.PacketConn
	process *natProcessRun
	id      string
	round   int
	trial   int
}

func (t *natProcessRun) probeConn(pc net.PacketConn, round, trial int) net.PacketConn {
	if t == nil {
		return pc
	}
	return &natProcessProbeConn{PacketConn: pc, process: t, id: t.socketID(pc), round: round, trial: trial}
}

func (c *natProcessProbeConn) packet(direction string, data []byte, address net.Addr, err error) {
	event := NATPunchProcessEvent{Kind: "stun_packet", Title: "STUN 报文", Direction: direction,
		Packet: "STUN", SocketID: c.id, Round: c.round, Trial: c.trial, Bytes: len(data)}
	if address != nil {
		event.RemoteAddress = address.String()
	}
	if err != nil {
		event.Kind, event.Title, event.Detail = "stun_error", "STUN 收发未完成", err.Error()
	} else {
		message := &stun.Message{Raw: data}
		if message.Decode() == nil {
			event.Packet = message.Type.String()
			event.TransactionID = hex.EncodeToString(message.TransactionID[:])
			if value, e := message.Get(stun.AttrChangeRequest); e == nil && len(value) == 4 {
				event.ChangeRequest = int(value[3])
			}
			var other stun.OtherAddress
			if other.GetFrom(message) == nil {
				event.OtherAddress = net.JoinHostPort(other.IP.String(), fmt.Sprint(other.Port))
			}
		}
	}
	c.process.packet(event)
}

func (c *natProcessProbeConn) WriteTo(data []byte, to net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(data, to)
	c.packet("out", data[:n], to, err)
	return n, err
}

func (c *natProcessProbeConn) ReadFrom(data []byte) (int, net.Addr, error) {
	n, from, err := c.PacketConn.ReadFrom(data)
	c.packet("in", data[:n], from, err)
	return n, from, err
}

func (c *natProcessProbeConn) Close() error {
	err := c.PacketConn.Close()
	c.process.record(NATPunchProcessEvent{Kind: "socket_closed", Title: "采样 socket 已关闭",
		SocketID: c.id, Round: c.round, Trial: c.trial})
	return err
}

func (c *natProcessProbeConn) ObserveSTUNBinding(server netip.AddrPort, transaction [stun.TransactionIDSize]byte, change byte, sample nat.Sample, err error) {
	event := NATPunchProcessEvent{Kind: "mapped", Title: "STUN 事务匹配，取得映射端点",
		SocketID: c.id, Round: c.round, Trial: c.trial, ServerAddress: server.String(),
		TransactionID: hex.EncodeToString(transaction[:]), ChangeRequest: int(change), AddressSource: "stun"}
	if err != nil {
		event.Kind, event.Title, event.Detail = "sample_error", "STUN 事务未完成", err.Error()
	} else {
		event.MappedAddress, event.RemoteAddress, event.Validation = sample.Mapped.String(), sample.Source.String(), "transaction_matched"
		if sample.Other.IsValid() {
			event.OtherAddress = sample.Other.String()
		}
	}
	c.process.record(event)
}

func (c *natProcessProbeConn) ObserveNATProbeStage(stage string) {
	title := map[string]string{
		"nat_base": "同一 socket 逐目标重复采样",
		"nat_extra": "使用新 socket 验证端口分配",
		"nat_filter_base": "新 socket 建立过滤探测基准",
		"nat_filter_ip_port": "请求从不同 IP 与端口响应",
		"nat_filter_port": "请求从同 IP 的不同端口响应",
		"nat_filter_alternate": "确认备用 IP 的连通性",
		"nat_classify": "汇总映射与过滤证据",
		"nat_validate": "在额外 socket 上验证分配步长",
		"nat_reuse": "重复访问同一目标确认映射稳定",
	}[stage]
	if title == "" {
		return
	}
	role := "nat_base"
	switch stage {
	case "nat_extra", "nat_validate", "nat_reuse":
		role = "nat_validation"
	case "nat_filter_base", "nat_filter_ip_port", "nat_filter_port", "nat_filter_alternate":
		role = "nat_filtering"
	}
	c.process.socket(c.PacketConn, role, c.round, c.trial)
	c.process.record(NATPunchProcessEvent{Kind: "stage", Stage: stage, Title: title, SocketID: c.id, Round: c.round, Trial: c.trial})
}

type natProcessNetwork struct {
	nat.Network
	process *natProcessRun
	round   int
	trial   int
}

// Embedding the entire platform UDP interface preserves its supported methods;
// raw sockets remain tracked by iceNetwork for QUIC tuning.
type natProcessICEConn struct {
	transport.UDPConn
	observer *natProcessProbeConn
}

func (c *natProcessICEConn) observe(direction string, data []byte, address net.Addr, err error) {
	if c.observer.process.active.Load() && stun.IsMessage(data) {
		c.observer.packet(direction, data, address, err)
	}
}
func (c *natProcessICEConn) ReadFrom(data []byte) (int, net.Addr, error) {
	n, from, err := c.UDPConn.ReadFrom(data)
	c.observe("in", data[:n], from, err)
	return n, from, err
}
func (c *natProcessICEConn) ReadFromUDP(data []byte) (int, *net.UDPAddr, error) {
	n, from, err := c.UDPConn.ReadFromUDP(data)
	c.observe("in", data[:n], from, err)
	return n, from, err
}
func (c *natProcessICEConn) WriteTo(data []byte, to net.Addr) (int, error) {
	n, err := c.UDPConn.WriteTo(data, to)
	c.observe("out", data[:n], to, err)
	return n, err
}
func (c *natProcessICEConn) WriteToUDP(data []byte, to *net.UDPAddr) (int, error) {
	n, err := c.UDPConn.WriteToUDP(data, to)
	c.observe("out", data[:n], to, err)
	return n, err
}
func (c *natProcessICEConn) ReadMsgUDP(data, oob []byte) (int, int, int, *net.UDPAddr, error) {
	n, oobn, flags, from, err := c.UDPConn.ReadMsgUDP(data, oob)
	c.observe("in", data[:n], from, err)
	return n, oobn, flags, from, err
}
func (c *natProcessICEConn) WriteMsgUDP(data, oob []byte, to *net.UDPAddr) (int, int, error) {
	n, oobn, err := c.UDPConn.WriteMsgUDP(data, oob, to)
	c.observe("out", data[:n], to, err)
	return n, oobn, err
}
func (c *natProcessICEConn) Close() error {
	err := c.UDPConn.Close()
	c.observer.process.record(NATPunchProcessEvent{Kind: "socket_closed", Title: "ICE socket 已关闭", SocketID: c.observer.id})
	return err
}

func (n *natProcessNetwork) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	pc, err := n.Network.ListenPacket(ctx, network, address)
	if err == nil {
		n.process.socket(pc, "nat_probe", n.round, n.trial)
		return n.process.probeConn(pc, n.round, n.trial), nil
	}
	n.process.record(NATPunchProcessEvent{Kind: "socket_error", Title: "创建采样 socket 失败",
		Round: n.round, Trial: n.trial, Detail: err.Error()})
	return nil, err
}

func (n *natProcessNetwork) resolve(ctx context.Context, address string, ipv4 bool) (*net.UDPAddr, error) {
	resolve := n.Network.ResolveUDP
	if source, ok := n.Network.(interface {
		ResolveUDP4(context.Context, string) (*net.UDPAddr, error)
	}); ok && ipv4 {
		resolve = source.ResolveUDP4
	}
	n.process.record(NATPunchProcessEvent{Kind: "dns_start", Title: "解析 STUN 地址", Round: n.round, Trial: n.trial, Detail: address})
	endpoint, err := resolve(ctx, address)
	event := NATPunchProcessEvent{Kind: "dns_result", Title: "STUN 地址解析完成", Round: n.round, Trial: n.trial, Detail: address}
	if endpoint != nil {
		event.RemoteAddress = endpoint.String()
	}
	if err != nil {
		event.Title, event.Detail = "STUN 地址解析失败", address+" · "+err.Error()
	}
	n.process.record(event)
	return endpoint, err
}

func (n *natProcessNetwork) ResolveUDP(ctx context.Context, address string) (*net.UDPAddr, error) {
	return n.resolve(ctx, address, false)
}

func (n *natProcessNetwork) ResolveUDP4(ctx context.Context, address string) (*net.UDPAddr, error) {
	return n.resolve(ctx, address, true)
}

func (n *natProcessNetwork) DialUDP(ctx context.Context, address *net.UDPAddr) (*net.UDPConn, error) {
	if source, ok := n.Network.(interface {
		DialUDP(context.Context, *net.UDPAddr) (*net.UDPConn, error)
	}); ok {
		return source.DialUDP(ctx, address)
	}
	return nil, fmt.Errorf("selected route endpoint unavailable")
}
