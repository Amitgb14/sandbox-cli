package gateway

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// FileStore is the Store kept in one JSON file.
//
// The file holds no secret. An API key and an SSH token are kept as the hex
// SHA-256 of the secret, which is a 256-bit random string: a hash of that
// needs no salt or stretching, because there is no dictionary to try. Reading
// the file tells an attacker who owns what — and a service's spec, env values
// included, since every replica is created from it — so it is still written
// 0600 and a file others can read is refused at load.
//
// Every change is written before the call returns, atomically (a temporary
// file, fsync, rename), so a crash leaves the old state or the new one. Writes
// are coalesced: concurrent changes wait on one write that includes them all,
// which is what keeps a burst of creates from writing the file once each.
//
// One process holds the file at a time (an exclusive lock beside it). A
// second process — the CLI while the gateway serves — would otherwise write
// its change and then have it overwritten by the gateway's next one, and a
// revoked key would come back to life.
type FileStore struct {
	path string
	lock *fileLock
	now  func() time.Time

	mu    sync.RWMutex
	st    fileState
	gen   uint64 // bumped by every change, under mu
	flush sync.Mutex
	saved uint64 // the generation on disk, under flush
}

// fileState is the file's content.
type fileState struct {
	Version   int                      `json:"version"`
	Keys      []Key                    `json:"keys"`
	SSHKeys   []SSHKey                 `json:"ssh_keys"`
	Tokens    []tokenRecord            `json:"ssh_tokens"`
	Sandboxes map[string]sandboxRecord `json:"sandboxes"`
	Volumes   map[string]Owner         `json:"volumes"`
	Snapshots map[string]Owner         `json:"snapshots"`
	Nodes     []NodeConfig             `json:"nodes"`
	// Secrets hold values sealed with the secrets key (secrets.go), and
	// jobs their specs and runs' outcomes (jobs.go); what runs kept is in
	// files beside the state, not in it.
	Secrets  []secretRecord        `json:"secrets,omitempty"`
	Jobs     map[string]*jobRecord `json:"jobs,omitempty"`
	Services *serviceState         `json:"services,omitempty"` // services_store.go
	// Orgs are the organisations users made and who belongs to each
	// (orgs.go). A state file from before them has none, and loads.
	Orgs []orgRecord `json:"orgs,omitempty"`
}

type tokenRecord struct {
	Hash    string    `json:"hash"`
	User    string    `json:"user"`
	Tenant  string    `json:"tenant"`
	Sandbox string    `json:"sandbox"`
	Expires time.Time `json:"expires"`
}

// sandboxRecord is an owner and what the sandbox was given, so a tenant's
// quota can be counted without asking every node.
type sandboxRecord struct {
	Owner
	CPUs     float64   `json:"cpus,omitempty"`
	MemoryMB int       `json:"memory_mb,omitempty"`
	DiskMB   int       `json:"disk_mb,omitempty"` // for the room a terminate gives back (nodes.go)
	Recorded time.Time `json:"recorded"`
}

const stateVersion = 1

// Prefixes of the secrets the gateway issues. They make a leaked one
// recognisable to a secret scanner and to a person.
const (
	keySecretPrefix   = "sgk_"
	tokenSecretPrefix = "sgt_"
)

