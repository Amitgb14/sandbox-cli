package session

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// What this proves, precisely: the **wire** is faithful. A request crossing the
// socket produces the argv that the same params produce in-process.
//
// It is deliberately *not* the BuildArgs invariant. Both sides here go through
// `OptionsFor`, so a bug in `OptionsFor` is invisible to it — verified by making the
// builder set `GitIdentity` unconditionally, which this test happily passed. The
// claim that a socket-spawned container is the container the **CLI** would have
// started is a different comparison and lives where both paths are reachable:
// `internal/cli.TestSocketSpawnMatchesTheRunPath`.
//
// Kept anyway, because it is the half that catches a transport or decoding fault —
// a field dropped by JSON, a param renamed on one side — which the cross-package
// test would report as a policy difference.
func TestSocketSpawnDryRunMatchesTheSameSpec(t *testing.T) {
	dir, repoID := repo(t)
	srv := open(t, dir)
	srv.Launcher = sandbox.New(config.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, &fakeEngine{}) }()
	sock := SockPath(srv.Dir())
	waitForSocket(t, sock, done)

	cases := []struct {
		name   string
		params protocol.PaneSpawnParams
	}{
		{"a plain command", protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"npm", "test"}}},
		{"with a verify", protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"npm", "test"}, Verify: "test -f built"}},
		{"network none", protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"true"}, Network: "none"}},
		{"capped", protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"true"}, Memory: "1g", CPUs: "1.5"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.params
			p.DryRun = true

			var res protocol.PaneSpawnResult
			if err := Call(sock, protocol.OpPaneSpawn, p, &res); err != nil {
				t.Fatal(err)
			}
			if len(res.Argv) == 0 {
				t.Fatal("a dry run returned no argv")
			}
			if res.Pane != nil {
				t.Error("a dry run minted a pane; it starts nothing, so there is no container to label")
			}

			// The same route, in-process: effective config, worktree, options,
			// BuildSpec, BuildArgs. If the wire ever diverges from this, it has grown
			// a second spec.
			cfg, err := EffectiveConfig(srv.Launcher.Cfg, tc.params)
			if err != nil {
				t.Fatal(err)
			}
			// The real repo id, because it is stamped as a label and a stand-in would make
			// the comparison fail on the test's own input rather than on the code.
			wantOpts, err := OptionsFor(cfg, dir, dir, repoID, tc.params)
			if err != nil {
				t.Fatal(err)
			}
			launcher := sandbox.New(cfg)
			spec, err := launcher.Prepare(wantOpts)
			if err != nil {
				t.Fatal(err)
			}
			want := runtime.BuildArgs(spec)

			// The container name carries a timestamp for a run with no branch, so the
			// two differ in that token by construction rather than by policy.
			if got, wantS := maskName(res.Argv), maskName(want); got != wantS {
				t.Errorf("the socket and the same spec disagree.\n  socket: %s\n  spec:   %s", got, wantS)
			}
		})
	}

	// And a dry run stamps no pane labels, which is what keeps the comparison
	// meaningful: a pane id is a label, and a dry run has no container to put one on.
	var res protocol.PaneSpawnResult
	if err := Call(sock, protocol.OpPaneSpawn, protocol.PaneSpawnParams{
		Kind: protocol.PaneCommand, Argv: []string{"true"}, DryRun: true,
	}, &res); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Argv, " ")
	for _, label := range []string{sandbox.LabelPane, sandbox.LabelPaneKind, sandbox.LabelPaneSession} {
		if strings.Contains(joined, label+"=") {
			t.Errorf("a dry run stamped %s:\n%s", label, joined)
		}
	}
}

