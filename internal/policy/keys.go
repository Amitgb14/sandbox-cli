package policy

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The schema in config.go still parses every key beta.15 read — the trust
// rules and the profiles are written against them, and they are ported with
// their tests — but the rewrite's client reads only a few. A key that parses
// and does nothing is the worst kind of setting: someone who wrote
// `security: {seccomp: required}` believes a control is in force. So a config
// file setting any other key is refused, naming what replaced it, and a typo
// is refused with it rather than ignored.

// liveKeys are the top-level keys the rewrite's client acts on.
var liveKeys = map[string]bool{
	"image": true, "env": true, "env_allow": true, "network": true, "secrets": true,
	"routing": true, "providers": true, "profile": true, "persist_auth": true,
}

// replacedKeys say what became of beta.15's keys.
var replacedKeys = map[string]string{
	"user":     "a sandbox runs its commands as its own user; there is no host user to map",
	"workdir":  "the workspace is always /workspace, a clone of your repository",
	"home":     "a sandbox's HOME is the guest's own",
	"hostname": "a sandbox's hostname is its id",
	"mounts":   "nothing is mounted: use --bind on a local Mac, or a named volume (--volume)",
	"ports":    "reach a port inside with sandbox-cli tunnel",
	"security": "the boundary is a VM now; sandboxd's policy file sets limits and egress",
	"cache":    "keep a cache in a named volume (sandbox-cli volume, --volume)",
	"snapshot": "checkpoints replace snapshots (--checkpoint-every)",
	"sync":     "an agent's conversation history stays in its sandbox",
	"engine":   "there is no container engine: sandboxd runs VMs",
	"runtime":  "there is no container engine: sandboxd runs VMs",
}

// ErrDeadKeys is a config file setting keys that do nothing.
type ErrDeadKeys struct {
	Path string
	Keys []string
}

func (e *ErrDeadKeys) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s sets keys this version does not read; remove them:", e.Path)
	for _, k := range e.Keys {
		if why, ok := replacedKeys[k]; ok {
			fmt.Fprintf(&b, "\n  %s: %s", k, why)
		} else {
			fmt.Fprintf(&b, "\n  %s: not a setting (known: %s)", k, strings.Join(sortedLive(), ", "))
		}
	}
	return b.String()
}

func sortedLive() []string {
	out := make([]string, 0, len(liveKeys))
	for k := range liveKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CheckLiveKeys refuses a config file that sets a key the client does not
// read. A file that is not there, or empty, passes; one that does not parse is
// left to the loader, which says so in its own words.
func CheckLiveKeys(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var top map[string]any
	if yaml.Unmarshal(data, &top) != nil {
		return nil
	}
	var dead []string
	for k := range top {
		if !liveKeys[k] {
			dead = append(dead, k)
		}
	}
	if len(dead) == 0 {
		return nil
	}
	sort.Strings(dead)
	return &ErrDeadKeys{Path: path, Keys: dead}
}
