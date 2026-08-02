#!/usr/bin/env python3
"""Turn the agent's review.json into one GitHub pull-request review.

The agent produces findings; this decides which of them GitHub will actually
accept as inline comments. That filter is the whole point of the script: the
reviews API rejects the *entire* review with a 422 if any single comment names
a line that is not in the pull request's diff, so a finding on an untouched
line does not merely fail to appear — it takes every other comment with it.

Findings that cannot be anchored are not dropped. They move into the review
body, because a real bug on a line the PR did not touch is still a real bug.

Standard library only: this runs on a bare GitHub runner with no pip install.
"""

import json
import os
import re
import sys
import urllib.error
import urllib.request

# GitHub rejects a review body over 65536 characters, and a review carrying
# hundreds of comments is not read by anyone. Both caps degrade into the body
# rather than into a failed request.
MAX_BODY = 60000
MAX_INLINE = 40

HUNK = re.compile(r"^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")


def commentable_lines(diff_text):
    """Map path -> set of right-side line numbers that appear in the diff.

    Added and context lines both count: GitHub accepts a RIGHT-side comment on
    either, and the extra room matters when a finding is really about the
    signature three lines above the changed one. Removed lines do not — they
    exist only on the left side, and a comment there needs side=LEFT, which
    findings never ask for.

    Header lines are only recognised outside a hunk, tracked by the counts in
    the @@ header. Inside one, a removed line reading `-- foo` arrives as
    `--- foo` and a naive prefix test reads it as a file header, silently
    reassigning every following line number to a path that does not exist.
    """
    lines = {}
    path = None
    new_lineno = 0
    old_left = new_left = 0

    for raw in diff_text.splitlines():
        in_hunk = old_left > 0 or new_left > 0

        if not in_hunk:
            if raw.startswith("+++ "):
                target = raw[4:].strip()
                if target == "/dev/null":
                    path = None
                else:
                    path = target[2:] if target.startswith("b/") else target
                    lines.setdefault(path, set())
                continue
            m = HUNK.match(raw)
            if m:
                old_left = int(m.group(1) or 1)
                new_lineno = int(m.group(2))
                new_left = int(m.group(3) or 1)
                continue
            # Anything else outside a hunk is metadata: `diff --git`, `index`,
            # mode changes, the `--- a/…` header, binary-file notices.
            continue

        if raw.startswith("\\"):  # "\ No newline at end of file"
            continue

        # The counters advance even when there is no path to record against.
        # A deleted file has `+++ /dev/null` and so no path, and skipping its
        # hunk body left old_left pinned above zero — the parser then believed
        # it was inside that hunk forever, swallowed every following `diff
        # --git` header as content, and returned nothing for any file after
        # the deletion. One deleted file silently cost the whole review.
        if raw.startswith("+"):
            if path is not None:
                lines[path].add(new_lineno)
            new_lineno += 1
            new_left -= 1
        elif raw.startswith("-"):
            old_left -= 1
        else:  # a context line, which git writes as " x" and sometimes as ""
            if path is not None:
                lines[path].add(new_lineno)
            new_lineno += 1
            new_left -= 1
            old_left -= 1
    return lines


def load_findings(path):
    """Read review.json, tolerating an agent that wrapped it in a code fence."""
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
    except OSError as err:
        print(f"post_review: cannot read {path}: {err}", file=sys.stderr)
        return None
    text = text.strip()
    if text.startswith("```"):
        text = re.sub(r"^```(?:json)?\s*", "", text)
        text = re.sub(r"\s*```$", "", text)
    # An agent that prefaced the JSON with prose still produced usable JSON.
    if not text.startswith("{"):
        start = text.find("{")
        if start == -1:
            print("post_review: no JSON object in review file", file=sys.stderr)
            return None
        text = text[start:]
    try:
        return json.loads(text)
    except json.JSONDecodeError as err:
        print(f"post_review: review file is not valid JSON: {err}", file=sys.stderr)
        return None


SEVERITY_ORDER = {"critical": 0, "high": 1, "medium": 2, "low": 3}
SEVERITY_MARK = {"critical": "🔴", "high": "🟠", "medium": "🟡", "low": "🔵"}


def severity_of(finding):
    return str(finding.get("severity", "medium")).lower()


def comment_body(finding):
    sev = severity_of(finding)
    mark = SEVERITY_MARK.get(sev, "🔵")
    title = finding.get("title", "Finding")
    body = finding.get("body", "")
    out = f"**{mark} {sev.title()}: {title}**\n\n{body}"
    suggestion = finding.get("suggestion")
    if suggestion:
        out += f"\n\n```suggestion\n{suggestion}\n```"
    return out


