package cli

import (
	"fmt"
	"os"

	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// shareMount resolves --share into a bind-mount string and says so on stderr.
//
// The resolution itself is sandbox.ShareMount, shared with the Studio daemon,
// which cannot import this package and must not have a second copy of code that
// decides what a container can reach. What is left here is the sentence: a run
// that widens the boundary says which directory it widened it to, at the moment
// it happens, to the person who typed the flag.
func shareMount(name string) (string, error) {
	sm, err := sandbox.ShareMount(name)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "sandbox-cli: sharing %s at %s\n", sm.Host, sm.Target)
	return sm.Mount, nil
}
