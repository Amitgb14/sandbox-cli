package image

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A private repository is pulled with the operator's login for it, and only
// with one that covers it; without one, the error says how to give one.
func TestPullAPrivateImageWithTheOperatorsLogin(t *testing.T) {
	r, _, _ := newRegistry(t, "amd64")
	r.login = "amit:s3cret-token"
	p, host := puller(t, r)
	ctx := context.Background()

	_, err := p.Pull(ctx, host+"/team/app:1.0")
	if err == nil || !strings.Contains(err.Error(), "--registry-auth-file") {
		t.Fatalf("an anonymous pull of a private image: %v; want it refused, naming --registry-auth-file", err)
	}

	// A login for other repositories is not sent.
	p.Logins = []Login{{Registry: host, Prefix: "other/", Username: "amit", Password: "s3cret-token"}}
	if _, err := p.Pull(ctx, host+"/team/app:1.0"); err == nil {
		t.Fatal("a login for other/ was used for team/app")
	}

	p.Logins = []Login{{Registry: host, Prefix: "team/", Username: "amit", Password: "wrong-token"}}
	_, err = p.Pull(ctx, host+"/team/app:1.0")
	if err == nil || !strings.Contains(err.Error(), "refused the login for "+host+"/team") {
		t.Fatalf("a wrong token: %v", err)
	}
	if strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("the error holds the token: %v", err)
	}

	p.Logins = []Login{
		{Registry: host, Prefix: "team/", Username: "amit", Password: "wrong-token"},
		{Registry: host, Prefix: "team/app", Username: "amit", Password: "s3cret-token"},
	}
	if _, err := p.Pull(ctx, host+"/team/app:1.0"); err != nil {
		t.Fatalf("the longest prefix's login: %v", err)
	}
}

func TestLoadLogins(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		os.Chmod(path, mode)
		return path
	}
	good := "logins:\n  - registry: ghcr.io\n    repositories: amitgb14/\n    username: amitgb14\n    password: ghp_secret\n"
	ls, err := LoadLogins(write("good", good, 0o600))
	if err != nil || len(ls) != 1 || ls[0].Registry != "ghcr.io" || ls[0].Prefix != "amitgb14/" || ls[0].Password != "ghp_secret" {
		t.Fatalf("good file: %+v, %v", ls, err)
	}
	if _, err := LoadLogins(write("open", good, 0o644)); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a file others can read: %v", err)
	}
	for name, body := range map[string]string{
		"no repositories":   "logins:\n  - registry: ghcr.io\n    username: u\n    password: ghp_secret\n",
		"a whole registry":  "logins:\n  - registry: ghcr.io\n    repositories: /\n    username: u\n    password: ghp_secret\n",
		"no password":       "logins:\n  - registry: ghcr.io\n    repositories: a/\n    username: u\n",
		"a url as registry": "logins:\n  - registry: https://ghcr.io/\n    repositories: a/\n    username: u\n    password: ghp_secret\n",
		"an unknown key":    "logins:\n  - registry: ghcr.io\n    repositories: a/\n    username: u\n    password: ghp_secret\n    token: ghp_secret\n",
		"twice":             good + "  - registry: ghcr.io\n    repositories: amitgb14\n    username: v\n    password: ghp_secret\n",
		"none":              "logins: []\n",
		"not yaml":          "logins: [password: ghp_secret\n",
	} {
		_, err := LoadLogins(write(strings.ReplaceAll(name, " ", "-"), body, 0o600))
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "ghp_secret") {
			t.Errorf("%s: the error holds the token: %v", name, err)
		}
	}
}
