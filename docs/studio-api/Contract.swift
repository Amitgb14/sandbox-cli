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

/// ErrorResponse is the body of every non-2xx response.
public struct ErrorResponse: Codable, Hashable, Sendable {
    public var error: String

    public init(
        error: String
    ) {
        self.error = error
    }
}

/// EgressPosture is what a run launched by this daemon may reach.
public struct EgressPosture: Codable, Hashable, Sendable {
    /// Mode is "allowlist", "default" (unrestricted) or "none".
    public var mode: String
    /// Baseline reports whether the built-in domains are part of an allowlist.
    public var baseline: Bool
    /// Domains is how many the allowlist resolved to. Always present, because a
    /// count discloses nothing about which hosts and is most of what a screen
    /// renders anyway.
    public var domains: Int?
    /// Allow is the resolved list — baseline ∪ configured — which is what the
    /// firewall is actually programmed with, rather than the configured half a
    /// reader would have to add the other half to.
    ///
    /// Present only for an authenticated caller: see egressPosture.
    public var allow: [String]?

    public init(
        mode: String,
        baseline: Bool,
        domains: Int? = nil,
        allow: [String]? = nil
    ) {
        self.mode = mode
        self.baseline = baseline
        self.domains = domains
        self.allow = allow
    }
}

/// HealthResponse answers "is the control plane usable right now".
public struct HealthResponse: Codable, Hashable, Sendable {
    public var status: String  // "ok" | "degraded"
    public var version: String
    public var engine: String  // "docker" | "podman"
    public var engineVersion: String
    public var dockerAvailable: Bool
    public var project: String  // the host directory this server manages
    public var profile: String  // "dev" | "prod"
    /// AuthRequired reports whether this daemon was started with a -token, so a
    /// client can say "you need the token" instead of failing every request with
    /// a 401 it cannot explain.
    ///
    /// Health is the one endpoint that answers unauthenticated, which is exactly
    /// why the fact belongs here: it is the only thing a client without a token
    /// can still ask. It reports *that* a token is required, never any part of
    /// the token itself.
    public var authRequired: Bool
    /// Egress is the posture this daemon will launch with, resolved from its own
    /// config layers.
    ///
    /// Reported because a client cannot work it out and must not guess. The
    /// network mode is **not expressible per request** — a launch may add domains
    /// and may not loosen the posture, the same tighten-only rule
    /// internal/config/trust.go applies to a project file — so a form that
    /// rendered a mode selector was offering a control the request does not have,
    /// initialised to a value nobody had asked for. Showing what the daemon *will*
    /// do, and where to change it, is the honest version of that field.
    public var egress: EgressPosture
    /// Host is what this machine is, as the engine and the Go runtime report it.
    /// Always present: a client showing "where am I running" has nowhere to put
    /// an absent object, and the zero values are honest — 0 bytes means the
    /// engine would not say, which is the same answer `fleet` accepts when it
    /// cannot size the host.
    public var host: HostInfo

    public init(
        status: String,
        version: String,
        engine: String,
        engineVersion: String,
        dockerAvailable: Bool,
        project: String,
        profile: String,
        authRequired: Bool,
        egress: EgressPosture,
        host: HostInfo
    ) {
        self.status = status
        self.version = version
        self.engine = engine
        self.engineVersion = engineVersion
        self.dockerAvailable = dockerAvailable
        self.project = project
        self.profile = profile
        self.authRequired = authRequired
        self.egress = egress
        self.host = host
    }
}

/// HostInfo is the daemon's view of the machine it runs on.
public struct HostInfo: Codable, Hashable, Sendable {
    public var os: String
    public var arch: String
    public var cpus: Int
    public var memBytes: Int

    public init(
        os: String,
        arch: String,
        cpus: Int,
        memBytes: Int
    ) {
        self.os = os
        self.arch = arch
        self.cpus = cpus
        self.memBytes = memBytes
    }
}

/// Project is one repository this control plane will answer about — the unit
/// every branch-addressed request is scoped to.
///
/// ID, not Root, is what a request names. It is worktree.RepoID, the same id that
/// becomes a container's sandbox.repo label, which is what lets "the runs for
/// this repository" and "the worktrees for this repository" be the same question:
/// two clones sharing a directory name do not share an id, and a path is not
/// something a client is trusted to hand back. See internal/studioapi/projects.go.
public struct Project: Codable, Hashable, Sendable {
    public var id: String
    public var name: String
    public var root: String
    /// Default marks the repository this daemon was started in — the one every
    /// request that names no repo is about. Exactly one project carries it, and it
    /// is the one that cannot be removed.
    public var `default`: Bool?
    /// Missing reports a repository that is registered but cannot be read right
    /// now: the directory is gone, is no longer a git repository, or sits on a
    /// volume that is not mounted. Listed rather than dropped, because an absent
    /// checkout is not the same as one the user never asked for — and a client
    /// should show it greyed out rather than silently lose the row.
    public var missing: Bool?

    enum CodingKeys: String, CodingKey {
        case id = "id"
        case name = "name"
        case root = "root"
        case `default` = "default"
        case missing = "missing"
    }

    public init(
        id: String,
        name: String,
        root: String,
        `default`: Bool? = nil,
        missing: Bool? = nil
    ) {
        self.id = id
        self.name = name
        self.root = root
        self.`default` = `default`
        self.missing = missing
    }
}

/// FileEntry is one row of a directory listing.
///
/// Path is repository-relative and slash-separated, so a client feeds it straight
/// back as the next request's `path` without assembling anything itself — and
/// never learns a host path it did not already have from Project.Root.
public struct FileEntry: Codable, Hashable, Sendable {
    public var name: String
    public var path: String
    public var dir: Bool?
    public var size: Int?
    /// Symlink marks a link rather than resolving it. It is reported because
    /// opening one may well be refused: a link leaving the repository is not
    /// readable through this API, which is the rule that keeps an agent-written
    /// `notes.md -> ~/.ssh/id_ed25519` from being served over loopback.
    public var symlink: Bool?
    public var modifiedAt: String?

    public init(
        name: String,
        path: String,
        dir: Bool? = nil,
        size: Int? = nil,
        symlink: Bool? = nil,
        modifiedAt: String? = nil
    ) {
        self.name = name
        self.path = path
        self.dir = dir
        self.size = size
        self.symlink = symlink
        self.modifiedAt = modifiedAt
    }
}

/// FilesResponse is the body of GET /files.
public struct FilesResponse: Codable, Hashable, Sendable {
    /// Path is the listed directory, repository-relative; "" is the root.
    public var path: String
    public var entries: [FileEntry]
    /// Truncated reports a directory with more entries than one listing carries.
    /// Said out loud rather than silently cut: a listing that stops without
    /// saying so reads as "this is everything".
    public var truncated: Bool?

    public init(
        path: String,
        entries: [FileEntry],
        truncated: Bool? = nil
    ) {
        self.path = path
        self.entries = entries
        self.truncated = truncated
    }
}

/// FileContentResponse is the body of GET /files/content.
public struct FileContentResponse: Codable, Hashable, Sendable {
    public var path: String
    public var size: Int
    /// Binary files are reported, never sent: their bytes rendered as text are
    /// noise, and the size is the useful fact about them.
    public var binary: Bool?
    /// Truncated reports that Content is the first part of a larger file.
    public var truncated: Bool?
    public var content: String?

    public init(
        path: String,
        size: Int,
        binary: Bool? = nil,
        truncated: Bool? = nil,
        content: String? = nil
    ) {
        self.path = path
        self.size = size
        self.binary = binary
        self.truncated = truncated
        self.content = content
    }
}

/// BrowseEntry is one directory offered by the folder picker.
///
/// Names and a path, and nothing else: no size, no modification time, no
/// contents. See internal/studioapi/browse.go for why this endpoint is
/// deliberately not a file browser.
public struct BrowseEntry: Codable, Hashable, Sendable {
    public var name: String
    public var path: String
    /// Repo marks a directory holding a .git — a hint, so the picker can point at
    /// what is worth adding. POST /projects still decides.
    public var repo: Bool?
    /// Registered marks a repository this Studio already manages, so the picker
    /// can say so instead of letting somebody add it twice.
    public var registered: Bool?

    public init(
        name: String,
        path: String,
        repo: Bool? = nil,
        registered: Bool? = nil
    ) {
        self.name = name
        self.path = path
        self.repo = repo
        self.registered = registered
    }
}

/// BrowseResponse is the body of GET /browse.
public struct BrowseResponse: Codable, Hashable, Sendable {
    /// Path is the directory being listed, absolute and symlink-resolved.
    public var path: String
    /// Parent is the directory above, or "" at the filesystem root.
    public var parent: String?
    /// Home is this user's home directory — where a picker should start, and the
    /// one shortcut it can offer without guessing.
    public var home: String?
    /// Repo reports whether Path itself is a repository, so "Use this folder" can
    /// be offered for the directory you are standing in.
    public var repo: Bool?
    public var entries: [BrowseEntry]
    public var truncated: Bool?

    public init(
        path: String,
        parent: String? = nil,
        home: String? = nil,
        repo: Bool? = nil,
        entries: [BrowseEntry],
        truncated: Bool? = nil
    ) {
        self.path = path
        self.parent = parent
        self.home = home
        self.repo = repo
        self.entries = entries
        self.truncated = truncated
    }
}

