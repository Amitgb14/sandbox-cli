// Sandbox Studio API — Swift contract mirror.
//
// GENERATED from internal/studioapi/types.go by `make contract`. Do not edit:
// the Go types are the contract, their doc comments are its documentation, and
// a change made only here is a claim no server makes. `TestSwiftMirrorIsInSync`
// fails when this file and those types disagree — note the name: the test that
// guards the *TypeScript* mirrors is a different one, and running it instead
// would pass while this file was wrong.
//
// Three things a client must get right, all enforced server-side (see
// internal/studioapi/guard.go, and the trust model in docs/studio-api/README.md):
//
//   1. Any request carrying a body must send `Content-Type: application/json`.
//      A bodiless POST (e.g. /runs/:id/stop with no options) needs no header.
//   2. Requests must reach the server by a name it answers to — a loopback name
//      by default, plus anything named with `-allow-host`. A native client sends
//      no `Origin` and so is unaffected by the CORS half; the `Host` half still
//      applies, and is why a tailnet name needs the flag.
//   3. With a token configured, send `Authorization: Bearer <token>`.
//      URLSessionWebSocketTask *can* set headers, so this client never uses the
//      `?token=` query form — that exists for browsers, which cannot, and a
//      token in a URL lands in logs and shell history.
//
//      Three endpoints go further and require the *server* to have been started
//      with a token at all, answering 403 otherwise: `POST /runs/:id/console/input`,
//      `POST /runs/:id/console/resize` and `POST /projects/clone`. A keyboard on
//      a running agent and a clone that writes to the host are not capabilities
//      to hand out because somebody forgot a flag.
//
// Timestamps are `String`, ISO-8601 (Go renders time.Time as RFC3339). They are
// not `Date`: decoding one would mean this generated file picking a date
// strategy on the app's behalf, and a strategy that does not match turns one
// malformed field into a whole response that will not decode.
//
// Optionality is one spelling here where the TypeScript mirror has two. There,
// `field?: T` is a key the server omits when empty and `field: T | null` is a
// key always sent that may be null; Swift writes both `T?`, and Codable's
// decodeIfPresent treats them alike. What survives the collapse is the part that
// mattered: `Worktree.verified` is `Bool?`, and nil still means nothing checked
// the branch rather than something checked it and said no. Read
// docs/studio-api/types.ts when the difference matters.

import Foundation
