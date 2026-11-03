#!/usr/bin/env python3
"""Rebuild openapi.yaml from Hooppy's official API reference.

The reference is the SPA route https://hooppy.ru/api-docs, rendered from a
public static JS chunk (ApiDocsPage-<hash>.js). The chunk carries every
endpoint as a JS object literal (method, path, title, description,
parameters, request and response examples). This script:

  1. loads https://hooppy.ru/ and finds the current index-*.js,
  2. finds the ApiDocsPage-*.js chunk it references,
  3. downloads the chunk and PARSES the endpoint array as data -- the JS is
     never executed, only tokenised by a small literal parser,
  4. writes openapi.yaml (stdlib only; no PyYAML).

Usage:
  python3 -I scripts/fetch_api_docs.py                 # fetch + write openapi.yaml
  python3 -I scripts/fetch_api_docs.py --check         # fetch, exit 1 if openapi.yaml would change
  python3 -I scripts/fetch_api_docs.py --chunk-file F  # offline: parse a local copy (development)

The chunk URL and sha256 are recorded in the YAML header, so a diff of that
header tells you whether the docs changed upstream.
"""
import argparse
import datetime
import hashlib
import json
import os
import re
import sys
import urllib.request

SITE = "https://hooppy.ru"
CHUNK_RE = re.compile(r"ApiDocsPage-[A-Za-z0-9_-]+\.js")
INDEX_RE = re.compile(r"/assets/(index-[A-Za-z0-9_-]+\.js)")
UA = {"User-Agent": "go-hooppy-fetch-api-docs/1 (+https://github.com/anatolykoptev/go-hooppy)"}
METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE")


def http_get(url):
    req = urllib.request.Request(url, headers=UA)
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read()


def discover_chunk_url():
    """Home page -> index-*.js -> ApiDocsPage-*.js (the chunk name is hashed)."""
    home = http_get(SITE + "/").decode("utf-8", "replace")
    m = INDEX_RE.search(home)
    if not m:
        raise SystemExit("no /assets/index-*.js referenced from " + SITE + "/")
    index = http_get(SITE + "/assets/" + m.group(1)).decode("utf-8", "replace")
    c = CHUNK_RE.search(index)
    if not c:
        raise SystemExit("ApiDocsPage-*.js not referenced from " + m.group(1))
    return SITE + "/assets/" + c.group(0)


# ---------------------------------------------------------------- JS literal parser

class JSLiteralError(ValueError):
    pass


