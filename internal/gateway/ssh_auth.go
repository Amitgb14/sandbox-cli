package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// TokenPrefix starts every short-lived SSH token. A username with it is
// always read as a token and never as a sandbox reference, so a sandbox
// cannot be named so as to be mistaken for one.
const TokenPrefix = "sgt_"

// maxUsername bounds the SSH username, which is untrusted text that reaches
// the store and the router; a sandbox id or a token is far shorter.
const maxUsername = 256

// resolveTimeout bounds the Router call made during authentication.
const resolveTimeout = 15 * time.Second

// Messages a refused login is told, in a banner. Not-found and forbidden say
// the same thing, so a login cannot learn whether a sandbox it may not use
// exists.
const (
	msgNoSandbox = "sandbox-gateway: no such sandbox for this login\r\n"
	msgNodeDown  = "sandbox-gateway: the node holding this sandbox is not answering; try again shortly\r\n"
	msgAmbiguous = "sandbox-gateway: that name matches more than one sandbox this key may use; log in with the sandbox id\r\n"
	msgInternal  = "sandbox-gateway: the sandbox cannot be reached\r\n"
)

// sshLogin is what authentication decided about one connection: who, which
// sandbox, and the client for its node. It is written by the auth callbacks
// and read only after the handshake has returned, which orders the two.
type sshLogin struct {
	principal Principal
	id        string
	client    *api.Client
	// how names the credential for logs: a key's id and fingerprint, or
	// "a token"; never the secret.
	how string
	// fingerprint names the credential in the audit record: the key's
	// SHA256 fingerprint, or "token". refused is the same for a credential
	// that was refused, for a connection that ends without logging in.
	fingerprint string
	refused     string
	remote      string // the client's address, set once logged in
}

func (l *sshLogin) describe() string {
	return fmt.Sprintf("user %q via %s", termsafe.Clean(l.principal.User), l.how)
}

// serverConfig is built per connection, so the auth callbacks can record
// their decision in login without a table keyed by session id.
//
// Two ways in, and nothing else: no passwords and no keyboard-interactive,
// because neither exists for these accounts and offering them would only
// give a guesser something to try.
//   - A public key, with the username naming the sandbox.
//   - A token as the username, with no other credential ("none" auth). Only a
//     username that redeems as a valid token gets through "none"; every other
//     username must present a key.
func (s *SSHServer) serverConfig(login *sshLogin) *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		MaxAuthTries: s.cfg.MaxAuthTries,
		NoClientAuth: true,
		NoClientAuthCallback: func(cm ssh.ConnMetadata) (*ssh.Permissions, error) {
			return nil, s.authToken(cm, login)
		},
		// Called for every key offered, before the client has proved it holds
		// the private half. It only says whether the key is known: resolving
		// the sandbox here would let anyone holding someone's public key ask
		// which sandboxes that person has.
		PublicKeyCallback: func(cm ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if len(s.keyCandidates(cm.User(), key)) == 0 {
				if login.refused == "" {
					login.refused = ssh.FingerprintSHA256(key)
				}
				return nil, errors.New("unknown key")
			}
			return &ssh.Permissions{}, nil
		},
		// Called once the client has signed with the key: now it is the
		// key's owner asking, and the sandbox is resolved for them.
		VerifiedPublicKeyCallback: func(cm ssh.ConnMetadata, key ssh.PublicKey, p *ssh.Permissions, _ string) (*ssh.Permissions, error) {
			return p, s.authKey(cm, key, login)
		},
	}
	cfg.AddHostKey(s.signer)
	return cfg
}

// usernameForLog is the SSH username as it may appear in a log: a token is
// the credential itself, so it never does.
func usernameForLog(u string) string {
	if strings.HasPrefix(u, TokenPrefix) {
		return "(a token)"
	}
	if len(u) > 64 {
		u = u[:64] + "…"
	}
	return fmt.Sprintf("%q", termsafe.Clean(u))
}

// userActive reports whether user still holds an API key that is not
// revoked. An SSH key or token is a credential the user made with an API key;
// revoking a user's keys must end their SSH access too, or a revoked user
// keeps every sandbox they own for as long as their SSH keys are registered.
func userActive(st Store, user string) bool {
	for _, k := range st.Keys() {
		if k.User == user && !k.Revoked {
			return true
		}
	}
	return false
}

