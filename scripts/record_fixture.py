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
    array   -> [] if empty, else ONE element holding the UNION of every
               element's shape (keys merged across all elements, recursed).
               A heterogeneous array's key set is the union of what each
               element actually carries, so a key present only in element 2
               survives. Scalar/mixed arrays keep the reduced first element.
               LIMITATION: the union is a single merged element, so per-element
               key PRESENCE patterns (which element has which key) are lost —
               only the union key set and a merged shape are recorded. This is
               a deliberate trade for completeness of the key set.
    string  -> "str"
    integer -> 0
    float   -> 0.0   (ALWAYS — a float that is mathematically an integer is NOT
               an integer on the wire to Go's encoding/json: json.Unmarshal of
               `1.0` into an `int` field aborts with "cannot unmarshal number
               1.0 into ... of type int". Collapsing 1.0 to 0 would record a
               fixture that decodes green into `int` while the live call aborts
               — the exact shape of the last_check_date defect this recorder
               exists to prevent.)
    boolean -> true
    null    -> null

Two modes:
    LIVE  (default): performs an authenticated GET against the Hooppy API.
        Requires HOOPPY_API_TOKEN. The endpoint path is the first positional
        argument (e.g. /cross-posting). The output name is the second. The API
        base is read from HOOPPY_BASE_URL (the same env var the Go client uses,
        DefaultBaseURL = "https://api.hooppy.ru/api" in endpoints.go) so the
        recorder and the client share one source of truth. Pointing it at the
        Nuxt web app (https://hooppy.ru) returns HTML, not the API envelope —
        the smoke checks below fail that loudly instead of recording garbage.
    OFFLINE (--from-file): reads a saved raw response from a file instead of
        making a live call. Used for developing the reducer without API access.
        Refuses input whose scalar leaves are already exclusively the
        placeholder set — an already-reduced file is a silent fixed point
        (reduce(reduced) == reduced) and "succeeds" without recording anything,
        which masks a run that never saw a raw response.

A self-check mode (--self-check) verifies the reducer is a fixed point on every
fixture in testdata/live/: reduce(fixture) == fixture for all of them. This is
the idempotency oracle — an assertion nobody chose by hand. It is what the
crossposting_test.go F4 test shells out to.

Usage:
    # Live: record a fixture from the API
    HOOPPY_API_TOKEN=... python3 scripts/record_fixture.py \
        /cross-posting cross_postings.json

    # Offline: reduce a saved raw response
    python3 scripts/record_fixture.py --from-file raw_response.json \
        cross_postings.json

    # Self-check: assert reduce(fixture)==fixture for every committed fixture
    python3 scripts/record_fixture.py --self-check

The output is written to testdata/live/<name> with sorted keys and 2-space
indentation, matching the existing fixtures. The script does NOT overwrite an
existing fixture without --force.

Exit codes:
    0  fixture written / self-check passed
    1  usage error / missing token / HTTP failure / smoke failure / fixed-point refusal / self-check divergence
    2  output exists and --force was not given
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request

# The API base. Read from HOOPPY_BASE_URL so the recorder and the Go client
# (endpoints.go DefaultBaseURL) share one source of truth. The default MUST
# match DefaultBaseURL exactly — "https://api.hooppy.ru/api" (the api.
# subdomain), NOT "https://hooppy.ru/api" (the Nuxt web app, which returns
# HTML and is not the API). A wrong default here made the recorder's live mode
# 404/HTML-traceback on every run, so only --from-file was ever exercised.
DEFAULT_API_BASE = "https://api.hooppy.ru/api"
FIXTURE_DIR = "testdata/live"

# The scalar placeholder set. An input whose scalar leaves are ALL in this set
# is already reduced — --from-file refuses it so a wrong run cannot produce a
# fixture that looks correctly recorded without ever seeing a raw response.
_PLACEHOLDERS = (str, bool, type(None))


def _is_placeholder_scalar(v) -> bool:
    """True if v is a scalar placeholder the reducer would emit."""
    if v is None or isinstance(v, bool):
        return True
    if isinstance(v, str):
        return v == "str"
    if isinstance(v, (int, float)):
        # 0 (int) and 0.0 (float) are both placeholders; a real 0 value is
        # indistinguishable, which is the accepted false-positive risk of this
        # heuristic guard (an 89-key raw response is never all-zero in
        # practice).
        return v == 0 or v == 0.0
    return False


def _scalar_leaves(v):
    """Yield every scalar leaf in a generic-decoded JSON value."""
    if isinstance(v, dict):
        for val in v.values():
            yield from _scalar_leaves(val)
    elif isinstance(v, list):
        for val in v:
            yield from _scalar_leaves(val)
    else:
        yield v


def is_already_reduced(decoded) -> bool:
    """True if decoded has >=1 scalar leaf and every scalar leaf is a
    placeholder — i.e. the input looks already-reduced, not a raw response."""
    leaves = list(_scalar_leaves(decoded))
    if not leaves:
        return False
    return all(_is_placeholder_scalar(leaf) for leaf in leaves)


def merge_values(values):
    """Merge a list of already-reduced JSON values into one.

    The array reducer keeps ONE element that is the UNION of every element's
    shape: for object elements the key sets are unioned (shared keys recursed);
    for list elements the element shapes are unioned; otherwise the first
    reduced value wins. This records the full key set a heterogeneous array
    actually carries, so a key present only in element 2 survives — the
    completeness hole "arrays keep only element 0" left.
    """
    if not values:
        return None
    if all(isinstance(x, dict) for x in values):
        out = {}
        keys = set()
        for d in values:
            keys.update(d.keys())
        for k in sorted(keys):
            present = [d[k] for d in values if k in d]
            out[k] = merge_values(present)
        return out
    if all(isinstance(x, list) for x in values):
        elems = []
        for lst in values:
            elems.extend(lst)
        if not elems:
            return []
        return [merge_values(elems)]
    return values[0]


def reduce_value(v):
    """Apply the mechanical reduction to a generic-decoded JSON value."""
    if v is None:
        return None
    if isinstance(v, bool):
        return True
    if isinstance(v, int):
        return 0
    if isinstance(v, float):
        # ALWAYS emit 0.0 for a float. Go's encoding/json distinguishes 1.0
        # from 1: unmarshalling `1.0` into an `int` field aborts with
        # "cannot unmarshal number 1.0 into ... of type int". Collapsing an
        # integer-valued float to 0 would record a fixture that decodes green
        # into `int` while the live call aborts — the last_check_date defect
        # shape. The wire type is the property; preserve it.
        return 0.0
    if isinstance(v, str):
        return "str"
    if isinstance(v, list):
        if not v:
            return []
        return [merge_values([reduce_value(x) for x in v])]
    if isinstance(v, dict):
        return {k: reduce_value(val) for k, val in v.items()}
    raise TypeError(f"unexpected JSON type: {type(v).__name__}")


def reduce_response(raw_bytes: bytes) -> bytes:
    """Decode, reduce, and re-encode a raw JSON response."""
    decoded = json.loads(raw_bytes)
    reduced = reduce_value(decoded)
    return json.dumps(reduced, sort_keys=True, indent=2).encode() + b"\n"


def fetch_live(token: str, base: str, path: str) -> bytes:
    """Perform an authenticated GET against the Hooppy API.

    Smoke-checks the response so a wrong host (the Nuxt web app returning HTML,
    or an SPA JSON envelope that is not the API's shape) fails loudly instead
    of recording a fixture that is not the API's.
    """
    url = base + path
    req = urllib.request.Request(url, headers={
        "Authorization": f"Bearer {token}",
        "Accept": "application/json",
    })
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            ctype = resp.headers.get_content_type()
            body = resp.read()
    except urllib.error.HTTPError as e:
        body_text = e.read().decode(errors="replace")
        print(f"HTTP {e.code} {e.reason} for {url}\n{body_text}", file=sys.stderr)
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"network error for {url}: {e}", file=sys.stderr)
        sys.exit(1)

    # Smoke 1: Content-Type must be JSON-ish. The Nuxt web app serves
    # text/html; an API response is application/json (or +json).
    if not (ctype == "application/json" or "json" in ctype.lower()):
        snippet = body[:200].decode(errors="replace")
        print(f"smoke failure: {url} returned Content-Type {ctype!r}, expected "
              f"JSON — the base is likely the web app, not the API "
              f"(DefaultBaseURL is https://api.hooppy.ru/api). First 200 bytes:\n"
              f"{snippet}", file=sys.stderr)
        sys.exit(1)
    # Smoke 2: the body must parse as JSON AND be a structured value (object
    # or array). A stub returning HTML or a bare scalar is not an API envelope.
    try:
        parsed = json.loads(body)
    except json.JSONDecodeError as e:
        snippet = body[:200].decode(errors="replace")
        print(f"smoke failure: {url} body is not valid JSON ({e}) — the base is "
              f"likely the web app. First 200 bytes:\n{snippet}", file=sys.stderr)
        sys.exit(1)
    if not isinstance(parsed, (dict, list)):
        print(f"smoke failure: {url} decoded to a {type(parsed).__name__}, not an "
              f"object/array — not an API envelope.", file=sys.stderr)
        sys.exit(1)
    return body


