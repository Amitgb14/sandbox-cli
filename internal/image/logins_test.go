package image

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// A private repository is pulled with the login the request carries, and
// only with one for its registry; without one, the error says so, and never
// holds the token.
func TestPullAPrivateImageWithTheRequestsLogin(t *testing.T) {
	r, _, _ := newRegistry(t, "amd64")
	r.login = "amit:s3cret-token"
	p, host := puller(t, r)
	ctx := context.Background()

	_, err := p.Pull(ctx, host+"/team/app:1.0")
	if err == nil || !strings.Contains(err.Error(), "refused an anonymous pull") || !strings.Contains(err.Error(), "registry login for "+host) {
		t.Fatalf("an anonymous pull of a private image: %v; want it refused, saying a login is needed", err)
	}

	// A login for another registry is not sent.
	other := WithLogin(ctx, Login{Registry: "ghcr.io", Username: "amit", Password: "s3cret-token"})
	if _, err := p.Pull(other, host+"/team/app:1.0"); err == nil {
		t.Fatal("a login for ghcr.io was used for " + host)
	}

	wrong := WithLogin(ctx, Login{Registry: host, Username: "amit", Password: "wrong-token"})
	_, err = p.Pull(wrong, host+"/team/app:1.0")
	if err == nil || !strings.Contains(err.Error(), "refused the login of amit for "+host) {
		t.Fatalf("a wrong token: %v", err)
	}
	if strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("the error holds the token: %v", err)
	}

	right := WithLogin(ctx, Login{Registry: host, Username: "amit", Password: "s3cret-token"})
	if _, err := p.Pull(right, host+"/team/app:1.0"); err != nil {
		t.Fatalf("the right login: %v", err)
	}
}

// What one requester's login fetched is not another's: the pull token it
// earned is not sent for a pull without it, and the manifest kept for an
// offline start is not read by one. Before tokens and manifests were kept
// per login, a pull with no login, after one with it, was served the
// private image.
func TestALoginsPullIsNotHandedToAPullWithoutIt(t *testing.T) {
	r, digest, _ := newRegistry(t, "amd64")
	r.login = "amit:s3cret-token"
	var host string
	srv := httptest.NewServer(r.handler(&host))
	host = strings.TrimPrefix(srv.URL, "http://")
	p := &Puller{Cache: t.TempDir(), PlainHTTP: map[string]bool{host: true}, Arch: "amd64"}
	ctx := context.Background()
	right := WithLogin(ctx, Login{Registry: host, Username: "amit", Password: "s3cret-token"})
	if _, err := p.Pull(right, host+"/team/app:1.0"); err != nil {
		t.Fatal(err)
	}

	if _, err := p.Pull(ctx, host+"/team/app:1.0"); err == nil {
		t.Fatal("a pull without the login used the token the login earned")
	}
	wrong := WithLogin(ctx, Login{Registry: host, Username: "amit", Password: "wrong-token"})
	if _, err := p.Pull(wrong, host+"/team/app:1.0"); err == nil {
		t.Fatal("a pull with another login used the token the right one earned")
	}

	srv.Close() // offline: the cached manifest is the login's alone
	if _, err := p.Pull(ctx, host+"/team/app:1.0"); err == nil {
		t.Fatal("offline, a pull without the login was served the manifest the login fetched")
	}
	got, err := p.Pull(right, host+"/team/app:1.0")
	if err != nil {
		t.Fatalf("offline, with the login: %v", err)
	}
	if got.Digest != digest {
		t.Errorf("offline digest %s, want %s", got.Digest, digest)
	}
}
