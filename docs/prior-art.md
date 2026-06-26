# Prior art — what we borrowed, where we diverge

`vabbe` builds the 5% of `containerlab` we need, using patterns proven by
`kind`. We don't fork either; we read source, then write minimal Go.

## kind (`sigs.k8s.io/kind`)

- `pkg/cluster/internal/providers/docker/network.go` — how to create **and
  re-use** a single named bridge network with a fixed subnet, avoiding clashes.
  Our `EnsureNetwork` does this for exactly one network per lab.
- `pkg/cluster/internal/providers/docker/` node `HostConfig` — the privileged
  + tmpfs + `StopSignal = SIGRTMIN+3` + restart-policy recipe that lets systemd
  boot cleanly as PID 1 in a container. Our `createNode` mirrors it.
- `images/base/` — the node image itself: systemd-as-PID1 entrypoint, what to
  mask, cgroup v2 assumptions. Our `cmd/vabbe/image/Dockerfile` (§8 of the spec) is a
  stripped-down version.

## containerlab (`srl-labs/containerlab`)

- `runtime/docker/` — Docker SDK usage: `IPAMConfig.IPv4Address` for static
  IPv4, `NetworkingConfig` at create time, `NetworkConnect` with IPAM for
  reattach. Same shape as our `docker.go`.
- The **label scheme** (`containerlab=<labName>`, `containerlab-node=<name>`)
  used to find/destroy lab objects. We use `vabbe.lab` and `vabbe.node` for
  the same reason: **labels-as-state**, no state file. `down`/`ls` query by
  label; `up` reconciles by label.
- `nodes/linux/` — the generic `linux` node is effectively what we run for VM
  nodes, minus the netns/veth wiring (we don't do point-to-point links).

## Where we deliberately diverge

- **No links / no topologies.** One flat user-defined bridge per lab.
  containerlab's link model and `kind`'s multi-network model are out — YAGNI.
- **No network-OS images, no vrnetlab.** Generic Linux only.
- **No `netns` surgery, `iproute2`, or `/var/run/netns`.** This is the whole
  reason `vabbe` is cross-platform: we only use the Docker Engine API, so
  Docker Desktop's Linux VM, Colima, and bare Linux all behave identically.
- **No telemetry.** No exporters, no event taps. `vabbe logs` just tails the
  container journal-stream.
- **No state file.** Labels on Docker objects are the state. containerlab
  carries a small topology file; we don't, we re-derive from the YAML.
