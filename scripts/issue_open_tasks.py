#!/usr/bin/env python3
"""Count unchecked GitHub task-list items in an issue body.

GitHub task-list items are markdown list entries whose first token is a checkbox:
    - [ ] not done      (unchecked)
    - [x] done          (checked, also [X])
    - [ ] nested items  (leading whitespace allowed)

Used by .github/workflows/issue-open-tasks-guard.yml to decide whether a closed
issue still has open work. Kept as a standalone script so the parsing step is
unit-testable (scripts/test_issue_open_tasks.py) rather than buried in YAML.

Usage (workflow):
    ISSUE_BODY="$body" python3 scripts/issue_open_tasks.py
Exit codes:
    0  body has no unchecked task items (or no task items at all)
    1  body has >=1 unchecked task item
Stdout (always): JSON {"count": N, "unchecked": ["trimmed line", ...]}
"""

from __future__ import annotations

import json
import os
import re
import sys

# A markdown task-list item: optional leading whitespace, a list marker
# (-, *, or +), whitespace, then a checkbox. We capture the unchecked form
# ([ ] with optional inner space) and the remainder of the line.
_UNCHECKED_RE = re.compile(r"^\s*[-*+]\s+\[\s\]\s+(.+?)\s*$")
# Any task-list item (checked or unchecked) — used to detect "has task items".
_ANY_TASK_RE = re.compile(r"^\s*[-*+]\s+\[[ xX]\]\s+.+$")


def parse_open_tasks(body: str) -> list[str]:
    """Return the trimmed text of every unchecked task-list item in body."""
    if not body:
        return []
    open_items: list[str] = []
    for line in body.splitlines():
        m = _UNCHECKED_RE.match(line)
        if m:
            open_items.append(m.group(1).strip())
    return open_items


def has_task_items(body: str) -> bool:
    if not body:
        return False
    return any(_ANY_TASK_RE.match(line) for line in body.splitlines())


def main() -> int:
    body = os.environ.get("ISSUE_BODY", "")
    if not body and not sys.stdin.isatty():
        body = sys.stdin.read()
    open_items = parse_open_tasks(body)
    sys.stdout.write(json.dumps({"count": len(open_items), "unchecked": open_items}))
    return 1 if open_items else 0


if __name__ == "__main__":
    sys.exit(main())
