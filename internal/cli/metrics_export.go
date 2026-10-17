package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// exportFlags are run's and update's flags for a sandbox's metrics export.
// A header's value is never a flag's: it would sit in the process's
// arguments and the shell's history. --metrics-export-header names the
// environment variable holding it, and --metrics-export-header-secret a
// gateway secret.
type exportFlags struct {
	endpoint      string
	every         time.Duration
	headers       []string
	headerSecrets []string
}

func (ef *exportFlags) register(f *pflag.FlagSet, update bool) {
	usage := "push the sandbox's CPU, memory, network and disk to this OTLP/HTTP endpoint (your collector; https)"
	if update {
		usage = `push the sandbox's usage to this OTLP/HTTP endpoint, replacing any export; "none" stops it`
	}
	f.StringVar(&ef.endpoint, "metrics-export", "", usage)
	f.DurationVar(&ef.every, "metrics-export-every", 0, "how often to push, e.g. 30s (default 60s; the server sets the shortest)")
	f.StringArrayVar(&ef.headers, "metrics-export-header", nil, "NAME=ENV: send header NAME with the value of the environment variable ENV, e.g. Authorization=OTEL_AUTH (repeatable)")
	f.StringArrayVar(&ef.headerSecrets, "metrics-export-header-secret", nil, "NAME=SECRET: through a gateway, send header NAME with the value of your secret SECRET (repeatable; sandbox-cli secret set)")
}

// export is the request's metrics export, nil when no flag asked for one.
// On update, "none" is an export with no endpoint, which stops it.
func (ef *exportFlags) export() (*api.MetricsExport, error) {
	if ef.endpoint == "" {
		if ef.every != 0 || len(ef.headers) > 0 || len(ef.headerSecrets) > 0 {
			return nil, fmt.Errorf("--metrics-export-every and --metrics-export-header* go with --metrics-export")
		}
		return nil, nil
	}
	if ef.endpoint == "none" {
		if ef.every != 0 || len(ef.headers) > 0 || len(ef.headerSecrets) > 0 {
			return nil, fmt.Errorf("--metrics-export none takes no interval or headers")
		}
		return &api.MetricsExport{}, nil
	}
	if ef.every < 0 || ef.every%time.Second != 0 {
		return nil, fmt.Errorf("--metrics-export-every %v: whole seconds", ef.every)
	}
	me := &api.MetricsExport{Endpoint: ef.endpoint, IntervalSecs: int(ef.every / time.Second)}
	for _, h := range ef.headers {
		name, env, ok := strings.Cut(h, "=")
		if !ok || name == "" || env == "" {
			return nil, fmt.Errorf("--metrics-export-header %q: want NAME=ENV, the header and the environment variable holding its value", h)
		}
		v, set := os.LookupEnv(env)
		if !set || v == "" {
			return nil, fmt.Errorf("--metrics-export-header %s: $%s is not set", name, env)
		}
		if me.Headers == nil {
			me.Headers = map[string]string{}
		}
		me.Headers[name] = v
	}
	for _, h := range ef.headerSecrets {
		name, secret, ok := strings.Cut(h, "=")
		if !ok || name == "" || secret == "" {
			return nil, fmt.Errorf("--metrics-export-header-secret %q: want NAME=SECRET", h)
		}
		if me.HeaderSecrets == nil {
			me.HeaderSecrets = map[string]string{}
		}
		me.HeaderSecrets[name] = secret
	}
	return me, nil
}

// exportSummary is a sandbox's export in a line: where, how often, and how
// the last push went.
func exportSummary(st *api.MetricsExportStatus) string {
	if st == nil {
		return "none"
	}
	s := fmt.Sprintf("%s every %ds", st.Endpoint, st.IntervalSecs)
	switch {
	case st.LastError != "":
		s += "; failing: " + st.LastError
	case st.LastPushAt != nil:
		s += "; last push " + st.LastPushAt.Local().Format(time.RFC3339)
	}
	return s
}
