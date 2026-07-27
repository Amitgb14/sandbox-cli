package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// claudeEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Claude Code session, applied only if present in the host environment. Nothing
// else about the host crosses the boundary.
var claudeEnvAllow = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
}

// claudeBootstrap ensures a self-updating Claude install exists in the persisted
// HOME (~/.local/bin, installed via the native installer on first run) and execs
// it. The baked npm copy in /usr/local/bin is the offline fallback. Because the
// persisted install is user-writable, Claude Code keeps itself up to date across
// runs — the baked copy could not (root-owned).
const claudeBootstrap = `export PATH="$PATH:$HOME/.local/bin"
if [ ! -x "$HOME/.local/bin/claude" ]; then
  command -v curl >/dev/null 2>&1 && curl -fsSL https://claude.ai/install.sh | bash >/dev/null 2>&1 || true
fi
if [ -x "$HOME/.local/bin/claude" ]; then
  exec "$HOME/.local/bin/claude" "$@"
fi
exec claude "$@"`

// claudeStatuslineSettings is the managed-settings.json (highest precedence, does
// not touch the user's own settings) that points Claude Code's status line at the
// baked cgroup mem/cpu script.
const claudeStatuslineSettings = `{"statusLine":{"type":"command","command":"/usr/local/bin/sandbox-statusline","padding":0,"refreshInterval":3}}
`

func newClaudeCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "claude [sandbox-flags --] [claude-args...]",
		Short: "Run Claude Code inside the sandbox",
		Long: "Runs `claude` inside the sandbox. Everything you pass is forwarded to\n" +
			"claude, so `sandbox-cli claude --dangerously-skip-permissions` just works. Sandbox\n" +
			"options (leading --flags below, or before a `--` separator) are consumed first.\n\n" +
			"Claude Code is installed into the persisted HOME on first run and self-updates\n" +
			"from there, so it stays current (the baked image copy is an offline fallback).\n" +
			"A status line showing the container's memory/CPU is added to the Claude UI;\n" +
			"disable it with --no-statusline.\n\n" +
			"Your Claude login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/claude, separate from your host ~/.claude), so you\n" +
			"log in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"Your host Claude history for this repo is read-write mounted into the sandbox\n" +
			"by default, so host session IDs resolve and a host session can be --resume'd\n" +
			"inside the container (and vice versa). Pass --no-sync to keep the sandbox's\n" +
			"conversation history separate from the host's.\n\n" +
			"Pasting an image gives Claude only its host path, which does not exist in\n" +
			"the container. Pass --paste to mount ~/Desktop, ~/Downloads and ~/Pictures\n" +
			"read-only at their host paths so that path resolves.\n\n" +
			"Forwards ANTHROPIC_API_KEY and related variables from your host environment\n" +
			"only if they are set. No other host files are mounted unless you pass --mount.",
		Example: "  sandbox-cli claude\n" +
			"  sandbox-cli claude --dangerously-skip-permissions\n" +
			"  sandbox-cli claude --project ~/app -- --dangerously-skip-permissions",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := []string{"sh", "-c", claudeBootstrap, "claude"}
			afterParse := func() error {
				if !rf.noStatusline {
					if p, err := ensureClaudeStatuslineSettings(); err != nil {
						// Non-fatal: the status line is a nicety, not core function.
						fmt.Fprintln(os.Stderr, "sandbox-cli: status line disabled: "+err.Error())
					} else {
						rf.mounts = append(rf.mounts, p+":/etc/claude-code/managed-settings.json:ro")
					}
				}
				// History sharing is on by default; when the host has no history for
				// this project yet there is simply nothing to mount (not an error).
				if !rf.noSync && syncEnabled(rf) {
					if src, target, ok := claudeHistoryMount(rf); ok {
						rf.mounts = append(rf.mounts, src+":"+target+":rw")
					}
				}
				return nil
			}
			return runWrapper(cmd, rf, args, agentCmd, claudeEnvAllow, afterParse)
		},
	}
	// Persists Claude's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/claude) mounted as the container HOME, so you log in once. Opt out
	// with --no-persist-auth.
	finishAgentCmd(cmd, rf, "claude")
	cmd.Flags().BoolVar(&rf.noStatusline, "no-statusline", false, "don't add the sandbox memory/CPU status line to Claude")
	cmd.Flags().BoolVar(&rf.noSync, "no-sync", false, "don't mount your host Claude history for this repo (keeps sandbox sessions separate)")
	return cmd
}

// claudeProjectBucket names a project's session directory under
// ~/.claude/projects. It lives in internal/agentctx with the rest of what
// sandbox-cli knows about agent session stores; this is the one caller that has
// to reproduce a store layout rather than read it.
func claudeProjectBucket(absPath string) string { return agentctx.ProjectBucket(absPath) }

// claudeHistoryMount resolves the host Claude project-history dir for the
// workspace and the matching in-container target (under the persisted HOME's
// -workspace bucket). Returns ok=false if the host has no history for this repo.
// Assumes the default HOME (/sandbox/home) and workdir (/workspace); with those
// overridden, resume-by-id may not line up.
func claudeHistoryMount(rf *runFlags) (src, target string, ok bool) {
	p := rf.project
	if p == "" {
		if wd, err := os.Getwd(); err == nil {
			p = wd
		}
	}
	p = config.ExpandTilde(p)
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", "", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}
	src = filepath.Join(home, ".claude", "projects", claudeProjectBucket(abs))
	// Create the project's history directory when it is not there yet.
	//
	// This used to bail out instead, and that made the sync a chicken-and-egg: the
	// host bucket only exists once Claude Code has run on the *host* in this
	// project, so a project used only inside the sandbox never had one, the mount
	// was never made, and every sandboxed session landed in the persisted HOME's
	// shared `-workspace` bucket instead.
	//
	// Two consequences, and the second is the worse one. `context list` could not
	// find those sessions at all — it looks up the per-project bucket, which never
	// existed — so a conversation was reachable only by grepping the agent HOME by
	// hand. And every project's sessions pooled into that one bucket, so
	// `--resume` against another repository's conversation was one id away, with
	// expandResumeID unable to tell them apart either.
	//
	// Creating an empty directory is what Claude Code itself does on first run,
	// and it stays scoped to this one project bucket — the rule that governs this
	// mount.
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		if err != nil && !os.IsNotExist(err) {
			return "", "", false
		}
		if fi != nil && !fi.IsDir() {
			return "", "", false // something else is there; do not touch it
		}
		if err := os.MkdirAll(src, 0o700); err != nil {
			return "", "", false
		}
	}
	wd := rf.workdir
	if wd == "" {
		wd = "/workspace"
	}
	target = "/sandbox/home/.claude/projects/" + claudeProjectBucket(wd)
	return src, target, true
}

// ensureClaudeStatuslineSettings writes the managed-settings.json to a sandbox-
// owned host path and returns it, for read-only mounting into the container.
func ensureClaudeStatuslineSettings() (string, error) {
	root := config.ConfigRoot()
	if root == "" {
		return "", fmt.Errorf("cannot determine config dir")
	}
	dir := filepath.Join(root, "managed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "claude-managed-settings.json")
	if err := os.WriteFile(p, []byte(claudeStatuslineSettings), 0o644); err != nil {
		return "", err
	}
	return p, nil
}
