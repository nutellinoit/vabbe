package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/spf13/cobra"
)

var (
	upRecreate bool
	upWait     bool
	upTimeout  time.Duration
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Create or reconcile the lab (network + all nodes), idempotent",
	RunE: func(*cobra.Command, []string) error {
		lab, dk, err := loadAndDocker()
		if err != nil {
			return err
		}
		ctx := context.Background()
		if err := dk.EnsureNetwork(ctx, lab); err != nil {
			return err
		}
		_, pub, err := EnsureKeypair(lab.VabbeDir())
		if err != nil {
			return err
		}
		subnet := lab.Network.Subnet
		if subnet == "" {
			subnet = "auto"
		}
		fmt.Printf("lab %s on subnet %s\n", bold(lab.Name), subnet)
		for i := range lab.Nodes {
			n := &lab.Nodes[i]
			warns, recreated, err := dk.EnsureNode(ctx, lab, n, pub, upRecreate)
			if err != nil {
				return err
			}
			ip := n.IP
			if ip == "" { // auto-assigned — show the address Docker gave it
				if a, e := dk.IP(ctx, lab.Name, n.Name); e == nil {
					ip = a
				}
			}
			fmt.Printf("  %s %s %s (%s)\n", green("✓"), n.Name, ip, n.Image)
			switch {
			case recreated:
				fmt.Printf("    %s recreated (config changed: %s)\n", cyan("~"), strings.Join(warns, ", "))
			case len(warns) > 0:
				fmt.Printf("    %s config changed (%s): run `vabbe down`/`up` or `up --recreate` to apply\n", yellow("!"), strings.Join(warns, ", "))
			}
		}
		if upWait {
			if err := waitReady(ctx, dk, lab, upTimeout); err != nil {
				return err
			}
		}
		return nil
	},
}

// waitReady blocks until every node is reachable (sshd up for server nodes),
// so a following `ansible`/provisioning step does not race container boot.
func waitReady(ctx context.Context, dk *Docker, lab *Lab, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for i := range lab.Nodes {
		n := &lab.Nodes[i]
		for !dk.Reachable(ctx, lab, n) {
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for %s to become ready", n.Name)
			}
			time.Sleep(time.Second)
		}
		fmt.Printf("  %s %s ready\n", green("✓"), n.Name)
	}
	return nil
}

var (
	downKeepNet bool
	downAll     bool
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Tear everything down by label (containers + network)",
	RunE: func(*cobra.Command, []string) error {
		ctx := context.Background()
		// --all ignores the config and removes every vabbe-managed lab on the
		// daemon, so it can clean up orphans whose vabbe.yaml is gone.
		if downAll {
			dk, err := NewDocker()
			if err != nil {
				return err
			}
			removed, err := dk.DownAll(ctx, downKeepNet)
			if err != nil {
				return err
			}
			for _, n := range removed {
				fmt.Printf("  removed container %s\n", n)
			}
			fmt.Printf("  removed %d vabbe container(s); %s\n", len(removed),
				map[bool]string{true: "kept networks", false: "removed networks"}[downKeepNet])
			return nil
		}
		lab, dk, err := loadAndDocker()
		if err != nil {
			return err
		}
		removed, err := dk.Down(ctx, lab, downKeepNet)
		if err != nil {
			return err
		}
		for _, n := range removed {
			fmt.Printf("  removed container %s\n", n)
		}
		what := "removed network"
		if downKeepNet {
			what = "kept network"
		}
		fmt.Printf("  %s %s\n", what, lab.Name)
		return nil
	},
}

var lsJSON bool

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List nodes in the lab",
	RunE: func(*cobra.Command, []string) error {
		lab, dk, err := loadAndDocker()
		if err != nil {
			return err
		}
		cs, err := dk.ListByLab(context.Background(), lab.Name)
		if err != nil {
			return err
		}
		type row struct {
			Node   string `json:"node"`
			IP     string `json:"ip"`
			Image  string `json:"image"`
			Status string `json:"status"`
		}
		rows := make([]row, 0, len(cs))
		for _, c := range cs {
			ip := ""
			if c.NetworkSettings != nil {
				if ep := c.NetworkSettings.Networks[lab.Name]; ep != nil {
					if ep.IPAMConfig != nil && ep.IPAMConfig.IPv4Address != "" {
						ip = ep.IPAMConfig.IPv4Address
					} else {
						ip = ep.IPAddress
					}
				}
			}
			status := c.State
			if c.State == "running" {
				status = "Up"
			}
			rows = append(rows, row{c.Names[0][1:], ip, c.Image, status})
		}
		if lsJSON {
			b, _ := json.MarshalIndent(rows, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		fmt.Printf("%-20s %-18s %-30s %s\n", "NODE", "IP", "IMAGE", "STATUS")
		for _, r := range rows {
			s := r.Status
			switch s {
			case "Up":
				s = green(s)
			case "restarting", "paused":
				s = yellow(s)
			default:
				s = red(s)
			}
			fmt.Printf("%-20s %-18s %-30s %s\n", r.Node, r.IP, r.Image, s)
		}
		return nil
	},
}

var ipCmd = &cobra.Command{
	Use:   "ip <node>",
	Short: "Print a node's static IP",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, a []string) error {
		lab, dk, err := loadAndDocker()
		if err != nil {
			return err
		}
		ip, err := dk.IP(context.Background(), lab.Name, a[0])
		if err != nil {
			return err
		}
		fmt.Println(ip)
		return nil
	},
}

var keygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "(Re)generate the lab SSH keypair",
	RunE: func(*cobra.Command, []string) error {
		lab, err := Load(cfgFile)
		if err != nil {
			return err
		}
		priv, pub, err := RegenerateKeypair(lab.VabbeDir())
		if err != nil {
			return err
		}
		fmt.Printf("regenerated:\n  %s\n  %s\n", priv, pub)
		return nil
	},
}

var shellPref string

// sshInto runs an interactive shell in the node. The shell is `--shell` if set,
// else bash when present (falling back to sh).
func sshInto(lab *Lab, dk *Docker, node string) error {
	ctx := context.Background()
	shell := shellPref
	if shell == "" {
		if n := lab.nodeByName(node); n != nil && isVMRuntime(n.Runtime) {
			// Can't docker-exec a VM node to probe; vabbe-node images ship bash.
			shell = "bash"
		} else {
			shell = dk.pickShell(ctx, lab.Name, node)
		}
	}
	return nodeExec(lab, dk, node, []string{shell}, true)
}

// nodeExec runs cmd in a node. A VM-runtime node (Kata etc.) runs systemd but owns
// its cgroup, so docker exec can't attach a process to it (EBUSY) — for those we
// shell out to real ssh over the lab keypair. Shared-kernel (runc) nodes keep
// using docker exec, which works before sshd/the network has even settled.
func nodeExec(lab *Lab, dk *Docker, node string, cmd []string, tty bool) error {
	if n := lab.nodeByName(node); n != nil && isVMRuntime(n.Runtime) {
		return sshExec(lab, dk, node, cmd, tty)
	}
	return dk.Exec(context.Background(), lab.Name, node, cmd, tty)
}

// sshExec runs cmd in a node via the system `ssh` client, using the lab keypair
// and the node's live IP. Used for VM-runtime nodes (see nodeExec).
func sshExec(lab *Lab, dk *Docker, node string, cmd []string, tty bool) error {
	ip, err := dk.IP(context.Background(), lab.Name, node)
	if err != nil {
		return err
	}
	args := []string{
		"-i", filepath.Join(lab.VabbeDir(), "id_ed25519"),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}
	if tty {
		args = append(args, "-t")
	}
	args = append(args, "root@"+ip)
	args = append(args, cmd...)
	c := exec.Command("ssh", args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

var sshCmd = &cobra.Command{
	Use:   "ssh <node>",
	Short: "Interactive shell INTO a node via docker exec",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, a []string) error {
		lab, dk, err := loadAndDocker()
		if err != nil {
			return err
		}
		return sshInto(lab, dk, a[0])
	},
}

var shellCmd = &cobra.Command{
	Use:   "shell [node]",
	Short: "Drop into the runner (or a named node)",
	RunE: func(_ *cobra.Command, a []string) error {
		lab, dk, err := loadAndDocker()
		if err != nil {
			return err
		}
		node := ""
		if len(a) > 0 {
			node = a[0]
		} else {
			r := lab.Runner()
			if r == nil {
				return fmt.Errorf("no runner flagged; set `runner: true` on a node or pass a node name")
			}
			node = r.Name
		}
		fmt.Printf("→ %s\n", node)
		return sshInto(lab, dk, node)
	},
}

var execCmd = &cobra.Command{
	Use:   "exec <node> -- <cmd>...",
	Short: "Run a command in a node via docker exec",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, a []string) error {
		lab, dk, err := loadAndDocker()
		if err != nil {
			return err
		}
		rest := a[1:]
		if len(rest) > 0 && rest[0] == "--" {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			rest = []string{"sh"}
		}
		return nodeExec(lab, dk, a[0], rest, isTerminal())
	},
}

func isTerminal() bool {
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		return true
	}
	return false
}

var logsCmd = &cobra.Command{
	Use:   "logs <node>",
	Short: "Container logs (systemd journal is inside)",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, a []string) error {
		lab, dk, err := loadAndDocker()
		if err != nil {
			return err
		}
		rc, err := dk.Logs(context.Background(), lab.Name, a[0])
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		_, err = stdcopy.StdCopy(os.Stdout, os.Stderr, rc)
		return err
	},
}

func init() {
	rootCmd.AddCommand(
		upCmd, downCmd, lsCmd, ipCmd, keygenCmd,
		sshCmd, shellCmd, execCmd, logsCmd,
	)
	downCmd.Flags().BoolVar(&downKeepNet, "keep-net", false, "keep the lab network after removing containers")
	downCmd.Flags().BoolVar(&downAll, "all", false, "remove ALL vabbe-managed labs on the daemon (ignores -f config)")
	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "output as JSON")
	shellCmd.Flags().StringVar(&shellPref, "shell", "", "shell to use (default: bash if present, else sh)")
	sshCmd.Flags().StringVar(&shellPref, "shell", "", "shell to use (default: bash if present, else sh)")
	upCmd.Flags().BoolVar(&upRecreate, "recreate", false, "recreate nodes whose config has drifted (image/env/mounts/ports/privileged/runtime/cpus/memory/entrypoint/cmd)")
	upCmd.Flags().BoolVar(&upWait, "wait", false, "wait until each node is reachable (sshd up) before returning")
	upCmd.Flags().DurationVar(&upTimeout, "timeout", 90*time.Second, "max time to wait when --wait is set")
}
