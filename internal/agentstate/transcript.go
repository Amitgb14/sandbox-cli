package agentstate

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Reading an agent's conversation out of its sandbox, over the API, for the
// state an agent is reported in.

// transcriptStore is where an agent keeps this run's conversation inside the
// sandbox, and the reader for its format. Only agents whose format is verified
// (agentctx) are here, so only their conversation crosses; any other agent's
// state is decided without it.
type transcriptStore struct {
	dir    string
	depth  int    // directories below dir that sessions are sharded into
	prefix string // a session file's name starts with this
	parse  func(io.Reader, int) ([]agentctx.Message, error)
}

var transcriptStores = map[string]transcriptStore{
	// The bucket name is Claude Code's spelling of the working directory, and
	// every run starts in the sandbox user's home: /sandbox/home.
	"claude": {dir: agenthome.GuestHome + "/.claude/projects/-sandbox-home", parse: agentctx.ParseTranscript},
	// Sharded by date, YYYY/MM/DD, not by project; a fresh sandbox holds only
	// this run's sessions, so the shard is not a question.
	"codex": {dir: agenthome.GuestHome + "/.codex/sessions", depth: 3, prefix: "rollout-", parse: agentctx.ParseCodexTranscript},
}

// maxTranscripts and maxTranscriptDirs bound the search for this run's
// conversation. A fresh sandbox holds one; the directory is the agent's to
// write, and a thousand planted files or directories should not mean a
// thousand reads.
const (
	maxTranscripts    = 8
	maxTranscriptDirs = 16
)

// ReadTranscript reads this run's conversation out of the sandbox. Best-effort
// by construction: an agent that died before writing one is the commonest case
// here. The contents come from the guest, so they are parsed as data (bounded
// by the protocol's read limit) and only ever quoted into a briefing for the
// next agent — never acted on by the host.
func ReadTranscript(ctx context.Context, c *api.Client, sandbox, agent string) []agentctx.Message {
	st, ok := transcriptStores[agent]
	if !ok {
		return nil
	}
	var best []agentctx.Message
	read, listed := 0, 0
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if listed++; listed > maxTranscriptDirs {
			return
		}
		entries, err := c.ListDir(ctx, sandbox, dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			// Names come from the guest: one carrying a slash or naming a
			// parent is not a directory entry this walk will follow.
			if e.Name == "" || e.Name == "." || e.Name == ".." || strings.Contains(e.Name, "/") {
				continue
			}
			if e.Type == "dir" && depth > 0 {
				walk(dir+"/"+e.Name, depth-1)
				continue
			}
			if e.Type != "file" || depth != 0 || !strings.HasPrefix(e.Name, st.prefix) || !strings.HasSuffix(e.Name, ".jsonl") {
				continue
			}
			if read++; read > maxTranscripts {
				return
			}
			data, err := c.ReadFile(ctx, sandbox, dir+"/"+e.Name)
			if err != nil {
				continue
			}
			msgs, err := st.parse(bytes.NewReader(data), 0)
			if err != nil || len(msgs) == 0 {
				continue
			}
			// The conversation that ended last is the one that failed.
			if best == nil || msgs[len(msgs)-1].At.After(best[len(best)-1].At) {
				best = msgs
			}
		}
	}
	walk(st.dir, st.depth)
	return best
}
