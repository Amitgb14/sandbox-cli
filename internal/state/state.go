// Package state records what no later command could otherwise recover about a
// sandbox: its repository, branch, agent and base.
//
// Docker labels where the backend has them, a catalog under
// ~/.config/sandbox/instances/ where it does not — never both for one backend,
// since two sources for one fact is how a listing and a reaper come to disagree.
// Filled in at M2 (labels) and with the first label-less backend (catalog).
package state
