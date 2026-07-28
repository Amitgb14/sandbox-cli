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
	StartedAt  time.Time         // zero if never started
	FinishedAt time.Time         // zero while still running
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
	// Remove deletes a stopped container along with its logs.
	Remove(ctx context.Context, id string) error
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
	return d.inspect(ctx, ids)
}

// dockerInspect mirrors the subset of `docker inspect` output we rely on.
type dockerInspect struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status     string `json:"Status"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
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
		infos = append(infos, ContainerInfo{
			ID:         r.ID,
			Name:       strings.TrimPrefix(r.Name, "/"),
			Labels:     r.Config.Labels,
			State:      r.State.Status,
			ExitCode:   r.State.ExitCode,
			StartedAt:  parseDockerTime(r.State.StartedAt),
			FinishedAt: parseDockerTime(r.State.FinishedAt),
		})
	}
	// Newest first: the run you just started is the one you are looking for.
	sort.Slice(infos, func(i, j int) bool { return infos[i].StartedAt.After(infos[j].StartedAt) })
	return infos, nil
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
