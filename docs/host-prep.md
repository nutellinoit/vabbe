# Why `host-prep` exists

Short version: **`host-prep` is the bridge between "containers pretending to be
VMs" and the host kernel — and in practice it exists to make Kubernetes
(kubeadm) work.** If you're not running kube-style installers, you probably
don't need it.

## The root cause: containers share one kernel

A vabbe "node" is a container, not a real VM. Real VMs each have their **own**
kernel; vabbe nodes all share the **host** kernel (on Docker Desktop, the host
is the Docker Desktop Linux VM). Most of the VM illusion — systemd, sshd, static
IPs, isolated filesystems — holds up fine inside the container. **Kernel state
does not.**

kubeadm's preflight (and kube-proxy/kubelet) require kernel state that is
**global**, not per-node:

- it can't be satisfied from inside a node container (a node can't `swapoff` the
  host's swap, and `modprobe` from a container loads into the host kernel
  anyway), and
- setting it per-node makes no sense — there is only one kernel to configure.

So it has to be arranged **once, on the host/VM**, before `up`. That single
preparatory step is `host-prep`.

## What it does, and why each piece

| Action | Why Kubernetes needs it |
|---|---|
| `swapoff -a` | kubeadm preflight fails (and kubelet refuses) with swap on. Swap belongs to the host kernel — only the host can turn it off. |
| `modprobe ip_vs ip_vs_rr ip_vs_wrr ip_vs_sh` | kube-proxy in IPVS mode needs these loaded. |
| `modprobe br_netfilter` | makes bridged traffic traverse iptables, which Kubernetes networking relies on. |
| `modprobe overlay` | containerd's overlayfs snapshotter. |
| `modprobe nf_conntrack` | connection tracking for Service load-balancing. |
| `sysctl -w fs.inotify.max_user_watches/instances` | many pods exhaust inotify and hit "too many open files"; these limits are host-global (not per-node), so the host is the only place to raise them. |

These are exactly the steps a kubeadm guide tells you to run on a fresh VM. On a
real VM you'd do them once per machine; with vabbe you do them once on the
shared host — that's the whole difference.

## Why it's a separate, explicit, opt-in command

`host-prep` is the **one** place vabbe steps outside the Docker Engine API and
touches the real host kernel. That is deliberate and bounded:

- **Never during `up`.** `up` must not silently change a shared host's kernel
  (swap off, modules loaded) behind your back.
- **Plan by default.** `vabbe host-prep` only prints the plan; `--run` executes
  it. On Linux `--run` requires root — it does **not** call `sudo` for you, so
  re-run as root (e.g. `sudo vabbe host-prep --run`).
- **Reversible.** `vabbe host-prep --restore` re-enables the swap it disabled.
- **Two backends.** On Linux it runs the commands directly on the host kernel;
  on Docker Desktop it uses a privileged `nsenter1` helper to enter the VM's
  PID 1 namespace and do the same to the VM kernel.

## When you do NOT need it

If your installer doesn't require swap to be off or those modules to be loaded —
i.e. you're not doing kubeadm/Kubernetes — skip `host-prep`. vabbe nodes boot
and talk to each other without it; it's purely the kernel-level prep that
Kubernetes-shaped workloads demand.

See [`gotchas.md`](gotchas.md) (§10–§13) for the gory details: overlay-on-overlay,
the swap failure mode, and how `doctor` reminds you to run this.