/// ProviderStatus is one agent's provider, and whether it is answering.
public struct ProviderStatus: Codable, Hashable, Sendable {
    public var agent: String
    /// Host is what was asked, empty for an agent with nothing to ask — opencode
    /// is provider-agnostic, and an agent behind a proxy is not talking to the
    /// vendor at all.
    public var host: String?
    /// Probed distinguishes "asked and answered" from "never asked". Unknown is
    /// not down: an unprobeable agent still works, it simply cannot be skipped in
    /// advance.
    public var probed: Bool
    public var reachable: Bool
    /// Reason is why an unreachable provider is unreachable, in a phrase: "timed
    /// out", "provider answered 503". It is also what tells an outage from a
    /// laptop with no network, which this cannot distinguish on its own.
    public var reason: String?
    /// Overridden reports a host the user chose rather than the one compiled into
    /// the descriptor — which is the only way opencode gets probed at all, and the
    /// right answer for anyone pointing an agent at a proxy.
    public var overridden: Bool?
    /// Managed says the override came from the file Studio writes, rather than
    /// from the user's own config.yaml — which outranks it and cannot be edited
    /// from here.
    ///
    /// The distinction is not cosmetic: a client that rebuilds its save payload
    /// from every overridden row copies config.yaml's values into Studio's file,
    /// where they then persist after the config lines are deleted, and an edit to
    /// an agent config.yaml also names appears to save and silently reverts on the
    /// next daemon start. A row that is overridden but not managed is read-only,
    /// and saying so is the only honest thing this API can do about a layer it
    /// does not own.
    public var managed: Bool?
    /// Routable is whether a chain may contain this agent at all — it needs a
    /// verified non-interactive mode, or it would hang in the fallback slot where
    /// nobody is looking.
    public var routable: Bool

    public init(
        agent: String,
        host: String? = nil,
        probed: Bool,
        reachable: Bool,
        reason: String? = nil,
        overridden: Bool? = nil,
        managed: Bool? = nil,
        routable: Bool
    ) {
        self.agent = agent
        self.host = host
        self.probed = probed
        self.reachable = reachable
        self.reason = reason
        self.overridden = overridden
        self.managed = managed
        self.routable = routable
    }
}

/// ProvidersRequest is the body of POST /routing/providers: the host to probe per
/// agent. An empty value is an explicit "do not probe this one".
public struct ProvidersRequest: Codable, Hashable, Sendable {
    public var providers: [String: String]

    public init(
        providers: [String: String]
    ) {
        self.providers = providers
    }
}

/// ProbeBucket is one slot of a provider's uptime strip: how many probes in that
/// span answered and how many did not.
///
/// Both counts rather than a state, because zero-and-zero is a third thing: the
/// daemon was not running, or was started with probing off, and nothing was
/// asked. A bucket that reported "down" for that would turn every night a laptop
/// was closed into an incident.
public struct ProbeBucket: Codable, Hashable, Sendable {
    public var at: String
    public var up: Int
    public var down: Int
    public var reason: String?

    public init(
        at: String,
        up: Int,
        down: Int,
        reason: String? = nil
    ) {
        self.at = at
        self.up = up
        self.down = down
        self.reason = reason
    }
}

/// ProviderHistory is one agent's strip.
public struct ProviderHistory: Codable, Hashable, Sendable {
    public var agent: String
    public var buckets: [ProbeBucket]
    /// Uptime is the fraction of *taken* samples that answered, and Samples is how
    /// many there were. The pair travels together on purpose: 100% of two samples
    /// is not the claim 100% of six hundred is, and a percentage with no count
    /// behind it invites reading the first as the second.
    public var uptime: Double?
    public var samples: Int?

    public init(
        agent: String,
        buckets: [ProbeBucket],
        uptime: Double? = nil,
        samples: Int? = nil
    ) {
        self.agent = agent
        self.buckets = buckets
        self.uptime = uptime
        self.samples = samples
    }
}

/// ProbeHistoryResponse is the body of GET /routing/history.
public struct ProbeHistoryResponse: Codable, Hashable, Sendable {
    public var hours: Int
    /// Interval is the sampling period in seconds, 0 when probing is off. A client
    /// needs it to say what a gap means — with no prober running, every gap is
    /// simply "not collected" rather than anything about the provider.
    public var interval: Int
    public var providers: [ProviderHistory]

    public init(
        hours: Int,
        interval: Int,
        providers: [ProviderHistory]
    ) {
        self.hours = hours
        self.interval = interval
        self.providers = providers
    }
}

/// RoutingResponse is the body of GET /routing.
public struct RoutingResponse: Codable, Hashable, Sendable {
    public var providers: [ProviderStatus]

    public init(
        providers: [ProviderStatus]
    ) {
        self.providers = providers
    }
}

/// ProjectsResponse is the body of GET /projects.
public struct ProjectsResponse: Codable, Hashable, Sendable {
    public var projects: [Project]

    public init(
        projects: [Project]
    ) {
        self.projects = projects
    }
}

/// ProjectCreateRequest is the body of POST /projects, and the only place in this
/// contract where a client hands over a host path. Every refusal that applies to
/// a directory Studio will touch is applied here, once, so that every other
/// endpoint can take an id and be done.
public struct ProjectCreateRequest: Codable, Hashable, Sendable {
    /// Path is an absolute host directory inside the git repository to add. It is
    /// resolved to the repository *root* before being recorded: Studio addresses
    /// work by branch, and a branch belongs to a repository rather than to
    /// whichever subdirectory somebody happened to type.
    public var path: String

    public init(
        path: String
    ) {
        self.path = path
    }
}

/// ProjectCloneRequest is the body of POST /projects/clone.
///
/// The one request in this API that makes the daemon write to the host filesystem
/// and run a program, which is why the handler's refusals are the substance of it
/// — see internal/studioapi/clone.go.
public struct ProjectCloneRequest: Codable, Hashable, Sendable {
    /// URL is the repository to clone. https, ssh, or git@host:path; everything
    /// else is refused, `ext::` above all, because it executes a command rather
    /// than fetching a repository.
    public var url: String
    /// Parent is the absolute directory to clone *into*. It must exist and pass
    /// the same refusals a typed project path does.
    public var parent: String
    /// Name is the directory to create inside it. Empty takes git's own answer:
    /// the last path segment without .git.
    public var name: String?

    public init(
        url: String,
        parent: String,
        name: String? = nil
    ) {
        self.url = url
        self.parent = parent
        self.name = name
    }
}

/// AgentInfo describes one agent adapter sandbox-cli knows how to launch
/// headlessly. Only agents with a verified non-interactive mode are ever listed —
/// see internal/agents' package doc — because a Studio-launched run is always
/// detached, and an agent that stops to ask permission would just hang.
public struct AgentInfo: Codable, Hashable, Sendable {
    public var name: String
    public var label: String
    public var persistDir: String
    public var envAllow: [String]
    /// Env is what sandbox-cli itself sets in the container for this agent — the
    /// keyring the container has no daemon for, and so on. Distinct from EnvAllow, which
    /// is host values forwarded by *name* only if the host has them.
    public var env: [String]
    /// HeadlessVerified is true for every agent listed here, and saying so
    /// explicitly is not redundant: internal/agents only registers adapters with a
    /// confirmed non-interactive argv (TestEveryAgentHasAVerifiedHeadlessArgv is
    /// where that stops being a convention), because a Studio-launched run is
    /// always detached and an agent that stops to ask permission does not fail —
    /// it hangs. A client that cannot see the flag cannot warn about the agents
    /// that would, so the field is sent rather than inferred from membership.
    public var headlessVerified: Bool
    /// CanSkipPermissions is whether this agent's approval prompts can be turned
    /// off with a *flag*, which is what an interactive run would need. False for
    /// the agents whose non-interactive mode is a subcommand instead (`codex
    /// exec`, `opencode run`): there is nothing to add to a console
    /// session, and a control that silently did nothing would be worse than one
    /// that is not offered.
    public var canSkipPermissions: Bool
    /// SkipPermissionArgs is that flag, verbatim — `--dangerously-skip-permissions`,
    /// `--yolo`. Sent rather than left for the client to know, because a control
    /// that turns off an agent's approval prompts should be able to name what it
    /// adds, and a UI carrying its own copy of two flag strings is a second
    /// definition of a security-relevant argv. Empty exactly when
    /// CanSkipPermissions is false.
    public var skipPermissionArgs: [String]?
    /// CanSeedConsolePrompt is whether an *interactive* run of this agent can be
    /// handed a first turn on the command line.
    ///
    /// A prompt is not a thing every agent can be given, and assuming otherwise
    /// cost a real run: the argv appended it as a bare positional for everyone,
    /// and opencode reads a lone positional as *the project directory to open*,
    /// so a console run died inside the container with `Failed to change
    /// directory to /workspace/review the code`.
    ///
    /// POST /runs already refuses the combination (see handleCreateRun), so this
    /// field changes nothing about what the daemon will do. It changes what a
    /// client can say *before* asking: without it a UI can only offer the prompt
    /// box, take the 400, and explain a refusal after the fact. That is the same
    /// argument CanSkipPermissions is here for, on the neighbouring question.
    public var canSeedConsolePrompt: Bool
    /// CanResume is whether a conversation of this agent's can be reopened by its
    /// native session id — `claude --resume`, `codex resume`, `opencode --session`.
    /// **Gemini declares none**, so for them "carry this conversation on"
    /// is not expressible at all, and the only honest continuation is a fresh run
    /// with a briefing.
    ///
    /// Sent rather than inferred for the same reason CanSkipPermissions is: a
    /// client that cannot see the flag cannot warn about the agents that lack it,
    /// and a picker offering "resume" where the argv has no way to say it would be
    /// offering a control the request does not have. Read from internal/agentctx's
    /// store table — the same table resumeArgsFor consults when the run is built,
    /// so the offer and the launch cannot disagree.
    public var canResume: Bool
    /// AutonomousInvocation is the argv a fleet task or a detached run would start
    /// this agent with, prompt elided — the same string `fleet run --dry-run`
    /// prints, so a launch preview and a dry run cannot disagree about what is
    /// about to happen.
    public var autonomousInvocation: [String]
    /// Delivery is how the binary reaches the container. Four adapters are baked
    /// into the base image and the rest are installed lazily into the persisted
    /// HOME on first use — baking every adapter would put hundreds of megabytes in
    /// front of every user for agents most will never run. This is a fact about
    /// assets/Dockerfile rather than about the descriptor, which is why it is a
    /// list here and cannot be read off agents.Descriptor.
    public var delivery: String  // "baked" | "npm"
    /// Auth reports whether this agent has logged in yet: the sandbox-owned HOME
    /// mounted for it, whether it exists, and when it last changed. Never its
    /// contents — the persisted directory holds an OAuth refresh token.
    public var auth: AgentAuth
    /// StatusLine and HistorySync are true for claude alone, and that is a
    /// deliberate limit rather than an oversight: no other agent has a status-line
    /// hook, and only claude mounts the host's per-project history bucket.
    public var statusLine: Bool
    public var historySync: Bool
    /// Sessions and ContextStore come from the persisted record of what has
    /// actually been confirmed on this machine. An agent with no verified
    /// descriptor is reported untracked rather than guessed at.
    public var sessions: Int
    public var contextStore: String  // "verified" | "empty" | "missing" | "untracked"

