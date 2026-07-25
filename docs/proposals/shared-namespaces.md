# Proposal: addressing the shared directory by project and branch

**Status:** proposed, not implemented.
**Depends on:** `--share` (shipped), `worktree.RepoID`/`SanitizeName` (shipped).
**Relates to:** [`pinned-contracts.md`](pinned-contracts.md) (the layer above this one),
[`multi-agent-fleet.md`](multi-agent-fleet.md) (the thing that makes this urgent).
**Code required for v1:** ~40 lines in `internal/cli` + `internal/config`, no new
mounts in the default mode.

## Problem

`--share` mounts one host directory — `~/.config/sandbox/shared`
(`internal/config/load.go:87`) — at `/shared` in every sandbox that asks for it
(`internal/cli/root.go:182-199`). It is deliberately the only mount not derived
from the project: that is what makes it a channel rather than a mount.

One flat read-write directory is right for two agents and wrong for five. With
`--worktree` shipped and a fleet proposed, the expected steady state is N agents
running at once, and they all see the same `/shared`:

- **Write collisions.** Every agent's obvious filename is the same filename. Two
  agents asked to "write the API contract to `/shared/openapi.yaml`" produce one
  file and no error.
- **No addressability.** There is no way to say "the contract *from the
  `feature-login` agent*". The only namespace is one the human invents in a
  prompt and every agent has to be told about, consistently, every time.
- **No provenance.** A file in `/shared` does not say which repo, branch, or run
  produced it. After the fact, nobody can tell.

Note what is *not* on that list: access. Any sandbox with `--share` can already
read anything any other sandbox wrote there. The missing piece is naming, not
reach.

## The thing to get right first

The natural fix — "give each sandbox `/shared/<project>/<branch>`" — is right
about the layout and can be badly wrong about the mount. If each sandbox mounts
*only its own* `<project>/<branch>` directory, the cross-read that motivated the
whole feature is gone: B cannot read A's artifacts, because A's directory is not
in B's mount namespace at all.

So the rule this design is built on:

> **The mount stays the root. The namespace lives inside it.**

Namespacing buys collision-freedom, addressability and provenance. Mounting the
root is what buys access. They are independent, and only the first one needs to
change.

## Design

### Layout

```
~/.config/sandbox/shared/                 →  /shared         (the mount, unchanged)
├── README.md                             (seeded today; still yours to edit)
├── <ad-hoc files>                        (today's flat scratch; keeps working)
└── projects/
    ├── README.md                         (seeded: explains the layout)
    └── web-ui-3f9a1c2b/                  worktree.RepoID
        ├── main/
        │   └── openapi.yaml
        └── feature-login/                worktree.SanitizeName(branch)
            └── notes.md
```

`--share` creates the sandbox's own leaf directory before launch and tells it
where that is. Nothing else about the mount changes in the default mode.

### The key

| Component | Source | Why this one |
|---|---|---|
| `projects/` prefix | constant | Keeps the structured area out of the flat area, so today's ad-hoc files can never be mistaken for a project and `share clean` has exactly one directory it is allowed to delete. |
| repo | `worktree.RepoID` (`internal/worktree/worktree.go:376`) | Already the identity behind container names and the `sandbox.repo` label. Follows a linked worktree back to its main repo, so every branch of one repo groups under one key. Two clones of a same-named repo stay distinct (basename + 8 hex of the path hash). |
| branch | `worktree.SanitizeName` (`:400`) of `opts.Branch` (`internal/cli/root.go:167`) | The same sanitizer that names worktree paths and detached container names, so `sandbox-web-ui-3f9a1c2b-feature-login` (the container), `refs/sandbox/...` (the snapshot) and `/shared/projects/web-ui-3f9a1c2b/feature-login` (the artifacts) agree by construction rather than by coincidence. |

Fallbacks, so the path always exists:

- **Non-git project** — no `RepoID`. Key on the project directory instead:
  `SanitizeName(basename)` + `-` + 8 hex of the sha256 of its absolute path,
  i.e. the same shape `repoID` already produces (`:367`).
- **Detached HEAD** — `worktree.Branch` already returns the short sha (`:292`),
  which is a fine directory name.
- **No branch at all** (non-repo) — `_none`.

