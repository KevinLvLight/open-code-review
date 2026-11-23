#!/usr/bin/env python3

# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 alibaba/open-code-review Contributors


"""Tests for mr_context.py. Standard-library unittest only, no network:

    python3 -m unittest discover -s skills/open-code-review-plus/scripts -p '*_test.py'
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

import mr_context as mc  # noqa: E402
from mr_context import MRRef  # noqa: E402

MR_JSON = {
    "id": 900,
    "project_id": 42,
    "iid": 12,
    "title": "Add cache",
    "description": "Speeds up lookups",
    "target_branch": "main",
    "web_url": "https://gitlab.com/grp/proj/-/merge_requests/12",
    "diff_refs": {"base_sha": "b" * 40, "start_sha": "s" * 40, "head_sha": "h" * 40},
}


class FakeResponse(io.BytesIO):
    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class RecordingOpener:
    def __init__(self, payload=None, error=None):
        self.payload = payload
        self.error = error
        self.requests = []

    def __call__(self, req, timeout=None):
        self.requests.append(req)
        if self.error is not None:
            raise self.error
        return FakeResponse(json.dumps(self.payload).encode())


def http_error(status):
    return urllib.error.HTTPError("https://x", status, "err", {}, io.BytesIO(b""))


class ParseMRURLTest(unittest.TestCase):
    def test_parse_gitlab_com(self):
        r = mc.parse_mr_url("https://gitlab.com/grp/proj/-/merge_requests/12")
        self.assertEqual(r, MRRef("https://gitlab.com", "grp/proj", 12))

    def test_parse_nested_groups(self):
        r = mc.parse_mr_url("https://git.corp.com/a/b/c/proj/-/merge_requests/7")
        self.assertEqual((r.server_url, r.project_path, r.iid), ("https://git.corp.com", "a/b/c/proj", 7))

    def test_parse_trailing_parts(self):
        for u in [
            "https://h.com/g/p/-/merge_requests/3/diffs",
            "https://h.com/g/p/-/merge_requests/3?tab=overview",
            "https://h.com/g/p/-/merge_requests/3#note_99",
        ]:
            self.assertEqual(mc.parse_mr_url(u), MRRef("https://h.com", "g/p", 3), u)

    def test_parse_server_url_override_with_subpath(self):
        r = mc.parse_mr_url("https://h.com/gitlab/g/p/-/merge_requests/5", server_url="https://h.com/gitlab")
        self.assertEqual(r, MRRef("https://h.com/gitlab", "g/p", 5))

    def test_parse_invalid_raises(self):
        for u in [
            "https://h.com/g/p/-/issues/1",
            "not a url",
            "https://h.com/g/p/-/merge_requests/abc",
            "https://h.com/-/merge_requests/1",
        ]:
            with self.assertRaises(ValueError, msg=u):
                mc.parse_mr_url(u)


class ResolveTokenTest(unittest.TestCase):
    def test_token_order(self):
        no_glab = lambda _: None  # noqa: E731
        self.assertEqual(mc.resolve_token("h.com", {"GITLAB_TOKEN": "a", "GITLAB_API_TOKEN": "b"}, which=no_glab), "a")
        self.assertEqual(mc.resolve_token("h.com", {"GITLAB_API_TOKEN": "b"}, which=no_glab), "b")

    def test_glab_host_token_wins_over_env(self):
        run = mock.Mock(return_value=subprocess.CompletedProcess([], 0, stdout="corp-tok\n", stderr=""))
        tok = mc.resolve_token("gitlab.corp", {"GITLAB_TOKEN": "com-tok"}, which=lambda _: "/bin/glab", run=run)
        self.assertEqual(tok, "corp-tok")

    def test_env_token_falls_back_when_glab_has_none(self):
        run = mock.Mock(return_value=subprocess.CompletedProcess([], 0, stdout="\n", stderr=""))
        self.assertEqual(mc.resolve_token("h.com", {"GITLAB_TOKEN": "a"}, which=lambda _: "/bin/glab", run=run), "a")

    def test_env_token_not_sent_to_other_gitlab_host(self):
        env = {"GITLAB_TOKEN": "a", "GITLAB_HOST": "https://gitlab.com"}
        self.assertEqual(mc.resolve_token("gitlab.corp", env, which=lambda _: None), "")
        self.assertEqual(mc.resolve_token("gitlab.com", env, which=lambda _: None), "a")

    def test_token_from_glab(self):
        run = mock.Mock(return_value=subprocess.CompletedProcess([], 0, stdout="glab-tok\n", stderr=""))
        tok = mc.resolve_token("h.com", {}, which=lambda _: "/usr/bin/glab", run=run)
        self.assertEqual(tok, "glab-tok")
        self.assertEqual(run.call_args[0][0], ["glab", "config", "get", "token", "--host", "h.com"])

    def test_glab_failure_yields_empty(self):
        run = mock.Mock(return_value=subprocess.CompletedProcess([], 1, stdout="", stderr="no"))
        self.assertEqual(mc.resolve_token("h.com", {}, which=lambda _: "/usr/bin/glab", run=run), "")

    def test_token_missing(self):
        self.assertEqual(mc.resolve_token("h.com", {}, which=lambda _: None), "")


class FetchMRTest(unittest.TestCase):
    def test_fetch_mr_shapes_output(self):
        opener = RecordingOpener(MR_JSON)
        out = mc.fetch_mr(MRRef("https://gitlab.com", "grp/proj", 12), "tok", opener=opener)
        req = opener.requests[0]
        self.assertEqual(req.full_url, "https://gitlab.com/api/v4/projects/grp%2Fproj/merge_requests/12")
        self.assertEqual(req.get_header("Private-token"), "tok")
        self.assertEqual(set(out), {"server_url", "project_path", "project_id", "iid", "title",
                                    "description", "target_branch", "web_url", "diff_refs"})
        self.assertEqual(out["project_id"], 42)
        self.assertEqual(out["diff_refs"]["head_sha"], "h" * 40)

    def test_missing_diff_refs_raises_gitlab_error(self):
        for refs in (None, {"base_sha": None, "start_sha": None, "head_sha": None}):
            payload = dict(MR_JSON, diff_refs=refs)
            with self.assertRaises(mc.GitLabError):
                mc.fetch_mr(MRRef("https://gitlab.com", "grp/proj", 12), "tok", opener=RecordingOpener(payload))

    def test_null_description_becomes_empty(self):
        payload = dict(MR_JSON, description=None)
        out = mc.fetch_mr(MRRef("https://gitlab.com", "grp/proj", 12), "tok", opener=RecordingOpener(payload))
        self.assertEqual(out["description"], "")

    def test_fetch_mr_partial_payload_raises_gitlab_error(self):
        payload = {k: v for k, v in MR_JSON.items() if k != "target_branch"}
        with self.assertRaises(mc.GitLabError):
            mc.fetch_mr(MRRef("https://gitlab.com", "grp/proj", 12), "tok", opener=RecordingOpener(payload))
        with self.assertRaises(mc.GitLabError):
            mc.fetch_mr(MRRef("https://gitlab.com", "grp/proj", 12), "tok", opener=RecordingOpener(["not", "a", "dict"]))

    def test_api_get_404_raises(self):
        with self.assertRaises(mc.GitLabError) as cm:
            mc.api_get("https://h.com", "/projects/1", "tok", opener=RecordingOpener(error=http_error(404)))
        self.assertEqual(cm.exception.status, 404)

    def test_api_get_network_error_status_zero(self):
        err = urllib.error.URLError("refused")
        with self.assertRaises(mc.GitLabError) as cm:
            mc.api_get("https://h.com", "/projects/1", "tok", opener=RecordingOpener(error=err))
        self.assertEqual(cm.exception.status, 0)


class BackgroundTextTest(unittest.TestCase):
    def test_background_file_truncated_and_verbatim(self):
        t = mc.background_text("Fix `x` $(rm)", "d" * 5000)
        self.assertTrue(t.startswith("Fix `x` $(rm)\n\n"))
        self.assertLessEqual(len(t), 2000)

    def test_background_without_description(self):
        self.assertEqual(mc.background_text("Title", ""), "Title")


class MainTest(unittest.TestCase):
    def run_main(self, argv, env=None, opener=None):
        err = io.StringIO()
        out = io.StringIO()
        with mock.patch.dict(os.environ, env or {}, clear=True), \
                mock.patch.object(mc.shutil, "which", return_value=None), \
                mock.patch.object(mc.urllib.request, "urlopen", opener or RecordingOpener(MR_JSON)), \
                mock.patch("sys.stdout", out), mock.patch("sys.stderr", err):
            code = mc.main(argv)
        return code, out.getvalue(), err.getvalue()

    def test_bad_url_exits_2(self):
        code, _, err = self.run_main(["https://h.com/g/p/-/issues/1"], {"GITLAB_TOKEN": "t"})
        self.assertEqual(code, 2)
        self.assertIn("not a GitLab merge request URL", err)

    def test_missing_token_exits_3(self):
        code, _, err = self.run_main(["https://h.com/g/p/-/merge_requests/1"])
        self.assertEqual(code, 3)
        self.assertIn("glab auth login --hostname h.com", err)

    def test_api_error_exits_4(self):
        code, _, err = self.run_main(["https://h.com/g/p/-/merge_requests/1"], {"GITLAB_TOKEN": "t"},
                                     RecordingOpener(error=http_error(403)))
        self.assertEqual(code, 4)
        self.assertIn("api scope", err)

    def test_success_prints_json_without_token(self):
        code, out, _ = self.run_main(["https://gitlab.com/grp/proj/-/merge_requests/12"], {"GITLAB_TOKEN": "sekret"})
        self.assertEqual(code, 0)
        printed = json.loads(out)
        self.assertEqual(printed["iid"], 12)
        self.assertNotIn("sekret", out)
        self.assertNotIn("description", printed)
        self.assertNotIn("title", printed)
        self.assertNotIn("Speeds up lookups", out)

    def test_print_token(self):
        code, out, _ = self.run_main(["https://h.com/g/p/-/merge_requests/1", "--print-token"], {"GITLAB_TOKEN": "t"})
        self.assertEqual((code, out), (0, "t\n"))

    def test_background_out_written(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "sub", "bg.md")
            code, _, _ = self.run_main(["https://gitlab.com/grp/proj/-/merge_requests/12", "--background-out", path],
                                       {"GITLAB_TOKEN": "t"})
            self.assertEqual(code, 0)
            with open(path) as f:
                self.assertEqual(f.read(), "Add cache\n\nSpeeds up lookups")


if __name__ == "__main__":
    unittest.main()
