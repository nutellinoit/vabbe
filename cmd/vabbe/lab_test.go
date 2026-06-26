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
