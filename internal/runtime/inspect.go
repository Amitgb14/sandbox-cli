package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ContainerInfo is what sandbox-cli needs to know about a container it started
// earlier: enough to say whether an agent is still working, and how it ended.
type ContainerInfo struct {
	ID         string
	Name       string            // without docker's leading "/"
	Labels     map[string]string // the sandbox.* labels the run was started with
	State      string            // "running", "exited", "created", "dead", …
	ExitCode   int               // meaningful once State is "exited"
	CreatedAt  time.Time         // when docker created it; always set
	StartedAt  time.Time         // zero if never started
	FinishedAt time.Time         // zero while still running

	// OpenStdin and TTY are how the container was started, and together they say
	// what attaching to it can do. A detached run has neither — `--detach`
	// replaces `-i`/`-it` on purpose — so attaching to one shows its output and
	// cannot type at it. `attach` reads these to say so up front, rather than
	// leaving someone typing into a container that is not listening.
	OpenStdin bool
	TTY       bool

	// What the container actually is, as docker recorded it at launch. These
	// come from the same `docker inspect` call the fields above do — it returns
	// the whole object either way, so surfacing them costs nothing.
	//
	// They exist because a supervision layer eventually has to answer "what was
	// this run allowed to reach", and the honest source is the container itself
	// rather than a config file that may have been edited since. A label says
	// what the launcher intended; these say what it got.
	Image    string   // the image reference it was started from
	User     string   // the user the guest runs as, as docker recorded it
	Command  []string // entrypoint + cmd, as executed
	Workdir  string
	Env      []string // NAME=VALUE, as docker holds them — see EnvNames
	Mounts   []MountInfo
	Security SecurityInfo

	// Runtime is the OCI runtime the engine recorded for this container
	// ("runc", "runsc", "kata-runtime", …), which is the one setting that
	// changes the *kind* of boundary rather than its degree. Read back rather
	// than remembered: a label says what the launcher asked for, this says what
	// it got. See StrongerIsolation.
	Runtime string

	// NetworkMode is docker's own word for it ("bridge", "none", a named
	// network), not this tool's posture. Whether an egress allowlist is in force
	// is a different question, answered by EgressAllowlisted.
	NetworkMode string
}

// MountInfo is one host path the container can reach.
type MountInfo struct {
	Source      string
	Destination string
	ReadWrite   bool
}

// SecurityInfo is the confinement docker applied, read back rather than assumed.
type SecurityInfo struct {
	CapDrop     []string
	CapAdd      []string
	SecurityOpt []string
	PidsLimit   int64 // 0 means unset
	MemoryBytes int64 // 0 means unlimited
	NanoCPUs    int64 // 0 means unlimited; 1e9 per CPU
}

// EnvNames returns the variable names in the container's environment, never
// their values.
//
// The whole point of the credential broker is that secret values stay off the
// argv and out of config files, and a supervision API is one more place a value
// must not surface — internal/audit records environment variables by name for
// exactly this reason, and has nowhere to put a value on purpose. Callers that
// want to show "what was forwarded" get the names; nothing here hands back what
// they were set to.
func (c ContainerInfo) EnvNames() []string {
	out := make([]string, 0, len(c.Env))
	for _, kv := range c.Env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out = append(out, kv[:i])
			continue
		}
		if kv != "" {
			out = append(out, kv)
		}
	}
	return out
}

// EgressAllowlisted reports whether this container was started with the egress
// allowlist in force, read from the control variable the entrypoint acts on.
func (c ContainerInfo) EgressAllowlisted() bool {
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, "SANDBOX_EGRESS_ALLOW=") && len(kv) > len("SANDBOX_EGRESS_ALLOW=") {
			return true
		}
	}
	return false
}

// Running reports whether the container is still executing its guest command.
func (c ContainerInfo) Running() bool { return c.State == "running" }

// Inspector is the optional capability of finding containers this tool started
// before. Like Starter it is kept out of the Runtime interface so a backend that
// cannot enumerate containers is still a valid Runtime.
type Inspector interface {
	// Containers returns every container (running or not) carrying all of the
	// given labels. An empty result is not an error.
	Containers(ctx context.Context, labels map[string]string) ([]ContainerInfo, error)
}

