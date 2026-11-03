#!/usr/bin/env python3
"""Offline tests for fetch_api_docs.py: literal parser, duplicate handling,
YAML shape. Stdlib only; run by `make py-test`. No network."""
import importlib.util
import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("fetch_api_docs", os.path.join(HERE, "fetch_api_docs.py"))
fad = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fad)

CHUNK = (
    'var x=1;var N=[{slug:`a`,title:`Посты`,description:`d`,endpoints:['
    '{id:`one`,method:`GET`,path:`/items/social/pages`,title:`T1`,description:`D1 \\` q`,auth:!0,'
    'query:[{name:`item_id`,type:`integer`,required:!0,description:`id`,example:5}],'
    'body:[],bodyExample:null,responseExample:{links:{822454:`https://vk.ru/wall-1_2`}},notes:[`n1`]},'
    '{id:`two`,method:`POST`,path:`/posts/{id}`,title:`T2`,description:``,auth:!1,'
    'pathParams:[{name:`id`,type:`integer`,required:!0,description:`pid`,example:7}],'
    'body:[{name:`ids`,type:`integer[]`,required:!0,description:`x`}],bodyExample:{ids:[1,2]},responseExample:{success:!0}}'
    ']},{slug:`b`,title:`Err`,description:`e`,endpoints:['
    '{id:`dup`,method:`GET`,path:`/items/social/pages`,title:`T3`,description:`D3`,auth:!0,query:[],responseExample:{}}'
    ']}];var y=2;'
)


class ParserTests(unittest.TestCase):
    def test_literal_values(self):
        p = fad.Parser('{a:`x\\ny`,"05":[1,-2.5,!0,!1,null,void 0],b:{}}')
        self.assertEqual(p.value(), {"a": "x\ny", "05": [1, -2.5, True, False, None, None], "b": {}})

    def test_refuses_code(self):
        for bad in ("{a:foo}", "{a:f()}", "{a:`x${y}`}"):
            with self.assertRaises(fad.JSLiteralError, msg=bad):
                fad.Parser(bad).value()

    def test_extract_sections_skips_surrounding_js(self):
        secs = fad.extract_sections(CHUNK)
        self.assertEqual([s["slug"] for s in secs], ["a", "b"])
        self.assertEqual(secs[0]["endpoints"][0]["responseExample"], {"links": {"822454": "https://vk.ru/wall-1_2"}})


class RenderTests(unittest.TestCase):
    def setUp(self):
        self.secs = fad.extract_sections(CHUNK)

    def test_paths_and_duplicate_kept_as_data(self):
        paths = fad.build(self.secs)
        self.assertEqual(sorted(paths), ["/items/social/pages", "/posts/{id}"])
        get = paths["/items/social/pages"]["get"]
        self.assertEqual(get["operationId"], "one")
        self.assertEqual(get["x-also-documented-as"][0]["operationId"], "dup")

    def test_request_body_and_security(self):
        post = fad.build(self.secs)["/posts/{id}"]["post"]
        self.assertEqual(post["security"], [])
        sch = post["requestBody"]["content"]["application/json"]["schema"]
        self.assertEqual(sch["properties"]["ids"], {"type": "array", "items": {"type": "integer"}, "description": "x"})
        self.assertEqual(sch["required"], ["ids"])

    def test_header_records_chunk_and_sha(self):
        out = fad.render(self.secs, "https://hooppy.ru/assets/ApiDocsPage-X.js", "ab" * 32, "2026-11-03", "2026-11-03")
        self.assertIn("# chunk-url: https://hooppy.ru/assets/ApiDocsPage-X.js", out)
        self.assertIn("# chunk-sha256: " + "ab" * 32, out)
        self.assertIn("paths: 2  operations: 2", out)
        self.assertIn("name: Apache 2.0", out)  # licence block kept


if __name__ == "__main__":
    unittest.main()
