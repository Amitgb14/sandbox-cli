package cli

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/creds"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
)

// loadConfig is every config load the CLI makes: the layered load, then the
// refusal of keys this version does not read. After the load, so a project
// file's trust refusals come first — a key that would widen the boundary is
// refused as such, not as merely dead.
//
// ov carries the run's own network flags, which the profile is checked with:
// a flag outranks every file but not the profile, so `--network open` cannot
// take a prod run out of what prod requires.
func loadConfig(project, configPath, profile string, ov policy.Overrides) (policy.Config, error) {
	cfg, err := policy.LoadProfileWith(project, configPath, profile, ov)
	if err != nil {
		return cfg, err
	}
	for _, p := range []string{policy.UserConfigPath(), policy.FindProjectConfig(project), configPath} {
		if err := policy.CheckLiveKeys(p); err != nil {
			return cfg, err
		}
	}
	// The merged result, checked as a whole: a mode nobody defines, a secret
	// with two sources, a reserved name in env: or secrets:. beta.15 ran this
	// on every load and the rewrite had dropped the call, so a typo'd
	// network.mode ran as the server's default.
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// applyConfig layers the user's and the project's configuration under the
// flags: built-in defaults, then ~/.config/sandbox/config.yaml, then the nearest
// .sandbox.yaml (whose privileged keys are refused by policy's trust rules),
// then the profile, then the flags. Whatever a flag sets wins.
//
// What crosses into the request: the image, environment (constant values,
// names forwarded from the host, and secrets resolved here on the host by the
// credential broker), and network. The prod profile turns persisted logins
// off, so a refresh token never enters a sandbox there.
func applyConfig(rf *runFlags, project string, req *api.CreateSandboxRequest, caps api.Capabilities, agentHost string) error {
	ov, err := rf.overrides()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(project, rf.configPath, rf.profile, ov)
	if err != nil {
		return err
	}
	if rf.image == "" && cfg.Image != "" && cfg.Image != policy.DefaultImage {
		req.Image = cfg.Image
	}
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	set := func(k, v string) {
		if _, flagged := req.Env[k]; !flagged {
			req.Env[k] = v
		}
	}
	for k, v := range cfg.Env {
		set(k, v)
	}
	// Before env_allow and secrets but after the config's env: a TZ the user
	// set, by --env or in their config, is their answer.
	if tz := hostTimezone(); tz != "" {
		set("TZ", tz)
	}
	for _, name := range cfg.EnvAllow {
		if v, ok := os.LookupEnv(name); ok {
			set(name, v)
		}
	}
	if len(cfg.Secrets) > 0 {
		src := map[string]creds.Source{}
		for name, s := range cfg.Secrets {
			src[name] = creds.Source{File: s.File, Command: s.Command, Env: s.Env}
		}
		vals, err := creds.Resolve(src)
		if err != nil {
			return fmt.Errorf("secrets: %w", err)
		}
		warnLongLivedSecrets(vals, time.Now())
		for _, v := range vals {
			set(v.Name, v.Value)
		}
	}
	if req.Network, err = resolveNetwork(cfg, rf.allow, rf.deny, caps, agentHost); err != nil {
		return err
	}
	if !cfg.PersistAuthEnabled() {
		rf.noPersistAuth = true
	}
	return nil
}

// overrides is the run's network flags as policy sees them. The flag says
// "open" where the config file says "default": one spelling for the API's
// mode, one for beta.15's key, and the same thing.
func (rf *runFlags) overrides() (policy.Overrides, error) {
	ov := policy.Overrides{Allow: rf.allow}
	switch rf.network {
	case "":
	case api.NetworkNone, api.NetworkAllowlist:
		ov.NetworkMode = rf.network
	case api.NetworkOpen:
		ov.NetworkMode = "default"
	default:
		return ov, fmt.Errorf("--network %q: none, allowlist or open", rf.network)
	}
	return ov, nil
}

// withAgentAPI adds an agent run's own API host to an allowlist that lacks it.
//
// An agent that cannot reach its model does not run at all, so under any
// allowlist — the prod profile's, which starts empty, included — the agent's
// provider is the one name it always gets. It is the host the agent was going
// to talk to anyway, chosen by sandbox-cli's descriptor table and never by the
// repository or the agent; every other name still has to be asked for. Open
// and none are left as they are: open already reaches it, and none was a
// request for nothing.
func withAgentAPI(n *api.NetworkPolicy, agent *agents.Descriptor, caps api.Capabilities) *api.NetworkPolicy {
	if agent == nil || agent.ProviderHost == "" {
		return n
	}
	host := agent.ProviderHost
	if n == nil {
		// The server's default applies. Only an allowlist default lacking the
		// host needs spelling out; an open or none default stays the server's.
		d := caps.Network.Default
		if d.Mode != api.NetworkAllowlist || slices.Contains(d.Allow, host) {
			return nil
		}
		return &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: append(append([]string{}, d.Allow...), host)}
	}
	if n.Mode != api.NetworkAllowlist || slices.Contains(n.Allow, host) {
		return n
	}
	out := *n
	out.Allow = append(append([]string{}, n.Allow...), host)
	return &out
}

// resolveNetwork is the network a run asks for: the resolved config, with
// --allow and --deny on top. It is the one place that decides it, for run,
// the agent commands, routing and Studio, so none of them
// can disagree about what a config means.
//
// nil asks for the server's default, and is returned only when nothing was
// asked: an allowlist with the baseline on, no hosts named and none denied.
// The client's baseline is the server's default allowlist, so that is the same
// list, and a server whose default is narrower (or none) keeps it.
//
// An allowlist that resolves to nothing is refused. That is prod's shape until
// it names its hosts, and sending nothing instead would hand the run the
// server's default, whose baseline prod exists to turn off.
func resolveNetwork(cfg policy.Config, allow, deny []string, caps api.Capabilities, agentHost string) (*api.NetworkPolicy, error) {
	switch cfg.Network.Mode {
	case "none":
		return &api.NetworkPolicy{Mode: api.NetworkNone}, nil
	case "default":
		return &api.NetworkPolicy{Mode: api.NetworkOpen, Deny: deny}, nil
	}
	named := policy.DedupeDomains(append(append([]string{}, cfg.Network.Allow...), allow...))
	// Nothing asked for: the server's default, whatever it is (open on a
	// sandboxd with no policy file). An explicit allowlist is never this:
	// sending nothing for it would hand the run the server's default, which
	// may be open.
	if cfg.Network.Mode == "" && len(named) == 0 && len(deny) == 0 {
		return nil, nil
	}
	var domains []string
	if cfg.Network.BaselineEnabled() {
		// The server's default list, or the built-in baseline when its default
		// is not an allowlist (open, say): --allow adds to agents' APIs and
		// registries, as it always has, rather than replacing them.
		if caps.Network.Default.Mode == api.NetworkAllowlist {
			domains = append(domains, caps.Network.Default.Allow...)
		} else {
			domains = append(domains, policy.BaselineEgress()...)
		}
	}
	// An agent run's own API is the one host an allowlist always has: an agent
	// that cannot reach its model does not run at all (see withAgentAPI). It
	// counts as named, so prod's empty list resolves to just that host.
	if agentHost != "" {
		named = append(named, agentHost)
	}
	domains = policy.DedupeDomains(append(domains, named...))
	if len(domains) == 0 {
		return nil, fmt.Errorf("the egress allowlist resolves to nothing (profile %s, baseline off, no hosts named): "+
			"name the hosts this run needs with --allow or network.allow, or ask for --network none", cfg.Profile)
	}
	return &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: domains, Deny: deny}, nil
}
