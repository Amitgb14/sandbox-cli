# sandbox-cli — Test Plan

This document lists every test case for sandbox-cli and how to run it. It has two
parts:

- **Part 1 — Automated tests** (Go unit + Docker-gated integration). Run these first.
- **Part 2 — Manual / E2E test cases** with step-by-step instructions and expected
  results, for behavior that can't be asserted headlessly (interactive TUIs, login,
  the Claude status line).

Legend: **[A]** = covered by an automated test · **[M]** = manual verification.

---

## Setup / Preconditions

1. Docker Desktop is running (`docker info` succeeds).
2. Go 1.25+ installed.
3. Build the binary:
   ```sh
   make build          # -> bin/sandbox-cli
   ```
4. First run builds the base image (`sandbox-base:<gen>-<hash>`); allow a few minutes once:
   ```sh
   ./bin/sandbox-cli run --build --no-tty --no-metrics -- true
   ```
5. Optional: put a real key in the environment to test agents end-to-end:
   `export ANTHROPIC_API_KEY=...` and/or `export OPENAI_API_KEY=...`.

Cleanup helper (run between manual tests if needed):
```sh
docker ps --filter name=sandbox- -q | xargs -r docker rm -f
```

---

## Part 1 — Automated tests

### How to run

```sh
make test                 # unit tests only (no Docker) — fast, always run in CI
make test-integration     # unit + Docker-gated tests (requires Docker + base image)
gofmt -l .                # must print nothing
go vet ./...              # must be clean
```

### Automated coverage map

