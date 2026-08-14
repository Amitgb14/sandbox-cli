package version

import (
	"os"
	"regexp"
	"testing"
)

// The site prints a version, and nothing was keeping it honest.
//
// `web/src/lib/site.ts` carries its own constant because the site is a static
// export with no Go in it, and the release ritual — date the changelog, bump
// version.Version — never mentioned that third copy. It sat at 0.0.1beta.11
// through two releases and was noticed by a reader, not by us: the front page
// advertised a version three behind the one being downloaded from it.
//
// A doc line would have been the same instruction that was already being
// followed and still missed. This is the version of that instruction that fails.
func TestSiteVersionMatchesTheBinary(t *testing.T) {
	const path = "../../web/src/lib/site.ts"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no site to check (%v)", err) // a checkout without web/ is not a failure
	}
	m := regexp.MustCompile(`export const VERSION = "([^"]+)"`).FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s no longer declares `export const VERSION = \"…\"`; update this test with it", path)
	}
	if got := string(m[1]); got != Version {
		t.Fatalf("the site says %s and the binary says %s — bump %s in the release commit", got, Version, path)
	}
}