    public init(
        name: String,
        label: String,
        persistDir: String,
        envAllow: [String],
        env: [String],
        headlessVerified: Bool,
        canSkipPermissions: Bool,
        skipPermissionArgs: [String]? = nil,
        canSeedConsolePrompt: Bool,
        canResume: Bool,
        autonomousInvocation: [String],
        delivery: String,
        auth: AgentAuth,
        statusLine: Bool,
        historySync: Bool,
        sessions: Int,
        contextStore: String
    ) {
        self.name = name
        self.label = label
        self.persistDir = persistDir
        self.envAllow = envAllow
        self.env = env
        self.headlessVerified = headlessVerified
        self.canSkipPermissions = canSkipPermissions
        self.skipPermissionArgs = skipPermissionArgs
        self.canSeedConsolePrompt = canSeedConsolePrompt
        self.canResume = canResume
        self.autonomousInvocation = autonomousInvocation
        self.delivery = delivery
        self.auth = auth
        self.statusLine = statusLine
        self.historySync = historySync
        self.sessions = sessions
        self.contextStore = contextStore
    }
}

/// UsageWindow is one subscription window: how much of it is spent, and when it
/// resets.
///
/// Utilization is a pointer because absent and zero are different answers, and
/// this is the field where confusing them matters most. A window past its reset
/// has a cached figure describing the period that already ended, so it reports
/// null — "we cannot honestly say" — rather than a number that is merely wrong.
public struct UsageWindow: Codable, Hashable, Sendable {
    public var kind: String  // "five_hour" | "seven_day"
    public var label: String  // for display: "5-hour", "Weekly"
    public var utilization: Double?
    public var resetsAt: String?
    public var scope: String?  // the model a per-model allowance covers
    /// Active is whether the agent reported this window as the one currently in
    /// force. Null when it said nothing — a window described only by the
    /// five_hour/seven_day fields carries no such flag, and rendering "not in
    /// force" from a missing one would state the absence of a field as a fact.
    public var active: Bool?

    public init(
        kind: String,
        label: String,
        utilization: Double? = nil,
        resetsAt: String? = nil,
        scope: String? = nil,
        active: Bool? = nil
    ) {
        self.kind = kind
        self.label = label
        self.utilization = utilization
        self.resetsAt = resetsAt
        self.scope = scope
        self.active = active
    }
}

/// UsageSnapshot is one reading of an agent's usage cache.
///
/// FetchedAt is when the *agent* last refreshed these numbers from the server,
/// not when this server read the file — and it is always sent, because these
/// figures refresh only when the agent talks to the server and an unlabelled
/// percentage can be hours stale.
public struct UsageSnapshot: Codable, Hashable, Sendable {
    public var agent: String
    public var windows: [UsageWindow]
    /// CanRefresh is whether the agent that owns this cache is on this machine's
    /// PATH. The figures are readable without it — they come from a file, and the
    /// sandbox keeps its own copy — so "there are numbers" and "they can be made
    /// current" are different questions, and a client that offered a refresh it
    /// cannot perform would be answering the second with the first.
    public var canRefresh: Bool
    public var fetchedAt: String?
    public var path: String?
    /// Source is which kind of file answered: "statusline" for the recording
    /// sandbox-statusline writes from the hook payload, "cache" for Claude Code's
    /// own ~/.claude.json. A client needs it to say how a newer reading is
    /// obtained — driving the agent advances the cache and nothing else, so a
    /// refresh control belongs to one of these and not the other.
    public var source: String?
    /// Abandoned reports that the file carrying these figures is being written
    /// while the reading inside it is not — the agent is running and no longer
    /// recording usage there.
    ///
    /// The distinction matters because the remedies are opposite. An old reading
    /// on an idle machine is fixed by using the agent, or by the refresh button.
    /// An abandoned one cannot be fixed at all: refreshing drives the agent, the
    /// agent rewrites the file, and the reading stays where it was. A client that
    /// cannot tell them apart offers a button that does nothing, which is what
    /// this field exists to stop.
    public var abandoned: Bool?

    public init(
        agent: String,
        windows: [UsageWindow],
        canRefresh: Bool,
        fetchedAt: String? = nil,
        path: String? = nil,
        source: String? = nil,
        abandoned: Bool? = nil
    ) {
        self.agent = agent
        self.windows = windows
        self.canRefresh = canRefresh
        self.fetchedAt = fetchedAt
        self.path = path
        self.source = source
        self.abandoned = abandoned
    }
}

/// DoctorCheck is one host property, as `sandbox-cli doctor` reports it.
///
/// UnderDev and UnderProd both travel because the same fact means different
/// things to the two profiles — a control the host cannot provide warns under
/// dev and refuses under prod — and a reader deciding whether this machine is
/// ready for unattended work should not have to switch profiles and ask again.
public struct DoctorCheck: Codable, Hashable, Sendable {
    public var id: String
    public var title: String
    public var result: String  // "pass" | "warn" | "fail" | "unknown"
    public var detail: String
    public var remedy: String?
    public var underDev: String  // "warn" | "fail"
    public var underProd: String  // "warn" | "fail"

    public init(
        id: String,
        title: String,
        result: String,
        detail: String,
        remedy: String? = nil,
        underDev: String,
        underProd: String
    ) {
        self.id = id
        self.title = title
        self.result = result
        self.detail = detail
        self.remedy = remedy
        self.underDev = underDev
        self.underProd = underProd
    }
}

/// DoctorResponse is the body of GET /v1/doctor.
public struct DoctorResponse: Codable, Hashable, Sendable {
    public var profile: String
    public var checks: [DoctorCheck]

    public init(
        profile: String,
        checks: [DoctorCheck]
    ) {
        self.profile = profile
        self.checks = checks
    }
}

/// AgentAuth is where an agent's login is persisted, and whether it is there yet.
public struct AgentAuth: Codable, Hashable, Sendable {
    public var persisted: Bool
    public var path: String
    public var lastSeen: String?  // RFC3339, or absent when never

    public init(
        persisted: Bool,
        path: String,
        lastSeen: String? = nil
    ) {
        self.persisted = persisted
        self.path = path
        self.lastSeen = lastSeen
    }
}

/// AgentsResponse is the body of GET /agents.
public struct AgentsResponse: Codable, Hashable, Sendable {
    public var agents: [AgentInfo]

    public init(
        agents: [AgentInfo]
    ) {
        self.agents = agents
    }
}

/// RunKind separates a fleet task from a run someone (or Studio) started directly
/// — the same distinction `sandbox-cli list`'s KIND column makes, carried into the
/// API for the same reason: a client deciding what it may stop or reap needs it.
public enum RunKind: String, Codable, Hashable, Sendable, CaseIterable {
    case interactive = "interactive"
    case fleet = "fleet"
}

/// RunState mirrors the docker container states runtime.ContainerInfo reports,
/// spelled out as a closed set so a TypeScript client can switch over them
/// exhaustively instead of guessing at docker's vocabulary.
public enum RunState: String, Codable, Hashable, Sendable, CaseIterable {
    case created = "created"
    case running = "running"
    case paused = "paused"
    case restarting = "restarting"
    case exited = "exited"
    case dead = "dead"
    case unknown = "unknown"