| Area | Test | Package |
|---|---|---|
| docker arg construction (the isolation invariant) | `TestBuildArgs_*` | `internal/runtime` |
| env ordering deterministic / passthrough / network | `TestBuildArgs_*` | `internal/runtime` |
| workspace safety refusals (`/`, `$HOME`, ancestor, file, missing) | `TestResolveWorkspace_*`, `TestIsAncestor` | `internal/sandbox` |
| RunSpec build: mounts, env allowlist, image/user override | `TestBuildSpec_*` | `internal/sandbox` |
| config defaults / precedence / relative mounts / discovery | `TestDefault`, `TestLoad_*`, `TestValidate_*`, `TestFindProjectConfig_*` | `internal/config` |
| egress allowlist resolution (root+caps+entrypoint+env, overrides `none`) | `TestBuildSpec_Egress*`, `TestBuildSpec_AllowlistOverridesNetworkNone`, `TestEgressDomains` | `internal/sandbox`, `internal/config` |
| cache volumes: default off, `--cache` adds named volumes, stable names | `TestBuildSpec_CacheVolumes`, `TestCacheVolumeName`, `TestCachePathsAndEnabled`, `TestBuildArgs_VolumeMount` | `internal/sandbox`, `internal/config`, `internal/runtime` |
| credential broker: resolve file/cmd/env, forward by name (not on argv), inject at run time | `TestResolve_*`, `TestBuildSpec_SecretsForwardedByName`, `TestBuildSpec_BadSecretFlag`, `TestInjectSecrets_SetsEnvFromSources`, `TestValidate_Secrets`, `TestLoad_SecretsMergePerKey` | `internal/creds`, `internal/sandbox`, `internal/config` |
| git worktrees: branch-name sanitize, stable namespaced path, create/reuse/list/remove (real git) | `TestSanitizeBranch`, `TestWorktreePath_StableAndNamespaced`, `TestResolveAndList_RealGit`, `TestResolve_NotAGitRepo` | `internal/worktree` |
| ergonomics: `--add-host`, `--host-gateway`, `--git` (safe.directory env + identity forwarded by name) | `TestBuildArgs_AddHost`, `TestBuildSpec_HostGatewayAndAddHosts`, `TestBuildSpec_GitIdentity` | `internal/runtime`, `internal/sandbox` |
| `--runtime` passthrough (kata/gVisor): emitted, off by default, flag overrides config | `TestBuildArgs_Runtime`, `TestBuildSpec_Runtime`, `TestLoad_RuntimeFromConfig` | `internal/runtime`, `internal/sandbox`, `internal/config` |
| unknown-runtime pre-flight hint: parse `docker info` runtimes, friendly actionable error | `TestParseRuntimeNames`, `TestRuntimeHint` | `internal/runtime` |
| **[integration]** egress allowlist drops privileges + blocks non-allowlisted host | `TestEgressAllowlist` | `internal/cli` |
| **[integration]** `--git` forwards host identity + trusts workspace in-container | `TestGitIdentity` | `internal/cli` |
| **[integration]** `--host-gateway` maps host.docker.internal in /etc/hosts | `TestHostGateway` | `internal/cli` |
| **[integration]** `--secret` delivers file/env/cmd sources into the container | `TestSecretDelivery` | `internal/cli` |
| **[integration]** `--cache` non-root write + persistence across --rm runs | `TestCachePersistsAndWritable` | `internal/cli` |
| **[integration]** `--worktree` mounts the branch's checkout at /workspace | `TestWorktreeEndToEnd` | `internal/cli` |
| crash snapshots leave the repo untouched (index, HEAD, branches, tree) and write only `refs/sandbox` | `TestSnapshotLeavesTheRepositoryUntouched` | `internal/rescue` |
| snapshots capture uncommitted + untracked work, skip ignored paths, no-op when unchanged | `TestSnapshotCapturesWorkAndSkipsIgnored`, `TestSnapshotIsANoOpWhenNothingChanged` | `internal/rescue` |
| an agent's `git reset --hard` cannot destroy its own commits (HEAD carried as a snapshot parent) | `TestSnapshotSurvivesAResetHard` | `internal/rescue` |
| restore adds a branch and changes nothing else; `--into-worktree` refuses a dirty tree | `TestRestoreOntoABranchChangesNothingElse`, `TestRestoreIntoWorktreeRefusesADirtyTree` | `internal/rescue` |
| worktree snapshots are visible from the main checkout; retention prunes refs + sessions | `TestSnapshotsFromAWorktreeAreVisibleFromTheMainRepo`, `TestPruneDropsOldSessionsAndTheirRefs` | `internal/rescue` |
| superseded prune: exact tree match only (a partial commit keeps the snapshot), and never a live session | `TestPruneSupersededOnlyWhenTheWorkIsFullyCommitted`, `TestPruneLeavesALiveSessionAlone` | `internal/rescue` |
| repair rebuilds a deleted worktree admin dir without discarding uncommitted work | `TestRepairRebuildsADeletedWorktreeAdminDir` | `internal/rescue` |
| diagnose: only *stale* locks, interrupted merges reported not repaired, quiet when healthy | `TestDiagnoseFindsOnlyStaleLocks`, `TestInterruptedOperationsAreReportedNotRepaired`, `TestDiagnoseIsQuietOnAHealthyRepo` | `internal/rescue` |
| `--dry-run` stays pure (no session, no snapshot ref); safety net on by default | `TestDryRunTakesNoSnapshotAndRecordsNoSession`, `TestSnapshotsAreOnByDefault` | `internal/cli` |
| wrapper arg splitting (claude/codex flag passthrough) | `TestSplitWrapperArgs`, `TestClaudeWrapperParsesWithoutError` | `internal/cli` |
| `--dry-run` golden (asserts `--rm`, fake HOME, no host-home mount) | `TestDryRunInvariants` | `internal/cli` |
| metrics parsing / bar / duration / humanBytes / footer / summary | `TestParseBytes`, `TestParseMemUsage`, `TestBar`, `TestFormatDuration`, `TestHumanBytes`, `TestFooterForwardsOutputIntact`, `TestMeterSummary` | `internal/metrics` |
| branch label: right-aligned, dropped when too narrow, never wraps | `TestLayout`, `TestMeterFormatBranch`, `TestBranch` | `internal/metrics`, `internal/worktree` |
| **[integration]** isolation smoke (HOME=/sandbox/home, /workspace writes reach host) | `TestIsolation_HomeAndWorkspace` | `internal/cli` |
| **[integration]** `rm -rf ~` cannot touch host | `TestRmRfHomeSafety` | `internal/cli` |
| **[integration]** env allowlist forwards / non-allowlisted absent | `TestEnvPassthrough` | `internal/cli` |
| **[integration]** live gauge forwards output + draws | `TestRunWithMetrics_ForwardsOutputAndExit` | `internal/runtime` |
| **[integration]** post-run summary prints peak | `TestRunWithSummary_PrintsPeak` | `internal/runtime` |
| **[integration]** `stats` collector lists sandbox containers | `TestCollectSandboxStats` | `internal/cli` |

