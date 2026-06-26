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
