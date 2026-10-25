package core

import "testing"

func TestFixedEndpointConfig(t *testing.T) {
	for _, preferred := range []string{"ice", "auto"} {
		for _, address := range []string{"203.0.113.10:50000", "[2001:db8::1]:65535", "gateway.example.com:1", "localhost:443"} {
			cfg := validConfig()
			cfg.Transport = TransportConfig{Preferred: preferred, FixedEndpoint: &FixedEndpointConfig{Listen: "[::]:40000", Advertise: address}}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			normalized := cfg.Normalized()
			normalized.Transport.FixedEndpoint.Advertise = "changed.example:22"
			if cfg.Transport.FixedEndpoint.Advertise != address {
				t.Fatal("configuration pointer was not detached")
			}
		}
	}
	for _, endpoint := range []FixedEndpointConfig{
		{}, {Listen: "0.0.0.0:40000"}, {Advertise: "example.com:40000"},
		{Listen: "localhost:40000", Advertise: "example.com:40000"},
		{Listen: ":40000", Advertise: "example.com:40000"},
		{Listen: "0.0.0.0:0", Advertise: "example.com:40000"},
		{Listen: "[::]:65536", Advertise: "example.com:40000"},
		{Listen: "127.0.0.1:40000", Advertise: "0.0.0.0:40000"},
		{Listen: "127.0.0.1:40000", Advertise: "[::]:40000"},
		{Listen: "127.0.0.1:40000", Advertise: "example.com:0"},
		{Listen: "127.0.0.1:40000", Advertise: "bad/path:44"},
		{Listen: "127.0.0.1:40000", Advertise: "-bad.example:44"},
		{Listen: "127.0.0.1:40000", Advertise: "[::%lo]:44"},
	} {
		cfg := validConfig()
		cfg.Transport = TransportConfig{Preferred: PreferredICE, FixedEndpoint: &endpoint}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted %+v", endpoint)
		}
	}
	for _, preferred := range []string{PreferredIPv6, PreferredICE} {
		cfg := validConfig()
		cfg.Transport = TransportConfig{Preferred: preferred, FixedEndpoint: &FixedEndpointConfig{"0.0.0.0:40000", "example.com:50000"}}
		cfg.ICE.RelayOnly = preferred == PreferredICE
		if err := cfg.Validate(); err == nil {
			t.Fatal("accepted incompatible transport policy")
		}
	}
	text := "transport:\n  preferred: ice\n  fixed_endpoint:\n    listen: 0.0.0.0:40000\n    advertise: gateway.example:50000\n"
	cfg, err := ParseConfig([]byte(text))
	if err != nil || cfg.Transport.FixedEndpoint == nil || cfg.Transport.FixedEndpoint.Advertise != "gateway.example:50000" {
		t.Fatal(cfg, err)
	}
	if _, err := ParseConfig([]byte(text + "    typo: no\n")); err == nil {
		t.Fatal("unknown fixed field accepted")
	}
}
