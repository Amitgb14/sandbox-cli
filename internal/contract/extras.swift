
// ---------------------------------------------------------------------------
// Hand-written, because these have no Go struct to be generated from.
//
// Query parameters are read off the URL rather than decoded into a type, so
// nothing in types.go describes them — and this file exists because the mirror
// shipped without them twice. The TypeScript generator dropped `RunListQuery`
// when it replaced the hand-maintained mirror, and the Swift generator then
// dropped it again by appending no tail at all, taking the typed shape of the
// most-used listing endpoint with it both times. Anything added here is a shape
// the server really has and the Go types really do not carry; everything else
// belongs in types.go, where it is generated and checked.
// ---------------------------------------------------------------------------

/// Query parameters for `GET /v1/runs`, read at internal/studioapi/runs.go.
///
/// `all` is the one worth reading twice: the endpoint lists **live containers
/// only** unless it is set, so a client with a "finished" filter that does not
/// send it leaves that filter permanently empty.
public struct RunListQuery: Hashable, Sendable {
    /// Include runs that have exited; the default lists only live ones.
    public var all: Bool?
    /// Scope to one repository, by id from `GET /v1/projects`.
    public var repo: String?
    public var branch: String?
    public var agent: String?
    /// Fleet runs only, or interactive only when false.
    public var fleet: Bool?

    public init(
        all: Bool? = nil,
        repo: String? = nil,
        branch: String? = nil,
        agent: String? = nil,
        fleet: Bool? = nil
    ) {
        self.all = all
        self.repo = repo
        self.branch = branch
        self.agent = agent
        self.fleet = fleet
    }

    /// The parameters as they go on the URL.
    ///
    /// Rendered here rather than at the call site because the daemon reads
    /// `all` and `fleet` with `Query.Has` — their *presence* is the signal and
    /// the value is never parsed — so `all=false` enables exactly what it looks
    /// like it disables. Omitting them when false is the only correct encoding,
    /// and it is not one a caller would guess.
    public var queryItems: [URLQueryItem] {
        var items: [URLQueryItem] = []
        if all == true { items.append(URLQueryItem(name: "all", value: "1")) }
        if let repo { items.append(URLQueryItem(name: "repo", value: repo)) }
        if let branch { items.append(URLQueryItem(name: "branch", value: branch)) }
        if let agent { items.append(URLQueryItem(name: "agent", value: agent)) }
        if fleet == true { items.append(URLQueryItem(name: "fleet", value: "1")) }
        return items
    }
}
