package main

import "testing"

func TestStrSliceEqual(t *testing.T) {
	cases := []struct {
		a    []string
		b    []string
		want bool
	}{
		{nil, nil, true},
		{[]string{"/bin/sleep", "infinity"}, []string{"/bin/sleep", "infinity"}, true},
		{[]string{"/sbin/init"}, []string{"/bin/sleep", "infinity"}, false},
		{[]string{"a"}, []string{"a", "b"}, false},
		{[]string{"a", "b"}, []string{"b", "a"}, false},
	}
	for _, c := range cases {
		if got := strSliceEqual(c.a, c.b); got != c.want {
			t.Errorf("strSliceEqual(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestEnvHas(t *testing.T) {
	env := []string{"PATH=/usr/bin", "GITHUB_TOKEN=abc", "FOO=bar"}
	if !envHas(env, "GITHUB_TOKEN", "abc") {
		t.Error("expected GITHUB_TOKEN=abc to be present")
	}
	if envHas(env, "GITHUB_TOKEN", "xyz") {
		t.Error("GITHUB_TOKEN=xyz must not match a different value")
	}
	if envHas(env, "MISSING", "x") {
		t.Error("missing key must not match")
	}
}

func TestPortBound(t *testing.T) {
	pm := parsePortBindings([]string{"8080:80"})
	if !portBound(pm, "8080:80") {
		t.Error("identical port spec should be bound")
	}
	if portBound(pm, "9090:90") {
		t.Error("unbound container port must report not bound")
	}
}

func TestParsePort(t *testing.T) {
	cases := []struct {
		in                    string
		ip, host, node, proto string
		ok                    bool
	}{
		{"80", "", "80", "80", "tcp", true},
		{"8080:80", "", "8080", "80", "tcp", true},
		{"8080:80/udp", "", "8080", "80", "udp", true},
		{"127.0.0.1:6443:6443", "127.0.0.1", "6443", "6443", "tcp", true},
		{"127.0.0.1:53:53/udp", "127.0.0.1", "53", "53", "udp", true},
		{"a:b:c:d", "", "", "", "", false},
		{":80", "", "", "", "", false},
	}
	for _, c := range cases {
		s, ok := parsePort(c.in)
		if ok != c.ok {
			t.Errorf("parsePort(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if s.ip != c.ip || s.host != c.host || s.node != c.node || s.proto != c.proto {
			t.Errorf("parsePort(%q) = %+v, want ip=%s host=%s node=%s proto=%s", c.in, s, c.ip, c.host, c.node, c.proto)
		}
	}
}