    /// A value this build does not know decodes as `unknown` rather than throwing.
    ///
    /// Sound only because the Go side declares that member: it means the daemon
    /// could not tell either, so a state added after this app shipped lands
    /// somewhere the contract already says means "no answer".
    public init(from decoder: any Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = RunState(rawValue: raw) ?? .unknown
    }
}

/// Run is a container sandbox-cli started, addressed the same way
/// `sandbox-cli list`/`kill`/`logs` addresses one: by id, name, or branch. It is
/// assembled from runtime.ContainerInfo plus the sandbox.* labels stamped on the
/// container — never re-derived, since docker is the state store.
public struct Run: Codable, Hashable, Sendable {
    public var id: String  // short id (12 chars) — what the rest of the API accepts back
    public var containerId: String  // full id
    public var name: String
    public var kind: RunKind
    public var state: RunState
    public var exitCode: Int?  // set once State is "exited"
    public var detached: Bool
    /// RepoID is `sandbox.repo`: worktree.RepoID, an id and not a path.
    ///
    /// Spelled `repoId` to match Worktree, and that is a fix rather than a
    /// preference. This field was `repo` while Worktree's was `repoId` — one fact
    /// under two names in one contract — and Studio was written against the other
    /// spelling, so filtering runs by repository compared every row against
    /// `undefined` and quietly produced nothing. It looked like an empty
    /// repository rather than like a broken field, which is why it survived: the
    /// only way to see it was to select a repository that definitely had runs.
    public var repoId: String?
    /// RoutedFrom is the agent that was asked for, when routing fell through to a
    /// different one; empty when the run used what it was given. RouteReason says
    /// why. Read from the container's labels rather than from the audit log,
    /// because a detached run's audit line is written when it *ends* — long after
    /// somebody looks at the listing and asks why it says codex.
    public var routedFrom: String?
    public var routeReason: String?
    /// RouteID is the episode, and RouteAttempt the position in it — 1 for the
    /// agent first asked for, 2 for the one the supervisor started after it
    /// failed. Both from labels, for the reason above.
    ///
    /// The attempt is what separates the two kinds of switch, which look identical
    /// through RoutedFrom alone: a *preflight* skip is attempt 1 and carries no
    /// conversation, because the agent it names never ran, while attempt 2 or more
    /// is a run that failed and handed its work over with a briefing. Telling a
    /// user "it did not inherit the conversation" about the second case is simply
    /// untrue.
    public var routeId: String?
    public var routeAttempt: Int?
    /// HandoffFrom is the agent whose conversation this run was briefed with, and
    /// HandoffSession the session it came from. Empty for a run that started from
    /// its own prompt.
    ///
    /// Read from labels, and reported separately from RoutedFrom even though a
    /// failover sets both: routing says a provider stopped answering, a handoff
    /// says a person chose. A listing that collapsed them would answer "why is
    /// codex doing this" with the wrong story half the time.
    public var handoffFrom: String?
    public var handoffSession: String?
    /// RepoName is the display half of that id. Two clones of a same-named repo
    /// share it and do not share RepoID, so it is for showing and never for
    /// matching — which is exactly the mistake this pair exists to keep separate.
    public var repoName: String?
    public var branch: String?
    public var base: String?
    public var agent: String?
    public var verify: String?
    /// Profile is the posture the run was launched under, and Prompt what the
    /// agent was asked to do. Both are read from labels stamped at launch: a
    /// container says what confinement it got but not which profile chose it, and
    /// a prompt otherwise survives only inside an agent-specific argv. Absent for
    /// containers started before either label existed, which is why both omit
    /// rather than send an empty string.
    public var profile: String?
    public var prompt: String?
    public var createdAt: String
    public var startedAt: String?
    public var finishedAt: String?
    /// OpenStdin/TTY say what `attach` could do with this run — see
    /// runtime.ContainerInfo. A Studio-launched run always has both false: it is
    /// always detached.
    public var openStdin: Bool
    public var tty: Bool
    /// What the run actually was, read back off the container rather than
    /// re-derived from a config file that may have been edited since. The labels
    /// above say what the launcher *intended*; these say what docker gave it,
    /// which is the question someone reviewing a finished run is asking.
    public var image: String
    public var command: [String]
    public var workdir: String
    public var workspace: String  // the host path mounted at /workspace
    public var engine: String
    public var durationMs: Int?
    public var mounts: [RunMount]
    public var network: RunNetwork
    public var security: RunSecurity
    /// EnvNames is names only, never values. The credential broker exists to keep
    /// secret values off the argv and out of config files; an API response is one
    /// more file, in a browser's cache. internal/audit makes the same trade and
    /// has nowhere to put a value on purpose.
    public var envNames: [String]

    public init(
        id: String,
        containerId: String,
        name: String,
        kind: RunKind,
        state: RunState,
        exitCode: Int? = nil,
        detached: Bool,
        repoId: String? = nil,
        routedFrom: String? = nil,
        routeReason: String? = nil,
        routeId: String? = nil,
        routeAttempt: Int? = nil,
        handoffFrom: String? = nil,
        handoffSession: String? = nil,
        repoName: String? = nil,
        branch: String? = nil,
        base: String? = nil,
        agent: String? = nil,
        verify: String? = nil,
        profile: String? = nil,
        prompt: String? = nil,
        createdAt: String,
        startedAt: String? = nil,
        finishedAt: String? = nil,
        openStdin: Bool,
        tty: Bool,
        image: String,
        command: [String],
        workdir: String,
        workspace: String,
        engine: String,
        durationMs: Int? = nil,
        mounts: [RunMount],
        network: RunNetwork,
        security: RunSecurity,
        envNames: [String]
    ) {
        self.id = id
        self.containerId = containerId
        self.name = name
        self.kind = kind
        self.state = state
        self.exitCode = exitCode
        self.detached = detached
        self.repoId = repoId
        self.routedFrom = routedFrom
        self.routeReason = routeReason
        self.routeId = routeId
        self.routeAttempt = routeAttempt
        self.handoffFrom = handoffFrom
        self.handoffSession = handoffSession
        self.repoName = repoName
        self.branch = branch
        self.base = base
        self.agent = agent
        self.verify = verify
        self.profile = profile
        self.prompt = prompt
        self.createdAt = createdAt
        self.startedAt = startedAt
        self.finishedAt = finishedAt
        self.openStdin = openStdin
        self.tty = tty
        self.image = image
        self.command = command
        self.workdir = workdir
        self.workspace = workspace
        self.engine = engine
        self.durationMs = durationMs
        self.mounts = mounts
        self.network = network
        self.security = security
        self.envNames = envNames
    }
}

/// RunMount is one host path a run could reach. Named host/container/mode rather
/// than docker's source/destination/rw because that is the vocabulary the rest of
/// sandbox-cli uses — a `mounts:` entry in a config file reads the same way.
public struct RunMount: Codable, Hashable, Sendable {
    public var host: String
    public var container: String
    public var mode: String  // "ro" | "rw"
    public var origin: String?

    public init(
        host: String,
        container: String,
        mode: String,
        origin: String? = nil
    ) {
        self.host = host
        self.container = container
        self.mode = mode
        self.origin = origin
    }
}

/// RunNetwork is the egress posture this container actually ran with, read back
/// off the container rather than from the config that asked for it.
///
/// Allow is the *resolved* list — baseline ∪ configured — because that is what
/// the entrypoint was handed and therefore what the firewall and proxy actually
/// enforce. Baseline says whether the built-in set is part of it, which is the
/// difference between "these nine hosts plus mine" and "only mine".
public struct RunNetwork: Codable, Hashable, Sendable {
    public var mode: String  // "default" | "none" | "allowlist"
    public var baseline: Bool
    public var allow: [String]
    public var networkName: String?
    /// Enforcement names how the allowlist is applied, and null when there is no
    /// allowlist at all. "name" means the in-container proxy decided on the
    /// hostname; "address" would mean IP rules alone, which cannot tell two hosts
    /// sharing an address apart.
    public var enforcement: String?
    public var ingressPorts: [Int]?

    public init(
        mode: String,
        baseline: Bool,
        allow: [String],
        networkName: String? = nil,
        enforcement: String? = nil,
        ingressPorts: [Int]? = nil
    ) {
        self.mode = mode
        self.baseline = baseline
        self.allow = allow
        self.networkName = networkName
        self.enforcement = enforcement
        self.ingressPorts = ingressPorts
    }
}

/// RunSecurity is the confinement docker applied, read back rather than assumed.
public struct RunSecurity: Codable, Hashable, Sendable {
    public var noNewPrivileges: Bool
    public var capDrop: [String]
    public var capAdd: [String]
    public var pidsLimit: Int
    /// Memory and CPUs are the strings a config file would carry ("2g", "1.5"),
    /// empty when unlimited — not "0", which reads as a limit of nothing.
    public var memory: String
    public var cpus: String
    public var seccomp: String
    public var user: String
    /// Hardening is whether the confinement this tool applies by default is
    /// actually in force, so a client can say "this run was hardened" without
    /// re-deriving the rule from four fields.
    public var hardening: Bool
    /// Runtime is the OCI runtime the engine reported for this container
    /// ("runc", "runsc", "kata-runtime", …), empty when the engine named none.
    /// StrongerIsolation says whether that runtime gives the container a kernel
    /// of its own — a separate field because an unrecognised name is shown and
    /// deliberately not characterised, and a client should not have to keep its
    /// own copy of that list to find out.
    public var runtime: String?
    public var stronger_isolation: Bool

