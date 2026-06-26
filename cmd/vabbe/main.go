package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	cfgFile string

	// imageFS holds the per-base node Dockerfiles (image/<base>/Dockerfile) and
	// the shared boot-id-token.service.
	//go:embed image
	imageFS embed.FS
)

// imageBases lists the node image flavors we can build, in stable order.
var imageBases = []string{"ubuntu", "rocky"}

// baseDockerfile returns the embedded Dockerfile for a node base (e.g. "ubuntu").
func baseDockerfile(base string) ([]byte, error) {
	return imageFS.ReadFile("image/" + base + "/Dockerfile")
}

// bootIDUnit returns the shared boot-id-token systemd unit.
func bootIDUnit() ([]byte, error) {
	return imageFS.ReadFile("image/boot-id-token.service")
}

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
