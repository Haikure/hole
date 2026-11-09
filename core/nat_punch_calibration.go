package core

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pion/stun/v4"
	"hole/core/natprobe"
)

const natPunchCalibration = "barrier-v1"
const maxPunchCalibrations = 3

type punchCalibrationPlans struct {
	local, remote *natPunchPlan
}

// Resolve before the calibration barrier: slow DNS must not age the peer's
// mapping. Keep lookups on the pinned platform, bounded and IPv4-only.
func punchSTUNServers(ctx context.Context, platform Platform, urls []string, previous string) ([]*net.UDPAddr, error) {
	resolve := platform.ResolveUDP
	if ipv4, ok := platform.(interface {
		ResolveUDP4(context.Context, string) (*net.UDPAddr, error)
	}); ok {
		resolve = ipv4.ResolveUDP4
	}
	urls = urls[:min(len(urls), 8)]
	addresses := make([]*net.UDPAddr, len(urls))
	var wg sync.WaitGroup
	for i, raw := range urls {
		wg.Go(func() {
			u, err := stun.ParseURI(raw)
			if err != nil || u.Scheme != stun.SchemeTypeSTUN || u.Proto != stun.ProtoTypeUDP {
				return
			}
			lookup, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			address, err := resolve(lookup, net.JoinHostPort(u.Host, strconv.Itoa(u.Port)))
			if err == nil && address != nil && address.IP.To4() != nil && !address.IP.IsUnspecified() && !address.IP.IsMulticast() && address.Port > 0 && address.Port <= 65535 {
				addresses[i] = address
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	servers := make([]*net.UDPAddr, 0, len(addresses))
	for _, address := range addresses {
		if address != nil && !seen[address.String()] {
			seen[address.String()] = true
			servers = append(servers, address)
		}
	}
	if len(servers) == 0 {
		return nil, errors.New("no IPv4 STUN for calibration")
	}
	rand.Shuffle(len(servers), func(i, j int) { servers[i], servers[j] = servers[j], servers[i] })
	// Prefer a different server IP, then a different port on the same IP. DNS
	// aliases must not turn apparent server rotation into the same endpoint.
	previousIP, _, _ := net.SplitHostPort(previous)
	rank := func(a *net.UDPAddr) int {
		if a.String() == previous {
			return 2
		}
		if a.IP.String() == previousIP {
			return 1
		}
		return 0
	}
	sort.SliceStable(servers, func(i, j int) bool { return rank(servers[i]) < rank(servers[j]) })
	return servers, nil
}

// A failed Binding uses another server and a fresh calibration socket. With
// barrier-v1 both peers discard the previous calibration, even if one Binding
// succeeded. Business sockets that have never sent remain prebound; cone/next
// replace the socket they will also use for punching. All ports stay reserved
// by predictNATPunch until the entire prediction ends.
func (p *icePeer) calibrateNATPunch(ctx context.Context, a *iceAttempt, s *natPunchSession, round int, servers []*net.UDPAddr, sockets []net.PacketConn, open func() (net.PacketConn, error), previous *string, diagnostic *NATPunchDiagnostic) (*punchCalibrationPlans, error) {
	synchronized := a.ready.PunchCalibration == natPunchCalibration
	separate := s.mode == "fixed" || s.mode == "single" && natGroup(s.local) == "symmetric" || s.mode == "pool" && natGroup(s.local) != "cone"
	var lastErr error
	failed := map[string]bool{}
	next := 0
	for trial := 1; trial <= maxPunchCalibrations; trial++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var server *net.UDPAddr
		for range len(servers) {
			candidate := servers[next%len(servers)]
			next++
			if !failed[candidate.String()] {
				server = candidate
				break
			}
		}
		if server == nil {
			return nil, fmt.Errorf("calibration STUN servers exhausted: %w", lastErr)
		}
		calibrator := sockets[0]
		if separate || trial > 1 {
			var err error
			calibrator, err = open()
			if err != nil {
				return nil, err
			}
			if !separate {
				sockets[0] = calibrator
			}
		}
		if synchronized {
			diagnostic.Stage = "calibration_wait"
			if err := p.sendNATPunch(a, natPunchSignal{Kind: "calibrate", Round: round, Trial: trial}); err != nil {
				return nil, err
			}
			if _, err := p.waitNATPunchTrial(ctx, a, "calibrate", round, trial); err != nil {
				return nil, err
			}
		}
		diagnostic.Stage = "calibration"
		diagnostic.STUNServer, *previous = server.String(), server.String()
		diagnostic.CalibrationAttempts = trial
		diagnostic.MappedAddress = ""
		sample, err := natprobe.Binding(ctx, calibrator, server, 0, 3*time.Second)
		var plan *natPunchPlan
		result := "ok"
		if err != nil {
			failed[server.String()] = true
			lastErr, result = err, "retry"
			if len(failed) == len(servers) {
				result = "exhausted"
			}
		} else {
			diagnostic.MappedAddress = sample.Mapped.String()
			plan, err = makeNATPunchPlan(sample.Mapped, s.local, s.mode, len(sockets), s.config.ScanWidth)
			if err != nil {
				lastErr, result = err, "exhausted"
				if errors.Is(err, errNATPunchModel) {
					result = "refresh"
				}
			}
		}
		peerResult := "ok"
		var remotePlan *natPunchPlan
		if synchronized {
			if err := p.sendNATPunch(a, natPunchSignal{Kind: "calibrated", Round: round, Trial: trial, Result: result, Mode: s.mode, Plan: plan}); err != nil {
				return nil, err
			}
			peer, err := p.waitNATPunchTrial(ctx, a, "calibrated", round, trial)
			if err != nil {
				return nil, err
			}
			peerResult = peer.Result
			remotePlan = peer.Plan
			if peer.Mode != s.mode || peerResult == "ok" && remotePlan == nil {
				return nil, errors.New("invalid peer calibration plan")
			}
			if peerResult != "ok" && peerResult != "retry" && peerResult != "refresh" && peerResult != "exhausted" {
				return nil, errors.New("invalid peer calibration result")
			}
		}
		if result == "refresh" || peerResult == "refresh" {
			return nil, errNATPunchModel
		}
		if result == "exhausted" || peerResult == "exhausted" {
			if result == "exhausted" {
				return nil, fmt.Errorf("calibration exhausted: %w", lastErr)
			}
			return nil, errors.New("peer calibration exhausted")
		}
		if result == "ok" && peerResult == "ok" {
			return &punchCalibrationPlans{local: plan, remote: remotePlan}, nil
		}
		if result == "ok" {
			lastErr = errors.New("peer Binding failed")
		}
	}
	return nil, fmt.Errorf("calibration retries exhausted: %w", lastErr)
}
