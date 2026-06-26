package main

import "testing"

func TestZoneHost(t *testing.T) {
	cases := []struct {
		name, ip, zone, want string
	}{
		{"cp0", "10.202.1.3", "nip.io", "cp0-10-202-1-3.nip.io"},
		{"runner", "10.10.10.10", "nip.io", "runner-10-10-10-10.nip.io"},
		{"haproxy", "192.168.0.2", "sslip.io", "haproxy-192-168-0-2.sslip.io"},
	}
	for _, c := range cases {
		if got := zoneHost(c.name, c.ip, c.zone); got != c.want {
			t.Errorf("zoneHost(%q, %q, %q) = %q, want %q", c.name, c.ip, c.zone, got, c.want)
		}
	}
}
