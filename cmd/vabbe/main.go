package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	cfgFile string

	//go:embed image/Dockerfile
	adminDockerfile []byte

	//go:embed image/boot-id-token.service
	adminBootIDUnit []byte
)

var version = "0.0.0-dev"

var rootCmd = &cobra.Command{
	Use:          "vabbe",
	Short:        "Docker containers that cosplay as throwaway VMs",
	Long:         "vabbe spins up Docker containers that act like VMs (systemd + sshd, static IPs on a network you define) for testing installers.",
	SilenceUsage: true,
	Version:      version,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "file", "f", "vabbe.yaml", "lab config file")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		die(err)
	}
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "vabbe:", err.Error())
		os.Exit(1)
	}
}
