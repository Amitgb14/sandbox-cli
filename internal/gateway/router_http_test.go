package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// No two (service, tenant) pairs share a router name, and each name reads
// back as the pair that made it: the router can never send one tenant's
// traffic to another's service.
func TestRouterNamesCannotCollide(t *testing.T) {
	names := []string{"a", "web", "web-api", "a-b", "b", "x1"}
	tenants := []string{"", "a", "b", "acme", "a-b", "b--c", "web", "Acme", "acme.corp", "x@y"}
	seen := map[string][2]string{}
	for _, n := range names {
		if !validServiceName(n) {
			t.Fatalf("%q should be a valid name", n)
		}
		for _, tn := range tenants {
			l, ok := routerLabel(n, tn)
			if !ok {
				if tenantLabelRE.MatchString(tn) {
					t.Errorf("(%s, %s) has no name", n, tn)
				}
				continue
			}
			if prev, dup := seen[l]; dup {
				t.Errorf("%s names both %v and (%s, %s)", l, prev, n, tn)
			}
			seen[l] = [2]string{n, tn}
			gotName, gotTenant, _ := strings.Cut(l, "--")
			if gotName != n || gotTenant != tn {
				t.Errorf("%s reads back as (%s, %s), not (%s, %s)", l, gotName, gotTenant, n, tn)
			}
		}
	}
	for _, bad := range []string{"a--b", "-a", "a-", "A", "a.b", "", strings.Repeat("a", 64), "xn--abc"} {
		if validServiceName(bad) {
			t.Errorf("%q is a valid service name", bad)
		}
	}
	if _, ok := routerLabel(strings.Repeat("a", 40), strings.Repeat("b", 30)); ok {
		t.Error("a name over 63 bytes was given")
	}
}

func TestRouterMakesCookiesHostOnly(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "s=1; Domain=apps.example.com; Path=/; HttpOnly; Secure")
	resp.Header.Add("Set-Cookie", "t=2; Path=/x")
	resp.Header.Add("Set-Cookie", "\x00broken")
	if err := hostOnlyCookies(resp); err != nil {
		t.Fatal(err)
	}
	got := resp.Header.Values("Set-Cookie")
	if len(got) != 2 {
		t.Fatalf("Set-Cookie = %q", got)
	}
	for _, c := range got {
		if strings.Contains(strings.ToLower(c), "domain") {
			t.Errorf("%q keeps its domain", c)
		}
	}
	if !strings.Contains(got[0], "HttpOnly") || !strings.Contains(got[0], "Secure") || !strings.Contains(got[1], "Path=/x") {
		t.Errorf("attributes lost: %q", got)
	}
}

// A host names one service by one name. "web--" once read as the default
// tenant's "web" — the same service at a second origin, whose cookies and
// storage the first does not share.
func TestRouterHostNamesOneServiceOnce(t *testing.T) {
	pub := func(name, tenant string) *service {
		return &service{rec: serviceRecord{Name: name, Tenant: tenant, Spec: api.ServiceSpec{Name: name, Public: true}}}
	}
	g := &Gateway{services: &serviceCtl{svcs: map[string]*service{
		svcKey("", "web"):     pub("web", ""),
		svcKey("acme", "web"): pub("web", "acme"),
	}}}
	for host, want := range map[string]bool{
		"web.apps.test": true, "WEB.apps.test.": true, "web.apps.test:8080": true, "web--acme.apps.test": true,
		"web--.apps.test": false, "web--acme--.apps.test": false, "x.web.apps.test": false, "apps.test": false,
		"web.other.test": false,
	} {
		if _, ok := g.routeHost(host, "apps.test"); ok != want {
			t.Errorf("%s routes: %v, want %v", host, ok, want)
		}
	}
}
