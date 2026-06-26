# Gotchas — the expensive lessons baked into vabbe

These cost a night of doing this by hand. They're encoded as behavior, defaults,
and doctor warnings. Read this before you wonder why something "should work"
and doesn't.

## 1. Reachability: container↔container yes, host→container **no** (on macOS)

- On a **user-defined bridge**, containers reach each other by **static IP**
  and **by name** (Docker's embedded DNS).
- On **Linux**, the host can also route to those IPs directly.
- On **macOS Docker Desktop**, the bridge lives inside Docker's Linux VM and the
  Mac host **cannot** route to `10.x` container IPs. Not a bug; the platform.
- **Consequences:**
  - The driver of your test (the thing running furyctl/ansible/kubectl) must
    itself be **a node inside the lab** (a `runner:`). All traffic then stays
    container-to-container and is identical on both OSes.
  - `vabbe ssh`/`exec`/`shell` go through `docker exec`, **not** a TCP
    connection from the host. Real SSH (port 22) is for node↔node only.
  - To expose a service to the **Mac host**, use Docker published ports
    (`ports:` on a node = `-p`). That's the only thing that crosses the VM
    boundary on macOS.

## 2. Containers must be **privileged** to be VMs

`vabbe` defaults `privileged: true` on every node. systemd as PID 1 and nested
runtimes (containerd/kubeadm) need it. Also required:
- tmpfs on `/run` and `/run/lock` (and `/tmp`). vabbe sets these by default.
- `StopSignal = SIGRTMIN+3` so `docker stop` shuts systemd down cleanly
  (inherited from the image, mirrored in `HostConfig`).
- A handful of units are masked in the node image (`getty@tty1`,
  `console-getty`, `systemd-udevd`, `systemd-udev-trigger`, debug/trace mounts)
  to avoid a `degraded` boot. **`systemd-modules-load.service` is intentionally
  NOT masked** — kubeadm-style provisioners `systemctl enable` it; masking
  makes node prep fail with "Unit file is masked".

## 3. The node image is 90% of the pain

The orchestrator is easy; the image is where time goes. The bundled
`ghcr.io/nutellinoit/vabbe-node:24.04` (see `docs/node-image.md`) ships:
- `systemd systemd-sysv dbus` + `openssh-server` with root key login.
- `python3` **and `python3-apt`** — ansible's `apt`/`package` modules fail
  without `python3-apt` with a misleading "Could not detect a supported
  package manager".
- `iproute2 iputils-ping kmod iptables ipset ipvsadm` for kube-proxy IPVS,
  keepalived, etc.

## 4. Architecture / emulation (Apple Silicon)

Don't force `--platform`. Let Docker pick native (arm64) on Apple Silicon.
Heavy workloads under qemu emulation are slow and flaky. On Docker Desktop:
- the "Apple Virtualization framework" backend + the Rosetta toggle supports
  amd64 via Rosetta;
- the "Docker VMM" backend does **not** — amd64 images give `exec format
  error`.
`vabbe doctor` prints the daemon OS/arch; if a `vabbe up` fails with `exec
format error`, your `image:` is amd64-only and emulation isn't available.

## 5. `/etc/hosts`

Do **not** mount the host's `/etc/hosts` (read-only on macOS; editing fails).
You don't need to: embedded DNS on the user network resolves node names.
`nip.io` etc. resolve fine because containers have internet DNS by default.

## 6. keepalived / VRRP

VRRP works on the user bridge. With `privileged` (or `NET_ADMIN`+`NET_RAW`),
a node can claim a virtual IP. Multi-node VRRP election works on the bridge
(multicast and unicast_peer both function). Set `caps: [NET_ADMIN, NET_RAW]`
to run keepalived on a non-privileged node.

## 7. GitHub rate limits are your problem, but vabbe helps

Unauthenticated GitHub rate limits hit fast (mise, furyctl tool downloads,
raw release assets). Not vabbe's job to fix — but pass **`env:`** into the
runner (e.g. `GITHUB_TOKEN: "${GITHUB_TOKEN}"`) so your workload has it. The
`vabbe.yaml` `${VAR}` form expands from the host env at load time.

## 8. Swap: the container sees the host/VM swap — disable it on the host

A container's `/proc/meminfo` reflects the **host kernel** (on Docker Desktop,
the Linux VM). If that VM has swap, every node reports `SwapTotal > 0`, so
kubeadm's swap preflight and ansible "disable swap" tasks fire — and
`swapoff -a` inside the node **fails** (`rc=32`) because the container can't
turn off the host's swap, even when privileged.

**Fix:** run `vabbe host-prep` once before `vabbe up`. By default it only
**prints the plan** — add `--run` to execute:
- Linux: `sudo vabbe host-prep --run` runs `swapoff -a` + `modprobe`s directly.
  It never invokes `sudo` for you, and `--run` requires root. Undo with
  `sudo vabbe host-prep --restore --run`.