class Parser:
    """Parses the JS data-literal subset minifiers emit: objects with bare or
    quoted keys, arrays, backtick/single/double-quoted strings, numbers,
    !0/!1, true/false/null/void 0. Anything else (identifiers, calls,
    template interpolation) raises -- the chunk is data, never code."""

    ESC = {"n": "\n", "t": "\t", "r": "\r", "b": "\b", "f": "\f", "v": "\v", "0": "\0"}

    def __init__(self, s, i=0):
        self.s, self.i = s, i

    def ws(self):
        s = self.s
        while self.i < len(s) and s[self.i] in " \t\r\n":
            self.i += 1

    def err(self, msg):
        raise JSLiteralError("%s at offset %d: %r" % (msg, self.i, self.s[self.i:self.i + 40]))

    def value(self):
        self.ws()
        c = self.s[self.i]
        if c == "{":
            return self.obj()
        if c == "[":
            return self.arr()
        if c in "`'\"":
            return self.string()
        if c == "!":
            if self.s.startswith("!0", self.i):
                self.i += 2
                return True
            if self.s.startswith("!1", self.i):
                self.i += 2
                return False
            self.err("unsupported unary")
        m = re.compile(r"-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?").match(self.s, self.i)
        if m:
            self.i = m.end()
            t = m.group(0)
            return float(t) if re.search(r"[.eE]", t) else int(t)
        for lit, val in (("true", True), ("false", False), ("null", None), ("void 0", None)):
            if self.s.startswith(lit, self.i):
                self.i += len(lit)
                return val
        self.err("unsupported value")

    def string(self):
        q = self.s[self.i]
        self.i += 1
        out = []
        s = self.s
        while True:
            if self.i >= len(s):
                self.err("unterminated string")
            c = s[self.i]
            if c == q:
                self.i += 1
                return "".join(out)
            if c == "\\":
                n = s[self.i + 1]
                if n == "u":
                    out.append(chr(int(s[self.i + 2:self.i + 6], 16)))
                    self.i += 6
                    continue
                if n == "x":
                    out.append(chr(int(s[self.i + 2:self.i + 4], 16)))
                    self.i += 4
                    continue
                out.append(self.ESC.get(n, n))
                self.i += 2
                continue
            if q == "`" and c == "$" and s.startswith("${", self.i):
                self.err("template interpolation in data")
            out.append(c)
            self.i += 1

    def key(self):
        self.ws()
        c = self.s[self.i]
        if c in "`'\"":
            return self.string()
        m = re.compile(r"[A-Za-z_$][\w$]*|\d+").match(self.s, self.i)
        if not m:
            self.err("bad key")
        self.i = m.end()
        return m.group(0)

    def obj(self):
        self.i += 1
        out = {}
        while True:
            self.ws()
            if self.s[self.i] == "}":
                self.i += 1
                return out
            k = self.key()
            self.ws()
            if self.s[self.i] != ":":
                self.err("expected ':'")
            self.i += 1
            out[k] = self.value()
            self.ws()
            if self.s[self.i] == ",":
                self.i += 1

    def arr(self):
        self.i += 1
        out = []
        while True:
            self.ws()
            if self.s[self.i] == "]":
                self.i += 1
                return out
            out.append(self.value())
            self.ws()
            if self.s[self.i] == ",":
                self.i += 1


def extract_sections(js):
    """Finds the sections array: the array literal whose first element is an
    object starting with `slug:` and which carries `endpoints:`."""
    for m in re.finditer(r"\[\{slug:", js):
        try:
            val = Parser(js, m.start()).value()
        except (JSLiteralError, IndexError):
            continue
        if isinstance(val, list) and val and all(isinstance(x, dict) and "endpoints" in x for x in val):
            return val
    raise SystemExit("sections array ([{slug:..., endpoints:[...]}]) not found in chunk")


# ---------------------------------------------------------------- YAML emitter

def q(s):
    """YAML double-quoted scalar. JSON string syntax is a YAML subset."""
    return json.dumps(s, ensure_ascii=False)


def scalar(v):
    if v is True:
        return "true"
    if v is False:
        return "false"
    if v is None:
        return "null"
    if isinstance(v, (int, float)):
        return json.dumps(v)
    return q(str(v))


def emit(v, ind, out, key_line=None):
    """Appends block YAML for v. key_line is the already-written 'key:' text
    prefix handled by the caller; this function writes only the value."""
    pad = "  " * ind
    if isinstance(v, dict):
        if not v:
            out[-1] += " {}"
            return
        for k, x in v.items():
            ks = k if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_-]*", k) and k.lower() not in ("true", "false", "null", "yes", "no", "on", "off", "y", "n") else q(k)
            if isinstance(x, (dict, list)) and x:
                out.append("%s%s:" % (pad, ks))
                emit(x, ind + 1, out)
            elif isinstance(x, (dict, list)):
                out.append("%s%s: %s" % (pad, ks, "{}" if isinstance(x, dict) else "[]"))
            else:
                out.append("%s%s: %s" % (pad, ks, scalar(x)))
    elif isinstance(v, list):
        for x in v:
            if isinstance(x, dict) and x:
                first = True
                sub = []
                emit(x, ind + 1, sub)
                for line in sub:
                    if first:
                        out.append("%s- %s" % (pad, line[len(pad) + 2:]))
                        first = False
                    else:
                        out.append(line)
            elif isinstance(x, list) and x:
                out.append(pad + "-")
                emit(x, ind + 1, out)
            elif isinstance(x, (dict, list)):
                out.append("%s- %s" % (pad, "{}" if isinstance(x, dict) else "[]"))
            else:
                out.append("%s- %s" % (pad, scalar(x)))


