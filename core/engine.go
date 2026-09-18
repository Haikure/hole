// Package core implements the shared CLI/Android port-forwarding engine.
// Construction, validation and stopped snapshots have no network side effects.
package core

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sync"
	"time"
)

type Options struct {
	Platform      Platform
	EventCapacity int
	// Mobile hosts keep the requested run alive while connectivity is absent.
	RetryNetwork bool
}

// Request is a complete immutable effective configuration, not a partial edit.
// Android-only fields such as enabled and entry_id must be removed by the host.
type Request struct {
	ServerURL string
	Config    Config
}

func (r Request) Validate() error {
	_, err := r.validated()
	return err
}

func (r Request) validated() (Request, error) {
	u, err := url.Parse(r.ServerURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil || u.Fragment != "" {
		return Request{}, &Fault{Code: "invalid_config", Message: "server_url 需要完整 ws:// 或 wss:// URL，凭据使用独立字段"}
	}
	r.Config = r.Config.normalized()
	if r.Config.Transport.Preferred == PreferredICE && u.Scheme != "wss" && !r.Config.Transport.AllowInsecureSignal {
		return Request{}, &Fault{Code: "insecure_signal", Message: "ICE 使用 wss:// 信令；本地 ws:// 测试需显式开启 transport.allow_insecure_signal"}
	}
	if err := r.Config.Validate(); err != nil {
		return Request{}, &Fault{Code: "invalid_config", Message: redact(err.Error(), r.Config)}
	}
	return r, nil
}

type engineRun struct {
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	agent      *Agent // protected by Engine.mu
	err        error  // published by closing done
	generation uint64
	changes    chan struct{}
	epoch      uint64 // invalidates callbacks before a replacement starts
	sessions   *sessionManager
	ice        *iceCoordinator
	runtimeID  string
}

type Engine struct {
	operations          sync.Mutex
	mu                  sync.Mutex
	platform            Platform
	events              chan Event
	closed              bool
	request             *Request
	run                 *engineRun
	requested           bool
	state               string
	signal              string
	generation          uint64
	sequence            uint64
	dropped             uint64
	lastError           *Fault
	mappings            map[string]MappingSnapshot
	retryNetwork        bool
	transportGeneration uint64
	networkChanges      uint64
	reconnects          uint64
	startedAt           string
	network             NetworkSnapshot
}

func NewEngine(options Options) *Engine {
	capacity := options.EventCapacity
	if capacity == 0 {
		capacity = 256
	}
	capacity = max(1, min(capacity, 1024))
	platform := options.Platform
	if platform == nil {
		platform = DefaultPlatform{}
	}
	return &Engine{
		platform: platform, events: make(chan Event, capacity),
		retryNetwork: options.RetryNetwork,
		state:        "stopped", signal: "disconnected", mappings: make(map[string]MappingSnapshot),
	}
}

func (e *Engine) Events() <-chan Event { return e.events }

// Start validates synchronously and starts network work asynchronously. An
// identical repeat is idempotent. Acceptance is not a signal/mapping connection.
func (e *Engine) Start(request Request) error {
	request, err := request.validated()
	if err != nil {
		return err
	}
	e.operations.Lock()
	defer e.operations.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	if e.run != nil {
		select {
		case <-e.run.done:
		default:
			if reflect.DeepEqual(e.request, &request) {
				return nil
			}
			return ErrRunning
		}
	}
	e.setRequestLocked(request)
	e.generation++
	ctx, cancel := context.WithCancel(context.Background())
	id, _ := newSessionID()
	run := &engineRun{ctx: ctx, cancel: cancel, done: make(chan struct{}), generation: e.generation, changes: make(chan struct{}, 1), runtimeID: id.String()}
	e.run = run
	e.requested, e.state, e.signal = true, "starting", "disconnected"
	e.lastError = nil
	e.startedAt = time.Now().UTC().Format(time.RFC3339)
	e.publishLocked(Event{Kind: "engine", State: e.state})
	go e.execute(run, request)
	return nil
}

func (e *Engine) finishRun(run *engineRun, err error) {
	defer close(run.done)
	defer run.cancel()
	e.mu.Lock()
	defer e.mu.Unlock()
	run.agent = nil
	run.sessions = nil
	e.signal = "disconnected"
	if run.ctx.Err() != nil || errors.Is(err, context.Canceled) {
		e.state = "stopped"
		run.err = context.Canceled
	} else if err != nil {
		e.state = "error"
		e.lastError = classifyError(err)
		e.lastError.Message = redact(e.lastError.Message, e.request.Config)
		run.err = e.lastError
	} else {
		e.state = "stopped"
		e.requested = false
	}
	e.publishLocked(Event{Kind: "engine", State: e.state, Error: e.lastError})
}

// Wait is intended for CLI hosts. Canceling its context explicitly stops Engine.
func (e *Engine) Wait(ctx context.Context) error {
	e.mu.Lock()
	run := e.run
	e.mu.Unlock()
	if run == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		_ = e.Stop()
		return ctx.Err()
	case <-run.done:
		return run.err
	}
}

