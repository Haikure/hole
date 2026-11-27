package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"
)

type roomLink struct {
	PeerID      string `json:"peer_id"`
	RuntimeID   string `json:"runtime_id"`
	TransportID string `json:"transport_id"`
	Generation  uint64 `json:"generation,string"`
}

func validDeviceName(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, b := range []byte(value) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

type roomNode struct {
	DeviceName       string     `json:"device_name"`
	RuntimeID        string     `json:"runtime_id"`
	CoreVersion      string     `json:"core_version"`
	WorkerVersion    string     `json:"worker_version"`
	Epoch            uint64     `json:"transport_epoch,string"`
	Revision         uint64     `json:"revision,string"`
	SignalOnline     bool       `json:"signal_online"`
	SignalRecovering bool       `json:"signal_recovering"`
	Resident         bool       `json:"resident"`
	Neighbours       []roomLink `json:"neighbours"`
}

type roomGroup struct {
	ID        string   `json:"group_id"`
	Members   []string `json:"members"`
	Signaling []string `json:"signaling"`
}

type RoomSnapshot struct {
	DiscoverySupported bool                `json:"discovery_supported"`
	Services           []DiscoveredService `json:"services"`
	GroupID            string              `json:"group_id"`
	WorkerVersion      string              `json:"worker_version"`
	SignalVia          []string            `json:"signal_via"`
	Members            []voiceRoomMember   `json:"members"`
	Groups             []roomGroup         `json:"groups"`
}

type roomPeer interface {
	roomLink() roomLink
	sendRoomFrame(roomFrame) bool
}

type roomNodeRecord struct {
	node  roomNode
	frame roomFrame
}

type roomWakeWaiter struct {
	device  string
	runtime string
	done    chan struct{}
}

type roomPeerWake struct {
	runtime    string
	generation uint64
	until      time.Time
}

// The graph only contains existing, authenticated device connections. It never
// creates a connection just to join two components. Worker connectivity is a
// property of a reachable component, not a room-wide leader lock or lease.
type roomControl struct {
	owner            *iceCoordinator
	room             string
	device           string
	runtime          string
	certificate      string
	private          *ecdsa.PrivateKey
	mu               sync.Mutex
	workerKey        *ecdsa.PublicKey
	workerVersion    string
	directory        map[string]voiceRoomMember
	nodes            map[string]roomNodeRecord
	peers            map[string]roomPeer
	seen             map[string]bool
	seenOrder        []string
	recovery         map[string]uint64
	waking           map[string]roomWakeWaiter
	peerWakes        map[string]roomPeerWake
	sequence         uint64
	lastState        string
	lastDirectory    *roomFrame
	stateRevision    uint64
	remoteGroups     []roomGroup
	online           bool
	recoveringSignal bool
	synced           bool
	forceSignal      bool
	keepBackup       bool
	changed          chan struct{}
	wake             chan struct{}
	notices          chan iceSignalMessage
}

func newRoomControl(c *iceCoordinator) *roomControl {
	r := &roomControl{owner: c, room: c.request().Config.Room, device: c.deviceID(), runtime: c.runtimeID,
		directory: map[string]voiceRoomMember{}, nodes: map[string]roomNodeRecord{}, peers: map[string]roomPeer{},
		seen: map[string]bool{}, recovery: map[string]uint64{}, waking: map[string]roomWakeWaiter{}, peerWakes: map[string]roomPeerWake{}, changed: make(chan struct{}, 1), wake: make(chan struct{}, 1), notices: make(chan iceSignalMessage, 128)}
	if c.identity != nil && len(c.identity.Certificates) > 0 {
		r.private, _ = c.identity.Certificates[0].PrivateKey.(*ecdsa.PrivateKey)
		r.certificate = base64.StdEncoding.EncodeToString(c.identity.Certificates[0].Certificate[0])
	}
	r.directory[r.device] = voiceRoomMember{DeviceName: r.device, RuntimeID: r.runtime, CoreVersion: CoreVersion,
		Certificate: r.certificate, CertFingerprint: c.fingerprint, TransportEpoch: c.networkEpoch.Load()}
	return r
}

func (r *roomControl) enabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return modernSoftware(r.workerVersion) && r.workerKey != nil && r.private != nil
}