# ---------------------------------------------------------------- OpenAPI build

SCHEMA_TYPES = {"integer": "integer", "string": "string", "boolean": "boolean", "number": "number", "array": "array", "object": "object"}


def schema_for(typ):
    """Doc type strings are free-form: 'integer', 'integer[]', 'object|null',
    'integer|boolean', 'array[7]', 'string (JSON)', 'file[]', 'group'. Anything
    not understood becomes a string carrying the raw text in x-doc-type."""
    t = (typ or "string").strip()
    if t in SCHEMA_TYPES:
        return {"type": SCHEMA_TYPES[t]}
    if t == "file":
        return {"type": "string", "format": "binary"}
    if "|" in t:
        parts = [x.strip() for x in t.split("|")]
        nullable = "null" in parts
        subs = [schema_for(x) for x in parts if x != "null"]
        out = subs[0] if len(subs) == 1 else {"oneOf": subs}
        if nullable:
            out = dict(out, nullable=True)
        return out
    m = re.fullmatch(r"array\[(\d+)\]", t)
    if m:
        return {"type": "array", "minItems": int(m.group(1)), "maxItems": int(m.group(1)), "items": {}}
    if t.endswith("[]"):
        return {"type": "array", "items": schema_for(t[:-2])}
    if t == "string (JSON)":
        return {"type": "string", "description": "JSON-encoded string"}
    return {"type": "string", "x-doc-type": t}


def param(p, where):
    d = {"name": p["name"], "in": where, "required": True if where == "path" else bool(p.get("required"))}
    if p.get("description"):
        d["description"] = p["description"]
    d["schema"] = schema_for(p.get("type"))
    if p.get("example") not in (None, ""):
        d["example"] = p["example"]
    return d


def operation(sec, ep):
    op = {"operationId": ep["id"], "summary": ep["title"]}
    if ep.get("description"):
        op["description"] = ep["description"]
    op["tags"] = [sec["title"]]
    params = [param(p, "path") for p in ep.get("pathParams") or []]
    params += [param(p, "query") for p in ep.get("query") or []]
    if params:
        op["parameters"] = params
    if ep.get("auth") is False:
        op["security"] = []
    body = ep.get("body") or []
    if body or ep.get("bodyExample") is not None:
        ct = ep.get("contentType") or "application/json"
        props, req = {}, []
        for b in body:
            s = schema_for(b.get("type"))
            if b.get("description"):
                s["description"] = b["description"]
            if b.get("example") not in (None, ""):
                s["example"] = b["example"]
            props[b["name"]] = s
            if b.get("required"):
                req.append(b["name"])
        sch = {"type": "object", "properties": props}
        if req:
            sch["required"] = req
        media = {"schema": sch}
        if ep.get("bodyExample") is not None:
            media["example"] = ep["bodyExample"]
        op["requestBody"] = {"required": bool(req), "content": {ct: media}}
    resp = {"description": "Успешный ответ"}
    if ep.get("responseExample") is not None:
        resp["content"] = {"application/json": {"example": ep["responseExample"]}}
    op["responses"] = {"200": resp}
    if ep.get("notes"):
        op["x-notes"] = list(ep["notes"])
    return op


def build(sections):
    paths = {}
    for sec in sections:
        for ep in sec["endpoints"]:
            m = ep["method"].upper()
            if m not in METHODS:
                raise SystemExit("unexpected method %r on %s" % (ep["method"], ep["path"]))
            slot = paths.setdefault(ep["path"], {})
            if m.lower() in slot:
                # The reference documents a few operations twice (e.g. "where
                # is a post published" in Посты and the generic form in
                # Ошибки публикаций). Keep the first as the operation and
                # carry the later wording as data instead of dropping it.
                dup = {"operationId": ep["id"], "summary": ep["title"], "tag": sec["title"]}
                for k in ("description", "notes", "responseExample"):
                    if ep.get(k):
                        dup[k] = ep[k]
                if ep.get("query"):
                    dup["query"] = [{"name": x["name"], "description": x.get("description", "")} for x in ep["query"]]
                slot[m.lower()].setdefault("x-also-documented-as", []).append(dup)
                continue
            slot[m.lower()] = operation(sec, ep)
    return paths


