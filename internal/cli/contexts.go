package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// A context is an endpoint the CLI talks to: this machine's sandboxd ("local"),
// a self-hosted one, or the cloud. Switching is the whole difference between
// them, as far as the CLI is concerned — the API is the same.
type endpointContext struct {
	Endpoint  string `json:"endpoint"`
	TokenFile string `json:"token_file,omitempty"`
	CAFile    string `json:"ca_file,omitempty"`
	// Org is the organisation a gateway context acts in (sandbox-cli org
	// use); empty is the key's own tenant.
	Org string `json:"org,omitempty"`
}

type contextFile struct {
	Current  string                     `json:"current"`
	Contexts map[string]endpointContext `json:"contexts"`
}

func configDir() string { return agenthome.ConfigDir() }

func contextsPath() string { return filepath.Join(configDir(), "contexts.json") }

// localSocket is where sandboxd listens by default — kept in step with
// cmd/sandboxd's defaultSocket.
func localSocket() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "sandboxd.sock")
	}
	return filepath.Join(configDir(), "sandboxd.sock")
}

func loadContexts() (contextFile, error) {
	cf := contextFile{Current: "local", Contexts: map[string]endpointContext{}}
	b, err := os.ReadFile(contextsPath())
	if errors.Is(err, os.ErrNotExist) {
		return cf, nil
	}
	if err != nil {
		return cf, err
	}
	if err := json.Unmarshal(b, &cf); err != nil {
		return cf, fmt.Errorf("%s: %w", contextsPath(), err)
	}
	if cf.Contexts == nil {
		cf.Contexts = map[string]endpointContext{}
	}
	if cf.Current == "" {
		cf.Current = "local"
	}
	return cf, nil
}

func (cf contextFile) save() error {
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cf, "", "  ")
	tmp := contextsPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, contextsPath())
}

func (cf contextFile) resolve(name string) (endpointContext, error) {
	if c, ok := cf.Contexts[name]; ok {
		return c, nil
	}
	if name == "local" {
		return endpointContext{Endpoint: "unix://" + localSocket()}, nil
	}
	return endpointContext{}, fmt.Errorf("no context named %q (sandbox-cli context ls)", name)
}

// orgFlag is the global --org: the organisation to act in on a gateway, over
// SANDBOX_ORG and the context's own.
var orgFlag string

// selectedOrg is the organisation a command acts in: --org, then SANDBOX_ORG,
// then the context's.
func selectedOrg(c endpointContext) string {
	if orgFlag != "" {
		return orgFlag
	}
	if v := os.Getenv("SANDBOX_ORG"); v != "" {
		return v
	}
	return c.Org
}

// newClient connects to the selected context: --context, then SANDBOX_CONTEXT,
// then the current one. On a gateway it acts in the selected organisation.
func newClient(flagContext string) (*api.Client, string, error) {
	cf, err := loadContexts()
	if err != nil {
		return nil, "", err
	}
	name := cf.Current
	if v := os.Getenv("SANDBOX_CONTEXT"); v != "" {
		name = v
	}
	if flagContext != "" {
		name = flagContext
	}
	c, err := cf.resolve(name)
	if err != nil {
		return nil, "", err
	}
	token := ""
	if c.TokenFile != "" {
		b, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return nil, "", fmt.Errorf("context %s: token: %w", name, err)
		}
		token = strings.TrimSpace(string(b))
	}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, "", fmt.Errorf("context %s: CA: %w", name, err)
		}
		cl, err := api.NewClientWithCA(c.Endpoint, token, pem)
		if err != nil {
			return nil, name, err
		}
		return cl.WithOrg(selectedOrg(c)), name, nil
	}
	cl, err := api.NewClient(c.Endpoint, token)
	if err != nil {
		return nil, name, err
	}
	return cl.WithOrg(selectedOrg(c)), name, nil
}

func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Choose which sandboxd the CLI talks to: local, self-hosted, or cloud",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List contexts; * marks the current one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cf, err := loadContexts()
			if err != nil {
				return err
			}
			names := []string{"local"}
			for n := range cf.Contexts {
				if n != "local" {
					names = append(names, n)
				}
			}
			sort.Strings(names[1:])
			for _, n := range names {
				c, _ := cf.resolve(n)
				mark := " "
				if n == cf.Current {
					mark = "*"
				}
				line := fmt.Sprintf("%s %-12s %s", mark, termsafe.Clean(n), termsafe.Clean(c.Endpoint))
				if c.Org != "" {
					line += "  (org " + termsafe.Clean(c.Org) + ")"
				}
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	})
	var tokenFile, caFile, org string
	add := &cobra.Command{
		Use:   "add NAME ENDPOINT",
		Short: "Add a context: https://host:port with --token-file, or unix:///path",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.ContainsAny(args[0], "/ \t") || args[0] == "" {
				return fmt.Errorf("context name %q: no slashes or spaces", args[0])
			}
			if _, err := api.NewClient(args[1], ""); err != nil {
				return err
			}
			for _, f := range []string{tokenFile, caFile} {
				if f != "" {
					if _, err := os.Stat(f); err != nil {
						return err
					}
				}
			}
			cf, err := loadContexts()
			if err != nil {
				return err
			}
			abs := func(p string) string {
				if p == "" {
					return ""
				}
				a, _ := filepath.Abs(p)
				return a
			}
			if org != "" && !validOrgArg(org) {
				return fmt.Errorf("--org %q is not an organization name", termsafe.Clean(org))
			}
			cf.Contexts[args[0]] = endpointContext{Endpoint: args[1], TokenFile: abs(tokenFile), CAFile: abs(caFile), Org: org}
			return cf.save()
		},
	}
	add.Flags().StringVar(&tokenFile, "token-file", "", "file holding the endpoint's bearer token")
	add.Flags().StringVar(&caFile, "ca", "", "CA certificate for a self-hosted endpoint's TLS")
	add.Flags().StringVar(&org, "org", "", "on a gateway, the organization to act in (default: the key's own tenant)")
	cmd.AddCommand(add)
	cmd.AddCommand(&cobra.Command{
		Use:   "use NAME",
		Short: "Make NAME the current context",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cf, err := loadContexts()
			if err != nil {
				return err
			}
			if _, err := cf.resolve(args[0]); err != nil {
				return err
			}
			cf.Current = args[0]
			return cf.save()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cf, err := loadContexts()
			if err != nil {
				return err
			}
			delete(cf.Contexts, args[0])
			if cf.Current == args[0] {
				cf.Current = "local"
			}
			return cf.save()
		},
	})
	return cmd
}
