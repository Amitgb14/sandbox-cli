package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Registry logins.
//
// A pull is anonymous unless the request that caused it carries a login
// (WithLogin): through a gateway, the login the requester's organization
// keeps for the image's registry. There is no node-wide login: an image a
// node pulled with one would be every user's to start from. The rules:
//
//   - A login is used only for an image on the registry it names, and only
//     in the token request the registry's challenge names, over https (or a
//     registry the operator listed as plain http). It is never logged or put
//     in an error.
//   - What a login fetched is not handed to a pull without it. Pull tokens
//     and the manifests kept for an offline start are kept per login
//     (Login.key), so a requester without the login meets the registry's
//     refusal: the manifest is fetched on every pull, and the cached layers
//     it names are reached only through it.

// Login is one registry login.
type Login struct {
	Registry string // host[:port], as ParseRef reads it from an image
	Username string
	Password string
}

// key tells logins apart in the caches, without holding the password. ""
// is an anonymous pull, so what anonymous pulls cached before logins
// existed is still theirs.
func (l Login) key() string {
	if l == (Login{}) {
		return ""
	}
	sum := sha256.Sum256([]byte(l.Registry + "\x00" + l.Username + "\x00" + l.Password))
	return hex.EncodeToString(sum[:16])
}

type loginKey struct{}

// WithLogin is ctx carrying a login for the pulls made under it.
func WithLogin(ctx context.Context, l Login) context.Context {
	return context.WithValue(ctx, loginKey{}, l)
}

// loginFor is the login ctx carries for r, if it is for r's registry.
func loginFor(ctx context.Context, r Ref) (Login, bool) {
	l, ok := ctx.Value(loginKey{}).(Login)
	if !ok || l.Registry != r.Registry || l.Username == "" || l.Password == "" {
		return Login{}, false
	}
	return l, true
}
