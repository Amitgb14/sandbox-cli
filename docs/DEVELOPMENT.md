# Development

Everything you need to build, test, install, and release `sandbox-cli` locally.

## Prerequisites

- **Go 1.25+** — the module targets 1.25 (`go.mod`).
- **Docker** — only for `make test-integration`, `make docker-build`, and `make image`.
  A running daemon is required; the base image is built automatically on first use.
- **GoReleaser** (release engineering only) — `go install github.com/goreleaser/goreleaser/v2@latest`.

Standard library + `cobra` + `yaml.v3` only — no other runtime dependencies.

## Getting the source

```sh
git clone https://github.com/Amitgb14/sandbox-cli.git
cd sandbox-cli
```

To *use* the tool rather than work on it, the one-line installer in
[README.md](../README.md#install) is quicker — it needs no Go toolchain.

## Everyday workflow

```sh
make build              # -> bin/sandbox-cli (embeds version via -ldflags)
make test               # unit tests, no Docker required
make test-integration   # end-to-end tests; requires a running Docker daemon
make fmt                # gofmt -w .
make clean              # rm -rf bin dist bin-docker
```

The version string is derived from `git describe --tags --always` and injected at
build time into `internal/version.Version`. A dirty/untagged tree builds fine and
reports something like `0.0.1beta.6-26-g97f461a`.

### Running a single test

```sh
go test ./internal/runtime -run TestBuildArgs
go test -tags docker_integration -run TestClaude ./internal/cli   # a single integration test
```

Integration tests are gated behind the `docker_integration` build tag, so plain
`go test ./...` (`make test`) never touches Docker.

## Installing locally

```sh
make install            # go install ./cmd/sandbox-cli  ->  $GOBIN (or $GOPATH/bin)
```

`make install` drops the binary in `$GOBIN` (falling back to `$GOPATH/bin`, e.g.
`~/go/bin`). Make sure that directory is on your `PATH`, and ahead of any older
copy of `sandbox-cli`.

### macOS gotchas

- **PATH shadowing.** If an older `sandbox-cli` lives in a directory that precedes
  `~/go/bin` on your `PATH` (a common one is `~/.local/bin`), `make install` won't
  appear to take effect — the shell keeps resolving the older binary. Check with
  `which sandbox-cli` and compare `sandbox-cli version` against your build. Fix by
  either putting `~/go/bin` first on `PATH`, or copying the new binary over the
  shadowing one (see next point).

- **Re-sign after copying.** Copying a freshly built Go binary on macOS invalidates
  its code signature, and the kernel kills it on launch with exit code 137 (SIGKILL).
  Re-sign ad-hoc after any `cp`:

  ```sh
  cp ~/go/bin/sandbox-cli ~/.local/bin/sandbox-cli
  codesign -s - -f ~/.local/bin/sandbox-cli
  ```

## Release engineering

Releases are built by GoReleaser (`.goreleaser.yaml`) and normally published by CI
when a version tag is pushed — see `.github/workflows/release.yml`.

```sh
make snapshot           # dry-run: full matrix, archives, checksums, Homebrew cask into ./dist (no publish)
make release            # publish; needs goreleaser, GITHUB_TOKEN, and a pushed tag
```

You rarely run `make release` by hand. The normal flow is:

```sh
git tag 0.0.1beta.1 && git push origin 0.0.1beta.1   # CI runs the release
```

> **Never change the release version as a side effect of unrelated work.** The
> current version lives in `internal/version`.

### Building images

```sh
make docker-build       # one binary for this machine, built in Docker -> bin/sandbox-cli
make image              # multi-arch runnable image (requires buildx)
```

## Invariants to keep honest

Isolation lives in one pure function, `runtime.BuildArgs`, plus
`sandbox.ResolveWorkspace`. Any change that could affect what the container can
reach must keep these tests honest — update the golden output intentionally, never
just to make a test pass:

- `internal/runtime/args_test.go`
- the `--dry-run` golden test in `internal/cli/dryrun_test.go`

`internal/rescue` has an invariant of its own: it only ever *creates* git objects
and refs under `refs/sandbox/`, never writing `HEAD`, a branch, the repository
index, or a file in the working tree. It runs automatically against every user's
repository while an agent is loose in it, so that restraint is the whole licence
to be there. `TestSnapshotLeavesTheRepositoryUntouched` pins it byte-for-byte;
see `docs/proposals/crash-recovery.md` for the reasoning.

See `CLAUDE.md` for the full architecture notes and `TESTING.md` for the test
strategy.
