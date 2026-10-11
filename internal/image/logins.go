package image

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registry logins.
//
// sandboxd pulls anonymously unless its operator gives it logins
// (--registry-auth-file): a registry, the repositories under it the login is
// for, and a user name and token. The rules:
//
//   - A login is the operator's, not a requester's. An image it pulls is
//     cached on the node like any other, so every user of the node may start
//     from it: it is for images the operator publishes for its users, which
//     is why each login names the repositories it is for and is used for
//     those only, never for the rest of the registry.
//   - The file holds tokens, so one others can read is refused, as the API
//     token's is.
//   - A login is sent only in the token request the registry's challenge
//     names, over https (or a registry the operator listed as plain http),
//     and never logged or put in an error.

// Login is one registry login.
type Login struct {
	Registry string `yaml:"registry"`
	// Prefix is the repositories the login is for: amitgb14/ is every one
	// under amitgb14, amitgb14/app that one repository and those under it.
	Prefix   string `yaml:"repositories"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// covers reports whether l is the login for r.
func (l Login) covers(r Ref) bool {
	if l.Registry != r.Registry {
		return false
	}
	p := strings.TrimSuffix(l.Prefix, "/")
	return r.Repo == p || strings.HasPrefix(r.Repo, p+"/")
}

// LoadLogins reads a --registry-auth-file:
//
//	logins:
//	  - registry: ghcr.io
//	    repositories: amitgb14/
//	    username: amitgb14
//	    password: ghp_…          # a token that may read packages
func LoadLogins(path string) ([]Login, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by others (mode %v); chmod 600 it", path, fi.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Logins []Login `yaml:"logins"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		// The decoder's message can quote the file; the file holds tokens.
		return nil, fmt.Errorf("%s: not a registry auth file (logins: [{registry, repositories, username, password}])", path)
	}
	for i, l := range f.Logins {
		where := fmt.Sprintf("%s: login %d", path, i+1)
		switch {
		case l.Registry == "" || strings.ContainsAny(l.Registry, "/ "):
			return nil, fmt.Errorf("%s: registry is a host[:port], e.g. ghcr.io", where)
		case strings.Trim(l.Prefix, "/") == "" || !repoRE.MatchString(strings.TrimSuffix(l.Prefix, "/")):
			return nil, fmt.Errorf("%s: repositories names the repositories it is for, e.g. amitgb14/ (never a whole registry)", where)
		case l.Username == "" || l.Password == "":
			return nil, fmt.Errorf("%s: username and password are both needed", where)
		}
		for _, o := range f.Logins[:i] {
			if o.Registry == l.Registry && strings.TrimSuffix(o.Prefix, "/") == strings.TrimSuffix(l.Prefix, "/") {
				return nil, fmt.Errorf("%s: %s/%s has a login already", where, l.Registry, l.Prefix)
			}
		}
	}
	if len(f.Logins) == 0 {
		return nil, errors.New(path + ": no logins")
	}
	return f.Logins, nil
}

// loginFor is the login for r, the one with the longest prefix that covers
// it, if any.
func (p *Puller) loginFor(r Ref) (Login, bool) {
	var best Login
	found := false
	for _, l := range p.Logins {
		if l.covers(r) && (!found || len(strings.TrimSuffix(l.Prefix, "/")) > len(strings.TrimSuffix(best.Prefix, "/"))) {
			best, found = l, true
		}
	}
	return best, found
}
