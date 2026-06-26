package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/system"
	"github.com/spf13/cobra"
)

var hostPrepDry bool

var hostPrepCmd = &cobra.Command{
	Use:   "host-prep",
	Short: "Disable swap + ensure kernel modules on the host/VM (Linux or Docker Desktop)",
	RunE: func(cmd *cobra.Command, args []string) error {
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
			return prepDockerDesktop(ctx, dk)
		}
		if runtime.GOOS != "linux" {
			return fmt.Errorf("host-prep supports Linux hosts and Docker Desktop (got %q)",
				info.OperatingSystem)
		}
		return prepLinux(info)
	},
}

const nsenter1Image = "justincormack/nsenter1"

const nsenter1Cmd = "swapoff -a && modprobe ip_vs ip_vs_rr ip_vs_wrr ip_vs_sh nf_conntrack br_netfilter overlay 2>/dev/null || true"

// prepDockerDesktop runs a short-lived privileged container joined to the
// Docker Desktop VM's PID 1 namespace so it can swapoff/modprobe the VM kernel.
func prepDockerDesktop(ctx context.Context, dk *Docker) error {
	fmt.Printf("Docker Desktop detected. Running a privileged helper to enter the VM PID 1 namespace:\n")
	fmt.Printf("  image: %s\n", nsenter1Image)
	fmt.Printf("  cmd:   %s\n", nsenter1Cmd)
	if hostPrepDry {
		fmt.Println("(dry-run; not executed)")
		return nil
	}
	cli := dk.c
	if err := dk.ensureImage(ctx, nsenter1Image); err != nil {
		return err
	}
	created, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:        nsenter1Image,
			Cmd:          []string{"sh", "-c", nsenter1Cmd},
			AttachStdout: true,
			AttachStderr: true,
		},
		&container.HostConfig{
			Privileged: true,
			PidMode:    "host",
			AutoRemove: true,
		},
		nil, nil, "vabbe-host-prep")
	if err != nil {
		return fmt.Errorf("create helper: %w", err)
	}
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start helper: %w", err)
	}
	logs, err := cli.ContainerLogs(ctx, created.ID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true,
	})
	if err == nil {
		_, _ = io.Copy(os.Stdout, logs)
		_ = logs.Close()
	}
	statusC, errC := cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case status := <-statusC:
		if status.StatusCode != 0 {
			return fmt.Errorf("helper exited with code %d", status.StatusCode)
		}
	case err := <-errC:
		return err
	}
	fmt.Println("✓ host-prep done on Docker Desktop VM.")
	return nil
}

// prepLinux shells out to sudo — host-prep is the explicit exception to the
// "Engine API only" rule: it must touch the real host kernel.
func prepLinux(info system.Info) error {
	fmt.Println("Linux host detected. Running (requires sudo):")
	for _, c := range [][]string{
		{"sudo", "swapoff", "-a"},
		{"sudo", "modprobe", "ip_vs"},
		{"sudo", "modprobe", "ip_vs_rr"},
		{"sudo", "modprobe", "ip_vs_wrr"},
		{"sudo", "modprobe", "ip_vs_sh"},
		{"sudo", "modprobe", "nf_conntrack"},
		{"sudo", "modprobe", "br_netfilter"},
		{"sudo", "modprobe", "overlay"},
	} {
		fmt.Printf("  %s\n", strings.Join(c, " "))
		if hostPrepDry {
			continue
		}
		c := exec.Command(c[0], c[1:]...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Printf("    (failed, continuing) %s\n", err)
		}
	}
	fmt.Println("✓ host-prep done on Linux host.")
	return nil
}

func init() {
	rootCmd.AddCommand(hostPrepCmd)
	hostPrepCmd.Flags().BoolVar(&hostPrepDry, "dry-run", false, "print what would run, do not execute")
}
