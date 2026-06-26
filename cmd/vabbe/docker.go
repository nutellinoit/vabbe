package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

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
	"golang.org/x/term"
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
	// If a network with this name already exists, only reuse it when it is ours
	// (carries our label) and its subnet still matches the config — otherwise we
	// would silently hijack or contradict someone else's network.
	if nw, err := d.c.NetworkInspect(ctx, lab.Name, network.InspectOptions{}); err == nil {
		if nw.Labels[labelLab] != lab.Name {
			return fmt.Errorf("network %q already exists and is not managed by vabbe (missing %s label); rename the lab or remove that network", lab.Name, labelLab)
		}
		if cur := networkSubnet(nw); cur != "" && cur != lab.Network.Subnet {
			return fmt.Errorf("network %q exists with subnet %s but config wants %s; run `vabbe down` first", lab.Name, cur, lab.Network.Subnet)
		}
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspect network %s: %w", lab.Name, err)
	}
	_, err := d.c.NetworkCreate(ctx, lab.Name, network.CreateOptions{
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

// networkSubnet returns the first configured IPv4 subnet of a network, or "".
func networkSubnet(nw network.Inspect) string {
	for _, c := range nw.IPAM.Config {
		if c.Subnet != "" {
			return c.Subnet
		}
	}
	return ""
}

// EnsureNode creates the node if missing, otherwise reconciles it. The returned
// strings are config-drift warnings: fields whose desired value differs from the
// running container. By default we only warn (not recreate) because labs are
// throwaway and `down`/`up` is the cheap, unambiguous way to apply such changes;
// with recreate=true a drifted node is removed and recreated instead, and the
// returned bool reports whether that happened.
func (d *Docker) EnsureNode(ctx context.Context, lab *Lab, node *Node, pubKeyPath string, recreate bool) ([]string, bool, error) {
	existing, err := d.findContainer(ctx, lab.Name, node.Name)
	if err != nil {
		return nil, false, err
	}
	if existing == nil {
		return nil, false, d.createNode(ctx, lab, node, pubKeyPath)
	}
	if recreate {
		if drift := d.drift(ctx, lab, node, existing.ID); len(drift) > 0 {
			if err := d.c.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
				return nil, false, fmt.Errorf("recreate %s: %w", node.Name, err)
			}
			return drift, true, d.createNode(ctx, lab, node, pubKeyPath)
		}
	}
	w, err := d.reconcile(ctx, lab, node, existing)
	return w, false, err
}

// Reachable reports whether a node is ready to be used. Server nodes are ready
// once their sshd unit is active; runner nodes have no sshd (they are SSH
// clients), so they count as ready once the container is running.
func (d *Docker) Reachable(ctx context.Context, lab *Lab, node *Node) bool {
	c, err := d.findContainer(ctx, lab.Name, node.Name)
	if err != nil || c == nil {
		return false
	}
	if node.Runner {
		return c.State == "running"
	}
	// The sshd unit is "ssh" on Debian/Ubuntu and "sshd" on RHEL/Rocky — accept
	// either so readiness works across node bases.
	code, err := d.execCode(ctx, c.ID, []string{"sh", "-c",
		"systemctl is-active ssh >/dev/null 2>&1 || systemctl is-active sshd >/dev/null 2>&1"})
	return err == nil && code == 0
}

// execCode runs a command in a container and returns its exit code, discarding
// output. Used for readiness probes where streaming would be noise.
func (d *Docker) execCode(ctx context.Context, containerID string, cmd []string) (int, error) {
	id, err := d.c.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return -1, err
	}
	attach, err := d.c.ContainerExecAttach(ctx, id.ID, container.ExecAttachOptions{})
	if err != nil {
		return -1, err
	}
	_, _ = io.Copy(io.Discard, attach.Reader)
	attach.Close()
	insp, err := d.c.ContainerExecInspect(ctx, id.ID)
	if err != nil {
		return -1, err
	}
	return insp.ExitCode, nil
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
	env := make([]string, 0, len(node.Env)+1)
	for k, v := range node.Env {
		env = append(env, k+"="+v)
	}
	// vabbe-resolv.service reads VABBE_DNS at boot to replace Docker's embedded
	// 127.0.0.11 resolver with a pod-reachable upstream.
	if dns := lab.NodeDNS(node); len(dns) > 0 {
		env = append(env, "VABBE_DNS="+strings.Join(dns, " "))
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
	cs, err := d.ListByLab(ctx, lab.Name)
	if err != nil {
		return nil, err
	}
	return d.removeAll(ctx, cs, fmt.Sprintf("%s=%s", labelLab, lab.Name), keepNet)
}

// DownAll removes every vabbe-managed container and network on the daemon,
// identified by the presence of the `vabbe.lab` label — so it can clean up
// orphaned labs even when their `vabbe.yaml` is gone.
func (d *Docker) DownAll(ctx context.Context, keepNet bool) ([]string, error) {
	cs, err := d.c.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelLab)),
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Names[0] < cs[j].Names[0] })
	return d.removeAll(ctx, cs, labelLab, keepNet)
}

