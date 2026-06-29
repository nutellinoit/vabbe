# Alternative container runtimes (Kata Containers)

By default vabbe nodes run with Docker's default runtime (`runc`): containers
that **share the host kernel**. That's why `host-prep` exists and why several
[gotchas](gotchas.md) are about host-global kernel state.

Set a node's `runtime:` to run it under a different OCI runtime instead. The
headline case is **[Kata Containers](https://katacontainers.io)**, which runs
each node as a lightweight **VM with its own kernel** — true isolation, closer to
a "real VM" than a shared-kernel container. The same field also works for gVisor
(`runsc`), sysbox, etc.

```yaml
defaults:
  runtime: kata          # every node, unless overridden
nodes:
  - { name: cp0 }
  - { name: runner, runtime: runc, runner: true }   # per-node override
```

Empty/unset = Docker's default runtime (fully backwards compatible).

## What changes when a node uses a VM runtime

A node with a non-`runc` runtime is a real micro-VM with its own kernel. **systemd
still boots as PID1**, so the node behaves like a normal VM — `systemctl` works,
services start, installers that drive units (Kubernetes/kubelet, the ansible
examples) run. vabbe arranges this automatically, zero config:

- **systemd boots, but needs help.** Under Kata the node *is* a VM, and Kata mounts
  `/sys/fs/cgroup` **read-only**. systemd-as-init must *write* the cgroup tree (to
  create slices/scopes), so on a read-only cgroup it exits 255 and crash-loops. So
  vabbe gives a VM-runtime node **`CAP_SYS_ADMIN`** and a tiny shim command that
  **remounts `/sys/fs/cgroup` read-write before handing off to systemd**:
  `sh -c "mount -o remount,rw /sys/fs/cgroup; exec /sbin/init"`. Set your own
  `entrypoint:`/`cmd:` to override (the cap is still added).
- **`privileged` defaults to `false`** for a VM runtime (you can still force it on).
  `CAP_SYS_ADMIN` is enough for the cgroup remount, and full `privileged: true`
  actually *breaks* Kata: it tries to recreate device nodes that already exist in
  the guest (`Creating container device /dev/full … EEXIST`).
- **`vabbe exec`/`shell`/`ssh` go over real SSH** for a VM node (using the lab
  keypair and the node IP), not `docker exec`. A Kata node runs systemd, which owns
  the cgroup, so the runtime can't attach a `docker exec` process to it
  (`EBUSY: Failed to attach processes to control group`). Readiness (`up --wait`)
  is likewise a TCP probe of port 22 instead of a `docker exec` of `systemctl`.
- vabbe **does not bind-mount the host `/lib/modules`**: the guest has its own
  kernel, so the host's modules would be the wrong kernel's.
- `vabbe doctor` lists the daemon's available runtimes and flags a node whose
  `runtime:` the daemon doesn't have, so you catch it before `up`.

## Installing & registering Kata with Docker

Use the **official static release** (self-contained: shim + guest kernel + image +
QEMU, version-matched to recent Docker). Distro packages are often incomplete or
stale — e.g. on Arch the AUR `kata-*-bin` packages shipped no guest kernel and an
old version, which does **not** work with Docker 26+.

```sh
# 1. Install the static release to /opt/kata (needs Kata >= 3.29 for Docker >= 26)
curl -fsSL https://github.com/kata-containers/kata-containers/releases/download/3.32.0/kata-static-3.32.0-amd64.tar.zst -o /tmp/kata.tar.zst
sudo tar -C / -xf /tmp/kata.tar.zst
/opt/kata/bin/kata-runtime --version

# 2. Register it as a Docker runtime — MERGE into daemon.json, don't overwrite
#    (preserve any existing keys like data-root). Example merge with jq:
jq '.runtimes.kata = {"runtimeType":"/opt/kata/bin/containerd-shim-kata-v2"}' \
   /etc/docker/daemon.json | sudo tee /etc/docker/daemon.json.new
sudo mv /etc/docker/daemon.json.new /etc/docker/daemon.json

# 3. Reload Docker HOT (re-reads runtimes; no container downtime — no restart)
sudo systemctl reload docker
docker info --format '{{range $k,$v := .Runtimes}}{{$k}} {{end}}'   # should list: kata

# 4. Smoke test
docker run --runtime kata --rm ubuntu:24.04 uname -r   # shows the Kata guest kernel
```

## Sizing the VM (`cpus:` / `memory:`)

Set `cpus:`/`memory:` on a node (or in `defaults`) to size the guest VM — they map
to Docker's `--cpus`/`--memory`, which Kata uses to size the micro-VM:

```yaml
nodes:
  - { name: cp0, runtime: kata, cpus: 2, memory: 4g }
```

Heads-up: Kata sizes the guest **on top of** its config base
(`default_vcpus`/`default_memory` in `configuration.toml`, default `1` / `2048`).
So `cpus: 2` yields ~3 vCPUs and `memory: 4g` ~6 GB on a stock config. For exact
sizing, set `default_vcpus = 0` / lower `default_memory` in the Kata config.

## Host requirements (the gotchas we actually hit)

- **KVM**: `/dev/kvm` must exist. On a bare-metal host with VT-x/AMD-V it's there;
  inside a VM you need nested virtualization (e.g. standard GitHub-hosted runners
  do **not** support it reliably).
- **`vhost_vsock` kernel module** must be loadable on the host (QEMU Kata uses
  vsock for host↔guest). `sudo modprobe vhost_vsock vhost_net`. Missing it gives:
  `failed to create shim task: open /dev/vhost-vsock: no such device`.
- **Reboot after a kernel update**: if the kernel package was upgraded but you
  haven't rebooted, the running kernel's modules may be gone from `/lib/modules`,
  so `vhost_vsock` won't load until you reboot into the new kernel. (Watch out:
  `yay -S …` runs a full `-Syu` and can upgrade your kernel as a side effect.)

## Caveat: Kata guest kernel is minimal

Kata's bundled guest kernel is stripped down. Kubernetes features that need
specific modules (e.g. `ip_vs` for kube-proxy IPVS) may not be present in the
guest — you'd need a custom Kata kernel/config. So "Kata makes host-prep
unnecessary" is true for swap (each guest has its own), but **module
availability moves into the guest**, it doesn't disappear.