    public init(
        noNewPrivileges: Bool,
        capDrop: [String],
        capAdd: [String],
        pidsLimit: Int,
        memory: String,
        cpus: String,
        seccomp: String,
        user: String,
        hardening: Bool,
        runtime: String? = nil,
        stronger_isolation: Bool
    ) {
        self.noNewPrivileges = noNewPrivileges
        self.capDrop = capDrop
        self.capAdd = capAdd
        self.pidsLimit = pidsLimit
        self.memory = memory
        self.cpus = cpus
        self.seccomp = seccomp
        self.user = user
        self.hardening = hardening
        self.runtime = runtime
        self.stronger_isolation = stronger_isolation
    }
}

/// LogLine is one line of a run's output, as GET /runs/{id}/logs returns it
/// without follow.
///
/// Stream is kept rather than merged because which one a line came from is how a
/// reader tells the agent's own output from the egress proxy's DENY lines
/// interleaved with it — and TS is empty when docker recorded none, not a
/// substituted "now", since a log's value is that it says what happened when.
public struct LogLine: Codable, Hashable, Sendable {
    public var seq: Int
    public var ts: String
    public var stream: String  // "stdout" | "stderr"
    public var text: String

    public init(
        seq: Int,
        ts: String,
        stream: String,
        text: String
    ) {
        self.seq = seq
        self.ts = ts
        self.stream = stream
        self.text = text
    }
}

/// MetricSample is one reading of a running container's resource use, in the
/// units a chart wants: bytes and percentages, with the time it was taken.
public struct MetricSample: Codable, Hashable, Sendable {
    public var t: String
    public var cpuPct: Double
    public var memBytes: Int
    public var memLimitBytes: Int  // 0 means unlimited
    public var netRxBytes: Int
    public var netTxBytes: Int
    public var blockReadBytes: Int
    public var blockWriteBytes: Int
    public var pids: Int

    public init(
        t: String,
        cpuPct: Double,
        memBytes: Int,
        memLimitBytes: Int,
        netRxBytes: Int,
        netTxBytes: Int,
        blockReadBytes: Int,
        blockWriteBytes: Int,
        pids: Int
    ) {
        self.t = t
        self.cpuPct = cpuPct
        self.memBytes = memBytes
        self.memLimitBytes = memLimitBytes
        self.netRxBytes = netRxBytes
        self.netTxBytes = netTxBytes
        self.blockReadBytes = blockReadBytes
        self.blockWriteBytes = blockWriteBytes
        self.pids = pids
    }
}

/// MetricSeries is the body of GET /runs/{id}/metrics.
///
/// A series of one, for now: docker reports what a container is using *now* and
/// keeps no history, so anything longer has to be accumulated by a client that
/// stays connected to ?stream=1. Shaped as a series anyway because that is what
/// the reading is — a point on a chart — and a client should not have to change
/// its type when a second point arrives.
public struct MetricSeries: Codable, Hashable, Sendable {
    public var runId: String
    public var samples: [MetricSample]
    public var peak: MetricPeak

    public init(
        runId: String,
        samples: [MetricSample],
        peak: MetricPeak
    ) {
        self.runId = runId
        self.samples = samples
        self.peak = peak
    }
}

/// MetricPeak is the high-water mark over the samples, which is what the CLI's
/// footer summary prints when a run ends.
public struct MetricPeak: Codable, Hashable, Sendable {
    public var cpuPct: Double
    public var memBytes: Int

    public init(
        cpuPct: Double,
        memBytes: Int
    ) {
        self.cpuPct = cpuPct
        self.memBytes = memBytes
    }
}

/// AuditRecord is one line of the run log: what ran, how it was confined, and how
/// it ended.
///
/// EnvNames carries names and never values, which is the rule the log itself
/// keeps — the credential broker exists so secret values stay off the argv and
/// out of files, and this is one more file.
public struct AuditRecord: Codable, Hashable, Sendable {
    public var time: String
    /// RepoID is which repository this run belonged to, derived from Workspace
    /// rather than recorded — the log predates repositories being plural and has
    /// no such field. Empty means "no repository this daemon knows about", which
    /// is a true statement about a run in a checkout nobody registered.
    /// See repoIDForWorkspace.
    public var repoId: String?
    /// RunID identifies the container, and Finished says whether the outcome below
    /// is a result or a placeholder.
    ///
    /// A detached run is written twice — once when it launches, once when it ends —
    /// because at launch there is no exit code to wait for. Two lines, one run: a
    /// client that counted them as two would double every Studio run in every
    /// total on every screen, so `GET /v1/audit` collapses the pair and keeps the
    /// finished half.
    public var runId: String?
    public var finished: Bool?
    /// Routing, when this run was part of an episode. Runs sharing a RouteID are
    /// one attempt at one task — the agent that failed and the one that ran
    /// instead — which is the only way to tell a rescue from two unrelated runs.
    public var routedFrom: String?
    public var routeReason: String?
    public var routeId: String?
    public var routeAttempt: Int?
    public var image: String
    public var workspace: String
    public var workdir: String
    public var agent: String?
    public var branch: String?
    public var command: [String]
    public var engine: String
    public var network: String
    public var networkName: String
    /// EgressEnforcementRequested is named for a *request* rather than an
    /// outcome, because that is all the host can honestly know: the container
    /// programs its own firewall, and this says what it was asked to do.
    public var egressEnforcementRequested: String?
    public var egressAllow: [String]
    public var envNames: [String]
    public var exitCode: Int
    public var durationMs: Int
    public var detached: Bool

    public init(
        time: String,
        repoId: String? = nil,
        runId: String? = nil,
        finished: Bool? = nil,
        routedFrom: String? = nil,
        routeReason: String? = nil,
        routeId: String? = nil,
        routeAttempt: Int? = nil,
        image: String,
        workspace: String,
        workdir: String,
        agent: String? = nil,
        branch: String? = nil,
        command: [String],
        engine: String,
        network: String,
        networkName: String,
        egressEnforcementRequested: String? = nil,
        egressAllow: [String],
        envNames: [String],
        exitCode: Int,
        durationMs: Int,
        detached: Bool
    ) {
        self.time = time
        self.repoId = repoId
        self.runId = runId
        self.finished = finished
        self.routedFrom = routedFrom
        self.routeReason = routeReason
        self.routeId = routeId
        self.routeAttempt = routeAttempt
        self.image = image
        self.workspace = workspace
        self.workdir = workdir
        self.agent = agent
        self.branch = branch
        self.command = command
        self.engine = engine
        self.network = network
        self.networkName = networkName
        self.egressEnforcementRequested = egressEnforcementRequested
        self.egressAllow = egressAllow
        self.envNames = envNames
        self.exitCode = exitCode
        self.durationMs = durationMs
        self.detached = detached
    }
}

/// HistoryStatsResponse is the body of GET /v1/stats/history: what the run log
/// says about outcomes, aggregated in the index rather than in the client.
public struct HistoryStatsResponse: Codable, Hashable, Sendable {
    public var stats: Stats
    public var days: [DayBucket]

    public init(
        stats: Stats,
        days: [DayBucket]
    ) {
        self.stats = stats
        self.days = days
    }
}

/// AuditResponse is the body of GET /v1/audit.
public struct AuditResponse: Codable, Hashable, Sendable {
    public var records: [AuditRecord]

    public init(
        records: [AuditRecord]
    ) {
        self.records = records
    }
}

/// DiffFile is one file's change in a run's work.
///
/// Hunks are empty for now and that is stated rather than hidden: this reports
/// *what* changed and by how much, which is the question a reviewer asks first,
/// and rendering the content is a second call the client can make against git
/// itself. An empty list is not a claim that the file has no content.
public struct DiffFile: Codable, Hashable, Sendable {
    public var path: String
    public var previousPath: String?
    public var status: String  // "added" | "modified" | "deleted" | "renamed"
    public var insertions: Int
    public var deletions: Int
    public var binary: Bool?
    public var hunks: [DiffHunk]

    public init(
        path: String,
        previousPath: String? = nil,
        status: String,
        insertions: Int,
        deletions: Int,
        binary: Bool? = nil,
        hunks: [DiffHunk]
    ) {
        self.path = path
        self.previousPath = previousPath
        self.status = status
        self.insertions = insertions
        self.deletions = deletions
        self.binary = binary
        self.hunks = hunks
    }
}

/// DiffHunk is a contiguous run of changed lines.
public struct DiffHunk: Codable, Hashable, Sendable {
    public var header: String
    public var lines: [DiffLine]

    public init(
        header: String,
        lines: [DiffLine]
    ) {
        self.header = header
        self.lines = lines
    }
}

/// DiffLine is one line of a hunk.
public struct DiffLine: Codable, Hashable, Sendable {
    public var kind: String  // "add" | "del" | "ctx" | "meta"
    public var oldNo: Int?
    public var newNo: Int?
    public var content: String

    public init(
        kind: String,
        oldNo: Int? = nil,
        newNo: Int? = nil,
        content: String
    ) {
        self.kind = kind
        self.oldNo = oldNo
        self.newNo = newNo
        self.content = content
    }
}

