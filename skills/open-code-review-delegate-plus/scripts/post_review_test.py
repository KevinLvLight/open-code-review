#!/usr/bin/env python3

# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 alibaba/open-code-review Contributors

"""Tests for the open-code-review-plus changes to post_review.py: the summary note is
posted only when something could not go inline. The rest of the script is
covered by examples/gitlab_ci/post_review_test.py."""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import post_review as pr  # noqa: E402

CONFIG = {"success_delay": 0, "failure_delay": 0, "rate_limit_threshold": 0, "run_tag": "1-1"}
DIFF_REFS = {"base_sha": "b", "start_sha": "s", "head_sha": "h"}


class Poster:
    def __init__(self, disc_ok=True, diffs=None):
        self.disc_ok = disc_ok
        self.diffs = diffs
        self.notes = []
        self.updates = []
        self.discussions = []

    def post_note(self, body):
        self.notes.append(body)
        return {"success": True, "url": "https://gitlab.example/note/1", "id": 1}

    def update_note(self, note_id, body):
        self.updates.append(body)
        return {"success": True, "url": "https://gitlab.example/note/%s" % note_id}

    def list_notes(self):
        return []

    def list_discussions(self):
        return []

    def post_discussion(self, discussion, comment_id=None):
        self.discussions.append(discussion)
        if self.disc_ok:
            return {"success": True}
        return {"success": False, "http_status": 500, "failed_reason": "server error"}

    def get_mr_diffs(self):
        return self.diffs or {"files": {}, "known": set(), "complete": False}

    def mr_url(self):
        return "https://gitlab.example/mr/1"


def comment(line=3, content="bug here"):
    return {"path": "a.go", "start_line": line, "end_line": line, "content": content,
            "severity": "low", "category": "maintainability"}


class SummaryOnlyOnFallbackTest(unittest.TestCase):
    def test_all_inline_posts_no_summary(self):
        poster = Poster()
        stats = pr.publish({"comments": [comment(), comment(9)]}, DIFF_REFS, poster, dict(CONFIG))
        self.assertEqual(stats["inline"], 2)
        self.assertEqual(poster.notes + poster.updates, [])
        self.assertEqual(stats["summary_url"], "https://gitlab.example/mr/1")

    def test_unlocated_comment_gets_summary(self):
        poster = Poster()
        pr.publish({"comments": [comment(), comment(0, "no line")]}, DIFF_REFS, poster, dict(CONFIG))
        self.assertIn("no line", (poster.updates or poster.notes)[-1])

    def test_failed_inline_gets_summary(self):
        poster = Poster(disc_ok=False)
        stats = pr.publish({"comments": [comment()]}, DIFF_REFS, poster, dict(CONFIG))
        self.assertEqual(stats["failed"], 1)
        self.assertIn("bug here", (poster.updates or poster.notes)[-1])

    def test_warnings_get_summary(self):
        poster = Poster()
        pr.publish({"comments": [comment()], "warnings": ["x.go: review timed out"]}, DIFF_REFS, poster, dict(CONFIG))
        self.assertIn("review timed out", (poster.updates or poster.notes)[-1])

    def test_no_findings_still_reports_clean_review(self):
        poster = Poster()
        pr.publish({"comments": []}, DIFF_REFS, poster, dict(CONFIG))
        self.assertIn("Looks good to me", poster.notes[-1])


# The hunk from a real MR: 336-338 context, 339-344 added, 345-346 context.
PATCH = "@@ -336,6 +336,12 @@\n a\n b\n c\n+d\n+e\n+f\n+g\n+h\n+i\n j\n k\n l\n"


def diff_for(path):
    return {"files": {path: [{"start": 336, "end": 347}]}, "known": {path},
            "positions": {path: pr.build_new_line_positions(PATCH)}, "complete": True}


class ContextLineAnchorTest(unittest.TestCase):
    def test_range_ending_on_context_line_sends_old_line(self):
        # GitLab rejects an unchanged-line anchor that carries new_line alone.
        poster = Poster(diffs=diff_for("a.go"))
        c = comment(341)
        c["end_line"] = 345
        pr.publish({"comments": [c]}, DIFF_REFS, poster, dict(CONFIG))
        pos = poster.discussions[0]["position"]
        self.assertEqual((pos["new_line"], pos.get("old_line")), (345, 339))
        self.assertEqual(pos["line_range"]["end"]["old_line"], 339)

    def test_added_line_anchor_has_no_old_line(self):
        poster = Poster(diffs=diff_for("a.go"))
        pr.publish({"comments": [comment(341)]}, DIFF_REFS, poster, dict(CONFIG))
        self.assertNotIn("old_line", poster.discussions[0]["position"])


if __name__ == "__main__":
    unittest.main()