**Expected:** all packages report `ok`; `gofmt -l` prints nothing; `go vet` is clean.

---

## Part 2 — Manual / E2E test cases

Each case: **Steps** → **Expected**. Commands assume `./bin/sandbox-cli` (or `sandbox-cli`
if installed via `make install`).

### Group 1 — Build & version

**TC-01 [A/M] Binary builds and reports version**
1. `make build`
2. `./bin/sandbox-cli version`
- Expected: prints `sandbox-cli <ver> (base image: sandbox-base:<gen>-<hash>)`. The
  hash is derived from the embedded Dockerfile, so it changes whenever the image does.

**TC-02 [M] Dockerfile builds a static binary**
1. `docker build --target export --output type=local,dest=./bin-docker .`
2. `file ./bin-docker/sandbox-cli`
- Expected: `ELF 64-bit ... statically linked, stripped`. (Clean up `bin-docker/`.)

**TC-03 [M] Root help lists all commands**
1. `./bin/sandbox-cli --help`
- Expected: shows `run`, `claude`, `codex`, `init`, `config`, `stats`, `version`.

### Group 2 — Core isolation (the security story)

**TC-10 [A] HOME + workspace isolation**
1. `mkdir /tmp/proj && cd /tmp/proj && echo hi > a.txt`
2. `./bin/sandbox-cli run --no-tty --no-metrics -- sh -c 'echo $HOME; ls /workspace; echo x > /workspace/made.txt'`
- Expected: prints `/sandbox/home`; lists `a.txt`; `/tmp/proj/made.txt` now exists on host.

**TC-11 [A] `rm -rf ~` cannot harm the host**
1. Create a canary: `mkdir /tmp/fakehome && echo keep > /tmp/fakehome/canary.txt`
2. `HOME=/tmp/fakehome ./bin/sandbox-cli run --no-tty --no-metrics -p /tmp/proj -- sh -c 'rm -rf ~ || true; rm -rf / 2>/dev/null || true; echo done'`
3. `cat /tmp/fakehome/canary.txt`
- Expected: step 2 prints `done`; canary still says `keep` (host untouched).

**TC-12 [M] Host filesystem is not visible inside**
1. `./bin/sandbox-cli run --no-tty --no-metrics -- sh -c 'ls ~ ; cat /etc/hostname'`
- Expected: `~` is the ephemeral container home (not your Mac home); no host files leak.

### Group 3 — Workspace detection & safety refusals

**TC-20 [A] Refuse to mount filesystem root**
1. `./bin/sandbox-cli run -p / --dry-run -- true`
- Expected: error `refusing to mount filesystem root ...`; nothing runs.

**TC-21 [A] Refuse to mount `$HOME`**
1. `./bin/sandbox-cli run -p "$HOME" --dry-run -- true`
- Expected: error `refusing to mount your home directory ...`.

**TC-22 [A] Refuse an ancestor of `$HOME`**
1. `./bin/sandbox-cli run -p "$(dirname "$HOME")" --dry-run -- true`
- Expected: error `... is an ancestor of your home directory ...`.

**TC-23 [A] Nonexistent / non-directory project**
1. `./bin/sandbox-cli run -p /no/such/path --dry-run -- true`
- Expected: error `project path does not exist ...` (a file path → `... is not a directory`).

### Group 4 — Config

**TC-30 [M] `init` scaffolds `.sandbox.yaml`**
1. `cd /tmp/proj && /path/to/sandbox-cli init`
2. `cat .sandbox.yaml`
- Expected: writes `.sandbox.yaml`; re-running without `--force` errors "already exists".