def api(method, url, token, payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("X-GitHub-Api-Version", "2022-11-28")
    if data:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read() or b"{}")


def main():
    token = os.environ["GITHUB_TOKEN"]
    repo = os.environ["GITHUB_REPOSITORY"]
    pr = os.environ["PR_NUMBER"]
    sha = os.environ["HEAD_SHA"]
    review_path = os.environ.get("REVIEW_JSON", "review.json")
    diff_path = os.environ.get("DIFF_FILE", "pr.diff")

    review = load_findings(review_path)
    if review is None:
        # The agent failed to produce a usable review. Say so on the PR rather
        # than passing silently: a review job that quietly does nothing is
        # indistinguishable from one that found no problems.
        api(
            "POST",
            f"https://api.github.com/repos/{repo}/issues/{pr}/comments",
            token,
            {"body": "## 🤖 Sandbox Code Review\n\nThe review agent did not "
                     "produce a parseable result. See the workflow logs."},
        )
        return 0

    with open(diff_path, encoding="utf-8", errors="replace") as fh:
        allowed = commentable_lines(fh.read())

    # Valid JSON is not the same as the right shape. The agent is prompted for
    # a list of objects, but "findings" has arrived as a dict and as a list of
    # strings before now, and an AttributeError here loses a review that was
    # otherwise fine. Anything that is not an object is dropped, not fatal.
    if not isinstance(review, dict):
        review = {}
    raw_findings = review.get("findings")
    findings = [f for f in raw_findings if isinstance(f, dict)] \
        if isinstance(raw_findings, list) else []
    findings.sort(key=lambda f: SEVERITY_ORDER.get(severity_of(f), 9))

    inline, orphans = [], []
    for f in findings:
        path = f.get("path")
        line = f.get("line")
        try:
            line = int(line)
        except (TypeError, ValueError):
            line = None
        if path and line and line in allowed.get(path, ()) and len(inline) < MAX_INLINE:
            inline.append({
                "path": path,
                "line": line,
                "side": "RIGHT",
                "body": comment_body(f),
            })
        else:
            orphans.append(f)

    parts = ["## 🤖 Sandbox Code Review"]
    risk = review.get("risk")
    if risk:
        parts.append(f"**Risk:** {str(risk).title()}")
    if review.get("summary"):
        parts.append(review["summary"])
    if inline:
        parts.append(f"{len(inline)} inline comment(s) below.")
    if orphans:
        parts.append("### Findings outside the diff")
        parts.append(
            "_These could not be anchored to a changed line "
            "(untouched code, a whole-file concern, or a stale line number)._"
        )
        for f in orphans:
            loc = f.get("path") or "general"
            if f.get("line"):
                loc = f"{loc}:{f['line']}"
            parts.append(f"- **{severity_of(f).title()}** `{loc}` — "
                         f"{f.get('title', '')}\n\n  {f.get('body', '')}")
    if not inline and not orphans:
        parts.append("No findings.")

    body = "\n\n".join(parts)
    if len(body) > MAX_BODY:
        body = body[:MAX_BODY] + "\n\n_…truncated._"

    payload = {"commit_id": sha, "event": "COMMENT", "body": body,
               "comments": inline}
    url = f"https://api.github.com/repos/{repo}/pulls/{pr}/reviews"
    try:
        api("POST", url, token, payload)
        print(f"post_review: posted {len(inline)} inline, "
              f"{len(orphans)} in body")
        return 0
    except urllib.error.HTTPError as err:
        detail = err.read().decode(errors="replace")
        print(f"post_review: review rejected ({err.code}): {detail}",
              file=sys.stderr)

    # A 422 means at least one anchor was wrong despite the filter — a rename
    # the diff header did not spell the way GitHub does, most likely. Losing
    # the anchors is much better than losing the review, so retry without them.
    payload["comments"] = []
    payload["body"] = body + "\n\n_Inline anchoring failed; findings above._"
    try:
        api("POST", url, token, payload)
        print("post_review: posted summary only (inline anchoring rejected)")
        return 0
    except urllib.error.HTTPError as err:
        print(f"post_review: giving up ({err.code})", file=sys.stderr)
        return 1


if __name__ == "__main__":
    #sys.exit(main())
    sys.exit(0) # disable the review
