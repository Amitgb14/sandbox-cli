package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Jobs and agent runs on a gateway: work that runs after the command that
// started it has returned. The gateway makes a sandbox per run, starts the
// command or the agent, keeps the output and the files the job names, and
// takes the sandbox down. Nothing here runs anything locally; these are
// thin clients of /v1/jobs and /v1/agent-runs.
//
// An agent's login cannot come along: the CLI's run copies the agent's saved
// login into the sandbox from this machine, and a job's runs start on the
// fleet with nobody's machine involved. An agent on a fleet authenticates
// with an API key the job names as a secret (`sandbox-cli secret set
// ANTHROPIC_API_KEY`, then `--secret ANTHROPIC_API_KEY`).

func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Run commands and agents on a gateway's fleet, after you have gone: one run or a batch",
		Long: "A job is a command (or an agent and a prompt) run in a fresh sandbox per run,\n" +
			"by the gateway, with retries, a timeout and a parallelism limit. Its output\n" +
			"and the files it names are kept for a day after it finishes. Gateway only.",
	}
	var ctxFlag string
	var file string
	var wait bool
	run := &cobra.Command{
		Use:   "run -f JOB.yaml",
		Short: "Start a job from a YAML (or JSON) spec; -f - reads it from stdin",
		Long: "The spec's keys are the API's (docs: README, \"Agents and jobs on a fleet\"):\n" +
			"name, image, command | agent + prompt, prompts (a batch), parallelism,\n" +
			"completions, retries, timeout_secs, env, secrets, network, resources,\n" +
			"from_snapshot, keep: {output, files}, notify. An unknown key is refused.",
		Example: "  sandbox-cli job run -f job.yaml\n  sandbox-cli job run -f job.yaml --wait",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := readJobSpec(file, cmd.InOrStdin())
			if err != nil {
				return err
			}
			c, err := requireGateway(cmd.Context(), ctxFlag, "job")
			if err != nil {
				return err
			}
			j, err := c.CreateJob(cmd.Context(), spec)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), termsafe.Clean(j.ID))
			if !wait {
				return nil
			}
			j, err = waitForJob(cmd.Context(), c, j.ID)
			if err != nil {
				return err
			}
			printJob(cmd.OutOrStdout(), j)
			if j.State != api.JobSucceeded {
				return exitError{code: 1}
			}
			return nil
		},
	}
	run.Flags().StringVarP(&file, "file", "f", "", "the job spec (YAML or JSON); - for stdin")
	run.Flags().BoolVar(&wait, "wait", false, "wait for the job to finish and print it; exit 1 unless it succeeded")
	_ = run.MarkFlagRequired("file")

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List your jobs, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "job")
			if err != nil {
				return err
			}
			jobs, err := c.Jobs(cmd.Context())
			if err != nil {
				return err
			}
			w := newTable(cmd.OutOrStdout())
			fmt.Fprintln(w, "ID\tNAME\tSTATE\tRUNS\tSUCCEEDED\tFAILED\tCREATED")
			for _, j := range jobs {
				total := j.Queued + j.Running + j.Succeeded + j.Failed
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n", termsafe.Clean(j.ID), termsafe.Clean(j.Name),
					termsafe.Clean(j.State), total, j.Succeeded, j.Failed, j.Created.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	get := &cobra.Command{
		Use:   "get ID",
		Short: "Show a job and each of its runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "job")
			if err != nil {
				return err
			}
			j, err := c.Job(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			printJob(cmd.OutOrStdout(), j)
			return nil
		},
	}
	var outFile string
	output := &cobra.Command{
		Use:   "output ID RUN",
		Short: "Print what a run kept of its output (stdout, then stderr to stderr); --file prints a kept file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 0 {
				return fmt.Errorf("RUN %q: want a run number (0, 1, ...)", args[1])
			}
			c, err := requireGateway(cmd.Context(), ctxFlag, "job")
			if err != nil {
				return err
			}
			if outFile != "" {
				data, err := c.JobFile(cmd.Context(), args[0], n, outFile)
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			out, err := c.JobOutput(cmd.Context(), args[0], n)
			if err != nil {
				return err
			}
			// Raw, as `logs` prints a process's output: it is the user's own
			// run's, going to their own terminal or pipe.
			_, _ = cmd.OutOrStdout().Write(out.Stdout)
			_, _ = cmd.ErrOrStderr().Write(out.Stderr)
			if out.Truncated {
				fmt.Fprintln(cmd.ErrOrStderr(), "sandbox-cli: the output was longer than the gateway keeps; this is its start")
			}
			return nil
		},
	}
	output.Flags().StringVar(&outFile, "file", "", "print this kept file (a path from keep.files) instead")
	cancel := &cobra.Command{
		Use:   "cancel ID",
		Short: "Cancel a job: runs not started never are, and running ones' sandboxes are terminated",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "job")
			if err != nil {
				return err
			}
			j, err := c.CancelJob(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", termsafe.Clean(j.ID), termsafe.Clean(j.State))
			return nil
		},
	}
	for _, sub := range []*cobra.Command{run, ls, get, output, cancel} {
		sub.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to use")
		cmd.AddCommand(sub)
	}
	return cmd
}

