package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/spf13/cobra"
)

var (
	hostPrepRun     bool
	hostPrepRestore bool
)

var hostPrepCmd = &cobra.Command{
	Use:   "host-prep",
	Short: "Prepare the shared host/VM kernel for Kubernetes (swap off + kernel modules)",
	Long: `Prepare the shared host/VM kernel for Kubernetes-style installers.

vabbe nodes are containers that share ONE kernel, but kubeadm has kernel-global
prerequisites that can't be set per node: swap must be off, a few modules
(ip_vs*, br_netfilter, overlay, nf_conntrack) must be loaded, and inotify limits
raised. host-prep arranges these once — on the host (Linux) or the Docker
Desktop VM.

It is the only command that touches the real host kernel, so it never runs during
'up': it prints a plan by default and only executes with --run (root on Linux).
Undo the swap change with --restore. If you're not running Kubernetes, you most
likely don't need it. See docs/host-prep.md for the why.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		dk, err := NewDocker()
		if err != nil {
			return err
		}
		ctx := context.Background()
		info, err := dk.Info(ctx)
		if err != nil {
			return err
		}
		if isDockerDesktop(info) {
			return prepDockerDesktop(ctx, dk, hostPrepRestore)
		}
		if runtime.GOOS != "linux" {
			return fmt.Errorf("host-prep supports Linux hosts and Docker Desktop (got %q)",
				info.OperatingSystem)
		}
		return prepLinux(hostPrepRestore)
	},
}

const nsenter1Image = "justincormack/nsenter1"

// swapStateFile holds the swap devices active before host-prep disabled them, so
// --restore can re-enable exactly those. It lives in the VM's tmpfs /run: a
// reboot wipes it, but a reboot also re-enables swap on its own, so that's fine.
const swapStateFile = "/run/vabbe-hostprep.swap"

// hostPrepMarker is echoed only if the helper script ran to completion. Its
// absence in the helper output means the command never executed (e.g. the VM
// PID1 namespace couldn't exec the shell) — so we must not report success.
const hostPrepMarker = "__vabbe_host_prep_ok__"

// nsenter1PrepCmd runs under `set -e`, so a real `swapoff -a` failure aborts
// before the marker (modprobe stays tolerant — builtin/loaded modules are fine).
// PATH is set because swapoff/modprobe live in /sbin, not the bare-sh default.
const nsenter1PrepCmd = "set -e\n" +
	"export PATH=/sbin:/usr/sbin:/bin:/usr/bin\n" +
	"swapon --show=NAME --noheadings > " + swapStateFile + " 2>/dev/null || true\n" +
	"swapoff -a\n" +
	"modprobe ip_vs ip_vs_rr ip_vs_wrr ip_vs_sh nf_conntrack br_netfilter overlay 2>/dev/null || true\n" +
	"sysctl -w fs.inotify.max_user_watches=524288 2>/dev/null || true\n" +
	"sysctl -w fs.inotify.max_user_instances=8192 2>/dev/null || true\n" +
	"echo " + hostPrepMarker

// nsenter1RestoreCmd replays the snapshot if present, else falls back to
// `swapon -a` (re-enable fstab swap). swapon is tolerant; the marker confirms it ran.
const nsenter1RestoreCmd = "set -e\n" +
	"export PATH=/sbin:/usr/sbin:/bin:/usr/bin\n" +
	"if [ -s " + swapStateFile + " ]; then " +
	"while IFS= read -r d; do swapon \"$d\" 2>/dev/null || true; done < " + swapStateFile + "; " +
	"else swapon -a 2>/dev/null || true; fi\n" +
	"echo " + hostPrepMarker

// prepDockerDesktop runs a short-lived privileged container joined to the
// Docker Desktop VM's PID 1 namespace so it can swapoff/modprobe (or restore
// swap on) the VM kernel.
func prepDockerDesktop(ctx context.Context, dk *Docker, restore bool) error {
	cmdStr := nsenter1PrepCmd
	if restore {
		cmdStr = nsenter1RestoreCmd
	}
	fmt.Printf("Docker Desktop detected. Running a privileged helper to enter the VM PID 1 namespace:\n")
	fmt.Printf("  image: %s\n", nsenter1Image)
	fmt.Printf("  cmd:   %s\n", cmdStr)
	if !hostPrepRun {
		fmt.Println("(plan only — nothing was executed; re-run with --run to apply)")
		return nil
	}
	cli := dk.c
	if err := dk.ensureImage(ctx, nsenter1Image); err != nil {
		return err
	}
	// Clear any helper left behind by a previous crashed run before reusing the name.
	_ = cli.ContainerRemove(ctx, "vabbe-host-prep", container.RemoveOptions{Force: true})
	created, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: nsenter1Image,
			// Absolute path: nsenter1 execs the command in the VM PID1 namespace
			// without PATH resolution, so a bare "sh" fails with ENOENT.
			Cmd:          []string{"/bin/sh", "-c", cmdStr},
			AttachStdout: true,
			AttachStderr: true,
		},
		&container.HostConfig{
			Privileged: true,
			PidMode:    "host",
		},
		nil, nil, "vabbe-host-prep")
	if err != nil {
		return fmt.Errorf("create helper: %w", err)
	}
	// Remove the helper ourselves once output and exit code are captured. With
	// AutoRemove the daemon could delete it mid-read, so a successful prep would
	// still error with "no such container"/missing log file.
	defer func() {
		_ = cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	}()
	// Subscribe to the exit event before starting so the wait can never miss it.
	statusC, errC := cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start helper: %w", err)
	}
	var out bytes.Buffer
	if logs, err := cli.ContainerLogs(ctx, created.ID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true,
	}); err == nil {
		// Demux the multiplexed (non-TTY) stream and capture it so we can both
		// show it and check the completion marker.
		_, _ = stdcopy.StdCopy(&out, &out, logs)
		_ = logs.Close()
	}
	var code int64
	select {
	case status := <-statusC:
		code = status.StatusCode
	case err := <-errC:
		return err
	}
	// Show the helper output (minus the marker line).
	if shown := strings.TrimSpace(strings.ReplaceAll(out.String(), hostPrepMarker, "")); shown != "" {
		fmt.Println(shown)
	}
	if code != 0 {
		return fmt.Errorf("host-prep helper exited with code %d (see output above)", code)
	}
	// The marker is missing if the shell never ran (e.g. exec failure) — do not
	// claim success when nothing happened.
	if !strings.Contains(out.String(), hostPrepMarker) {
		return fmt.Errorf("host-prep helper did not run to completion; the VM PID1 namespace may not have executed the command")
	}
	if restore {
		fmt.Println("✓ swap restored on Docker Desktop VM.")
	} else {
		fmt.Println("✓ host-prep done on Docker Desktop VM.")
	}
	return nil
}

// linuxSwapStateFile is where the Linux path snapshots active swap devices.
func linuxSwapStateFile() string { return filepath.Join(os.TempDir(), "vabbe-host-prep.swap") }

// prepLinux prints the kernel ops it would run and, only with --run, executes
// them directly — host-prep is the explicit exception to the "Engine API only"
// rule: it touches the real host kernel. It never invokes sudo itself (that
// would hide privilege escalation and hang in CI); --run requires root.
func prepLinux(restore bool) error {
	if err := requireRoot(restore); err != nil {
		return err
	}
	if restore {
		return restoreLinux()
	}
	fmt.Println("Linux host detected. Commands:")
	saveLinuxSwapState()
	for _, c := range [][]string{
		{"swapoff", "-a"},
		{"modprobe", "ip_vs"},
		{"modprobe", "ip_vs_rr"},
		{"modprobe", "ip_vs_wrr"},
		{"modprobe", "ip_vs_sh"},
		{"modprobe", "nf_conntrack"},
		{"modprobe", "br_netfilter"},
		{"modprobe", "overlay"},
		// inotify limits are host-global (not namespaced); raise them so many
		// pods don't hit "too many open files".
		{"sysctl", "-w", "fs.inotify.max_user_watches=524288"},
		{"sysctl", "-w", "fs.inotify.max_user_instances=8192"},
	} {
		runCmd(c)
	}
	fmt.Println(doneOrPlan("host-prep done on Linux host."))
	return nil
}

// requireRoot fails when --run is set but we are not effectively root, pointing
// at the exact re-run command so the user owns the privilege escalation. Without
// --run we only print the plan, so root is not needed.
func requireRoot(restore bool) error {
	if !hostPrepRun || os.Geteuid() == 0 {
		return nil
	}
	flag := ""
	if restore {
		flag = " --restore"
	}
	return fmt.Errorf("host-prep --run touches the host kernel and must run as root; re-run as root, e.g. `sudo vabbe host-prep%s --run`", flag)
}

// doneOrPlan returns the success line when executing, or a hint that nothing ran.
func doneOrPlan(done string) string {
	if hostPrepRun {
		return "✓ " + done
	}
	return "(plan only — re-run as root with --run to execute)"
}

// saveLinuxSwapState records active swap devices (best-effort) so restoreLinux
// can re-enable exactly those.
func saveLinuxSwapState() {
	if !hostPrepRun {
		return
	}
	out, err := exec.Command("swapon", "--show=NAME", "--noheadings").Output()
	if err != nil {
		return
	}
	_ = os.WriteFile(linuxSwapStateFile(), out, 0o644)
}

// restoreLinux re-enables the snapshotted swap devices, falling back to
// `swapon -a` when no snapshot exists (the hybrid restore).
func restoreLinux() error {
	fmt.Println("Restoring swap on Linux host. Commands:")
	var cmds [][]string
	if data, err := os.ReadFile(linuxSwapStateFile()); err == nil && len(strings.Fields(string(data))) > 0 {
		for _, dev := range strings.Fields(string(data)) {
			cmds = append(cmds, []string{"swapon", dev})
		}
	} else {
		cmds = append(cmds, []string{"swapon", "-a"})
	}
	for _, c := range cmds {
		runCmd(c)
	}
	fmt.Println(doneOrPlan("swap restored on Linux host."))
	return nil
}

// runCmd prints and runs a command, continuing (not failing the whole run) if
// it errors — a missing module or already-on swap should not abort host-prep.
func runCmd(c []string) {
	fmt.Printf("  %s\n", strings.Join(c, " "))
	if !hostPrepRun {
		return
	}
	cmd := exec.Command(c[0], c[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("    (failed, continuing) %s\n", err)
	}
}

func init() {
	rootCmd.AddCommand(hostPrepCmd)
	hostPrepCmd.Flags().BoolVar(&hostPrepRun, "run", false,
		"actually execute the host changes (default: print the plan only); on Linux requires root")
	hostPrepCmd.Flags().BoolVar(&hostPrepRestore, "restore", false,
		"re-enable swap captured by a previous host-prep (falls back to `swapon -a`)")
}