def self_check() -> int:
    """Verify reduce_response is a fixed point on every committed fixture."""
    if not os.path.isdir(FIXTURE_DIR):
        print(f"self-check: {FIXTURE_DIR} does not exist", file=sys.stderr)
        return 1
    names = sorted(f for f in os.listdir(FIXTURE_DIR) if f.endswith(".json"))
    if not names:
        print(f"self-check: no .json fixtures in {FIXTURE_DIR}", file=sys.stderr)
        return 1
    failures = 0
    for name in names:
        path = os.path.join(FIXTURE_DIR, name)
        with open(path, "rb") as f:
            raw = f.read()
        try:
            reduced = reduce_response(raw)
        except Exception as e:
            print(f"self-check: {name}: reduce raised {e}", file=sys.stderr)
            failures += 1
            continue
        if reduced != raw:
            failures += 1
            print(f"self-check: {name}: reduce(fixture) != fixture — the reducer "
                  f"is not a fixed point on this committed fixture (a reducer "
                  f"change, or a trailing-newline drift).", file=sys.stderr)
            print(f"  want ({len(raw)} bytes): ...{raw[-40:]!r}", file=sys.stderr)
            print(f"  got  ({len(reduced)} bytes): ...{reduced[-40:]!r}",
                  file=sys.stderr)
    if failures:
        print(f"self-check: {failures}/{len(names)} fixtures diverged", file=sys.stderr)
        return 1
    print(f"self-check: {len(names)} fixtures are fixed points of the reducer")
    return 0


