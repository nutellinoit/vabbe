<!-- markdownlint-disable MD033 MD041 -->
<h1 align="center">
  <img alt="vabbe logo" src="docs/images/logo.png" width="200"><br/>
  vabbè
</h1>
<!-- markdownlint-enable MD033 MD041 -->

> *vabbè* — Italian for *"eh, whatever, fine."* Making VMs out of containers is an antipattern. I know. But it's **good enough** for throwaway test environments, so — *vabbè*.

`vabbe` spins up Docker containers that act like throwaway VMs (systemd + sshd, static IPs on a network you define) for testing installers — like `kind`, but the nodes are generic VMs instead of a k8s cluster.

Tested on **Linux with Docker** and **macOS with Docker Desktop**.

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
defaults: { image: ghcr.io/nutellinoit/vabbe-node:v0.2.0 }
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

See [`docs/`](docs/) for everything else: config reference, gotchas, macOS specifics, the node image, releases.