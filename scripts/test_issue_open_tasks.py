#!/usr/bin/env python3
"""Table-driven tests for scripts/issue_open_tasks.py.

Run: python3 scripts/test_issue_open_tasks.py
No external deps. Exits non-zero on any failure.
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import issue_open_tasks as iot  # noqa: E402

# (name, body, expected_unchecked, expected_has_tasks)
CASES = [
    ("empty body", "", [], False),
    ("no task items", "Just prose, no checkboxes.\nMore prose.", [], False),
    ("all checked", "- [x] part 1\n- [X] part 2\n- [x] part 3", [], True),
    ("all unchecked", "- [ ] part 1\n- [ ] part 2\n- [ ] part 3",
     ["part 1", "part 2", "part 3"], True),
    ("mixed", "- [x] part 1 done\n- [ ] part 2 open\n- [ ] part 3 open",
     ["part 2 open", "part 3 open"], True),
    ("nested unchecked", "- [ ] top\n  - [ ] nested\n  - [x] nested done",
     ["top", "nested"], True),
    ("asterisk marker", "* [ ] asterisk task", ["asterisk task"], True),
    ("plus marker", "+ [ ] plus task", ["plus task"], True),
    ("checkbox not a list item", "Sentence with [ ] inline box.", [], False),
    ("loose bracket not task", "- [ ]no space after brackets", [], False),
    ("indented non-task bullet", "  - regular bullet", [], False),
    ("trailing whitespace unchecked", "- [ ] task   ", ["task"], True),
    ("empty checkbox text", "- [ ] ", [], False),
    ("multiline with code fence", "```\n- [ ] inside code, still matches\n```\n- [ ] real",
     ["inside code, still matches", "real"], True),
    ("issue #69 shape", "## Goal\n\n- [x] part 1: scrape\n- [ ] part 2: import\n- [ ] part 3: schedule\n",
     ["part 2: import", "part 3: schedule"], True),
]


def run() -> int:
    failures = 0
    for name, body, exp_unchecked, exp_has in CASES:
        got_unchecked = iot.parse_open_tasks(body)
        got_has = iot.has_task_items(body)
        if got_unchecked != exp_unchecked:
            print(f"FAIL {name}: parse_open_tasks -> {got_unchecked!r}, want {exp_unchecked!r}")
            failures += 1
        if got_has != exp_has:
            print(f"FAIL {name}: has_task_items -> {got_has!r}, want {exp_has!r}")
            failures += 1
    if failures:
        print(f"\n{failures} failure(s)")
        return 1
    print(f"ok ({len(CASES)} cases)")
    return 0


if __name__ == "__main__":
    sys.exit(run())
