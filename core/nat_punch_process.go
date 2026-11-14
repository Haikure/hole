package core

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	natProcessEventLimit = 4096
	natProcessStepLimit  = 512
	natProcessRunLimit   = 6
)

// NATPunchProcess is read separately from the ordinary runtime snapshot. Events
// are incremental; addresses always retain their observation source.
type NATPunchProcess struct {
	TransportID   string                 `json:"transport_id"`
	PeerID        string                 `json:"peer_id"`
	Sequence      uint64                 `json:"sequence,string"`
	EventsDropped uint64                 `json:"events_dropped,string"`
	EventsEvicted uint64                 `json:"events_evicted,string"`
	Attempts      []NATPunchProcessRun   `json:"attempts"`
	Events        []NATPunchProcessEvent `json:"events"`
}

type NATPunchProcessRun struct {
	Generation uint64                  `json:"generation,string"`
	Phase      string                  `json:"phase"`
	StartedAt  string                  `json:"started_at"`
	FinishedAt string                  `json:"finished_at,omitempty"`
	ElapsedMS  int64                   `json:"elapsed_ms,string"`
	State      string                  `json:"state"`
	Stage      string                  `json:"stage"`
	Network    NetworkSnapshot         `json:"network"`
	Rounds     []NATPunchProcessRound  `json:"rounds"`
	Sockets    []NATPunchProcessSocket `json:"sockets"`
	Discovery  *NATPunchProcess        `json:"discovery,omitempty"`
}

type NATPunchProcessRound struct {
	Round          int                 `json:"round"`
	Trial          int                 `json:"trial"`
	Mode           string              `json:"mode"`
	Stage          string              `json:"stage"`
	LocalModel     NATSnapshot         `json:"local_model"`
	PeerModel      NATSnapshot         `json:"peer_model"`
	LocalPlan      *natPunchPlan       `json:"local_plan,omitempty"`
	PeerPlan       *natPunchPlan       `json:"peer_plan,omitempty"`
	Targets        []string            `json:"targets"`
	Dialer         bool                `json:"dialer"`
	SelectedSocket string              `json:"selected_socket,omitempty"`
	Diagnostic     *NATPunchDiagnostic `json:"diagnostic,omitempty"`
}

func (v NATPunchProcessRound) clone() NATPunchProcessRound {
	v.LocalModel.IPEvidence = append(v.LocalModel.IPEvidence[:0:0], v.LocalModel.IPEvidence...)
	v.PeerModel.IPEvidence = append(v.PeerModel.IPEvidence[:0:0], v.PeerModel.IPEvidence...)
	v.Targets = append([]string{}, v.Targets...)
	if v.LocalPlan != nil {
		plan := *v.LocalPlan
		v.LocalPlan = &plan
	}
	if v.PeerPlan != nil {
		plan := *v.PeerPlan
		v.PeerPlan = &plan
	}
	if v.Diagnostic != nil {
		diagnostic := *v.Diagnostic
		v.Diagnostic = &diagnostic
	}
	return v
}

type NATPunchProcessSocket struct {
	ID                 string `json:"id"`
	Round              int    `json:"round"`
	Trial              int    `json:"trial"`
	Role               string `json:"role"`
	BoundAddress       string `json:"bound_address"`
	LocalAddress       string `json:"local_address,omitempty"`
	LocalAddressSource string `json:"local_address_source,omitempty"`
	MappedAddress      string `json:"mapped_address,omitempty"`
	MappingSource      string `json:"mapping_source,omitempty"`
	State              string `json:"state"`
	Target             string `json:"target,omitempty"`
	CreatedMS          int64  `json:"created_ms,string"`
	LastActivityMS     int64  `json:"last_activity_ms,string"`
	ProbesSent         int    `json:"probes_sent"`
	ACKsSent           int    `json:"acks_sent"`
	ProbesReceived     int    `json:"probes_received"`
	ACKsReceived       int    `json:"acks_received"`
}

