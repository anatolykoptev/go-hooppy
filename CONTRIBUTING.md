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

## Recording `testdata/live/` fixtures

Fixtures in `testdata/live/` are the oracle for the unknown-field diagnostic
(`unknown_field_diagnostic_test.go`) and the decode gate (`TestLiveFixtureDecodes`).
They are **recorded from live authenticated GETs and then mechanically reduced**,
never hand-authored from a field-name list. A hand-authored fixture encodes the
same guess the struct encodes, so it agrees with a wrong struct and the gate
stays green while the live call fails — that is how the cross-posting decode
defect shipped (the fixture guessed `last_check_date` as a string; the API sends
a number; the gate was green; `hooppy crossposting list` failed).

The required way to add or refresh a fixture is `scripts/record_fixture.py`:

```sh
# Live: record from the API (requires HOOPPY_API_TOKEN)
HOOPPY_API_TOKEN=... python3 scripts/record_fixture.py /cross-posting cross_postings.json

# Offline: reduce a saved raw response (no API call, for reproduction)
python3 scripts/record_fixture.py --from-file raw_response.json cross_postings.json
```

The API base comes from `HOOPPY_BASE_URL`, defaulting to the same value the Go
client uses (`DefaultBaseURL` in `endpoints.go`), so the recorder and the client
cannot drift apart. Pointing it at the bare `hooppy.ru` host returns the web
app's HTML rather than the API; the recorder's smoke checks reject that loudly
instead of writing a fixture full of nothing.

The reduction replaces every scalar with a type placeholder (`"str"`, `0`, `0.0`,
`true`, `null`) while preserving key names, nesting, and JSON types. **A float
always reduces to `0.0`, never `0`**, because `encoding/json` refuses to decode
`1.0` into an `int` while accepting `0` — collapsing the two would record a
fixture that passes the gate and a call that fails in production, which is the
defect this recorder exists to prevent.

An array reduces to **one element carrying the union of every element's shape**,
so a key present only in element 2 survives. The limitation is that per-element
key *presence* patterns are lost: the fixture records which keys the array can
carry, not which element carries which.

Zero non-placeholder values are present, so no account data or credentials ship
in the repo. The output has sorted keys and 2-space indentation, matching the
existing fixtures.

`--from-file` refuses an input whose scalars are already exclusively
placeholders: reducing an already-reduced file is a fixed point that reports
success while recording nothing, which would mask a run that never saw a raw
response.

`python3 scripts/record_fixture.py --self-check` asserts `reduce(fixture) ==
fixture` for every file in `testdata/live/`. That is the reducer's own gate, and
it is an oracle nobody chose by hand — unlike a test input written to agree with
the reducer, which proves only that the author was consistent with themselves.

After recording, update the struct to decode the fixture, update the
`unmodelledBaselines` in `unknown_field_diagnostic_test.go` if the key set
changed, and run `make preflight`. **Never edit a fixture to make a test pass** —
the fixture is the oracle; fix the struct.
