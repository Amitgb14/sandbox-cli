package gateway

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func reopen(t *testing.T, s *FileStore, path string) *FileStore {
	t.Helper()
	s.Close()
	s2, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	return s2
}

// The file holds hashes; a secret that reached it would be readable by
// anyone who reads a backup of it.
func TestKeySecretIsNeverStored(t *testing.T) {
	s, path := openStore(t)
	secret, k, err := s.CreateKey("alice", "acme", []string{ScopeRead, ScopeCreate})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "sgk_") || len(secret) < 4+52 {
		t.Fatalf("secret %q: want sgk_ and 32 random bytes", secret)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), strings.TrimPrefix(secret, "sgk_")) {
		t.Fatal("the key's secret is in the state file")
	}
	if !strings.Contains(string(data), k.Hash) || len(k.Hash) != 64 {
		t.Fatalf("hash %q is not the stored hex SHA-256", k.Hash)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode %v, want 0600", fi.Mode().Perm())
	}

	got, ok := s.KeyBySecret(secret)
	if !ok || got.ID != k.ID || got.User != "alice" || got.Tenant != "acme" {
		t.Fatalf("KeyBySecret = %+v, %v", got, ok)
	}
	for _, wrong := range []string{"", "sgk_", secret + "x", secret[:len(secret)-1], k.Hash, strings.Replace(secret, "sgk_", "sgt_", 1)} {
		if _, ok := s.KeyBySecret(wrong); ok {
			t.Errorf("KeyBySecret(%q) matched", wrong)
		}
	}

	s = reopen(t, s, path)
	if _, ok := s.KeyBySecret(secret); !ok {
		t.Fatal("the key did not survive a restart")
	}
	if err := s.RevokeKey(k.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.KeyBySecret(secret); ok {
		t.Fatal("a revoked key still authenticates")
	}
	s = reopen(t, s, path)
	if _, ok := s.KeyBySecret(secret); ok {
		t.Fatal("a revoked key came back after a restart")
	}
	if !errors.Is(s.RevokeKey("key_nope"), ErrNoSuchKey) {
		t.Error("revoking an unknown key did not say so")
	}
}

func TestCreateKeyValidates(t *testing.T) {
	s, _ := openStore(t)
	for _, c := range []struct {
		user, tenant string
		scopes       []string
	}{
		{"", "", []string{ScopeRead}},
		{"has space", "", []string{ScopeRead}},
		{"alice", "bad\ntenant", []string{ScopeRead}},
		{"alice", "", nil},
		{"alice", "", []string{"sandbox:everything"}},
		{"alice", "", []string{ScopeRead, ScopeRead}},
	} {
		if _, _, err := s.CreateKey(c.user, c.tenant, c.scopes); err == nil {
			t.Errorf("CreateKey(%q, %q, %v) was accepted", c.user, c.tenant, c.scopes)
		}
	}
}

// A file others can read is refused rather than used: it says who owns
// what, and permissions loosened by hand are a mistake worth stopping on.
func TestStoreRefusesAReadableFile(t *testing.T) {
	s, path := openStore(t)
	s.Close()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileStore(path); err == nil || !strings.Contains(err.Error(), "readable by others") {
		t.Fatalf("open = %v; want refused", err)
	}
}

// Two processes on one file would each overwrite the other's changes, and a
// revocation made by one would be undone by the other.
func TestStoreIsHeldByOneProcess(t *testing.T) {
	_, path := openStore(t)
	if _, err := OpenFileStore(path); err == nil {
		t.Fatal("a second open of a held state file succeeded")
	}
}

func TestStoreRejectsUnknownFields(t *testing.T) {
	s, path := openStore(t)
	s.Close()
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), `"version": 1`, `"version": 1, "admin_bypass": true`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileStore(path); err == nil {
		t.Fatal("a state file with an unknown field was loaded")
	}
}

// Concurrent changes are coalesced into fewer writes, and none is lost.
func TestStoreConcurrentChangesAllLand(t *testing.T) {
	s, path := openStore(t)
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RecordSandbox(fmt.Sprintf("sbx_%016x", i), Owner{User: "u", Tenant: "t", Node: "n1"}, 1, 512); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	s = reopen(t, s, path)
	u := s.UsageOf("t")
	if u.Sandboxes != 200 || u.CPUs != 200 || u.MemoryMB != 200*512 {
		t.Fatalf("usage after restart %+v", u)
	}
	if err := s.ForgetSandboxes([]string{"sbx_0000000000000000", "sbx_0000000000000001"}); err != nil {
		t.Fatal(err)
	}
	if s.UsageOf("t").Sandboxes != 198 {
		t.Fatal("forgotten sandboxes still count")
	}
	if _, ok := s.OwnerOf("sbx_0000000000000002"); !ok {
		t.Fatal("a recorded owner is missing")
	}
}