type NATPunchProcessEvent struct {
	Sequence       uint64 `json:"sequence,string"`
	Generation     uint64 `json:"generation,string"`
	Time           string `json:"time"`
	ElapsedMS      int64  `json:"elapsed_ms,string"`
	Round          int    `json:"round"`
	Trial          int    `json:"trial"`
	Kind           string `json:"kind"`
	Stage          string `json:"stage,omitempty"`
	Title          string `json:"title"`
	SocketID       string `json:"socket_id,omitempty"`
	Direction      string `json:"direction,omitempty"`
	Packet         string `json:"packet,omitempty"`
	PacketSequence uint32 `json:"packet_sequence,omitempty"`
	RetryAfterMS   int64  `json:"retry_after_ms,omitempty"`
	ProbeLimit     int    `json:"probe_limit,omitempty"`
	PeerSocket     int    `json:"peer_socket,omitempty"`
	LocalAddress   string `json:"local_address,omitempty"`
	BoundAddress   string `json:"bound_address,omitempty"`
	RemoteAddress  string `json:"remote_address,omitempty"`
	MappedAddress  string `json:"mapped_address,omitempty"`
	AddressSource  string `json:"address_source,omitempty"`
	LocalAddressSource string `json:"local_address_source,omitempty"`
	SocketRole string `json:"socket_role,omitempty"`
	Bytes          int    `json:"bytes,omitempty"`
	Validation     string `json:"validation,omitempty"`
	Detail         string `json:"detail,omitempty"`
	TransactionID  string `json:"transaction_id,omitempty"`
	ChangeRequest  int    `json:"change_request,omitempty"`
	ServerAddress  string `json:"server_address,omitempty"`
	OtherAddress   string `json:"other_address,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Result         string `json:"result,omitempty"`
	Model          *NATSnapshot `json:"model,omitempty"`
	Plan           *natPunchPlan `json:"plan,omitempty"`
	RoundSnapshot  *NATPunchProcessRound `json:"round_snapshot,omitempty"`
}

func (v NATPunchProcessEvent) clone() NATPunchProcessEvent {
	if v.Model != nil {
		model := *v.Model
		model.IPEvidence = append(model.IPEvidence[:0:0], model.IPEvidence...)
		v.Model = &model
	}
	if v.Plan != nil {
		plan := *v.Plan
		v.Plan = &plan
	}
	if v.RoundSnapshot != nil {
		round := v.RoundSnapshot.clone()
		v.RoundSnapshot = &round
	}
	return v
}

type natProcessRecorder struct {
	mu        sync.Mutex
	sequence  uint64
	transport string
	peer      string
	runs      []*natProcessRun
	events    []NATPunchProcessEvent
	head      int
	steps     []NATPunchProcessEvent
	evicted   uint64
	dropped   atomic.Uint64
}

type natProcessRun struct {
	owner     *natProcessRecorder
	data      NATPunchProcessRun
	started   time.Time
	ended     time.Time
	enabled   bool
	active    atomic.Bool
	sockets   map[string]int
	socketIndices map[string]int
	discovery *natProcessRecorder
}

func (r *natProcessRecorder) resetLocked() {
	for _, run := range r.runs {
		run.enabled = false
		run.active.Store(false)
	}
	r.runs, r.events, r.steps = nil, nil, nil
	r.head, r.evicted = 0, 0
	r.dropped.Store(0)
}

func (r *natProcessRecorder) reset() {
	r.mu.Lock()
	r.resetLocked()
	r.mu.Unlock()
}

func (r *natProcessRecorder) begin(ready iceSignalMessage) *natProcessRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transport, r.peer = ready.TransportID, ready.PeerDevice
	if len(r.runs) >= natProcessRunLimit {
		r.runs[0].enabled = false
		r.runs[0].active.Store(false)
		r.runs = r.runs[1:]
	}
	phase := ready.Phase
	if ready.Phase == "direct" && ready.DirectStage != "" {
		phase = ready.DirectStage
	}
	now := time.Now()
	run := &natProcessRun{owner: r, started: now, enabled: true, sockets: make(map[string]int), socketIndices: make(map[string]int),
		data: NATPunchProcessRun{Generation: ready.TransportGeneration, Phase: phase,
			StartedAt: now.UTC().Format(time.RFC3339Nano), State: "running", Stage: "preparing",
			Rounds: []NATPunchProcessRound{}, Sockets: []NATPunchProcessSocket{}}}
	r.runs = append(r.runs, run)
	run.active.Store(true)
	run.recordLocked(NATPunchProcessEvent{Kind: "stage", Stage: "preparing", Title: "开始连接尝试"}, now)
	return run
}

func (t *natProcessRun) roundLocked(round int) *NATPunchProcessRound {
	for i := range t.data.Rounds {
		if t.data.Rounds[i].Round == round {
			return &t.data.Rounds[i]
		}
	}
	t.data.Rounds = append(t.data.Rounds, NATPunchProcessRound{Round: round, Targets: []string{}})
	return &t.data.Rounds[len(t.data.Rounds)-1]
}

func (t *natProcessRun) recordLocked(event NATPunchProcessEvent, now time.Time) {
	if !t.enabled {
		return
	}
	r := t.owner
	r.sequence++
	event.Sequence, event.Generation = r.sequence, t.data.Generation
	event.Time, event.ElapsedMS = now.UTC().Format(time.RFC3339Nano), now.Sub(t.started).Milliseconds()
	if event.SocketID != "" {
		if i, ok := t.socketIndices[event.SocketID]; ok {
			s := &t.data.Sockets[i]
			if event.Round == 0 {
				event.Round = s.Round
			}
			if event.Trial == 0 {
				event.Trial = s.Trial
			}
			event.BoundAddress = s.BoundAddress
			event.SocketRole, event.LocalAddressSource = s.Role, s.LocalAddressSource
			if event.LocalAddress == "" {
				event.LocalAddress = s.LocalAddress
			}
			if event.AddressSource == "" {
				event.AddressSource = s.LocalAddressSource
			}
			s.LastActivityMS = event.ElapsedMS
			if event.RemoteAddress != "" {
				s.Target = event.RemoteAddress
			}
			if event.Kind == "mapped" {
				s.MappedAddress, s.MappingSource = event.MappedAddress, event.AddressSource
			}
			switch event.Kind {
			case "probe_sent":
				s.ProbesSent++
				if s.State == "bound" || s.State == "probing" {
					s.State = "probing"
				}
			case "ack_sent":
				s.ACKsSent++
			case "probe_received":
				s.ProbesReceived++
			case "ack_received":
				s.ACKsReceived++
			case "ack_verified":
				s.State = "verified"
			case "quic_started", "quic_established":
				s.State = "quic"
			case "socket_selected":
				s.State = "selected"
				if event.LocalAddress != "" {
					s.LocalAddress, s.LocalAddressSource = event.LocalAddress, event.AddressSource
					event.LocalAddressSource = s.LocalAddressSource
				}
			case "socket_retained":
				s.State = "retained"
			case "socket_closed":
				s.State = "closed"
			}
		}
	}
	if event.Kind == "stage" {
		t.data.Stage = event.Stage
		if event.Round > 0 {
			v := t.roundLocked(event.Round)
			v.Stage, v.Trial = event.Stage, event.Trial
			value := v.clone()
			event.RoundSnapshot = &value
		}
	} else if event.Round > 0 && event.Trial == 0 {
		event.Trial = t.roundLocked(event.Round).Trial
	}
	if len(r.events) < natProcessEventLimit {
		r.events = append(r.events, event)
	} else {
		r.events[r.head] = event
		r.head = (r.head + 1) % natProcessEventLimit
		r.evicted++
	}
	if event.Direction == "" || event.Packet == "" {
		if len(r.steps) >= natProcessStepLimit {
			copy(r.steps, r.steps[1:])
			r.steps = r.steps[:len(r.steps)-1]
		}
		r.steps = append(r.steps, event)
	}
}

func (t *natProcessRun) record(event NATPunchProcessEvent) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	t.recordLocked(event, time.Now())
	t.owner.mu.Unlock()
}

// A telemetry reader never stalls the packet loop. Loss is exposed explicitly.
func (t *natProcessRun) packet(event NATPunchProcessEvent) {
	if t == nil || !t.active.Load() {
		return
	}
	if !t.owner.mu.TryLock() {
		t.owner.dropped.Add(1)
		return
	}
	t.recordLocked(event, time.Now())
	t.owner.mu.Unlock()
}

func (a *iceAttempt) processStep(stage, title string, round, trial int) {
	a.process.record(NATPunchProcessEvent{Kind: "stage", Stage: stage, Title: title, Round: round, Trial: trial})
}

func (t *natProcessRun) network(snapshot NetworkSnapshot) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	t.data.Network = snapshot.clone()
	t.owner.mu.Unlock()
}

func (t *natProcessRun) sampling(discovery *natProcessRecorder) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	t.discovery = discovery
	t.owner.mu.Unlock()
}

func (t *natProcessRun) models(round int, mode string, local, peer NATSnapshot) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	v := t.roundLocked(round)
	v.Mode, v.LocalModel, v.PeerModel = mode, local.Current(), peer.Current()
	value := v.clone()
	t.recordLocked(NATPunchProcessEvent{Kind: "model", Stage: "model", Round: round,
		Title: "双端 NAT 模型已记录", Mode: mode, RoundSnapshot: &value}, time.Now())
	t.owner.mu.Unlock()
}

func (t *natProcessRun) plans(round, trial int, local, peer *natPunchPlan) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	v := t.roundLocked(round)
	v.Trial = trial
	if local != nil {
		value := *local
		v.LocalPlan = &value
	}
	if peer != nil {
		value := *peer
		v.PeerPlan = &value
	}
	value := v.clone()
	t.recordLocked(NATPunchProcessEvent{Kind: "plan", Title: "双方端口计划已确认", Round: round, Trial: trial, RoundSnapshot: &value}, time.Now())
	t.owner.mu.Unlock()
}

func (t *natProcessRun) targets(round int, targets []*net.UDPAddr, dialer bool) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	v := t.roundLocked(round)
	v.Targets, v.Dialer = make([]string, len(targets)), dialer
	for i, target := range targets {
		v.Targets[i] = target.String()
	}
	value := v.clone()
	t.recordLocked(NATPunchProcessEvent{Kind: "targets", Title: "确定本轮实际扫描端点", Round: round, Trial: v.Trial, RoundSnapshot: &value}, time.Now())
	t.owner.mu.Unlock()
}

func (t *natProcessRun) socket(pc net.PacketConn, role string, round, trial int) string {
	if t == nil || pc == nil {
		return ""
	}
	bound := pc.LocalAddr().String()
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	if i, ok := t.sockets[bound]; ok && t.data.Sockets[i].State != "closed" {
		s := &t.data.Sockets[i]
		changed := s.Role != role
		s.Role, s.Trial = role, trial
		if changed {
			t.recordLocked(NATPunchProcessEvent{Kind: "socket_role", Title: "更新 socket 用途", SocketID: s.ID, Round: round, Trial: trial, Detail: role}, time.Now())
		}
		return s.ID
	}
	if len(t.data.Sockets) >= 512 {
		return ""
	}
	i := len(t.data.Sockets)
	id := fmt.Sprintf("%d/%d", t.data.Generation, i+1)
	now := time.Now()
	s := NATPunchProcessSocket{ID: id, Role: role, Round: round, Trial: trial,
		BoundAddress: bound, State: "bound", CreatedMS: now.Sub(t.started).Milliseconds()}
	if endpoint, err := netip.ParseAddrPort(bound); err == nil && !endpoint.Addr().IsUnspecified() {
		s.LocalAddress, s.LocalAddressSource = bound, "socket"
	} else if err == nil {
		var selected netip.Addr
		for _, raw := range t.data.Network.Addresses {
			ip, e := netip.ParseAddr(raw)
			if e == nil && ip.Is4() == endpoint.Addr().Is4() && !ip.IsUnspecified() && !ip.IsMulticast() {
				if selected.IsValid() {
					selected = netip.Addr{}
					break
				}
				selected = ip
			}
		}
		if selected.IsValid() {
			s.LocalAddress = netip.AddrPortFrom(selected, endpoint.Port()).String()
			s.LocalAddressSource = "selected_network"
		}
	}
	t.sockets[bound] = i
	t.socketIndices[id] = i
	t.data.Sockets = append(t.data.Sockets, s)
	t.recordLocked(NATPunchProcessEvent{Kind: "socket_bound", Title: "UDP socket 已绑定",
		SocketID: id, Round: round, Trial: trial, Detail: role}, now)
	return id
}

func (t *natProcessRun) socketID(pc net.PacketConn) string {
	if t == nil || pc == nil {
		return ""
	}
	if observed, ok := pc.(*natProcessProbeConn); ok {
		return observed.id
	}
	return t.socketAddressID(pc.LocalAddr().String())
}

func (t *natProcessRun) socketAddressID(address string) string {
	if t == nil {
		return ""
	}
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	if i, ok := t.sockets[address]; ok {
		return t.data.Sockets[i].ID
	}
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil {
		return ""
	}
	id := ""
	for _, s := range t.data.Sockets {
		bound, err := netip.ParseAddrPort(s.BoundAddress)
		if err == nil && s.State != "closed" && bound.Port() == endpoint.Port() && bound.Addr().Is4() == endpoint.Addr().Is4() {
			if id != "" {
				return ""
			}
			id = s.ID
		}
	}
	return id
}

func (t *natProcessRun) mapped(pc net.PacketConn, endpoint, source string, round, trial int) {
	if t == nil {
		return
	}
	t.record(NATPunchProcessEvent{Kind: "mapped", Title: "取得映射端点", SocketID: t.socketID(pc),
		MappedAddress: endpoint, AddressSource: source, Round: round, Trial: trial})
}

func (t *natProcessRun) selected(pc net.PacketConn, local, remote string, round int) {
	t.selectedID(t.socketID(pc), local, remote, round)
}

func (t *natProcessRun) selectedID(id, local, remote string, round int) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	v := t.roundLocked(round)
	v.SelectedSocket = id
	value := v.clone()
	source := ""
	if local != "" {
		source = "selected_route"
	}
	t.recordLocked(NATPunchProcessEvent{Kind: "socket_selected", Title: "选中业务 socket",
		SocketID: id, LocalAddress: local, RemoteAddress: remote, AddressSource: source, Round: round, RoundSnapshot: &value}, time.Now())
	t.owner.mu.Unlock()
}

func (t *natProcessRun) diagnostic(d NATPunchDiagnostic) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	v := t.roundLocked(d.Round)
	copy := d
	v.Diagnostic, v.Stage = &copy, d.Stage
	value := v.clone()
	t.recordLocked(NATPunchProcessEvent{Kind: "round_summary", Title: "本轮收发统计已记录", Round: d.Round, RoundSnapshot: &value}, time.Now())
	t.owner.mu.Unlock()
}

func (t *natProcessRun) finish(state, detail string) {
	if t == nil {
		return
	}
	t.owner.mu.Lock()
	if t.ended.IsZero() {
		now := time.Now()
		t.ended = now
		t.active.Store(false)
		t.data.State, t.data.FinishedAt = state, now.UTC().Format(time.RFC3339Nano)
		t.recordLocked(NATPunchProcessEvent{Kind: "finished", Title: "连接尝试已结束", Result: state, Detail: detail}, now)
	}
	t.owner.mu.Unlock()
}

func (r *natProcessRecorder) snapshot(after uint64) NATPunchProcess {
	r.mu.Lock()
	if after > r.sequence {
		after = 0
	}
	result := NATPunchProcess{TransportID: r.transport, PeerID: r.peer, Sequence: r.sequence,
		EventsDropped: r.dropped.Load(), EventsEvicted: r.evicted,
		Attempts: []NATPunchProcessRun{}, Events: []NATPunchProcessEvent{}}
	valid := make(map[uint64]bool, len(r.runs))
	discoveries := make([]*natProcessRecorder, 0, len(r.runs))
	now := time.Now()
	for _, run := range r.runs {
		v := run.data
		valid[v.Generation] = true
		until := run.ended
		if until.IsZero() {
			until = now
		}
		v.ElapsedMS = until.Sub(run.started).Milliseconds()
		v.Network = v.Network.clone()
		v.Sockets = append([]NATPunchProcessSocket{}, v.Sockets...)
		v.Rounds = append([]NATPunchProcessRound{}, v.Rounds...)
		for i := range v.Rounds {
			v.Rounds[i] = v.Rounds[i].clone()
		}
		result.Attempts = append(result.Attempts, v)
		discoveries = append(discoveries, run.discovery)
	}
	seen := make(map[uint64]bool)
	appendEvent := func(event NATPunchProcessEvent) {
		if valid[event.Generation] && event.Sequence > after && !seen[event.Sequence] {
			seen[event.Sequence] = true
			result.Events = append(result.Events, event.clone())
		}
	}
	for _, event := range r.steps {
		appendEvent(event)
	}
	for i := range r.events {
		appendEvent(r.events[(r.head+i)%len(r.events)])
	}
	r.mu.Unlock()
	for i, discovery := range discoveries {
		if discovery != nil && discovery != r {
			value := discovery.snapshot(0)
			result.Attempts[i].Discovery = &value
		}
	}
	sort.Slice(result.Events, func(i, j int) bool { return result.Events[i].Sequence < result.Events[j].Sequence })
	return result
}

// NATPunchProcess reads one peer's bounded trace without waking or restarting
// any transport. after is the last sequence already consumed by the host.
func (e *Engine) NATPunchProcess(transportID string, after uint64) NATPunchProcess {
	e.mu.Lock()
	var coordinator *iceCoordinator
	if e.run != nil {
		coordinator = e.run.ice
	}
	e.mu.Unlock()
	if coordinator != nil {
		coordinator.mu.Lock()
		peer := coordinator.peers[transportID]
		coordinator.mu.Unlock()
		if peer != nil {
			return peer.process.snapshot(after)
		}
	}
	return NATPunchProcess{TransportID: transportID, Attempts: []NATPunchProcessRun{}, Events: []NATPunchProcessEvent{}}
}
