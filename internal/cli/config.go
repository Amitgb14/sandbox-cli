package cli

import (
	"fmt"
	"os"
	"time"

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
func applyConfig(rf *runFlags, project string, req *api.CreateSandboxRequest, caps api.Capabilities) error {
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
	if req.Network, err = resolveNetwork(cfg, rf.allow, rf.deny, caps); err != nil {
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

// resolveNetwork is the network a run asks for: the resolved config, with
// --allow and --deny on top. It is the one place that decides it, for run,
// the agent commands, routing, Studio and every fleet task, so none of them
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
func resolveNetwork(cfg policy.Config, allow, deny []string, caps api.Capabilities) (*api.NetworkPolicy, error) {
	switch cfg.Network.Mode {
	case "none":
		return &api.NetworkPolicy{Mode: api.NetworkNone}, nil
	case "default":
		return &api.NetworkPolicy{Mode: api.NetworkOpen, Deny: deny}, nil
	}
	named := policy.DedupeDomains(append(append([]string{}, cfg.Network.Allow...), allow...))
	if cfg.Network.BaselineEnabled() && len(named) == 0 && len(deny) == 0 {
		return nil, nil
	}
	var domains []string
	if cfg.Network.BaselineEnabled() {
		domains = append(domains, caps.Network.Default.Allow...)
	}
	domains = policy.DedupeDomains(append(domains, named...))
	if len(domains) == 0 {
		return nil, fmt.Errorf("the egress allowlist resolves to nothing (profile %s, baseline off, no hosts named): "+
			"name the hosts this run needs with --allow or network.allow, or ask for --network none", cfg.Profile)
	}
	return &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: domains, Deny: deny}, nil
}