// Stop waits for network and application sockets to close. It is repeatable;
// a later Start is a new run with a new certificate and session scope.
func (e *Engine) Stop() error {
	e.operations.Lock()
	defer e.operations.Unlock()
	e.stop()
	return nil
}

func (e *Engine) stop() {
	e.mu.Lock()
	run := e.run
	e.requested = false
	if run != nil {
		e.state = "stopping"
		run.cancel()
	}
	e.mu.Unlock()
	if run != nil {
		<-run.done
	}
	e.mu.Lock()
	e.state, e.signal = "stopped", "disconnected"
	e.mu.Unlock()
}

// Close permanently retires this instance and its event channel. Late network
// events are generation/cancellation checked before touching the channel.
func (e *Engine) Close() error {
	e.operations.Lock()
	defer e.operations.Unlock()
	e.stop()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		close(e.events)
	}
	return nil
}

// ApplyConfig accepts an immutable desired snapshot. The run supervisor drains
// the previous transport before a full authenticated rejoin, retaining only
// application sessions whose configuration and connection scope still match.
func (e *Engine) ApplyConfig(request Request) error {
	request, err := request.validated()
	if err != nil {
		return err
	}
	e.operations.Lock()
	defer e.operations.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	if e.request != nil {
		if sameEffective(*e.request, request) {
			e.request = &request
			e.publishLocked(Event{Kind: "config", State: "unchanged"})
			return nil
		}
	}
	previous := e.request
	e.setRequestLocked(request)
	e.lastError = nil
	if e.requested && e.run != nil {
		e.state, e.signal = "reconfiguring", "reconnecting"
		if previous != nil && e.run.ice != nil && sameScope(*previous, request) {
			e.run.ice.submit(request)
		} else {
			e.requestCycleLocked()
		}
		e.publishLocked(Event{Kind: "config", State: "applying"})
	} else {
		e.publishLocked(Event{Kind: "config", State: "staged"})
	}
	return nil
}

func (e *Engine) NetworkChanged() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	if !e.requested || e.run == nil || e.run.ctx.Err() != nil {
		return nil
	}
	e.networkChanges++
	e.state, e.signal = "recovering", "reconnecting"
	if e.run.ice != nil {
		e.transportGeneration++
		e.run.ice.networkChanged()
	} else {
		e.requestCycleLocked()
	}
	e.publishLocked(Event{Kind: "network", State: "recovering"})
	return nil
}

func (e *Engine) requestCycleLocked() {
	e.run.epoch++
	select {
	case e.run.changes <- struct{}{}:
	default:
	}
}

func sameEffective(a, b Request) bool {
	return a.ServerURL == b.ServerURL && reflect.DeepEqual(a.Config, b.Config)
}

func (e *Engine) setRequestLocked(request Request) {
	e.request = &request
	e.mappings = make(map[string]MappingSnapshot)
	for _, item := range request.Config.Provide {
		e.mappings[item.ID] = MappingSnapshot{ID: item.ID, Role: "provide", Protocol: item.Service.Protocol, State: "waiting_peer", Endpoint: endpointString(item.Service.Addr, item.Service.Port)}
	}
	for _, item := range request.Config.Consume {
		e.mappings[item.ID] = MappingSnapshot{ID: item.ID, Role: "consume", State: "waiting_peer", Endpoint: endpointString(item.Expose.Addr, item.Expose.Port)}
	}
}

func (e *Engine) agentEvent(run *engineRun, event Event) {
	e.agentEventEpoch(run, nil, event)
}

