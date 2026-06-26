# Releasing vabbe

> Work-in-progress. The CI workflows (`release.yml`, `image.yml`) are not in
> this slice; this file documents the intended path so we wire it next.

## CLI binary — goreleaser, 4 targets

Pure Go + `CGO_ENABLED=0` means all four targets are plain `go build`s:

| GOOS   | GOARCH | target                       |
|--------|--------|------------------------------|
| darwin | amd64  | `releases/vabbe-darwin-amd64`  |
| darwin | arm64  | `releases/vabbe-darwin-arm64`  |
| linux  | amd64  | `releases/vabbe-linux-amd64`   |
| linux  | arm64  | `releases/vabbe-linux-arm64`   |

Local snapshot:

```
mise run cross        # cross-build into releases/
mise run release      # goreleaser release --clean (needs a tag)
```

The `.goreleaser.yaml` (to be added) sets `builds.goos: [darwin, linux]`,
`builds.goarch: [amd64, arm64]`, `env: [CGO_ENABLED=0]`, archives as `.tar.gz`
with a `checksums.txt`.

## Node image — multi-arch to GHCR

Container images are Linux-only. We publish **`linux/amd64` + `linux/arm64`**
to `ghcr.io/nutellinoit/vabbe-node`:

- registry: `ghcr.io/nutellinoit/vabbe-node`
- tags: `:24.04`, `:latest`, and the git SHA/tag
- public package so `docker pull` needs no auth
- CI: `docker/setup-qemu-action` + `docker/setup-buildx-action` +
  `docker/login-action` (to `ghcr.io` with `GITHUB_TOKEN`,
  `packages: write`) + `docker/build-push-action`
  `platforms: linux/amd64,linux/arm64`

The Dockerfile is the embedded one at `cmd/vabbe/image/Dockerfile`.
