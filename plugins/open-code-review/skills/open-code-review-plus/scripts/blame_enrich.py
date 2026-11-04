#!/usr/bin/env python3

# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 alibaba/open-code-review Contributors


"""Append the introducing author to each comment in an `ocr review` JSON result.

For every located comment, blames its line range at the reviewed head, takes
the most recent commit touching those lines, and appends
"Introduced by: @<username> (<sha>)". The username comes from a GitLab noreply
address, otherwise from the local part of the author email: non-admin tokens
cannot look up private emails, and corporate addresses usually share their
local part with the username. A name that is no GitLab user renders as plain text.

Never fails the pipeline: a comment that cannot be blamed is left as is, and
an unreadable result file is left untouched for post_review.py to report.
"""

import argparse
import json
import re
import subprocess
import sys
import urllib.parse
from typing import Callable, Dict, List, Optional

NOREPLY_RE = re.compile(r"^\d+-([^@]+)@users\.noreply\.(.+)$", re.IGNORECASE)
HEADER_RE = re.compile(r"^([0-9a-f]{40}) \d+ \d+(?: \d+)?$")


def parse_blame_porcelain(text: str) -> List[dict]:
    commits: Dict[str, dict] = {}
    current = None
    for line in text.splitlines():
        m = HEADER_RE.match(line)
        if m:
            current = commits.setdefault(m.group(1), {"sha": m.group(1), "author": "",
                                                      "author_mail": "", "committer_time": 0})
            continue
        if current is None or line.startswith("\t"):
            continue
        key, _, value = line.partition(" ")
        if key == "author":
            current["author"] = value
        elif key == "author-mail":
            current["author_mail"] = value.strip("<>")
        elif key == "committer-time":
            current["committer_time"] = int(value)
    return list(commits.values())


def newest(commits: List[dict]) -> Optional[dict]:
    return max(commits, key=lambda c: c["committer_time"], default=None)


def username_from_noreply(email: str, host: str) -> Optional[str]:
    m = NOREPLY_RE.match(email)
    return m.group(1) if m and m.group(2).lower() == host.lower() else None


def username_for(email: str, host: str) -> Optional[str]:
    return username_from_noreply(email, host) or email.partition("@")[0] or None


def introducer_line(commit: dict, username: Optional[str]) -> str:
    who = "@" + username if username else commit["author"]
    return "Introduced by: %s (%s)" % (who, commit["sha"][:8])


def git_blame(head: str, path: str, start: int, end: int) -> Optional[str]:
    proc = subprocess.run(["git", "blame", "--porcelain", "-L", "%d,%d" % (start, end), head, "--", path],
                          capture_output=True, text=True, check=False)
    return proc.stdout if proc.returncode == 0 else None


def enrich(result: dict, head: str, blame: Callable[[str, str, int, int], Optional[str]],
           lookup: Callable[[str], Optional[str]]) -> int:
    enriched = 0
    for c in result.get("comments") or []:
        start = int(c.get("start_line") or 0)
        end = int(c.get("end_line") or 0) or start
        start, end = min(start, end), max(start, end)
        path = c.get("path") or ""
        if start <= 0 or not path:
            continue
        out = blame(head, path, start, end)
        commit = newest(parse_blame_porcelain(out)) if out else None
        if commit is None:
            print("warning: %s:%d-%d: git blame failed, introducer skipped" % (path, start, end), file=sys.stderr)
            continue
        c["content"] = (c.get("content") or "") + "\n\n" + introducer_line(commit, lookup(commit["author_mail"]))
        enriched += 1
    return enriched


def parse_args(argv):
    p = argparse.ArgumentParser(description="Add the introducing author to ocr review comments.")
    p.add_argument("input", help="ocr review --format json result, rewritten in place")
    p.add_argument("--head", required=True, help="reviewed head commit (diff_refs.head_sha)")
    p.add_argument("--server-url", required=True, help="GitLab base URL, used to recognise its noreply addresses")
    return p.parse_args(argv)


def main(argv=None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        with open(args.input, encoding="utf-8") as f:
            result = json.load(f)
    except (OSError, ValueError) as e:
        print("warning: cannot read %s (%s); leaving it unchanged" % (args.input, e), file=sys.stderr)
        return 0
    if not isinstance(result, dict) or not isinstance(result.get("comments"), list):
        print("warning: %s has no comments list; leaving it unchanged" % args.input, file=sys.stderr)
        return 0

    host = urllib.parse.urlsplit(args.server_url).hostname or ""
    n = enrich(result, args.head, blame=git_blame, lookup=lambda email: username_for(email, host))
    with open(args.input, "w", encoding="utf-8") as f:
        json.dump(result, f, ensure_ascii=False, indent=2)
    print("Introducer added to %d/%d comment(s)" % (n, len(result["comments"])), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
