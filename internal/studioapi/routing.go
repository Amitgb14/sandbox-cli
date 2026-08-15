package studioapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/routing"
)

// Which providers are answering right now.
//
// It exists because "is claude down" is the question routing is configured
// against, and until now the only way to ask it was to launch a run and see. A
// screen that shows the answer turns a chain from something you set up hopefully
// into something you can check.
//
// Two things this deliberately is not. It is **not** an auth check — the probe
// carries no credentials, so a healthy endpoint answering 401 is reported as up,
// and "my key is wrong" is a different question with a different answer. And it
// is **not** a status page: it reports what this machine can reach from here, so
// a corporate proxy or an offline laptop shows the same thing an outage would.
// The reason string is what tells those apart, which is why every unreachable
// answer carries one.

// providerCacheTTL bounds how often the providers are actually asked.
//
// The screen polls, several agents are probed per request, and a provider being
// hammered by a status widget is a poor way to repay it. Thirty seconds is short
// enough that an outage shows up while somebody is still looking at the page and
// long enough that a dashboard left open is not a load generator.
const providerCacheTTL = 30 * time.Second

type providerCache struct {
	mu   sync.Mutex
	at   time.Time
	seen []ProviderStatus
}

var providers providerCache

// handleRouting is GET /v1/routing: every agent that can be routed to, whether
// its provider is answering, and what a chain may contain.
func (s *Server) handleRouting(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, RoutingResponse{
		Providers: probeAll(r.Context(), s.Session.Cfg.Providers, r.URL.Query().Has("refresh")),
	})
}

// probeAll asks every routable agent's provider, or returns the recent answer.
//
// Probes run concurrently: they are independent network calls with a three
// second timeout each, and doing them in series would make a four-provider page
// take twelve seconds to say "everything is fine".
func probeAll(ctx context.Context, overrides map[string]string, force bool) []ProviderStatus {
	providers.mu.Lock()
	if !force && time.Since(providers.at) < providerCacheTTL && providers.seen != nil {
		out := providers.seen
		providers.mu.Unlock()
		return out
	}
	providers.mu.Unlock()

	names := agents.Names()
	out := make([]ProviderStatus, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		d, ok := agents.Lookup(name)
		if !ok {
			continue
		}
		host := routing.HostFor(name, overrides)
		out[i] = ProviderStatus{
			Agent: name,
			Host:  host,
			// Whether this host came from the user rather than from the
			// descriptor, so a screen can offer to change it and can say which
			// agents have nothing compiled in to begin with.
			Overridden: overrides[name] != "" && overrides[name] == host,
			// An agent with no verified non-interactive mode cannot be routed to
			// at all, and saying so here is what stops a UI offering it in a chain
			// that would hang the moment it fired.
			Routable: d.AutonomousArgs != nil,
		}
		if host == "" {
			// Nothing to ask. Reported as unprobed rather than as up, because
			// "not checked" and "checked and healthy" are different claims — and
			// a provider-agnostic agent like opencode has no single host whose
			// health would mean anything about it.
			continue
		}
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			a := routing.Probe(ctx, name, overrides)
			out[i].Probed = a.Probed
			out[i].Reachable = a.Reachable
			out[i].Reason = a.Reason
		}(i, name)
	}
	wg.Wait()

	providers.mu.Lock()
	providers.seen, providers.at = out, time.Now()
	providers.mu.Unlock()
	return out
}

// handleSetProviders is POST /v1/routing/providers: set which host is probed for
// an agent.
//
// POST rather than PUT because nothing else in this API uses PUT, and the CORS
// header names the methods a browser may preflight — adding one to that list to
// spell a single endpoint differently is a wider allowance for no gain.
//
// **A narrow endpoint, not a config writer**, and the narrowness is the whole
// design. Studio has never been able to write the user's configuration, and the
// day it can write one key it is one refactor from writing `image:` or
// `mounts:` — the keys trust.go refuses from a repository precisely because
// they choose what executes. So this writes exactly one map, into a file of its
// own, and validates every value as a host.
//
// It also does not touch config.yaml. A hand-written value there outranks this
// one and keeps its comments; see config.loadProviderOverrides.
func (s *Server) handleSetProviders(w http.ResponseWriter, r *http.Request) {
	var req ProvidersRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	clean := map[string]string{}
	for agent, host := range req.Providers {
		if _, ok := agents.Lookup(agent); !ok {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Errorf("unknown agent %q (known: %s)", agent, strings.Join(agents.Names(), ", ")))
			return
		}
		host = strings.TrimSpace(host)
		if host == "" {
			// An explicit "do not probe this one", which is a real thing to want
			// for an agent behind something this machine cannot reach.
			clean[agent] = ""
			continue
		}
		if err := validProviderHost(host); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		clean[agent] = host
	}
	if err := config.SaveProviderOverrides(clean); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// The running server holds a resolved config, so the change has to reach it
	// too or the next probe would use the old host until a restart.
	s.Session.Cfg.Providers = mergedProviders(s.Session.Cfg.Providers, clean)

	// Forced: the point of setting a host is to find out whether it answers, and
	// a cached "not checked" would sit there for thirty seconds looking like the
	// setting had not taken.
	writeJSON(w, http.StatusOK, RoutingResponse{
		Providers: probeAll(r.Context(), s.Session.Cfg.Providers, true),
	})
}

// mergedProviders folds a saved map into the running one. Keys absent from the
// save are left alone: the endpoint writes the set Studio manages, and a value
// that came from the user's own config.yaml is not Studio's to forget.
func mergedProviders(current, saved map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range current {
		out[k] = v
	}
	for k, v := range saved {
		out[k] = v
	}
	return out
}

// validProviderHost accepts a hostname or host:port and nothing else.
//
// No scheme, no path, no credentials: the probe builds `https://<host>/`, so a
// value carrying its own scheme or path would either fail confusingly or reach
// somewhere the person did not read. Rejecting here means the failure is a
// sentence on the screen rather than a probe that quietly asks the wrong thing.
func validProviderHost(host string) error {
	if strings.ContainsAny(host, "/?#@ ") || strings.Contains(host, "://") {
		return fmt.Errorf(
			"%q is not a host: give a hostname like api.openai.com, or host:port — no scheme, no path", host)
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return fmt.Errorf("%q is not a valid hostname", host)
	}
	return nil
}
