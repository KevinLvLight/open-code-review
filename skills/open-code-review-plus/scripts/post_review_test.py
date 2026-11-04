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
    def __init__(self, disc_ok=True):
        self.disc_ok = disc_ok
        self.notes = []
        self.updates = []

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
        if self.disc_ok:
            return {"success": True}
        return {"success": False, "http_status": 500, "failed_reason": "server error"}

    def get_mr_diffs(self):
        return {"files": {}, "known": set(), "complete": False}

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


if __name__ == "__main__":
    unittest.main()
