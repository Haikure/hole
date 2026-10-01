package core

import (
	"bytes"
	"context"
	"math/rand"
	"testing"
	"time"
)

func TestReplayBlocksPreserveSuffixAndReleaseBudget(t *testing.T) {
	budget := &replayBudget{limit: 4 * tcpFramePayload}
	r := tcpReplay{budget: budget}
	defer r.clear()
	random := rand.New(rand.NewSource(4))
	var want []byte
	for i := 0; i < 3000; i++ {
		if len(want) > 0 && random.Intn(2) == 0 {
			n := random.Intn(len(want)) + 1
			r.discard(n)
			want = want[n:]
		} else {
			data := make([]byte, random.Intn(tcpFramePayload)+1)
			_, _ = random.Read(data)
			if ok, _ := r.append(data); ok {
				want = append(want, data...)
			}
		}
		got := make([]byte, r.size)
		r.copyAt(got, 0)
		if !bytes.Equal(got, want) {
			t.Fatalf("replay changed bytes at operation %d", i)
		}
		if r.size > 0 {
			offset := random.Intn(r.size)
			suffix := make([]byte, r.size-offset)
			r.copyAt(suffix, offset)
			if !bytes.Equal(suffix, want[offset:]) {
				t.Fatal("offset replay changed bytes")
			}
		}
		if budget.used != len(r.blocks)*tcpFramePayload || budget.used > budget.limit {
			t.Fatal("incorrect allocation accounting")
		}
	}
	r.clear()
	if budget.used != 0 {
		t.Fatal("closed replay retained budget")
	}
}

func TestReplayBudgetWakesAnotherSessionAndCancelsBlockedReader(t *testing.T) {
	budget := &replayBudget{limit: tcpFramePayload}
	traffic := &tcpTraffic{budget: budget}
	id, _ := newSessionID()
	a := newTCPSession(context.Background(), id, newMemoryTCPSocket(make([]byte, 3*tcpFramePayload)), traffic)
	defer a.close()
	wait := func(s *tcpSession) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			n := s.tx.size
			s.mu.Unlock()
			if n == tcpFramePayload {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("reader did not acquire replay budget")
	}
	wait(a)
	b := newTCPSession(context.Background(), id, newMemoryTCPSocket(make([]byte, 3*tcpFramePayload)), traffic)
	defer b.close()
	a.close()
	wait(b)
	b.close()
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.used != 0 {
		t.Fatal("aborted reader retained shared budget")
	}
}

func TestAttachedIdleTCPDoesNotExpire(t *testing.T) {
	id := sessionID{1}
	s := newTCPSession(context.Background(), id, newMemoryTCPSocket(nil))
	defer s.close()
	link, _, err := s.attach(nil, nil, 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.expired(time.Now().Add(24*time.Hour), time.Minute) {
		t.Fatal("attached idle session expired")
	}
	s.detach(link)
	if !s.expired(time.Now().Add(2*time.Minute), time.Minute) {
		t.Fatal("detached session never expires")
	}
}

func BenchmarkUDPReassembly(b *testing.B) {
	id := sessionID{1}
	now := time.Now()
	payload := make([]byte, 1000)
	var r udpReassembler
	for i := uint64(1); i <= 2048; i++ {
		_, _, _ = r.push(udpTestFragment(id, i, 1000, 0, payload), now)
	}
	b.ReportAllocs()
	b.SetBytes(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = r.push(udpTestFragment(id, uint64(i)+3000, 1000, 0, payload), now)
	}
}

func TestUDPFastPathOwnsPayloadAndExpiresWithoutPruneTick(t *testing.T) {
	var r udpReassembler
	now := time.Now()
	id := sessionID{1}
	data := []byte("packet")
	packet, ok, err := r.push(udpTestFragment(id, 1, len(data), 0, data), now)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	data[0] = 'X'
	if string(packet) != "packet" {
		t.Fatal("reassembly retained borrowed input")
	}
	r.nextPrune = now.Add(time.Hour)
	if _, ok, err = r.push(udpTestFragment(id, 1, len(data), 0, data), now.Add(udpFragmentTimeout)); !ok || err != nil {
		t.Fatal("expired duplicate blocked new packet", err)
	}
	_, _, _ = r.push(udpTestFragment(id, 2, 10, 0, []byte("part")), now)
	packet, ok, err = r.push(udpTestFragment(id, 2, 3, 0, []byte("new")), now.Add(udpFragmentTimeout))
	if !ok || err != nil || string(packet) != "new" || r.bytes != 0 {
		t.Fatal("expired assembly contaminated fast path", ok, err, r.bytes)
	}
}
