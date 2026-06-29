package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

const DefaultImage = "ghcr.io/nutellinoit/vabbe-node:24.04"

// defaultDNS is written into a node's resolv.conf when neither the node nor the
// lab sets `dns:`. Docker's embedded 127.0.0.11 only works in the node's root
// netns, so Kubernetes pods (CoreDNS) can't use it — a real upstream is needed.
var defaultDNS = []string{"1.1.1.1", "1.0.0.1"}

// NodeDNS resolves the upstream resolvers for a node: node `dns:` wins, then the
// lab `defaults.dns:`, then the built-in public default.
func (l *Lab) NodeDNS(n *Node) []string {
	if len(n.DNS) > 0 {
		return n.DNS
	}
	if len(l.Defaults.DNS) > 0 {
		return l.Defaults.DNS
	}
	return defaultDNS
}

type Lab struct {
	Name     string   `yaml:"name"`
	Network  Network  `yaml:"network"`
	Defaults Defaults `yaml:"defaults"`
	Nodes    []Node   `yaml:"nodes"`
	dir      string
}

type Network struct {
	Subnet string `yaml:"subnet"`
}

type Defaults struct {
	Image      *string  `yaml:"image"`
	Privileged *bool    `yaml:"privileged"`
	DNS        []string `yaml:"dns"`
	Runtime    *string  `yaml:"runtime"`
}

type Node struct {
	Name       string            `yaml:"name"`
	IP         string            `yaml:"ip"`
	Image      string            `yaml:"image"`
	Entrypoint []string          `yaml:"entrypoint"`
	Cmd        []string          `yaml:"cmd"`
	Mounts     []string          `yaml:"mounts"`
	Env        map[string]string `yaml:"env"`
	Caps       []string          `yaml:"caps"`
	Ports      []string          `yaml:"ports"`
	Hostname   string            `yaml:"hostname"`
	Runner     bool              `yaml:"runner"`
	Privileged *bool             `yaml:"privileged"`
	DNS        []string          `yaml:"dns"`
	Runtime    string            `yaml:"runtime"`
}

func Load(path string) (*Lab, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = []byte(expandEnv(string(data)))
	var lab Lab
	if err := yaml.Unmarshal(data, &lab); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if lab.Name == "" {
		return nil, fmt.Errorf("lab.name is required")
	}
	if len(lab.Nodes) == 0 {
		return nil, fmt.Errorf("at least one node is required")
	}
	// Resolve the lab dir to an absolute path. Relative bind sources (e.g.
	// "./:/workspace") are joined against this; Docker rejects a relative bind
	// source as a (too-short) volume name, so the base must be absolute.
	lab.dir = absPath(filepath.Dir(path))
	lab.applyDefaults()
	if err := lab.validate(); err != nil {
		return nil, err
	}
	return &lab, nil
}

func (l *Lab) Dir() string { return l.dir }

func (l *Lab) VabbeDir() string { return filepath.Join(l.dir, ".vabbe", l.Name) }

// loadAndDocker is the standard preamble of every verb: load the YAML and
// connect to Docker. Trims the per-command boilerplate.
func loadAndDocker() (*Lab, *Docker, error) {
	lab, err := Load(cfgFile)
	if err != nil {
		return nil, nil, err
	}
	dk, err := NewDocker()
	if err != nil {
		return nil, nil, err
	}
	return lab, dk, nil
}

func (l *Lab) applyDefaults() {
	for i := range l.Nodes {
		n := &l.Nodes[i]
		if n.Image == "" && l.Defaults.Image != nil {
			n.Image = *l.Defaults.Image
		}
		if n.Image == "" {
			n.Image = DefaultImage
		}
		if n.Hostname == "" {
			n.Hostname = n.Name
		}
		if n.Privileged == nil {
			if n.Runner {
				p := false
				n.Privileged = &p
			} else if l.Defaults.Privileged != nil {
				p := *l.Defaults.Privileged
				n.Privileged = &p
			} else {
				t := true
				n.Privileged = &t
			}
		}
		if n.Runtime == "" && l.Defaults.Runtime != nil {
			n.Runtime = *l.Defaults.Runtime
		}
		for k, v := range n.Env {
			n.Env[k] = expandEnv(v)
		}
	}
}

