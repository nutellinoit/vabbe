# vabbe

> *vabbè* — Italian for *"eh, whatever, fine."* Making VMs out of containers is an antipattern. We know. But it's **good enough** for throwaway test environments, so — *vabbè*.

`vabbe` spins up Docker containers that act like throwaway VMs (systemd + sshd, static IPs on a network you define) for testing installers — like `kind`, but the nodes are generic VMs instead of a k8s cluster.

## Install

```
go install github.com/nutellinoit/vabbe@latest
# or: mise install  (in this repo) then `mise run build`
```

## The one micro example

`vabbe.yaml`:

```yaml
name: e2e
network: { subnet: 10.10.1.0/24 }
defaults: { image: ghcr.io/nutellinoit/vabbe-node:24.04 }
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
vabbe ls           # NODE IP IMAGE STATUS
vabbe dns          # nip.io hostnames per node (--common-dns-zone for sslip.io/etc)
vabbe shell        # drop into the runner (in-network driver)
vabbe exec a -- ping -c1 10.10.1.3     # container↔container works
vabbe down         # remove containers + network
```

## The one rule that surprises people

**On macOS Docker Desktop the host cannot route to container IPs.** Run your driver (ansible/furyctl/kubectl) as an in-network `runner` node, not on the host. `vabbe ssh`/`exec`/`shell` use `docker exec`, so they work on both OSes.

See [`docs/`](docs/) for everything else: config reference, gotchas, macOS specifics, the node image, releases.