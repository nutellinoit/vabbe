package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

const DefaultImage = "ghcr.io/nutellinoit/vabbe-node:24.04"

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
	Image      *string `yaml:"image"`
	Privileged *bool   `yaml:"privileged"`
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
	if lab.Network.Subnet == "" {
		return nil, fmt.Errorf("network.subnet is required")
	}
	if len(lab.Nodes) == 0 {
		return nil, fmt.Errorf("at least one node is required")
	}
	lab.dir = filepath.Dir(path)
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
		for k, v := range n.Env {
			n.Env[k] = expandEnv(v)
		}
	}
}

func (l *Lab) validate() error {
	_, net, err := parseCIDR(l.Network.Subnet)
	if err != nil {
		return fmt.Errorf("network.subnet %q: %w", l.Network.Subnet, err)
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
		ip := parseIP(n.IP)
		if ip == nil {
			return fmt.Errorf("node %q: invalid ip %q", n.Name, n.IP)
		}
		if !net.Contains(ip) {
			return fmt.Errorf("node %q: ip %s not in subnet %s", n.Name, n.IP, net.String())
		}
		if prev, ok := seenIP[n.IP]; ok {
			return fmt.Errorf("node %q: ip %s already used by %q", n.Name, n.IP, prev)
		}
		seenIP[n.IP] = n.Name
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