// authToken is "none" authentication: the username is a short-lived token.
func (s *SSHServer) authToken(cm ssh.ConnMetadata, login *sshLogin) error {
	user := cm.User()
	if !strings.HasPrefix(user, TokenPrefix) || len(user) > maxUsername {
		// Not a token: this login must present a key. Silent, because every
		// client tries "none" first.
		return errors.New("a key is required")
	}
	tok, ok := s.cfg.Store.RedeemSSHToken(user)
	// The store is expected to refuse an expired token; the gateway checks
	// again rather than trust that, and a token with no expiry is refused.
	if !ok || tok.Sandbox == "" || tok.User == "" || tok.Expires.IsZero() || !s.cfg.Now().Before(tok.Expires) {
		s.cfg.Logf("ssh: %s: a token was refused (unknown or expired)", cm.RemoteAddr())
		login.refused = "token"
		return errors.New("token refused")
	}
	if !userActive(s.cfg.Store, tok.User) {
		s.cfg.Logf("ssh: %s: a token for user %q was refused: the user holds no active API key", cm.RemoteAddr(), termsafe.Clean(tok.User))
		return refusal(ErrNotFound)
	}
	p := Principal{User: tok.User, Tenant: tok.Tenant, KeyID: "ssh-token", Scopes: sshScopes, Sandbox: tok.Sandbox}
	id, c, err := s.resolve(p, tok.Sandbox)
	if err == nil && id != tok.Sandbox {
		err = ErrNotFound
	}
	if err != nil {
		s.cfg.Logf("ssh: %s: user %q with a token for %s refused: %v", cm.RemoteAddr(), termsafe.Clean(tok.User), tok.Sandbox, err)
		login.refused = "token"
		return refusal(err)
	}
	*login = sshLogin{principal: p, id: id, client: c, how: "a token", fingerprint: "token"}
	return nil
}

// keyCandidates is every stored key that is exactly key and belongs to a
// user. The fingerprint finds them; the full key is compared as well, so the
// match does not rest on the store's indexing alone.
func (s *SSHServer) keyCandidates(user string, key ssh.PublicKey) []SSHKey {
	if strings.HasPrefix(user, TokenPrefix) || user == "" || len(user) > maxUsername {
		// A token is never also a sandbox reference, and an empty or
		// oversized name is not one either.
		return nil
	}
	want := key.Marshal()
	var out []SSHKey
	for _, k := range s.cfg.Store.SSHKeysByFingerprint(ssh.FingerprintSHA256(key)) {
		if k.User == "" {
			continue
		}
		pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.AuthorizedKey))
		if err != nil || !bytes.Equal(pk.Marshal(), want) {
			continue
		}
		out = append(out, k)
	}
	slices.SortFunc(out, func(a, b SSHKey) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// authKey resolves the username for the owner of a key the client has proved
// it holds.
//
// The same public key may be registered by more than one user (a public key
// is public; registering someone's key is granting them access). Each owner
// is tried; a reference that resolves for exactly one sandbox wins, and a
// name that resolves to different sandboxes for different owners is refused
// rather than guessed at.
func (s *SSHServer) authKey(cm ssh.ConnMetadata, key ssh.PublicKey, login *sshLogin) error {
	ref, fp := cm.User(), ssh.FingerprintSHA256(key)
	var found *sshLogin
	var lastErr error = ErrNotFound
	for _, k := range s.keyCandidates(ref, key) {
		if !userActive(s.cfg.Store, k.User) {
			s.cfg.Logf("ssh: %s: key %s (%s) refused: user %q holds no active API key", cm.RemoteAddr(), k.ID, fp, termsafe.Clean(k.User))
			continue
		}
		p := Principal{User: k.User, Tenant: k.Tenant, KeyID: k.ID, Scopes: sshScopes, Sandbox: k.Sandbox}
		id, c, err := s.resolve(p, ref)
		// A key limited to one sandbox reaches only that one. The principal
		// carries the limit for the router to enforce; this checks the
		// answer as well, so the limit holds whatever the router does.
		if err == nil && k.Sandbox != "" && id != k.Sandbox {
			err = ErrNotFound
		}
		if err != nil {
			if !errors.Is(lastErr, ErrNodeDown) {
				lastErr = err
			}
			continue
		}
		if found != nil && found.id != id {
			s.cfg.Logf("ssh: %s: key %s (%s) for %s refused: matches more than one sandbox", cm.RemoteAddr(), k.ID, fp, usernameForLog(ref))
			login.refused = fp
			return &ssh.BannerError{Err: errors.New("ambiguous"), Message: msgAmbiguous}
		}
		if found == nil {
			found = &sshLogin{principal: p, id: id, client: c, how: fmt.Sprintf("key %s (%s)", k.ID, fp), fingerprint: fp}
		}
	}
	if found == nil {
		s.cfg.Logf("ssh: %s: key %s for %s refused: %v", cm.RemoteAddr(), fp, usernameForLog(ref), lastErr)
		login.refused = fp
		return refusal(lastErr)
	}
	*login = *found
	return nil
}

func (s *SSHServer) resolve(p Principal, ref string) (string, *api.Client, error) {
	ctx, cancel := context.WithTimeout(s.baseCtx, resolveTimeout)
	defer cancel()
	id, c, err := s.cfg.Router.Resolve(ctx, p, ref, ScopeSSH)
	if err == nil && (id == "" || c == nil) {
		err = errors.New("router returned no sandbox")
	}
	return id, c, err
}

// refusal turns a Router error into a failed authentication that tells the
// client why, in a banner. The error text itself stays in the gateway's log:
// an internal error can name a node or an address the client has no business
// knowing.
func refusal(err error) error {
	msg := msgInternal
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrForbidden), errors.Is(err, ErrUnauthenticated):
		msg = msgNoSandbox
	case errors.Is(err, ErrNodeDown):
		msg = msgNodeDown
	}
	return &ssh.BannerError{Err: err, Message: msg}
}
