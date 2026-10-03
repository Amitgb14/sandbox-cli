---
name: commit-and-pr
description: Commit, push and open a pull request in this repository the way it is done here — message style, the no-company-names check, pushing without stored git credentials, and what to do after the push.
---

# Commit, push, pull request

Commit only when asked. Branch first if on `main`.

## Before committing

```sh
gofmt -l cmd internal          # must print nothing
go vet ./... && go test ./...
```

Check what is staged for company names. Docs, comments and messages describe a
vendor by its role instead:

```sh
git diff --cached | grep -iE '<names that came up in this session>'
```

User-facing change? Add an entry under `Unreleased` in `CHANGELOG.md`.

## Message

- **Title:** a plain sentence saying what changed, not a conventional-commit
  prefix. For example: "Close four ways a run could reach the host through files
  it may write".
- **Body:** prose explaining *why* and what was decided against, one paragraph
  per change, as the existing history does. Name behaviour changes a user would
  notice.
- **No `Co-Authored-By` trailer.**

## Push

git has no stored credentials here; `gh` is logged in. Use it as the helper:

```sh
git -c credential.helper='!gh auth git-credential' push -u origin <branch>
```

A push touching `.github/workflows/` needs the `workflow` scope. If GitHub
refuses with "without `workflow` scope", ask the maintainer to run
`gh auth refresh -h github.com -s workflow`. Do not work around it.

## Pull request

```sh
gh pr create --base main --head <branch> --title "<title>" --body-file -
```

Body:
- what changed, and why;
- behaviour changes;
- anything residual or not done;
- how it was verified, plus the exact commands for the maintainer to run on real
  hosts.

## After the push

Report what was pushed and the PR link, then stop. Do not poll CI in a loop; the
maintainer reports the result.
