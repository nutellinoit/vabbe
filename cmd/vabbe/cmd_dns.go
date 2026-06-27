package main

import (
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// defaultDNSZone is trusted without a live lookup (it works and keeps `dns`
// usable offline). Any other zone passed via --common-dns-zone must prove it
// actually resolves to the node IPs before we print it.
const defaultDNSZone = "nip.io"

// commonDNSZone is a wildcard-DNS zone that resolves an embedded IP, like
// nip.io / sslip.io: "<name>-1-2-3-4.<zone>" resolves to 1.2.3.4. This gives
// each node a real, stable hostname with no registration, usable wherever the
// node IP is routable (inside the lab, from the runner, or from a Linux host).
var commonDNSZone string

var dnsCmd = &cobra.Command{
	Use:   "dns [node]",
	Short: "Print wildcard-DNS hostnames for nodes (default zone nip.io)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		lab, err := Load(cfgFile)
		if err != nil {
			return err
		}
		addr := nodeAddrResolver(lab)
		type entry struct{ name, ip, host string }
		var entries []entry
		for i := range lab.Nodes {
			n := &lab.Nodes[i]
			if len(args) == 1 && n.Name != args[0] {
				continue
			}
			ip, err := addr(n)
			if err != nil {
				return fmt.Errorf("node %q address: %w (is the lab up?)", n.Name, err)
			}
			entries = append(entries, entry{n.Name, ip, zoneHost(n.Name, ip, commonDNSZone)})
		}
		// A custom zone is only useful if it resolves; fail loudly rather than
		// printing hostnames that point nowhere.
		if commonDNSZone != defaultDNSZone {
			for _, e := range entries {
				if err := verifyResolves(e.host, e.ip); err != nil {
					return err
				}
			}
		}
		for _, e := range entries {
			fmt.Printf("%s\t%s\n", e.name, e.host)
		}
		return nil
	},
}

// zoneHost builds "<name>-<ip-with-dashes>.<zone>", e.g. zoneHost("cp0",
// "10.202.1.3", "nip.io") == "cp0-10-202-1-3.nip.io".
func zoneHost(name, ip, zone string) string {
	return fmt.Sprintf("%s-%s.%s", name, strings.ReplaceAll(ip, ".", "-"), zone)
}

// verifyResolves checks that host resolves and includes wantIP, so a custom DNS
// zone is rejected unless it behaves like the wildcard-IP resolver it claims.
func verifyResolves(host, wantIP string) error {
	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("dns zone check: %s does not resolve: %w", host, err)
	}
	if !slices.Contains(ips, wantIP) {
		return fmt.Errorf("dns zone check: %s resolves to %v, not %s", host, ips, wantIP)
	}
	return nil
}

func init() {
	dnsCmd.Flags().StringVar(&commonDNSZone, "common-dns-zone", defaultDNSZone,
		"wildcard-DNS zone that resolves an embedded IP (e.g. nip.io, sslip.io); a non-default zone must resolve to the node IPs")
	rootCmd.AddCommand(dnsCmd)
}
