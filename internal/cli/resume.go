package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// expandResumeID rewrites a shortened session id in the guest args into the full
// id the agent needs, and refuses early when the session is real but out of the
// sandbox's reach.
//
// `context list` prints ids abbreviated, the way `git log --oneline` does — but
// git can do that because git resolves prefixes, and the agents do not. Claude
// Code refuses anything that is not a full UUID ("Provided value \"37888763\" is
// not a UUID"). Rather than widen the table to 36-character ids, sandbox-cli
// resolves the prefix itself.
//
// It resolves within *this project's* history, which is the same scope the
// container will have: a sandbox mounts only the current project's history, so a
// session belonging to another project is one the agent cannot open no matter
// how correct the id is. That case used to surface as the agent's own bare "no
// session found", which sends the user looking for a missing file rather than at
// the directory they are standing in.
//
// The rewrite is deliberately timid. It fires only on the token after a known
// resume argument, only when that token is not already a full id, and only when
// exactly one session matches. A value that matches nothing anywhere is passed
// through untouched — it may not be an id at all, since Claude Code also resumes
// by session title.
func expandResumeID(agent, project string, guest []string) ([]string, error) {
	if agent == "" || len(guest) < 2 {
		return guest, nil
	}
	store, known := agentctx.Lookup(agent)
	if !known || len(store.ResumeArgs) == 0 {
		return guest, nil
	}

	// Find a resume argument with a value after it, before doing any I/O: the
	// overwhelmingly common run has no resume flag at all and must pay nothing.
	idx := -1
	for i := 0; i < len(guest)-1; i++ {
		if slices.Contains(store.ResumeArgs, guest[i]) && !strings.HasPrefix(guest[i+1], "-") {
			idx = i + 1
			break
		}
	}
	if idx < 0 {
		return guest, nil
	}

	f, ok := agentctx.Resolve(agent, agentctx.DefaultRoots(), time.Now())
	if !ok {
		return guest, nil
	}
	short := guest[idx]

	// This project first: what the sandbox will actually be able to see.
	if ref, expanded := agentctx.ExpandID(f, agentctx.ListOpts{Project: project}, short); expanded {
		out := append([]string{}, guest...)
		out[idx] = ref.ID
		fmt.Fprintf(os.Stderr, "sandbox-cli: resuming session %s\n", ref.ID)
		return out, nil
	}

	// Not here — but is it somewhere? Saying so is the whole point: the id is
	// fine, the directory is wrong, and only sandbox-cli is in a position to know
	// that, because it is the one deciding which history gets mounted.
	if ref, found := agentctx.ExpandID(f, agentctx.ListOpts{All: true}, short); found {
		return nil, fmt.Errorf("session %s is not in this project's %s history\n"+
			"  it is in %s\n"+
			"  a sandbox mounts only the current project's history, so %s cannot open it from here\n"+
			"  run sandbox-cli from that project, or pass --project <dir>",
			short, agent, shortenHome(ref.Dir()), agent)
	}
	return guest, nil
}

// resumeProject is the directory whose history a run will have mounted: the
// --project the user gave, or the working directory. It matches how the claude
// wrapper picks the history to mount, so the check and the mount cannot disagree.
func resumeProject(rf *runFlags) string {
	p := rf.project
	if p == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		p = wd
	}
	abs, err := filepath.Abs(config.ExpandTilde(p))
	if err != nil {
		return ""
	}
	return abs
}
