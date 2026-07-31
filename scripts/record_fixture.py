#!/usr/bin/env python3
"""Record a mechanically-reduced testdata/live/ fixture from an API response.

The fixtures in testdata/live/ are the oracle for the unknown-field diagnostic
(unknown_field_diagnostic_test.go) and the decode gate (TestLiveFixtureDecodes).
They are NOT hand-authored from a field-name list — that is how the
cross-posting decode defect shipped: the fixture agreed with the wrong struct,
the gate was green, and `hooppy crossposting list` failed against the live API.

The reduction convention is documented in the diagnostic test's header comment:
every scalar value is replaced by a type placeholder ("str", 0, 0.0, true, null)
while key names, nesting, and JSON types are preserved exactly. Zero non-
placeholder values are present, so no account data or credentials ship in the
repo. The diagnostic needs the server's key set and types, not its values.

Reduction rules:
    object  -> recurse into each value
    array   -> [] if empty, else keep only the first element (recursed)
    string  -> "str"
    integer -> 0
    non-integer number -> 0.0
    boolean -> true
    null    -> null

Two modes:
    LIVE  (default): performs an authenticated GET against the hooppy.ru API.
        Requires HOOPPY_API_TOKEN. The endpoint path is the first positional
        argument (e.g. /cross-posting). The output name is the second.
    OFFLINE (--from-file): reads a saved raw response from a file instead of
        making a live call. Used for reproducing an existing fixture (F4) and
        for developing the reducer without API access.

Usage:
    # Live: record a fixture from the API
    HOOPPY_API_TOKEN=... python3 scripts/record_fixture.py \
        /cross-posting cross_postings.json

    # Offline: reduce a saved raw response
    python3 scripts/record_fixture.py --from-file raw_response.json \
        cross_postings.json

The output is written to testdata/live/<name> with sorted keys and 2-space
indentation, matching the existing fixtures. The script does NOT overwrite an
existing fixture without --force.

Exit codes:
    0  fixture written
    1  usage error / missing token / HTTP failure
    2  output exists and --force was not given
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request

API_BASE = "https://hooppy.ru/api"
FIXTURE_DIR = "testdata/live"


def reduce_value(v):
    """Apply the mechanical reduction to a generic-decoded JSON value."""
    if v is None:
        return None
    if isinstance(v, bool):
        return True
    if isinstance(v, int):
        return 0
    if isinstance(v, float):
        # integer-valued floats reduce as integers (JSON has no int/float
        # distinction, but Python's json module decodes 1 as int and 1.0 as
        # float — a float that is mathematically an integer is an integer
        # on the wire too).
        if v.is_integer():
            return 0
        return 0.0
    if isinstance(v, str):
        return "str"
    if isinstance(v, list):
        if not v:
            return []
        return [reduce_value(v[0])]
    if isinstance(v, dict):
        return {k: reduce_value(val) for k, val in v.items()}
    raise TypeError(f"unexpected JSON type: {type(v).__name__}")


def reduce_response(raw_bytes: bytes) -> bytes:
    """Decode, reduce, and re-encode a raw JSON response."""
    decoded = json.loads(raw_bytes)
    reduced = reduce_value(decoded)
    return json.dumps(reduced, sort_keys=True, indent=2).encode() + b"\n"


def fetch_live(token: str, path: str) -> bytes:
    """Perform an authenticated GET against the hooppy.ru API."""
    url = API_BASE + path
    req = urllib.request.Request(url, headers={
        "Authorization": f"Bearer {token}",
        "Accept": "application/json",
    })
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.read()
    except urllib.error.HTTPError as e:
        body = e.read().decode(errors="replace")
        print(f"HTTP {e.code} {e.reason} for {url}\n{body}", file=sys.stderr)
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"network error for {url}: {e}", file=sys.stderr)
        sys.exit(1)


def main(argv: list[str]) -> int:
    args = argv[1:]
    from_file = False
    force = False
    positional = []

    for a in args:
        if a == "--from-file":
            from_file = True
        elif a == "--force":
            force = True
        elif a.startswith("--"):
            print(f"unknown option: {a}", file=sys.stderr)
            return 1
        else:
            positional.append(a)

    if from_file:
        if len(positional) != 2:
            print("offline mode: record_fixture.py --from-file <raw.json> <output-name>",
                  file=sys.stderr)
            return 1
        raw_path, out_name = positional
        with open(raw_path, "rb") as f:
            raw = f.read()
    else:
        if len(positional) != 2:
            print("live mode: HOOPPY_API_TOKEN=... record_fixture.py <path> <output-name>",
                  file=sys.stderr)
            return 1
        api_path, out_name = positional
        token = os.environ.get("HOOPPY_API_TOKEN", "")
        if not token:
            print("HOOPPY_API_TOKEN is required for live mode (or use --from-file)",
                  file=sys.stderr)
            return 1
        raw = fetch_live(token, api_path)

    reduced = reduce_response(raw)

    out_path = os.path.join(FIXTURE_DIR, out_name)
    if os.path.exists(out_path) and not force:
        print(f"{out_path} already exists — use --force to overwrite", file=sys.stderr)
        return 2

    os.makedirs(FIXTURE_DIR, exist_ok=True)
    with open(out_path, "wb") as f:
        f.write(reduced)

    print(f"wrote {out_path} ({len(reduced)} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