func newAgentRunCmd() *cobra.Command {
	var ctxFlag, image, name, notify string
	var secrets, env, files []string
	var timeout durationFlag
	var retries int
	var wait bool
	cmd := &cobra.Command{
		Use:   "agent-run AGENT PROMPT",
		Short: "Run an agent on a prompt on a gateway's fleet, unattended; prints the job id",
		Long: "Starts AGENT in its headless mode on PROMPT, in a fresh sandbox the gateway\n" +
			"makes and takes down: a job of one run (sandbox-cli job). Your saved login\n" +
			"does not come along; the agent authenticates with an API key you keep on the\n" +
			"gateway as a secret, named with --secret:\n\n" +
			"  sandbox-cli secret set ANTHROPIC_API_KEY < key.txt\n" +
			"  sandbox-cli agent-run claude \"fix the failing test\" --secret ANTHROPIC_API_KEY --wait\n\n" +
			"The agent must be in the image (or installable from it). With --wait, waits,\n" +
			"prints the run's output and exits with the agent's exit code.\n\n" +
			"Agents: " + strings.Join(agents.Names(), ", ") + ".",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := agents.Lookup(args[0]); !ok {
				return fmt.Errorf("agent %q: want one of %s", args[0], strings.Join(agents.Names(), ", "))
			}
			req := api.AgentRunRequest{Agent: args[0], Prompt: args[1], Name: name, Image: image,
				Retries: retries, TimeoutSecs: timeout.secs, Secrets: secrets, Notify: notify}
			var err error
			if req.Env, err = parseJobEnv(env); err != nil {
				return err
			}
			if len(files) > 0 {
				req.Keep = &api.JobKeep{Files: files}
			}
			c, err := requireGateway(cmd.Context(), ctxFlag, "agent-run")
			if err != nil {
				return err
			}
			j, err := c.AgentRun(cmd.Context(), req)
			if err != nil {
				return err
			}
			if !wait {
				fmt.Fprintln(cmd.OutOrStdout(), termsafe.Clean(j.ID))
				fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-cli: started job %s. Follow: sandbox-cli job get %s · Output: sandbox-cli job output %s 0\n",
					termsafe.Clean(j.ID), termsafe.Clean(j.ID), termsafe.Clean(j.ID))
				return nil
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-cli: job %s started; waiting\n", termsafe.Clean(j.ID))
			if j, err = waitForJob(cmd.Context(), c, j.ID); err != nil {
				return err
			}
			run := j.Runs[0]
			if out, err := c.JobOutput(cmd.Context(), j.ID, 0); err == nil {
				_, _ = cmd.OutOrStdout().Write(out.Stdout)
				_, _ = cmd.ErrOrStderr().Write(out.Stderr)
			}
			if run.State != api.RunSucceeded {
				fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-cli: the run %s: %s\n", termsafe.Clean(run.State), termsafe.Clean(run.Error))
				if run.ExitCode != nil && *run.ExitCode != 0 {
					return exitError{code: *run.ExitCode}
				}
				return exitError{code: 1}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ctxFlag, "context", "", "which endpoint to use")
	f.StringArrayVar(&secrets, "secret", nil, "a gateway secret to set in the run's environment, by name (repeatable)")
	f.StringArrayVarP(&env, "env", "e", nil, "KEY=VALUE, or KEY to send this machine's value (repeatable)")
	f.StringVar(&image, "image", "", "image to run (default: the fleet's)")
	f.StringVar(&name, "name", "", "name the job")
	f.Var(&timeout, "timeout", "how long the agent may run, e.g. 30m (default: the gateway's, 1h)")
	f.IntVar(&retries, "retries", 0, "attempts after a failed one")
	f.StringArrayVar(&files, "keep-file", nil, "a guest file to keep when the run ends (absolute path; repeatable)")
	f.StringVar(&notify, "notify", "", "a URL POSTed the run's state when it ends (https to a public address, unless the gateway allows private ones)")
	f.BoolVar(&wait, "wait", false, "wait, print the output, and exit with the agent's exit code")
	return cmd
}

// readJobSpec reads a job spec from a YAML or JSON file. YAML is decoded to
// plain values and then as JSON, so the keys are the API's and an unknown
// one is refused rather than ignored.
func readJobSpec(file string, stdin io.Reader) (api.JobSpec, error) {
	var data []byte
	var err error
	if file == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 4<<20))
	} else {
		data, err = os.ReadFile(file)
	}
	if err != nil {
		return api.JobSpec{}, err
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return api.JobSpec{}, fmt.Errorf("%s: %w", file, err)
	}
	if v == nil {
		return api.JobSpec{}, fmt.Errorf("%s: the spec is empty", file)
	}
	js, err := json.Marshal(v)
	if err != nil {
		return api.JobSpec{}, fmt.Errorf("%s: %w", file, err)
	}
	var spec api.JobSpec
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return api.JobSpec{}, fmt.Errorf("%s: %w", file, err)
	}
	return spec, nil
}