Two properties worth stating plainly in the docs, because both are consequences
rather than bugs:

- **The branch is resolved once, at launch.** If the agent runs `git checkout -b`
  mid-session, its share directory does not follow. The directory is named for
  the branch the run *started* on, and that is what makes it a stable address for
  the other side.
- **Two runs on one repo+branch share one directory.** Same key, same rule as
  "one agent per worktree" — and for detached runs docker's name uniqueness
  already refuses the second one.

### Mount modes

`--share` gains an optional value (`NoOptDefVal: "rw"`, so bare `--share` keeps
working and `splitWrapperArgs` (`internal/cli/run.go:60-88`) already handles both
`--share` and `--share=ro` in the agent wrappers).

| Mode | Mounts | Meaning |
|---|---|---|
| `--share` / `--share=rw` | root `rw` at `/shared` — **unchanged from today** | Everything shared, everyone can write. Ad-hoc, and still the right default. |
| `--share=ro` | root `ro` at `/shared`, **plus** own leaf `rw` at `/shared/projects/<repo>/<branch>` | You may write your own directory and read everyone else's. Ownership becomes a property of the system instead of a sentence in a prompt. |

`ro` is the mode that answers the open question `pinned-contracts.md` closes on
("Ownership enforcement … `--share` is a single boolean today"). It costs one
extra bind of a subdirectory of a directory that is already mounted — which
grants no reach the container did not have a moment earlier, the same argument
`root.go:147-160` already makes for double-mounting a worktree.

The nested bind (a `rw` child inside a `ro` parent) relies on the daemon sorting
mountpoints by destination so the parent lands first. That is long-standing
docker behavior, but it is behavior this repo does not currently depend on, so it
gets an integration test rather than an assumption.

### Discoverability

A bind mount is invisible to an agent nobody told about it — the same reasoning
that already seeds `README.md` (`internal/cli/share.go:14-53`). Three cheap
channels, in increasing order of how likely an agent is to notice:

1. **Env, always set when `--share` is on** — `SANDBOX_SHARE=/shared` and
   `SANDBOX_SHARE_SELF=/shared/projects/<repo>/<branch>`. Agents read their
   environment; this is the one an autonomous run will actually find.
2. **Printed at launch**, extending the existing `sandbox-cli: sharing … at …`
   line with the leaf path, so the human can paste the address into the *other*
   agent's prompt.
3. **`projects/README.md`**, seeded on first use. Deliberately a *new* file
   rather than an edit to the existing `README.md`: `seedSharedReadme` never
   clobbers, so anyone who ran `--share` before this change would otherwise never
   see the layout described.

Plus one host-side helper, because the repo-id hash is not something a human
should have to derive:

```sh
sandbox-cli share path                     # this project, this branch → host + container path
sandbox-cli share path --branch feature-a  # a sibling branch's address, to paste into a prompt
sandbox-cli share ls                       # what exists, with sizes and mtimes
```

### Lifecycle

Leaf directories accumulate: one per repo × branch ever run with `--share`. That
is slow-growing and small, but unbounded, so `share ls` ships with a way to act
on what it prints — `share rm <repo>/<branch>` and `share clean --older-than 30d`,
scoped so they can only ever delete under `projects/`.

Deliberately **not** wired to `worktree rm`: the handoff artifact outliving the
worktree that produced it is usually the point.

## Isolation review

- **Default mode adds no mount.** The mount set for `--share` is byte-identical
  to today's; the only new host-side action is one `MkdirAll` under a directory
  the CLI already creates.
- **`ro` mode adds one bind** of a subdirectory of the already-mounted root, and
  *narrows* the default by making the rest of the tree read-only.
- **`ResolveWorkspace` and `BuildArgs` are untouched.** No change to what may be
  mounted, to `HOME`, or to the host-home refusals.
- **One genuinely new leak, and it is small.** Auto-created directories mean a
  sandbox for project Y can see the *names* of the repos and branches you work on
  elsewhere, where today it only sees what someone deliberately put in `/shared`.
  Names only — the contents are whatever agents wrote, which was already shared.
  If that ever matters, the answer is a third mode that mounts only the leaf
  (see Open questions), not a change to these two.