/// ResolvedConfig is the configuration a run actually got, read off its
/// container.
public struct ResolvedConfig: Codable, Hashable, Sendable {
    public var profile: String
    public var image: String
    public var workdir: String
    public var user: String
    public var home: String
    public var engine: String
    public var network: RunNetwork
    public var security: RunSecurity
    public var mounts: [RunMount]
    public var envAllow: [String]
    public var persistAuth: Bool
    public var sync: Bool
    /// Fields is the layered provenance — which of default/user/project/flag
    /// supplied each value. Empty here, deliberately: a container records the
    /// resolved answer and not the layers behind it, and a guessed layer is worse
    /// than none when the entire point of the view is to say where a value came
    /// from.
    public var fields: [ResolvedField]
    /// Argv is what the container was started with. Display only.
    public var argv: [String]

    public init(
        profile: String,
        image: String,
        workdir: String,
        user: String,
        home: String,
        engine: String,
        network: RunNetwork,
        security: RunSecurity,
        mounts: [RunMount],
        envAllow: [String],
        persistAuth: Bool,
        sync: Bool,
        fields: [ResolvedField],
        argv: [String]
    ) {
        self.profile = profile
        self.image = image
        self.workdir = workdir
        self.user = user
        self.home = home
        self.engine = engine
        self.network = network
        self.security = security
        self.mounts = mounts
        self.envAllow = envAllow
        self.persistAuth = persistAuth
        self.sync = sync
        self.fields = fields
        self.argv = argv
    }
}

/// ResolvedField is one setting and the layer that supplied it.
public struct ResolvedField: Codable, Hashable, Sendable {
    public var key: String
    public var value: String
    public var layer: String
    public var refusedFrom: String?

    public init(
        key: String,
        value: String,
        layer: String,
        refusedFrom: String? = nil
    ) {
        self.key = key
        self.value = value
        self.layer = layer
        self.refusedFrom = refusedFrom
    }
}

/// Commit is one commit on a branch.
///
/// Subject and Author are text from the repository, exactly like a branch name:
/// render them, never interpret them.
public struct Commit: Codable, Hashable, Sendable {
    public var sha: String
    public var shortSha: String
    public var subject: String
    public var author: String
    public var date: String
    public var files: Int
    public var insertions: Int
    public var deletions: Int

    public init(
        sha: String,
        shortSha: String,
        subject: String,
        author: String,
        date: String,
        files: Int,
        insertions: Int,
        deletions: Int
    ) {
        self.sha = sha
        self.shortSha = shortSha
        self.subject = subject
        self.author = author
        self.date = date
        self.files = files
        self.insertions = insertions
        self.deletions = deletions
    }
}

/// CommitsResponse is the body of GET /v1/worktrees/{branch}/commits.
public struct CommitsResponse: Codable, Hashable, Sendable {
    public var base: String
    public var commits: [Commit]

    public init(
        base: String,
        commits: [Commit]
    ) {
        self.base = base
        self.commits = commits
    }
}

/// RunsResponse is the body of GET /runs.
public struct RunsResponse: Codable, Hashable, Sendable {
    public var runs: [Run]

    public init(
        runs: [Run]
    ) {
        self.runs = runs
    }
}

/// RunCreateRequest is the body of POST /runs.
///
/// It always launches detached: an HTTP request/response cycle has nowhere to
/// hold an interactive terminal, so unlike the CLI's `run` there is no foreground
/// mode here — every Studio run is what `sandbox-cli run --detach` or a fleet task
/// would produce. The one variation is Console, which asks for a run somebody
/// intends to attach to and type at; it changes the agent's argv and the
/// container's stdin, and nothing about what either can reach.
public struct RunCreateRequest: Codable, Hashable, Sendable {
    /// Repo names which registered repository this run is about, by id from GET
    /// /projects. Empty means the repository this daemon was started in.
    ///
    /// This is the field a UI should send, and the difference from Project below
    /// is the trust model rather than convenience: an id is resolved against the
    /// registry — a list of directories somebody deliberately added — while a path
    /// is a directory named by whoever composed the request. With no worktree, the
    /// repository root is itself the workspace; with one, the worktree is resolved
    /// inside this repository.
    public var repo: String?
    /// Project is a host directory to mount at /workspace. Defaults to the
    /// server's configured project root. Mutually exclusive with Worktree and
    /// with Repo.
    ///
    /// It predates the registry and is kept for callers that are not a browser —
    /// a script that already knows the path it means. Prefer Repo: it is the one
    /// that cannot name a directory nobody registered.
    public var project: String?
    /// Worktree, when set, resolves (creating if needed) a git worktree for this
    /// branch under sandbox-cli's managed worktree directory and mounts *that* as
    /// the workspace instead — the same mechanism `--worktree` and `fleet` use, so
    /// several runs can work in parallel without colliding. Branch defaults to
    /// this value when Branch is empty.
    public var worktree: String?
    /// Fallback are the agents to try, in order, when Agent's provider is not
    /// answering — the chain from internal/routing.
    ///
    /// Two mechanisms, as in the CLI. The daemon probes before launching and takes
    /// the first agent that answers — the Run it answers with says which one that
    /// was. And a launch with somewhere left to fall through to is *supervised*:
    /// when it exits non-zero having left the workspace untouched, the next agent
    /// is started with a briefing of the conversation so far. See supervisor.go
    /// for the two limits that carries.
    public var fallback: [String]?
    /// Agent is one of the names from GET /agents. Required unless Command is set.
    /// When set, Prompt is run through the agent's autonomous/headless mode,
    /// unless Console asks for the interactive one.
    public var agent: String?
    public var prompt: String?
    /// HandoffFrom starts this run with a briefing built from another
    /// conversation — the answer to "my claude conversation, run it via codex".
    ///
    /// It is **not** a resume and the two are refused together. A session id is a
    /// primary key into one vendor's private store and the schemas differ
    /// entirely, so handing claude's id to codex cannot work; what crosses instead
    /// is internal/handoff's export — HANDOFF.md, a vendor-neutral
    /// transcript.jsonl, and a files.md derived from git — mounted read-only, with
    /// a prompt that tells the target it is reading a briefing rather than its own
    /// history. docs/proposals/shared-context.md argues why the other direction is
    /// refused: an agent told it is resuming answers as though a fabricated
    /// history were its own, confidently, with file-writing tools.
    ///
    /// The source agent may be the *same* agent, and that is not a degenerate
    /// case: gemini declares no resume argv, so a briefing from itself is
    /// the only way to carry one of their conversations on.
    public var handoffFrom: HandoffRef?
    /// Console starts the agent in its *interactive* mode on a container that
    /// keeps a pty and stdin open, so `sandbox-cli attach` from any terminal can
    /// answer it. Prompt, when set, seeds the first turn instead of being the
    /// whole run.
    ///
    /// It is one field for both halves deliberately. A console without the
    /// interactive argv is a keyboard wired to a headless agent that will never
    /// ask anything; the interactive argv without a console is an agent waiting on
    /// stdin that does not exist. Neither half is useful alone, so neither is
    /// separately requestable.
    ///
    /// Refused with Verify: verify's exit code is the answer it exists to give,
    /// and an interactive session's exit code is whenever the person quit.
    public var console: Bool?
    /// SkipPermissions turns off the agent's approval prompts on a console run.
    ///
    /// Headless runs always have it — an agent that stops to ask does not fail,
    /// it hangs — but a console run is one somebody is attached to, where being
    /// asked is the point. So it is opt-in here, for the case where you want to
    /// watch a run that does not wait for you. The container is the blast-radius
    /// boundary either way; this changes what the agent asks, not what it can
    /// reach.
    ///
    /// Only meaningful for agents whose non-interactive mode is a flag rather
    /// than a subcommand (claude, gemini).
    public var skipPermissions: Bool?
    /// Resume carries on an existing conversation instead of starting one, by
    /// the agent's own session id. Requires Console: resuming is something you
    /// do interactively, and a headless resume would replay one prompt into an
    /// old conversation and exit.
    public var resume: String?
    /// Command is a plain guest argv, for a run with no agent (mutually exclusive
    /// with Agent).
    public var command: [String]?
    /// Branch/Base become the sandbox.branch/sandbox.base labels. Branch defaults
    /// to Worktree (or the project's current git branch) when empty.
    public var branch: String?
    public var base: String?
    /// Verify is a shell command run after the agent; its exit code becomes the
    /// container's, same as a fleet task's `verify:`.
    public var verify: String?
    public var image: String?
    public var memory: String?
    public var cpus: String?
    /// Allow adds egress domains and switches the allowlist on for this run, same
    /// as --allow.
    public var allow: [String]?
    /// Env sets literal KEY=VALUE pairs in the container. Reserved control
    /// variables (config.IsReservedEnv) are refused, same as the CLI.
    public var env: [String: String]?
    /// Publish binds container ports on the daemon's host, in docker's syntax —
    /// "8000", "8080:8000", "0.0.0.0:8000:8000" — so an agent's dev server can be
    /// opened in a browser.
    ///
    /// A bare port binds **127.0.0.1**, which is where sandbox-cli deliberately
    /// differs from `docker -p`: you asked to see the port from your machine, not
    /// to serve it to the network. Writing an address out still does exactly what
    /// it says.
    ///
    /// This is the one launch option that opens a way *in*, so it is worth being
    /// clear about who may ask. A project `.sandbox.yaml` may not — trust.go
    /// refuses `ports:` with the reasoning that declaring a dev-server port is a
    /// real use but a decision about the boundary, so it belongs to the user. A
    /// request carrying this *is* the user, driving their own daemon, which is the
    /// same act as typing `--publish`. What it is not is a repository choosing for
    /// them.
    ///
    /// Under an allowlist the firewall's default-deny INPUT chain gains a carve-out
    /// for exactly these ports (SANDBOX_INGRESS_PORTS), which is what makes a
    /// published port reachable at all.
    ///
    /// That variable is also where RunNetwork.IngressPorts is read from, so a run
    /// on an *unrestricted* daemon publishes its ports and reports none — there
    /// was no inbound chain to carve, so nothing recorded them. Worth knowing
    /// before reading an empty field as "nothing was published".
    public var publish: [String]?

