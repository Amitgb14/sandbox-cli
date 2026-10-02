// Package session is list, logs, attach, kill, clean and stats: supervising
// sandboxes that outlive the process that started them.
//
// The rule carried over from the old tree: a reference is matched against our
// own listing and never handed to the engine to resolve, so `kill postgres`
// finds nothing rather than somebody's database. Built at M4.
package session
