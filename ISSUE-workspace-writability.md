## Summary

On native Linux the container runs as `--user 1001:<host gid>` (`sharedGroupUser`), but
nothing checks that this user can actually **write** the two host paths a run depends on:

1. `/workspace` — the project or worktree checkout
2. the parent repository's `.git`, which `--worktree` runs mount read-write because git
   cannot work otherwise

When either is group-read-only, the run starts normally and then fails in a way that is
hard to attribute: the agent cannot edit files, or `git commit` fails with git's own
opaque message, which names the object store but not which of its 256 fan-out
directories is at fault:

```
error: insufficient permission for adding an object to repository database
       /home/<user>/project/sandbox-cli/.git/objects
error: Error building trees
```

Finding the cause took a scripted probe over every `objects/??` directory.

## What happened

A `--worktree docs` session on a Rocky Linux host, main at 983b235 (0.0.1beta.11):

```
container: uid=1001(sandbox) gid=1000(node) groups=1000(node)
/workspace                      uid=1000 gid=1000 mode=755   ← group r-x, no w
/sandbox/home                   drwxrws--- node node          ← ShareWithSandboxGroup, correct
.git/objects/{01,07,…} (16)     gid=979                       ← foreign group
.git/objects/{8d,e5}            mode=755                      ← umask 022
```

Nothing could be written to `/workspace` at all, and after that was fixed by hand,
committing still failed until every object directory was made group-writable. Note the
third line: `ShareWithSandboxGroup` is visibly working on the persisted HOME while the
workspace beside it is untouched.

## Why the existing fix does not cover this

`internal/sandbox/hostgroup.go` already solves exactly this problem, and is deliberately
scoped to sandbox-**owned** directories. `ShareWithSandboxGroup` has three call sites:

```
internal/sandbox/sandbox.go:54,113   → opts.AuthPersistDir   (persisted agent HOME)
internal/cli/claude.go:148           → the claude history bucket
internal/sandbox/guestdir.go:73      → each level EnsureGuestDir creates
```

It is never called on the workspace, and never on the worktree's parent `.git`. That
scoping is right — those are the user's own trees, and the rejected-alternatives comment
in `hostgroup.go` explains why chowning them would break the host side. The gap is not
that they aren't repaired; it is that nobody **checks or reports** them.

## When this bites

Whether the container user can write the workspace depends entirely on the host umask,
which nothing surfaces:

- umask 002 (user-private groups; RHEL family) → files `664`, dirs `775` → works
- umask 022 → files `644`, dirs `755` → the agent gets a **read-only workspace**

An agent with a read-only workspace does not fail cleanly. It attempts edits, reports it
could not write, and the user has no reason to suspect a host permission bit.

## Proposed fix — detect and report, do not repair

`EnsureGuestDir` already sets the precedent: a foreign-owned level is reported by name
"because that state can be detected and not repaired". Same principle here.

1. **A `doctor` check** — can the resolved container user write the workspace? *Tried*,
   not queried, in the same shape as the existing seccomp and firewall checks.
2. **A launch-time gate** — warn under `dev`, refuse under `prod`, when the container
   user cannot write `/workspace`. A one-line refusal beats an agent flailing against a
   read-only tree.
3. **Extend both to the `--worktree` `.git` mount.** It is mounted read-write precisely
   because git needs it; an unwritable object store produces the failure above. One
   probe on `.git/objects`, and the message can name the fix.

Docs alongside: recommend `git config core.sharedRepository group` on the parent repo
for worktree users on Linux, so git creates future object directories group-writable
itself. That is what prevents the `mode=755` half from recurring.

## Explicitly not proposed

- Recursively chmod'ing the user's workspace — invasive, and the host side loses.
- Running the container as the host uid — already rejected in `hostgroup.go`: it leaves
  an unwritable HOME on every run that does not mount a persisted one.

## Environment

- Host: Rocky Linux, docker, host uid/gid 1000
- Container: uid 1001, gid 1000, image `sandbox-base` (0.0.1beta.11)
- Repro: `sandbox-cli claude --worktree <branch>` where the worktree checkout is
  `mode=755` and owned by the host user
