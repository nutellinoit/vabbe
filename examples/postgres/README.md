# postgres example

Two vabbe nodes — a primary and a streaming-replication replica. Role-free:
distro PostgreSQL 16 + `ansible.builtin`, with the replication user created via
the [`community.postgresql`](https://galaxy.ansible.com/ui/repo/published/community/postgresql/)
collection and the replica seeded with `pg_basebackup`.

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

`verify` inserts a row on the primary, then reads it back from the (read-only)
replica, retrying until replication catches up.

## Notes

- Ansible runs **from your host** against the node IPs — works on Linux. On
  macOS run it from an in-network runner and use `vabbe inventory --runner`.
- Throwaway lab: a fixed replication password and permissive `pg_hba` for the
  lab subnet. Not for anything real.
- Override the node image with `VABBE_NODE_IMAGE` (defaults to `:v0.1.3`).