// Controller is the optional capability of acting on containers this tool
// started: reading their output, stopping them, and reaping them. Separate from
// Inspector so a read-only caller cannot reach the destructive operations.
type Controller interface {
	// Logs streams a container's output. With follow it blocks until the
	// container exits or ctx is cancelled.
	Logs(ctx context.Context, id string, follow bool, stdout, stderr io.Writer) error
	// Stop asks the guest to exit, killing it after a grace period.
	Stop(ctx context.Context, id string) error
	// Kill terminates the guest immediately, with no chance to finish what it was
	// writing. Separate from Stop because the difference is the difference between
	// an agent that closed the file it was editing and one that did not, so a
	// caller has to ask for it by name.
	Kill(ctx context.Context, id string) error
	// Remove deletes a stopped container along with its logs.
	Remove(ctx context.Context, id string) error
}

// Attacher is the optional capability of connecting a terminal to a container
// that is already running. Kept out of Controller because it is a different kind
// of act: Controller's methods do one thing and return, this one hands the
// caller's stdio to another process for as long as they want it.
type Attacher interface {
	Attach(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer) error
}

// Logs implements Controller.
func (d *DockerCLI) Logs(ctx context.Context, id string, follow bool, stdout, stderr io.Writer) error {
	args := []string{"logs"}
	if follow {
		args = append(args, "--follow")
	}
	cmd := exec.CommandContext(ctx, d.bin(), append(args, id)...)
	// Container stdout and stderr stay on their own streams, so piping a fleet
	// agent's output into a file does not swallow its diagnostics.
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	// Following logs is normally ended by the user (Ctrl-C) or by the caller's
	// context; neither is a failure worth reporting.
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// Stop implements Controller. It uses docker's default grace period, which sends
// SIGTERM first so an agent can finish writing whatever it was writing.
func (d *DockerCLI) Stop(ctx context.Context, id string) error {
	if out, err := exec.CommandContext(ctx, d.bin(), "stop", id).CombinedOutput(); err != nil {
		return fmt.Errorf("stopping %s: %s", short(id), strings.TrimSpace(string(out)))
	}
	return nil
}

// Kill implements Controller. No grace period: the guest gets SIGKILL and never
// runs another instruction, which is why `sandbox-cli kill` reaches this only
// when asked with --force.
func (d *DockerCLI) Kill(ctx context.Context, id string) error {
	if out, err := exec.CommandContext(ctx, d.bin(), "kill", id).CombinedOutput(); err != nil {
		return fmt.Errorf("killing %s: %s", short(id), strings.TrimSpace(string(out)))
	}
	return nil
}

// Attach implements Attacher.
//
// --sig-proxy=false is the load-bearing part. By default `docker attach` forwards
// signals to the container, so the Ctrl-C someone presses to stop *looking* at an
// agent would instead stop the agent — from a command whose whole purpose is to
// observe one. Attaching is a way to look, and looking must not be able to end a
// run; `kill` is a separate word on purpose. With sig-proxy off, Ctrl-C ends this
// client and leaves the guest working.
func (d *DockerCLI) Attach(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, d.bin(), "attach", "--sig-proxy=false", id)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	// Detaching is how an attach is *meant* to end, and the caller's context being
	// cancelled is the same event arriving another way — neither is a failure worth
	// reporting. The same bargain Logs makes.
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// Remove implements Controller.
func (d *DockerCLI) Remove(ctx context.Context, id string) error {
	if out, err := exec.CommandContext(ctx, d.bin(), "rm", id).CombinedOutput(); err != nil {
		return fmt.Errorf("removing %s: %s", short(id), strings.TrimSpace(string(out)))
	}
	return nil
}

// short truncates a container id to the 12 characters docker itself displays.
func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Containers implements Inspector for the docker CLI backend.
//
// Two calls rather than one: `docker ps --format` reports labels as a flat
// comma-joined string, which is ambiguous the moment a label value contains a
// comma (a repository path legitimately can). `docker inspect` returns them as
// real JSON objects, along with the exit code and timestamps that `ps` only
// exposes inside a human-readable status string.
func (d *DockerCLI) Containers(ctx context.Context, labels map[string]string) ([]ContainerInfo, error) {
	args := []string{"ps", "-aq"}
	for _, k := range sortedKeys(labels) {
		args = append(args, "--filter", "label="+k+"="+labels[k])
	}
	out, err := exec.CommandContext(ctx, d.bin(), args...).Output()
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil, nil
	}
	// One `docker inspect` per batch rather than one for every id at once: a long
	// fleet history is a long argv, and the limit it eventually hits (E2BIG) would
	// show up as an inscrutable exec failure on exactly the machines that have been
	// using this the longest.
	var infos []ContainerInfo
	for start := 0; start < len(ids); start += inspectBatch {
		end := start + inspectBatch
		if end > len(ids) {
			end = len(ids)
		}
		batch, err := d.inspect(ctx, ids[start:end])
		if err != nil {
			return nil, err
		}
		infos = append(infos, batch...)
	}
	sortNewestFirst(infos)
	return infos, nil
}

