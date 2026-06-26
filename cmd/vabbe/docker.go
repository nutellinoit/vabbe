package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

const (
	labelLab  = "vabbe.lab"
	labelNode = "vabbe.node"
)

type Docker struct {
	c *client.Client
}

func NewDocker() (*Docker, error) {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Docker{c: c}, nil
}

func (d *Docker) Ping(ctx context.Context) (types.Ping, error)  { return d.c.Ping(ctx) }
func (d *Docker) Info(ctx context.Context) (system.Info, error) { return d.c.Info(ctx) }

func (d *Docker) EnsureNetwork(ctx context.Context, lab *Lab) error {
	found, err := d.findNetwork(ctx, lab.Name)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = d.c.NetworkCreate(ctx, lab.Name, network.CreateOptions{
		Driver: "bridge",
		IPAM: &network.IPAM{
			Driver: "default",
			Config: []network.IPAMConfig{{Subnet: lab.Network.Subnet}},
		},
		Labels: map[string]string{labelLab: lab.Name},
	})
	if err != nil {
		return fmt.Errorf("create network %s: %w", lab.Name, err)
	}
	return nil
}

func (d *Docker) findNetwork(ctx context.Context, name string) (bool, error) {
	nws, err := d.c.NetworkList(ctx, network.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", fmt.Sprintf("%s=%s", labelLab, name))),
	})
	if err != nil {
		return false, fmt.Errorf("list networks: %w", err)
	}
	return len(nws) > 0, nil
}

// EnsureNode creates the node if missing, otherwise reconciles it. The returned
// strings are config-drift warnings: fields whose desired value differs from the
// running container. We only warn (not recreate) because labs are throwaway and
// `down`/`up` is the cheap, unambiguous way to apply such changes.
func (d *Docker) EnsureNode(ctx context.Context, lab *Lab, node *Node, pubKeyPath string) ([]string, error) {
	existing, err := d.findContainer(ctx, lab.Name, node.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return d.reconcile(ctx, lab, node, existing)
	}
	return nil, d.createNode(ctx, lab, node, pubKeyPath)
}

func (d *Docker) createNode(ctx context.Context, lab *Lab, node *Node, pubKeyPath string) error {
	if err := d.ensureImage(ctx, node.Image); err != nil {
		return err
	}
	privileged := node.Privileged != nil && *node.Privileged
	binds := append([]string{}, node.Mounts...)
	// Inject the lab keypair so nodes can ssh //node↔node// while root login
	// stays key-only. The public key becomes authorized_keys; the private key
	// lets a node (or a runner) ssh its peers.
	pub := absPath(pubKeyPath)
	priv := absPath(filepath.Join(filepath.Dir(pubKeyPath), "id_ed25519"))
	binds = append(binds,
		fmt.Sprintf("%s:/root/.ssh/authorized_keys:ro", pub),
		fmt.Sprintf("%s:/root/.ssh/id_ed25519:ro", priv),
	)
	if !node.Runner {
		binds = append(binds, "/lib/modules:/lib/modules:ro")
	}
	// Resolve bind source paths to absolute; user mounts are relative to lab dir.
	for i, b := range binds {
		binds[i] = absBind(lab.Dir(), b)
	}
	tmpfs := map[string]string{"/run": "", "/run/lock": "", "/tmp": ""}
	env := make([]string, 0, len(node.Env))
	for k, v := range node.Env {
		env = append(env, k+"="+v)
	}
	hc := &container.HostConfig{
		Privileged:      privileged,
		Tmpfs:           tmpfs,
		Binds:           binds,
		PortBindings:    parsePortBindings(node.Ports),
		CapAdd:          capsIfNotPrivileged(node.Caps, privileged),
		RestartPolicy:   container.RestartPolicy{Name: "unless-stopped"},
		PublishAllPorts: false,
	}
	cc := &container.Config{
		Image:      node.Image,
		Hostname:   node.Hostname,
		Env:        env,
		Labels:     map[string]string{labelLab: lab.Name, labelNode: node.Name},
		StopSignal: "SIGRTMIN+3",
	}
	if len(node.Entrypoint) > 0 {
		cc.Entrypoint = strslice.StrSlice(node.Entrypoint)
	}
	if len(node.Cmd) > 0 {
		cc.Cmd = strslice.StrSlice(node.Cmd)
	}
	created, err := d.c.ContainerCreate(ctx, cc, hc,
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				lab.Name: {IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: node.IP}},
			},
		},
		nil,
		lab.Name+"."+node.Name,
	)
	if err != nil {
		return fmt.Errorf("create %s: %w", node.Name, err)
	}
	if err := d.c.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start %s: %w", node.Name, err)
	}
	return nil
}