func (r *roomControl) joined(m iceSignalMessage) error {
	if !modernSoftware(m.WorkerVersion) {
		r.mu.Lock()
		r.workerVersion = m.WorkerVersion
		r.workerKey = nil
		r.online = true
		r.recoveringSignal = false
		r.mu.Unlock()
		r.publish()
		return nil
	}
	der, err := base64.StdEncoding.DecodeString(m.RoomPublicKey)
	if err != nil || len(der) > 256 {
		return &Fault{Code: "protocol_mismatch", Message: "协调服务没有提供有效的房间签名公钥"}
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	key, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok || key.Curve.Params().BitSize != 256 || r.private == nil {
		return &Fault{Code: "protocol_mismatch", Message: "房间控制签名算法不兼容"}
	}
	r.mu.Lock()
	// Only the direct, authenticated WSS handshake may establish this trust root.
	if r.workerKey == nil || !r.workerKey.Equal(key) {
		r.stateRevision = m.StateRevision
		r.lastDirectory = nil
		r.remoteGroups = nil
	} else {
		r.stateRevision = max(r.stateRevision, m.StateRevision)
	}
	r.workerKey, r.workerVersion = key, m.WorkerVersion
	r.online, r.synced = true, false
	r.recoveringSignal = false
	r.mu.Unlock()
	r.publish()
	return nil
}

func (r *roomControl) signalClosed(recovering bool) {
	r.mu.Lock()
	r.online, r.synced = false, false
	r.recoveringSignal = recovering
	r.mu.Unlock()
	r.publish()
}

func (r *roomControl) synchronized() {
	r.mu.Lock()
	r.synced, r.forceSignal = true, false
	r.recoveringSignal = false
	r.mu.Unlock()
	r.publish()
}

func (r *roomControl) requireSignal(backup bool) {
	r.mu.Lock()
	r.forceSignal = true
	r.keepBackup = r.keepBackup || backup
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
	r.notify()
}

func (r *roomControl) notify() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// Queue signaling recovery at each direct peer before a local reconfiguration
// closes the control edges used to reach suspended members.
func (r *roomControl) wakeExistingPeers() {
	if r == nil || !r.enabled() {
		return
	}
	r.mu.Lock()
	peers := make([]roomPeer, 0, len(r.peers))
	for _, peer := range r.peers {
		link := peer.roomLink()
		if node := r.nodes[link.PeerID].node; node.RuntimeID == link.RuntimeID && node.SignalOnline {
			continue
		}
		peers = append(peers, peer)
	}
	r.mu.Unlock()
	waits := make(map[string]roomWakeWaiter, len(peers))
	defer func() {
		r.mu.Lock()
		for id := range waits {
			delete(r.waking, id)
		}
		r.mu.Unlock()
	}()
	for _, peer := range peers {
		link := peer.roomLink()
		frame, err := r.makeFrame("wake", link, link.PeerID, link.RuntimeID)
		if err != nil {
			continue
		}
		waiter := roomWakeWaiter{device: link.PeerID, runtime: link.RuntimeID, done: make(chan struct{})}
		r.mu.Lock()
		if len(r.waking) >= maxPeerTransports {
			r.mu.Unlock()
			break
		}
		r.waking[frame.ID] = waiter
		r.markSeenLocked(frame.ID)
		r.mu.Unlock()
		if peer.sendRoomFrame(frame) {
			waits[frame.ID] = waiter
		} else {
			r.mu.Lock()
			delete(r.waking, frame.ID)
			r.mu.Unlock()
		}
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for _, waiter := range waits {
		select {
		case <-waiter.done:
		case <-r.owner.ctx.Done():
			return
		case <-timer.C:
			return
		}
	}
}

func (r *roomControl) signalPending() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for id, request := range r.peerWakes {
		peer := r.peers[id]
		if !now.Before(request.until) || (peer != nil && (peer.roomLink().RuntimeID != request.runtime || peer.roomLink().Generation > request.generation)) {
			delete(r.peerWakes, id)
		}
	}
	return len(r.peerWakes) > 0
}

func (r *roomControl) attach(peer roomPeer) {
	if r == nil || peer == nil {
		return
	}
	link := peer.roomLink()
	r.mu.Lock()
	r.peers[link.TransportID] = peer
	var frames []roomFrame
	if r.lastDirectory != nil {
		frames = append(frames, *r.lastDirectory)
	}
	for _, record := range r.nodes {
		frames = append(frames, record.frame)
	}
	r.mu.Unlock()
	for _, frame := range frames {
		r.sendFrame(peer, frame)
	}
	r.publish()
}

func (r *roomControl) sendFrame(peer roomPeer, frame roomFrame) {
	if !peer.sendRoomFrame(frame) {
		// Resynchronize over a replacement path rather than lose revocations
		// or topology updates when the bounded control queue is full.
		if mux, ok := peer.(*muxPeer); ok {
			mux.stop()
		}
	}
}

func (r *roomControl) detach(peer roomPeer) {
	if r == nil || peer == nil {
		return
	}
	link := peer.roomLink()
	r.mu.Lock()
	if r.peers[link.TransportID] != peer {
		r.mu.Unlock()
		return
	}
	delete(r.peers, link.TransportID)
	r.mu.Unlock()
	r.publish()
	if !r.hasSignalVia() && !r.waitForSignal() {
		r.requireSignal(true)
	}
}

func (r *roomControl) reachableLocked() map[string]bool {
	reachable := map[string]bool{r.device: true}
	queue := []string{r.device}
	for len(queue) > 0 {
		device := queue[0]
		queue = queue[1:]
		var links []roomLink
		if device == r.device {
			for _, peer := range r.peers {
				links = append(links, peer.roomLink())
			}
		} else {
			node := r.nodes[device].node
			member := r.directory[device]
			if node.RuntimeID != member.RuntimeID || node.Epoch < member.TransportEpoch {
				continue
			}
			links = node.Neighbours
		}
		for _, link := range links {
			if reachable[link.PeerID] {
				continue
			}
			member, known := r.directory[link.PeerID]
			if !known || member.RuntimeID != link.RuntimeID {
				continue
			}
			if device != r.device {
				back := r.nodes[link.PeerID].node
				if back.RuntimeID != link.RuntimeID || back.Epoch < member.TransportEpoch {
					continue
				}
				matched := false
				for _, reverse := range back.Neighbours {
					if reverse.PeerID == device && reverse.RuntimeID == r.nodes[device].node.RuntimeID && reverse.TransportID == link.TransportID && reverse.Generation == link.Generation {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			reachable[link.PeerID] = true
			queue = append(queue, link.PeerID)
		}
	}
	return reachable
}

func (r *roomControl) signalViaLocked() []string {
	var devices []string
	for device := range r.reachableLocked() {
		if device == r.device {
			continue
		}
		node := r.nodes[device].node
		if node.SignalOnline && modernSoftware(node.WorkerVersion) {
			devices = append(devices, device)
		}
	}
	sort.Strings(devices)
	return devices
}

func (r *roomControl) hasSignalVia() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.signalViaLocked()) > 0
}

func (r *roomControl) waitForSignal() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.waitForSignalLocked()
}

func (r *roomControl) waitForSignalLocked() bool {
	recovering := false
	for device := range r.reachableLocked() {
		if device != r.device && r.nodes[device].node.SignalRecovering {
			recovering = true
		}
	}
	return modernSoftware(r.workerVersion) && !r.forceSignal && (len(r.signalViaLocked()) > 0 || recovering)
}

func (r *roomControl) deferPeerRecovery(m iceSignalMessage) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.waitForSignalLocked() {
		return false
	}
	member, known := r.directory[m.PeerDevice]
	if known && member.RuntimeID == m.PeerRuntimeID {
		if r.reachableLocked()[m.PeerDevice] {
			return false
		}
		// A fresh Worker directory can confirm that both endpoints survived
		// in separate components. Before that, an unreachable cached member
		// may simply have gone offline; its old mapping must not wake WSS.
		local := r.directory[r.device]
		if local.GroupID != "" && member.GroupID != "" && local.GroupID != member.GroupID {
			return false
		}
	}
	return true
}

