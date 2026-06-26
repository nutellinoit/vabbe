# vabbe docs

Advanced material that doesn't belong in the top-level [README](../README.md).

| Doc | What it's for |
| --- | --- |
| [config-reference.md](config-reference.md) | Full annotated `vabbe.yaml` schema — every field, defaults, and what each one maps to. |
| [gotchas.md](gotchas.md) | The expensive lessons (swap, kernel modules, systemd, SSH key ownership…) encoded as defaults and `doctor` warnings. Read this when something "should work" but doesn't. |
| [macos.md](macos.md) | Docker Desktop specifics, chiefly: the Mac host can't reach container IPs — run your driver as an in-network `runner` node. |
| [node-image.md](node-image.md) | The `vabbe-node` VM base image: what's inside, how to build it locally, and how it's published. |
| [prior-art.md](prior-art.md) | What we borrowed from `kind` and `containerlab`, and where vabbe deliberately diverges. |
| [releasing.md](releasing.md) | How CLI binaries (goreleaser) and the node image (GHCR) get built and published on tags. |
| [images/](images/) | Static assets used by the docs and the README (e.g. the logo). |
