package agents

import (
	"encoding/json"
)

// authKeep lists, for a saved login file that is also the agent's settings,
// the keys that make up the login. Everything else is dropped when the file
// is saved from a sandbox and again when it is restored into one.
//
// A saved login is written by a guest and handed to every later run of the
// agent, in every repository. A credential in it is fine: it is the agent's
// own. A setting is not, when a setting can name a command: Claude Code's
// ~/.claude.json and Gemini CLI's settings.json both hold MCP servers, each a
// command the agent starts at launch, and ~/.claude.json holds per-project
// tool approvals too. An agent compromised in one repository that wrote one
// there would run it in all the others, beside their workspaces and secrets.
// beta.15 accepted that channel because the whole agent HOME persisted; the
// rewrite carries only named files, so it can carry only the login inside
// them.
//
// An allowlist, so a key the agent adds next release is dropped until it is
// read and found to be login. A key missing here costs a login or onboarding
// prompt, which is visible; a command that crosses is not.
var authKeep = map[string][][]string{
	".claude.json": {
		{"oauthAccount"}, {"userID"}, {"hasCompletedOnboarding"}, {"lastOnboardingVersion"},
		{"theme"}, {"bypassPermissionsModeAccepted"},
	},
	".gemini/settings.json": {
		{"security", "auth", "selectedType"}, {"selectedAuthType"},
	},
}

// FilterAuth returns what of a saved login file may cross between runs: the
// file unchanged when it is only a credential, or just its login keys when it
// is also settings. ok is false when such a file is not a JSON object, and
// then nothing of it crosses.
func FilterAuth(rel string, data []byte) (out []byte, ok bool) {
	keep, filtered := authKeep[rel]
	if !filtered {
		return data, true
	}
	var in map[string]any
	if err := json.Unmarshal(data, &in); err != nil || in == nil {
		return nil, false
	}
	kept := map[string]any{}
	for _, path := range keep {
		if v, found := lookup(in, path); found {
			put(kept, path, v)
		}
	}
	out, err := json.Marshal(kept)
	return out, err == nil
}

func lookup(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[k]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func put(m map[string]any, path []string, v any) {
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}

// authFiltered is for the test that every filtered file is one an agent saves.
func authFiltered() []string {
	var out []string
	for rel := range authKeep {
		out = append(out, rel)
	}
	return out
}
