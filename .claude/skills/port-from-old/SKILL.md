---
name: port-from-old
description: Move a package or file from _old/ (the beta.15 tree) into the new tree on the rewrite branch, keeping its history and its tests. Use whenever code is carried over rather than written fresh.
---

# Porting from `_old/`

`_old/` is the beta.15 tree. The Go tool ignores it, it does not compile in
place, and every rule in it was learned from a reproduced bug or escape. Porting
carries that over; it is not an occasion to rewrite the rule from memory.

## Steps

1. **Find the plan's verdict.** `docs/rewrite/PLAN.md` has a table saying
   whether the thing is *port*, *port + fix*, *port + move*, *defer*, *rewrite*
   or *drop*. If it is not in the table, stop and ask; do not decide alone.
2. **Read the reasoning first.** Find the section of `_old/CLAUDE.md` that
   describes it, and read the package's own doc comments. Note which rules exist
   because of the docker backend (obsolete) and which are about the boundary
   (they come with the code).
3. **Move with `git mv`**, never copy and delete, so `git log --follow` keeps the
   history:
   ```sh
   git mv _old/internal/<pkg> internal/<newpkg>
   ```
   A rename changes the `package` clause in every file, including the tests,
   and nothing else.
4. **Split a file that is half host, half guest** (example: `mounts.go` became
   `hostpath`, and its guest-target checks stayed behind). Move the file, cut the
   part that does not belong, and put that part back under `_old/` with a header
   comment saying where the rest went and which milestone takes it. **Nothing is
   deleted without being either ported or recorded as dropped in the plan.**
5. **Tests pass unchanged, except for:**
   - import paths and package names;
   - tests of something the plan drops (the `engine`/`runtime` keys, for
     example). These are deleted *with* the feature, and the commit says so.

   A test that needs code not yet ported (`BuildSpec`, say) goes back to `_old/`
   in its own file, with a header naming what it waits for. It is not rewritten
   to pass.
6. **No imports from `_old/`.** If ported code needs something still in `_old/`,
   either port that too, or invert the dependency. `policy.DefaultImage` is a
   variable rather than an import of `image` for this reason.
7. **Verify:**
   ```sh
   gofmt -l cmd internal; go vet ./...; go test -race -count=1 ./...
   ```
8. **Record it.** Update the milestone in `docs/rewrite/PLAN.md` with what moved,
   what was dropped and what was left behind, and why.