    public init(
        repo: String? = nil,
        project: String? = nil,
        worktree: String? = nil,
        fallback: [String]? = nil,
        agent: String? = nil,
        prompt: String? = nil,
        handoffFrom: HandoffRef? = nil,
        console: Bool? = nil,
        skipPermissions: Bool? = nil,
        resume: String? = nil,
        command: [String]? = nil,
        branch: String? = nil,
        base: String? = nil,
        verify: String? = nil,
        image: String? = nil,
        memory: String? = nil,
        cpus: String? = nil,
        allow: [String]? = nil,
        env: [String: String]? = nil,
        publish: [String]? = nil
    ) {
        self.repo = repo
        self.project = project
        self.worktree = worktree
        self.fallback = fallback
        self.agent = agent
        self.prompt = prompt
        self.handoffFrom = handoffFrom
        self.console = console
        self.skipPermissions = skipPermissions
        self.resume = resume
        self.command = command
        self.branch = branch
        self.base = base
        self.verify = verify
        self.image = image
        self.memory = memory
        self.cpus = cpus
        self.allow = allow
        self.env = env
        self.publish = publish
    }
}

/// HandoffRef names the conversation a run is briefed with: the agent that held
/// it, and its session id from GET /agents/{agent}/sessions.
///
/// By id, never by path — the same rule the projects registry and the session
/// endpoints keep. SessionSummary reports a `path` so a raw view can say what it
/// is showing; it is not accepted back, here least of all, since this one is
/// read and mounted into a container.
public struct HandoffRef: Codable, Hashable, Sendable {
    public var agent: String
    public var sessionId: String

    public init(
        agent: String,
        sessionId: String
    ) {
        self.agent = agent
        self.sessionId = sessionId
    }
}

/// RunStopRequest is the body of POST /runs/:id/stop.
public struct RunStopRequest: Codable, Hashable, Sendable {
    /// Force kills immediately (SIGKILL) instead of asking the guest to exit
    /// first. Same distinction as `sandbox-cli kill --force`.
    public var force: Bool?

    public init(
        force: Bool? = nil
    ) {
        self.force = force
    }
}

/// RestoreMode selects what a recover call does with the recovered snapshot —
/// mirrors rescue.RestoreMode.
public enum RestoreMode: String, Codable, Hashable, Sendable, CaseIterable {
    /// RestoreModeBranch points a new branch at the snapshot. The default: it is
    /// the only mode that cannot destroy anything already on disk.
    case branch = "branch"
    /// RestoreModePatch returns the snapshot as a unified diff instead of touching
    /// any branch or working tree.
    case patch = "patch"
    /// RestoreModeWorktree writes the snapshot's files back into the run's
    /// workspace. Refused if that workspace has uncommitted changes.
    case worktree = "worktree"
}

/// RunRecoverRequest is the body of POST /runs/:id/recover.
///
/// It restores the crash-recovery snapshot (internal/rescue) most recently
/// associated with this run's workspace — the same correlation
/// `sandbox-cli recover` performs by agent, project and time window. There may be
/// none: a run that finished cleanly, or one that never wrote a snapshot, has
/// nothing to recover.
public struct RunRecoverRequest: Codable, Hashable, Sendable {
    public var mode: RestoreMode?  // default RestoreModeBranch
    /// Branch overrides the generated branch name (RestoreModeBranch only).
    public var branch: String?

    public init(
        mode: RestoreMode? = nil,
        branch: String? = nil
    ) {
        self.mode = mode
        self.branch = branch
    }
}

/// RunRecoverResponse is the body of a successful POST /runs/:id/recover.
public struct RunRecoverResponse: Codable, Hashable, Sendable {
    public var sessionId: String  // the rescue.Session this snapshot came from
    public var mode: RestoreMode
    public var branch: String?  // set for RestoreModeBranch
    public var patch: String?  // the diff text, for RestoreModePatch
    public var files: Int
    /// MatchesWorkingTree reports that the workspace on disk already holds what
    /// the snapshot holds — the common case, since /workspace is a bind mount and
    /// the snapshot is the belt, not the braces. See rescue.RestoreResult.
    public var matchesWorkingTree: Bool

    public init(
        sessionId: String,
        mode: RestoreMode,
        branch: String? = nil,
        patch: String? = nil,
        files: Int,
        matchesWorkingTree: Bool
    ) {
        self.sessionId = sessionId
        self.mode = mode
        self.branch = branch
        self.patch = patch
        self.files = files
        self.matchesWorkingTree = matchesWorkingTree
    }
}

/// LogEventType discriminates a LogEvent. A client switching on it exhaustively
/// knows the difference between "the run's output ended" and "the connection
/// did", which is the one thing a log viewer must not guess: an incomplete
/// stream that renders as a complete one is how a half-finished agent run reads
/// as a finished one.
public enum LogEventType: String, Codable, Hashable, Sendable, CaseIterable {
    case log = "log"
    /// LogEventError carries a failure of the stream itself (docker unreachable
    /// mid-follow, say), not anything the container printed to stderr.
    case error = "error"
    /// LogEventEnd is the last event of a stream that finished on its own terms.
    case end = "end"
}

/// LogEvent is one event of GET /runs/:id/logs, identical on both transports: a
/// WebSocket text frame carries exactly this object, and an SSE `data:` line
/// carries exactly this object with `event:` repeating its Type.
public struct LogEvent: Codable, Hashable, Sendable {
    public var type: LogEventType
    public var stream: String?  // "stdout" | "stderr", on Type "log"
    /// Data is one line with its newline stripped, and is deliberately *not*
    /// omitempty: a blank line is ordinary log output, and omitting the field for
    /// it would make `data` optional in the contract — forcing every consumer to
    /// coalesce a missing string on the hottest path in a log viewer.
    public var data: String
    public var error: String?  // on Type "error"

    public init(
        type: LogEventType,
        stream: String? = nil,
        data: String,
        error: String? = nil
    ) {
        self.type = type
        self.stream = stream
        self.data = data
        self.error = error
    }
}

/// RunMetrics is a single point-in-time resource sample for one run — the same
/// numbers the CLI's live gauge and `sandbox-cli stats` sample from `docker
/// stats`, as parsed numbers rather than docker's formatted strings.
public struct RunMetrics: Codable, Hashable, Sendable {
    public var id: String
    public var memUsageBytes: Int
    public var memLimitBytes: Int?
    public var memPercent: Double
    public var cpuPercent: Double
    public var pids: Int
    public var sampledAt: String

    public init(
        id: String,
        memUsageBytes: Int,
        memLimitBytes: Int? = nil,
        memPercent: Double,
        cpuPercent: Double,
        pids: Int,
        sampledAt: String
    ) {
        self.id = id
        self.memUsageBytes = memUsageBytes
        self.memLimitBytes = memLimitBytes
        self.memPercent = memPercent
        self.cpuPercent = cpuPercent
        self.pids = pids
        self.sampledAt = sampledAt
    }
}

/// StatsResponse is the body of GET /stats: one sample per live sandbox
/// container, host-wide (the API equivalent of `sandbox-cli stats --once`).
public struct StatsResponse: Codable, Hashable, Sendable {
    public var runs: [RunMetrics]
    public var sampledAt: String

    public init(
        runs: [RunMetrics],
        sampledAt: String
    ) {
        self.runs = runs
        self.sampledAt = sampledAt
    }
}

/// Worktree describes one git worktree sandbox-cli manages for the project.
public struct Worktree: Codable, Hashable, Sendable {
    public var branch: String
    public var path: String
    /// Dirty is the modified/untracked paths, and is always present — never
    /// `omitempty`. A clean worktree is the common case, and omitting the field
    /// for it sent no key at all, which every client then reads as `undefined`
    /// rather than as "nothing dirty". A list-valued field that vanishes when
    /// empty makes the ordinary case the one that crashes.
    public var dirty: [String]
    public var dirtyCount: Int
    public var head: String  // the abbreviated commit this branch points at
    public var repoId: String  // the id every container of this project is labelled with
    /// Primary marks the repository's **own checkout** rather than a managed
    /// worktree — the directory `-project` names, the one branch that has no
    /// worktree of its own, and where a run launched without `--worktree` works.
    ///
    /// It is listed at all because a client asking "which branches can I look at"
    /// has to be told about it: internal/worktree.List deliberately reports only
    /// the worktrees sandbox-cli manages, which is right for `worktree list` (they
    /// are the ones it created and can remove) and wrong for a branch picker,
    /// where its absence meant `main` appeared nowhere. Marked rather than mixed
    /// in, because the operations differ: it cannot be removed, and `land` merges
    /// *into* it.
    public var primary: Bool?
    /// Ahead and Behind are counted against Base. "3 ahead" says there is
    /// something to land; "3 ahead, 40 behind" says landing it will be a merge.
    public var ahead: Int
    public var behind: Int
    /// Base is the branch this work is meant to land on, taken from the label the
    /// launching run stamped rather than from whatever is checked out now — the
    /// label is the intent, and `land` treats a disagreement between the two as a
    /// refusal rather than a preference. Null when nothing recorded one.
    public var base: String?
    /// RunID is the run currently working this branch, if one is live.
    public var runId: String?
    /// Verified is what the branch's last run said about its own definition of
    /// done: true if it passed, false if it failed or died before reaching its
    /// verify, and **null when nothing checked it** — no container left to ask, or
    /// a run that declared no verify at all. Null is not false. `land` refuses a
    /// branch that never passed, so a client showing "unverified" and "failed" the
    /// same way would be misreporting the one distinction that decides the merge.
    public var verified: Bool?
    /// CreatedAt is when the checkout appeared on disk. git records no creation
    /// time for a worktree, so this is the directory's own — accurate for the
    /// managed worktrees this lists, all of which sandbox-cli created.
    public var createdAt: String

