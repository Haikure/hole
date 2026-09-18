package core

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

func endpointString(host string, port int) string { return net.JoinHostPort(host, fmt.Sprint(port)) }

func sameScope(a, b Request) bool {
	return a.ServerURL == b.ServerURL && a.Config.Room == b.Config.Room &&
		a.Config.Password == b.Config.Password && a.Config.Token == b.Config.Token &&
		a.Config.DeviceName == b.Config.DeviceName &&
		a.Config.Transport.Preferred == b.Config.Transport.Preferred
}

// One supervisor owns all transport replacements. Stop cancels this supervisor;
// no timer or late callback can create a second run. Certificates and application
// sockets outlive a transport, but never outlive their authenticated scope.
func (e *Engine) execute(run *engineRun, initial Request) {
	var manager *sessionManager
	var identity *tls.Config
	previous := initial
	var terminal error
	defer func() {
		if manager != nil {
			manager.cancel()
			manager.close()
		}
		e.finishRun(run, terminal)
	}()
	backoff := time.Second
	hadTransport := false
	for run.ctx.Err() == nil {
		e.mu.Lock()
		request, epoch := *e.request, run.epoch
		select {
		case <-run.changes:
		default:
		}
		e.transportGeneration++
		e.mu.Unlock()
		if manager == nil || !sameScope(previous, request) {
			if manager != nil {
				manager.cancel()
				manager.close()
			}
			manager = newSessionManager(request.Config.SessionTimeout, sessionOptions{
				platform: e.platform,
				shared:   request.Config.Transport.Preferred == PreferredICE,
				emit:     func(event Event) { e.agentEvent(run, event) },
				logf: func(format string, args ...any) {
					e.agentEvent(run, Event{Kind: "log", Message: fmt.Sprintf(format, args...)})
				},
			})
			var err error
			identity, err = makeTLSConfig()
			if err != nil {
				terminal = err
				return
			}
		} else {
			manager.reconfigure(previous.Config, request.Config)
		}
		previous = request
		e.mu.Lock()
		run.sessions = manager
		e.mu.Unlock()
		ctx, cancel := context.WithCancel(run.ctx)
		result := make(chan error, 1)
		go func() { result <- e.serveCycle(ctx, run, epoch, request, manager, identity) }()
		var err error
		changed := false
		select {
		case <-run.ctx.Done():
			cancel()
			<-result
			terminal = run.ctx.Err()
			return
		case <-run.changes:
			changed = true
			cancel()
			<-result
		case err = <-result:
		}
		cancel()
		e.mu.Lock()
		run.agent = nil
		hadTransport = hadTransport || e.signal == "joined" || e.state == "running"
		if epoch != run.epoch {
			changed = true
		}
		if !changed && run.ctx.Err() == nil {
			e.signal = "disconnected"
			if err != nil {
				e.lastError = classifyError(err)
				e.lastError.Message = redact(e.lastError.Message, request.Config)
			}
			e.state = "recovering"
			e.publishLocked(Event{Kind: "engine", State: e.state, Error: e.lastError})
		}
		e.mu.Unlock()
		if changed {
			backoff = time.Second
			continue
		}
		if !e.retryNetwork && !hadTransport {
			terminal = err
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-run.ctx.Done():
			timer.Stop()
			terminal = run.ctx.Err()
			return
		case <-run.changes:
			timer.Stop()
			backoff = time.Second
		case <-timer.C:
			backoff = min(30*time.Second, backoff*2)
		}
	}
	terminal = run.ctx.Err()
}