**TC-31 [M] `config show` reflects merged config**
1. `./bin/sandbox-cli config show`
- Expected: YAML with `user: sandbox`, `workdir: /workspace`, `home: /sandbox/home`, and an
  `image:` of `sandbox-base:<gen>-<hash>`.

**TC-32 [A] Project config overrides defaults; flags override config**
1. In `/tmp/proj/.sandbox.yaml` set `image: my-image:9` and `user: root`.
2. `./bin/sandbox-cli run --dry-run -- true` → image is `my-image:9`, `--user root`.
3. `./bin/sandbox-cli run --image other:1 --dry-run -- true` → image is `other:1` (flag wins).

**TC-33 [A] `config validate` rejects bad values**
1. Set `network: { mode: bogus }` in `.sandbox.yaml`; `./bin/sandbox-cli config validate`
- Expected: non-zero exit, error about `network.mode`.

### Group 5 — run, mounts, env, TTY

**TC-40 [M] `--dry-run` prints the docker command**
1. `./bin/sandbox-cli run --dry-run -- echo hi`
- Expected: a `docker run --init --rm --name sandbox-... -e HOME=/sandbox/home -w /workspace ... echo hi` line; contains no mount of your host home.

**TC-41 [A] Extra mounts (ro/rw)**
1. `./bin/sandbox-cli run --mount /tmp/proj/data:/data:rw --mount /etc/hosts:/h:ro --dry-run -- true`
- Expected: `/data` mount without `readonly`; `/h` mount with `,readonly`.

**TC-42 [A] Env allowlist is default-deny**
1. `FOO=bar SECRET=leak ./bin/sandbox-cli run --env FOO --no-tty --no-metrics -- sh -c 'echo FOO=$FOO SECRET=$SECRET'`
- Expected: prints `FOO=bar SECRET=` (SECRET not forwarded).

**TC-43 [M] TTY auto-detect**
1. Interactive: `./bin/sandbox-cli run -- bash` → you get an interactive shell (`-it`).
2. Piped: `echo | ./bin/sandbox-cli run -- cat` → works without a TTY.

### Group 5b — Egress allowlist & persistent caches

**TC-44 [A] `--allow` renders the firewall wiring**
1. `./bin/sandbox-cli run --dry-run --allow example.com -- npm ci`
- Expected: `--user root`, `--cap-add NET_ADMIN`, `--cap-add NET_RAW`,
  `--entrypoint /usr/local/bin/sandbox-firewall`, and
  `-e SANDBOX_EGRESS_ALLOW=...,example.com` plus `-e SANDBOX_RUN_AS=sandbox`.
  A plain run (no `--allow`) shows none of these and `--user sandbox`.

**TC-45 [M] Egress allowlist blocks non-allowlisted hosts (needs Docker + Linux)**
1. `./bin/sandbox-cli run --allow example.com --no-tty --no-metrics -- sh -c 'whoami; curl -s -m 5 -o /dev/null https://1.1.1.1 && echo REACHABLE || echo BLOCKED'`
- Expected: prints `sandbox` (privileges dropped after firewall setup) then `BLOCKED`.
2. A registry in the baseline still works: `... --allow example.com -- sh -c 'curl -sI -m 10 https://registry.npmjs.org | head -1'` → `HTTP/... 200`.
- Note: also covered by the `TestEgressAllowlist` integration test.

**TC-46 [A] `--cache` mounts named volumes**
1. `./bin/sandbox-cli run --dry-run --cache -- npm ci`
- Expected: `--mount type=volume,source=sandbox-cache-npm,target=/sandbox/home/.npm`
  and the pip/cargo/go/yarn cache volumes; the `/workspace` bind mount is unchanged.
  A plain run has no `type=volume` mounts.

**TC-47 [M] Cache persists across runs and is writable by the non-root user (needs Docker)**
1. `./bin/sandbox-cli run --cache --no-tty --no-metrics -- sh -c 'echo hi > /sandbox/home/.npm/_probe && echo WROTE'` → prints `WROTE` (no permission error).
2. `./bin/sandbox-cli run --cache --no-tty --no-metrics -- sh -c 'cat /sandbox/home/.npm/_probe'` → prints `hi` (survived the `--rm`).
3. Cleanup: `docker volume rm sandbox-cache-npm` (note: this is the shared cache volume).