func (d *Docker) ensureImage(ctx context.Context, ref string) error {
	if _, err := d.c.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspect image %s: %w", ref, err)
	}
	rc, err := d.c.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

func (d *Docker) reconcile(ctx context.Context, lab *Lab, node *Node, existing *container.Summary) ([]string, error) {
	if existing.State != "running" {
		if err := d.c.ContainerStart(ctx, existing.ID, container.StartOptions{}); err != nil {
			return nil, fmt.Errorf("restart %s: %w", node.Name, err)
		}
	}
	ep, ok := existing.NetworkSettings.Networks[lab.Name]
	if !ok || ep == nil {
		err := d.c.NetworkConnect(ctx, lab.Name, existing.ID, &network.EndpointSettings{
			IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: node.IP},
		})
		return nil, err
	}
	cur := ""
	if ep.IPAMConfig != nil {
		cur = strings.TrimSpace(ep.IPAMConfig.IPv4Address)
	}
	if cur == "" {
		cur = strings.TrimSpace(ep.IPAddress)
	}
	if cur != node.IP {
		_ = d.c.NetworkDisconnect(ctx, lab.Name, existing.ID, true)
		err := d.c.NetworkConnect(ctx, lab.Name, existing.ID, &network.EndpointSettings{
			IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: node.IP},
		})
		return nil, err
	}
	return d.drift(ctx, lab, node, existing.ID), nil
}

// drift reports config fields whose desired value differs from the running
// container. Best-effort: an inspect failure yields no warnings rather than
// blocking `up`. It compares only fields a user changes intentionally and that
// compare unambiguously, to avoid false positives that would cry wolf.
func (d *Docker) drift(ctx context.Context, lab *Lab, node *Node, id string) []string {
	info, err := d.c.ContainerInspect(ctx, id)
	if err != nil {
		return nil
	}
	var r []string
	if info.Config != nil {
		if info.Config.Image != node.Image {
			r = append(r, fmt.Sprintf("image (%s → %s)", info.Config.Image, node.Image))
		}
		if len(node.Entrypoint) > 0 && !strSliceEqual(info.Config.Entrypoint, node.Entrypoint) {
			r = append(r, "entrypoint")
		}
		if len(node.Cmd) > 0 && !strSliceEqual(info.Config.Cmd, node.Cmd) {
			r = append(r, "cmd")
		}
		for k, v := range node.Env {
			if !envHas(info.Config.Env, k, v) {
				r = append(r, "env "+k)
			}
		}
	}
	if info.HostConfig != nil {
		want := node.Privileged != nil && *node.Privileged
		if info.HostConfig.Privileged != want {
			r = append(r, fmt.Sprintf("privileged (%t → %t)", info.HostConfig.Privileged, want))
		}
		for _, m := range node.Mounts {
			if !sliceContains(info.HostConfig.Binds, absBind(lab.Dir(), m)) {
				r = append(r, "mount "+strings.SplitN(m, ":", 2)[0])
			}
		}
		for _, p := range node.Ports {
			if !portBound(info.HostConfig.PortBindings, p) {
				r = append(r, "port "+p)
			}
		}
	}
	sort.Strings(r)
	return r
}

func strSliceEqual(a strslice.StrSlice, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func envHas(env []string, k, v string) bool {
	want := k + "=" + v
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func sliceContains(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

// portBound reports whether the desired "host:container" (or bare "port") spec
// already has a matching binding in the running container.
func portBound(pm nat.PortMap, spec string) bool {
	for want := range parsePortBindings([]string{spec}) {
		if _, ok := pm[want]; !ok {
			return false
		}
	}
	return true
}

func (d *Docker) findContainer(ctx context.Context, labName, nodeName string) (*container.Summary, error) {
	cs, err := d.c.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("%s=%s", labelLab, labName)),
			filters.Arg("label", fmt.Sprintf("%s=%s", labelNode, nodeName)),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	if len(cs) == 0 {
		return nil, nil
	}
	return &cs[0], nil
}

func (d *Docker) ListByLab(ctx context.Context, labName string) ([]container.Summary, error) {
	cs, err := d.c.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", fmt.Sprintf("%s=%s", labelLab, labName))),
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Names[0] < cs[j].Names[0] })
	return cs, nil
}

func (d *Docker) Down(ctx context.Context, lab *Lab, keepNet bool) ([]string, error) {
	removed := []string{}
	cs, err := d.ListByLab(ctx, lab.Name)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		name := strings.TrimPrefix(c.Names[0], "/")
		if err := d.c.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return removed, fmt.Errorf("remove %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	if !keepNet {
		found, err := d.findNetwork(ctx, lab.Name)
		if err != nil {
			return removed, err
		}
		if found {
			if err := d.c.NetworkRemove(ctx, lab.Name); err != nil {
				return removed, fmt.Errorf("remove network %s: %w", lab.Name, err)
			}
		}
	}
	return removed, nil
}

