# nginx example

One vabbe node configured with the [`geerlingguy.nginx`](https://galaxy.ansible.com/ui/standalone/roles/geerlingguy/nginx/)
community role — the smallest end-to-end loop.

## Prerequisites

- Docker (running)
- [mise](https://mise.jdx.dev) — installs everything else (Python, uv, the `vabbe` CLI) pinned in `mise.toml`

## Run

```sh
mise trust      # once, to trust this folder's mise.toml
mise run test   # the whole loop, then tears down
```

Or step by step:

```sh
mise run deps        # uv venv + ansible-core + the Galaxy role
mise run up          # vabbe up --wait (node booted, sshd ready)
mise run inventory   # vabbe inventory > inventory.ini
mise run converge    # ansible-playbook -i inventory.ini playbook.yml
mise run verify      # curl nginx on the node, expect HTTP 200
mise run down        # vabbe down
```

## What it proves

`verify` runs `vabbe exec web -- curl localhost` and checks for **HTTP 200**.

## Notes

- Ansible runs **from your host** against the node IP — works on Linux. On macOS
  the host can't reach container IPs; run Ansible from an in-network runner and
  generate the inventory with `vabbe inventory --runner`.
- Override the node image with `VABBE_NODE_IMAGE` (defaults to the published
  `:v0.1.3`); CI sets it to a locally-built image.
