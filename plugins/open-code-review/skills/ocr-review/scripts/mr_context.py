#!/usr/bin/env python3

# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 alibaba/open-code-review Contributors


"""Resolve a GitLab merge request URL into the metadata ocr-review needs.

Prints the MR as JSON (never the token), optionally writes the review
background file, or with --print-token prints only the resolved token so the
caller can inline it into a single command without persisting it.

Standard library only, matching post_review.py.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Mapping, NamedTuple, Optional

MR_SEPARATOR = "/-/merge_requests/"
BACKGROUND_LIMIT = 2000
API_TIMEOUT = 30


class MRRef(NamedTuple):
    server_url: str
    project_path: str
    iid: int


class GitLabError(Exception):
    def __init__(self, status: int, message: str):
        super().__init__(message)
        self.status = status


def parse_mr_url(url: str, server_url: Optional[str] = None) -> MRRef:
    parts = urllib.parse.urlsplit(url)
    if parts.scheme not in ("http", "https") or not parts.netloc or MR_SEPARATOR not in parts.path:
        raise ValueError(url)
    before, after = parts.path.split(MR_SEPARATOR, 1)
    iid = after.split("/", 1)[0]
    if not iid.isdigit():
        raise ValueError(url)

    base = "%s://%s" % (parts.scheme, parts.netloc)
    if server_url:
        base = server_url.rstrip("/")
        # A GitLab served under a sub-path cannot be told apart from a group
        # by the URL alone, so the caller supplies it and we strip it here.
        prefix = urllib.parse.urlsplit(base).path.rstrip("/")
        if prefix and before.startswith(prefix + "/"):
            before = before[len(prefix):]
    project_path = before.strip("/")
    if not project_path:
        raise ValueError(url)
    return MRRef(base, project_path, int(iid))


def resolve_token(host: str, env: Mapping[str, str], which=shutil.which, run=subprocess.run) -> str:
    for name in ("GITLAB_TOKEN", "GITLAB_API_TOKEN"):
        if env.get(name):
            return env[name]
    if which("glab"):
        proc = run(["glab", "config", "get", "token", "--host", host],
                    capture_output=True, text=True, check=False)
        if proc.returncode == 0:
            return proc.stdout.strip()
    return ""


def api_get(server_url: str, path: str, token: str, opener=None) -> Any:
    opener = opener or urllib.request.urlopen
    req = urllib.request.Request(server_url.rstrip("/") + "/api/v4" + path,
                                 headers={"PRIVATE-TOKEN": token})
    try:
        with opener(req, timeout=API_TIMEOUT) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        raise GitLabError(e.code, "HTTP %d" % e.code) from e
    except (urllib.error.URLError, OSError, ValueError) as e:
        raise GitLabError(0, str(e)) from e


def fetch_mr(ref: MRRef, token: str, opener=None) -> dict:
    path = "/projects/%s/merge_requests/%d" % (urllib.parse.quote(ref.project_path, safe=""), ref.iid)
    mr = api_get(ref.server_url, path, token, opener=opener)
    return {
        "server_url": ref.server_url,
        "project_path": ref.project_path,
        "project_id": mr["project_id"],
        "iid": mr["iid"],
        "title": mr.get("title") or "",
        "description": mr.get("description") or "",
        "target_branch": mr["target_branch"],
        "web_url": mr.get("web_url") or "",
        "diff_refs": mr.get("diff_refs") or {},
    }


def background_text(title: str, description: str, limit: int = BACKGROUND_LIMIT) -> str:
    text = title if not description else title + "\n\n" + description
    return text[:limit]


def parse_args(argv):
    p = argparse.ArgumentParser(description="Resolve a GitLab merge request for ocr-review.")
    p.add_argument("url", help="merge request URL, e.g. https://gitlab.com/group/project/-/merge_requests/1")
    p.add_argument("--server-url", help="GitLab base URL when the instance is served under a sub-path")
    p.add_argument("--print-token", action="store_true", help="print only the resolved token")
    p.add_argument("--background-out", help="write the MR title and description here for --background-file")
    return p.parse_args(argv)


def main(argv=None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        ref = parse_mr_url(args.url, args.server_url)
    except ValueError:
        print("error: not a GitLab merge request URL: %s (expected "
              "https://<host>/<group>/<project>/-/merge_requests/<iid>)" % args.url, file=sys.stderr)
        return 2

    host = urllib.parse.urlsplit(ref.server_url).hostname or ""
    token = resolve_token(host, os.environ)
    if not token:
        print("error: no GitLab token. Run 'glab auth login --hostname %s' or set GITLAB_TOKEN (api scope)." % host,
              file=sys.stderr)
        return 3
    if args.print_token:
        print(token)
        return 0

    try:
        mr = fetch_mr(ref, token)
    except GitLabError as e:
        if e.status in (401, 403):
            msg = "GitLab API returned %d; the token needs api scope to post comments." % e.status
        elif e.status == 404:
            msg = "merge request not found or not visible to this token."
        else:
            msg = "GitLab API request failed: %s" % e
        print("error: " + msg, file=sys.stderr)
        return 4

    if args.background_out:
        parent = os.path.dirname(args.background_out)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(args.background_out, "w", encoding="utf-8") as f:
            f.write(background_text(mr["title"], mr["description"]))

    print(json.dumps(mr, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
