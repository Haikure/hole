package core

import "testing"

func TestICESnapshotSortsPeersByDeviceAndTransport(t *testing.T) {
	c := &iceCoordinator{peers: map[string]*icePeer{
		"transport-z": {coordinator: nil, stats: PeerTransportSnapshot{PeerID: "device-b", TransportID: "transport-z"}},
		"transport-a": {coordinator: nil, stats: PeerTransportSnapshot{PeerID: "device-a", TransportID: "transport-a"}},
		"transport-y": {coordinator: nil, stats: PeerTransportSnapshot{PeerID: "device-b", TransportID: "transport-y"}},
	}}
	for _, peer := range c.peers {
		peer.coordinator = c
	}
	got := c.snapshot()
	want := []struct{ peer, transport string }{
		{peer: "device-a", transport: "transport-a"},
		{peer: "device-b", transport: "transport-y"},
		{peer: "device-b", transport: "transport-z"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d peers, want %d", len(got), len(want))
	}
	for i, expected := range want {
		if got[i].PeerID != expected.peer || got[i].TransportID != expected.transport {
			t.Fatalf("peer %d: got %s/%s, want %s/%s", i, got[i].PeerID, got[i].TransportID, expected.peer, expected.transport)
		}
	}
}
