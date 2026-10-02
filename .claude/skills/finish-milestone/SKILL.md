---
name: finish-milestone
description: Close a milestone of the rewrite plan — verification, the plan update, the hand-over of real-host checks, and the commit. Use when a milestone's work is believed complete.
---

# Finishing a milestone

A milestone is done when someone other than its author could check it. Work
through the **Before you finish** checklist at the end of `AGENTS.md` first;
this adds what is specific to a milestone.

1. **Green locally:**
   ```sh
   gofmt -l cmd internal; go vet ./...; go test -race -count=1 ./...
   ```
   From M2 on, the conformance suite against the fake as well.
2. **The plan says what happened**, not what was intended. In
   `docs/rewrite/PLAN.md`, mark the milestone *Done* and list:
   - what moved or was built;
   - what was dropped, and why;
   - what was left for a later milestone, and which one;
   - any decision taken along the way that the plan did not foresee.
3. **Nothing dropped silently.** Every file that left `_old/` is either in the
   new tree or recorded in the plan as dropped. `git status` shows no stray
   deletions.
4. **Real-host checks are handed over, not run.** For anything that needs a Mac
   with macOS 26 or a KVM Linux host, give the maintainer:
   - the exact command;
   - the preconditions (OS version, `/dev/kvm` access, a built `sandboxd`);
   - what a pass proves, and what it does not.
5. **Security-relevant changes** have been through `trust-boundary-review`.
6. **Commit** with `commit-and-pr`, one commit per milestone unless the plan
   commit is separate. Push when asked.
7. **Say what is next:** the next milestone's first concrete step, and any open
   question from the plan it depends on.
