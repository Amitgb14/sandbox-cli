package agenthome

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
)

// An agent's API keys can be saved on the host, for the runs that start
// where none is set in the environment: Studio's Agents screen writes them,
// and every agent run — the CLI's and Studio's — reads them.
//
// They live in ~/.config/sandbox/agent-keys.json, 0600, beside the agents'
// login directories and never in one: a login directory is copied into the
// sandbox whole, and a key belongs only in the variable its agent reads. A
// key reaches a sandbox the way a host variable always has, in the create
// request's environment; never in an argv, a log or the audit record, which
// keeps names only.

const keysFile = "agent-keys.json"

// maxKeyLen bounds a saved value. API keys are a few hundred bytes; a value
// the size of a file is a paste gone wrong.
const maxKeyLen = 16 << 10

// keysMu serialises this process's read-modify-write of the file. Studio is
// the one writer; the CLI only reads.
var keysMu sync.Mutex

func keysPath() string { return filepath.Join(ConfigDir(), keysFile) }

// readKeys is the whole file: agent name, then variable, then value. A file
// that is missing is no keys; one that is a symlink is refused, as every
// file sandbox-cli keeps a credential in is.
func readKeys() (map[string]map[string]string, error) {
	p := keysPath()
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file; not reading keys from it", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return out, nil
}

// SavedKeys is the keys saved for d, limited to the variables d reads: a
// name the file holds for an agent that no longer reads it is not forwarded.
func SavedKeys(d agents.Descriptor) map[string]string {
	all, err := readKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-cli: saved agent keys: %v\n", err)
		return nil
	}
	out := map[string]string{}
	for k, v := range all[d.Name] {
		if slices.Contains(d.EnvAllow, k) && !policy.IsReservedEnv(k) && v != "" {
			out[k] = v
		}
	}
	return out
}

// SaveKey sets d's variable name to value. Only a variable d reads may be
// saved: anything else would be an environment variable of the user's
// choosing in every run of d, which --env is for, and which this file has no
// business widening into.
func SaveKey(d agents.Descriptor, name, value string) error {
	if !slices.Contains(d.EnvAllow, name) {
		return fmt.Errorf("%s does not read %s", d.Name, name)
	}
	// None is today; this keeps a descriptor that one day lists one from
	// making an instruction to the guest settable from a browser.
	if policy.IsReservedEnv(name) {
		return fmt.Errorf("%s: %s", name, policy.ReservedEnvReason())
	}
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return errors.New("an empty key; remove it instead")
	case len(value) > maxKeyLen:
		return fmt.Errorf("a key longer than %d bytes", maxKeyLen)
	case strings.ContainsFunc(value, unicode.IsControl):
		return errors.New("a key with a control character in it")
	}
	return updateKeys(func(all map[string]map[string]string) {
		if all[d.Name] == nil {
			all[d.Name] = map[string]string{}
		}
		all[d.Name][name] = value
	})
}

// DeleteKey removes d's saved name, if there is one.
func DeleteKey(d agents.Descriptor, name string) error {
	return updateKeys(func(all map[string]map[string]string) {
		delete(all[d.Name], name)
		if len(all[d.Name]) == 0 {
			delete(all, d.Name)
		}
	})
}

func updateKeys(change func(map[string]map[string]string)) error {
	keysMu.Lock()
	defer keysMu.Unlock()
	all, err := readKeys()
	if err != nil {
		return err
	}
	change(all)
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return WritePrivate(ConfigDir(), keysFile, data)
}
