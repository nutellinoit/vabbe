# vabbe docs

Advanced material that doesn't belong in the top-level [README](../README.md).

| Doc | What it's for |
| --- | --- |
| [config-reference.md](config-reference.md) | Full annotated `vabbe.yaml` schema — every field, defaults, and what each one maps to. |
| [gotchas.md](gotchas.md) | The expensive lessons (swap, kernel modules, systemd, SSH key ownership…) encoded as defaults and `doctor` warnings. Read this when something "should work" but doesn't. |
| [host-prep.md](host-prep.md) | Why `host-prep` exists: containers share one kernel, so Kubernetes' kernel-global prereqs (swap off, modules) are arranged once on the host. |
| [kata.md](kata.md) | Run nodes under an alternative OCI runtime (Kata Containers = real per-node kernel/VM) via the `runtime:` field; install/register Kata with Docker. |
| [node-profiles.md](node-profiles.md) | Design note: what it takes to make a node behave like a VM per runtime (the requirements catalog) + a proposed `profile:` field (vm/runner/raw). |
| [kata-custom-kernel.md](kata-custom-kernel.md) | Build a k8s-ready Kata guest kernel (closes class-B gaps: Cilium `xt_socket`/L7, Longhorn iSCSI, Ceph rbd…). The `CONFIG_*` fragment + build recipe for Ansible. |
| [macos.md](macos.md) | Docker Desktop specifics, chiefly: the Mac host can't reach container IPs — run your driver as an in-network `runner` node. |
| [node-image.md](node-image.md) | The `vabbe-node` VM base image: what's inside, how to build it locally, and how it's published. |
| [releasing.md](releasing.md) | How CLI binaries (goreleaser) and the node image (GHCR) get built and published on tags. |
| [images/](images/) | Static assets used by the docs and the README (e.g. the logo). |
