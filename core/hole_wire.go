package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"net/netip"
	"time"
)

// The payload is always an end-to-end QUIC packet. No stream/datagram of the
// business connection is decoded by the forwarding backend.
const (
	holeHeader     = 46 // magic/version, kind, side, route, sequence, truncated MAC
	holePacketSize = 2048
	holeData       = byte(0)
	holeRegister   = byte(1)
	holeChallenge  = byte(2)
	holeConfirm    = byte(3)
	holeAck        = byte(4)
)

var holeMagic = [4]byte{'H', 'O', 'L', 1}

type holeGrant struct {
	Device       string    `json:"device"`
	Endpoint     string    `json:"endpoint"`
	RelayRuntime string    `json:"relay_runtime"`
	RelayEpoch   uint64    `json:"relay_epoch,string"`
	Route        string    `json:"route"`
	Keys         [2]string `json:"keys"`
	Generation   uint64    `json:"generation,string"`
	Devices      [2]string `json:"devices"`
	Runtimes     [2]string `json:"runtimes"`
	Epochs       [2]string `json:"epochs"`
	Error        string    `json:"error,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
}

type holeAuth struct {
	route [16]byte
	mac   [2]hash.Hash
	sum   [32]byte
}

func newHoleAuth(g *holeGrant) (*holeAuth, error) {
	a := new(holeAuth)
	id, err := hex.DecodeString(g.Route)
	if err != nil || len(id) != 16 {
		return nil, &Fault{Code: "protocol_mismatch", Message: "Hole 路由标识无效"}
	}
	copy(a.route[:], id)
	for i, key := range g.Keys {
		b, e := hex.DecodeString(key)
		if e != nil || len(b) != 32 {
			return nil, &Fault{Code: "protocol_mismatch", Message: "Hole 路由授权无效"}
		}
		a.mac[i] = hmac.New(sha256.New, b)
	}
	return a, nil
}

// Callers serialize access to each auth instance. Sum appends into caller-owned
// stack storage; neither signing nor verification allocates per datagram.
func (a *holeAuth) signature(b []byte) []byte {
	h := a.mac[int(b[5])]
	h.Reset()
	_, _ = h.Write(b[:30])
	_, _ = h.Write(b[holeHeader:])
	return h.Sum(a.sum[:0])
}
func (a *holeAuth) sign(b []byte, kind byte, side int, seq uint64) {
	copy(b[:4], holeMagic[:])
	b[4], b[5] = kind, byte(side)
	copy(b[6:22], a.route[:])
	binary.BigEndian.PutUint64(b[22:30], seq)
	sum := a.signature(b)
	copy(b[30:46], sum[:16])
}
func (a *holeAuth) valid(b []byte) bool {
	if len(b) < holeHeader || b[5] > 1 || string(b[:4]) != string(holeMagic[:]) || string(b[6:22]) != string(a.route[:]) {
		return false
	}
	sum := a.signature(b)
	return hmac.Equal(b[30:46], sum[:16])
}

type holeReplay struct{ top, bits uint64 }

func (r *holeReplay) accept(n uint64) bool {
	if n == 0 {
		return false
	}
	if n > r.top {
		d := n - r.top
		if d >= 64 {
			r.bits = 0
		} else {
			r.bits <<= d
		}
		r.bits |= 1
		r.top = n
		return true
	}
	d := r.top - n
	if d >= 64 || r.bits&(uint64(1)<<d) != 0 {
		return false
	}
	r.bits |= uint64(1) << d
	return true
}

type holeFrame struct {
	data    [holePacketSize]byte
	n       int
	address netip.AddrPort
	oob     [128]byte
	oobn    int
	flags   int
}
type holeCounters struct {
	ActivePairs    int    `json:"active_pairs"`
	ForwardedBytes uint64 `json:"forwarded_bytes,string"`
	QueueDrops     uint64 `json:"queue_drops,string"`
	RateDrops      uint64 `json:"rate_drops,string"`
	Backend        string `json:"backend"`
}

// One bucket accounts for both directions and all pairs. It charges the wire
// envelope as well as the encrypted payload, never ordinary fixed traffic.
type holeBucket struct {
	rate, tokens float64
	last         time.Time
}

func (b *holeBucket) allow(n int, now time.Time) bool {
	if b.rate == 0 {
		return true
	}
	burst := max(b.rate/10, float64(holePacketSize))
	b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < float64(n) {
		return false
	}
	b.tokens -= float64(n)
	return true
}
