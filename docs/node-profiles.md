# Node profiles & runtime requirements (design note)

> **Status: design note.** The "current behavior" sections describe what vabbe does
> **today** (accurate). The `profile:` field is a **proposal**, not implemented yet —
> don't put it in a `vabbe.yaml` expecting it to work.

This note captures two things:

1. **What it actually takes to make a node behave like a VM** under each runtime —
   i.e. the requirements/features we discovered the hard way (a useful reference on
   its own).
2. A proposed cleanup: separate **what kind of node** (a *profile*) from **how
   isolated** it is (the *runtime*), so all of the below lives behind one knob.

## Two orthogonal axes

- **Runtime** = *how isolated*: `runc` (shared host kernel) or a VM runtime like
  `kata` (own kernel, hypervisor-isolated). See [kata.md](kata.md).
- **Profile** = *what kind of node*: a plain container, a systemd "VM" node, or a
  raw passthrough. Today this axis is **implicit** (a node is a "VM node" by
  default; `runner: true` opts out). The proposal makes it explicit.

vabbe's job is to apply the right setup for the **(profile × runtime)** combination,
because the gaps differ per runtime.

## Current behavior (implicit model)

- Default node ⇒ **VM node** (systemd, etc.).
- `runner: true` ⇒ plain container (no systemd assumptions, not privileged, no
  `/lib/modules`).
- `runtime: kata` ⇒ the VM-node setup is auto-applied in **Kata flavor**.

### What a "VM node" gets, per runtime

| Concern | `runc` (shared kernel) | `kata` (own kernel) |
| --- | --- | --- |
| Privilege | `privileged: true` | `privileged: false` + **caps `ALL`** (`privileged` breaks Kata: `/dev/full` EEXIST) |
| PID 1 | `/sbin/init` (systemd) | shim: `mount -o remount,rw /sys/fs/cgroup; mount -o remount,rw /proc/sys; [ -e /dev/kmsg ] || mknod /dev/kmsg c 1 11; exec /sbin/init` |
| Kernel | host's (shared) | Kata guest kernel (own) |
| `/lib/modules` | bind-mounted from host (ro) | **not** bound; `vabbe-kmod` synthesizes `modules.builtin` so `modprobe` finds built-ins |
| Kernel config | host's `/proc/config.gz` / `/boot` | shipped in from the host (`kata-runtime env` → `config-*`) to `/boot/config-<kver>` + `/lib/modules/<kver>/config` |
| `/var/lib/containerd` | image `VOLUME` (real host fs) | ext4-on-loop (`vabbe-containerd-store`); virtiofs can't hold overlayfs |
| `/dev` extras | (privileged → full host /dev) | `mknod` `/dev/kmsg` (kubelet) + loop nodes (containerd store) |
| `exec`/`shell`/`ssh` | `docker exec` | **real SSH** (systemd owns the cgroup → `docker exec` EBUSY) |
| Readiness (`--wait`) | `docker exec systemctl is-active` | TCP probe of port 22 |
| Node-to-node names | Docker embedded resolver `127.0.0.11` | `/etc/hosts` (static-IP peers); `127.0.0.11` is **rejected** (unreachable in the VM) |
| Stop signal | `SIGRTMIN+3` | `SIGRTMIN+3` |
| Lab keypair, `dns:` rewrite, `restart: unless-stopped` | yes | yes |

A `runner` node gets **none** of the above (just the keypair + Docker defaults).

### Gap classes (why some things can't be fixed in vabbe)

- **Class A — user-space / config**: systemd init, cgroup/proc-rw, `modules.builtin`,
  kernel-config file, `/dev` nodes, ext4-loop storage. **vabbe fixes these** (the
  table above).
- **Class B — feature not compiled into the Kata kernel**: e.g. `CONFIG_ISCSI_TCP`
  (Longhorn), `CONFIG_IKCONFIG` (`/proc/config.gz`). **vabbe cannot fix these** — no
  user-space trick conjures a missing kernel feature. The only fix is a **custom
  Kata guest kernel** (host/infra), or run that workload on `runc`. `CONFIG_MODULES`
  was a class-B gap dodged by selecting the `vmlinux-nvidia-gpu` kernel (host
  config). See [kata.md](kata.md).

### Host requirements (not vabbe's job — provisioning/ansible)

- KVM (`/dev/kvm`), `vhost_vsock`, Kata registered as a Docker runtime.
- A **modules-enabled guest kernel** if you need `lsmod`/Ansible-`modprobe`
  (`vmlinux-nvidia-gpu`) — and a **custom kernel** for class-B features.

## Proposal: an explicit `profile:` field

```yaml
defaults:
  profile: vm            # vm | runner | raw   (proposed)
  runtime: kata
nodes:
  - { name: cp0 }                          # profile vm  + runtime kata
  - { name: drv, profile: runner }         # plain container
  - { name: x,  profile: raw, runtime: kata }  # kata as-is, no vabbe setup
```

| profile | meaning |
| --- | --- |
| `vm` (default for non-runners) | full systemd VM-node setup, applied per the table above for the chosen runtime |
| `runner` | plain container — today's `runner: true` |
| `raw` | use the runtime as-is, **no vabbe setup** (escape hatch: raw Kata/gVisor) |

**Why:** one knob ("what node do I want") wraps every customization above —
including the runc-only ones (the `/var/lib/containerd` volume becomes "part of
profile `vm` on runc", not baked unconditionally). It **decouples** runtime from
treatment (a custom-kernel Kata or gVisor needn't inherit kata-stock fixes), and
adds the missing **`raw`** escape hatch.

**Backward compatible:** `profile` defaults to `vm` for non-runners ⇒ `runtime:
kata` keeps "just working" with zero config; `runner: true` becomes an alias of
`profile: runner`.

## Timing

We're on **0.x**, so breaking changes are cheap — the "wait to avoid
backward-compat churn" argument **doesn't really apply**. Introducing `profile:`
now and reshaping it freely as we learn is fine; SemVer-wise nobody's locked in.

So the timing is a **focus/effort** call, not a compatibility one:

- **Do it now** — pro: the explicit model also *documents* requirements/features
  (this very note), and `raw` gives an escape hatch we lack today. Con: we're
  mid-discovery (today `/dev/kmsg`; Longhorn/custom-kernel looming), so we'll touch
  it again — but that's just adding a row in the same place, not an API break.
- **Do it after kubeadm completes** — pro: refactor once, on a settled set. Con:
  the implicit model (`runtime:` + `runner:`) keeps working in the meantime, so
  there's no user-facing pain forcing the change.

Either is defensible. The refactor is mostly **relabeling gating that already
exists**, so it's low-risk whenever we pull the trigger.
