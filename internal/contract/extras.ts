
// ---------------------------------------------------------------------------
// Hand-written, because these have no Go struct to be generated from.
//
// Query parameters are read off the URL rather than decoded into a type, so
// nothing in types.go describes them — and the first version of this generator
// silently dropped `RunListQuery` when it replaced the hand-maintained mirror,
// taking the typed shape of the most-used listing endpoint with it. Anything
// added here is a shape the server really has and the Go types really do not
// carry; everything else belongs in types.go, where it is generated and checked.
// ---------------------------------------------------------------------------

/** Query parameters for `GET /v1/runs`, read at internal/studioapi/runs.go. */
export interface RunListQuery {
  /** Include runs that have exited; the default lists only live ones. */
  all?: boolean;
  /** Scope to one repository, by id from `GET /v1/projects`. */
  repo?: string;
  branch?: string;
  agent?: string;
  /** Fleet runs only, or interactive only when false. */
  fleet?: boolean;
}
