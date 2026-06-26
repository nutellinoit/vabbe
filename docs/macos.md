# macOS (Docker Desktop)

## The big one: the Mac host can't reach container IPs

On macOS, Docker runs inside a Linux VM (the "Docker Desktop Linux VM"). The
lab's user-defined bridge and its `10.x` IPs live inside that VM — **the Mac
host cannot route to them**. There is no fix; this is how Docker Desktop works.

What you do instead in `vabbe`:
- Run your driver (ansible/furyctl/kubectl) as a **runner node** inside the
  lab, not on the host. Container↔container traffic stays inside the Linux VM
  and is identical to Linux-host behaviour.
- `vabbe ssh`, `vabbe exec`, `vabbe shell` use `docker exec` (the Docker
  socket crosses the VM boundary). They work on both OSes.
- To expose a service to the Mac (a dashboard, the API server), use
  **published ports** (`ports: ["8080:80"]` on the node) — the only thing that
  crosses the VM boundary.

## Apple Silicon

Don't force `--platform`. Let Docker pick native (arm64).

For amd64-only images there are two paths on Docker Desktop:
- **Apple Virtualization framework** backend + the **Rosetta** toggle:
  amd64 runs under Rosetta (fast, broadly compatible).
- **Docker VMM** backend: linux/arm64 only. amd64 images give
  `exec format error`.

`vabbe doctor` prints the daemon OS/arch so you can spot the mistake. If `up`
errors with `exec format error`, your `image:` is amd64-only and you need to
switch backends or enable Rosetta.

## `vabbe host-prep` on Docker Desktop

You can't `ssh` into the Desktop Linux VM. host-prep uses the
`justincormack/nsenter1` image with `--pid=host --privileged` to enter the
VM's PID 1 namespace and run `swapoff -a` (and `modprobe`) on the VM kernel.
This is the one cross-VM contingency vabbe performs on the host.