package worktree

import "strings"

// Branches lists the repository's local branches, and which one is checked out.
//
// Through runGit like everything else here, so githard applies: `for-each-ref`
// is not one of the commands that runs a hook, but the rule this package keeps
// is that no git call it makes on its own behalf is exempt — the exemption is
// what makes the next one easy to add without noticing.
//
// The current branch comes back separately rather than being marked in the list,
// because a caller offering a choice wants a default and a caller rendering a
// list wants names, and folding the two into one decorated string makes both
// parse it back out. Empty when HEAD is detached, which is a real state and not
// an error: a run based on a detached HEAD is a thing somebody can do.
func Branches(root string) (names []string, current string, err error) {
	out, err := runGit(root, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if b := strings.TrimSpace(line); b != "" {
			names = append(names, b)
		}
	}
	// `--quiet` so a detached HEAD is an empty answer rather than a failure the
	// caller has to tell apart from a broken repository.
	if head, herr := runGit(root, "symbolic-ref", "--quiet", "--short", "HEAD"); herr == nil {
		current = strings.TrimSpace(head)
	}
	return names, current, nil
}
