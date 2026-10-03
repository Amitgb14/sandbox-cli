# Documentation

| Page | What it answers |
|---|---|
| [API v1](api/v1.md) | The API every mode serves: sandboxes, processes, attach, files, workspace bundles, network policy, errors |
| [Self-hosting](self-hosting.md) | sandboxd on a Linux machine: install, TLS and tokens, the operator policy, how egress is enforced |
| [Local on a Mac](local-macos.md) | sandboxd with the native `container` runtime, and the points still to be measured |
| [End-to-end checks](testing/end-to-end.md) | What fakes cannot prove, and the command that proves it on a real host |
| [Rewrite plan](rewrite/PLAN.md) | Milestones, measurements and the decisions behind the design |
| [Roadmap](roadmap/README.md) | Where earlier tasks went, and which earlier decisions were reversed |
| [Security](security/) | The audit ledger and the open items |

The beta.15 user documentation (docker-based) is in `_old/docs/`. Fleets,
fallbacks, recovery, the audit log, volumes and pools are rebuilt and described in
the [README](../README.md) and the API doc; worktrees are replaced by sandboxes
on clones of the repository; Studio is `sandbox-cli studio`
([studio/README.md](../studio/README.md)).
