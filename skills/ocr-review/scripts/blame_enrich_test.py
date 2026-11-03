#!/usr/bin/env python3

# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 alibaba/open-code-review Contributors


"""Tests for blame_enrich.py. Standard-library unittest only, no network:

    python3 -m unittest discover -s skills/ocr-review/scripts -p '*_test.py'
"""

import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
import urllib.error
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import blame_enrich as be  # noqa: E402

PORCELAIN = (
    "aaaaaaaa11111111aaaaaaaa11111111aaaaaaaa 3 3 1\n"
    "author Alice\nauthor-mail <alice@x.com>\ncommitter-time 100\n\tline\n"
    "bbbbbbbb22222222bbbbbbbb22222222bbbbbbbb 4 4 1\n"
    "author Bob\nauthor-mail <bob@x.com>\ncommitter-time 200\n\tline\n"
    "aaaaaaaa11111111aaaaaaaa11111111aaaaaaaa 5 5\n\tline\n"
)


class FakeResponse(io.BytesIO):
    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class CountingOpener:
    def __init__(self, payload=None, error=None):
        self.payload = payload
        self.error = error
        self.calls = 0

    def __call__(self, req, timeout=None):
        self.calls += 1
        if self.error is not None:
            raise self.error
        return FakeResponse(json.dumps(self.payload).encode())


class PorcelainTest(unittest.TestCase):
    def test_parse_porcelain_dedupes_commits(self):
        cs = be.parse_blame_porcelain(PORCELAIN)
        self.assertEqual(len(cs), 2)
        bob = next(c for c in cs if c["author"] == "Bob")
        self.assertEqual(bob["author_mail"], "bob@x.com")
        self.assertEqual(bob["committer_time"], 200)

    def test_newest_picks_latest_committer_time(self):
        self.assertEqual(be.newest(be.parse_blame_porcelain(PORCELAIN))["author"], "Bob")

    def test_newest_empty(self):
        self.assertIsNone(be.newest([]))


class UsernameTest(unittest.TestCase):
    def test_noreply(self):
        self.assertEqual(be.username_from_noreply("123-jdoe@users.noreply.gitlab.com"), "jdoe")
        self.assertEqual(be.username_from_noreply("9-j.doe@USERS.NOREPLY.git.corp.com"), "j.doe")
        self.assertIsNone(be.username_from_noreply("jdoe@corp.com"))

    def test_lookup_requires_unique_exact_match(self):
        similar = [{"username": "bob", "public_email": "bob@x.com"},
                   {"username": "bob2", "public_email": "bob@x.com.cn"}]
        self.assertEqual(be.UserLookup("https://h", "t", opener=CountingOpener(similar)).username("Bob@X.com"), "bob")

        dup = [{"username": "bob", "public_email": "bob@x.com"}, {"username": "bob2", "email": "bob@x.com"}]
        self.assertIsNone(be.UserLookup("https://h", "t", opener=CountingOpener(dup)).username("bob@x.com"))

        none = [{"username": "carol", "public_email": "carol@x.com"}]
        self.assertIsNone(be.UserLookup("https://h", "t", opener=CountingOpener(none)).username("bob@x.com"))

    def test_lookup_caches_per_email(self):
        opener = CountingOpener([{"username": "bob", "public_email": "bob@x.com"}])
        lookup = be.UserLookup("https://h", "t", opener=opener)
        lookup.username("bob@x.com")
        lookup.username("bob@x.com")
        self.assertEqual(opener.calls, 1)

    def test_lookup_http_error_returns_none(self):
        err = urllib.error.HTTPError("https://h", 403, "forbidden", {}, io.BytesIO(b""))
        self.assertIsNone(be.UserLookup("https://h", "t", opener=CountingOpener(error=err)).username("bob@x.com"))