func (e *Engine) serveCycle(ctx context.Context, run *engineRun, epoch uint64, request Request, manager *sessionManager, identity *tls.Config) error {
	if request.Config.Transport.Preferred == PreferredICE {
		c := newICECoordinator(ctx, request, e.platform, manager, identity, run.runtimeID, e.transportGeneration,
			func(event Event) { e.agentEventEpoch(run, &epoch, event) }, func(network NetworkSnapshot) {
				e.mu.Lock()
				if e.run == run && run.epoch == epoch && ctx.Err() == nil {
					e.network = network.clone()
				}
				e.mu.Unlock()
			})
		e.mu.Lock()
		run.ice = c
		if e.request != nil && sameScope(request, *e.request) {
			c.submit(*e.request)
		}
		e.mu.Unlock()
		defer func() {
			e.mu.Lock()
			if run.ice == c {
				run.ice = nil
			}
			e.mu.Unlock()
		}()
		return c.run()
	}
	platform := e.platform
	if source, ok := platform.(NetworkSnapshotter); ok {
		selected, network, err := source.SnapshotNetwork(ctx)
		e.mu.Lock()
		if run.epoch == epoch && run.ctx.Err() == nil {
			e.network = network.clone()
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		platform = selected
	}
	agent, err := newAgentWithState(ctx, request.Config, platform, func(event Event) {
		// Redact against the producing generation as well as the current one.
		event.Message = redact(event.Message, request.Config)
		if event.Error != nil {
			copy := *event.Error
			copy.Message = redact(copy.Message, request.Config)
			event.Error = &copy
		}
		e.agentEventEpoch(run, &epoch, event)
	}, manager, identity)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if run.epoch == epoch && ctx.Err() == nil {
		run.agent = agent
	}
	e.mu.Unlock()
	return agent.run(ctx, request.ServerURL)
}

// Called only after every old Agent worker has exited. Closing changed mappings
// is distinct from disconnecting transports: deleted sockets never reappear on
// undo, and unchanged listeners retain their accept queues and replay buffers.
func (m *sessionManager) reconfigure(old, next Config) {
	before, after := provideTable(old.Provide), provideTable(next.Provide)
	consumers := consumeTable(next.Consume)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeout = next.SessionTimeout
	for id, client := range m.clients {
		item, ok := consumers[id]
		if !ok || item.Expose != client.t.expose {
			client.close()
			delete(m.clients, id)
		}
	}
	changed := func(id string) bool { a, ok := after[id]; return !ok || a != before[id] }
	for key, entry := range m.tcp {
		if changed(key.mapping) {
			if entry.cancel != nil {
				entry.cancel()
			}
			select {
			case <-entry.ready:
				if entry.session != nil {
					entry.session.close()
				}
			default:
			}
			delete(m.tcp, key)
		}
	}
	for key, group := range m.udp {
		if changed(key.mapping) {
			group.close()
			delete(m.udp, key)
		}
	}
	for key := range m.owners {
		_, provided := after[key.mappingID]
		_, consumed := consumers[key.mappingID]
		if !provided && !consumed {
			delete(m.owners, key)
		}
	}
}

func (m *sessionManager) terminate(mapping, fingerprint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.clients[mapping]; c != nil && (fingerprint == "" || c.fingerprint == fingerprint) {
		c.close()
		delete(m.clients, mapping)
	}
	for key, entry := range m.tcp {
		if key.mapping == mapping && (fingerprint == "" || key.fingerprint == fingerprint) {
			if entry.cancel != nil {
				entry.cancel()
			}
			select {
			case <-entry.ready:
				if entry.session != nil {
					entry.session.close()
				}
			default:
			}
			delete(m.tcp, key)
		}
	}
	for key, g := range m.udp {
		if key.mapping == mapping && (fingerprint == "" || key.fingerprint == fingerprint) {
			g.close()
			delete(m.udp, key)
		}
	}
}

func (m *sessionManager) peerAllowedLocked(mapping, fingerprint string) bool {
	known := false
	for key, value := range m.owners {
		if key.mappingID == mapping {
			known = true
			if value == fingerprint {
				return true
			}
		}
	}
	return !known
}

// Ownership lives with application sessions, not an individual Agent. A peer
// process replacing its certificate must not leave its old upstream sockets
// open until timeout, nor claim them with only a recycled device name.
func (m *sessionManager) claimPeer(mapping, peer, fingerprint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := peerMapping{mappingID: mapping, device: peer}
	old := m.owners[key]
	m.owners[key] = fingerprint
	if old == "" || old == fingerprint {
		return
	}
	if client := m.clients[mapping]; client != nil && client.fingerprint == old && client.t.peer == peer {
		client.close()
		delete(m.clients, mapping)
	}
	for key, entry := range m.tcp {
		if key.mapping == mapping && key.fingerprint == old {
			if entry.cancel != nil {
				entry.cancel()
			}
			select {
			case <-entry.ready:
				if entry.session != nil {
					entry.session.close()
				}
			default:
			}
			delete(m.tcp, key)
		}
	}
	for key, group := range m.udp {
		if key.mapping == mapping && key.fingerprint == old {
			group.close()
			delete(m.udp, key)
		}
	}
}
