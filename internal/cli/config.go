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
func loadConfig(project, configPath, profile string) (policy.Config, error) {
	cfg, err := policy.LoadProfile(project, configPath, profile)
	if err != nil {
		return cfg, err
	}
	for _, p := range []string{policy.UserConfigPath(), policy.FindProjectConfig(project), configPath} {
		if err := policy.CheckLiveKeys(p); err != nil {
			return cfg, err
		}
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
	cfg, err := loadConfig(project, rf.configPath, rf.profile)
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
	if req.Network == nil {
		switch cfg.Network.Mode {
		case "none":
			req.Network = &api.NetworkPolicy{Mode: api.NetworkNone}
		case "allowlist":
			p := &api.NetworkPolicy{Mode: api.NetworkAllowlist}
			if cfg.Network.BaselineEnabled() {
				p.Allow = append(p.Allow, caps.Network.Default.Allow...)
			}
			p.Allow = append(p.Allow, cfg.Network.Allow...)
			if len(p.Allow) > 0 {
				req.Network = p
			}
		}
	}
	if !cfg.PersistAuthEnabled() {
		rf.noPersistAuth = true
	}
	return nil
}
