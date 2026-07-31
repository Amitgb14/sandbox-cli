package studioapi

import (
	"net/http"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// handleRunConfig is GET /v1/runs/{id}/config: the configuration this run
// actually got.
//
// Read back off the container, not recomputed from the config files as they
// stand now. That distinction is the whole value of the endpoint: config is
// edited, and a run reviewed a week later was confined by what it was given, not
// by what the file says today. Same reason `land` reads the recorded base label
// rather than asking what is checked out.
//
// One field is deliberately empty. `fields` is the layered provenance — which of
// default/user/project/flag supplied each value — and a container records the
// resolved answer, not the layers it came from. Reporting a guessed layer would
// be worse than reporting none, because the whole point of that view is to say
// *where* a setting came from.
func (s *Server) handleRunConfig(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	run := toRun(c, s.Engine)
	cfg := ResolvedConfig{
		Profile:  c.Labels[sandbox.LabelProfile],
		Image:    run.Image,
		Workdir:  run.Workdir,
		User:     run.Security.User,
		Home:     envValue(c, "HOME"),
		Engine:   s.Engine,
		Network:  run.Network,
		Security: run.Security,
		Mounts:   run.Mounts,
		EnvAllow: forwardedNames(c),
		Argv:     run.Command,
		Fields:   []ResolvedField{},
	}
	if cfg.Profile == "" {
		// Containers started before the profile label existed carry no answer,
		// and the server's current profile is not that run's — it is this one's.
		cfg.Profile = ""
	}
	for _, m := range run.Mounts {
		switch m.Origin {
		case "persisted-home":
			cfg.PersistAuth = true
		case "history":
			cfg.Sync = true
		}
	}
	writeJSON(w, http.StatusOK, cfg)
}

// forwardedNames lists the host variables that crossed into this container,
// excluding the ones sandbox-cli sets itself.
//
// Names only, never values — the rule the audit log keeps and for the same
// reason. The control variables are filtered out because they are instructions
// this tool gave the container, not settings forwarded from the host, and
// listing them as "forwarded" would misdescribe where they came from.
func forwardedNames(c runtime.ContainerInfo) []string {
	skip := map[string]bool{
		"PATH": true, "HOME": true, "HOSTNAME": true, "TZ": true,
		"NODE_VERSION": true, "YARN_VERSION": true,
	}
	out := []string{}
	for _, name := range c.EnvNames() {
		if skip[name] || strings.HasPrefix(name, "SANDBOX_") {
			continue
		}
		out = append(out, name)
	}
	return out
}
