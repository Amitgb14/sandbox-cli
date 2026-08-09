# Alternatives and prior art

Running an agent in a disposable container is a crowded space: there are official
options (Docker Sandboxes' `sbx`, Anthropic's devcontainer and Sandbox Runtime,
Codex's built-in OS sandbox) and many open-source twins. sandbox-cli's edge is code
quality and a focused feature set (tested isolation invariants, default-deny env,
dual-agent wrapping, observability) rather than a hard security boundary — for that,
reach for microVM tooling.

| Feature / Aspect | sandbox-cli (Amitgb14) | Built-in agent sandboxes (Claude/Codex) | Docker Sandboxes (`sbx`) | Native OS tools (Seatbelt/Landlock) | Cloud microVMs (E2B, Daytona, …) |
|---|---|---|---|---|---|
| Isolation strength | Good (Docker + hardening; optional gVisor/Kata) | Medium (OS-level, shared kernel) | Excellent (microVM / Firecracker) | Good (kernel/OS primitives) | Excellent (microVMs) |
| Local / no cloud | Yes | Yes | Yes | Yes | No |
| Persistent agent auth | Excellent (dedicated persistent home) | Varies | Good | Varies | Varies |
| Package cache persistence | Yes (`--cache` volumes) | Limited | Good | Manual | Often built-in |
| Parallel agents (worktrees) | Excellent (built-in `--worktree`) | Poor | Good | Poor | Varies |
| Credential broker | Basic ¹ (`--secret` resolves references; the agent still reads the value) | Basic | Good (proxy) | Varies | Good |
| Egress / network control | Strong (allowlist enforced by hostname, fails closed) | Basic | Strong | Varies | Strong |
| Observability / metrics | Good ² (live gauge, stats, per-run log — no per-command trace) | Limited | Good | Poor | Varies |
| Project config | Excellent (`.sandbox.yaml`) | Limited | Good | Poor | API / config |
| Dry-run / preview | Yes | No | Varies | No | Varies |
| Ease of use | High (CLI-focused, good docs) | High | High | Medium | Medium (setup) |
| Cross-platform | Good (macOS/Linux/Windows) | Good | Excellent | Platform-specific | N/A |
| Docker dependency | Yes | No | Yes | No | No |
| Best for | Local multi-agent workflows, ergonomics | Quick minimal protection | Strongest local isolation | Lightweight, zero deps | Scale & long-running tasks |

¹ **This row used to say "Excellent", and that was wrong.** `internal/creds`
resolves secret *references* on the host so values stay off the argv and out of
config files — worth having, but the value still reaches the container's
environment, where the agent can read it with `printenv`. A real broker
terminates TLS and injects the credential so the agent never holds it; that is
[open security item 2](security/open-items.md) and it is not built. What
sandbox-cli does have is the blunter answer, and it is a good one: under
`--profile prod` the persisted OAuth refresh token is **not mounted at all**, so
there is nothing in the container to steal.

² Also downgraded. A live gauge, `stats`, and one run-log line per run
(`~/.config/sandbox/audit/sessions.jsonl`, which now also records the egress
refusals a run reported) are real, but there is no per-command trace and no
replay — see [roadmap task 4](roadmap/task-4-run-provenance.md).

This is our own read of the landscape, and the ratings for other projects are a
snapshot that will age — check their docs before choosing. The two footnotes
above are the rule this table is held to: a row we cannot defend against the code
gets corrected here rather than quietly kept.

The roadmap index also records what was
[considered and declined](roadmap/README.md#considered-and-declined), with reasons.

---

Back to the [documentation index](README.md).