// removeAll force-removes the given containers (with their anonymous volumes) and,
// unless keepNet, the networks matching labelArg (`key` or `key=value`).
func (d *Docker) removeAll(ctx context.Context, cs []container.Summary, labelArg string, keepNet bool) ([]string, error) {
	removed := []string{}
	for _, c := range cs {
		name := strings.TrimPrefix(c.Names[0], "/")
		if err := d.c.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
			return removed, fmt.Errorf("remove %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	if !keepNet {
		nws, err := d.c.NetworkList(ctx, network.ListOptions{
			Filters: filters.NewArgs(filters.Arg("label", labelArg)),
		})
		if err != nil {
			return removed, fmt.Errorf("list networks: %w", err)
		}
		for _, nw := range nws {
			if err := d.c.NetworkRemove(ctx, nw.ID); err != nil {
				return removed, fmt.Errorf("remove network %s: %w", nw.Name, err)
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
		if restore := d.setupRawTTY(ctx, id.ID); restore != nil {
			defer restore()
		}
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

// setupRawTTY puts the local terminal in raw mode and forwards its size (and
// later SIGWINCH resizes) to the exec session, so interactive shells get working
// arrow keys and line editing. Returns a restore func, or nil if stdin isn't a
// terminal.
func (d *Docker) setupRawTTY(ctx context.Context, execID string) func() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil
	}
	resize := func() {
		if w, h, err := term.GetSize(fd); err == nil {
			_ = d.c.ContainerExecResize(ctx, execID, container.ResizeOptions{Width: uint(w), Height: uint(h)})
		}
	}
	resize()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			resize()
		}
	}()
	return func() {
		signal.Stop(winch)
		close(winch)
		_ = term.Restore(fd, state)
	}
}

// pickShell returns "bash" if the node has it, else "sh".
func (d *Docker) pickShell(ctx context.Context, labName, nodeName string) string {
	c, err := d.findContainer(ctx, labName, nodeName)
	if err != nil || c == nil {
		return "sh"
	}
	if code, err := d.execCode(ctx, c.ID, []string{"sh", "-c", "command -v bash >/dev/null 2>&1"}); err == nil && code == 0 {
		return "bash"
	}
	return "sh"
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

// contextFile is one entry (besides the Dockerfile) in the SDK build context tar.
type contextFile struct {
	name string
	data []byte
}

func (d *Docker) BuildImage(ctx context.Context, dockerfile []byte, extras []contextFile, tags []string, w io.Writer) error {
	ctxBytes, err := buildContext(dockerfile, extras)
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

func buildContext(dockerfile []byte, extras []contextFile) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	files := append([]contextFile{{"Dockerfile", dockerfile}}, extras...)
	for _, f := range files {
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

// portSpec is one parsed `ports:` entry in the Docker `-p` syntax
// `[ip:]host:node[/proto]` (bare `node` means host==node, tcp).
type portSpec struct {
	ip    string // listen address; "" = all interfaces
	host  string // host port
	node  string // container port
	proto string // tcp | udp | sctp
}

func (s portSpec) natPort() nat.Port { return nat.Port(s.node + "/" + s.proto) }

// parsePort parses `[ip:]host:node[/proto]`. Returns ok=false on a malformed
// shape; numeric/protocol validation is done by validatePortSpec.
func parsePort(p string) (portSpec, bool) {
	s := portSpec{proto: "tcp"}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		s.proto = strings.ToLower(p[i+1:])
		p = p[:i]
	}
	parts := strings.Split(p, ":")
	switch len(parts) {
	case 1:
		s.host, s.node = parts[0], parts[0]
	case 2:
		s.host, s.node = parts[0], parts[1]
	case 3:
		s.ip, s.host, s.node = parts[0], parts[1], parts[2]
	default:
		return portSpec{}, false
	}
	if s.host == "" || s.node == "" {
		return portSpec{}, false
	}
	return s, true
}

func parsePortBindings(ports []string) nat.PortMap {
	pm := nat.PortMap{}
	for _, p := range ports {
		s, ok := parsePort(p)
		if !ok {
			continue
		}
		pm[s.natPort()] = append(pm[s.natPort()], nat.PortBinding{HostIP: s.ip, HostPort: s.host})
	}
	return pm
}
