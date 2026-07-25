# Proposal: pinned contracts between sandboxes

**Status:** proposed, not implemented. Targets a future release.
**Depends on:** `--share` (shipped).
**Code required for v1:** none — convention and documentation only.

## Problem

`--share` gives two sandboxes one directory in common, which is enough to move a
file. It is not enough to hand over an *artifact one side depends on*.

The motivating case: a UI agent finishes an API contract; a backend agent in a
different project has to implement it. `--share` transports the bytes and stops
there, leaving every coordination question open:

- **Notification.** The consumer reads whatever is on disk when it happens to
  look. Nothing tells it the file arrived, or changed.
- **Atomicity.** A large write can be read half-finished.
- **Staleness.** There is no way to answer "which contract was this built
  against?" after the fact.
- **Ownership.** The directory is read-write for every sandbox that mounts it;
  "don't edit this" is a sentence in a prompt, not a property of the system.

These are all the same problem wearing different hats: `/shared/openapi.yaml` is
implicit shared mutable state, and two agents reading and writing one mutable
location is the hardest coordination problem there is.

Making the channel richer — notifications, locking, a message queue between
containers — treats the symptom and adds failure modes. A handoff that can hang
is worse than one that is merely stale.

## Design

Invert the dependency. The consumer stops watching a file and starts depending on
a **version**.

```
contracts.git  ──tag v3──▶  consumer pins "v3" in its own repo
```

The consumer never asks "did the contract change?" — a question with a racy,
time-dependent answer. It asks "does `v3` exist?", which is always answerable and
stays true once true.

Three pieces:

1. **A bare git repo inside the shared directory** —
   `~/.config/sandbox/shared/contracts.git`. Neutral ground owned by neither
   project. Local only; no remote, no network, no credentials.
2. **Immutable version tags.** The producer commits the contract and tags it
   `v1`, `v2`, … A published tag is never rewritten; a change means a new tag.
3. **A `contracts.lock` file committed to the consumer's repo**, naming the tag
   it builds against.

Each coordination problem is answered by construction:

| Problem | Resolution |
|---|---|
| Notification | Nothing to notify. The consumer uses `v3` until the pin is changed deliberately. |
| Atomicity | A tag exists or does not. There is no partial state. |
| Staleness | Visible and intentional — the pin is a tracked line of code. |
| Ownership | The producer pushes; the consumer only reads a tag. |
| "Which contract was this built against?" | `git log contracts.lock`. |

Upgrading becomes a one-line diff in the consumer's repository: reviewable,
revertible, and present in `git blame` when something breaks later. This is the
same reason `package-lock.json` exists, and it works here for the same reason.

## Usage (v1 — no new code)

Once per machine:

```sh
git init --bare ~/.config/sandbox/shared/contracts.git
```

Producer's `CLAUDE.md`:

> When the API contract changes: write it to `/shared/contracts/openapi.yaml`,
> commit, tag the next version (`v1`, `v2`, …), and push. Never edit a published
> tag; cut a new one.

Consumer's `CLAUDE.md`:

> The API contract is pinned in `contracts.lock`. Read that version with
> `git -C /shared/contracts show $(cat contracts.lock):openapi.yaml` and
> implement against it. To adopt a newer contract, change `contracts.lock` in its
> own commit.

Both sandboxes run with `--share`. Sequencing is the shell:

```sh
sandbox-cli claude --share -p ~/web-ui  -p "publish contract v4" && \
sandbox-cli claude --share -p ~/backend -p "bump contracts.lock to v4 and implement"
```

No polling, no message bus, no race.

## Possible v2: ergonomics

v1 works today and needs nothing from `sandbox-cli`. That is a feature, and the
bar for adding code should stay high — the value here is the convention, not the
plumbing. If the git commands prove to be the thing users get wrong, the
narrowest useful addition is a pair of subcommands wrapping exactly them:

```sh
sandbox-cli contract publish openapi.yaml     # commit + tag next version + push
sandbox-cli contract pin v3                   # write contracts.lock
sandbox-cli contract show                     # print the pinned contract
```

These would be thin wrappers over `worktree.Git`-style passthrough helpers, with
no new isolation surface: they operate on the already-mounted shared directory
and the consumer's own repo.

Deliberately out of scope: watching, diffing against a live spec, validating
OpenAPI, or generating clients. Those belong to tools that already do them well.

## Open questions

- **Is `contracts.lock` the right name?** It suggests a single contract per repo.
  A consumer depending on two producers needs either multiple files or a keyed
  format.
- **One contracts repo per machine, or per pair of projects?** A single
  `contracts.git` under the shared dir is simplest, but couples unrelated
  projects into one history. Per-pair repos are cleaner and harder to discover.
  [`shared-namespaces.md`](shared-namespaces.md) gives the shared dir a
  per-repo/per-branch layout, which makes a per-producer `contracts.git` an
  address rather than a naming convention.
- **Should `--share` seed `contracts.git`?** It would remove the one-time setup
  step, but every sandbox would then carry a git repo it has no use for. Probably
  better as an explicit `sandbox-cli contract init`.
- **Ownership enforcement.** Mounting `/shared` read-only on the consumer makes
  the ownership rule structural rather than advisory, but `--share` is a single
  boolean today. A `--share-ro` variant, or a mode argument, would be needed.
  Proposed as `--share=ro` in [`shared-namespaces.md`](shared-namespaces.md),
  which mounts the root read-only and the producer's own directory read-write.

## Not chosen

- **A message bus / socket / queue between containers.** Real machinery for two
  agents on one laptop, and it converts a stale read into a hang.
- **Polling with lock files.** Reintroduces the shared-mutable-state problem it
  is meant to solve.
- **Making the contract a published package** (npm, PyPI). This is genuinely
  better where it fits — a version number and a type error instead of a runtime
  surprise — but it needs a registry and a release process, which is more than
  two agents on one machine should have to stand up.
