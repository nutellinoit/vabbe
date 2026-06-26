package main

import (
	"context"
	"fmt"
	"os"
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
		fmt.Printf("lab %q on subnet %s\n", lab.Name, lab.Network.Subnet)
		for i := range lab.Nodes {
			n := &lab.Nodes[i]
			warns, recreated, err := dk.EnsureNode(ctx, lab, n, pub, upRecreate)
			if err != nil {
				return err
			}
			fmt.Printf("  ✓ %s %s (%s)\n", n.Name, n.IP, n.Image)
			switch {
			case recreated:
				fmt.Printf("    ~ recreated (config changed: %s)\n", strings.Join(warns, ", "))
			case len(warns) > 0:
				fmt.Printf("    ! config changed (%s): run `vabbe down`/`up` or `up --recreate` to apply\n", strings.Join(warns, ", "))
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
		fmt.Printf("  ✓ %s ready\n", n.Name)
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
		fmt.Printf("%-20s %-18s %-30s %s\n", "NODE", "IP", "IMAGE", "STATUS")
		for _, c := range cs {
			name := c.Names[0][1:]
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
			fmt.Printf("%-20s %-18s %-30s %s\n", name, ip, c.Image, status)
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
func sshInto(dk *Docker, labName, node string) error {
	ctx := context.Background()
	shell := shellPref
	if shell == "" {
		shell = dk.pickShell(ctx, labName, node)
	}
	return dk.Exec(ctx, labName, node, []string{shell}, true)
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
		return sshInto(dk, lab.Name, a[0])
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
		return sshInto(dk, lab.Name, node)
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
		return dk.Exec(context.Background(), lab.Name, a[0], rest, isTerminal())
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
	shellCmd.Flags().StringVar(&shellPref, "shell", "", "shell to use (default: bash if present, else sh)")
	sshCmd.Flags().StringVar(&shellPref, "shell", "", "shell to use (default: bash if present, else sh)")
	upCmd.Flags().BoolVar(&upRecreate, "recreate", false, "recreate nodes whose config has drifted (image/env/mounts/ports/privileged/entrypoint/cmd)")
	upCmd.Flags().BoolVar(&upWait, "wait", false, "wait until each node is reachable (sshd up) before returning")
	upCmd.Flags().DurationVar(&upTimeout, "timeout", 90*time.Second, "max time to wait when --wait is set")
}
