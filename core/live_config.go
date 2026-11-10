package core

import (
	"context"
	"reflect"
	"time"
)

// These settings only affect future probes or local session retention. They do
// not invalidate an authenticated path or change the room's service declaration.
func signalingConfig(r Request) Request {
	r.Config.SessionTimeout = 0
	r.Config.ICE.STUNURLs = nil
	r.Config.ICE.NATSTUNURLs = nil
	r.Config.ICE.Punch = NATPunchConfig{}
	r.Config.ICE.DirectProbeTimeout = 0
	r.Config.ICE.GatherTimeout = 0
	r.Config.ICE.ConnectivityTimeout = 0
	r.Config.ICE.RetryMaxDelay = 0
	return r
}

func sameSignalingConfig(a, b Request) bool {
	return sameEffective(signalingConfig(a), signalingConfig(b))
}

func pathConfigChanged(a, b Config) bool {
	x, y := signalingConfig(Request{Config: a}).Config, signalingConfig(Request{Config: b}).Config
	return !reflect.DeepEqual(x.ICE, y.ICE) || !reflect.DeepEqual(x.TURN, y.TURN) ||
		!reflect.DeepEqual(x.Transport, y.Transport) || !reflect.DeepEqual(x.Relay, y.Relay) ||
		!reflect.DeepEqual(x.CandidateInterfaces, y.CandidateInterfaces) || !reflect.DeepEqual(x.CandidateAddresses, y.CandidateAddresses)
}

// Only the coordinator loop calls this. Advancing applied here prevents a
// later signaling reconnect from closing sessions created by a live update.
func (c *iceCoordinator) applyLocal(applied *Request, next Request) error {
	if !sameEffective(*applied, next) {
		if !sameSignalingConfig(*applied, next) {
			c.closeLegacy()
		}
		c.sessions.reconfigure(applied.Config, next.Config)
		*applied = next
		for _, p := range c.peerList() {
			p.refreshPolicy()
		}
	}
	return c.configureFixed(next)
}

func (c *iceCoordinator) configApplied(r Request) {
	if sameEffective(r, c.request()) {
		c.emit(Event{Kind: "config", State: "applied", appliedConfig: &r})
	}
}

// A single event loop owns the accepted declaration and the in-flight update.
// Edits coalesce while awaiting ACK; local revocations still apply immediately.
func (c *iceCoordinator) signalUpdates(ctx context.Context, incoming <-chan iceSignalMessage, results <-chan error, platform Platform, accepted Request, applied *Request) (error, bool) {
	joined, supported := false, false
	reported := accepted
	needsReport := false
	var revision uint64
	var pending *Request
	var requestID string
	var timeout <-chan time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	update := func() (bool, error) {
		c.configMu.RLock()
		next, epoch := c.desired, c.networkEpoch.Load()
		c.configMu.RUnlock()
		c.mu.Lock()
		legacy := c.legacy != nil
		c.mu.Unlock()
		if err := c.applyLocal(applied, next); err != nil {
			return false, err
		}
		if !joined || pending != nil {
			return false, nil
		}
		if sameSignalingConfig(accepted, next) {
			accepted = next
			if needsReport || !sameEffective(reported, next) {
				c.configApplied(next)
				reported = next
				needsReport = false
			}
			return false, nil
		}
		if !supported || legacy {
			return true, nil
		}
		message := c.memberDeclaration(ctx, platform, next)
		message.TransportEpoch = epoch
		message.Type = "member_update"
		revision++
		message.MemberRevision = revision
		id, err := newSessionID()
		if err != nil {
			return false, err
		}
		requestID = id.String()
		message.RequestID = requestID
		pending = &next
		timer.Reset(sessionHandshakeTimeout)
		timeout = timer.C
		return false, c.send(message)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err(), false
		case <-c.reset:
			return nil, true
		case err := <-results:
			return err, false
		case <-timeout:
			return &Fault{Code: "config_update_timeout", Message: "在线配置确认超时，正在重新同步"}, false
		case <-c.updates:
			needsReport = true
		case m := <-incoming:
			if m.Type == "member_updated" {
				if pending == nil || m.RequestID != requestID || m.MemberRevision != revision {
					continue
				}
				timer.Stop()
				timeout = nil
				accepted, pending = *pending, nil
				// Refresh records ignored while a newer local edit was pending.
				if sameSignalingConfig(accepted, c.request()) {
					_ = c.send(signalMessage("transport_sync"))
				}
			} else if m.Type == "error" && pending != nil && m.RequestID == requestID {
				if m.Code == "unknown_message" {
					return nil, true
				}
				return &Fault{Code: m.Code, Message: m.Message}, false
			} else {
				if m.Type == "joined" {
					joined, supported = true, m.LiveConfig
				}
				if err := c.handle(m, accepted, platform); err != nil {
					return err, false
				}
			}
		}
		if reconnect, err := update(); reconnect || err != nil {
			return err, reconnect
		}
	}
}
