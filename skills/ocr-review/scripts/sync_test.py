#!/usr/bin/env python3

# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 alibaba/open-code-review Contributors

"""Guards the vendored post_review.py against drifting from its original."""

import unittest
from pathlib import Path

SYNC_NOTE = "# Copied verbatim from examples/gitlab_ci/post_review.py; edit the original and re-copy.\n"


class PostReviewSyncTest(unittest.TestCase):
    def test_post_review_matches_example(self):
        here = Path(__file__).resolve().parent
        original = here.parents[2] / "examples" / "gitlab_ci" / "post_review.py"
        if not original.exists():
            self.skipTest("standalone skill install; examples/ not present")
        copy = (here / "post_review.py").read_text(encoding="utf-8")
        self.assertIn(SYNC_NOTE, copy)
        self.assertEqual(copy.replace(SYNC_NOTE, "", 1), original.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