func (e *Engine) agentEventEpoch(run *engineRun, epoch *uint64, event Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.run != run || run.ctx.Err() != nil || (epoch != nil && *epoch != run.epoch) {
		return
	}
	event.Message = redact(event.Message, e.request.Config)
	if event.Error != nil {
		copy := *event.Error
		copy.Message = redact(copy.Message, e.request.Config)
		event.Error = &copy
	}
	switch event.Kind {
	case "network":
		if event.State == "changed" {
			e.networkChanges++
			e.transportGeneration++
		}
	case "signal":
		previousState := e.state
		if event.State == "reconnecting" {
			e.reconnects++
		}
		e.signal = event.State
		if event.State == "joined" {
			e.state = "running"
			e.lastError = nil
		} else {
			// A signal reconnect invalidates the previous ready state. Keep
			// startup/reconfiguration labels while they are in progress, but
			// expose recovery once an established run loses its join.
			if e.state == "running" {
				e.state = "recovering"
			}
		}
		if event.Error != nil {
			e.lastError = event.Error
		}
		if previousState != e.state {
			e.publishLocked(Event{Kind: "engine", State: e.state, Error: e.lastError})
		}
	case "mapping":
		if mapping, ok := e.mappings[event.MappingID]; ok {
			mapping.State, mapping.Error = event.State, event.Error
			if event.Protocol != "" {
				mapping.Protocol = event.Protocol
			}
			if event.Peer != "" {
				mapping.Peer = event.Peer
			}
			if event.Path != "" {
				mapping.Path = event.Path
			}
			if event.Profile != "" {
				mapping.Profile = event.Profile
			}
			e.mappings[event.MappingID] = mapping
		}
	case "session":
		if mapping, ok := e.mappings[event.MappingID]; ok {
			// Keep path readiness separate from a failed application socket.
			if event.Error != nil || event.State == "active" {
				mapping.Error = event.Error
			}
			e.mappings[event.MappingID] = mapping
		}
	}
	e.publishLocked(event)
}

func (e *Engine) publishLocked(event Event) {
	if e.closed {
		return
	}
	e.sequence++
	event.Sequence, event.Generation = e.sequence, e.generation
	event.Time = time.Now().UTC().Format(time.RFC3339Nano)
	if event.Error != nil {
		copy := *event.Error
		event.Error = &copy
	}
	select {
	case e.events <- event:
	default:
		e.dropped++
	}
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	s := Snapshot{
		APIVersion: APIVersion, SessionProtocol: sessionProtocol, CoreVersion: CoreVersion,
		Configured: e.request != nil, RunRequested: e.requested,
		EngineState: e.state, SignalState: e.signal, Generation: e.generation,
		EventsDropped:       e.dropped,
		TransportGeneration: e.transportGeneration, NetworkChanges: e.networkChanges, Reconnects: e.reconnects,
		StartedAt: e.startedAt, Network: e.network.clone(),
		Mappings: []MappingSnapshot{}, PeerTransports: []PeerTransportSnapshot{}, Capabilities: Capabilities{Transport: "IPv6 / QUIC / hole-v2", RequiresIPv6: true, LiveConfiguration: true},
	}
	if p, ok := e.platform.(NetworkSnapshotter); ok {
		s.Capabilities.NetworkBinding = p.NetworkBinding()
	}
	if e.lastError != nil {
		copy := *e.lastError
		s.Error = &copy
	}
	if e.request == nil {
		e.mu.Unlock()
		return s
	}
	if e.request.Config.Transport.Preferred == PreferredICE {
		s.Capabilities.Transport = "ICE / QUIC / mux-v1"
		s.Capabilities.RequiresIPv6 = false
	}
	var manager *sessionManager
	var coordinator *iceCoordinator
	if e.run != nil {
		manager = e.run.sessions
		coordinator = e.run.ice
	}
	appendMapping := func(id string) {
		mapping := e.mappings[id]
		if mapping.Error != nil {
			copy := *mapping.Error
			mapping.Error = &copy
		}
		s.Mappings = append(s.Mappings, mapping)
	}
	for _, item := range e.request.Config.Provide {
		appendMapping(item.ID)
	}
	for _, item := range e.request.Config.Consume {
		appendMapping(item.ID)
	}
	e.mu.Unlock()
	activeMappings := map[string]bool{}
	if coordinator != nil {
		s.PeerTransports = coordinator.snapshot()
		activeMappings = coordinator.activeMappings()
	}
	// Never hold Engine.mu while taking session locks: session logging can emit
	// an Engine event while already holding a session-manager lock.
	stats := map[string]mappingStats{}
	if manager != nil {
		stats = manager.snapshot()
	}
	for i := range s.Mappings {
		mapping := &s.Mappings[i]
		mapping.TCPSessions, mapping.UDPSessions = stats[mapping.ID].tcp, stats[mapping.ID].udp
		mapping.TCPReadBytes, mapping.TCPWrittenBytes, mapping.ReplayBytes = stats[mapping.ID].read, stats[mapping.ID].written, stats[mapping.ID].buffered
		if activeMappings[mapping.ID] || (stats[mapping.ID].active && s.SignalState == "joined") {
			mapping.State = "active"
			if !isSessionFault(mapping.Error) {
				mapping.Error = nil
			}
		} else if mapping.State == "active" {
			mapping.State = "connecting"
		}
		if s.EngineState == "stopped" || s.EngineState == "error" {
			mapping.State = "stopped"
		}
	}
	return s
}