    public init(
        branch: String,
        path: String,
        dirty: [String],
        dirtyCount: Int,
        head: String,
        repoId: String,
        primary: Bool? = nil,
        ahead: Int,
        behind: Int,
        base: String? = nil,
        runId: String? = nil,
        verified: Bool? = nil,
        createdAt: String
    ) {
        self.branch = branch
        self.path = path
        self.dirty = dirty
        self.dirtyCount = dirtyCount
        self.head = head
        self.repoId = repoId
        self.primary = primary
        self.ahead = ahead
        self.behind = behind
        self.base = base
        self.runId = runId
        self.verified = verified
        self.createdAt = createdAt
    }
}

/// WorktreesResponse is the body of GET /worktrees.
public struct WorktreesResponse: Codable, Hashable, Sendable {
    public var worktrees: [Worktree]

    public init(
        worktrees: [Worktree]
    ) {
        self.worktrees = worktrees
    }
}

/// WorktreeCreateRequest is the body of POST /worktrees.
public struct WorktreeCreateRequest: Codable, Hashable, Sendable {
    public var branch: String
    /// Repo names which registered repository the worktree belongs to, by id from
    /// GET /projects. Empty means the repository this daemon was started in.
    public var repo: String?

    public init(
        branch: String,
        repo: String? = nil
    ) {
        self.branch = branch
        self.repo = repo
    }
}

/// ConversationResponse is what a run said, and whether it can be answered.
public struct ConversationResponse: Codable, Hashable, Sendable {
    public var messages: [Message]
    /// Writable reports whether this run can be typed at right now: it is running
    /// *and* was launched with a console. Sent rather than inferred client-side,
    /// because the two facts that decide it (container state, how stdin was
    /// created) both live here.
    public var writable: Bool
    /// SessionID is the agent's own id for this conversation, whole rather than
    /// abbreviated — Claude Code rejects anything that is not a complete UUID.
    public var sessionId: String?
    /// Resume is the exact line to type on the host to carry the conversation on
    /// after the container is gone. Built here rather than by a client, because
    /// the flags that make it work are not guessable from the id.
    public var resume: String?

    public init(
        messages: [Message],
        writable: Bool,
        sessionId: String? = nil,
        resume: String? = nil
    ) {
        self.messages = messages
        self.writable = writable
        self.sessionId = sessionId
        self.resume = resume
    }
}

/// ConsoleInputRequest is one delivery of keystrokes to a run's stdin.
public struct ConsoleInputRequest: Codable, Hashable, Sendable {
    public var data: String
    /// Enter appends the carriage return that submits it. Separate from Data so a
    /// client can send a partial line — and because \r is not what a caller would
    /// guess: the pty is in raw mode, where a \n arrives as a literal line feed
    /// and the agent's input box simply holds it.
    public var enter: Bool?

    public init(
        data: String,
        enter: Bool? = nil
    ) {
        self.data = data
        self.enter = enter
    }
}

/// ConsoleResizeRequest is a terminal's dimensions, in character cells.
public struct ConsoleResizeRequest: Codable, Hashable, Sendable {
    public var rows: Int
    public var cols: Int

    public init(
        rows: Int,
        cols: Int
    ) {
        self.rows = rows
        self.cols = cols
    }
}

/// SessionSummary is one resumable conversation.
public struct SessionSummary: Codable, Hashable, Sendable {
    public var id: String
    public var title: String?
    public var turns: Int
    public var modified: String
    public var started: String?
    /// Partial marks a session listed from its file alone, because sandbox-cli
    /// has no verified reader for this agent's format. The id and the dates are
    /// real; the title and turn count are unknown, and are reported as unknown
    /// rather than as zero.
    public var partial: Bool?
    /// Project is the working directory the transcript recorded. It is the one
    /// field that tells a sandbox conversation from a host one at a glance: a
    /// container's cwd is always /workspace, a host session's is the real path.
    public var project: String?
    /// Path is where the transcript lives, reported so a raw view can say what it
    /// is showing. It is never accepted *back* — a request names a session by id,
    /// and the daemon resolves it.
    public var path: String?
    public var size: Int?
    /// RepoID is the repository this conversation belongs to, or "" when it
    /// cannot be attributed — a session pooled in the shared bucket records only
    /// `/workspace` and nothing on disk says which project that was. Empty is an
    /// answer, not a failure: it lets a client hide those rather than file them
    /// under a repository they may not belong to.
    public var repoId: String?
    /// Store is "sandbox" (the agent HOME containers get) or "host" (the user's
    /// own history). Both are readable; only the first is this daemon's to
    /// resume, since resuming the other would mean mounting the host's history
    /// into a container that was not asked to have it.
    public var store: String?
    public var resumable: Bool?

    public init(
        id: String,
        title: String? = nil,
        turns: Int,
        modified: String,
        started: String? = nil,
        partial: Bool? = nil,
        project: String? = nil,
        path: String? = nil,
        size: Int? = nil,
        repoId: String? = nil,
        store: String? = nil,
        resumable: Bool? = nil
    ) {
        self.id = id
        self.title = title
        self.turns = turns
        self.modified = modified
        self.started = started
        self.partial = partial
        self.project = project
        self.path = path
        self.size = size
        self.repoId = repoId
        self.store = store
        self.resumable = resumable
    }
}

/// SessionTranscriptResponse is one conversation, parsed into turns.
public struct SessionTranscriptResponse: Codable, Hashable, Sendable {
    public var session: SessionSummary
    public var messages: [Message]

    public init(
        session: SessionSummary,
        messages: [Message]
    ) {
        self.session = session
        self.messages = messages
    }
}

/// SessionRawResponse is the transcript file as it is on disk.
///
/// The *tail* when it is long, because a conversation is appended to and the end
/// is what somebody opening it wants — and Truncated says so, since a client
/// showing half a file as though it were the file makes a claim nobody checked.
public struct SessionRawResponse: Codable, Hashable, Sendable {
    public var session: SessionSummary
    public var size: Int
    public var truncated: Bool?
    public var content: String

    public init(
        session: SessionSummary,
        size: Int,
        truncated: Bool? = nil,
        content: String
    ) {
        self.session = session
        self.size = size
        self.truncated = truncated
        self.content = content
    }
}

public struct SessionListResponse: Codable, Hashable, Sendable {
    public var sessions: [SessionSummary]

    public init(
        sessions: [SessionSummary]
    ) {
        self.sessions = sessions
    }
}

// ---------------------------------------------------------------------------
// Shapes owned by other packages, reached from the types above.
// ---------------------------------------------------------------------------

/// Message is one turn of a conversation, as read from a transcript.
public struct Message: Codable, Hashable, Sendable {
    public var role: String  // "user" | "assistant"
    public var text: String
    public var at: String?

    public init(
        role: String,
        text: String,
        at: String? = nil
    ) {
        self.role = role
        self.text = text
        self.at = at
    }
}

/// DayBucket is one day's runs, split by how they ended.
public struct DayBucket: Codable, Hashable, Sendable {
    public var date: String  // YYYY-MM-DD
    public var total: Int
    public var passed: Int
    public var failed: Int
    public var verifyFailed: Int
    public var stopped: Int

    public init(
        date: String,
        total: Int,
        passed: Int,
        failed: Int,
        verifyFailed: Int,
        stopped: Int
    ) {
        self.date = date
        self.total = total
        self.passed = passed
        self.failed = failed
        self.verifyFailed = verifyFailed
        self.stopped = stopped
    }
}

/// Stats is what the whole window says about outcomes.
public struct Stats: Codable, Hashable, Sendable {
    public var total: Int
    public var decided: Int
    public var passed: Int
    public var passRate: Double?  // percent; null when nothing decided
    public var medianDurationMs: Int?
    public var finishedToday: Int

    public init(
        total: Int,
        decided: Int,
        passed: Int,
        passRate: Double? = nil,
        medianDurationMs: Int? = nil,
        finishedToday: Int
    ) {
        self.total = total
        self.decided = decided
        self.passed = passed
        self.passRate = passRate
        self.medianDurationMs = medianDurationMs
        self.finishedToday = finishedToday
    }
}

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
