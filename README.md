<!-- markdownlint-disable MD033 MD041 -->
<h1 align="center">
  <img alt="vabbe logo" src="docs/images/logo.png" width="200"><br/>
  vabbè
</h1>
<!-- markdownlint-enable MD033 MD041 -->

> *vabbè* — Italian for *"eh, whatever, fine."* Making VMs out of containers is an antipattern. I know. But it's **good enough** for throwaway test environments, so — *vabbè*.

`vabbe` spins up Docker containers that act like throwaway VMs (systemd + sshd, static IPs on a network you define) for testing installers — like `kind`, but the nodes are generic VMs instead of a k8s cluster.

I built it because I wanted **kind's ergonomics** — one tool, one `vabbe.yaml`, the same environments both **locally and in CI** — but for **generic nodes**, with the freedom to make each one a plain Docker container *or* a real VM (a **Kata micro-VM** with its own kernel + systemd) as the job demands, even mixed in the same lab. And I wanted something **small enough to build and understand from the ground up** — not another big system with its own daemon to wrestle.

Tested on **Linux with Docker** and **macOS with Docker Desktop** — including **mixed `runc` + Kata labs** (some nodes shared-kernel, some real micro-VMs with their own kernel and systemd) running real installers (nginx, Postgres streaming replication) across both runtimes. See [Want real per-node kernels?](#want-real-per-node-kernels).

## Install

```
go install github.com/nutellinoit/vabbe@latest
# or with mise (global, prebuilt release binary):
mise use -g github:nutellinoit/vabbe
# or in this repo: mise install  then `mise run build`
```

> Use the `github:` mise backend, not `ubi:` — mise deprecated the `ubi` backend.

## The one micro example

`vabbe.yaml`:

```yaml
name: e2e
network: { subnet: 10.10.1.0/24 }
defaults: { image: ghcr.io/nutellinoit/vabbe-node:v0.3.2 }
nodes:
  - { name: a, ip: 10.10.1.2 }
  - { name: b, ip: 10.10.1.3 }
  - name: runner
    ip: 10.10.1.250
    image: jdxcode/mise:latest
    entrypoint: ["/bin/sleep", "infinity"]
    runner: true
    mounts: ["./:/workspace"]
```

```
vabbe up           # create network + containers, idempotent
vabbe up --wait    # ...and block until each node's sshd is up (no boot race)
vabbe up --recreate # rebuild nodes whose config drifted (else `up` just warns)
vabbe ls           # NODE IP IMAGE STATUS (colored; --json for scripting)
vabbe dns          # nip.io hostnames per node (--common-dns-zone for sslip.io/etc)
vabbe inventory    # Ansible inventory of the server nodes (--runner for in-runner use)
vabbe shell        # drop into the runner (bash by default; --shell to choose)
vabbe exec a -- ping -c1 10.10.1.3     # container↔container works
vabbe down         # remove containers + network
vabbe down --all   # remove ALL vabbe labs on the daemon (no -f needed; orphan cleanup)
```

## The one rule that surprises people

**On macOS Docker Desktop the host cannot route to container IPs.** Run your driver (ansible/furyctl/kubectl) as an in-network `runner` node, not on the host. `vabbe ssh`/`exec`/`shell` use `docker exec`, so they work on both OSes.

## Want real per-node kernels?

Set `runtime: kata` on a node (or in `defaults`) to run it as a lightweight VM with its own kernel via [Kata Containers](https://katacontainers.io) — a real VM, not a shared-kernel container, and **systemd still boots inside it** (so `systemctl` and installers work). vabbe drives it as a normal **Docker runtime** — same node images as `runc`, nothing Kata-specific to build.

To use it you need:

- a host with **KVM** (`/dev/kvm`) — bare metal, or a VM with nested virtualization;
- **Kata installed and registered as a Docker runtime** (`/etc/docker/daemon.json` → `runtimes.kata`) — no other tooling;
- only if your installer **loads kernel modules** (Kubernetes/kubeadm, IPVS…): point Kata at a **modules-enabled guest kernel** (the kata-static release already ships one, `vmlinux-nvidia-gpu` — it's a normal kernel, just the build name) so `lsmod`/Ansible `modprobe` work.

See [`docs/kata.md`](docs/kata.md) for the install steps, host requirements, and that module-tooling note.

## Alternatives & prior art

vabbe doesn't invent new primitives — it's a thin, opinionated combination of well-trodden ones. If one of these fits your case better, use it:

- **[kind](https://kind.sigs.k8s.io/)** — nodes-as-containers like vabbe, but specifically a Kubernetes cluster. Use it when you want k8s, not generic VMs.
- **[Vagrant](https://www.vagrantup.com/) / [Multipass](https://multipass.run/) / [Lima](https://lima-vm.io/)** — real VMs: higher fidelity, heavier and slower. Reach for them when you need a true VM and don't mind the weight.
- **[LXD / Incus](https://linuxcontainers.org/incus/)** — system containers that behave like VMs (systemd inside) plus a real VM mode, one API. The closest in spirit — but a bigger system with its own daemon.
- **[Molecule](https://ansible.readthedocs.io/projects/molecule/)** + systemd-in-Docker images — the standard way to test Ansible roles in containers.

vabbe's niche is the ergonomics: a single small binary on **plain Docker**, the **same `vabbe.yaml` for local and CI**, and a **per-node runtime** so you can mix cheap shared-kernel containers with real Kata micro-VMs in one lab. When you outgrow "good enough," the tools above are there.

See [`docs/`](docs/) for everything else: config reference, gotchas, macOS specifics, the node image, releases.