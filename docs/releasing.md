# Releasing vabbe

## CLI binary — goreleaser, 4 targets

Pure Go + `CGO_ENABLED=0` means all four targets are plain `go build`s:

| GOOS   | GOARCH | target                       |
|--------|--------|------------------------------|
| darwin | amd64  | `releases/vabbe-darwin-amd64`  |
| darwin | arm64  | `releases/vabbe-darwin-arm64`  |
| linux  | amd64  | `releases/vabbe-linux-amd64`   |
| linux  | arm64  | `releases/vabbe-linux-arm64`   |

Local checks:

```
mise run cross        # cross-build into releases/
mise run snapshot     # goreleaser snapshot, no publish
mise run release      # goreleaser release --clean (needs a tag)
```

`.goreleaser.yaml` builds `darwin/linux × amd64/arm64`, sets
`CGO_ENABLED=0`, injects `main.version`, archives as `.tar.gz`, and writes a
`checksums.txt`. `release.prerelease: auto` means `vX.Y.Z-rc.N` creates a
GitHub prerelease, while `vX.Y.Z` creates a normal release.

Release workflows publish from any pushed `v*` tag, including RC tags from a
feature/release branch:

```bash
git tag v0.1.0-rc.1
git push origin v0.1.0-rc.1
```

## Node image — multi-arch to GHCR

Container images are Linux-only. We publish **`linux/amd64` + `linux/arm64`**
to `ghcr.io/nutellinoit/vabbe-node`:

- registry: `ghcr.io/nutellinoit/vabbe-node`
- stable tags (`vX.Y.Z`): `:vX.Y.Z`, `:X.Y.Z`, `:24.04`, `:latest`, and `:sha-<shortsha>`
- prerelease tags (`vX.Y.Z-rc.N`): `:vX.Y.Z-rc.N`, `:X.Y.Z-rc.N`, `:rc`, and `:sha-<shortsha>`
- public package so `docker pull` needs no auth
- CI: `docker/setup-qemu-action` + `docker/setup-buildx-action` +
  `docker/login-action` (to `ghcr.io` with `GITHUB_TOKEN`,
  `packages: write`) + `docker/build-push-action`
  `platforms: linux/amd64,linux/arm64`

The Dockerfile is the embedded one at `cmd/vabbe/image/Dockerfile`.
