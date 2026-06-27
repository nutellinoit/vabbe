package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	inventoryKey    string
	inventoryRunner bool
)

var inventoryCmd = &cobra.Command{
	Use:   "inventory",
	Short: "Print an Ansible inventory for the lab's server nodes",
	Long: "Emit an INI Ansible inventory (group = lab name) of the non-runner " +
		"nodes, addressed by their static IP. By default the SSH key is the lab " +
		"key's absolute host path (run ansible from a Linux host); pass --runner " +
		"to target the in-network path /root/.ssh/id_ed25519 (run ansible from the " +
		"runner, the macOS-friendly way).",
	RunE: func(_ *cobra.Command, _ []string) error {
		lab, err := Load(cfgFile)
		if err != nil {
			return err
		}
		key := inventoryKey
		if key == "" {
			if inventoryRunner {
				key = "/root/.ssh/id_ed25519"
			} else {
				key = absPath(filepath.Join(lab.VabbeDir(), "id_ed25519"))
			}
		}
		addr := nodeAddrResolver(lab)
		var b strings.Builder
		fmt.Fprintf(&b, "[%s]\n", lab.Name)
		for i := range lab.Nodes {
			n := &lab.Nodes[i]
			if n.Runner {
				continue // runners are the ansible controller, not a target
			}
			ip, err := addr(n)
			if err != nil {
				return fmt.Errorf("node %q address: %w (is the lab up?)", n.Name, err)
			}
			fmt.Fprintf(&b, "%s ansible_host=%s\n", n.Name, ip)
		}
		fmt.Fprintf(&b, "\n[%s:vars]\n", lab.Name)
		fmt.Fprintf(&b, "ansible_user=root\n")
		fmt.Fprintf(&b, "ansible_ssh_private_key_file=%s\n", key)
		fmt.Fprintf(&b, "ansible_ssh_common_args=-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null\n")
		fmt.Print(b.String())
		return nil
	},
}

func init() {
	inventoryCmd.Flags().StringVar(&inventoryKey, "key", "", "ansible_ssh_private_key_file (default: lab key host path)")
	inventoryCmd.Flags().BoolVar(&inventoryRunner, "runner", false, "emit for running ansible FROM the runner (key at /root/.ssh/id_ed25519)")
	rootCmd.AddCommand(inventoryCmd)
}
