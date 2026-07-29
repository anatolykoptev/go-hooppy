#!/usr/bin/env python3
"""Table-driven tests for scripts/issue_open_tasks.py.

Run: python3 scripts/test_issue_open_tasks.py
No external deps. Exits non-zero on any failure.

Covers two layers:
  - parse layer: parse_open_tasks / has_task_items over realistic bodies
  - guard layer: guard_issue end-to-end via a fake Http seam that records
    the exact request bodies, without network access.
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import issue_open_tasks as iot  # noqa: E402


# (name, body, expected_unchecked, expected_has_tasks)
PARSE_CASES = [
    ("empty body", "", [], False),
    ("no task items", "Just prose, no checkboxes.\nMore prose.", [], False),
    # [x] and [X] both count as checked -> no unchecked items.
    ("all checked", "- [x] part 1\n- [X] part 2\n- [x] part 3", [], True),
    ("all unchecked", "- [ ] part 1\n- [ ] part 2\n- [ ] part 3",
     ["part 1", "part 2", "part 3"], True),
    ("mixed", "- [x] part 1 done\n- [ ] part 2 open\n- [ ] part 3 open",
     ["part 2 open", "part 3 open"], True),
    # Nested/indented items DO count (they are real sub-tasks).
    ("nested unchecked", "- [ ] top\n  - [ ] nested\n  - [x] nested done",
     ["top", "nested"], True),
    ("asterisk marker", "* [ ] asterisk task", ["asterisk task"], True),
    ("plus marker", "+ [ ] plus task", ["plus task"], True),
    ("checkbox not a list item", "Sentence with [ ] inline box.", [], False),
    ("loose bracket not task", "- [ ]no space after brackets", [], False),
    ("indented non-task bullet", "  - regular bullet", [], False),
    ("trailing whitespace unchecked", "- [ ] task   ", ["task"], True),
    ("empty checkbox text", "- [ ] ", [], False),
    # Unchecked items inside fenced code blocks must NOT count.
    ("fenced code block unchecked ignored",
     "```\n- [ ] inside code, not a task\n```\n- [ ] real",
     ["real"], True),
    ("tilde fence block unchecked ignored",
     "~~~\n- [ ] inside tilde code\n~~~\n- [ ] outside",
     ["outside"], True),
    ("fence with language hint",
     "```python\n- [ ] code sample\n```\n- [ ] prose task",
     ["prose task"], True),
    ("issue #69 shape", "## Goal\n\n- [x] part 1: scrape\n- [ ] part 2: import\n- [ ] part 3: schedule\n",
     ["part 2: import", "part 3: schedule"], True),
]


class FakeHttp:
    """Records every request; returns canned 200 success."""

    def __init__(self, status=200):
        self.calls = []  # (method, url, payload)
        self.status = status

    def request(self, method, url, headers, payload):
        self.calls.append((method, url, payload))
        return self.status, {}


# (name, body, expected_acted, expected_call_methods, expected_unchecked_in_comment)
GUARD_CASES = [
    ("unchecked items -> act",
     "- [x] done\n- [ ] part 2: import\n- [ ] part 3: schedule",
     True, ["POST", "PATCH"], ["part 2: import", "part 3: schedule"]),
    ("single unchecked -> act, singular wording",
     "- [ ] only one", True, ["POST", "PATCH"], ["only one"]),
    # Negative: all checked -> no calls at all.
    ("all checked -> no action",
     "- [x] a\n- [X] b", False, [], []),
    # Negative: no task list -> no calls at all.
    ("no task list -> no action",
     "Just prose, no checkboxes.", False, [], []),
    # Negative: unchecked only inside a code fence -> no calls.
    ("fenced unchecked only -> no action",
     "```\n- [ ] code sample\n```", False, [], []),
]


def run_parse_tests() -> int:
    failures = 0
    for name, body, exp_unchecked, exp_has in PARSE_CASES:
        got_unchecked = iot.parse_open_tasks(body)
        got_has = iot.has_task_items(body)
        if got_unchecked != exp_unchecked:
            print(f"FAIL parse {name}: parse_open_tasks -> {got_unchecked!r}, want {exp_unchecked!r}")
            failures += 1
        if got_has != exp_has:
            print(f"FAIL parse {name}: has_task_items -> {got_has!r}, want {exp_has!r}")
            failures += 1
    return failures


def run_guard_tests() -> int:
    failures = 0
    for name, body, exp_acted, exp_methods, exp_unchecked in GUARD_CASES:
        http = FakeHttp()
        result = iot.guard_issue(body, 42, "owner", "repo", "tok", http)
        if result["acted"] != exp_acted:
            print(f"FAIL guard {name}: acted -> {result['acted']}, want {exp_acted}")
            failures += 1
            continue
        methods = [c[0] for c in http.calls]
        if methods != exp_methods:
            print(f"FAIL guard {name}: call methods -> {methods}, want {exp_methods}")
            failures += 1
        if not exp_acted:
            # No calls at all is the contract for the negative cases.
            if http.calls:
                print(f"FAIL guard {name}: expected no calls, got {http.calls}")
                failures += 1
            continue
        # Positive: the POST comment payload must name the exact unchecked
        # lines and the count; the PATCH must reopen.
        if len(http.calls) != 2:
            print(f"FAIL guard {name}: expected 2 calls, got {len(http.calls)}")
            failures += 1
            continue
        post_method, post_url, post_payload = http.calls[0]
        patch_method, patch_url, patch_payload = http.calls[1]
        comment = post_payload.get("body", "")
        for item in exp_unchecked:
            if f"- [ ] {item}" not in comment:
                print(f"FAIL guard {name}: comment missing line '- [ ] {item}'\ncomment:\n{comment}")
                failures += 1
        count_word = f"{len(exp_unchecked)} task-list item"
        if len(exp_unchecked) != 1:
            count_word += "s"
        if count_word not in comment:
            print(f"FAIL guard {name}: comment missing count phrase '{count_word}'\ncomment:\n{comment}")
            failures += 1
        if post_url != "https://api.github.com/repos/owner/repo/issues/42/comments":
            print(f"FAIL guard {name}: POST url -> {post_url}")
            failures += 1
        if patch_url != "https://api.github.com/repos/owner/repo/issues/42":
            print(f"FAIL guard {name}: PATCH url -> {patch_url}")
            failures += 1
        if patch_payload != {"state": "open"}:
            print(f"FAIL guard {name}: PATCH payload -> {patch_payload}, want {{'state': 'open'}}")
            failures += 1
    return failures


def run() -> int:
    failures = 0
    failures += run_parse_tests()
    failures += run_guard_tests()
    if failures:
        print(f"\n{failures} failure(s)")
        return 1
    print(f"ok ({len(PARSE_CASES)} parse cases, {len(GUARD_CASES)} guard cases)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