**TC-48 [A] Brokered secret is forwarded by name, never on the argv, and dry-run does not resolve it**
1. `./bin/sandbox-cli run --dry-run --secret 'TOK=cmd:echo RAN >&2; printf leaked' -- sh -c true`
- Expected: the rendered command contains `-e TOK` (name only); it does **not** contain `leaked`, and `RAN` is **not** printed (dry-run must not execute the command source).
2. `./bin/sandbox-cli run --dry-run --secret 'TOK=file:/etc/hostname' -- sh -c true`
- Expected: `-e TOK` present, file contents absent.

**TC-49 [M] Brokered secret reaches the container at run time (needs Docker)**
1. `printf s3cr3t > /tmp/tok` then `./bin/sandbox-cli run --no-tty --no-metrics --secret 'TOK=file:/tmp/tok' -- sh -c 'echo $TOK'` → prints `s3cr3t`.
2. `SRCV=abc ./bin/sandbox-cli run --no-tty --no-metrics --secret 'TOK=env:SRCV' -- sh -c 'echo $TOK'` → prints `abc`.
- Note: `TC-48` (dry-run leak/exec safety) is also covered by unit tests.

**TC-4A [A/M] Parallel per-branch worktrees**
1. In a git repo: `./bin/sandbox-cli run --worktree feature/x --dry-run -- sh -c true`
- Expected: prints `sandbox-cli: created worktree "feature/x" at <…/worktrees/…/feature-x>`;
  the `--mount` source is that worktree path (not the repo cwd).
2. `./bin/sandbox-cli worktree list` → lists `feature/x` with its path.
3. `./bin/sandbox-cli worktree rm feature/x` → removes it; `list` then shows none.
4. Outside a git repo: `--worktree x` errors with "not a git repository".
- Note: also covered by `TestResolveAndList_RealGit` / `TestResolve_NotAGitRepo`.

**TC-4B [A/M] git / MCP ergonomics**
1. `./bin/sandbox-cli run --dry-run --host-gateway --git --add-host db:10.0.0.5 -- git status`
- Expected: `--add-host host.docker.internal:host-gateway` and `--add-host db:10.0.0.5`;
  `-e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0=*`; and `-e GIT_AUTHOR_NAME`
  … `-e GIT_COMMITTER_EMAIL` (names only). A bare run shows none of these.
2. [M] With Docker + a host git identity: `./bin/sandbox-cli run --git --no-tty --no-metrics -- git -C /workspace config user.email` prints your host email; committing in `/workspace` raises no "dubious ownership" error.

### Group 6 — Agent wrappers (claude / codex)

**TC-50 [A/M] Claude forwards agent flags without a separator**
1. `./bin/sandbox-cli claude --dry-run -- --dangerously-skip-permissions`
   (or run for real: `sandbox-cli claude --dangerously-skip-permissions`)
- Expected: command ends `... claude --dangerously-skip-permissions`; no `unknown flag` error.

**TC-51 [A] Sandbox flags before the agent, agent flags after**
1. `./bin/sandbox-cli claude --project /tmp/proj --dry-run -- --model opus`
- Expected: `/workspace` = `/tmp/proj`; guest command `claude --model opus`.

**TC-52 [A] Colliding short flag goes to the agent**
1. `./bin/sandbox-cli claude --dry-run -- -p "do X"`
- Expected: `-p "do X"` forwarded to claude (NOT interpreted as sandbox `--project`).

**TC-53 [M] Codex wrapper runs codex**
1. `./bin/sandbox-cli codex --dry-run -- exec 'run tests'`
- Expected: command ends `... codex exec 'run tests'`.

### Group 7 — Auth persistence (log in once)

**TC-60 [M] Claude login persists across runs** *(needs a real login)*
1. Clear: `rm -rf ~/.config/sandbox/agents/claude`
2. `sandbox-cli claude` → run `/login`, authenticate.
3. Quit, then `sandbox-cli claude` again.
- Expected: second launch does **not** ask to log in.
- Evidence: `~/.config/sandbox/agents/claude/.claude/.credentials.json` exists and `~/.config/sandbox/agents/claude/.claude.json` contains `oauthAccount`.

