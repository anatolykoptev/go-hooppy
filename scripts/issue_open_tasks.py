#!/usr/bin/env python3
"""Guard for issues closed while their task-list still has unchecked boxes.

A closing PR keyword (Closes #N) closes the issue even when only part of the
work is done; this guard makes that loud instead of silent: it parses the issue
body, and when unchecked task-list items remain, it posts a comment naming them
and reopens the issue via the REST API using GITHUB_TOKEN.

Kept as a standalone script so the whole behaviour (parse -> decide -> build
comment -> call) is unit-testable (scripts/test_issue_open_tasks.py) rather than
buried in YAML. The HTTP calls go through an injectable seam (Http.request) so
the test exercises the real decision path without network access.

Usage (workflow):
    ISSUE_BODY="$body" ISSUE_NUMBER=N GITHUB_TOKEN=$token \
    GITHUB_REPOSITORY="owner/repo" python3 scripts/issue_open_tasks.py
Exit codes:
    0  no unchecked task items, or action completed successfully
    2  unchecked items found but the API calls failed
"""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.request

# A markdown task-list item: optional leading whitespace (nested items count),
# a list marker (-, *, or +), whitespace, then a checkbox. We capture the
# unchecked form ([ ] with optional inner space) and the remainder of the line.
_UNCHECKED_RE = re.compile(r"^\s*[-*+]\s+\[\s\]\s+(.+?)\s*$")
# Any task-list item (checked or unchecked) — used to detect "has task items".
# [x] and [X] both count as checked.
_ANY_TASK_RE = re.compile(r"^\s*[-*+]\s+\[[ xX]\]\s+.+$")

_GITHUB_API = "https://api.github.com"


def _iter_prose_lines(body: str):
    """Yield body lines, skipping lines inside fenced code blocks.

    Fenced blocks (``` or ~~~) are code samples, not task lists: a line
    `- [ ] foo` inside a fence is not a task. Nested/indented task items
    outside fences DO count (they are real sub-tasks).
    """
    in_fence = False
    fence_marker = None
    for line in body.splitlines():
        stripped = line.lstrip()
        if stripped.startswith("```") or stripped.startswith("~~~"):
            marker = stripped[:3]
            if in_fence:
                if marker == fence_marker:
                    in_fence = False
                    fence_marker = None
            else:
                in_fence = True
                fence_marker = marker
            continue
        if in_fence:
            continue
        yield line


def parse_open_tasks(body: str) -> list[str]:
    """Return the trimmed text of every unchecked task-list item in body."""
    if not body:
        return []
    open_items: list[str] = []
    for line in _iter_prose_lines(body):
        m = _UNCHECKED_RE.match(line)
        if m:
            open_items.append(m.group(1).strip())
    return open_items


def has_task_items(body: str) -> bool:
    if not body:
        return False
    return any(_ANY_TASK_RE.match(line) for line in _iter_prose_lines(body))


def build_comment(unchecked: list[str]) -> str:
    """Build the comment body posted when an issue is reopened."""
    count = len(unchecked)
    lines = "\n".join(f"- [ ] {s}" for s in unchecked)
    return (
        f"This issue was closed but {count} task-list item"
        f"{'s' if count != 1 else ''} remain unchecked:\n\n"
        f"{lines}\n\n"
        "Reopening automatically. Close again only after these are done "
        "or no longer wanted."
    )


class Http:
    """Thin stdlib HTTP seam. Substituted in tests to record requests."""

    def request(self, method: str, url: str, headers: dict, payload: dict):
        data = json.dumps(payload).encode("utf-8")
        req = urllib.request.Request(url, data=data, method=method)
        for k, v in headers.items():
            req.add_header(k, v)
        try:
            with urllib.request.urlopen(req) as resp:
                body = resp.read().decode("utf-8")
                return resp.status, _safe_json(body)
        except urllib.error.HTTPError as e:
            return e.code, _safe_json(e.read().decode("utf-8"))


def _safe_json(text: str):
    try:
        return json.loads(text)
    except (ValueError, json.JSONDecodeError):
        return {"raw": text}


def guard_issue(
    body: str,
    issue_number: int,
    owner: str,
    repo: str,
    token: str,
    http: Http,
) -> dict:
    """Parse body; if unchecked items remain, post comment and reopen.

    Returns a summary dict: {"acted": bool, "unchecked": [...], "calls": [...]}.
    calls is the list of (method, url, payload) the seam received, so callers
    and tests can inspect exactly what was sent.
    """
    unchecked = parse_open_tasks(body)
    if not unchecked:
        return {"acted": False, "unchecked": [], "calls": []}
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    base = f"{_GITHUB_API}/repos/{owner}/{repo}/issues/{issue_number}"
    comment_body = build_comment(unchecked)
    calls = []
    status, _ = http.request("POST", f"{base}/comments", headers, {"body": comment_body})
    calls.append(("POST", f"{base}/comments", {"body": comment_body}))
    if status >= 300:
        return {"acted": True, "unchecked": unchecked, "calls": calls, "error": f"comment POST status {status}"}
    status, _ = http.request("PATCH", base, headers, {"state": "open"})
    calls.append(("PATCH", base, {"state": "open"}))
    if status >= 300:
        return {"acted": True, "unchecked": unchecked, "calls": calls, "error": f"reopen PATCH status {status}"}
    return {"acted": True, "unchecked": unchecked, "calls": calls}


def main() -> int:
    body = os.environ.get("ISSUE_BODY", "")
    if not body and not sys.stdin.isatty():
        body = sys.stdin.read()
    unchecked = parse_open_tasks(body)
    if not unchecked:
        sys.stdout.write(json.dumps({"acted": False, "unchecked": []}))
        return 0
    issue_number = os.environ.get("ISSUE_NUMBER")
    repo_full = os.environ.get("GITHUB_REPOSITORY", "")
    token = os.environ.get("GITHUB_TOKEN", "")
    if not issue_number or not repo_full or not token:
        # Not configured to act (e.g. run locally). Report what would happen.
        sys.stdout.write(json.dumps({"acted": False, "unchecked": unchecked, "reason": "missing env"}))
        return 0
    owner, _, repo = repo_full.partition("/")
    result = guard_issue(body, int(issue_number), owner, repo, token, Http())
    sys.stdout.write(json.dumps(result))
    return 2 if result.get("error") else 0


if __name__ == "__main__":
    sys.exit(main())
