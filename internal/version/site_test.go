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
//
// The rewrite removed the copy instead: the site pins no version, installs the
// latest release, and links the releases page. Nothing can go stale that does
// not exist, so what this pins now is that a version does not creep back —
// as a VERSION constant, or as an installer's --version argument.
func TestSiteDoesNotPinAVersion(t *testing.T) {
	const path = "../../web/src/lib/site.ts"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no site to check (%v)", err) // a checkout without web/ is not a failure
	}
	for _, re := range []string{`export const VERSION\b`, `--version \d`, `"\d+\.\d+\.\d+[^"]*"`} {
		if m := regexp.MustCompile(re).Find(data); m != nil {
			t.Errorf("%s pins a version (%q): it will be stale by the next release; install the latest and link the releases page", path, m)
		}
	}
}
