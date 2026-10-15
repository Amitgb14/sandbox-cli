package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// keepLoginOnGateway has a gateway keep the agent's login for this run
// (POST /v1/sandboxes/{ref}/agent-login): it copies the user's kept login in
// and saves it back from the sandbox as it changes, as it does for hosted
// Studio, so one login serves every client and machine. It reports whether
// the gateway took the login on; false leaves it to this machine's files.
//
// A gateway with no login kept for the user yet is given this machine's,
// if there is one: copied into the sandbox, where the gateway saves it as
// the first login it sees. A login made on this machine moves to the
// gateway without logging in again.
//
// A plain sandboxd, a gateway without a secrets key (501) and one older
// than agent logins (404) answer false quietly: the run keeps this
// machine's login, as before. Any other failure says so and does the same.
// Either way the run goes on: the login is a convenience, not a control.
func keepLoginOnGateway(ctx context.Context, c *api.Client, sandbox string, d agents.Descriptor) bool {
	gw, err := c.IsGateway(ctx)
	if err != nil || !gw {
		return false
	}
	kept, err := c.KeepAgentLogin(ctx, sandbox, d.Name)
	if err != nil {
		var ae *api.Error
		if !errors.As(err, &ae) || (ae.Status != http.StatusNotImplemented && ae.Status != http.StatusNotFound) {
			fmt.Fprintf(os.Stderr, "sandbox-cli: the gateway did not keep the %s login (%v); using this machine's\n", d.Name, err)
		}
		return false
	}
	if !kept.Restored {
		agenthome.RestoreLogin(ctx, c, sandbox, d)
	}
	return true
}
