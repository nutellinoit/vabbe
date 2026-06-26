# The vabbe node image

`vabbe-node` ships in two flavors — a Debian-family **Ubuntu** base (the default)
and a RHEL-family **Rocky** base — so you can test against either package
manager. Build one locally with:

```
vabbe image build --base ubuntu --tag <your-tag>   # default
vabbe image build --base rocky  --tag <your-tag>
```

The per-base Dockerfiles live at `cmd/vabbe/image/<base>/Dockerfile`, are
embedded in the binary, and are built via the Docker Engine API `ImageBuild`
call — no shell-out to `docker buildx`.

## Published tags

Each `v*` tag publishes both bases to GHCR:

| Tag pattern | Example | Base |
|---|---|---|
| `:ubuntu`, `:ubuntu-vX.Y.Z`, `:ubuntu-rc` | `:ubuntu-v0.0.2` | Ubuntu |
| `:rocky`, `:rocky-vX.Y.Z`, `:rocky-rc` | `:rocky-v0.0.2` | Rocky |
| `:24.04`, `:latest`, `:rc`, `:vX.Y.Z` (legacy, **= Ubuntu**) | `:24.04` | Ubuntu |

The unprefixed legacy tags always point at the Ubuntu base so existing labs keep
working; `defaults.image` defaults to `:24.04`.

## What it ships and why

Packages below are the **Ubuntu** set; the Rocky base installs the dnf
equivalents (`iproute`, `iputils`, `python3-dnf`, `openssh-clients`, …) and
enables `sshd` instead of `ssh`.

| Package(s) | Why |
|---|---|
| `systemd systemd-sysv dbus` | init as PID 1, dbus for systemd machinery |
| `openssh-server` | node↔node SSH (root key login) |
| `python3`, **`python3-apt`** | ansible's `apt`/`package` modules fail without `python3-apt` ("Could not detect a supported package manager") |
| `sudo`, `ca-certificates`, `curl`, `gnupg`, `apt-transport-https` | common k8s/containerd repo install paths |
| `iproute2`, `iputils-ping`, `kmod`, `iptables`, `ipset`, `ipvsadm` | kube-proxy IPVS, keepalived, node networking |

SSHD is configured with `PermitRootLogin prohibit-password` and
`PasswordAuthentication no` via `/etc/ssh/sshd_config.d/vabbe.conf`. The lab's
public key is bind-mounted to `/root/.ssh/authorized_keys:ro` by the
orchestrator, so image-time keys aren't needed.

A handful of units that fail or are pointless in a container are masked in the
image (`getty@tty1`, `console-getty`, `systemd-udevd`, `udev-trigger`,
`sys-kernel-debug.mount`, `sys-kernel-tracing.mount`,
`systemd-journald-audit.socket`) to avoid a `degraded` boot.
**`systemd-modules-load.service` is intentionally NOT masked** — kubeadm-style
provisioners `systemctl enable` it to load kernel modules at boot.

`STOPSIGNAL SIGRTMIN+3` so `docker stop` shuts systemd down cleanly (fast
teardown, not a 10s kill).

## Roll your own

Point `defaults.image` (or `node.image`) at your own image. Same rules apply:
systemd as PID 1 (CMD `["/sbin/init"]`), `/sbin/init` present, sshd enabled,
`python3-apt` if you'll drive it with ansible, `STOPSIGNAL SIGRTMIN+3`.

## Boot-id token (for `ansible.builtin.reboot`)

The repo also bundles and installs `cmd/vabbe/image/boot-id-token.service`: a
tiny systemd unit that writes a fresh per-boot value to `/run/boot-id-token`
on every boot. Pass `boot_time_command: "cat /run/boot-id-token"` to any
`ansible.builtin.reboot` task. See `docs/gotchas.md` §11 for why.
