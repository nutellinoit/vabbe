# The vabbe node image

`ghcr.io/nutellinoit/vabbe-node:24.04` is a known-good VM base for `vabbe`.
Build it locally with:

```
vabbe image build --tag <your-tag>
```

(The same `cmd/vabbe/image/Dockerfile` is embedded in the binary and built via the
Docker Engine API `ImageBuild` call — no shell-out to `docker buildx`.)

## What it ships and why

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
