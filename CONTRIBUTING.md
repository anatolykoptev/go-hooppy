# Contributing

## Multi-part issues use task-list checkboxes

When an issue has more than one piece of work, write the body as a GitHub
task list — one checkbox per part:

```markdown
- [x] part 1: scrape the source feed
- [ ] part 2: import scraped posts
- [ ] part 3: schedule the imported posts
```

This is not cosmetic. The `issue-open-tasks-guard` workflow
(`.github/workflows/issue-open-tasks-guard.yml`) reads the issue body on
close and **reopens the issue with a comment** if any box is still
unchecked. A closing PR keyword (`Closes #N`) closes the issue regardless
of how much work is actually done; the guard is what makes a partial close
loud instead of silent.

So:

- **One checkbox per part.** A part is anything that can land independently.
- **Tick the box only when that part is merged**, not when the PR opening it
  is opened.
- **No checkboxes** if the issue is a single unit of work — the guard does
  nothing when there is nothing to read, which is correct.
- **Close as `not_planned`** for an abandoned issue. The guard skips issues
  closed with `state_reason: not_planned`, so a deliberate abandonment with
  open boxes will not be reopened.

The parsing logic lives in `scripts/issue_open_tasks.py` and is covered by
`scripts/test_issue_open_tasks.py` (`python3 scripts/test_issue_open_tasks.py`).
