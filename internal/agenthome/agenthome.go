// Package agenthome is what an agent finds in its home directory in a
// sandbox: its saved login, copied in when a run starts and back out when it
// ends, and the tools volume an image without the agent installs it into.
// The host never mounts anything into the guest for either.
package agenthome

import (
	"os"
	"path/filepath"
)

// ConfigDir is sandbox-cli's own configuration directory on the host.
func ConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "sandbox")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sandbox")
}