// inspectBatch caps how many container ids go on one `docker inspect` argv.
const inspectBatch = 100

// dockerInspect mirrors the subset of `docker inspect` output we rely on.
type dockerInspect struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	State   struct {
		Status     string `json:"Status"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	Config struct {
		Labels     map[string]string `json:"Labels"`
		OpenStdin  bool              `json:"OpenStdin"`
		Tty        bool              `json:"Tty"`
		Image      string            `json:"Image"`
		User       string            `json:"User"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		Env        []string          `json:"Env"`
		WorkingDir string            `json:"WorkingDir"`
	} `json:"Config"`
	HostConfig struct {
		Runtime     string   `json:"Runtime"`
		NetworkMode string   `json:"NetworkMode"`
		CapDrop     []string `json:"CapDrop"`
		CapAdd      []string `json:"CapAdd"`
		SecurityOpt []string `json:"SecurityOpt"`
		PidsLimit   *int64   `json:"PidsLimit"`
		Memory      int64    `json:"Memory"`
		NanoCpus    int64    `json:"NanoCpus"`
	} `json:"HostConfig"`
	Mounts []struct {
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
}

func (d *DockerCLI) inspect(ctx context.Context, ids []string) ([]ContainerInfo, error) {
	out, err := exec.CommandContext(ctx, d.bin(), append([]string{"inspect"}, ids...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("inspecting containers: %w", err)
	}
	var raw []dockerInspect
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing docker inspect output: %w", err)
	}
	infos := make([]ContainerInfo, 0, len(raw))
	for _, r := range raw {
		mounts := make([]MountInfo, 0, len(r.Mounts))
		for _, m := range r.Mounts {
			mounts = append(mounts, MountInfo{Source: m.Source, Destination: m.Destination, ReadWrite: m.RW})
		}
		var pids int64
		if r.HostConfig.PidsLimit != nil {
			pids = *r.HostConfig.PidsLimit
		}
		infos = append(infos, ContainerInfo{
			ID:          r.ID,
			CreatedAt:   parseDockerTime(r.Created),
			Name:        strings.TrimPrefix(r.Name, "/"),
			Labels:      r.Config.Labels,
			State:       r.State.Status,
			ExitCode:    r.State.ExitCode,
			StartedAt:   parseDockerTime(r.State.StartedAt),
			FinishedAt:  parseDockerTime(r.State.FinishedAt),
			OpenStdin:   r.Config.OpenStdin,
			TTY:         r.Config.Tty,
			Image:       r.Config.Image,
			User:        r.Config.User,
			Command:     append(append([]string(nil), r.Config.Entrypoint...), r.Config.Cmd...),
			Workdir:     r.Config.WorkingDir,
			Env:         r.Config.Env,
			Mounts:      mounts,
			Runtime:     r.HostConfig.Runtime,
			NetworkMode: r.HostConfig.NetworkMode,
			Security: SecurityInfo{
				CapDrop:     r.HostConfig.CapDrop,
				CapAdd:      r.HostConfig.CapAdd,
				SecurityOpt: r.HostConfig.SecurityOpt,
				PidsLimit:   pids,
				MemoryBytes: r.HostConfig.Memory,
				NanoCPUs:    r.HostConfig.NanoCpus,
			},
		})
	}
	return infos, nil
}

// sortNewestFirst orders containers so the run you just started is first.
//
// "Newest" is creation, not start. A container in `created` state has a zero
// StartedAt, so sorting on that put a just-launched run *last*, behind the
// previous exited one — and a caller asking for the latest container would then
// read the old run's exit code and verify label, which is how `land` would judge
// a new run by an old verdict. Creation order is launch order, and docker always
// sets it.
func sortNewestFirst(infos []ContainerInfo) {
	sort.Slice(infos, func(i, j int) bool { return infos[i].CreatedAt.After(infos[j].CreatedAt) })
}

// parseDockerTime converts docker's RFC3339-with-nanoseconds timestamps. Docker
// writes the zero value as "0001-01-01T00:00:00Z" for containers that never ran
// or have not finished, which parses to Go's zero Time — exactly what callers
// check for — so an unparseable value is simply reported the same way.
func parseDockerTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
