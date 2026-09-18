package core

import (
	"reflect"
	"testing"
)

func TestHostInterfaceCandidatePolicy(t *testing.T) {
	interfaces := []InterfaceSnapshot{
		{Name: "wlan0", Up: true, Addresses: []string{"2001:db8::1", "2001:0db8::1", "fd00::1", "fe80::1", "127.0.0.1", "192.168.1.2", "::", "ff02::1", "bad"}},
		{Name: "rmnet_data0", Up: true, Addresses: []string{"2001:db8::2", "2001:db8::1"}},
		{Name: "tun0", Up: true, Addresses: []string{"2001:db8::3"}},
		{Name: "eth0", Up: false, Addresses: []string{"2001:db8::4"}},
		{Name: "lo", Up: true, Loopback: true, Addresses: []string{"2001:db8::5"}},
	}
	for _, tc := range []struct {
		name string
		cfg  Config
		want []Candidate
	}{
		{"default", Config{}, []Candidate{{"2001:db8::1", 55140}, {"2001:db8::2", 55140}}},
		{"selected interface", Config{CandidateInterfaces: []string{" rmnet_data0 "}}, []Candidate{{"2001:db8::2", 55140}, {"2001:db8::1", 55140}}},
		{"explicit tunnel opt in", Config{CandidateInterfaces: []string{"tun0"}}, []Candidate{{"2001:db8::3", 55140}}},
		{"absent interface", Config{CandidateInterfaces: []string{"missing"}}, nil},
		{"manual precedence", Config{CandidateInterfaces: []string{"missing"}, CandidateAddresses: []string{" 2001:db8::9 ", "2001:0db8::9"}}, []Candidate{{"2001:db8::9", 55140}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CandidatesFromInterfaces(tc.cfg, 55140, interfaces)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("candidates = %+v, err = %v, want %+v", got, err, tc.want)
			}
		})
	}
}

func TestHostCandidatesRejectInvalidExplicitAddresses(t *testing.T) {
	for _, address := range []string{"not-an-ip", "192.168.1.1", "fd00::1", "fe80::1", "::1"} {
		if _, err := CandidatesFromInterfaces(Config{CandidateAddresses: []string{address}}, 55140, nil); err == nil {
			t.Fatalf("accepted invalid manual address %q", address)
		}
	}
}
