# `vabbe.yaml` reference

```yaml
name: <lab-name>            # required; becomes the Docker network name and the vabbe.lab label
network:
  subnet: <CIDR>            # optional; e.g. 10.10.1.0/24. Omit it to let Docker pick a free
                            # subnet automatically (no collisions across parallel labs — good
                            # for CI). With a subnet, static node ips must fall inside it.
defaults:                   # optional, applied to every node before the node's own fields
  image: <image>
  privileged: <bool>
  dns: [<ip>...]            # optional; node resolv.conf upstreams (default [1.1.1.1, 1.0.0.1])
  runtime: <name>          # optional; OCI runtime, e.g. kata (default: Docker's, runc). See docs/kata.md
  cpus: <number>           # optional; default vCPUs for every node
  memory: <size>           # optional; default RAM for every node (e.g. 4g)
nodes:                      # at least one
  - name: <node-name>       # required; becomes the container hostname unless `hostname` is set
    ip: <ipv4>              # optional; static IP (needs network.subnet). Omit for a Docker-
                            # assigned IP — discover it with `vabbe ip`/`inventory`/`dns`/`ls`.
    image: <image>          # optional; falls back to defaults.image, then the vabbe default
    privileged: <bool>      # optional; default true for runc, false for VM runtimes (kata)
    dns: [<ip>...]          # optional; overrides defaults.dns for this node
    runtime: <name>        # optional; overrides defaults.runtime (e.g. kata; "" = runc)
    cpus: <number>         # optional; vCPUs (e.g. 2, 1.5). Maps to Docker --cpus.
    memory: <size>         # optional; RAM, e.g. 512m, 4g. Maps to Docker --memory.
    entrypoint: [<str>...]  # optional; overrides the image's ENTRYPOINT (runner-friendly)
    cmd: [<str>...]         # optional; overrides the image's CMD
    mounts: [<bind>...]     # optional; `host:container[:ro]`
    env: <map>              # optional; `${VAR}` expanded from host env at load time
    caps: [<cap>...]        # optional; e.g. [NET_ADMIN, NET_RAW] (ignored when privileged)
    ports: [<spec>...]      # optional; publish to the host. Docker `-p` syntax:
                            #   "80"                    host 80  -> node 80/tcp
                            #   "8080:80"               host 8080-> node 80/tcp
                            #   "8080:80/udp"           protocol udp (tcp|udp|sctp)
                            #   "127.0.0.1:6443:6443"   bind only localhost
                            # The only macOS host-reachable path (Docker Desktop can't route node IPs).
    hostname: <name>        # optional; defaults to `name`
    runner: <bool>          # optional; marks this as the `vabbe shell` target. Implies "not a VM":
                            # no systemd assumptions, no forced privileged, no /lib/modules bind.
```

## Defaults vabbe applies automatically

- `privileged: true` for `runc` VM nodes; **`false` for VM-runtime nodes** (Kata
  etc. — they have their own kernel and `privileged: true` breaks Kata device
  setup). See `docs/kata.md`.
- For a **VM-runtime node** (e.g. `runtime: kata`), systemd still boots as PID1,
  but vabbe adds `CAP_SYS_ADMIN` and a shim cmd that remounts `/sys/fs/cgroup` rw
  first (Kata mounts it read-only, which systemd-as-init can't use). `vabbe
  exec`/`shell`/`ssh` then go over real SSH, not `docker exec`. See `docs/kata.md`.
- tmpfs on `/run`, `/run/lock`, `/tmp`.
- `StopSignal: SIGRTMIN+3` (clean systemd shutdown).
- Node `/etc/resolv.conf` is rewritten at boot to the `dns:` upstreams (default
  `1.1.1.1`, `1.0.0.1`), replacing Docker's embedded `127.0.0.11` — see
  `docs/gotchas.md`. Runners keep Docker's resolver. Set `dns:` for an
  internal/corporate resolver.
- `restart: unless-stopped`.
- `Hostname` = node name (unless overridden).
- VM nodes bind-mount `/lib/modules:/lib/modules:ro` (so `modprobe` finds `modules.builtin`).
- The lab keypair is bind-mounted into **every** node (runners included):
  public key to `/root/.ssh/authorized_keys:ro`, private key to
  `/root/.ssh/id_ed25519:ro` — so any node, including the runner, can SSH its
  peers with no extra `mounts:`.

## `${VAR}` expansion

Both at the **whole-YAML** level (anywhere a `${VAR}` appears) and at the node
**`env:`** map, `vabbe` expands `${VAR}` (and `$VAR`) from the host environment
at config load. Unset variables are left as-is.

## Runner nodes

`runner: true` is sugar: it marks the node `vabbe shell` drops you into by
default, and signals that the node is not a systemd VM (e.g. an Alpine/mise
image with a custom entrypoint). vabbe doesn't force the `/lib/modules` bind on
runners — set that yourself if you want it. The lab keypair **is** still injected
(see above), so a runner can `ssh`/`furyctl`/`ansible` its peers out of the box;
you do **not** need to mount `id_ed25519` by hand.