func (r *roomControl) publish() {
	if r == nil || r.private == nil {
		return
	}
	r.mu.Lock()
	via := r.signalViaLocked()
	resident := r.online && (r.keepBackup || len(via) == 0 || r.device < via[0])
	node := roomNode{DeviceName: r.device, RuntimeID: r.runtime, CoreVersion: CoreVersion, WorkerVersion: r.workerVersion,
		Epoch: r.owner.networkEpoch.Load(), SignalOnline: r.online, SignalRecovering: r.recoveringSignal, Resident: resident, Neighbours: []roomLink{}}
	for _, peer := range r.peers {
		node.Neighbours = append(node.Neighbours, peer.roomLink())
	}
	sort.Slice(node.Neighbours, func(i, j int) bool { return node.Neighbours[i].PeerID < node.Neighbours[j].PeerID })
	state, _ := json.Marshal(node)
	if r.lastState == string(state) {
		r.mu.Unlock()
		r.notify()
		return
	}
	r.lastState = string(state)
	r.sequence++
	node.Revision = r.sequence
	r.mu.Unlock()
	frame, err := r.makeFrame("node", node, "", "")
	if err != nil {
		return
	}
	r.mu.Lock()
	if old := r.nodes[r.device]; old.node.Revision >= node.Revision {
		r.mu.Unlock()
		return
	}
	r.nodes[r.device] = roomNodeRecord{node: node, frame: frame}
	r.markSeenLocked(frame.ID)
	r.mu.Unlock()
	r.forward(frame, nil)
	r.uploadGraph()
	r.notify()
}

