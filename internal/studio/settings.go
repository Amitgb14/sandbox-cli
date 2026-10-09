package studio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// Studio's own settings: VM templates (a size to launch at, by name),
// allowlist groups (named sets of hosts a launch can allow) and deny rules
// (hosts every Studio launch is refused). They are this
// machine's user's, kept in ~/.config/sandbox/studio.json, so they are the
// same in every Studio this user starts, on whichever port.
//
// Neither widens anything sandboxd decides. A template is a request for
// resources, bounded and refused by the server's limits like the CLI's
// --cpus; a rule is an --allow or --deny, which the server checks against its
// ceiling and may_allow like any request's.

const settingsFile = "studio.json"

// Template is a size a sandbox can be launched at.
type Template struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	CPUs        float64 `json:"cpus"`
	MemoryMB    int     `json:"memory_mb"`
	// DiskMB is the writable disk; 0 is the server's default.
	DiskMB int `json:"disk_mb,omitempty"`
	// Builtin marks the sizes Studio ships with: listed always, never saved
	// or removed, so a Playground always has something to offer.
	Builtin bool `json:"builtin,omitempty"`
}

// builtinTemplates are micro to xlarge. Whole vCPUs: a microVM's vCPU count
// is an integer, and a fraction would be rounded where nobody sees it.
var builtinTemplates = []Template{
	{Name: "micro", Description: "Scripts and one-off commands", CPUs: 1, MemoryMB: 512, Builtin: true},
	{Name: "small", Description: "An agent on a small repository", CPUs: 1, MemoryMB: 1024, Builtin: true},
	{Name: "medium", Description: "Builds and test suites; a desktop image", CPUs: 2, MemoryMB: 2048, Builtin: true},
	{Name: "large", Description: "Heavy builds, several services at once", CPUs: 4, MemoryMB: 8192, Builtin: true},
	{Name: "xlarge", Description: "The biggest jobs this machine takes", CPUs: 8, MemoryMB: 16384, Builtin: true},
}

// EgressRule is a host every Studio launch is refused. Action is "deny";
// "allow", from before there were groups, is moved into one when read
// (migrateEgress).
type EgressRule struct {
	Host    string `json:"host"`
	Action  string `json:"action"`
	Enabled bool   `json:"enabled"`
	Note    string `json:"note,omitempty"`
}

type settings struct {
	Templates []Template    `json:"templates,omitempty"`
	Egress    []EgressRule  `json:"egress,omitempty"`
	Groups    []EgressGroup `json:"egress_groups,omitempty"`
}

// settingsMu serialises read-modify-write of the file within this Studio.
var settingsMu sync.Mutex

func settingsPath() string { return filepath.Join(agenthome.ConfigDir(), settingsFile) }

func loadSettings() (settings, error) {
	var st settings
	p := settingsPath()
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if !fi.Mode().IsRegular() {
		return st, fmt.Errorf("%s is not a regular file", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("%s: %w", p, err)
	}
	migrateEgress(&st)
	return st, nil
}

func updateSettings(change func(*settings) error) (settings, error) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	st, err := loadSettings()
	if err != nil {
		return st, err
	}
	if err := change(&st); err != nil {
		return st, err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return st, err
	}
	return st, agenthome.WritePrivate(agenthome.ConfigDir(), settingsFile, data)
}

// --- templates --------------------------------------------------------------------

var templateName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

func validTemplate(t Template) error {
	switch {
	case !templateName.MatchString(t.Name):
		return errors.New("name: lowercase letters, digits and dashes, up to 32")
	case len(t.Description) > 200:
		return errors.New("description: up to 200 characters")
	case !(t.CPUs > 0) || t.CPUs > 256 || t.CPUs != math.Trunc(t.CPUs):
		return errors.New("cpus: a whole number from 1 to 256")
	case t.MemoryMB < 128 || t.MemoryMB > 1<<20:
		return errors.New("memory_mb: from 128 to 1048576")
	case t.DiskMB != 0 && (t.DiskMB < 256 || t.DiskMB > 1<<24):
		return errors.New("disk_mb: 0 for the server's default, or from 256")
	}
	return nil
}

func allTemplates(st settings) []Template {
	out := append([]Template{}, builtinTemplates...)
	return append(out, st.Templates...)
}

func isBuiltin(name string) bool {
	return slices.ContainsFunc(builtinTemplates, func(t Template) bool { return t.Name == name })
}

// Templates is every template, built in first, then those saved in Studio:
// what `sandbox-cli template ls` prints.
func Templates() ([]Template, error) {
	st, err := loadSettings()
	if err != nil {
		return nil, err
	}
	return allTemplates(st), nil
}

