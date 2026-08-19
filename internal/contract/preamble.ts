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
// Timestamps are ISO-8601 (Go renders time.Time as RFC3339). A field marked
// optional here is one the server omits when empty — absent means absent, and
// never a zero standing in for an answer.