func (r *roomControl) makeFrame(kind string, body any, target, runtime string) (roomFrame, error) {
	id, err := newSessionID()
	if err != nil {
		return roomFrame{}, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return roomFrame{}, err
	}
	frame := roomFrame{ID: id.String(), Room: r.room, Origin: r.device, Runtime: r.runtime, Target: target,
		TargetRuntime: runtime, Kind: kind, Body: base64.StdEncoding.EncodeToString(data), Certificate: r.certificate, Hops: maxRoomHops}
	if err := signRoomFrame(r.private, &frame); err != nil {
		return roomFrame{}, err
	}
	if !frame.validSize() {
		return roomFrame{}, errors.New("room control message too large")
	}
	return frame, nil
}

func (r *roomControl) markSeenLocked(id string) {
	if r.seen[id] {
		return
	}
	r.seen[id] = true
	r.seenOrder = append(r.seenOrder, id)
	if len(r.seenOrder) > 4096 {
		delete(r.seen, r.seenOrder[0])
		r.seenOrder = r.seenOrder[1:]
	}
}

func (r *roomControl) forward(frame roomFrame, from roomPeer) {
	if frame.Hops <= 0 {
		return
	}
	frame.Hops--
	r.mu.Lock()
	peers := make([]roomPeer, 0, len(r.peers))
	for _, peer := range r.peers {
		if peer != from {
			peers = append(peers, peer)
		}
	}
	r.mu.Unlock()
	for _, peer := range peers {
		r.sendFrame(peer, frame)
	}
}

