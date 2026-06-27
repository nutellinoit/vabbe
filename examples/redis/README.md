# redis example

Three vabbe nodes — one primary and two replicas — installed with the
[`geerlingguy.redis`](https://galaxy.ansible.com/ui/standalone/roles/geerlingguy/redis/)
community role; replication is wired up at runtime (the role doesn't, in this
version).

## Prerequisites

- Docker (running)
- [mise](https://mise.jdx.dev) — installs Python, uv and the `vabbe` CLI pinned in `mise.toml`

## Run

```sh
mise trust      # once, to trust this folder's mise.toml
mise run test   # the whole loop, then tears down
```

Or step by step: `mise run deps | up | inventory | converge | verify | down`.

## What it proves

`verify` sets a key on the primary (`redis-cli set`) and reads it back from a
replica (`redis-cli get`), retrying until replication has propagated.

## Notes

- Ansible runs **from your host** against the node IPs — works on Linux. On
  macOS run it from an in-network runner and use `vabbe inventory --runner`.
- Throwaway lab: redis is bound to all interfaces with protected-mode off and no
  auth. Don't copy that to anything real.
- Override the node image with `VABBE_NODE_IMAGE` (defaults to `:v0.1.3`).