// LookupTemplate is the template called name, built in or saved: what the
// CLI's --template reads, so a size made in Studio is the same size there.
// A settings file that cannot be read is an error here, and only here: a
// run that names no template never opens it.
func LookupTemplate(name string) (Template, error) {
	st, err := loadSettings()
	if err != nil {
		return Template{}, err
	}
	all := allTemplates(st)
	names := make([]string, 0, len(all))
	for _, t := range all {
		if t.Name == name {
			return t, nil
		}
		names = append(names, t.Name)
	}
	return Template{}, fmt.Errorf("no template %q (have: %s; Studio's Templates screen adds more)", name, strings.Join(names, ", "))
}

func (s *Server) templates(w http.ResponseWriter, _ *http.Request) {
	st, err := loadSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": allTemplates(st)})
}

// putTemplate creates or replaces a saved template.
func (s *Server) putTemplate(w http.ResponseWriter, r *http.Request) {
	var t Template
	if !decode(w, r, &t) {
		return
	}
	t.Name, t.Builtin = r.PathValue("name"), false
	t.Description = strings.TrimSpace(t.Description)
	if isBuiltin(t.Name) {
		writeErr(w, http.StatusConflict, t.Name+" is a built-in template; save yours under another name")
		return
	}
	if err := validTemplate(t); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err := updateSettings(func(st *settings) error {
		if i := slices.IndexFunc(st.Templates, func(x Template) bool { return x.Name == t.Name }); i >= 0 {
			st.Templates[i] = t
			return nil
		}
		if len(st.Templates) >= 100 {
			return errors.New("100 templates is the most Studio keeps")
		}
		st.Templates = append(st.Templates, t)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if isBuiltin(name) {
		writeErr(w, http.StatusConflict, name+" is a built-in template")
		return
	}
	found := false
	_, err := updateSettings(func(st *settings) error {
		st.Templates = slices.DeleteFunc(st.Templates, func(x Template) bool {
			found = found || x.Name == name
			return x.Name == name
		})
		return nil
	})
	switch {
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	case !found:
		writeErr(w, http.StatusNotFound, "no template "+name)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- egress: allowlist groups and deny rules --------------------------------------

// An allowlist is managed as named groups — "go" holding proxy.golang.org and
// sum.golang.org, "internal" holding a company's registry — and a launch on
// an allowlist picks the groups it wants, rather than every host anybody ever
// allowed sitting in one list that applies to everything. Groups marked
// Default are the ones a launch gets when it picks none, which is also what
// a run on a server whose own default is an allowlist gets.
//
// Deny rules stay one list, each with a switch: a deny is a host no launch
// should reach, which is not a choice made per run.

// EgressGroup is a named set of hosts an allowlist launch can include.
type EgressGroup struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Hosts       []string `json:"hosts"`
	// Default groups are included in an allowlist launch that names none.
	Default bool `json:"default,omitempty"`
}

// hostPattern is a DNS name, optionally behind one leading "*." wildcard: the
// names sandboxd's allow and deny lists take (spec.hostRE). Checked here too
// so a host that can never apply is refused when it is saved, not at every
// launch after.
var hostPattern = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeHost(h string) (string, error) {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if len(h) > 253 || !hostPattern.MatchString(h) {
		return "", fmt.Errorf("%q: a host name, optionally *.name; no scheme, port or path", h)
	}
	return h, nil
}

func normalizeRules(in []EgressRule) ([]EgressRule, error) {
	if len(in) > 200 {
		return nil, errors.New("200 rules is the most Studio keeps")
	}
	seen := map[string]bool{}
	out := make([]EgressRule, 0, len(in))
	for _, r := range in {
		h, err := normalizeHost(r.Host)
		if err != nil {
			return nil, err
		}
		r.Host, r.Note = h, strings.TrimSpace(r.Note)
		if r.Action == "" {
			r.Action = "deny"
		}
		if r.Action != "deny" {
			return nil, fmt.Errorf("%s: a rule denies; a host to allow goes in an allowlist group", r.Host)
		}
		if len(r.Note) > 200 {
			return nil, fmt.Errorf("%s: a note of up to 200 characters", r.Host)
		}
		if seen[r.Host] {
			return nil, fmt.Errorf("%s has two rules; keep one", r.Host)
		}
		seen[r.Host] = true
		out = append(out, r)
	}
	return out, nil
}

func normalizeGroup(g EgressGroup) (EgressGroup, error) {
	g.Description = strings.TrimSpace(g.Description)
	switch {
	case !templateName.MatchString(g.Name):
		return g, errors.New("name: lowercase letters, digits and dashes, up to 32")
	case len(g.Description) > 200:
		return g, errors.New("description: up to 200 characters")
	case len(g.Hosts) > 500:
		return g, errors.New("500 hosts is the most a group holds")
	}
	seen := map[string]bool{}
	hosts := make([]string, 0, len(g.Hosts))
	for _, h := range g.Hosts {
		n, err := normalizeHost(h)
		if err != nil {
			return g, err
		}
		if !seen[n] {
			seen[n] = true
			hosts = append(hosts, n)
		}
	}
	g.Hosts = hosts
	return g, nil
}

// migrateEgress moves allow rules, from before there were groups, into a
// default group, so a host someone allowed is still allowed and is now
// somewhere they can manage it. Only enabled ones: a switched-off allow rule
// allowed nothing.
func migrateEgress(st *settings) {
	var hosts []string
	rules := st.Egress[:0]
	for _, r := range st.Egress {
		if r.Action == "allow" {
			if r.Enabled {
				hosts = append(hosts, r.Host)
			}
			continue
		}
		rules = append(rules, r)
	}
	st.Egress = rules
	if len(hosts) == 0 {
		return
	}
	for i, g := range st.Groups {
		if g.Name == "default" {
			st.Groups[i].Hosts = append(g.Hosts, hosts...)
			return
		}
	}
	st.Groups = append(st.Groups, EgressGroup{Name: "default", Description: "Hosts allowed before there were groups", Hosts: hosts, Default: true})
}

func (s *Server) egress(w http.ResponseWriter, _ *http.Request) {
	st, err := loadSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rules, groups := st.Egress, st.Groups
	if rules == nil {
		rules = []EgressRule{}
	}
	if groups == nil {
		groups = []EgressGroup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules, "groups": groups})
}

// putEgress replaces the deny rules whole: the screen edits a list, and a
// list saved as one write cannot be left half changed by two tabs.
func (s *Server) putEgress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules []EgressRule `json:"rules"`
	}
	if !decode(w, r, &body) {
		return
	}
	rules, err := normalizeRules(body.Rules)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := updateSettings(func(st *settings) error { st.Egress = rules; return nil }); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// putEgressGroup creates or replaces one group.
func (s *Server) putEgressGroup(w http.ResponseWriter, r *http.Request) {
	var g EgressGroup
	if !decode(w, r, &g) {
		return
	}
	g.Name = r.PathValue("name")
	g, err := normalizeGroup(g)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err = updateSettings(func(st *settings) error {
		if i := slices.IndexFunc(st.Groups, func(x EgressGroup) bool { return x.Name == g.Name }); i >= 0 {
			st.Groups[i] = g
			return nil
		}
		if len(st.Groups) >= 50 {
			return errors.New("50 groups is the most Studio keeps")
		}
		st.Groups = append(st.Groups, g)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) deleteEgressGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	found := false
	_, err := updateSettings(func(st *settings) error {
		st.Groups = slices.DeleteFunc(st.Groups, func(x EgressGroup) bool {
			found = found || x.Name == name
			return x.Name == name
		})
		return nil
	})
	switch {
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
	case !found:
		writeErr(w, http.StatusNotFound, "no group "+name)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// errUnknownGroup is a launch naming a group there is none of: the caller's
// mistake, said as one rather than as a server fault.
var errUnknownGroup = errors.New("no such allowlist group")

// egressFor is what a launch's groups and the enabled deny rules add to its
// --allow and --deny. chosen nil is "none picked": the default groups.
func egressFor(chosen []string) (allow, deny []string, err error) {
	st, err := loadSettings()
	if err != nil {
		return nil, nil, err
	}
	for _, r := range st.Egress {
		if r.Enabled && r.Action == "deny" {
			deny = append(deny, r.Host)
		}
	}
	for _, name := range chosen {
		if !slices.ContainsFunc(st.Groups, func(g EgressGroup) bool { return g.Name == name }) {
			return nil, nil, fmt.Errorf("%w: %s", errUnknownGroup, name)
		}
	}
	for _, g := range st.Groups {
		if (chosen == nil && g.Default) || slices.Contains(chosen, g.Name) {
			allow = append(allow, g.Hosts...)
		}
	}
	return allow, deny, nil
}

// --- agent keys -------------------------------------------------------------------

// putAgentKey saves a key for one of an agent's variables. The value is
// write-only: nothing Studio serves ever returns it.
func (s *Server) putAgentKey(w http.ResponseWriter, r *http.Request) {
	d, name, ok := agentVar(w, r)
	if !ok {
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := agenthome.SaveKey(d, name, body.Value); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// The name only, as the audit record keeps it.
	s.logf("saved a key for %s's %s", d.Name, name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAgentKey(w http.ResponseWriter, r *http.Request) {
	d, name, ok := agentVar(w, r)
	if !ok {
		return
	}
	if err := agenthome.DeleteKey(d, name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logf("removed the saved key for %s's %s", d.Name, name)
	w.WriteHeader(http.StatusNoContent)
}

// agentVar is the agent and variable a key request names: an agent Studio
// lists, and a variable that agent reads.
func agentVar(w http.ResponseWriter, r *http.Request) (agents.Descriptor, string, bool) {
	d, ok := agents.Lookup(r.PathValue("agent"))
	if !ok {
		writeErr(w, http.StatusNotFound, "no such agent")
		return d, "", false
	}
	name := r.PathValue("var")
	if !slices.Contains(d.EnvAllow, name) {
		writeErr(w, http.StatusBadRequest, d.Name+" does not read "+name)
		return d, "", false
	}
	return d, name, true
}