func (r *roomControl) accept(frame roomFrame, from roomPeer) {
	if r == nil || frame.Room != r.room || !frame.validSize() {
		return
	}
	r.mu.Lock()
	if r.seen[frame.ID] {
		r.mu.Unlock()
		return
	}
	workerKey := r.workerKey
	member, known := r.directory[frame.Origin]
	r.mu.Unlock()
	worker := frame.Origin == "$worker"
	if worker {
		if frame.Kind != "notice" || !verifyRoomFrame(workerKey, frame) {
			return
		}
	} else {
		if !known || member.RuntimeID != frame.Runtime {
			return
		}
		_, key, err := roomCertificate(frame.Certificate, member.CertFingerprint)
		if err != nil || !verifyRoomFrame(key, frame) {
			return
		}
	}
	r.mu.Lock()
	if r.seen[frame.ID] {
		r.mu.Unlock()
		return
	}
	r.markSeenLocked(frame.ID)
	r.mu.Unlock()
	if frame.Target != "" && (frame.Target != r.device || frame.TargetRuntime != r.runtime) {
		r.forward(frame, from)
		return
	}
	data, err := base64.StdEncoding.DecodeString(frame.Body)
	if err != nil || len(data) > maxRoomFrame {
		return
	}
	if worker && frame.Target != "" {
		data, err = decryptRoomNotice(r.private, frame)
		if err != nil {
			return
		}
	}
	if worker {
		var message iceSignalMessage
		if json.Unmarshal(data, &message) != nil {
			return
		}
		if message.Type == "room_state" {
			if !r.installDirectory(message, &frame) {
				return
			}
		}
		if message.Type == "room_wake" {
			r.requireSignal(false)
		} else {
			// Process notices outside the receiving mux's worker. A revocation
			// may close that same mux and wait for all its readers to exit.
			select {
			case r.notices <- message:
			default:
				r.requireSignal(false)
			}
		}
	} else {
		switch frame.Kind {
		case "wake":
			if frame.Target != r.device || frame.TargetRuntime != r.runtime {
				return
			}
			var link roomLink
			if json.Unmarshal(data, &link) != nil || link.PeerID != r.device || link.RuntimeID != r.runtime || link.Generation == 0 {
				return
			}
			r.mu.Lock()
			peer := r.peers[link.TransportID]
			valid := peer != nil && peer.roomLink().PeerID == frame.Origin && peer.roomLink().RuntimeID == frame.Runtime && peer.roomLink().Generation == link.Generation
			if valid {
				r.peerWakes[link.TransportID] = roomPeerWake{runtime: frame.Runtime, generation: link.Generation, until: time.Now().Add(sessionHandshakeTimeout)}
			}
			r.mu.Unlock()
			if !valid {
				return
			}
			r.requireSignal(false)
			ack, err := r.makeFrame("wake_ack", struct {
				RequestID string `json:"request_id"`
			}{frame.ID}, frame.Origin, frame.Runtime)
			if err == nil {
				r.mu.Lock()
				r.markSeenLocked(ack.ID)
				r.mu.Unlock()
				r.forward(ack, nil)
			}
			return
		case "wake_ack":
			if frame.Target != r.device || frame.TargetRuntime != r.runtime {
				return
			}
			var ack struct {
				RequestID string `json:"request_id"`
			}
			if json.Unmarshal(data, &ack) != nil {
				return
			}
			r.mu.Lock()
			if waiter, exists := r.waking[ack.RequestID]; exists && waiter.device == frame.Origin && waiter.runtime == frame.Runtime {
				delete(r.waking, ack.RequestID)
				close(waiter.done)
			}
			r.mu.Unlock()
			return
		case "node":
			var node roomNode
			if json.Unmarshal(data, &node) != nil || node.DeviceName != frame.Origin || node.RuntimeID != frame.Runtime || node.Revision == 0 || node.Epoch < member.TransportEpoch || len(node.Neighbours) > maxPeerTransports || node.CoreVersion != member.CoreVersion {
				return
			}
			seen := map[string]bool{}
			for _, link := range node.Neighbours {
				if link.PeerID == node.DeviceName || !validDeviceName(link.PeerID) || seen[link.PeerID] || link.TransportID == "" || len(link.TransportID) > 256 || link.Generation == 0 || len(link.RuntimeID) != 32 {
					return
				}
				seen[link.PeerID] = true
			}
			r.mu.Lock()
			old := r.nodes[node.DeviceName]
			if old.node.RuntimeID == node.RuntimeID && old.node.Revision >= node.Revision {
				r.mu.Unlock()
				return
			}
			r.nodes[node.DeviceName] = roomNodeRecord{node: node, frame: frame}
			r.mu.Unlock()
			r.publish()
			r.uploadGraph()
		case "recover":
			var request struct {
				Sequence uint64 `json:"sequence,string"`
			}
			if json.Unmarshal(data, &request) != nil || request.Sequence == 0 {
				return
			}
			key := frame.Origin + "/" + frame.Runtime
			r.mu.Lock()
			if r.recovery[key] >= request.Sequence {
				r.mu.Unlock()
				return
			}
			r.recovery[key] = request.Sequence
			r.mu.Unlock()
			r.requireSignal(true)
		default:
			return
		}
	}
	r.forward(frame, from)
	r.notify()
	if !r.owner.signalOnline() && !r.hasSignalVia() && !r.waitForSignal() {
		r.requireSignal(false)
	}
}

