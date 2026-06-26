package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestIntegrationLab brings up a real two-node lab against the Docker daemon and
// exercises the behaviours that unit tests cannot: the runner actually starts,
// its relative "./:/workspace" mount resolves to an absolute bind, node-to-node
// SSH works with the lab key, and `up` reports config drift. It is gated behind
// VABBE_IT so the default `go test ./...` stays fast and Docker-free.
//
//	VABBE_IT=1 go test ./cmd/vabbe -run TestIntegrationLab -v
//
// Override the image with VABBE_IT_IMAGE (defaults to the published rc image).
func TestIntegrationLab(t *testing.T) {
	if os.Getenv("VABBE_IT") == "" {
		t.Skip("set VABBE_IT=1 to run Docker integration tests")
	}
	img := os.Getenv("VABBE_IT_IMAGE")
	if img == "" {
		img = "ghcr.io/nutellinoit/vabbe-node:rc"
	}

	dir := t.TempDir()
	cfg := filepath.Join(dir, "vabbe.yaml")
	yaml := fmt.Sprintf(`
name: vabbeit
network: { subnet: 10.211.7.0/24 }
defaults: { image: %s, privileged: true }
nodes:
  - { name: cp0, ip: 10.211.7.3, ports: ["127.0.0.1:12222:22"] }
  - name: runner
    ip: 10.211.7.250
    image: %s
    entrypoint: ["/bin/sleep", "infinity"]
    runner: true
    mounts: ["./:/workspace"]
`, img, img)
	if err := os.WriteFile(cfg, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	lab, err := Load(cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dk, err := NewDocker()
	if err != nil {
		t.Fatalf("NewDocker: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _, _ = dk.Down(ctx, lab, false) })

	if err := dk.EnsureNetwork(ctx, lab); err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	_, pub, err := EnsureKeypair(lab.VabbeDir())
	if err != nil {
		t.Fatalf("EnsureKeypair: %v", err)
	}
	for i := range lab.Nodes {
		if _, _, err := dk.EnsureNode(ctx, lab, &lab.Nodes[i], pub, false); err != nil {
			t.Fatalf("EnsureNode %s: %v", lab.Nodes[i].Name, err)
		}
	}

	// Runner must be up with its relative mount resolved to an absolute bind:
	// this is the regression that produced "create .: volume name is too short".
	if err := dk.Exec(ctx, lab.Name, "runner", []string{"test", "-f", "/workspace/vabbe.yaml"}, false); err != nil {
		t.Fatalf("runner /workspace not mounted (the ./:/workspace bind): %v", err)
	}

	// Node-to-node SSH with the injected lab key. sshd on cp0 needs a moment, so
	// retry briefly before failing.
	ssh := []string{"ssh", "-o", "StrictHostKeyChecking=no", "-o", "BatchMode=yes",
		"-o", "ConnectTimeout=3", "-i", "/root/.ssh/id_ed25519", "root@10.211.7.3", "true"}
	var sshErr error
	for i := 0; i < 45; i++ {
		if sshErr = dk.Exec(ctx, lab.Name, "runner", ssh, false); sshErr == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if sshErr != nil {
		t.Logf("cp0 systemd/sshd state (diagnostics):")
		_ = dk.Exec(ctx, lab.Name, "cp0", []string{"sh", "-c",
			"systemctl is-system-running; echo '--- failed units ---'; " +
				"systemctl --failed --no-legend --no-pager; echo '--- sshd ---'; " +
				"systemctl status sshd ssh --no-pager 2>&1 | head -40"}, false)
		t.Fatalf("runner could not SSH cp0 with lab key: %v", sshErr)
	}

	// Node DNS: vabbe-resolv must have replaced Docker's 127.0.0.11 with the
	// configured upstream (default 1.1.1.1), or pods (CoreDNS) can't resolve.
	if err := dk.Exec(ctx, lab.Name, "cp0", []string{"sh", "-c",
		"grep -q '1.1.1.1' /etc/resolv.conf && ! grep -q '127.0.0.11' /etc/resolv.conf"}, false); err != nil {
		_ = dk.Exec(ctx, lab.Name, "cp0", []string{"cat", "/etc/resolv.conf"}, false)
		t.Fatalf("cp0 resolv.conf not rewritten to a pod-reachable upstream: %v", err)
	}

	// Published port: cp0's sshd (127.0.0.1:12222 -> 22) must answer on the host.
	var banner string
	for i := 0; i < 15; i++ {
		if c, derr := net.DialTimeout("tcp", "127.0.0.1:12222", 3*time.Second); derr == nil {
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			b := make([]byte, 64)
			n, _ := c.Read(b)
			_ = c.Close()
			banner = string(b[:n])
			if strings.HasPrefix(banner, "SSH-") {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.HasPrefix(banner, "SSH-") {
		t.Fatalf("published port 127.0.0.1:12222 did not reach cp0 sshd (got %q)", banner)
	}

	// Drift warning: change the runner entrypoint and reconcile. up must report
	// the change instead of silently doing nothing.
	lab.Nodes[1].Entrypoint = []string{"/bin/sleep", "120"}
	warns, recreated, err := dk.EnsureNode(ctx, lab, &lab.Nodes[1], pub, false)
	if err != nil {
		t.Fatalf("EnsureNode reconcile: %v", err)
	}
	if recreated {
		t.Fatal("node must not be recreated without --recreate")
	}
	if !slices.Contains(warns, "entrypoint") {
		t.Fatalf("expected entrypoint drift warning, got %v", warns)
	}

	// With recreate=true the drifted node is rebuilt and reported as recreated.
	warns, recreated, err = dk.EnsureNode(ctx, lab, &lab.Nodes[1], pub, true)
	if err != nil {
		t.Fatalf("EnsureNode recreate: %v", err)
	}
	if !recreated || !slices.Contains(warns, "entrypoint") {
		t.Fatalf("expected recreate on entrypoint drift, got recreated=%v warns=%v", recreated, warns)
	}
}
