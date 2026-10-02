# _old — the pre-rewrite tree, kept for reference

The whole of beta.15 (plus the host-escape fixes from PR #177), moved here at M0
of `docs/rewrite/PLAN.md`. The Go tool ignores directories whose name starts
with `_`, so nothing here is built, vetted or tested — `go test ./...` sees only
the new tree — while it all stays greppable.

It does not compile in place: its imports still name
`github.com/Amitgb14/sandbox-cli/internal/...`, which is now the new tree.

Porting a package means `git mv _old/internal/<pkg> internal/<pkg>` (so history
follows the file), then making it build against the new layout with its tests
unchanged. What is ported, rewritten or deferred is the table in the plan.
This directory is deleted in the M5 PR, once every CLAUDE.md invariant has a
test in the new tree.

Not buildable on this branch until M6, because their code lives here:
`make build-studio-api`, `Dockerfile.studio-api`, and the Studio frontend's
live mode (`studio/`).
