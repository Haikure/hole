package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"hole/core"
)

// NetworkProvider is implemented by the Android host using public platform
// APIs. Return a JSON array of interface snapshots promptly, from any background
// thread. Do not call Engine lifecycle methods synchronously from this callback.
// A Java exception becomes the error result; errors never fall back to netlink.
type NetworkProvider interface {
	InterfacesJSON() (string, error)
}

// NewEngineWithNetworkProvider is the Android constructor. Construction does
// not enumerate interfaces; Start and subsequent candidate refreshes do so.
// This is an additive API 1 extension. NewEngine retains its desktop behavior.
func NewEngineWithNetworkProvider(events EventSink, provider NetworkProvider) (*Engine, error) {
	if provider == nil {
		return nil, bridgeError(&core.Fault{Code: "network_provider_missing", Message: "Android 网络接口未初始化"})
	}
	platform := &networkPlatform{provider: provider}
	e := newEngine(events, platform)
	e.network = platform
	return e, nil
}

type networkPlatform struct {
	core.DefaultPlatform
	mu             sync.Mutex
	provider       NetworkProvider
	binding        NetworkBinding
	bindingEnabled bool
	lookupSlots    chan struct{}
}

func (p *networkPlatform) Candidates(ctx context.Context, cfg core.Config, port int) ([]core.Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(cfg.CandidateAddresses) > 0 {
		return core.CandidatesFromInterfaces(cfg, port, nil)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.provider == nil {
		return nil, core.ErrClosed
	}
	text, err := p.provider.InterfacesJSON()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, &core.Fault{Code: "network_interfaces_failed", Message: fmt.Sprintf("读取 Android 网络地址失败：%v", err)}
	}
	if len(text) > 128*1024 {
		return nil, &core.Fault{Code: "network_interfaces_invalid", Message: "Android 网络地址快照超过 128 KiB"}
	}
	var interfaces []core.InterfaceSnapshot
	if err := json.Unmarshal([]byte(text), &interfaces); err != nil || interfaces == nil {
		return nil, &core.Fault{Code: "network_interfaces_invalid", Message: "Android 网络地址快照格式错误"}
	}
	return core.CandidatesFromInterfaces(cfg, port, interfaces)
}

func (p *networkPlatform) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.provider = nil
	p.binding = nil
}