// userRE bounds a user or tenant name. They are stamped as label values and
// printed in listings, so they are short and plain.
var userRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+-]{0,63}$`)

// ValidUser reports whether s may name a user or a tenant.
func ValidUser(s string) bool { return userRE.MatchString(s) }

// OpenFileStore opens the state file at path, creating it (and its
// directory, 0700) when missing, and takes the lock that keeps any other
// process from writing it until Close.
func OpenFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("no state file named")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := lockFile(path + ".lock")
	if err != nil {
		return nil, err
	}
	s := &FileStore{path: path, lock: lock, now: time.Now}
	if err := s.load(); err != nil {
		lock.unlock()
		return nil, err
	}
	return s, nil
}

// Close releases the lock. The store must not be used afterwards.
func (s *FileStore) Close() error {
	if s.lock != nil {
		s.lock.unlock()
		s.lock = nil
	}
	return nil
}

func (s *FileStore) load() error {
	fi, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.st = fileState{Version: stateVersion}
		s.init()
		s.gen = 1
		return s.persist()
	}
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by others (mode %v); chmod 600 it", s.path, fi.Mode().Perm())
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s.st); err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}
	if s.st.Version != stateVersion {
		return fmt.Errorf("%s: state version %d; this gateway reads %d", s.path, s.st.Version, stateVersion)
	}
	s.init()
	return nil
}

func (s *FileStore) init() {
	if s.st.Sandboxes == nil {
		s.st.Sandboxes = map[string]sandboxRecord{}
	}
	if s.st.Volumes == nil {
		s.st.Volumes = map[string]Owner{}
	}
	if s.st.Snapshots == nil {
		s.st.Snapshots = map[string]Owner{}
	}
	if s.st.Jobs == nil {
		s.st.Jobs = map[string]*jobRecord{}
	}
}

// change applies fn under the write lock and then waits until the file
// holds the change. fn returning an error changes nothing.
func (s *FileStore) change(fn func() error) error {
	if err := s.stage(fn); err != nil {
		return err
	}
	return s.persist()
}

// stage is change without the write: the change is visible to readers at
// once, and on disk after the next persist. It lets a caller make a change
// visible under a lock of its own without holding that lock through a write.
func (s *FileStore) stage(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(); err != nil {
		return err
	}
	s.gen++
	return nil
}

// persist writes the state if the file is older than the newest change.
// Whoever holds flush writes every change made so far; the callers queued
// behind it find their change already written and return.
func (s *FileStore) persist() error {
	s.flush.Lock()
	defer s.flush.Unlock()
	s.mu.RLock()
	gen := s.gen
	if gen <= s.saved {
		s.mu.RUnlock()
		return nil
	}
	data, err := json.MarshalIndent(&s.st, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path, append(data, '\n')); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	s.saved = gen
	return nil
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// The rename is durable once the directory is.
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// newSecret returns prefix and 32 random bytes in lowercase base32.
func newSecret(prefix string) string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// --- API keys -----------------------------------------------------------------

// ValidateScopes checks a key's scopes: at least one, each known, no repeats.
func ValidateScopes(scopes []string) error {
	if len(scopes) == 0 {
		return errors.New("a key needs at least one scope")
	}
	seen := map[string]bool{}
	for _, sc := range scopes {
		if !slices.Contains(AllScopes, sc) {
			return fmt.Errorf("scope %q: want one of %s", sc, strings.Join(AllScopes, ", "))
		}
		if seen[sc] {
			return fmt.Errorf("scope %q is given twice", sc)
		}
		seen[sc] = true
	}
	return nil
}

// CreateKey issues an API key. The secret is returned once and never stored.
func (s *FileStore) CreateKey(user, tenant string, scopes []string) (string, Key, error) {
	if !ValidUser(user) {
		return "", Key{}, fmt.Errorf("user %q: letters, digits and . _ @ + -, at most 64", user)
	}
	if tenant != "" && !ValidUser(tenant) {
		return "", Key{}, fmt.Errorf("tenant %q: letters, digits and . _ @ + -, at most 64", tenant)
	}
	if err := ValidateScopes(scopes); err != nil {
		return "", Key{}, err
	}
	secret := newSecret(keySecretPrefix)
	k := Key{ID: newID("key_"), User: user, Tenant: tenant, Scopes: slices.Clone(scopes),
		Hash: hashSecret(secret), Created: s.now().UTC()}
	err := s.change(func() error {
		if err := s.nameTakenInOrg(tenant, user, tenant); err != nil {
			return err
		}
		s.st.Keys = append(s.st.Keys, k)
		return nil
	})
	if err != nil {
		return "", Key{}, err
	}
	return secret, k, nil
}

// KeyBySecret hashes the presented secret and compares it with every stored
// hash in constant time, so how long a refusal takes says nothing about how
// close a guess was.
func (s *FileStore) KeyBySecret(secret string) (Key, bool) {
	if !strings.HasPrefix(secret, keySecretPrefix) {
		return Key{}, false
	}
	h := []byte(hashSecret(secret))
	s.mu.RLock()
	defer s.mu.RUnlock()
	found := -1
	for i := range s.st.Keys {
		if subtle.ConstantTimeCompare(h, []byte(s.st.Keys[i].Hash)) == 1 {
			found = i
		}
	}
	if found < 0 || s.st.Keys[found].Revoked {
		return Key{}, false
	}
	k := s.st.Keys[found]
	k.Scopes = slices.Clone(k.Scopes)
	return k, true
}

// ErrNoSuchKey is returned for an API key or SSH key id the store does not hold.
var ErrNoSuchKey = errors.New("no such key")

// ErrNoSuchNode is returned for a node name the store does not hold.
var ErrNoSuchNode = errors.New("no such node")

// RevokeKey marks a key revoked. It stays listed, so the record says it
// existed and when it stopped working.
func (s *FileStore) RevokeKey(id string) error {
	return s.change(func() error {
		for i := range s.st.Keys {
			if s.st.Keys[i].ID == id {
				s.st.Keys[i].Revoked = true
				return nil
			}
		}
		return ErrNoSuchKey
	})
}

// Keys lists every key, oldest first.
func (s *FileStore) Keys() []Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Key, len(s.st.Keys))
	for i, k := range s.st.Keys {
		k.Scopes = slices.Clone(k.Scopes)
		out[i] = k
	}
	return out
}

// --- SSH keys -----------------------------------------------------------------

// ErrKeyTaken refuses a public key another user has registered: a login with
// it must name one user, not whichever the server finds first.
var ErrKeyTaken = errors.New("this key is registered to another user")

// ErrKeyRegistered refuses registering one key twice for the same sandbox.
var ErrKeyRegistered = errors.New("this key is already registered")

// AddSSHKey registers a public key for user; sandbox, when set, is the
// resolved sandbox id it is limited to. The line is parsed and stored
// without options.
func (s *FileStore) AddSSHKey(user, tenant, sandbox, authorizedKey string) (SSHKey, error) {
	pk, err := ParseAuthorizedKey(authorizedKey)
	if err != nil {
		return SSHKey{}, err
	}
	k := SSHKey{ID: newID("ssh_"), User: user, Tenant: tenant, Fingerprint: pk.Fingerprint,
		AuthorizedKey: pk.Line(), Sandbox: sandbox, Created: s.now().UTC()}
	err = s.change(func() error {
		for _, o := range s.st.SSHKeys {
			if o.Fingerprint != k.Fingerprint {
				continue
			}
			if o.User != user || o.Tenant != tenant {
				return ErrKeyTaken
			}
			if o.Sandbox == sandbox {
				return ErrKeyRegistered
			}
		}
		s.st.SSHKeys = append(s.st.SSHKeys, k)
		return nil
	})
	if err != nil {
		return SSHKey{}, err
	}
	return k, nil
}

// RemoveSSHKey removes a registered key.
func (s *FileStore) RemoveSSHKey(id string) error {
	return s.change(func() error {
		for i, k := range s.st.SSHKeys {
			if k.ID == id {
				s.st.SSHKeys = slices.Delete(s.st.SSHKeys, i, i+1)
				return nil
			}
		}
		return ErrNoSuchKey
	})
}

// SSHKeysByFingerprint returns every stored key with that fingerprint.
func (s *FileStore) SSHKeysByFingerprint(fingerprint string) []SSHKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []SSHKey
	for _, k := range s.st.SSHKeys {
		if subtle.ConstantTimeCompare([]byte(k.Fingerprint), []byte(fingerprint)) == 1 {
			out = append(out, k)
		}
	}
	return out
}

// SSHKeysFor returns user's keys, oldest first.
func (s *FileStore) SSHKeysFor(user string) []SSHKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []SSHKey
	for _, k := range s.st.SSHKeys {
		if k.User == user {
			out = append(out, k)
		}
	}
	return out
}

// --- SSH tokens ---------------------------------------------------------------

// NewSSHToken issues a token for one sandbox. Only its hash is stored.
//
// A token is reusable until it expires, not single-use: the command it is
// handed out with is what a person types, and a second ssh — or an scp, or
// a reconnect after a dropped session — with the same command is the
// expected next step. What bounds it is the expiry (15 minutes unless asked
// otherwise, never more than a day) and the one sandbox it names.
func (s *FileStore) NewSSHToken(user, tenant, sandbox string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		return "", time.Time{}, errors.New("a token needs a positive lifetime")
	}
	if sandbox == "" {
		return "", time.Time{}, errors.New("a token names one sandbox")
	}
	tok := newSecret(tokenSecretPrefix)
	now := s.now().UTC()
	exp := now.Add(ttl)
	err := s.change(func() error {
		s.pruneTokens(now)
		s.st.Tokens = append(s.st.Tokens, tokenRecord{Hash: hashSecret(tok), User: user, Tenant: tenant, Sandbox: sandbox, Expires: exp})
		return nil
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

// pruneTokens drops expired tokens; under mu.
func (s *FileStore) pruneTokens(now time.Time) {
	s.st.Tokens = slices.DeleteFunc(s.st.Tokens, func(t tokenRecord) bool { return !now.Before(t.Expires) })
}

// RedeemSSHToken checks a presented token and returns what it grants, until
// it expires.
func (s *FileStore) RedeemSSHToken(token string) (SSHToken, bool) {
	if !strings.HasPrefix(token, tokenSecretPrefix) {
		return SSHToken{}, false
	}
	h := []byte(hashSecret(token))
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out SSHToken
	ok := false
	for _, t := range s.st.Tokens {
		if subtle.ConstantTimeCompare(h, []byte(t.Hash)) == 1 && now.Before(t.Expires) {
			out, ok = SSHToken{User: t.User, Tenant: t.Tenant, Sandbox: t.Sandbox, Expires: t.Expires}, true
		}
	}
	return out, ok
}

// --- ownership ----------------------------------------------------------------

// SetOwner records who owns a sandbox and on which node it runs.
func (s *FileStore) SetOwner(sandbox string, o Owner) error {
	return s.RecordSandbox(sandbox, o, 0, 0)
}

// RecordSandbox is SetOwner with the resources the sandbox was given, which
// count against its tenant's quota until it is forgotten.
func (s *FileStore) RecordSandbox(sandbox string, o Owner, cpus float64, memoryMB int) error {
	if err := s.stageSandbox(sandbox, o, cpus, memoryMB, 0); err != nil {
		return err
	}
	return s.persist()
}

// stageSandbox is RecordSandbox without the write (see stage).
func (s *FileStore) stageSandbox(sandbox string, o Owner, cpus float64, memoryMB, diskMB int) error {
	if sandbox == "" || o.User == "" || o.Node == "" {
		return errors.New("an owner needs a sandbox, a user and a node")
	}
	return s.stage(func() error {
		s.st.Sandboxes[sandbox] = sandboxRecord{Owner: o, CPUs: cpus, MemoryMB: memoryMB, DiskMB: diskMB, Recorded: s.now().UTC()}
		return nil
	})
}

// OwnerOf returns a sandbox's owner.
func (s *FileStore) OwnerOf(sandbox string) (Owner, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.st.Sandboxes[sandbox]
	return r.Owner, ok
}

// SizeOf returns what a recorded sandbox was given.
func (s *FileStore) SizeOf(sandbox string) (api.NodeResources, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.st.Sandboxes[sandbox]
	return api.NodeResources{CPUs: r.CPUs, MemoryMB: r.MemoryMB, DiskMB: r.DiskMB}, ok
}

// ForgetSandbox drops a sandbox's record. Forgetting one not held is not an error.
func (s *FileStore) ForgetSandbox(sandbox string) error {
	return s.ForgetSandboxes([]string{sandbox})
}

// ForgetSandboxes drops several records in one write.
func (s *FileStore) ForgetSandboxes(ids []string) error {
	s.mu.RLock()
	held := false
	for _, id := range ids {
		_, ok := s.st.Sandboxes[id]
		held = held || ok
	}
	s.mu.RUnlock()
	if !held {
		return nil
	}
	return s.change(func() error {
		for _, id := range ids {
			delete(s.st.Sandboxes, id)
		}
		return nil
	})
}

// Usage is what a tenant's recorded sandboxes hold.
type Usage struct {
	Sandboxes int
	CPUs      float64
	MemoryMB  int
}

// UsageOf adds up a tenant's recorded sandboxes.
func (s *FileStore) UsageOf(tenant string) Usage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var u Usage
	for _, r := range s.st.Sandboxes {
		if r.Tenant == tenant {
			u.Sandboxes++
			u.CPUs += r.CPUs
			u.MemoryMB += r.MemoryMB
		}
	}
	return u
}

// SandboxesOn returns the sandboxes recorded on node, with when each was
// recorded, for reconciling the store against the node's own listing.
func (s *FileStore) SandboxesOn(node string) map[string]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]time.Time{}
	for id, r := range s.st.Sandboxes {
		if r.Node == node {
			out[id] = r.Recorded
		}
	}
	return out
}

// --- volumes and snapshots ------------------------------------------------------

// Volumes and snapshots belong to whoever made them through the gateway, and
// live on the node that made them: a sandbox that mounts a volume or starts
// from a snapshot has to be placed there.

// SetVolume records a volume's owner and node.
func (s *FileStore) SetVolume(name string, o Owner) error {
	return s.change(func() error { s.st.Volumes[name] = o; return nil })
}

// VolumeOwner returns a volume's owner and node.
func (s *FileStore) VolumeOwner(name string) (Owner, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.st.Volumes[name]
	return o, ok
}

// ForgetVolume drops a volume's record.
func (s *FileStore) ForgetVolume(name string) error {
	return s.change(func() error { delete(s.st.Volumes, name); return nil })
}

// VolumesOf returns the volumes user (in tenant) owns, by name, with the
// node each is on.
func (s *FileStore) VolumesOf(user, tenant string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for name, o := range s.st.Volumes {
		if o.User == user && o.Tenant == tenant {
			out[name] = o.Node
		}
	}
	return out
}

// SetSnapshot records a snapshot's owner and node.
func (s *FileStore) SetSnapshot(id string, o Owner) error {
	return s.change(func() error { s.st.Snapshots[id] = o; return nil })
}

// SnapshotOwner returns a snapshot's owner and node.
func (s *FileStore) SnapshotOwner(id string) (Owner, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.st.Snapshots[id]
	return o, ok
}

// ForgetSnapshotsGoneFrom drops the records of a node's snapshots that it no
// longer holds, given the whole of what it does. A schedule's retention
// removes snapshots on the node, not through here, and each left a record
// behind: at one every five minutes, hundreds a day per sandbox.
func (s *FileStore) ForgetSnapshotsGoneFrom(node string, holds map[string]bool) error {
	s.mu.RLock()
	var gone []string
	for id, o := range s.st.Snapshots {
		if o.Node == node && !holds[id] {
			gone = append(gone, id)
		}
	}
	s.mu.RUnlock()
	if len(gone) == 0 {
		return nil
	}
	return s.change(func() error {
		for _, id := range gone {
			delete(s.st.Snapshots, id)
		}
		return nil
	})
}

// ForgetSnapshot drops a snapshot's record.
func (s *FileStore) ForgetSnapshot(id string) error {
	return s.change(func() error { delete(s.st.Snapshots, id); return nil })
}

// --- nodes --------------------------------------------------------------------

// PutNode adds a node, or replaces the one with its name.
func (s *FileStore) PutNode(n NodeConfig) error {
	if !api.ValidNodeID(n.Name) {
		return fmt.Errorf("node name %q: lowercase letters, digits and dashes, at most 31", n.Name)
	}
	return s.change(func() error {
		for i := range s.st.Nodes {
			if s.st.Nodes[i].Name == n.Name {
				s.st.Nodes[i] = n
				return nil
			}
		}
		s.st.Nodes = append(s.st.Nodes, n)
		return nil
	})
}

// RemoveNode removes a node. What runs there keeps its owner records, so
// adding the node back makes its sandboxes reachable again.
func (s *FileStore) RemoveNode(name string) error {
	return s.change(func() error {
		for i, n := range s.st.Nodes {
			if n.Name == name {
				s.st.Nodes = slices.Delete(s.st.Nodes, i, i+1)
				return nil
			}
		}
		return ErrNoSuchNode
	})
}

// Nodes lists the stored nodes by name.
func (s *FileStore) Nodes() []NodeConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := slices.Clone(s.st.Nodes)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var _ Store = (*FileStore)(nil)