// parseJobEnv is --env: KEY=VALUE, or KEY for this machine's value.
func parseJobEnv(flags []string) (map[string]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, e := range flags {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			hv, set := os.LookupEnv(k)
			if !set {
				return nil, fmt.Errorf("--env %s: not set here; give KEY=VALUE", k)
			}
			v = hv
		}
		if !policy.ValidEnvName(k) {
			return nil, fmt.Errorf("--env %q: not a valid environment variable name", k)
		}
		if policy.IsReservedEnv(k) {
			return nil, fmt.Errorf("--env %s: %s", k, policy.ReservedEnvReason())
		}
		out[k] = v
	}
	return out, nil
}

// jobPoll is how often a waiting command asks after its job.
var jobPoll = time.Second

func waitForJob(ctx context.Context, c *api.Client, id string) (api.Job, error) {
	for {
		j, err := c.Job(ctx, id)
		if err != nil {
			return api.Job{}, err
		}
		if j.State != api.JobRunning {
			return j, nil
		}
		select {
		case <-ctx.Done():
			return api.Job{}, errors.New("stopped waiting; the job carries on (sandbox-cli job get " + id + ")")
		case <-time.After(jobPoll):
		}
	}
}

func printJob(w io.Writer, j api.Job) {
	t := newTable(w)
	fmt.Fprintf(t, "job\t%s\n", termsafe.Clean(j.ID))
	if j.Name != "" {
		fmt.Fprintf(t, "name\t%s\n", termsafe.Clean(j.Name))
	}
	fmt.Fprintf(t, "state\t%s\n", termsafe.Clean(j.State))
	what := strings.Join(j.Spec.Command, " ")
	if j.Spec.Agent != "" {
		what = "agent " + j.Spec.Agent
	}
	fmt.Fprintf(t, "runs\t%s\n", termsafe.Clean(what))
	fmt.Fprintf(t, "created\t%s\n", j.Created.Local().Format("2006-01-02 15:04:05"))
	if j.Expires != nil {
		fmt.Fprintf(t, "kept until\t%s\n", j.Expires.Local().Format("2006-01-02 15:04:05"))
	}
	if j.Error != "" {
		fmt.Fprintf(t, "error\t%s\n", termsafe.Clean(j.Error))
	}
	_ = t.Flush()
	if len(j.Runs) == 0 {
		return
	}
	fmt.Fprintln(w)
	t = newTable(w)
	fmt.Fprintln(t, "RUN\tSTATE\tEXIT\tATTEMPTS\tSANDBOX\tERROR")
	for _, r := range j.Runs {
		exit := "-"
		if r.ExitCode != nil {
			exit = strconv.Itoa(*r.ExitCode)
		}
		fmt.Fprintf(t, "%d\t%s\t%s\t%d\t%s\t%s\n", r.N, termsafe.Clean(r.State), exit, r.Attempts,
			termsafe.Clean(r.Sandbox), termsafe.Clean(r.Error))
	}
	_ = t.Flush()
}
