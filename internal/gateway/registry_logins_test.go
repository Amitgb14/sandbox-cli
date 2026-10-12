package gateway

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

const registryToken = "ghp_the-token-0123456789-do-not-leak"

// An organization's registry login is sealed, never returned, and added to
// its own creates of an image on that registry, and to no one else's.
func TestARegistryLoginIsTheOrganizationsAlone(t *testing.T) {
	n1 := startNode(t, "n1", append(allCaps, api.CapRegistryLogin)...)
	tg := startGateway(t, withKey, n1)
	ctx := ctxT(t)
	ana := tg.client("ana", "aa", ScopeRead, ScopeCreate, ScopeDelete, ScopeSecretsWrite)
	if err := ana.SetRegistryLogin(ctx, "GHCR.io", "ana-gh", registryToken); err != nil {
		t.Fatal(err)
	}
	list, err := ana.RegistryLogins(ctx)
	if err != nil || len(list) != 1 || list[0].Registry != "ghcr.io" || list[0].Username != "ana-gh" || list[0].Updated.IsZero() {
		t.Fatalf("logins = %+v, %v", list, err)
	}
	sec, _, _ := tg.store.CreateKey("ana", "aa", []string{ScopeRead})
	resp := tg.raw(http.MethodGet, "/v1/registry-logins", sec, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), registryToken) {
		t.Fatalf("the listing holds the token: %s", body)
	}
	if bytes.Contains(tg.store.rawBytes(), []byte(registryToken)) {
		t.Fatal("the store holds the token's plaintext")
	}

	// Added to the organization's create of an image on that registry.
	sb, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{Image: "ghcr.io/ana/app:1"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := n1.be.RegistryLogin(sb.ID)
	if want := (backend.RegistryLogin{Registry: "ghcr.io", Username: "ana-gh", Password: registryToken}); !ok || got != want {
		t.Fatalf("the node got %+v, %v; want %+v", got, ok, want)
	}
	// Not to one of another registry's image.
	sb, err = ana.CreateSandbox(ctx, api.CreateSandboxRequest{Image: "quay.io/ana/app:1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := n1.be.RegistryLogin(sb.ID); ok {
		t.Fatalf("a quay.io image was sent the ghcr.io login: %+v", got)
	}

	// Another organization neither sees it nor pulls with it.
	bo := tg.client("bo", "bb", ScopeRead, ScopeCreate, ScopeSecretsWrite)
	if l, err := bo.RegistryLogins(ctx); err != nil || len(l) != 0 {
		t.Fatalf("another organization sees %+v, %v", l, err)
	}
	sb, err = bo.CreateSandbox(ctx, api.CreateSandboxRequest{Image: "ghcr.io/ana/app:1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := n1.be.RegistryLogin(sb.ID); ok {
		t.Fatalf("another organization's create was sent the login: %+v", got)
	}
	wantCode(t, bo.DeleteRegistryLogin(ctx, "ghcr.io"), api.CodeNotFound)

	// Sealed to its organization: moved to another, it does not open.
	user, sealed, _, err := tg.store.Store.RegistryLogin(ctx, "aa", "ghcr.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := tg.store.Store.PutRegistryLogin(ctx, "bb", "ghcr.io", user, sealed); err != nil {
		t.Fatal(err)
	}
	_, err = bo.CreateSandbox(ctx, api.CreateSandboxRequest{Image: "ghcr.io/ana/app:1"})
	if err == nil || strings.Contains(err.Error(), registryToken) {
		t.Fatalf("a sealed login moved to another organization: %v", err)
	}

	// A client's own login is refused; so is setting one without the scope.
	_, err = ana.CreateSandbox(ctx, api.CreateSandboxRequest{Image: "ghcr.io/ana/app:1",
		RegistryLogin: &api.RegistryLogin{Registry: "ghcr.io", Username: "x", Password: "y"}})
	wantCode(t, err, api.CodeInvalidRequest)
	reader := tg.client("ana", "aa", ScopeRead, ScopeCreate)
	wantCode(t, reader.SetRegistryLogin(ctx, "ghcr.io", "x", "y"), api.CodeRefused)
	wantCode(t, reader.DeleteRegistryLogin(ctx, "ghcr.io"), api.CodeRefused)

	// Malformed requests.
	for _, c := range []struct{ registry, user, pw string }{
		{"ghcr.io:x", "u", "p"}, {"-ghcr.io", "u", "p"}, {"ghcr.io", "", "p"}, {"ghcr.io", "a:b", "p"}, {"ghcr.io", "u", ""}, {"ghcr.io", "u", "p\nq"},
	} {
		err := ana.SetRegistryLogin(ctx, c.registry, c.user, c.pw)
		wantCode(t, err, api.CodeInvalidRequest)
	}

	if err := ana.DeleteRegistryLogin(ctx, "ghcr.io"); err != nil {
		t.Fatal(err)
	}
	if l, _ := ana.RegistryLogins(ctx); len(l) != 0 {
		t.Fatalf("after removal: %+v", l)
	}
}

// A create that needs a login goes only to a node that can pull with one:
// on a node that cannot, it would pull without the login and fail, or, for
// an image cached there, not be asked for it at all.
func TestALoginsCreateGoesToANodeThatCanUseIt(t *testing.T) {
	without := startNode(t, "n1", allCaps...)
	with := startNode(t, "n2", append(allCaps, api.CapRegistryLogin)...)
	tg := startGateway(t, withKey, without, with)
	ctx := ctxT(t)
	ana := tg.client("ana", "aa", ScopeRead, ScopeCreate, ScopeSecretsWrite)
	if err := ana.SetRegistryLogin(ctx, "ghcr.io", "ana-gh", registryToken); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		sb, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{Image: "ghcr.io/ana/app:1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := with.be.RegistryLogin(sb.ID); !ok {
			t.Fatalf("%s was not placed on the node that can use the login", sb.ID)
		}
	}
}
