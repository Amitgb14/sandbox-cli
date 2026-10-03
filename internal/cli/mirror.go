package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/mirror"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// mirrorMu serialises session-record writes from a checkpoint goroutine and
// the run that owns it.
var mirrorMu sync.Mutex

// mirrorWork copies ref to the user's mirror, if one is configured, and
// records it on the session. It returns what to tell the user, or an error;
// the caller decides when to say it, because a line printed over an agent's
// terminal is a line nobody reads.
//
// A mirror is durability, not isolation: failing to copy is said loudly and
// does not fail the run, whose work is home in refs/sandbox/ either way.
func mirrorWork(ctx context.Context, spec *policy.MirrorSpec, sess *workspace.Session, ref, kind string) (string, error) {
	if spec == nil {
		return "", nil
	}
	m, err := mirror.New(spec, sess.Repo)
	if err != nil {
		return "", fmt.Errorf("mirroring %s: %w", ref, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	e, err := m.Upload(ctx, ref, sess.Sandbox, kind, time.Now())
	if err != nil {
		return "", fmt.Errorf("mirroring %s to %s: %w", ref, m.Bucket(), err)
	}
	mirrorMu.Lock()
	sess.Mirrored = append(sess.Mirrored, workspace.MirrorRecord{Key: e.Key, SHA: e.SHA, Kind: e.Kind, At: e.At})
	_ = sess.Save()
	mirrorMu.Unlock()
	return fmt.Sprintf("mirrored %s to s3://%s (%d MiB): %s", ref, m.Bucket(), e.Size>>20, e.Key), nil
}

// sayMirror prints mirrorWork's outcome.
func sayMirror(msg string, err error) {
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "sandbox-cli: %v\n  the work is still in this repository; mirror it later with: sandbox-cli mirror push\n", err)
	case msg != "":
		fmt.Fprintln(os.Stderr, "sandbox-cli: "+msg)
	}
}

// checkpointMirror is the taken-hook for a run's checkpoints under
// `mirror.upload: all`. Failures are kept, not printed — the agent owns the
// terminal while checkpoints run — and reported by the returned func.
func checkpointMirror(ctx context.Context, spec *policy.MirrorSpec, sess *workspace.Session) (taken func(ref string), report func()) {
	if spec.UploadMode() != policy.MirrorAll {
		return nil, func() {}
	}
	var mu sync.Mutex
	var last error
	failed := 0
	taken = func(ref string) {
		if _, err := mirrorWork(ctx, spec, sess, ref, mirror.KindCheckpoint); err != nil {
			mu.Lock()
			last, failed = err, failed+1
			mu.Unlock()
		}
	}
	report = func() {
		mu.Lock()
		defer mu.Unlock()
		if last != nil {
			fmt.Fprintf(os.Stderr, "sandbox-cli: %d checkpoint(s) were not mirrored; the last error: %v\n", failed, last)
		}
	}
	return taken, report
}

// mirrorSpecFor is the mirror configured for runs on repo: only the user's
// own config can set one (policy/trust.go), so loading it for the repository
// adds nothing a repository chose. nil when none is configured.
func mirrorSpecFor(repo string) (*policy.MirrorSpec, error) {
	cfg, err := loadConfig(repo, "", "", policy.Overrides{})
	if err != nil {
		return nil, err
	}
	return cfg.Mirror, nil
}

func newMirrorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mirror",
		Short: "Copy work that came back to object storage, and fetch it back on any machine",
		Long: "With `mirror:` in ~/.config/sandbox/config.yaml, the work a run brings home is\n" +
			"also copied to an S3-compatible bucket, as a self-contained git bundle, so it\n" +
			"outlives this machine (`upload: all` copies every checkpoint as well). The\n" +
			"config names the variables that hold the credentials, never the credentials,\n" +
			"and a repository's .sandbox.yaml cannot set any of it. Nothing here deletes\n" +
			"from the bucket: retention there is the bucket's lifecycle rules.\n\n" +
			"  mirror:\n" +
			"    s3:\n" +
			"      bucket: my-sandbox-work\n" +
			"      endpoint: https://s3.example.com   # omit for the default S3 service\n" +
			"      access_key_env: WORK_S3_KEY          # default AWS_ACCESS_KEY_ID\n" +
			"      secret_key_env: WORK_S3_SECRET       # default AWS_SECRET_ACCESS_KEY\n" +
			"    upload: bring-back                     # or all",
	}
	cmd.AddCommand(newMirrorCheckCmd(), newMirrorLsCmd(), newMirrorPushCmd(), newMirrorFetchCmd())
	return cmd
}