**TC-61 [M] Persistence uses a dedicated dir, not host `~/.claude`**
1. Inspect `./bin/sandbox-cli claude --dry-run` mounts.
- Expected: mount is `~/.config/sandbox/agents/claude → /sandbox/home` (whole home); your real host `~/.claude` is never mounted.

**TC-62 [M] `--no-persist-auth` gives a throwaway session**
1. `./bin/sandbox-cli claude --no-persist-auth --dry-run`
- Expected: no `agents/claude` mount present.

### Group 8 — Non-root user

**TC-70 [A/M] Runs as non-root `sandbox` by default**
1. `./bin/sandbox-cli run --no-tty --no-metrics -- sh -c 'whoami; id -u'`
- Expected: `sandbox`, uid `1001`. Files written to `/workspace` still appear on host owned by you (macOS virtualized ownership).

**TC-71 [M] Claude accepts `--dangerously-skip-permissions` (not root)**
1. `sandbox-cli claude --dangerously-skip-permissions` (with a key)
- Expected: does **not** error "cannot be used with root/sudo".

**TC-72 [M] `--user root` opt-in**
1. `./bin/sandbox-cli run --user root --no-tty --no-metrics -- whoami`
- Expected: `root`.

### Group 9 — Metrics: live gauge, summary, stats

**TC-80 [M] Live inline gauge on a non-interactive run**
1. In a real terminal: `./bin/sandbox-cli run --no-tty -- sh -c 'for i in 1 2 3 4 5 6; do echo line $i; sleep 1; done'`
- Expected: a dim status line pinned at the bottom — `⬢ sandbox … / sandbox-cli │ mem …  cpu …  Ns` — output scrolls above it; it disappears at exit.
- Note: not shown if stderr isn't a terminal (piped).
- In a git repo, the branch (`⎇ <branch>`) sits at the right edge; narrow the window until it no longer fits and the label drops rather than wrapping to a second line.

**TC-81 [A] Live gauge is off for interactive TTY**
1. `./bin/sandbox-cli run -- bash` (interactive)
- Expected: no inline gauge overlays the shell (would fight a full-screen TUI).

**TC-82 [A/M] Post-run summary (works for interactive too)**
1. `./bin/sandbox-cli run --no-tty -- sh -c 'sleep 6'` in a terminal.
- Expected: after exit, `sandbox-cli: peak mem … · cpu peak …% · Ns`. Runs <~2s print no summary (too short to sample).

**TC-83 [M] `--no-metrics` disables gauge + summary**
1. `./bin/sandbox-cli run --no-tty --no-metrics -- sh -c 'sleep 6'`
- Expected: no gauge, no summary line.

**TC-84 [M] `sandbox-cli stats` live table**
1. Terminal 1: `./bin/sandbox-cli run --no-tty --no-metrics -- sh -c 'sleep 30'`
2. Terminal 2: `./bin/sandbox-cli stats`
- Expected: a refreshing table with the running `sandbox-*` container's MEM/CPU/PIDS; `Ctrl-C` exits cleanly.

**TC-85 [A/M] `stats --once` snapshot / empty state**
1. With no sandbox containers: `./bin/sandbox-cli stats --once` → "no sandbox containers running…".
2. With one running (see TC-84 step 1): `./bin/sandbox-cli stats --once` → one row for it.

### Group 10 — Claude status line & version currency

**TC-90 [M] Status line renders inside a Claude session** *(needs interactive Claude)*
1. `sandbox-cli claude` (with a key/login).
- Expected: a line at the bottom of the Claude UI: `⬢ sandbox · mem <used>/<total> · cpu <n>%`, updating over time.

**TC-91 [M] `--no-statusline` disables it**
1. `./bin/sandbox-cli claude --no-statusline --dry-run`
- Expected: no `managed-settings.json` mount in the command.

**TC-92 [M] Status line is NOT injected for codex**
1. `./bin/sandbox-cli codex --dry-run`
- Expected: no `managed-settings.json` mount. (Codex has no status-line feature; use `sandbox-cli stats` for its mem/CPU.)