- Docker Desktop: `vabbe host-prep --run` starts a privileged helper with
  `--pid=host` that enters the VM's PID 1 namespace and runs `swapoff -a` on the
  VM kernel (no host sudo).

`vabbe` does **not** do this automatically during `up` — host-prep is opt-in,
loud, and never surprises you on a shared host. `vabbe doctor` reminds you to
run it. This is the single most surprising kubeadm-on-containers failure.

## 9. Kernel modules: nodes can't `modprobe`, but the modules are already there

Workloads run `modprobe ip_vs` etc. Inside a generic Ubuntu node this
**fails** (`Module ip_vs not found in /lib/modules/<kernel>`) — the image has
no module tree for the running (e.g. linuxkit) kernel.
But on Docker Desktop those modules are **builtin** to the VM kernel — the
node just lacks the metadata for `modprobe` to *know*.

**Fix:** vabbe bind-mounts the host's `/lib/modules` **read-only** into every
VM node (non-runner) by default. `modprobe` then reads `modules.builtin`,
sees the module is builtin/available, returns `0`. Works on Linux too (there
the tree holds real `.ko` files and modprobe actually loads them).
`vabbe host-prep` will also `modprobe` them on the host/VM as belt-and-suspenders.

## 10. `/var/lib/containerd` needs a real fs (overlay-on-overlay breaks pods)

If a node runs its **own** containerd (kubeadm/k8s), the snapshotter sits at
`/var/lib/containerd`. On Docker Desktop the node rootfs is already overlayfs,
so that's overlay-on-overlay — containerd starts but **running pods fails**:
`failed to create shim task: failed to mount rootfs component: invalid
argument`. etcd/systemd boot fine; only pods break (looks like "apiserver
never comes up").

**Fix:** back `/var/lib/containerd` with a fresh volume. The vabbe node image
declares `VOLUME ["/var/lib/containerd"]` (Docker auto-creates an anonymous
volume). `kind` does the same for its node `/var`. For VM nodes you can also
add it explicitly via `mounts:` if you want a named volume.

## 11. Reboots work, but `ansible.builtin.reboot` needs help

A "reboot" inside a node works mechanically: systemd calls `reboot(2)` (needs
`CAP_SYS_BOOT` — privileged has it); the PID namespace's init dies; the
container exits; with a Docker restart policy Docker restarts it → systemd
boots again on the **same static IP**, writable layer + volumes survive. So
`restart: unless-stopped` is the default for vabbe VM nodes.

**But `ansible.builtin.reboot` fails by default** — it confirms the reboot by
waiting for the kernel `boot_id` (`/proc/sys/kernel/random/boot_id`) to change,
and it **never changes** because the container shares the (un-rebooted)
host/VM kernel.

**Fix:** ship a per-boot token on tmpfs. The vabbe node image bundles a small
systemd unit (`boot-id-token.service`, embedded in this repo at
`cmd/vabbe/image/boot-id-token.service`) that writes a fresh value to
`/run/boot-id-token` on every boot. Then give the reboot task:

```yaml
ansible.builtin.reboot:
  boot_time_command: "cat /run/boot-id-token"
```

This works for both a container restart and a real VM reboot. (PID 1's
start-time from `/proc/1/stat` also works in a container but is unreliable on
a real VM reboot, so prefer the tmpfs token.)

## 12. "host-prep" is a real phase

Anything kernel-level (swap, modules, sysctls) must be arranged on the **host
kernel**, not the node — a container-as-VM shares the host kernel. `vabbe
host-prep` is that phase: on Docker Desktop it uses the `nsenter1` trick; on
Linux it runs `swapoff -a` + the needed `modprobe`s directly (run it as root —
it never calls `sudo` itself). It's safe by default: it only **prints the plan**
unless you pass `--run`, never runs during `up`, prints every command, and can
be undone with `--restore`.

## 13. Node DNS: Docker's `127.0.0.11` isn't reachable from pods

On a user-defined network Docker writes `nameserver 127.0.0.11` (its embedded
DNS) into the node's `/etc/resolv.conf`. That address is NAT-magic that only
works in the node's **root** netns — it is **unreachable from a pod netns**.
Kubernetes CoreDNS runs as a pod with `dnsPolicy: Default` (it inherits the node
resolv.conf), forwards to `127.0.0.11`, and dies with
`[FATAL] plugin/loop: Loop detected for zone "."`; anything needing cluster DNS
then fails. On a real VM resolv.conf holds a real upstream, so this never
happens. `docker --dns` does **not** fix it — on a user-defined network Docker
still puts `127.0.0.11` in resolv.conf and `--dns` only changes what sits behind
it. So vabbe rewrites the node's resolv.conf at boot (the `vabbe-resolv.service`
unit) to the `dns:` upstreams, default `1.1.1.1`/`1.0.0.1`. Set `dns:` for an
internal/corporate resolver. Losing Docker's per-container-name resolution is
fine — vabbe nodes address each other by static IP. Runners keep Docker's
resolver (they're clients in the node netns, not pods). Same spirit as `kind`:
never hand pods a resolver that only lives in the node's netns.