// A daemon with no launcher refuses a spawn rather than improvising.
//
// A request that appears to succeed is the worst kind of wrong answer for a launch:
// the caller then waits for a pane that will never exist. `sandbox_unavailable` rather
// than `invalid`, because the request was understood perfectly — this server simply
// cannot do it.
func TestSpawnRefusesWithNoLauncher(t *testing.T) {
	dir, _ := repo(t)
	srv := open(t, dir)

	_, perr := srv.spawnOp(context.Background(), protocol.PaneSpawnParams{
		Kind: protocol.PaneCommand, Argv: []string{"true"},
	})
	if perr == nil {
		t.Fatal("a server with no launcher accepted a spawn")
	}
	if perr.Code != protocol.CodeSandboxUnavailable {
		t.Errorf("code = %q, want %q", perr.Code, protocol.CodeSandboxUnavailable)
	}
}

// A refusal keeps its own code over the wire. The codes are what a client branches
// on, and a `policy_refused` answered as `invalid` reads as "you typed it wrong" for
// a request that was understood and declined.
func TestSpawnRefusalsKeepTheirCodes(t *testing.T) {
	dir, _ := repo(t)
	srv := open(t, dir)
	cfg := config.Default()
	cfg.Network.Mode = "none"
	srv.Launcher = sandbox.New(cfg)

	// Asking to loosen the network: understood, and declined.
	_, perr := srv.spawnOp(context.Background(), protocol.PaneSpawnParams{
		Kind: protocol.PaneCommand, Argv: []string{"true"}, Network: "allowlist",
	})
	if perr == nil {
		t.Fatal("a request to loosen the network was accepted")
	}
	if perr.Code != protocol.CodePolicyRefused {
		t.Errorf("code = %q, want %q", perr.Code, protocol.CodePolicyRefused)
	}

	// A malformed request is a different answer.
	_, perr = srv.spawnOp(context.Background(), protocol.PaneSpawnParams{})
	if perr == nil || perr.Code != protocol.CodeInvalid {
		t.Errorf("an empty spawn: %v, want %q", perr, protocol.CodeInvalid)
	}
}

// maskName drops the timestamped container name a branchless run gets by design, and
// only that form: a deterministic `sandbox-<repo>-<branch>` is the one-agent-per-branch
// lock, so masking it would hide the difference most worth catching.
func maskName(argv []string) string {
	out := append([]string(nil), argv...)
	for i, f := range out {
		if f != "--name" || i+1 >= len(out) {
			continue
		}
		name := strings.TrimPrefix(out[i+1], "sandbox-")
		if name != out[i+1] && !strings.Contains(name, "-") {
			out[i+1] = "<timestamped-name>"
		}
	}
	return strings.Join(out, " ")
}

// The ordering, pinned where it actually lives.
//
// `TestValidateSpawnTouchesNothing` proves the validator is pure; it does **not**
// prove the dispatch calls it first, and it passed with that ordering reverted. This
// is the one that bites: a refused request must leave no branch and no worktree
// behind, because `ResolveWorktreeFor` creates both and repeated bad requests would
// otherwise accumulate them.
func TestARefusedSpawnCreatesNoWorktree(t *testing.T) {
	dir, _ := repo(t)
	srv := open(t, dir)
	srv.Launcher = sandbox.New(config.Default())

	// Contradictory, and naming a worktree: the request from the review.
	_, perr := srv.spawnOp(context.Background(), protocol.PaneSpawnParams{
		Kind: protocol.PaneAgent, Agent: "claude", Argv: []string{"sh"}, Worktree: "junk-branch",
	})
	if perr == nil {
		t.Fatal("the contradictory request was accepted")
	}

	if _, exists, _ := worktree.Path(dir, "junk-branch"); exists {
		t.Error("a refused spawn created a worktree; every refusal comes before the first side effect")
	}
	if wts, err := worktree.List(dir); err == nil {
		for _, wt := range wts {
			if wt.Branch == "junk-branch" {
				t.Errorf("a refused spawn left the worktree for %q", wt.Branch)
			}
		}
	}
	// And the branch itself: `worktree.Resolve` creates one when it is missing, so a
	// leftover branch is the same leak wearing a different hat.
	if branchExistsIn(t, dir, "junk-branch") {
		t.Error("a refused spawn created the branch")
	}
}

func branchExistsIn(t *testing.T, dir, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = dir
	return cmd.Run() == nil
}
