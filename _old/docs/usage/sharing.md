# Sharing files between sandboxes

Two sandboxes can't see each other. Each one has its own project at `/workspace`
and nothing else — which is the point, but it leaves no way to hand something
over when a UI agent finishes an API contract the backend agent needs.

`--share` mounts one host directory at `/shared` in every sandbox that asks for
it:

```sh
sandbox-cli claude --share --project ~/web-ui     # writes /shared/openapi.yaml
sandbox-cli claude --share --project ~/backend    # reads it
```

Then just say so in the prompt — there's nothing else to learn:

> "Write the API contract to `/shared/openapi.yaml`"

> "Read `/shared/openapi.yaml` and implement the endpoints"

It works the same across git worktrees of one project, or across two unrelated
projects — the directory is the same for all of them. On the host it lives at
`~/.config/sandbox/shared`, so you can open it in an editor, diff it, or drop
files in yourself. It's created on first use with a README explaining what it is.

**On native Linux it is also group-accessible, and that is worth one look.** A
bind mount there carries real uids, so a directory owned by you at `0700` cannot
be opened by the container at all — `/shared` was simply `EACCES`. It is now
`0770` with your **primary** group, which the container already runs with. If
your primary group is personal (`you:you`, the usual case) nothing else changes.
If it's a shared one — `docker`, `users`, a team group — its other members can
read and write `/shared` too, and the setgid bit propagates that group to what
the agent writes. `id -gn` tells you which you have.

## Namespacing concurrent runs

Because `/shared` is one well-known path, two sandboxes racing to write the
same filename clobber each other. Give `--share` a value to get a
sub-directory of your own instead:

```sh
sandbox-cli claude --share --share-name api-review --project ~/web-ui   # /shared/api-review
sandbox-cli claude --share --share-name perf       --project ~/backend  # /shared/perf
```

`--share-name NAME` mounts `~/.config/sandbox/shared/NAME` at `/shared/NAME`,
created on first use, instead of the shared root. It needs `--share` as well —
a namespace is a way of sharing, not an alternative to it. Two things worth
knowing before you reach for it:

- **It prevents collisions. It is not an isolation boundary.** A namespaced run
  is not handed the other namespaces, which is what stops the accidental
  clobbering — but any sandbox started with a *bare* `--share` has the whole
  shared directory mounted read-write and can read and modify every namespace
  inside it. Namespaces are for cooperating runs that want separate scratch
  space; they are not a confidentiality mechanism, and nothing secret should go
  in one.
- **Use a bare `--share` on both sides** when the point is to hand a file over.

## Things worth knowing

- **Opt-in.** A channel between projects is exactly the reach the sandbox refuses
  by default, so it only exists when you pass the flag.
- **Read-write for everyone using it.** There's no per-sandbox permission split;
  treat it as scratch space with one owner per file. For a one-way channel,
  mount it yourself instead: `--mount ~/.config/sandbox/shared:/shared:ro`.
- **Not versioned.** You get the current file, not its history. If the contract
  starts changing and you want to see what moved, `git init --bare` a repo inside
  `/shared` and push to it from both sides. For the fuller version of that idea —
  immutable version tags and a `contracts.lock` in the consuming repo, so a
  handoff becomes a pinned dependency instead of a shared mutable file — see
  [docs/proposals/pinned-contracts.md](../proposals/pinned-contracts.md)
  (proposed; needs no code, works with `--share` today).
- **Files, not messages.** The agents don't get notified; the reader sees whatever
  is on disk when it looks.

A fleet reaches the same directory with `sandbox-cli fleet run --share` — it is a
launch flag rather than a `fleet.yaml` key, so a cross-project directory stays
something you type rather than something a copied file turns on.

---

Next: [Many agents at once](fleet.md) · [Worktrees](worktrees.md) ·
[documentation index](../README.md)
