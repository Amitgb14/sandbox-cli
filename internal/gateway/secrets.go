package gateway

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
)

// Secrets are named values a tenant keeps on the gateway so that a job can
// name them instead of carrying them: an agent's provider API key, a token
// for a package registry. The rules:
//
//   - A value is sealed (AES-256-GCM) before it is stored, with a key the
//     state file does not hold (--secrets-key-file). The additional data
//     binds each ciphertext to its tenant and name, so a sealed value moved
//     to another record in the file does not open there.
//   - A value is never returned by the API, never logged, and never in an
//     error message. GET /v1/secrets lists names and when each was set.
//   - A value leaves the gateway only in the create request of a run that
//     named it, as an environment variable; the node keeps environment
//     variables by name only in its audit record.
//   - A secret's name is the tenant's to choose (openai-prod,
//     openai-staging), and a spec says which variable each is set in:
//     "ENV=NAME", or "NAME" for a secret named as the variable it fills
//     (secretRef). The variable is held to what a run's environment may
//     set — a valid name, never a reserved one (policy.IsReservedEnv) —
//     when the spec is sent, so a mapping cannot reach LD_PRELOAD where a
//     name could not.
//
// Secrets are per tenant: every user of a tenant may name any of them in a
// job, and one with secrets:write may replace or remove them.

// secretRecord is a secret as the state file holds it.
type secretRecord struct {
	Tenant  string    `json:"tenant"`
	Name    string    `json:"name"`
	Sealed  []byte    `json:"sealed"` // nonce || AES-256-GCM ciphertext
	Updated time.Time `json:"updated"`
}

// Limits on what a secret may be.
const (
	maxSecretName  = 128
	maxSecretValue = 64 << 10
	maxSecrets     = 1000 // per tenant
)

// ErrNoSecretsKey is what every secret operation answers on a gateway
// started without a key.
var ErrNoSecretsKey = errors.New("this gateway keeps no secrets: it was started without --secrets-key-file")

