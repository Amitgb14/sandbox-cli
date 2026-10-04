---
name: trust-boundary-review
description: Checklist for any change touching host paths, the host-guest channel (guestd, vsock, bundles, synced files), config keys, environment variables, host-side git, credentials, egress policy or API request handling. Run it before committing such a change.
---

# Trust-boundary review

The guest is hostile by assumption: an agent with approvals off, possibly
prompt-injected, writing files on purpose. Everything here was a real escape
first. The ledger is `docs/security/audit-2026-07-26.md` and the open backlog is
`docs/security/open-items.md`.

## Ask of every change

**Host paths**
- Does any path the host will open, mount, chmod or create derive from
  something the guest or a repository wrote? A `.git` pointer, a symlink, a
  `commondir` file, a name in a bundle.
- Is it `Lstat`ed, not `Stat`ed, where a symlink would redirect it? Is a link
  refused rather than followed?
- Does it pass `hostpath.RefuseUnsafeHostPath` (identity, not string)?
- Known escapes:
  - `.git/hooks -> ~/.ssh` mounted on the next run;
  - a forged `gitdir:` mounting another repository;
  - `~/.claude/projects -> ~/.ssh` chmodded to 0660.

**Host-side git**
- Does every git invocation go through `githard` (`Args` + `Env`)?
- Does a new git operation read a config key that names a program? Hooks,
  filters, diff, merge drivers, `gpg.*`, fsmonitor, `core.sshCommand`, pager,
  editor and askpass all do.
- `internal/githard`'s tests: a trap script must fire *without* hardening (the
  precondition) and not fire *with* it.

**Config and requests**
- New config key: which side of `policy/trust.go` is it on? If a hostile
  repository setting it could widen reach, it is refused from a project config.
  Pin it in `TestProjectConfigRefusesPrivilegedKeys`.
- API request field: may it only **tighten** what the daemon decided? A request
  that loosens policy is refused, not clamped silently.

**Environment**
- New variable crossing into the guest: is it an instruction rather than a
  setting? Check `policy.IsReservedEnv` and its three groups.

**Secrets**
- Can the value reach an argv, a log line, an error message, the audit record,
  or a golden file? Names only.

**Guest → host data**
- Nothing a guest wrote is fetched into a host repository: there is no
  bundle, bring-back or checkpoint any more. A change that brings guest
  output into host git reintroduces that boundary and needs `githard`.
- Synced files land only in sandboxd-owned directories, with no link-following.
- `sandbox-guestd` gains no command that reads an arbitrary path back to the
  host on the guest's initiative.

**Fail closed**
- If a requested control cannot be delivered, does the run refuse? A backend
  without the capability is refused in `spec`, not discovered at run time.

**Output**
- Repository-controlled text printed to a terminal or a table goes through
  `termsafe`.

## A fix for an escape

1. Write the test that reproduces it, with a precondition proving the trap is
   armed.
2. Show it **fails on the old code**: put the old file back, run it, restore.
3. Fix it, and comment the *why* at the fix site: what was reproduced, and why
   the obvious alternative is not enough.
4. Add a `Security` entry to `CHANGELOG.md` that names the behaviour change a
   user might notice.
