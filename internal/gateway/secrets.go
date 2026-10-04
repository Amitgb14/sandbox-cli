package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
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
//     named it, as an environment variable of that name; the node keeps
//     environment variables by name only in its audit record.
//   - A name the node would refuse as reserved (policy.IsReservedEnv) is
//     refused here, when it is set, rather than at a run's start.
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
	if len(name) > maxSecretName || !policy.ValidEnvName(name) {
		return fmt.Errorf("secret name %q: letters, digits and underscores, not starting with a digit, at most %d", name, maxSecretName)
	}
	if policy.IsReservedEnv(name) {
		return fmt.Errorf("secret name %s is reserved: a run's environment cannot set it", name)
	}
	return nil
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

// secretValues opens the named secrets of tenant, for one run. An error
// names the secret, never its value.
func (g *Gateway) secretValues(tenant string, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if g.sealer == nil {
		return nil, ErrNoSecretsKey
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		sealed, ok := g.store.SealedSecret(tenant, name)
		if !ok {
			return nil, fmt.Errorf("secret %s does not exist", name)
		}
		v, err := g.sealer.open(sealed, secretAD(tenant, name))
		if err != nil {
			return nil, fmt.Errorf("secret %s does not open with this gateway's secrets key", name)
		}
		out[name] = string(v)
	}
	return out, nil
}

func (g *Gateway) putSecret(w http.ResponseWriter, r *http.Request, p Principal) {
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
	if err := g.store.PutSecret(p.Tenant, name, g.sealer.seal([]byte(req.Value), secretAD(p.Tenant, name))); err != nil {
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	}
	g.logf("secret %s set for tenant %q by key %s", name, p.Tenant, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) listSecrets(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	writeJSON(w, http.StatusOK, api.SecretList{Secrets: g.store.SecretsOf(p.Tenant)})
}

func (g *Gateway) deleteSecret(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeSecretsWrite) {
		return
	}
	name := r.PathValue("name")
	if err := g.store.DeleteSecret(p.Tenant, name); err != nil {
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
