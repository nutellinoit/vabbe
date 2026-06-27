# vabbe examples

Each folder is a self-contained project that spins up a vabbe lab and configures
it with **community Ansible roles** — driven entirely by `mise`. They double as
end-to-end smoke tests for vabbe.

## The pattern

Every example pins its toolchain in `mise.toml` (Python + uv + the `vabbe` CLI
via the `github:` backend) and exposes the same tasks:

| Task | What it does |
| --- | --- |
| `mise run deps` | create a uv venv, install `ansible-core`, fetch the Galaxy roles |
| `mise run up` | `vabbe up --wait` (nodes booted, sshd ready) |
| `mise run inventory` | `vabbe inventory > inventory.ini` |
| `mise run converge` | `ansible-playbook -i inventory.ini playbook.yml` |
| `mise run verify` | smoke-test the service (via `vabbe exec`, works on macOS too) |
| `mise run down` | `vabbe down` |
| `mise run test` | the whole loop end-to-end |

Run one with:

```
cd examples/nginx
mise trust && mise run test
```

> **Ansible runs from the host**, against the node IPs in `inventory.ini`. That
> works on Linux (host can reach node IPs). On macOS the host can't route to
> container IPs — run Ansible from the in-network `runner` instead and generate
> the inventory with `vabbe inventory --runner`.

## Examples

| Example | Nodes | Role(s) | Shows |
| --- | --- | --- | --- |
| [nginx](nginx/) | 1 | `geerlingguy.nginx` | the minimal end-to-end loop |
| [redis](redis/) | 3 | `geerlingguy.redis` | primary + 2 replicas (replication) |
| [postgres](postgres/) | 2 | `geerlingguy.postgresql` | primary + replica (streaming replication) |
| [kafka](kafka/) | 3 | community Kafka role | 3-node KRaft cluster (advanced, heavier) |