- **Pre-existing, worth confirming while here:** the shared dir is created `0700`
  and the container runs as `sandbox`, whose uid the image lets `useradd` pick
  (`internal/image/assets/Dockerfile:286`). On native Linux, if that uid differs
  from the host uid, `/shared` is unwritable — the same rough edge already
  documented for `/workspace` ownership (`README.md:658`). macOS Docker Desktop
  virtualizes it away. The `ro`-mode integration test should assert a write to
  the leaf actually succeeds on Linux CI.

## Usage

```sh
# producer — writes to its own leaf, readable by anyone
sandbox-cli claude --share=ro --worktree feature-login
#   sandbox-cli: sharing ~/.config/sandbox/shared at /shared (read-only)
#   sandbox-cli: your directory: /shared/projects/web-ui-3f9a1c2b/feature-login

# > "write the API contract to $SANDBOX_SHARE_SELF/openapi.yaml"

# consumer — a different project, reading that branch by name
sandbox-cli share path --project ~/web-ui --branch feature-login
#   /shared/projects/web-ui-3f9a1c2b/feature-login

sandbox-cli claude --share=ro --project ~/backend
# > "read /shared/projects/web-ui-3f9a1c2b/feature-login/openapi.yaml and implement it"
```

This proposal stops at naming. Versioning, atomicity and "which contract was this
built against?" are [`pinned-contracts.md`](pinned-contracts.md), which layers on
top unchanged — a bare repo inside `projects/<repo>/` instead of at the root.

## Verification

- `make test` — key derivation (repo + branch, linked worktree resolving to the
  main repo's id, detached HEAD, non-git project, empty branch); leaf created
  `0700`; `SANDBOX_SHARE_SELF` set in `rw` and `ro` alike; `--share=ro` producing
  exactly two mounts with the leaf `rw` and the root `ro`; the existing
  `TestShareOffByDefault` and `TestShareDoesNotClobberReadme` unchanged and still
  passing; `--share=ro` split correctly by `splitWrapperArgs` in a wrapper.
- The `--dry-run` golden (`internal/cli/dryrun_test.go`) gains a `--share=ro`
  case; the existing cases must not move.
- `make test-integration` — the one thing unit tests cannot answer: with the root
  mounted `ro` and the leaf `rw`, a write to the leaf succeeds and a write to
  `/shared/other` fails.

## Open questions

- **Is the hash in the path acceptable?** `web-ui-3f9a1c2b` is unambiguous and
  consistent with container names and labels, but it is not something a human
  types. `share path` and `$SANDBOX_SHARE_SELF` cover the two sides that matter;
  a `by-name/<basename> → ../projects/<repo-id>` symlink, created only when the
  basename is unambiguous, would cover the third. Probably not worth the moving
  part.
- **Should `ro` become the default later?** It is the safer posture and it breaks
  today's "drop a file at `/shared/x`" flow. If it does, it should be a major
  version, not a quiet change.
- **A third mode, `--share=own`** — mount *only* the leaf, so N fleet agents get
  a persistent outbox the human collects from without seeing each other. Real,
  but no user has asked; leave it until a fleet does.
- **`--share-from <repo>:<branch>`** — explicit consumption, mounting one peer
  leaf read-only. Only needed if `own` exists; ambient `ro` covers the case
  today.
- **`_none` collides** with a branch literally named `_none`. Theoretical; note
  it rather than encode around it.

## Not chosen

- **Replace the root mount with the per-branch leaf.** The obvious reading of
  "make it `/shared/project/<branch>`", and it silently removes the cross-sandbox
  read that is the entire point of `--share`.
- **Key on the branch alone** (`/shared/<branch>`). Shorter, and wrong the first
  time two repos both have a `main`.
- **Key on the run** (a timestamp or container id per launch). Perfect isolation,
  useless as an address: the consumer cannot name a directory that did not exist
  when its prompt was written.
- **Mount peer directories explicitly by flag, with nothing ambient.** Tighter,
  but it makes the consumer declare its producers before either agent has run,
  which is exactly the coordination the flat directory was avoiding.
- **Notify / lock / watch when a leaf changes.** `pinned-contracts.md` settles
  this: a handoff that can hang is worse than one that is merely stale.
