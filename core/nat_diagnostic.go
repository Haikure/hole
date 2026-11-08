package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/pion/stun/v4"
	"hole/core/internal/nat"
	"hole/core/natprobe"
)

// NATProbeOptions needs no room credentials and never starts forwarding.
type NATProbeOptions struct {
	STUNURLs           []string `json:"stun_urls"`
	NATSTUNURLs        []string `json:"nat_stun_urls"`
	InterfaceAllowlist []string `json:"interface_allowlist"`
}

func ParseNATProbeOptions(data []byte) (NATProbeOptions, error) {
	var options NATProbeOptions
	if len(data) > 16384 {
		return options, errors.New("NAT 检测参数过大")
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return options, errors.New("NAT 检测参数必须是对象")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&options); err != nil {
		return options, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return options, errors.New("NAT 检测参数必须是单个对象")
	}
	return options, nil
}

type DiagnosticField struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
}

type DiagnosticSection struct {
	Title  string            `json:"title"`
	Fields []DiagnosticField `json:"fields"`
}

type DiagnosticTile struct {
	Icon  string `json:"icon"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// Separate from automatic path classification: a manual result never changes
// transport policy or the evidence used by a live prediction round.
type NATDiagnostic struct {
	State      string              `json:"state"`
	StartedAt  string              `json:"started_at,omitempty"`
	FinishedAt string              `json:"finished_at,omitempty"`
	Result     NATSnapshot         `json:"result"`
	Network    NetworkSnapshot     `json:"network"`
	Servers    []string            `json:"servers"`
	Mappings   []string            `json:"mappings"`
	Targets    []nat.ProbeTarget   `json:"targets"`
	Title      string              `json:"title"`
	Summary    string              `json:"summary"`
	Tone       string              `json:"tone"`
	Badge      string              `json:"badge"`
	Highlights []DiagnosticTile    `json:"highlights"`
	Sections   []DiagnosticSection `json:"sections"`
}

func (e *Engine) StartNATProbe(options NATProbeOptions) error {
	cfg := Config{ICE: ICEConfig{STUNURLs: options.STUNURLs, NATSTUNURLs: options.NATSTUNURLs}}
	cfg.normalizeTransport()
	for _, list := range [][]string{options.STUNURLs, options.NATSTUNURLs} {
		if len(list) > 8 {
			return errors.New("NAT 检测最多接受 8 个 STUN 服务")
		}
		for _, raw := range list {
			u, err := stun.ParseURI(raw)
			if err != nil || u.Scheme != stun.SchemeTypeSTUN || u.Proto != stun.ProtoTypeUDP {
				return errors.New("NAT 检测只接受 UDP STUN 地址")
			}
		}
	}
	urls := natURLs(cfg.ICE)
	if len(urls) == 0 {
		return errors.New("NAT 检测服务已关闭，请在连接方式中配置 STUN 地址")
	}
	if len(urls) > 8 {
		urls = urls[:8]
	}
	e.operations.Lock()
	defer e.operations.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	if e.natProbeCancel != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	done := make(chan struct{})
	e.natProbeCancel, e.natProbeDone = cancel, done
	e.natDiagnostic = NATDiagnostic{State: "queued", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Servers: urls, Result: natUnknown()}
	e.publishLocked(Event{Kind: "nat_diagnostic", State: "queued"})
	allow := append([]string{}, options.InterfaceAllowlist...)
	go e.runNATProbe(ctx, cancel, done, urls, allow)
	return nil
}

func (e *Engine) CancelNATProbe() error {
	e.operations.Lock()
	defer e.operations.Unlock()
	e.cancelNATProbeAndWait()
	return nil
}

func (e *Engine) cancelNATProbeAndWait() {
	e.mu.Lock()
	cancel, done := e.natProbeCancel, e.natProbeDone
	if cancel != nil {
		cancel()
	}
	e.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (e *Engine) invalidateNATProbeLocked() {
	if e.natDiagnostic.State == "" {
		return
	}
	if e.natProbeCancel != nil {
		e.natProbeCancel()
	}
	e.natDiagnostic.State = "network_changed"
	e.publishLocked(Event{Kind: "nat_diagnostic", State: "network_changed"})
}

func (e *Engine) runNATProbe(ctx context.Context, cancel context.CancelFunc, done chan struct{}, urls, allow []string) {
	defer close(done)
	defer cancel()
	result := natUnknown()
	var mappings []string
	var targets []nat.ProbeTarget
	network := NetworkSnapshot{}
	state := "complete"
	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.natDiagnostic.State == "network_changed" {
			state = "network_changed"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			state = "cancelled"
		}
		e.natDiagnostic.State, e.natDiagnostic.Result, e.natDiagnostic.Network = state, result, network.clone()
		e.natDiagnostic.Mappings = mappings
		e.natDiagnostic.Targets = targets
		e.natDiagnostic.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		e.natProbeCancel, e.natProbeDone = nil, nil
		e.publishLocked(Event{Kind: "nat_diagnostic", State: state})
	}()
	release, err := e.natAllocation.acquire(ctx)
	if err != nil {
		state = "failed"
		result = nat.Unknown("等待正在进行的打洞操作超时或已取消")
		return
	}
	defer release()
	probeCtx, stop := context.WithTimeout(ctx, natprobe.DefaultTimeout)
	defer stop()
	platform := e.platform
	if source, ok := platform.(NetworkSnapshotter); ok {
		platform, network, err = source.SnapshotNetwork(probeCtx)
		if err != nil {
			state = "failed"
			result = nat.Unknown("当前没有可用于检测的网络")
			return
		}
	} else if source, ok := platform.(ICEPlatform); ok {
		interfaces, lookupErr := source.InterfaceSnapshots(probeCtx)
		if lookupErr != nil {
			state = "failed"
			result = nat.Unknown("无法读取当前网络接口")
			return
		}
		wanted := stringSet(allow)
		for _, iface := range interfaces {
			if !iface.Up || iface.Loopback || len(wanted) > 0 && !wanted[iface.Name] || len(wanted) == 0 && isDefaultExcludedInterface(iface.Name) {
				continue
			}
			for _, raw := range iface.Addresses {
				ip := net.ParseIP(raw)
				if ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() {
					network.Addresses = append(network.Addresses, iface.Name+": "+raw)
				}
			}
		}
		network.Transport, network.Interface, network.Available = "系统网络", "系统路由选择", len(network.Addresses) > 0
	}
	if source, ok := platform.(interface{ NATIPv4Available() bool }); ok && !source.NATIPv4Available() {
		state = "not_applicable"
		result = NATSnapshot{Status: state, Type: "unknown", Reason: "当前网络没有 IPv4 地址；IPv6 可用性请查看下方网络地址", ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		return
	}
	e.mu.Lock()
	if ctx.Err() == nil {
		e.natDiagnostic.State, e.natDiagnostic.Network = "probing", network.clone()
		e.publishLocked(Event{Kind: "nat_diagnostic", State: "probing"})
	}
	e.mu.Unlock()
	pc, err := platform.ListenPacket(probeCtx, "udp4", "0.0.0.0:0")
	if err != nil {
		state = "failed"
		result = nat.Unknown("无法在当前网络创建 UDP 检测连接")
		return
	}
	defer pc.Close()
	classified, observed, details := nat.ProbeDetailed(probeCtx, platform, pc, urls)
	result = classified
	targets = details
	for _, endpoint := range observed {
		mappings = append(mappings, endpoint.String())
	}
	if result.Status != "complete" {
		state = "insufficient"
	}
}
