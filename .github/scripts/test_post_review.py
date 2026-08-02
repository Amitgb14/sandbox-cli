#!/usr/bin/env python3
"""Tests for the diff parser and the anchoring split.

Run with: python3 .github/scripts/test_post_review.py

commentable_lines decides which findings GitHub will accept, and it fails
quietly rather than loudly: a parse bug does not raise, it just returns fewer
lines, and the review comes back with everything moved into the body as if the
agent had chosen bad anchors. Two of the cases below are regressions that
reached CI.
"""

import unittest

from post_review import commentable_lines, load_findings, severity_of


class TestCommentableLines(unittest.TestCase):
    def test_added_and_context_lines_on_the_right_side(self):
        diff = (
            "diff --git a/f.txt b/f.txt\n"
            "--- a/f.txt\n"
            "+++ b/f.txt\n"
            "@@ -1,3 +1,4 @@\n"
            " a\n"
            "-b\n"
            "+B\n"
            "+NEW\n"
            " c\n"
        )
        # 1=' a', 2='+B', 3='+NEW', 4=' c'. The removed 'b' has no right side.
        self.assertEqual(commentable_lines(diff), {"f.txt": {1, 2, 3, 4}})

    def test_deleted_file_does_not_swallow_later_files(self):
        """Regression: the counters have to advance with no path to record to.

        `+++ /dev/null` leaves path None. Skipping the hunk body left old_left
        above zero, so the parser thought it was still inside that hunk, read
        the next `diff --git` as content, and returned {} for the whole PR.
        """
        diff = (
            "diff --git a/gone.txt b/gone.txt\n"
            "deleted file mode 100644\n"
            "--- a/gone.txt\n"
            "+++ /dev/null\n"
            "@@ -1,2 +0,0 @@\n"
            "-k1\n"
            "-k2\n"
            "diff --git a/after.txt b/after.txt\n"
            "--- a/after.txt\n"
            "+++ b/after.txt\n"
            "@@ -1,2 +1,2 @@\n"
            " z1\n"
            "-z2\n"
            "+z2-CHANGED\n"
        )
        self.assertEqual(commentable_lines(diff), {"after.txt": {1, 2}})

    def test_removed_line_that_looks_like_a_file_header(self):
        """Regression: `-- foo` is emitted as `--- foo` inside a hunk.

        Treating it as a header reassigned every later line number to a path
        that does not exist. Recognising headers only outside a hunk is what
        makes a patch file inside a diff parse correctly.
        """
        diff = (
            "diff --git a/f.txt b/f.txt\n"
            "--- a/f.txt\n"
            "+++ b/f.txt\n"
            "@@ -1,3 +1,2 @@\n"
            " x\n"
            "--- looks like a header\n"
            " y\n"
        )
        self.assertEqual(commentable_lines(diff), {"f.txt": {1, 2}})

    def test_hunk_header_without_counts(self):
        """`@@ -1 +1 @@` means one line on each side; the counts are optional."""
        diff = (
            "diff --git a/f.txt b/f.txt\n"
            "--- a/f.txt\n"
            "+++ b/f.txt\n"
            "@@ -1 +1 @@\n"
            "-old\n"
            "+new\n"
            "diff --git a/g.txt b/g.txt\n"
            "--- a/g.txt\n"
            "+++ b/g.txt\n"
            "@@ -1 +1 @@\n"
            "-old\n"
            "+new\n"
        )
        self.assertEqual(commentable_lines(diff), {"f.txt": {1}, "g.txt": {1}})

    def test_new_file(self):
        diff = (
            "diff --git a/n.txt b/n.txt\n"
            "new file mode 100644\n"
            "--- /dev/null\n"
            "+++ b/n.txt\n"
            "@@ -0,0 +1,3 @@\n"
            "+one\n"
            "+two\n"
            "+three\n"
        )
        self.assertEqual(commentable_lines(diff), {"n.txt": {1, 2, 3}})

    def test_second_hunk_starts_at_its_own_offset(self):
        diff = (
            "diff --git a/f.txt b/f.txt\n"
            "--- a/f.txt\n"
            "+++ b/f.txt\n"
            "@@ -1,1 +1,1 @@\n"
            "+first\n"
            "-gone\n"
            "@@ -20,2 +20,3 @@\n"
            " ctx\n"
            "+added\n"
            " ctx2\n"
        )
        self.assertEqual(commentable_lines(diff), {"f.txt": {1, 20, 21, 22}})

    def test_empty_diff(self):
        self.assertEqual(commentable_lines(""), {})


class TestLoadFindings(unittest.TestCase):
    def _write(self, text):
        import tempfile
        fh = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
        fh.write(text)
        fh.close()
        return fh.name

    def test_plain_json(self):
        p = self._write('{"risk":"low","findings":[]}')
        self.assertEqual(load_findings(p)["risk"], "low")

    def test_code_fenced(self):
        p = self._write('```json\n{"risk":"high","findings":[]}\n```')
        self.assertEqual(load_findings(p)["risk"], "high")

    def test_prose_before_the_object(self):
        p = self._write('Here is the review:\n{"risk":"low","findings":[]}')
        self.assertEqual(load_findings(p)["risk"], "low")

    def test_not_json_at_all(self):
        self.assertIsNone(load_findings(self._write("no json here")))

    def test_missing_file(self):
        self.assertIsNone(load_findings("/nonexistent/review.json"))


class TestSeverity(unittest.TestCase):
    def test_defaults_to_medium_and_lowercases(self):
        self.assertEqual(severity_of({}), "medium")
        self.assertEqual(severity_of({"severity": "CRITICAL"}), "critical")


if __name__ == "__main__":
    unittest.main(verbosity=2)