**TC-93 [A/M] Status-line script output (unit-level)**
1. `./bin/sandbox-cli run --no-tty --no-metrics -- sh -c 'echo {} | /usr/local/bin/sandbox-statusline; sleep 1; echo {} | /usr/local/bin/sandbox-statusline'`
- Expected: first line `⬢ sandbox · mem …/…`; second line also includes `· cpu <n>%`.

**TC-94 [M] Claude self-updates via the persisted install**
1. `rm -rf ~/.config/sandbox/agents/claude`
2. `sandbox-cli claude -- --version`
- Expected: reports the current Claude Code version (e.g. `2.1.212`), installed at `~/.config/sandbox/agents/claude/.local/bin/claude`. Second run is instant and stays current.

### Group 11 — Crash recovery

**TC-100 [M] Work survives a killed sandbox**
1. In a git repo: `sandbox-cli claude --worktree crashme --snapshot-interval 20s`
2. Have the agent edit a file and commit; leave a second file uncommitted. Wait ~30s.
3. From another terminal: `kill -9` the `sandbox-cli` process.
4. `sandbox-cli recover list`
- Expected: one row for the run, branch `crashme`, state `crashed`, a non-zero snapshot count.

**TC-101 [M] Restore puts the work on a branch, changing nothing else**
1. From your *normal* checkout (not the worktree): `sandbox-cli recover restore <id>`
2. `git status`, `git branch`
- Expected: a new `sandbox-recover/crashme-<id>` branch holding both the committed and the
  uncommitted work; your current branch, HEAD and working tree are exactly as they were.

**TC-102 [M] Repair a worktree a prune broke**
1. `rm -rf <repo>/.git/worktrees/<name>` (what an in-container `git worktree prune` does).
2. In the worktree: `git status` → `fatal: not a git repository`.
3. `sandbox-cli recover repair --yes`, then `git status`, `git log --oneline -1`.
- Expected: repair names the right branch, git works again on that branch, the agent's commit
  is there, and the uncommitted file is still uncommitted (not staged, not discarded).

**TC-103 [M] Ctrl-C takes a final snapshot**
1. `sandbox-cli run --no-tty -- sh -c 'echo x > /workspace/new.txt; sleep 60'`
2. Press `Ctrl-C`.
- Expected: `sandbox-cli: interrupted — saving a final snapshot of your work…`, exit status 130,
  and `sandbox-cli recover list` shows the session as `interrupted` with a snapshot containing
  `new.txt`.

**TC-104 [A/M] `--no-snapshot` and a non-repo workspace are silent no-ops**
1. `sandbox-cli run --no-snapshot -- true` in a repo; `sandbox-cli run -- true` in a plain directory.
- Expected: both run normally, and `~/.config/sandbox/rescue` gains no session for either.

**TC-105 [M] A snapshot disappears once its work is committed**
1. After TC-100/101, commit only *some* of the recovered files, then `sandbox-cli recover prune`.
- Expected: `nothing to prune` — the rest exists nowhere else, so the snapshot stays.
2. Commit the rest (`git add -A && git commit`), then run any sandbox in that repo.
- Expected: no `recover prune` needed; `sandbox-cli recover list` no longer shows the session,
  and its `refs/sandbox/snapshots/<id>` is gone (`git for-each-ref refs/sandbox`).
3. Repeat with `sandbox-cli recover prune --superseded=false`.
- Expected: the session survives; only the 14-day expiry applies.

---

## Regression sign-off checklist

Run before tagging a release:

- [ ] `gofmt -l .` prints nothing
- [ ] `go vet ./...` clean
- [ ] `make test` — all `ok`
- [ ] `make test-integration` — all `ok` (Docker running)
- [ ] TC-40 dry-run shows `--rm`, fake HOME, and **no host-home mount**
- [ ] TC-11 `rm -rf ~` leaves the host canary intact
- [ ] TC-60 Claude login persists across two launches
- [ ] TC-84 `sandbox-cli stats` shows a live container
- [ ] TC-90 status line visible in a real Claude session
- [ ] TC-94 Claude version matches the host's current version