LICENSE_BLOCK = """  license:
    name: Apache 2.0
    url: https://www.apache.org/licenses/LICENSE-2.0.html"""


def render(sections, chunk_url, sha, version, fetched):
    paths = build(sections)
    n_ops = sum(len(v) for v in paths.values())
    out = [
        "# GENERATED by scripts/fetch_api_docs.py -- do not edit by hand.",
        "# Source: Hooppy official API reference (https://hooppy.ru/api-docs).",
        "# chunk-url: " + chunk_url,
        "# chunk-sha256: " + sha,
        "# fetched: " + fetched,
        "# paths: %d  operations: %d" % (len(paths), n_ops),
        "openapi: 3.0.3",
        "",
        "info:",
        "  title: Hooppy API",
        "  description: " + q("Официальная документация API Hooppy.ru (https://hooppy.ru/api-docs): создание, планирование и публикация постов в социальные сети."),
        LICENSE_BLOCK,
        "  version: " + q(version),
        "  contact:",
        "    name: Andrey Okhotin",
        "    url: https://hooppy.ru",
        "    email: me@icanary.ru",
        "servers:",
        "  - url: https://api.hooppy.ru/api",
        "security:",
        "  - bearerAuth: []",
        "",
        "tags:",
    ]
    for sec in sections:
        out.append("  - name: " + q(sec["title"]))
        out.append("    description: " + q(sec.get("description", "")))
    out += ["", "paths:"]
    for p in sorted(paths):
        out.append("  %s:" % q(p) if "{" in p else "  %s:" % p)
        for m in METHODS:
            if m.lower() in paths[p]:
                out.append("    %s:" % m.lower())
                emit(paths[p][m.lower()], 3, out)
    out += [
        "",
        "components:",
        "  securitySchemes:",
        "    bearerAuth:",
        "      type: http",
        "      scheme: bearer",
        "      bearerFormat: JWT",
        "",
    ]
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--chunk-url", help="override the discovered chunk URL")
    ap.add_argument("--chunk-file", help="parse a local copy instead of fetching (development only)")
    ap.add_argument("--out", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "openapi.yaml"))
    ap.add_argument("--version", help="info.version (default: today's date; the docs state none)")
    ap.add_argument("--fetched", help="date recorded in the header (default: today)")
    ap.add_argument("--check", action="store_true", help="do not write; exit 1 when the file would change")
    a = ap.parse_args()

    if a.chunk_file:
        raw = open(a.chunk_file, "rb").read()
        url = a.chunk_url or "(local file)"
    else:
        url = a.chunk_url or discover_chunk_url()
        raw = http_get(url)
    sha = hashlib.sha256(raw).hexdigest()
    sections = extract_sections(raw.decode("utf-8"))
    today = datetime.date.today().isoformat()
    text = render(sections, url, sha, a.version or today, a.fetched or today)

    if a.check:
        cur = open(a.out, encoding="utf-8").read() if os.path.exists(a.out) else ""
        strip = lambda t: "\n".join(l for l in t.splitlines() if not l.startswith(("# fetched:", "  version:")))
        if strip(cur) != strip(text):
            print("openapi.yaml is stale vs " + url, file=sys.stderr)
            return 1
        print("openapi.yaml up to date (chunk sha256 %s)" % sha)
        return 0
    with open(a.out, "w", encoding="utf-8") as f:
        f.write(text)
    print("wrote %s: %s" % (os.path.normpath(a.out), text.splitlines()[5][2:]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