func (r *roomControl) installDirectory(message iceSignalMessage, frame *roomFrame) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if message.StateRevision < r.stateRevision {
		return false
	}
	if len(message.RoomState) > 128 || len(message.RoomGroups) > 16 {
		return false
	}
	for _, member := range message.RoomState {
		if !validServiceAdvertisements(member.Services) {
			return false
		}
		if member.DeviceName == "" || member.RuntimeID == "" || len(member.DeviceName) > 128 {
			return false
		}
		if modernSoftware(member.CoreVersion) {
			if _, _, err := roomCertificate(member.Certificate, member.CertFingerprint); err != nil {
				return false
			}
		}
	}
	directory := make(map[string]voiceRoomMember, len(message.RoomState)+1)
	for _, member := range message.RoomState {
		if _, exists := directory[member.DeviceName]; exists {
			return false
		}
		member.Services = slices.Clone(member.Services)
		directory[member.DeviceName] = member
	}
	if _, exists := directory[r.device]; !exists {
		directory[r.device] = r.directory[r.device]
	}
	for device, old := range r.directory {
		member, exists := directory[device]
		if !exists || old.RuntimeID != member.RuntimeID || old.CertFingerprint != member.CertFingerprint {
			delete(r.nodes, device)
			delete(r.recovery, device+"/"+old.RuntimeID)
		}
	}
	r.directory = directory
	r.stateRevision = message.StateRevision
	r.remoteGroups = message.RoomGroups
	if frame != nil && frame.Target == "" {
		copy := *frame
		r.lastDirectory = &copy
	}
	return true
}

func (r *roomControl) bindPeer(device, runtime, fingerprint, version string) {
	if r == nil || device == "" || runtime == "" || fingerprint == "" {
		return
	}
	r.mu.Lock()
	member := r.directory[device]
	if member.RuntimeID != runtime || member.CertFingerprint != fingerprint {
		delete(r.nodes, device)
		member.Services = nil
	}
	member.DeviceName, member.RuntimeID, member.CertFingerprint, member.CoreVersion = device, runtime, fingerprint, version
	r.directory[device] = member
	r.mu.Unlock()
}

func (c *iceCoordinator) signalNeeded() bool {
	if c.signalingConfigPending() || c.room.signalPending() {
		return true
	}
	c.mu.Lock()
	queued := false
	for _, message := range c.pendingSignals {
		if message.wakesSignal() {
			queued = true
			break
		}
	}
	c.mu.Unlock()
	if queued {
		return true
	}
	for _, p := range c.peerList() {
		p.mu.Lock()
		negotiating := !p.closed && (p.pending != nil || p.retiring != nil || p.requestedGeneration != 0)
		recovering := !p.closed && !p.terminal && p.online && p.active == nil && (len(p.ready.Mappings) > 0 || p.ready.Voice)
		ready := p.ready
		p.mu.Unlock()
		if negotiating || (recovering && !c.room.deferPeerRecovery(ready)) {
			return true
		}
	}
	c.turnMu.Lock()
	requesting := c.turnRequestID != ""
	c.turnMu.Unlock()
	return requesting || c.turnRefreshDue()
}

func (r *roomControl) uploadGraph() {
	if r == nil || !r.owner.signalOnline() || !r.enabled() {
		return
	}
	r.mu.Lock()
	var frames []roomFrame
	for device := range r.reachableLocked() {
		if record, ok := r.nodes[device]; ok {
			frames = append(frames, record.frame)
		}
	}
	r.mu.Unlock()
	if len(frames) > 0 {
		_ = r.owner.send(iceSignalMessage{SignalMessage: SignalMessage{Type: "room_graph"}, RoomFrames: frames})
	}
}

