// Sandbox Studio API — TypeScript contract mirror.
//
// GENERATED from internal/studioapi/types.go by `make contract`. Do not edit:
// the Go types are the contract, their doc comments are its documentation, and
// a change made only here is a claim no server makes. `TestContractMirrorIsInSync`
// fails when this file and those types disagree.
//
// Three things a client must get right, all enforced server-side (see
// internal/studioapi/guard.go, and the trust model in README.md):
//
//   1. Any request carrying a body must send `Content-Type: application/json`.
//      A bodiless POST (e.g. /runs/:id/stop with no options) needs no header.
//   2. Requests must reach the server by a name it answers to — a loopback name
//      by default, plus anything named with `-allow-host`, which is how a
//      proxied deployment's public name is allowed. A page served from another
//      origin must be named in `-cors-origin`, or it is refused outright rather
//      than merely prevented from reading the response.
//   3. With a token configured, send `Authorization: Bearer <token>`. The one
//      exception is the WebSocket log stream, where the browser API cannot set
//      headers: pass `?token=<token>` on that URL only.
//
//      Three endpoints go further and require the *server* to have been started
//      with a token at all, answering 403 otherwise: `POST /runs/:id/console/input`,
//      `POST /runs/:id/console/resize` and `POST /projects/clone`. A keyboard on
//      a running agent and a clone that writes to the host are not capabilities
//      to hand out because somebody forgot a flag.
//
// Timestamps are ISO-8601 (Go renders time.Time as RFC3339).
//
// Optional and nullable are different claims here, and the difference is worth
// reading before writing a check against either. `field?: T` is a key the server
// omits when empty — absent means absent, never a zero standing in for an
// answer. `field: T | null` is a key that is always sent and may be null, which
// is what a Go pointer without `omitempty` produces: `Worktree.verified` is
// documented as null when nothing checked the branch, and null is not false.