func TestSSHTokens(t *testing.T) {
	s, path := openStore(t)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	tok, exp, err := s.NewSSHToken("alice", "acme", "sbx_0123456789abcdef", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "sgt_") || !exp.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("token %q expires %v", tok, exp)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), tok) {
		t.Fatal("the token is in the state file")
	}
	// Reusable until it expires: a second ssh with the same command works.
	for range 2 {
		got, ok := s.RedeemSSHToken(tok)
		if !ok || got.User != "alice" || got.Tenant != "acme" || got.Sandbox != "sbx_0123456789abcdef" {
			t.Fatalf("redeem = %+v, %v", got, ok)
		}
	}
	if _, ok := s.RedeemSSHToken(tok + "x"); ok {
		t.Fatal("a wrong token was redeemed")
	}
	now = now.Add(15 * time.Minute)
	if _, ok := s.RedeemSSHToken(tok); ok {
		t.Fatal("an expired token was redeemed")
	}
	// An expired token is pruned at the next issue.
	if _, _, err := s.NewSSHToken("bob", "", "sbx_0123456789abcdee", time.Minute); err != nil {
		t.Fatal(err)
	}
	if n := len(s.st.Tokens); n != 1 {
		t.Fatalf("%d tokens kept; the expired one should be gone", n)
	}
	for _, bad := range []time.Duration{0, -time.Second} {
		if _, _, err := s.NewSSHToken("alice", "", "sbx_0123456789abcdef", bad); err == nil {
			t.Errorf("ttl %v accepted", bad)
		}
	}
	if _, _, err := s.NewSSHToken("alice", "", "", time.Minute); err == nil {
		t.Error("a token for no sandbox was issued")
	}
}

func TestStoreSSHKeys(t *testing.T) {
	s, _ := openStore(t)
	line := ed25519Line(t, "alice@laptop")
	k, err := s.AddSSHKey("alice", "acme", "", line)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Fingerprint, "SHA256:") || k.AuthorizedKey != line {
		t.Fatalf("stored %+v", k)
	}
	if _, err := s.AddSSHKey("alice", "acme", "", line); !errors.Is(err, ErrKeyRegistered) {
		t.Errorf("registering twice: %v", err)
	}
	if _, err := s.AddSSHKey("alice", "acme", "sbx_0123456789abcdef", line); err != nil {
		t.Errorf("the same key limited to one sandbox: %v", err)
	}
	if _, err := s.AddSSHKey("bob", "acme", "", line); !errors.Is(err, ErrKeyTaken) {
		t.Errorf("another user's key: %v", err)
	}
	if _, err := s.AddSSHKey("alice", "other", "", line); !errors.Is(err, ErrKeyTaken) {
		t.Errorf("the same user in another tenant: %v", err)
	}
	if _, err := s.AddSSHKey("alice", "acme", "", `command="sh" `+line); err == nil {
		t.Error("a key with options was stored")
	}
	if got := s.SSHKeysByFingerprint(k.Fingerprint); len(got) != 2 {
		t.Errorf("by fingerprint: %d keys", len(got))
	}
	if got := s.SSHKeysFor("alice"); len(got) != 2 {
		t.Errorf("alice has %d keys", len(got))
	}
	if err := s.RemoveSSHKey(k.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.SSHKeysFor("alice"); len(got) != 1 {
		t.Errorf("after remove: %d keys", len(got))
	}
	if !errors.Is(s.RemoveSSHKey(k.ID), ErrNoSuchKey) {
		t.Error("removing twice did not say so")
	}
}

func TestStoreNodesVolumesSnapshots(t *testing.T) {
	s, path := openStore(t)
	if err := s.PutNode(NodeConfig{Name: "Bad_Name", Endpoint: "unix:///x"}); err == nil {
		t.Error("a node name that cannot appear in an id was stored")
	}
	for _, n := range []string{"n2", "n1"} {
		if err := s.PutNode(NodeConfig{Name: n, Endpoint: "unix:///run/" + n}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutNode(NodeConfig{Name: "n1", Endpoint: "unix:///run/n1b"}); err != nil {
		t.Fatal(err)
	}
	o := Owner{User: "alice", Tenant: "acme", Node: "n1"}
	if err := s.SetVolume("data", o); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSnapshot("snp_0123456789abcdef", o); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s, path)
	nodes := s.Nodes()
	if len(nodes) != 2 || nodes[0].Name != "n1" || nodes[0].Endpoint != "unix:///run/n1b" {
		t.Fatalf("nodes %+v", nodes)
	}
	if got, ok := s.VolumeOwner("data"); !ok || got != o {
		t.Fatalf("volume owner %+v %v", got, ok)
	}
	if got := s.VolumesOf("alice", "acme"); got["data"] != "n1" {
		t.Fatalf("alice's volumes %v", got)
	}
	if got := s.VolumesOf("alice", "other"); len(got) != 0 {
		t.Fatalf("alice in another tenant sees %v", got)
	}
	if got, ok := s.SnapshotOwner("snp_0123456789abcdef"); !ok || got != o {
		t.Fatalf("snapshot owner %+v %v", got, ok)
	}
	if err := s.RemoveNode("n2"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.RemoveNode("n2"), ErrNoSuchNode) {
		t.Error("removing a node twice did not say so")
	}
	_ = s.ForgetVolume("data")
	_ = s.ForgetSnapshot("snp_0123456789abcdef")
	if _, ok := s.VolumeOwner("data"); ok {
		t.Error("forgotten volume still owned")
	}
}
