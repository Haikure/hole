package main

import (
	"context"
	"testing"
)

func TestResolveServersLiteralDedupAndIPv4(t *testing.T) {
	servers, failures := resolveServers(context.Background(), []string{"stun:192.0.2.1:3478", "stun:192.0.2.1:3478", "stun:192.0.2.1:19302", "stun:[2001:db8::1]:3478"})
	if len(servers) != 2 || len(failures) != 1 {
		t.Fatal(servers, failures)
	}
	if servers[0].String() != "192.0.2.1:3478" || servers[1].String() != "192.0.2.1:19302" {
		t.Fatal(servers)
	}
}

func TestResolveServersCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	servers, _ := resolveServers(ctx, defaultSTUNServers)
	if len(servers) != 0 {
		t.Fatal(servers)
	}
}
