// Package agentctx knows where each agent keeps its conversation transcripts,
// and remembers what it found.
//
// Every agent stores sessions somewhere under its HOME, in a private layout that
// its vendor is free to change between releases. Hardcoding those paths and
// trusting them is how this feature would rot silently: a layout moves upstream,
// sandbox-cli keeps pointing at the old directory, and the user is told they have
// no sessions when they have hundreds.
//
// So the paths in this package are *candidates*, not facts. What makes one a fact
// is Probe finding it on this machine, and the Registry writing that down. Later
// commands read the registry — the verified path, with the date it was last seen
// — instead of re-deriving a guess.
package agentctx

// Format identifies the on-disk shape of a session file. It is recorded rather
// than inferred at read time so that a transcript reader can refuse a file it
// does not understand instead of half-parsing it.
const (
	FormatClaudeJSONL  = "claude-jsonl"
	FormatCodexRollout = "codex-rollout"
	FormatGeminiChat   = "gemini-chat"
	FormatOpencodeJSON = "opencode-json"
)

// Root names which home directory a candidate path hangs off. Both are real:
// most agents keep sessions inside the sandbox-owned HOME that persists their
// login (RootAgent), but the claude wrapper mounts the host's own project
// history over that directory by default (--no-sync opts out), so claude's
// transcripts land in the user's real home instead.
const (
	RootAgent = "agent" // ~/.config/sandbox/agents/<agent>
	RootHome  = "home"  // the user's real home directory
)

// BucketDashedPath is Claude Code's per-project directory name: the absolute
// path with '/' and '.' replaced by '-'. It is the only bucket scheme that can be
// derived rather than looked up, which is why it is the only one named here.
const BucketDashedPath = "dashed-path"

// Candidate is one place an agent may keep its sessions.
type Candidate struct {
	Root string // RootAgent or RootHome
	Rel  string // path relative to that root
}

// Store describes one agent's session storage. The fields are deliberately
// descriptive rather than clever: a directory to look in, a pattern that matches
// a session file, and how deep below the directory those files sit. That is
// enough to verify a layout without encoding any single vendor's scheme into the
// probe.
type Store struct {
	Agent  string
	Format string

	// Candidates are the directories to look in, best-known layout first.
	Candidates []Candidate

	// Glob matches one session file inside the store directory.
	Glob string

	// SubDir, when set, is the directory name that must immediately contain a
	// session file. Some stores keep sessions in a named subdirectory next to
	// unrelated state — Gemini's per-project directory holds a `chats/` folder and
	// a `logs.json` beside it — and a glob alone happily matches the wrong one.
	// A file that merely looks like a session is worse than no listing at all: it
	// offers the user an id to resume that was never a conversation.
	SubDir string

	// BucketStyle is how the store names its per-project subdirectory, when it has
	// one that sandbox-cli can derive. Only a derivable bucket lets a listing be
	// scoped to the project you are standing in; a store sharded by date, or by an
	// opaque hash, has to be listed whole and said so.
	BucketStyle string

	// MaxDepth is how many directory levels below the store directory session
	// files may sit: 1 for a flat per-project directory, 3 for a date-sharded
	// tree. Bounded so that probing can never walk an unbounded tree.
	MaxDepth int

	// Resume is the argv prefix that resumes a session by its native id, and
	// MintIDFlag is the flag that fixes the session id up front. An agent with a
	// MintIDFlag can be told the context id instead of being asked for its own
	// afterwards, which is the difference between knowing the id before the run
	// and having to go looking for it after.
	Resume     []string
	MintIDFlag string

	// ResumeArgs is every spelling of the token that introduces a session id,
	// including short forms. Unlike Resume (one canonical way to say it, for
	// printing) this is for recognising what the user typed: sandbox-cli expands a
	// shortened id into the full one before the agent sees it, and can only do
	// that where it knows which argument is an id.
	ResumeArgs []string

	// Note is a caveat worth surfacing about this layout (shown by `context list -v`).
	Note string
}

// stores is the table of known agents. Only agents whose store has actually been
// located are listed here; every other adapter is reported as untracked rather
// than guessed at, because a wrong path is worse than an admitted gap — it makes
// "you have no sessions" look like an answer instead of a failure.
var stores = []Store{
	{
		Agent:  "claude",
		Format: FormatClaudeJSONL,
		// The host home comes first on purpose: the claude wrapper mounts
		// ~/.claude/projects/<bucket> into the container by default, which shadows
		// the same path inside the persisted agent HOME. With --no-sync the agent
		// HOME is where sessions land, so both are probed.
		Candidates: []Candidate{
			{Root: RootHome, Rel: ".claude/projects"},
			{Root: RootAgent, Rel: ".claude/projects"},
		},
		Glob:        "*.jsonl",
		BucketStyle: BucketDashedPath,
		MaxDepth:    1,
		Resume:      []string{"--resume"},
		ResumeArgs:  []string{"--resume", "-r"},
		MintIDFlag:  "--session-id",
	},
	{
		Agent:  "codex",
		Format: FormatCodexRollout,
		Candidates: []Candidate{
			{Root: RootAgent, Rel: ".codex/sessions"},
			{Root: RootHome, Rel: ".codex/sessions"},
		},
		Glob:       "rollout-*.jsonl",
		MaxDepth:   3, // sharded YYYY/MM/DD
		Resume:     []string{"resume"},
		ResumeArgs: []string{"resume"},
		Note:       "sessions are sharded by date, not by project",
	},
	{
		Agent:  "gemini",
		Format: FormatGeminiChat,
		Candidates: []Candidate{
			{Root: RootAgent, Rel: ".gemini/tmp"},
			{Root: RootHome, Rel: ".gemini/tmp"},
		},
		Glob:     "*.json",
		SubDir:   "chats", // <project-hash>/chats/, not the logs.json beside it
		MaxDepth: 2,
		Note:     "project directories are opaque hashes; resume is in-session (/chat resume)",
	},
	{
		Agent:  "opencode",
		Format: FormatOpencodeJSON,
		Candidates: []Candidate{
			{Root: RootAgent, Rel: ".local/share/opencode/storage/session"},
			{Root: RootHome, Rel: ".local/share/opencode/storage/session"},
		},
		// Sessions are named ses_*; the glob says so rather than taking every
		// .json under the store, which is how a lock file becomes a conversation.
		Glob:       "ses_*.json",
		MaxDepth:   3,
		Resume:     []string{"--session"},
		ResumeArgs: []string{"--session", "-s"},
	},
}

// Stores returns the known agent store descriptors.
func Stores() []Store {
	out := make([]Store, len(stores))
	copy(out, stores)
	return out
}

// Lookup returns the descriptor for an agent, or ok=false when sandbox-cli does
// not know where that agent keeps its sessions.
func Lookup(agent string) (Store, bool) {
	for _, s := range stores {
		if s.Agent == agent {
			return s, true
		}
	}
	return Store{}, false
}