// repoMirror is the configured mirror for the repository the command runs in.
func repoMirror() (*mirror.Mirror, *policy.MirrorSpec, string, error) {
	repo, err := repoHere()
	if err != nil {
		return nil, nil, "", err
	}
	spec, err := mirrorSpecFor(repo)
	if err != nil {
		return nil, nil, "", err
	}
	m, err := mirror.New(spec, repo)
	return m, spec, repo, err
}

func newMirrorCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Say whether the configured bucket can be reached with the configured credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, spec, _, err := repoMirror()
			if err != nil {
				return err
			}
			if err := m.Check(cmd.Context()); err != nil {
				return fmt.Errorf("s3://%s: %w", m.Bucket(), err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "s3://%s answers; uploading %s\n", m.Bucket(), spec.UploadMode())
			return nil
		},
	}
}

func newMirrorLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls [SANDBOX]",
		Short: "List this repository's mirrored work, newest first",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, _, err := repoMirror()
			if err != nil {
				return err
			}
			sandbox := ""
			if len(args) == 1 {
				sandbox = args[0]
			}
			list, err := m.List(cmd.Context(), sandbox)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "WHEN\tKIND\tCOMMIT\tSIZE\tKEY")
			for _, e := range list {
				fmt.Fprintf(tw, "%s\t%s\t%.12s\t%d MiB\t%s\n", e.At.Local().Format("2006-01-02 15:04"), e.Kind, e.SHA, e.Size>>20, e.Key)
			}
			return tw.Flush()
		},
	}
}

func newMirrorPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push SANDBOX",
		Short: "Mirror a run's work now: what came back, or its last checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := workspace.LoadSession(args[0])
			if err != nil {
				return fmt.Errorf("no record of a run in %s on this machine", args[0])
			}
			spec, err := mirrorSpecFor(s.Repo)
			if err != nil {
				return err
			}
			if spec == nil {
				return fmt.Errorf("no mirror is configured (sandbox-cli mirror --help)")
			}
			ref, kind := s.BroughtBack, mirror.KindBringBack
			if ref == "" {
				ref, kind = s.Checkpoint, mirror.KindCheckpoint
			}
			if ref == "" {
				return fmt.Errorf("%s has no work on this machine to mirror: nothing brought back and no checkpoint", args[0])
			}
			msg, err := mirrorWork(cmd.Context(), spec, &s, ref, kind)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
}

func newMirrorFetchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fetch KEY|SANDBOX",
		Short: "Fetch mirrored work into refs/sandbox/mirror/<sandbox>, checked before it lands",
		Long: "Fetches an object (by key, or a sandbox's newest) into refs/sandbox/mirror/<sandbox>.\n" +
			"No branch moves; merging stays a git command you run. Before anything lands the\n" +
			"bundle must verify, carry exactly the commit its name says, and descend from this\n" +
			"repository's root. On the machine that mirrored it, the commit must also be the one\n" +
			"recorded at upload — so a bucket that was shared or tampered with cannot hand back\n" +
			"different work. Elsewhere there is no such record, and fetch says so.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, _, err := repoMirror()
			if err != nil {
				return err
			}
			key := args[0]
			if !strings.Contains(key, "/") {
				list, err := m.List(cmd.Context(), key)
				if err != nil {
					return err
				}
				if len(list) == 0 {
					return fmt.Errorf("nothing mirrored for %s", termsafe.Clean(key))
				}
				key = list[0].Key
			}
			recorded := ""
			if s, err := workspace.LoadSession(strings.Split(key, "/")[1]); err == nil {
				for _, r := range s.Mirrored {
					if r.Key == key {
						recorded = r.SHA
					}
				}
			}
			ref, err := m.Fetch(cmd.Context(), key, recorded)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), ref)
			if recorded == "" {
				fmt.Fprintln(os.Stderr, "sandbox-cli: no record of this upload on this machine, so it was checked for consistency and against this repository's root, not against what was uploaded; review it before merging")
			}
			fmt.Fprintf(os.Stderr, "sandbox-cli: review: git log -p HEAD..%s · merge: git merge %s\n", ref, ref)
			return nil
		},
	}
}