func (d *Docker) IP(ctx context.Context, labName, nodeName string) (string, error) {
	c, err := d.findContainer(ctx, labName, nodeName)
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", fmt.Errorf("node %q not found", nodeName)
	}
	insp, err := d.c.ContainerInspect(ctx, c.ID)
	if err != nil {
		return "", err
	}
	ep, ok := insp.NetworkSettings.Networks[labName]
	if !ok || ep == nil {
		return "", fmt.Errorf("network %s not attached to %s", labName, nodeName)
	}
	if ep.IPAMConfig != nil && ep.IPAMConfig.IPv4Address != "" {
		return ep.IPAMConfig.IPv4Address, nil
	}
	return strings.TrimSpace(ep.IPAddress), nil
}

func (d *Docker) Logs(ctx context.Context, labName, nodeName string) (io.ReadCloser, error) {
	c, err := d.findContainer(ctx, labName, nodeName)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("node %q not found", nodeName)
	}
	return d.c.ContainerLogs(ctx, c.ID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: false,
	})
}

func (d *Docker) Exec(ctx context.Context, labName, nodeName string, cmd []string, tty bool) error {
	c, err := d.findContainer(ctx, labName, nodeName)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("node %q not found", nodeName)
	}
	eo := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  tty,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          tty,
	}
	if tty {
		eo.Env = []string{"TERM=xterm-256color"}
	}
	id, err := d.c.ContainerExecCreate(ctx, c.ID, eo)
	if err != nil {
		return fmt.Errorf("exec create: %w", err)
	}
	attach, err := d.c.ContainerExecAttach(ctx, id.ID, container.ExecAttachOptions{Tty: tty})
	if err != nil {
		return fmt.Errorf("exec attach: %w", err)
	}
	defer attach.Close()
	if tty {
		go func() { _, _ = io.Copy(attach.Conn, os.Stdin) }()
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(os.Stdout, attach.Reader)
			close(done)
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
		return d.execStatus(ctx, id.ID)
	}
	// Non-tty: demultiplex the docker stream (stdout+stderr frames).
	_, _ = stdcopy.StdCopy(os.Stdout, os.Stderr, attach.Reader)
	return d.execStatus(ctx, id.ID)
}

func (d *Docker) execStatus(ctx context.Context, id string) error {
	insp, err := d.c.ContainerExecInspect(ctx, id)
	if err != nil {
		return err
	}
	if insp.ExitCode != 0 {
		return fmt.Errorf("exec exited with code %d", insp.ExitCode)
	}
	return nil
}

func (d *Docker) BuildImage(ctx context.Context, dockerfile, bootIDUnit []byte, tags []string, w io.Writer) error {
	ctxBytes, err := buildContext(dockerfile, bootIDUnit)
	if err != nil {
		return err
	}
	resp, err := d.c.ImageBuild(ctx, ctxBytes, build.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       tags,
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("image build: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if msg.Error != "" {
			return fmt.Errorf("build: %s", msg.Error)
		}
		if w != nil {
			_, _ = fmt.Fprint(w, msg.Stream)
		}
	}
}

func buildContext(dockerfile, bootIDUnit []byte) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"Dockerfile", dockerfile},
		{"boot-id-token.service", bootIDUnit},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data))}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	_ = tw.Close()
	return &buf, nil
}

func capsIfNotPrivileged(caps []string, privileged bool) []string {
	if privileged {
		return nil
	}
	return caps
}

// absPath resolves a local path to absolute (best-effort).
func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// absBind makes the source side of a bind-path absolute if it is relative. A
// bind path looks like `host:container[:mode]`. Special absolute sources
// (`/lib/modules`) are left alone.
func absBind(base, b string) string {
	parts := strings.SplitN(b, ":", 3)
	if len(parts) < 2 {
		return b
	}
	if filepath.IsAbs(parts[0]) {
		return b
	}
	parts[0] = filepath.Clean(filepath.Join(base, parts[0]))
	return strings.Join(parts, ":")
}

func parsePortBindings(ports []string) nat.PortMap {
	pm := nat.PortMap{}
	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) == 1 {
			pm[nat.Port(parts[0]+"/tcp")] = []nat.PortBinding{{HostPort: parts[0]}}
		} else {
			pm[nat.Port(parts[1]+"/tcp")] = []nat.PortBinding{{HostPort: parts[0]}}
		}
	}
	return pm
}
