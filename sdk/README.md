# SDKs

Clients for [Sandbox API v1](../docs/api/v1.md). They speak to any endpoint —
local, self-hosted or cloud — and learn what it offers from `capabilities()`.

| SDK | Status | Tested |
|---|---|---|
| [`python/`](python/) (`sandboxapi`) | standard library only; unix socket, http(s), private CA | yes: `make test-sdk` runs it against a real `sandboxd` |
| [`typescript/`](typescript/) (`sandboxapi`) | `fetch`-based: Node 18+, Deno, Bun, browsers; http(s) endpoints | **not yet run**: written to the same contract, no Node in the development environment that wrote it |

Both cover sandboxes, processes (run, background, output streaming, stdin,
signals), files, suspend/resume and snapshots. Attach and
tunnels (connection upgrades) are in the Go client and the CLI only, for now.

Byte fields are bytes (`bytes` / `Uint8Array`); the wire format's base64 is
handled inside. A sandbox has no repository: put code in with the files calls,
or clone it inside the sandbox.
