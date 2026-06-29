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
  the OCI read-only paths `/sys/fs/cgroup` and `/proc/sys` **read-only**. systemd
  must *write* the cgroup tree (to create slices/scopes) — on a read-only cgroup it
  exits 255 and crash-loops — and installers need `/proc/sys` writable for `sysctl`
  (kubeadm wants `net.ipv4.ip_forward=1`, Cilium/kube-proxy set many). So vabbe
  gives a VM-runtime node **`CAP_SYS_ADMIN`** and a tiny shim that **remounts both
  read-write before handing off to systemd**:
  `sh -c "mount -o remount,rw /sys/fs/cgroup; mount -o remount,rw /proc/sys; exec /sbin/init"`.
  (runc VM nodes get these rw via `privileged: true`.) Set your own
  `entrypoint:`/`cmd:` to override (the cap is still added).
- **`privileged` defaults to `false`, but caps default to `ALL`** for a VM runtime.
  A Kata node is a hypervisor-isolated VM, so VM-grade capabilities inside are safe
  and expected (it's the parity of the `privileged: true` runc VM nodes get) —
  installers like kubeadm/Cilium rely on it. Override with `caps:` for a tighter
  set (`SYS_ADMIN` is always kept; it's needed for the cgroup remount). Full
  `privileged: true` is *not* used because it **breaks** Kata: it recreates device
  nodes that already exist in the guest (`Creating container device /dev/full …
  EEXIST`).
- **`modprobe` works for built-in modules.** The guest has no `/lib/modules` tree,
  so `modprobe` would `FATAL` even for modules compiled into the Kata kernel —
  breaking installers that load `nf_conntrack`, `br_netfilter`, `overlay`, the
  `ip_vs*` family, `configs` (kubeadm preflight), etc. vabbe's node image ships a
  boot service (`vabbe-kmod`) that synthesizes a `modules.builtin` for that common
  set on VM-runtime nodes, so `modprobe <mod>` is a no-op success (the feature is
  already in the kernel). A module *not* in that set still fails (correctly). No-op
  on runc. (Caveat: `configs` is listed so kubeadm's `modprobe configs` doesn't
  `FATAL`, but the stock Kata kernels are built `CONFIG_IKCONFIG=n`, so
  `/proc/config.gz` won't actually appear — kubeadm's kernel-config check then just
  warns, it doesn't block.) Note: this fixes
  the **`modprobe` CLI**; tools that read `/proc/modules` (`lsmod`, Ansible's
  `modprobe` module) need a modules-enabled guest kernel — see
  [Loadable-module tooling](#loadable-module-tooling-lsmod-ansible-modprobe) below.
- **`vabbe exec`/`shell`/`ssh` go over real SSH** for a VM node (using the lab
  keypair and the node IP), not `docker exec`. A Kata node runs systemd, which owns
  the cgroup, so the runtime can't attach a `docker exec` process to it
  (`EBUSY: Failed to attach processes to control group`). Readiness (`up --wait`)
  is likewise a TCP probe of port 22 instead of a `docker exec` of `systemctl`.
- vabbe **does not bind-mount the host `/lib/modules`**: the guest has its own
  kernel, so the host's modules would be the wrong kernel's.
- `vabbe doctor` lists the daemon's available runtimes and flags a node whose
  `runtime:` the daemon doesn't have, so you catch it before `up`.

## Caveat: DNS — no Docker embedded resolver inside the VM

Docker's embedded resolver `127.0.0.11` (which resolves other nodes by name, and
forwards external lookups) is iptables/proxy magic that lives in the host network
namespace. **Inside a Kata VM it doesn't work** — there `127.0.0.11` is just the
guest's own loopback, with nothing listening, so name resolution silently fails.

What this means for Kata nodes:

- **Use a real upstream resolver** for external lookups. vabbe's default (`1.1.1.1`,
  `1.0.0.1`) already does this, so a Kata node resolves the internet out of the box
  (apt/dnf work). Setting `dns: ["127.0.0.11"]` on a Kata node would leave it with
  no DNS at all, so **vabbe rejects it at `up`** with a clear error — switch that
  node to a real upstream. (Some examples set `127.0.0.11` for runc; override it
  when running them under Kata.)
- **Node-to-node name resolution still works for statically-addressed nodes.**
  vabbe injects every peer that has a static `ip:` into each node's `/etc/hosts`
  (via Docker `ExtraHosts`), which *is* honored inside the VM — so `ping cp1` etc.
  work without the embedded resolver. **Auto-assigned (no-subnet) peers** aren't in
  `/etc/hosts` (their IP isn't known until they start), so reach those by **IP** —
  `vabbe ip` / `vabbe inventory` / `vabbe dns` report the live addresses.

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

# 3. (Needed only if your installer LOADS kernel modules — Kubernetes/kubeadm,
#     IPVS, …) Kata's default guest kernel has no loadable-module support, so it
#     exposes no /proc/modules and `lsmod` / Ansible's modprobe module break. Point
#     Kata at the modules-enabled kernel the static release already ships
#     (`vmlinux-nvidia-gpu` — "nvidia" is just the build flavor; it's a normal
#     kernel that boots fine without a GPU). See "Loadable-module tooling" below.
sudo install -d /etc/kata-containers
sudo cp /opt/kata/share/defaults/kata-containers/configuration-qemu.toml /etc/kata-containers/configuration.toml
sudo sed -i 's#^kernel = .*#kernel = "/opt/kata/share/kata-containers/vmlinux-nvidia-gpu.container"#' /etc/kata-containers/configuration.toml

# 4. Reload Docker HOT (re-reads runtimes; no container downtime — no restart)
sudo systemctl reload docker
docker info --format '{{range $k,$v := .Runtimes}}{{$k}} {{end}}'   # should list: kata

# 5. Smoke test
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

## Loadable-module tooling (`lsmod`, Ansible `modprobe`)

`vabbe-kmod` fixes the **`modprobe` CLI**, but tools that *introspect* loaded
modules read `/proc/modules`:

- `lsmod` → `libkmod: ERROR ... could not open /proc/modules`
- Ansible's `community.general.modprobe` / `ansible.builtin.modprobe` (used by e.g.
  the KFD `kube-node-common` role) → `[Errno 2] No such file or directory:
  '/proc/modules'` → the task fails on every node, so kubeadm never runs.

The reason: Kata's **default** guest kernel is built **without loadable-module
support** (`CONFIG_MODULES=n`), so the kernel never exposes `/proc/modules`. It
can't be faked at runtime — `/proc` is procfs (you can't `touch` it, and there's
no mount point to bind over). The only fix is a guest kernel built with
`CONFIG_MODULES=y`.

**No rebuild needed** — the kata-static release already ships one:
`vmlinux-nvidia-gpu` is built with `CONFIG_MODULES=y`. Despite the name it's a
**standard kernel with module support** — it boots fine without a GPU and is
essentially the same size; the `nvidia-gpu` label is just the Kata build flavor.
Point Kata at it (host-wide):

```toml
# /etc/kata-containers/configuration.toml  (override; copy from
# /opt/kata/share/defaults/kata-containers/configuration-qemu.toml)
kernel = "/opt/kata/share/kata-containers/vmlinux-nvidia-gpu.container"
```

```sh
sudo systemctl reload docker   # or restart, per your setup
```

Verified result on a vabbe Kata node: the node boots normally (`uname -r` →
`…-nvidia-gpu`, systemd `running`), `/proc/modules` exists (empty — the modules are
built **in**, so nothing is "loaded"), `lsmod` works, and the Ansible `modprobe`
module is satisfied (it reads the empty `/proc/modules`, runs `modprobe`, which
returns 0 thanks to `vabbe-kmod`'s `modules.builtin`). Built-in modules never show
up in `/proc/modules` — that's expected and fine.

(Per-node instead of host-wide: Kata's `io.katacontainers.config.hypervisor.kernel`
annotation can select the kernel per container, but Kata blocks kernel-path
annotations unless you allow them via `enable_annotations` — so it still needs a
Kata config change. Host-wide is simpler.)

## Caveat: Kata guest kernel is minimal

Kata's bundled guest kernel is reasonably complete — the Kubernetes essentials
(`nf_conntrack`, `br_netfilter`, `overlay`, the `ip_vs*` family, `vxlan`) are
compiled **in**, and vabbe's `vabbe-kmod` boot service makes `modprobe` find them
(see above). But the kernel is fixed: a module that *isn't* built into Kata's
kernel can't be loaded at all (there's no matching `/lib/modules/*.ko` to insert),
so a feature outside the built-in set needs a **custom Kata kernel/config**. So
"Kata makes host-prep unnecessary" is true for swap (each guest has its own), but
**module availability moves into the guest** — it doesn't disappear, it's fixed at
the Kata kernel build.