func (r *roomControl) recoverTogether() {
	if r == nil || !r.enabled() {
		return
	}
	r.requireSignal(true)
	r.mu.Lock()
	r.recoveringSignal = false
	r.sequence++
	sequence := r.sequence
	r.mu.Unlock()
	r.publish()
	frame, err := r.makeFrame("recover", struct {
		Sequence uint64 `json:"sequence,string"`
	}{sequence}, "", "")
	if err == nil {
		r.mu.Lock()
		r.markSeenLocked(frame.ID)
		r.mu.Unlock()
		r.forward(frame, nil)
	}
}

func (r *roomControl) snapshot() RoomSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	reachable := r.reachableLocked()
	members := make([]voiceRoomMember, 0, len(reachable))
	ids := make([]string, 0, len(reachable))
	for device := range reachable {
		member := r.directory[device]
		member.Services = slices.Clone(member.Services)
		member.SignalOnline = r.nodes[device].node.SignalOnline
		member.SignalRecovering = r.nodes[device].node.SignalRecovering
		member.ObservedWorkerVersion = r.nodes[device].node.WorkerVersion
		if device == r.device {
			member.SignalOnline = r.online
		}
		members = append(members, member)
		ids = append(ids, device)
	}
	sort.Strings(ids)
	sort.Slice(members, func(i, j int) bool { return members[i].DeviceName < members[j].DeviceName })
	groupID := ""
	if len(ids) > 0 {
		groupID = ids[0]
	}
	groups := make([]roomGroup, len(r.remoteGroups))
	for i, group := range r.remoteGroups {
		groups[i] = roomGroup{ID: group.ID, Members: slices.Clone(group.Members), Signaling: slices.Clone(group.Signaling)}
	}
	return RoomSnapshot{DiscoverySupported: r.directory[r.device].Services != nil, Services: r.servicesLocked(), GroupID: groupID, WorkerVersion: r.workerVersion, SignalVia: r.signalViaLocked(), Members: members, Groups: groups}
}

func (r *roomControl) run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-r.notices:
			if message.Type == "joined" || message.Type == "transport_synced" {
				continue
			}
			if message.Type == "room_state" {
				r.mu.Lock()
				stale := message.StateRevision < r.stateRevision
				r.mu.Unlock()
				if stale {
					continue
				}
			}
			// New negotiations use the endpoints' direct WSS. In-place updates
			// to a healthy generation remain valid while signaling is delegated.
			if message.Type == "transport_ready" && !r.owner.signalOnline() && !r.owner.healthyGeneration(message) {
				r.requireSignal(false)
				continue
			}
			if err := r.owner.handle(message, r.owner.request(), r.owner.platform); err != nil {
				r.owner.emit(Event{Kind: "room", State: "error", Error: classifyError(err)})
			}
		case <-ticker.C:
			r.owner.pruneLocalPeerRelays()
			if r.owner.turnRefreshDue() {
				r.requireSignal(false)
			}
		}
	}
}

func authorizationContext(ctx context.Context, message iceSignalMessage) (context.Context, context.CancelFunc) {
	if message.localAuthorization() {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, time.UnixMilli(message.LeaseUntil))
}

func (c *iceCoordinator) healthyGeneration(m iceSignalMessage) bool {
	c.mu.Lock()
	p := c.peers[m.TransportID]
	c.mu.Unlock()
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active != nil && p.active.mux.ctx.Err() == nil && p.active.ready.TransportGeneration == m.TransportGeneration && p.active.ready.PeerRuntimeID == m.PeerRuntimeID && p.active.ready.PeerFingerprint == m.PeerFingerprint
}

func (c *iceCoordinator) turnRefreshDue() bool {
	if c.request().Config.TURN.Mode != "worker" || !c.needsTURN() {
		return false
	}
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	return c.turnRefresh <= time.Now().UnixMilli()
}

func (r roomLink) String() string {
	return fmt.Sprintf("%s/%s/%d", r.PeerID, r.TransportID, r.Generation)
}