func (l *Lab) validate() error {
	// subnet and per-node ip are optional: omit them to let Docker auto-allocate
	// (handy for ephemeral CI labs — no subnet collisions across parallel runs).
	var cidr *net.IPNet
	if l.Network.Subnet != "" {
		_, c, err := parseCIDR(l.Network.Subnet)
		if err != nil {
			return fmt.Errorf("network.subnet %q: %w", l.Network.Subnet, err)
		}
		cidr = c
	}
	seenIP := map[string]string{}
	seenName := map[string]bool{}
	for _, n := range l.Nodes {
		if n.Name == "" {
			return fmt.Errorf("node missing name")
		}
		if seenName[n.Name] {
			return fmt.Errorf("duplicate node name %q", n.Name)
		}
		seenName[n.Name] = true
		if n.IP == "" {
			continue // auto-assigned by Docker
		}
		if cidr == nil {
			return fmt.Errorf("node %q: ip %s set but network.subnet is not — omit the ip for an auto-assigned address, or set the subnet", n.Name, n.IP)
		}
		ip := parseIP(n.IP)
		if ip == nil {
			return fmt.Errorf("node %q: invalid ip %q", n.Name, n.IP)
		}
		if !cidr.Contains(ip) {
			return fmt.Errorf("node %q: ip %s not in subnet %s", n.Name, n.IP, cidr.String())
		}
		if prev, ok := seenIP[n.IP]; ok {
			return fmt.Errorf("node %q: ip %s already used by %q", n.Name, n.IP, prev)
		}
		seenIP[n.IP] = n.Name
	}
	return l.validatePorts()
}

// validatePorts checks every node's `ports:` entries parse, are in range, use a
// known protocol, and that no two nodes claim the same host endpoint.
func (l *Lab) validatePorts() error {
	seenHost := map[string]string{} // "ip:host/proto" -> node
	for _, n := range l.Nodes {
		for _, p := range n.Ports {
			s, ok := parsePort(p)
			if !ok {
				return fmt.Errorf("node %q: invalid port %q (want [ip:]host:node[/proto])", n.Name, p)
			}
			if err := validatePortNum(s.host); err != nil {
				return fmt.Errorf("node %q port %q: host %w", n.Name, p, err)
			}
			if err := validatePortNum(s.node); err != nil {
				return fmt.Errorf("node %q port %q: node %w", n.Name, p, err)
			}
			switch s.proto {
			case "tcp", "udp", "sctp":
			default:
				return fmt.Errorf("node %q port %q: protocol must be tcp, udp or sctp", n.Name, p)
			}
			ip := s.ip
			if ip == "" {
				ip = "0.0.0.0"
			}
			key := ip + ":" + s.host + "/" + s.proto
			if prev, ok := seenHost[key]; ok {
				return fmt.Errorf("node %q: host port %s already published by %q", n.Name, key, prev)
			}
			seenHost[key] = n.Name
		}
	}
	return nil
}

func validatePortNum(p string) error {
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port %q out of range 1-65535", p)
	}
	return nil
}

func (l *Lab) Runner() *Node {
	for i := range l.Nodes {
		if l.Nodes[i].Runner {
			return &l.Nodes[i]
		}
	}
	return nil
}

func parseCIDR(s string) (net.IP, *net.IPNet, error) {
	return net.ParseCIDR(s)
}

func parseIP(s string) net.IP {
	return net.ParseIP(s)
}

var envRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

func expandEnv(s string) string {
	return envRe.ReplaceAllStringFunc(s, func(m string) string {
		var name string
		if len(m) > 2 && m[1] == '{' {
			name = m[2 : len(m)-1]
		} else {
			name = m[1:]
		}
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return m
	})
}