class EnrichTest(unittest.TestCase):
    def test_introducer_line(self):
        c = {"sha": "bbbbbbbb22222222", "author": "Bob"}
        self.assertEqual(be.introducer_line(c, "bob"), "Introduced by: @bob (bbbbbbbb)")
        self.assertEqual(be.introducer_line(c, None), "Introduced by: Bob (bbbbbbbb)")

    def test_enrich_appends_and_counts(self):
        result = {"comments": [{"path": "a.go", "start_line": 3, "end_line": 5, "content": "bug"}]}
        calls = []

        def blame(head, path, start, end):
            calls.append((head, path, start, end))
            return PORCELAIN

        n = be.enrich(result, "HEAD", blame=blame, lookup=lambda e: "bob" if e == "bob@x.com" else None)
        self.assertEqual(n, 1)
        self.assertEqual(calls, [("HEAD", "a.go", 3, 5)])
        self.assertEqual(result["comments"][0]["content"], "bug\n\nIntroduced by: @bob (bbbbbbbb)")

    def test_enrich_single_line_uses_start_when_end_missing(self):
        result = {"comments": [{"path": "a.go", "start_line": 4, "end_line": 0, "content": "x"}]}
        calls = []
        be.enrich(result, "H", blame=lambda *a: calls.append(a) or PORCELAIN, lookup=lambda e: None)
        self.assertEqual(calls, [("H", "a.go", 4, 4)])

    def test_enrich_skips_zero_lines(self):
        result = {"comments": [{"path": "a.go", "start_line": 0, "end_line": 0, "content": "x"}]}
        blame = mock.Mock()
        self.assertEqual(be.enrich(result, "H", blame=blame, lookup=lambda e: None), 0)
        blame.assert_not_called()
        self.assertEqual(result["comments"][0]["content"], "x")

    def test_enrich_skips_when_blame_fails(self):
        result = {"comments": [
            {"path": "gone.go", "start_line": 1, "end_line": 1, "content": "one"},
            {"path": "a.go", "start_line": 3, "end_line": 3, "content": "two"},
        ]}
        n = be.enrich(result, "H", blame=lambda h, p, s, e: None if p == "gone.go" else PORCELAIN,
                      lookup=lambda e: None)
        self.assertEqual(n, 1)
        self.assertEqual(result["comments"][0]["content"], "one")
        self.assertTrue(result["comments"][1]["content"].endswith("Introduced by: Bob (bbbbbbbb)"))

    def test_git_blame_returns_none_on_error(self):
        with mock.patch.object(be.subprocess, "run",
                               return_value=subprocess.CompletedProcess([], 128, stdout="", stderr="fatal")):
            self.assertIsNone(be.git_blame("H", "a.go", 1, 2))


class MainTest(unittest.TestCase):
    def test_main_leaves_broken_json_untouched(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "r.json")
            with open(path, "w") as f:
                f.write("{not json")
            with mock.patch("sys.stderr", io.StringIO()):
                code = be.main([path, "--head", "H", "--server-url", "https://h.com"])
            self.assertEqual(code, 0)
            with open(path) as f:
                self.assertEqual(f.read(), "{not json")

    def test_main_rewrites_file(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "r.json")
            with open(path, "w") as f:
                json.dump({"comments": [{"path": "a.go", "start_line": 3, "end_line": 3, "content": "c"}]}, f)
            with mock.patch.object(be, "git_blame", return_value=PORCELAIN), \
                    mock.patch.object(be, "resolve_token", return_value=""), \
                    mock.patch("sys.stderr", io.StringIO()) as err:
                code = be.main([path, "--head", "H", "--server-url", "https://h.com"])
            self.assertEqual(code, 0)
            with open(path) as f:
                self.assertTrue(json.load(f)["comments"][0]["content"].endswith("Introduced by: Bob (bbbbbbbb)"))
            self.assertIn("Introducer added to 1/1 comment(s)", err.getvalue())


if __name__ == "__main__":
    unittest.main()
