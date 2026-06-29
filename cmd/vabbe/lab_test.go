package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalLab = `
name: e2e
network: { subnet: 10.10.1.0/24 }
nodes:
  - { name: a, ip: 10.10.1.2 }
  - { name: b, ip: 10.10.1.3 }
`

func writeLab(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "vabbe.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Regression: a relative -f path (e.g. the default "vabbe.yaml") left lab.dir
// as ".", so a relative mount like "./:/workspace" resolved to the bare "."
// bind source, which Docker rejects as a too-short volume name.
func TestLoadAbsDirAndBind(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join(dir, "vabbe.yaml")
	if err := os.WriteFile(rel, []byte(minimalLab), 0o644); err != nil {
		t.Fatal(err)
	}
	lab, err := Load(rel)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(lab.Dir()) {
		t.Fatalf("lab.Dir() must be absolute, got %q", lab.Dir())
	}
	got := absBind(lab.Dir(), "./:/workspace")
	src := strings.SplitN(got, ":", 3)[0]
	if !filepath.IsAbs(src) {
		t.Fatalf("bind source must be absolute, got %q (from %q)", src, got)
	}
}

func TestLoadMinimal(t *testing.T) {
	dir := t.TempDir()
	p := writeLab(t, dir, minimalLab)
	lab, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(lab.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(lab.Nodes))
	}
	if lab.Nodes[0].Name != "a" || lab.Nodes[0].IP != "10.10.1.2" {
		t.Errorf("node a: %+v", lab.Nodes[0])
	}
	if lab.Nodes[0].Image != DefaultImage {
		t.Errorf("default image not applied: %q", lab.Nodes[0].Image)
	}
	if lab.Nodes[0].Privileged == nil || !*lab.Nodes[0].Privileged {
		t.Errorf("privileged should default true")
	}
}

func TestLoadRejectsBadAndDuplicatePorts(t *testing.T) {
	cases := map[string]string{
		"bad protocol": `
name: p
network: { subnet: 10.10.1.0/24 }
nodes:
  - { name: a, ip: 10.10.1.2, ports: ["80:80/icmp"] }
`,
		"out of range": `
name: p
network: { subnet: 10.10.1.0/24 }
nodes:
  - { name: a, ip: 10.10.1.2, ports: ["99999:80"] }
`,
		"duplicate host port across nodes": `
name: p
network: { subnet: 10.10.1.0/24 }
nodes:
  - { name: a, ip: 10.10.1.2, ports: ["8080:80"] }
  - { name: b, ip: 10.10.1.3, ports: ["8080:443"] }
`,
	}
	for name, bad := range cases {
		p := writeLab(t, t.TempDir(), bad)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestLoadRuntimeMerge(t *testing.T) {
	cfg := `
name: rt
defaults: { runtime: kata }
nodes:
  - { name: a }
  - { name: b, runtime: runc }
`
	p := writeLab(t, t.TempDir(), cfg)
	lab, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lab.Nodes[0].Runtime != "kata" {
		t.Errorf("node a should inherit defaults.runtime=kata, got %q", lab.Nodes[0].Runtime)
	}
	if lab.Nodes[1].Runtime != "runc" {
		t.Errorf("node b should keep its override runc, got %q", lab.Nodes[1].Runtime)
	}
}

func TestLoadRuntimeDefaultsEmpty(t *testing.T) {
	cfg := `
name: rt
nodes:
  - { name: a }
`
	p := writeLab(t, t.TempDir(), cfg)
	lab, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lab.Nodes[0].Runtime != "" {
		t.Errorf("runtime should stay empty (daemon default), got %q", lab.Nodes[0].Runtime)
	}
}

func hasCap(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func TestRuntimeNodeKataSystemd(t *testing.T) {
	cfg := `
name: rt
defaults: { runtime: kata }
nodes:
  - { name: auto }
  - { name: custom, cmd: ["/usr/sbin/sshd","-D"] }
  - { name: runner, runner: true }
  - { name: plain, runtime: runc }
`
	p := writeLab(t, t.TempDir(), cfg)
	lab, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	byName := map[string]*Node{}
	for i := range lab.Nodes {
		byName[lab.Nodes[i].Name] = &lab.Nodes[i]
	}
	// kata node with no cmd gets the cgroup-remount-then-systemd shim + SYS_ADMIN,
	// so systemd boots as PID1 inside the VM.
	got := byName["auto"].Cmd
	if len(got) != 3 || got[2] != "mount -o remount,rw /sys/fs/cgroup 2>/dev/null; exec /sbin/init" {
		t.Errorf("auto node should default to the systemd shim cmd, got %v", got)
	}
	if !hasCap(byName["auto"].Caps, "SYS_ADMIN") {
		t.Errorf("auto kata node should get SYS_ADMIN, got %v", byName["auto"].Caps)
	}
	// explicit cmd wins, but SYS_ADMIN is still added (needed for cgroup rw).
	if c := byName["custom"].Cmd; len(c) != 2 || c[0] != "/usr/sbin/sshd" {
		t.Errorf("custom node cmd should be preserved, got %v", c)
	}
	if !hasCap(byName["custom"].Caps, "SYS_ADMIN") {
		t.Errorf("custom kata node should still get SYS_ADMIN, got %v", byName["custom"].Caps)
	}
	// runner opts out entirely (manages its own entrypoint, no SYS_ADMIN).
	if c := byName["runner"].Cmd; len(c) != 0 {
		t.Errorf("runner should not get the systemd cmd, got %v", c)
	}
	if hasCap(byName["runner"].Caps, "SYS_ADMIN") {
		t.Errorf("runner should not get SYS_ADMIN")
	}
	// explicit runtime: runc shares the host kernel — systemd boots normally, no
	// shim/cap needed and the image CMD is kept.
	if c := byName["plain"].Cmd; len(c) != 0 {
		t.Errorf("runc node should keep image CMD, got %v", c)
	}
	if hasCap(byName["plain"].Caps, "SYS_ADMIN") {
		t.Errorf("runc node should not get SYS_ADMIN")
	}
}

func TestLoadCpusMemory(t *testing.T) {
	cfg := `
name: rt
defaults: { cpus: 2, memory: 2g }
nodes:
  - { name: a }
  - { name: b, cpus: 4, memory: 8g }
`
	p := writeLab(t, t.TempDir(), cfg)
	lab, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lab.Nodes[0].Cpus != 2 || lab.Nodes[0].Memory != "2g" {
		t.Errorf("node a should inherit defaults cpus=2 memory=2g, got %v/%q", lab.Nodes[0].Cpus, lab.Nodes[0].Memory)
	}
	if lab.Nodes[1].Cpus != 4 || lab.Nodes[1].Memory != "8g" {
		t.Errorf("node b should keep its override cpus=4 memory=8g, got %v/%q", lab.Nodes[1].Cpus, lab.Nodes[1].Memory)
	}
	if b, err := parseMemory(lab.Nodes[1].Memory); err != nil || b != 8*1024*1024*1024 {
		t.Errorf("parseMemory(8g) = %d, %v", b, err)
	}
}

func TestLoadRejectsBadMemory(t *testing.T) {
	bad := `
name: bad
nodes:
  - { name: a, memory: "not-a-size" }
`
	p := writeLab(t, t.TempDir(), bad)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for invalid memory")
	}
}

func TestLoadAutoNetwork(t *testing.T) {
	auto := `
name: auto
nodes:
  - { name: a }
  - { name: b }
`
	p := writeLab(t, t.TempDir(), auto)
	lab, err := Load(p)
	if err != nil {
		t.Fatalf("auto network/ip should load: %v", err)
	}
	if lab.Network.Subnet != "" || lab.Nodes[0].IP != "" {
		t.Errorf("expected empty subnet/ip, got subnet=%q ip=%q", lab.Network.Subnet, lab.Nodes[0].IP)
	}
}

func TestLoadRejectsIPWithoutSubnet(t *testing.T) {
	bad := `
name: bad
nodes:
  - { name: a, ip: 10.10.1.2 }
`
	p := writeLab(t, t.TempDir(), bad)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error: ip set without a subnet")
	}
}

func TestLoadRejectsIPNotInSubnet(t *testing.T) {
	dir := t.TempDir()
	bad := `
name: bad
network: { subnet: 10.10.1.0/24 }
nodes:
  - { name: a, ip: 192.168.1.5 }
`
	p := writeLab(t, dir, bad)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for ip not in subnet")
	}
}