// LoadSecretsKey reads the key that seals secrets: 32 bytes, raw or as 64
// hex digits, in a file only its owner can read.
func LoadSecretsKey(path string) ([]byte, error) {
	if err := checkPrivate(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 32 {
		return data, nil
	}
	if s := strings.TrimSpace(string(data)); len(s) == 64 {
		if k, err := hex.DecodeString(s); err == nil {
			return k, nil
		}
	}
	return nil, fmt.Errorf("%s: want 32 random bytes (head -c 32 /dev/urandom), raw or as 64 hex digits", path)
}

// sealer seals and opens values with the secrets key.
type sealer struct{ aead cipher.AEAD }

func newSealer(key []byte) (*sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("the secrets key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) seal(plain []byte, ad string) []byte {
	nonce := make([]byte, s.aead.NonceSize(), s.aead.NonceSize()+len(plain)+s.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(ad))
}

func (s *sealer) open(sealed []byte, ad string) ([]byte, error) {
	ns := s.aead.NonceSize()
	if len(sealed) < ns {
		return nil, errors.New("sealed value too short")
	}
	return s.aead.Open(nil, sealed[:ns], sealed[ns:], []byte(ad))
}

// secretAD binds a sealed value to its tenant and name.
func secretAD(tenant, name string) string {
	return "sandbox-gateway secret v1\x00" + tenant + "\x00" + name
}

// checkSecretName refuses a name that cannot be an environment variable, or
// is one the node refuses to set.
func checkSecretName(name string) error {
	if len(name) > maxSecretName || !secretNameRE.MatchString(name) {
		return fmt.Errorf("secret name %q: letters, digits and . _ -, starting with a letter or an underscore, at most %d", name, maxSecretName)
	}
	// Refused still, though a mapping is checked where it is used: a secret
	// named LD_PRELOAD reads as one meant to be set as it.
	if policy.IsReservedEnv(name) {
		return fmt.Errorf("secret name %s is reserved: a run's environment cannot set it", name)
	}
	return nil
}

// secretNameRE is a secret's own name. Every environment variable name is
// one, so a secret set before names and variables were told apart still is;
// "." and "-" are for names like openai-prod. Nothing in it is special in a
// URL path (PUT /v1/secrets/{name}).
var secretNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// secretRef is one entry of a spec's secrets: the environment variable a
// secret is set in, and the secret.
type secretRef struct{ env, name string }

// parseSecretRefs reads a spec's secrets, each "ENV=NAME" or "NAME" for
// NAME into a variable of its own name, and refuses a variable a run's
// environment may not set, one set twice, or one env sets too. The error
// names variables and secrets, which are names, never values.
func parseSecretRefs(refs []string, env map[string]string) ([]secretRef, error) {
	out := make([]secretRef, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		v, name, mapped := strings.Cut(ref, "=")
		if !mapped {
			name = v
		}
		if err := checkSecretName(name); err != nil {
			return nil, err
		}
		switch {
		case !policy.ValidEnvName(v) && !mapped:
			return nil, fmt.Errorf("secret %s is not an environment variable name; say which variable it is set in: VAR=%s", name, name)
		case !policy.ValidEnvName(v) || len(v) > maxSecretName:
			return nil, fmt.Errorf("%q: the variable is letters, digits and underscores, not starting with a digit, at most %d", ref, maxSecretName)
		case policy.IsReservedEnv(v):
			return nil, fmt.Errorf("%s is reserved: a run's environment cannot set it", v)
		case seen[v]:
			return nil, fmt.Errorf("%s is set twice", v)
		}
		if _, ok := env[v]; ok {
			return nil, fmt.Errorf("%s is both in env and in secrets", v)
		}
		seen[v] = true
		out = append(out, secretRef{env: v, name: name})
	}
	return out, nil
}

// --- the store ------------------------------------------------------------------

// ErrNoSuchSecret is returned for a secret the tenant does not have.
var ErrNoSuchSecret = errors.New("no such secret")

// PutSecret stores a sealed value, replacing the one of that name.
func (s *FileStore) PutSecret(tenant, name string, sealed []byte) error {
	now := s.now().UTC()
	return s.change(func() error {
		n := 0
		for i := range s.st.Secrets {
			r := &s.st.Secrets[i]
			if r.Tenant != tenant {
				continue
			}
			if r.Name == name {
				r.Sealed, r.Updated = slices.Clone(sealed), now
				return nil
			}
			n++
		}
		if n >= maxSecrets {
			return fmt.Errorf("a tenant holds at most %d secrets", maxSecrets)
		}
		s.st.Secrets = append(s.st.Secrets, secretRecord{Tenant: tenant, Name: name, Sealed: slices.Clone(sealed), Updated: now})
		return nil
	})
}

// SealedSecret returns a secret's sealed value.
func (s *FileStore) SealedSecret(tenant, name string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.st.Secrets {
		if r.Tenant == tenant && r.Name == name {
			return slices.Clone(r.Sealed), true
		}
	}
	return nil, false
}

// SecretsOf lists a tenant's secrets by name, without their values.
func (s *FileStore) SecretsOf(tenant string) []api.SecretInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []api.SecretInfo{}
	for _, r := range s.st.Secrets {
		if r.Tenant == tenant {
			out = append(out, api.SecretInfo{Name: r.Name, Updated: r.Updated})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DeleteSecret removes a secret.
func (s *FileStore) DeleteSecret(tenant, name string) error {
	return s.change(func() error {
		for i, r := range s.st.Secrets {
			if r.Tenant == tenant && r.Name == name {
				s.st.Secrets = slices.Delete(s.st.Secrets, i, i+1)
				return nil
			}
		}
		return ErrNoSuchSecret
	})
}

// --- the gateway ------------------------------------------------------------------

// secretValues opens the secrets a spec names (parseSecretRefs) for one
// run, keyed by the variable each is set in. The spec was checked when it
// was sent; it is read again here because a stored one is not trusted to
// have been. An error names the secret, never its value.
func (g *Gateway) secretValues(ctx context.Context, tenant string, refs []string) (map[string]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if g.sealer == nil {
		return nil, ErrNoSecretsKey
	}
	parsed, err := parseSecretRefs(refs, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(parsed))
	for _, ref := range parsed {
		sealed, ok, err := g.store.SealedSecret(ctx, tenant, ref.name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("secret %s does not exist", ref.name)
		}
		v, err := g.sealer.open(sealed, secretAD(tenant, ref.name))
		if err != nil {
			return nil, fmt.Errorf("secret %s does not open with this gateway's secrets key", ref.name)
		}
		out[ref.env] = string(v)
	}
	return out, nil
}

func (g *Gateway) putSecret(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeSecretsWrite) {
		return
	}
	if g.sealer == nil {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, ErrNoSecretsKey.Error())
		return
	}
	name := r.PathValue("name")
	if err := checkSecretName(name); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	var req api.SecretRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.Value) > maxSecretValue {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, fmt.Sprintf("a secret's value is at most %d bytes", maxSecretValue))
		return
	}
	if strings.ContainsRune(req.Value, 0) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "a secret's value cannot hold a NUL byte: it becomes an environment variable")
		return
	}
	if err := g.storeFor(p).PutSecret(ctx, p.Tenant, name, g.sealer.seal([]byte(req.Value), secretAD(p.Tenant, name))); err != nil {
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	}
	g.logf("secret %s set for tenant %q by key %s", name, p.Tenant, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) listSecrets(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeRead) {
		return
	}
	list, err := g.storeFor(p).SecretsOf(ctx, p.Tenant)
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.SecretList{Secrets: list})
}

func (g *Gateway) deleteSecret(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeSecretsWrite) {
		return
	}
	name := r.PathValue("name")
	if err := g.storeFor(p).DeleteSecret(ctx, p.Tenant, name); err != nil {
		if errors.Is(err, ErrNoSuchSecret) {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such secret")
			return
		}
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, err.Error())
		return
	}
	g.logf("secret %s removed for tenant %q by key %s", name, p.Tenant, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}
