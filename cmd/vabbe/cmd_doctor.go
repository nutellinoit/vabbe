package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/docker/docker/api/types/system"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Environment sanity checks and remediation hints",
	RunE: func(*cobra.Command, []string) error {
		dk, err := NewDocker()
		if err != nil {
			return fmt.Errorf("docker not reachable: %w", err)
		}
		ctx := context.Background()
		ping, err := dk.Ping(ctx)
		if err != nil {
			return fmt.Errorf("docker daemon unreachable: %w", err)
		}
		fmt.Printf("%s Docker reachable (API %s, OS %s)\n", green("✓"), ping.APIVersion, ping.OSType)
		info, err := dk.Info(ctx)
		if err != nil {
			fmt.Println(yellow("⚠")+" Info() failed:", err)
		} else {
			fmt.Printf("  daemon: %s %s\n", info.OperatingSystem, info.Architecture)
			if isDockerDesktop(info) {
				fmt.Println(yellow("⚠") + " Docker Desktop: if kubeadm swap preflight fails run `vabbe host-prep`")
			} else if runtime.GOOS == "linux" {
				fmt.Println(blue("i") + " on Linux: run `vabbe host-prep` (or `sudo swapoff -a` + needed modprobes) if swap/modules errors occur")
			}
		}
		if _, err := os.Stat(cfgFile); err == nil {
			lab, lerr := Load(cfgFile)
			if lerr != nil {
				fmt.Printf("%s %s invalid: %s\n", red("✗"), cfgFile, lerr)
			} else {
				fmt.Printf("%s lab %s valid: %d nodes, subnet %s\n", green("✓"), bold(lab.Name), len(lab.Nodes), lab.Network.Subnet)
				for i := range lab.Nodes {
					n := &lab.Nodes[i]
					fmt.Printf("  - %s %s (%s)\n", n.Name, n.IP, n.Image)
				}
				if lab.Runner() == nil {
					fmt.Println(blue("i") + " no `runner: true` node set (needed by `vabbe shell`)")
				}
			}
		}
		return nil
	},
}

func isDockerDesktop(info system.Info) bool {
	o := info.OperatingSystem
	return strings.Contains(o, "Docker Desktop") || strings.Contains(o, "boot2docker") || strings.Contains(o, "linuxkit")
}

func init() { rootCmd.AddCommand(doctorCmd) }