func TestLoadRejectsDuplicateIPs(t *testing.T) {
	dir := t.TempDir()
	bad := `
name: bad
network: { subnet: 10.10.1.0/24 }
nodes:
  - { name: a, ip: 10.10.1.5 }
  - { name: b, ip: 10.10.1.5 }
`
	p := writeLab(t, dir, bad)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for duplicate IPs")
	}
}

func TestLoadRejectsDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	bad := `
name: bad
network: { subnet: 10.10.1.0/24 }
nodes:
  - { name: dup, ip: 10.10.1.5 }
  - { name: dup, ip: 10.10.1.6 }
`
	p := writeLab(t, dir, bad)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for duplicate names")
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("FOO", "bar")
	dir := t.TempDir()
	yaml := `
name: env
network: { subnet: 10.10.1.0/24 }
defaults:
  image: ghcr.io/nutellinoit/vabbe-node:24.04
nodes:
  - name: n
    ip: 10.10.1.5
    env:
      TOKEN: ${FOO}
      LIT: hello
`
	p := writeLab(t, dir, yaml)
	lab, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v := lab.Nodes[0].Env["TOKEN"]; v != "bar" {
		t.Errorf("TOKEN: want bar, got %q", v)
	}
	if v := lab.Nodes[0].Env["LIT"]; v != "hello" {
		t.Errorf("LIT: %q", v)
	}
}

func TestDefaultsMerge(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: m
network: { subnet: 10.0.0.0/24 }
defaults:
  image: myimage:1
  privileged: false
nodes:
  - { name: a, ip: 10.0.0.2 }
  - { name: b, ip: 10.0.0.3, image: other:2 }
`
	p := writeLab(t, dir, yaml)
	lab, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lab.Nodes[0].Image != "myimage:1" {
		t.Errorf("a image: %q", lab.Nodes[0].Image)
	}
	if lab.Nodes[1].Image != "other:2" {
		t.Errorf("b image should override: %q", lab.Nodes[1].Image)
	}
	if lab.Nodes[0].Privileged == nil || *lab.Nodes[0].Privileged {
		t.Errorf("a privileged should be false (from defaults)")
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"simple":          "'simple'",
		"two words":       "'two words'",
		"select x from t": "'select x from t'",
		"it's":            `'it'\''s'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
