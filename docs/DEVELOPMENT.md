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
[the install page](install.md) is quicker — it needs no Go toolchain.

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

## Verifying Studio before you push

Studio is the one part of this repository whose pieces are published rather than
compiled by the person running them — two container images, two binaries in the
release archive, and a script that assembles them on someone else's machine. So
"it works here" is a weaker claim than usual: the thing users get is built by CI
from the same tree, and most of what can go wrong is a disagreement between the
halves rather than a compile error in either.

Run this before pushing anything under `studio/`, `cmd/sandbox-studio-api/`,
`studio.sh` or `.github/workflows/images.yml`. From the repository root:

```sh
BIN=$(mktemp -d)          # scratch, so nothing here touches ~/.local/bin
```

**1. Static checks.** Most breakage is caught here, in seconds.

```sh
gofmt -l . && go vet ./... && go build ./... && go test ./...
go test -race ./internal/sandbox/ ./internal/egressproxy/ ./internal/studioapi/ ./internal/fleet/
sh -n studio.sh && sh -n install.sh
(cd studio && npx tsc --noEmit && npm run lint)
(cd web    && npx tsc --noEmit && npm run lint)
```

The second line is not optional and is easy to skip, because everything it
catches passes without it. CI runs the race detector over the four packages that
start goroutines, and a plain `go test` over those same packages is green while
holding a genuine race — a background loop reading a package-level variable that
a test restores, say. Run it before pushing anything that starts a goroutine.

**2. Build what CI will build.** The `:local` tag is what step 3 pulls instead of
GHCR.

```sh
docker build -f studio/Dockerfile     -t ghcr.io/amitgb14/sandbox-studio-ui:local  studio
docker build -f Dockerfile.studio-api -t ghcr.io/amitgb14/sandbox-studio-api:local .
go build -o "$BIN/sandbox-cli" ./cmd/sandbox-cli
go build -o "$BIN/sandbox-studio-api" ./cmd/sandbox-studio-api
```

**3. Start the pair on non-default ports**, so it cannot collide with a compose
stack or a Studio you already have up. `--config` is needed for *this*
repository specifically: its own `.sandbox.yaml` carries `env` and `secrets`,
which discovery refuses from a project file.

```sh
sh studio.sh up --no-install --no-pull --dest "$BIN" --tag local \
  --port 3199 --api-port 8799 --config "$PWD/studio.sandbox.yaml"
```

**4. Assert it wired up**, rather than merely started. Each line here has failed
at least once in a way the startup output did not show:

```sh
TOKEN=$(cat ~/.config/sandbox/studio/token)

# the image was told its port at run time — the whole runtime-config mechanism
curl -fsS http://localhost:3199 | grep -o 'window.__SANDBOX_API__=[^;]*;'

curl -fsS http://127.0.0.1:8799/v1/health                                   # 200 + JSON
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8799/v1/worktrees # 401
curl -s -o /dev/null -w '%{http_code}\n' -H 'Origin: http://evil.example' \
     http://127.0.0.1:8799/v1/health                                        # 403
curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Origin: http://localhost:3199' \
     http://127.0.0.1:8799/v1/worktrees | head -c 120                       # your worktrees
```

Then open `http://localhost:3199`: the header badge reads **live** on first
load, with nothing pasted into a settings field.

The first `curl` is the one worth understanding. `studio/src/app/layout.tsx` is
`force-dynamic` on purpose — prerendered, the layout reads `process.env` once at
image build time, the script tag is simply absent at runtime, and every screen
quietly falls back to the baked default. Nothing else in the output changes, so
this assertion is the only thing standing between that and a shipped image.

**5. The project is the repository root, not your working directory.**

```sh
sh studio.sh down
(cd studio && sh ../studio.sh up --no-install --no-pull --dest "$BIN" --tag local \
   --port 3199 --api-port 8799 --config "$PWD/../studio.sandbox.yaml")
```

It must report `project: …/sandbox-cli (repository root of …/studio)`. Handed a
subdirectory, the API answers every branch-addressed request with *not a git
repository … Stopping at filesystem boundary*, once per request, naming nothing
that would explain it — which is the same failure `SANDBOX_PROJECT` exists to
prevent on the compose route.

**6. The other path, then teardown.**

```sh
sh studio.sh down
sh studio.sh up --no-install --no-pull --tag local --port 3199 --api-port 8799 \
  --api-in-docker --config "$PWD/studio.sandbox.yaml"   # expect the socket warning
sh studio.sh status --port 3199 --api-port 8799
sh studio.sh down && sh studio.sh down                  # second: "nothing was running"
```

**7. What steps 1–6 cannot reach.** These need tooling or a published release,
and are worth running when you have touched the thing each one covers:

```sh
# multi-arch, as the workflow builds it (no push; ~5 min under QEMU)
docker buildx build --platform linux/amd64,linux/arm64 -f studio/Dockerfile studio

# the archive really carries both binaries
goreleaser check && goreleaser release --snapshot --clean --skip=validate
tar -tzf dist/sandbox-cli_*_darwin_arm64.tar.gz

# install.sh against a release that predates the second binary: a message, not a failure
sh install.sh --with-studio-api --dest "$BIN/probe" --no-config
```

**8. Cleanup.**

```sh
docker rmi ghcr.io/amitgb14/sandbox-studio-{ui,api}:local
rm -rf "$BIN" dist
```

A note on `studio/e2e`: `npm run test:e2e` currently has failures that are not
yours. Several specs (`wtcols`, `copy`, `headless`, `nocontoken`, `tabswitch`,
`ttylogs`, `dbg`) were written against a live daemon holding particular runs and
branches, and fail without it. Before blaming a change, stash it and re-run the
same specs — an unchanged failure list is the answer.

### Publishing

Images go to GHCR from `.github/workflows/images.yml`: `edge` on every push to
`main`, and the version plus `latest` on a tag. **A newly created GHCR package is
private**, so the first publish needs its visibility set to public by hand
(GitHub → Packages → the package → Package settings → Change visibility) or
`studio.sh` fails on `docker pull` with `denied` for everyone but you.

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
