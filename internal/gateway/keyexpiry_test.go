package gateway

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A key issued to expire works until then and is refused after, as a
// revoked one is: at the API, and for SSH and jobs, which ask whether its
// user still holds an active key.
func TestAKeyStopsWorkingWhenItExpires(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	k, err := tg.admin.CreateKey(ctx, api.CreateKeyRequest{User: "dana", Tenant: "dana", Scopes: demoScopes, ExpiresInSecs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if k.Expires == nil || time.Until(*k.Expires) > time.Second {
		t.Fatalf("expires: %v", k.Expires)
	}
	dana := api.NewClientWithHTTP(tg.ts.URL, k.Secret, tg.ts.Client())
	if _, err := dana.Whoami(ctx); err != nil {
		t.Fatalf("before it expires: %v", err)
	}
	if !userActive(tg.store, "dana", "dana") {
		t.Fatal("before it expires, dana holds no active key")
	}
	time.Sleep(1100 * time.Millisecond)
	_, err = dana.Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	if userActive(tg.store, "dana", "dana") || userMaySSH(tg.store, "dana", "dana") {
		t.Error("an expired key still counts as active, for jobs or for SSH")
	}

	// Renewed, it works again; listed, it says when it ends.
	got, err := tg.admin.SetKeyExpiry(ctx, k.ID, 3600)
	if err != nil || got.Expires == nil || time.Until(*got.Expires) < 59*time.Minute {
		t.Fatalf("renewed: %+v, %v", got.Expires, err)
	}
	if _, err := dana.Whoami(ctx); err != nil {
		t.Fatalf("after renewal: %v", err)
	}
	keys, _ := tg.admin.Keys(ctx)
	for _, ki := range keys {
		if ki.ID == k.ID && (ki.Expires == nil || !ki.Expires.Equal(*got.Expires)) {
			t.Errorf("listed expiry %v, want %v", ki.Expires, got.Expires)
		}
	}
	// Never.
	if got, err := tg.admin.SetKeyExpiry(ctx, k.ID, 0); err != nil || got.Expires != nil {
		t.Errorf("never: %+v, %v", got.Expires, err)
	}
}

// Expiring a key now ends what it has open at once, as revoking it does:
// here, its owner's running job.
func TestExpiringAKeyNowCancelsItsOwnersJobs(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	k, err := tg.admin.CreateKey(ctx, api.CreateKeyRequest{User: "ana", Scopes: []string{ScopeRead, ScopeCreate}, ExpiresInSecs: 86400})
	if err != nil {
		t.Fatal(err)
	}
	ana := api.NewClientWithHTTP(tg.ts.URL, k.Secret, tg.ts.Client())
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, ana, j.ID, "the run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })
	if _, err := tg.admin.SetKeyExpiry(ctx, k.ID, -1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ana's job to be cancelled", func() bool { r, _ := tg.store.Job(j.ID); return r.State == api.JobCancelled })
	_, err = ana.Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
}

// Only an admin sets an expiry; a revoked key is not renewed, an unknown one
// is not found, and an expiry is never in the past at issue.
func TestKeyExpiryRefusals(t *testing.T) {
	tg := startGateway(t, nil)
	ctx := ctxT(t)
	k, err := tg.admin.CreateKey(ctx, api.CreateKeyRequest{User: "eve", Scopes: demoScopes})
	if err != nil {
		t.Fatal(err)
	}
	if k.Expires != nil {
		t.Errorf("a key issued without an expiry has one: %v", k.Expires)
	}
	eve := api.NewClientWithHTTP(tg.ts.URL, k.Secret, tg.ts.Client())
	_, err = eve.SetKeyExpiry(ctx, k.ID, 0)
	wantCode(t, err, api.CodeRefused)
	_, err = tg.admin.CreateKey(ctx, api.CreateKeyRequest{User: "fay", Scopes: demoScopes, ExpiresInSecs: -5})
	wantCode(t, err, api.CodeInvalidRequest)
	_, err = tg.admin.SetKeyExpiry(ctx, "key_nope", 60)
	wantCode(t, err, api.CodeNotFound)
	if code := tg.do(adminSecret(t, tg), http.MethodDelete, "/v1/admin/keys/"+k.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	_, err = tg.admin.SetKeyExpiry(ctx, k.ID, 3600)
	wantCode(t, err, api.CodeConflict)
}

// Expiry is kept across a restart, and a state file from before it loads
// with its keys never expiring.
func TestKeyExpirySurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	secret, k, err := st.CreateKeyExpiring("gus", "", demoScopes, &at)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateKeyExpiring("gus", "", demoScopes, &time.Time{}); err == nil {
		t.Error("a key issued already expired")
	}
	st.Close()
	st, err = OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, ok := st.KeyBySecret(secret)
	if !ok || got.ID != k.ID || got.Expires == nil || !got.Expires.Equal(at) {
		t.Fatalf("after a reopen: %+v %v", got, ok)
	}
	st.now = func() time.Time { return at }
	if _, ok := st.KeyBySecret(secret); ok {
		t.Error("the key was accepted at its expiry")
	}
}