def main(argv: list[str]) -> int:
    args = argv[1:]
    from_file = False
    force = False
    self_check_mode = False
    positional = []

    for a in args:
        if a == "--from-file":
            from_file = True
        elif a == "--force":
            force = True
        elif a == "--self-check":
            self_check_mode = True
        elif a.startswith("--"):
            print(f"unknown option: {a}", file=sys.stderr)
            return 1
        else:
            positional.append(a)

    if self_check_mode:
        if positional or from_file or force:
            print("--self-check takes no other arguments", file=sys.stderr)
            return 1
        return self_check()

    if from_file:
        if len(positional) != 2:
            print("offline mode: record_fixture.py --from-file <raw.json> <output-name>",
                  file=sys.stderr)
            return 1
        raw_path, out_name = positional
        with open(raw_path, "rb") as f:
            raw = f.read()
        # Refuse an already-reduced input: reduce(reduced)==reduced is a silent
        # fixed point that "succeeds" without recording anything, masking a run
        # that never saw a raw response. Combined with a wrong host, a
        # plausible wrong run produces a fixture that looks correctly recorded.
        try:
            decoded = json.loads(raw)
        except json.JSONDecodeError as e:
            print(f"--from-file: {raw_path} is not valid JSON: {e}", file=sys.stderr)
            return 1
        if is_already_reduced(decoded):
            print(f"--from-file: {raw_path} appears already-reduced (every scalar "
                  f"leaf is a placeholder). --from-file reduces a RAW response; "
                  f"re-running it on a committed fixture is a silent fixed point "
                  f"that records nothing. Pass a raw response captured from the "
                  f"API.", file=sys.stderr)
            return 1
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
        base = os.environ.get("HOOPPY_BASE_URL", DEFAULT_API_BASE)
        raw = fetch_live(token, base, api_path)

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
