package agenthome

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// ToolsInstallLabel marks an installer sandbox, with the agent's name: while
// one holds a tools volume, runs leave the volume alone rather than wait.
const ToolsInstallLabel = "agent.tools.install"

// ToolsVolumeMB bounds a tools volume. It is sparse, so this is a ceiling, not
// what it costs: an npm agent takes a few hundred MiB of it.
const ToolsVolumeMB = 2048

// installTimeoutSecs bounds the installer: an install that hangs on a network
// it cannot reach should not hold the run that asked for it for longer.
const installTimeoutSecs = 600

// AgentTools returns the read-only mount of d's tools volume, so the run finds
// d installed instead of installing it again. The first run of an agent on an
// endpoint makes the volume: it creates it, installs into it from an installer
// sandbox (agents.Tools says why that sandbox and nothing else writes it), and
// then mounts it like every later run.
//
// It returns nil, and the run installs the agent itself as before, when d is in
// the image, the endpoint has no volumes or gives sandboxes no network (an
// installer could not reach a registry, and failing at it costs the run a
// minute of retries before its own install fails the same way), another client's installer holds the
// volume right now, or the install failed. A cache is not a control: missing it
// costs time, never safety, so nothing here refuses a run.
func AgentTools(ctx context.Context, c *api.Client, caps api.Capabilities, d agents.Descriptor, logf func(string, ...any)) *api.VolumeMount {
	t, ok := d.Tools()
	if !ok || !caps.Has(api.CapVolumes) || caps.Network.Default.Mode == api.NetworkNone {
		return nil
	}
	name := t.Volume(d.Name)
	mount := &api.VolumeMount{Name: name, Path: agents.ToolsDir, ReadOnly: true}
	vols, err := c.Volumes(ctx)
	if err != nil {
		return nil
	}
	for _, v := range vols {
		if v.Name != name {
			continue
		}
		if v.AttachedTo != "" {
			// Readers share it; an installer has it alone.
			if sb, err := c.Sandbox(ctx, v.AttachedTo); err != nil || sb.Labels[ToolsInstallLabel] != "" {
				return nil
			}
		}
		return mount
	}

	if _, err := c.CreateVolume(ctx, api.CreateVolumeRequest{Name: name, SizeMB: ToolsVolumeMB}); err != nil {
		// Another client created it a moment ago and is installing.
		return nil
	}
	logf("preparing volume %s for %s, once: if the image does not carry it, it is installed there for every later run on this endpoint", name, d.Name)
	if err := installTools(ctx, c, d, t, name); err != nil {
		// A volume left half-installed would be skipped by its missing ready
		// marker, but would also never be retried; remove it so the next run
		// installs afresh.
		_ = c.DeleteVolume(context.Background(), name)
		logf("installing %s into a volume failed, so this run installs it itself: %v", d.Name, err)
		return nil
	}
	for _, old := range supersededTools(vols, d.Name, name) {
		logf("volume %s held an earlier install of %s and is no longer used: sandbox-cli volume rm %s", old, d.Name, old)
	}
	return mount
}

// supersededTools names the earlier tools volumes of an agent: its name, then
// eight hex digits, but not current. Said rather than deleted: a volume is
// the user's to remove, and a name of that shape is only very probably ours.
func supersededTools(vols []api.Volume, agent, current string) []string {
	var out []string
	prefix := "agent-" + agent + "-"
	for _, v := range vols {
		rest, ok := strings.CutPrefix(v.Name, prefix)
		if !ok || v.Name == current || len(rest) != 8 || strings.Trim(rest, "0123456789abcdef") != "" {
			continue
		}
		out = append(out, v.Name)
	}
	return out
}

// installTools runs the install in a sandbox of its own: the volume writable,
// no workspace cloned in, no environment but its label, and terminated before
// any run mounts the volume, so the volume's last writer is gone first.
func installTools(ctx context.Context, c *api.Client, d agents.Descriptor, t agents.Tools, name string) error {
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{
		Labels:  map[string]string{ToolsInstallLabel: d.Name},
		Volumes: []api.VolumeMount{{Name: name, Path: agents.ToolsDir}},
	})
	if err != nil {
		return err
	}
	defer c.TerminateSandbox(context.Background(), sb.ID)
	res, err := c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"sh", "-c", t.InstallScript()}, TimeoutSecs: installTimeoutSecs})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("exit %d: %s", res.ExitCode, lastLine(res.Stderr))
	}
	// Terminated here, not deferred past the return: a run mounting the
	// volume is refused while its writer is alive.
	if err := c.TerminateSandbox(ctx, sb.ID); err != nil {
		return errors.New("the installer sandbox did not stop: " + err.Error())
	}
	return nil
}

// lastLine is the end of an installer's stderr, made safe to print: it came
// from the guest.
func lastLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 200 {
		s = s[len(s)-200:]
	}
	return termsafe.Clean(s)
}
