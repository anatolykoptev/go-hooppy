package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// --- stripVKMarkup: table-driven over the real shapes ---

func TestStripVKMarkup(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single link converts to display text",
			in:   "See [https://vk.com/x|Заголовок] for details",
			want: "See Заголовок for details",
		},
		{
			name: "multiple links in one text all convert",
			in:   "[https://vk.com/a|First] and [https://vk.com/b|Second]",
			want: "First and Second",
		},
		{
			name: "pipe inside link text preserved (split on first pipe only)",
			in:   "[https://vk.com/x|text|with|pipes]",
			want: "text|with|pipes",
		},
		{
			name: "unterminated bracket left untouched",
			in:   "Text [https://vk.com/x|Заголовок without close",
			want: "Text [https://vk.com/x|Заголовок without close",
		},
		{
			name: "bracket with no pipe left untouched (not a wiki-link)",
			in:   "See [https://vk.com/x] here",
			want: "See [https://vk.com/x] here",
		},
		{
			name: "empty brackets left untouched",
			in:   "See [] here",
			want: "See [] here",
		},
		{
			name: "double-bracket internal page with display converts to display",
			in:   "See [[page_name|Display Text]] here",
			want: "See Display Text here",
		},
		{
			name: "double-bracket internal page without pipe left untouched",
			in:   "See [[page_name]] here",
			want: "See [[page_name]] here",
		},
		{
			name: "unterminated double-bracket left untouched",
			in:   "See [[page_name|Display without close",
			want: "See [[page_name|Display without close",
		},
		{
			name: "no markup passes through byte-identical",
			in:   "Plain text with no markup at all.\nSecond line.",
			want: "Plain text with no markup at all.\nSecond line.",
		},
		{
			name: "no markup with leading/trailing spaces preserved byte-identical",
			in:   "  spaced text  ",
			want: "  spaced text  ",
		},
		{
			name: "adjacent links with no gap",
			in:   "[https://vk.com/a|One][https://vk.com/b|Two]",
			want: "OneTwo",
		},
		{
			name: "link text containing brackets in display is handled by first-close rule",
			in:   "[https://vk.com/x|text] stuff]",
			want: "text stuff]",
		},
		// F1: a malformed marker must pass through byte-identical, including
		// when valid-looking markup appears later in the same text. The old
		// unterminated-[[ branch emitted only the first "[" and advanced by
		// one, so the second "[" re-entered the single-bracket parser, found
		// a later "]" and "|", and transformed text that was never a valid
		// marker. Each case below was mangled before the fix; all must now be
		// byte-identical.
		{
			name: "unterminated [[ with later ] and | passes through byte-identical",
			in:   "[[a|b]|c]",
			want: "[[a|b]|c]",
		},
		{
			name: "unterminated [[ with later ] and space passes through byte-identical",
			in:   "[[page|Display] text]",
			want: "[[page|Display] text]",
		},
		{
			name: "nested broken [[ with inner [ passes through byte-identical",
			in:   "[[a|[c|d]]]",
			want: "[[a|[c|d]]]",
		},
		// F5: a "[" inside a [url|text] inner is nested/broken markup — leave
		// it byte-untouched so strip is idempotent (the old first-close rule
		// turned "[url|[x|y]]" into "[x|y]" on pass 1 and "y" on pass 2).
		{
			name: "single bracket with inner [ passes through byte-identical (idempotence)",
			in:   "[url|[x|y]]",
			want: "[url|[x|y]]",
		},
		// A "[" inside the display text is NOT only nested/broken markup — it can
		// be a legitimate link whose display contains "[". stripVKMarkup leaves
		// such a marker byte-untouched DELIBERATELY to stay idempotent and avoid
		// the nested-bracket corruption class, at the cost of not converting this
		// rare valid form. This case pins the trade-off in the suite: the marker
		// reaches the wire byte-identical, not mangled and not converted.
		{
			name: "link whose display text contains [ is left byte-untouched (trade-off pinned)",
			in:   "[https://vk.com/x|see [note]]",
			want: "[https://vk.com/x|see [note]]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripVKMarkup(tc.in)
			if got != tc.want {
				t.Errorf("stripVKMarkup(%q) =\n  %q\nwant\n  %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripVKMarkup_Idempotent pins strip(strip(x)) == strip(x) over the whole
// table, so the idempotence property is asserted rather than incidental. A
// non-idempotent strip is a corruption vector on a live publishing queue: a
// caller that pre-transforms and then re-transforms would get a different
// result, and the first-close rule could leave a "[" in the output that
// re-parses on the second pass.
func TestStripVKMarkup_Idempotent(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"single link", "See [https://vk.com/x|Заголовок] for details"},
		{"multiple links", "[https://vk.com/a|First] and [https://vk.com/b|Second]"},
		{"pipe in display", "[https://vk.com/x|text|with|pipes]"},
		{"unterminated single", "Text [https://vk.com/x|Заголовок without close"},
		{"no pipe", "See [https://vk.com/x] here"},
		{"empty brackets", "See [] here"},
		{"double-bracket display", "See [[page_name|Display Text]] here"},
		{"double-bracket no pipe", "See [[page_name]] here"},
		{"unterminated double-bracket", "See [[page_name|Display without close"},
		{"plain text", "Plain text with no markup at all.\nSecond line."},
		{"spaced", "  spaced text  "},
		{"adjacent links", "[https://vk.com/a|One][https://vk.com/b|Two]"},
		{"first-close display", "[https://vk.com/x|text] stuff]"},
		{"F1 unterminated [[ with later pipe", "[[a|b]|c]"},
		{"F1 unterminated [[ with space", "[[page|Display] text]"},
		{"F1 nested broken [[", "[[a|[c|d]]]"},
		{"F5 single bracket with inner [", "[url|[x|y]]"},
		{"display text contains [ (trade-off)", "[https://vk.com/x|see [note]]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			once := stripVKMarkup(tc.in)
			twice := stripVKMarkup(once)
			if once != twice {
				t.Errorf("strip not idempotent on %q:\n  pass1=%q\n  pass2=%q", tc.in, once, twice)
			}
		})
	}
}

// --- detectVKMarkup: returns the raw markup strings ---

func TestDetectVKMarkup(t *testing.T) {
	t.Run("finds single link", func(t *testing.T) {
		hits := detectVKMarkup("See [https://vk.com/x|Заголовок] here")
		if len(hits) != 1 || hits[0] != "[https://vk.com/x|Заголовок]" {
			t.Errorf("detectVKMarkup = %v, want [\"[https://vk.com/x|Заголовок]\"]", hits)
		}
	})
	t.Run("finds multiple links", func(t *testing.T) {
		hits := detectVKMarkup("[https://vk.com/a|A] [https://vk.com/b|B]")
		if len(hits) != 2 {
			t.Errorf("detectVKMarkup = %v, want 2 hits", hits)
		}
	})
	t.Run("no markup returns empty", func(t *testing.T) {
		hits := detectVKMarkup("plain text")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty", hits)
		}
	})
	t.Run("unterminated bracket not reported", func(t *testing.T) {
		hits := detectVKMarkup("[https://vk.com/x|no close")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (unterminated is not a marker)", hits)
		}
	})
	t.Run("double-bracket page without pipe is reported", func(t *testing.T) {
		// [[page]] has no display text, but it is still VK wiki markup (an
		// internal page reference) — report it so the operator knows it is
		// there, even though strip leaves it untouched.
		hits := detectVKMarkup("See [[page_name]] here")
		if len(hits) != 1 || hits[0] != "[[page_name]]" {
			t.Errorf("detectVKMarkup = %v, want [\"[[page_name]]\"]", hits)
		}
	})
	t.Run("single bracket without pipe not reported", func(t *testing.T) {
		// [url] with no pipe is not a wiki-link — do not report it.
		hits := detectVKMarkup("See [https://vk.com/x] here")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (no pipe = not a wiki-link)", hits)
		}
	})
	t.Run("unterminated double-bracket not reported", func(t *testing.T) {
		hits := detectVKMarkup("See [[page_name|Display without close")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (unterminated [[ is not a marker)", hits)
		}
	})
	// A "[" inside a marker's inner means nested/broken markup — not a valid
	// VK marker. stripVKMarkup leaves it byte-untouched, so detection MUST
	// NOT report it either: reporting it would warn "VK markup found" and
	// suggest --strip-vk-markup, a flag that changes nothing. Detection and
	// strip must agree on what is a marker.
	t.Run("malformed nested double-bracket not reported (strip leaves it, detection agrees)", func(t *testing.T) {
		hits := detectVKMarkup("[[a|[c|d]]]")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (inner [ = malformed, strip leaves it, detection must agree)", hits)
		}
	})
	t.Run("malformed single bracket with inner [ not reported (strip leaves it, detection agrees)", func(t *testing.T) {
		hits := detectVKMarkup("[url|[x|y]]")
		if len(hits) != 0 {
			t.Errorf("detectVKMarkup = %v, want empty (inner [ = malformed, strip leaves it, detection must agree)", hits)
		}
	})
}

// --- detectAdMarkers: returns the matched lines ---

func TestDetectAdMarkers(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantLines []string
	}{
		{
			name:      "Erid marker matched",
			in:        "Some post text\nErid: 2VtzqwxyzAB\nmore text",
			wantLines: []string{"Erid: 2VtzqwxyzAB"},
		},
		{
			// The real-world token is written "erid:" in lower case at least
			// as often as "Erid:". Erid is a Latin token and the disclosure
			// body is Cyrillic, so the lower-case form cannot appear inside a
			// Russian word — case-insensitive matching is safe here.
			name:      "lower-case erid marker matched (case-insensitive)",
			in:        "erid: 2VtzqwxyzAB",
			wantLines: []string{"erid: 2VtzqwxyzAB"},
		},
		{
			// Regression guard: "Реклама." stays case-sensitive. The lower-case
			// "реклама." is an ordinary Russian word ("this is an ad. a good
			// one.") and would fire on non-disclosure text.
			name:      "lower-case реклама. NOT matched (Cyrillic word, false-positive risk)",
			in:        "это реклама. хорошая.",
			wantLines: nil,
		},
		{
			name:      "Реклама. marker matched",
			in:        "Реклама. ООО «Ромашка».",
			wantLines: []string{"Реклама. ООО «Ромашка»."},
		},
		{
			name:      "ИНН marker matched",
			in:        "ИНН 1234567890",
			wantLines: []string{"ИНН 1234567890"},
		},
		{
			// F4: "ИНН" is a Cyrillic ABBREVIATION, not a word — the lower-case
			// "инн" is not an ordinary Russian word, so folding has no
			// false-positive cost (a hit is one extra warning, never
			// corruption), and the miss is real: "инн 1234567890" in a sloppy
			// source escapes detection when the marker is case-sensitive.
			name:      "lower-case инн matched (Cyrillic abbreviation, fold-safe)",
			in:        "инн 1234567890",
			wantLines: []string{"инн 1234567890"},
		},
		{
			name: "full advertising block matched as one line",
			in:   "Реклама. ООО «Ромашка». ИНН 1234567. Erid: 2VtzqwxyzAB",
			wantLines: []string{
				"Реклама. ООО «Ромашка». ИНН 1234567. Erid: 2VtzqwxyzAB",
			},
		},
		{
			name:      "no markers returns empty",
			in:        "Just a normal post about cats.",
			wantLines: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectAdMarkers(tc.in)
			if len(got) != len(tc.wantLines) {
				t.Fatalf("detectAdMarkers = %v, want %v", got, tc.wantLines)
			}
			for i, line := range tc.wantLines {
				if got[i] != line {
					t.Errorf("detectAdMarkers[%d] = %q, want %q", i, got[i], line)
				}
			}
		})
	}
}

// --- runImport integration tests with httptest ---

// importStubServer serves GET /posts-search/{id}/edit and POST /posts,
// capturing every import request body. The edit responses are keyed by
// search-post ID so each post can carry different text.
func importStubServer(t *testing.T, editBodies map[int]string) (*httptest.Server, *importCapturer) {
	t.Helper()
	cap := &importCapturer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/posts-search/"), "/edit")
			id := 0
			for _, ch := range idStr {
				id = id*10 + int(ch-'0')
			}
			body, ok := editBodies[id]
			if !ok {
				t.Errorf("stub: no edit body for id %d (path %s)", id, r.URL.Path)
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			raw, _ := io.ReadAll(r.Body)
			cap.add(raw)
			// Distinguish single (has "id" in response) from batch ({"success":true}).
			// The test asserts on request bodies, not response shape, so a generic
			// single-id response works for both single and per-post-strip paths.
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":7001}`))
		default:
			t.Errorf("stub: unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

type importCapturer struct {
	mu      sync.Mutex
	bodies  []map[string]interface{}
	rawJSON []string
}

func (c *importCapturer) add(raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	c.bodies = append(c.bodies, m)
	c.rawJSON = append(c.rawJSON, string(raw))
}

func (c *importCapturer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func (c *importCapturer) texts(n int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 0 || n >= len(c.bodies) {
		return ""
	}
	texts, ok := c.bodies[n]["texts"].([]interface{})
	if !ok || len(texts) == 0 {
		return ""
	}
	first, ok := texts[0].(map[string]interface{})
	if !ok {
		return ""
	}
	s, _ := first["text"].(string)
	return s
}

func (c *importCapturer) ids(n int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 0 || n >= len(c.bodies) {
		return ""
	}
	s, _ := c.bodies[n]["ids"].(string)
	return s
}

// attachments returns the decoded wire attachments array for the n-th import
// request, or nil if absent. Used to pin attachment delivery per mode.
func (c *importCapturer) attachments(n int) []interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 0 || n >= len(c.bodies) {
		return nil
	}
	a, _ := c.bodies[n]["attachments"].([]interface{})
	return a
}

func newImportTestClient(t *testing.T, srv *httptest.Server) *hooppy.Client {
	t.Helper()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// editBodyFor builds a SearchPostEditResponse JSON with the given text.
func editBodyFor(text string) string {
	// Marshal safely to avoid escaping issues with Cyrillic and brackets.
	resp := map[string]interface{}{
		"id":                     "100",
		"publication_when_type":  1,
		"publication_how_type":   1,
		"publication_where_type": 1,
		"texts":                  []map[string]string{{"text": text}},
		"attachments":            []interface{}{},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// editBodyWithAttachments builds an edit response carrying the given raw
// attachments (each {type, data}) so attachment delivery can be pinned on the
// decoded import wire bodies.
func editBodyWithAttachments(text string, attachments []map[string]interface{}) string {
	resp := map[string]interface{}{
		"id":                     "100",
		"publication_when_type":  1,
		"publication_how_type":   1,
		"publication_where_type": 1,
		"texts":                  []map[string]string{{"text": text}},
		"attachments":            attachments,
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// TestRunImport_DetectionWarningsToStderr verifies that:
// - VK wiki-link markup triggers a stderr warning showing the markup
// - advertising markers trigger a stderr warning naming the matched line
// - stdout remains valid JSON (parseable) — a stray warning on stdout fails
// - the warning names --strip-vk-markup when VK markup is detected but flag is off
func TestRunImport_DetectionWarningsToStderr(t *testing.T) {
	text := "See [https://vk.com/x|Заголовок] here\nРеклама. ООО «Р». ИНН 1. Erid: 2Vtz"
	editBodies := map[int]string{1001: editBodyFor(text)}
	srv, cap := importStubServer(t, editBodies)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postID:   1001,
		whenType: 1,
		howType:  1,
		stripVK:  false,
	})
	if code != 0 {
		t.Fatalf("runImport exit %d; stderr=%s", code, errOut.String())
	}
	// stdout MUST be valid JSON.
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON (a warning leaked to stdout?): %v\nstdout=%s", err, out.String())
	}
	// stderr must name the VK markup and the flag.
	stderr := errOut.String()
	if !strings.Contains(stderr, "[https://vk.com/x|Заголовок]") {
		t.Errorf("stderr does not show the VK markup; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--strip-vk-markup") {
		t.Errorf("stderr does not name --strip-vk-markup; got:\n%s", stderr)
	}
	// stderr must name the advertising marker line.
	if !strings.Contains(stderr, "Erid") || !strings.Contains(stderr, "Реклама.") || !strings.Contains(stderr, "ИНН") {
		t.Errorf("stderr does not name the advertising marker line; got:\n%s", stderr)
	}
	// One import call, text untransformed (the original text from edit).
	if cap.count() != 1 {
		t.Errorf("import call count = %d, want 1 (flag off = single import)", cap.count())
	}
	if got := cap.texts(0); got != text {
		t.Errorf("import text = %q, want original %q (flag off = untransformed)", got, text)
	}
}

// TestRunImport_StripVKMarkup_Batch verifies that with --strip-vk-markup on a
// batch, each post's transformed text reaches the wire per post (N import
// calls instead of 1), asserted on the decoded request bodies the stub
// receives.
func TestRunImport_StripVKMarkup_Batch(t *testing.T) {
	editBodies := map[int]string{
		3001: editBodyFor("[https://vk.com/a|First] text"),
		3002: editBodyFor("[https://vk.com/b|Second] text"),
		3003: editBodyFor("no markup here"),
	}
	srv, cap := importStubServer(t, editBodies)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postIDs:  "3001,3002,3003",
		whenType: 1,
		howType:  1,
		stripVK:  true,
	})
	if code != 0 {
		t.Fatalf("runImport exit %d; stderr=%s", code, errOut.String())
	}
	// 3 import calls (one per post), not 1 batch call.
	if got, want := cap.count(), 3; got != want {
		t.Fatalf("import call count = %d, want %d (strip routes each post through its own import)", got, want)
	}
	// Each call carries the TRANSFORMED text for that post.
	wantTexts := []string{"First text", "Second text", "no markup here"}
	for i, want := range wantTexts {
		if got := cap.texts(i); got != want {
			t.Errorf("import[%d] text = %q, want %q (VK markup stripped)", i, got, want)
		}
	}
	// Each call carries a scalar id (per-post path), not a comma-joined batch.
	for i := 0; i < 3; i++ {
		if got := cap.ids(i); got == "" || strings.Contains(got, ",") {
			t.Errorf("import[%d] ids = %q, want a scalar id (per-post path)", i, got)
		}
	}
	// stdout must be valid JSON.
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout=%s", err, out.String())
	}
}

// TestF3_BatchDistinctTextsYieldsThreePostsWithOwnText verifies that
// --post-ids a,b,c with distinct source texts yields three posts, each
// carrying its OWN text (not a broadcast, not blank, not the first post's
// text). This is the CLI-level falsification of the old server-side batch
// defect where the batch form could not express per-post text.
//
// F3: --post-ids a,b,c with distinct texts → three posts with their own text.
func TestF3_BatchDistinctTextsYieldsThreePostsWithOwnText(t *testing.T) {
	editBodies := map[int]string{
		4001: editBodyFor("First post text"),
		4002: editBodyFor("Second post text"),
		4003: editBodyFor("Third post text"),
	}
	srv, cap := importStubServer(t, editBodies)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postIDs:  "4001,4002,4003",
		whenType: 1,
		howType:  1,
		stripVK:  false,
	})
	if code != 0 {
		t.Fatalf("runImport exit %d; stderr=%s", code, errOut.String())
	}
	// Three independent resolve+publish pairs (not one batch call).
	if got, want := cap.count(), 3; got != want {
		t.Fatalf("publish call count = %d, want %d (one per post)", got, want)
	}
	// Each post carries its OWN resolved text — not a broadcast, not blank.
	wantTexts := []string{"First post text", "Second post text", "Third post text"}
	for i, want := range wantTexts {
		if got := cap.texts(i); got != want {
			t.Errorf("publish[%d] text = %q, want %q (each post keeps its own resolved text)", i, got, want)
		}
	}
	// Each call carries a scalar id, not a comma-joined batch.
	for i := 0; i < 3; i++ {
		if got := cap.ids(i); got == "" || strings.Contains(got, ",") {
			t.Errorf("publish[%d] ids = %q, want a scalar id (per-post, not batch)", i, got)
		}
	}
}

// TestRunImport_NoStrip_BatchUnchanged verifies that with the flag OFF, a
// batch import still routes through the per-post path (N independent
// resolve+publish pairs). Each call carries the resolved text (no strip)
// and a scalar id.
func TestRunImport_NoStrip_BatchUnchanged(t *testing.T) {
	editBodies := map[int]string{
		3001: editBodyFor("[https://vk.com/a|First] text"),
		3002: editBodyFor("[https://vk.com/b|Second] text"),
	}
	srv, cap := importStubServer(t, editBodies)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postIDs:  "3001,3002",
		whenType: 1,
		howType:  1,
		stripVK:  false,
	})
	if code != 0 {
		t.Fatalf("runImport exit %d; stderr=%s", code, errOut.String())
	}
	// N import calls (one per post), not 1 batch call.
	if got, want := cap.count(), 2; got != want {
		t.Fatalf("import call count = %d, want %d (N per-post calls)", got, want)
	}
	// Each call carries a scalar id, not a comma-joined batch.
	for i := 0; i < 2; i++ {
		if got := cap.ids(i); got == "" || strings.Contains(got, ",") {
			t.Errorf("import[%d] ids = %q, want a scalar id", i, got)
		}
	}
	// stderr warns about VK markup (detection always on) and names the flag.
	stderr := errOut.String()
	if !strings.Contains(stderr, "[https://vk.com/a|First]") {
		t.Errorf("stderr does not warn about VK markup in post 3001; got:\n%s", stderr)
	}
}

// TestRunImport_StripVKMarkup_SinglePost verifies that strip on a single post
// transforms the text in-place (still one import call).
func TestRunImport_StripVKMarkup_SinglePost(t *testing.T) {
	editBodies := map[int]string{1001: editBodyFor("[https://vk.com/x|Заголовок] body")}
	srv, cap := importStubServer(t, editBodies)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postID:   1001,
		whenType: 1,
		howType:  1,
		stripVK:  true,
	})
	if code != 0 {
		t.Fatalf("runImport exit %d; stderr=%s", code, errOut.String())
	}
	if cap.count() != 1 {
		t.Fatalf("import call count = %d, want 1 (single post = one call)", cap.count())
	}
	if got, want := cap.texts(0), "Заголовок body"; got != want {
		t.Errorf("import text = %q, want %q (VK markup stripped in-place)", got, want)
	}
}

// TestRunImport_AdMarkersWarnOnly verifies that advertising markers trigger a
// warning but are NEVER removed from the text on the wire, even with
// --strip-vk-markup on (strip only touches VK wiki-link markup).
func TestRunImport_AdMarkersWarnOnly(t *testing.T) {
	adText := "Реклама. ООО «Р». ИНН 1. Erid: 2Vtz"
	editBodies := map[int]string{1001: editBodyFor(adText)}
	srv, cap := importStubServer(t, editBodies)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postID:   1001,
		whenType: 1,
		howType:  1,
		stripVK:  true, // even with strip on
	})
	if code != 0 {
		t.Fatalf("runImport exit %d; stderr=%s", code, errOut.String())
	}
	// The ad text must be byte-identical on the wire — strip does NOT touch it.
	if got := cap.texts(0); got != adText {
		t.Errorf("import text = %q, want %q (ad markers never auto-removed)", got, adText)
	}
	// stderr must warn about the ad marker.
	if !strings.Contains(errOut.String(), "Erid") {
		t.Errorf("stderr does not warn about ad marker; got:\n%s", errOut.String())
	}
}

// TestRunImport_AttachmentDelivery_BothModes verifies that both strip and
// non-strip batch modes route through the per-post path (N independent
// resolve+publish pairs). Each call carries the resolved attachments from
// the edit response — the client-side loop always sends them, regardless of
// --strip-vk-markup (which only affects text).
func TestRunImport_AttachmentDelivery_BothModes(t *testing.T) {
	photo := []map[string]interface{}{
		{"type": "photo", "data": map[string]interface{}{"id": 555, "url": "https://vk.com/p.jpg"}},
	}
	editBodies := map[int]string{
		4001: editBodyWithAttachments("post one", photo),
		4002: editBodyWithAttachments("post two", photo),
	}

	// --- NON-STRIP batch: N per-post calls, attachments from resolve ---
	srv1, cap1 := importStubServer(t, editBodies)
	c1 := newImportTestClient(t, srv1)
	var out1, err1 strings.Builder
	code := runImport(context.Background(), c1, &out1, &err1, importArgs{
		postIDs:  "4001,4002",
		whenType: 1,
		howType:  1,
		stripVK:  false,
	})
	if code != 0 {
		t.Fatalf("non-strip exit %d; stderr=%s", code, err1.String())
	}
	if got := cap1.count(); got != 2 {
		t.Fatalf("non-strip import calls = %d, want 2 (N per-post calls)", got)
	}
	for i := 0; i < 2; i++ {
		atts := cap1.attachments(i)
		if len(atts) != 1 {
			t.Fatalf("non-strip import[%d] attachments len = %d, want 1 (photo group from resolve)", i, len(atts))
		}
		group, ok := atts[0].(map[string]interface{})
		if !ok || group["type"] != "photos" {
			t.Errorf("non-strip import[%d] attachment[0] = %v, want {type:photos, data:[...]}", i, atts[0])
		}
	}

	// --- STRIP batch: same N per-post calls, attachments from resolve ---
	srv2, cap2 := importStubServer(t, editBodies)
	c2 := newImportTestClient(t, srv2)
	var out2, err2 strings.Builder
	code = runImport(context.Background(), c2, &out2, &err2, importArgs{
		postIDs:  "4001,4002",
		whenType: 1,
		howType:  1,
		stripVK:  true,
	})
	if code != 0 {
		t.Fatalf("strip exit %d; stderr=%s", code, err2.String())
	}
	if got := cap2.count(); got != 2 {
		t.Fatalf("strip import calls = %d, want 2 (per-post)", got)
	}
	for i := 0; i < 2; i++ {
		atts := cap2.attachments(i)
		if len(atts) != 1 {
			t.Fatalf("strip import[%d] attachments len = %d, want 1 (photo group)", i, len(atts))
		}
		group, ok := atts[0].(map[string]interface{})
		if !ok || group["type"] != "photos" {
			t.Errorf("strip import[%d] attachment[0] = %v, want {type:photos, data:[...]}", i, atts[0])
		}
	}
}

// TestRunImport_CostWarning_SingleIsFree pins that the single-post path's
// "flag off + VK markup detected" warning tells the operator the flag is FREE
// (a single post is already one import call), not the batch N-calls cost —
// discouraging a single-post caller from a free correctness win.
func TestRunImport_CostWarning_SingleIsFree(t *testing.T) {
	editBodies := map[int]string{1001: editBodyFor("See [https://vk.com/x|Заголовок] body")}
	srv, _ := importStubServer(t, editBodies)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postID:   1001,
		whenType: 1,
		howType:  1,
		stripVK:  false,
	})
	if code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut.String())
	}
	stderr := errOut.String()
	if !strings.Contains(stderr, "no extra cost") {
		t.Errorf("single-post warning does not say the flag is free; got:\n%s", stderr)
	}
	if strings.Contains(stderr, "N import calls instead of 1 batch call") {
		t.Errorf("single-post warning leaks the batch N-calls cost message; got:\n%s", stderr)
	}
}

// importStubServerFailingNth is like importStubServer but the failNth-th
// POST /posts request (1-based) returns 500, so the per-post import
// path sees a failure on exactly one post. Used to pin that a partial batch
// failure does not discard already-published posts.
func importStubServerFailingNth(t *testing.T, editBodies map[int]string, failNth int) *httptest.Server {
	t.Helper()
	var importCount int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/posts-search/"), "/edit")
			id := 0
			for _, ch := range idStr {
				id = id*10 + int(ch-'0')
			}
			body, ok := editBodies[id]
			if !ok {
				t.Errorf("stub: no edit body for id %d (path %s)", id, r.URL.Path)
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			mu.Lock()
			importCount++
			n := importCount
			mu.Unlock()
			if n == failNth {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"simulated failure"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			// Distinct ids per call so the test can assert which landed.
			fmt.Fprintf(w, `{"id":%d}`, 7000+n)
		default:
			t.Errorf("stub: unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunImport_StripBatch_PartialFailurePreservesPublished pins F3: a
// per-post import failure mid-batch must NOT discard the posts already
// published. The strip-batch path is NOT atomic (N import calls), so
// returning early before encoding would hide live-in-queue posts from stdout
// and invite a duplicate-spawning re-run. The fix: continue past the failure,
// always encode a per-post result array, exit non-zero only AFTER stdout is
// written. Asserted by PARSING stdout (not substring), so the shape contract
// is pinned.
func TestRunImport_StripBatch_PartialFailurePreservesPublished(t *testing.T) {
	editBodies := map[int]string{
		5001: editBodyFor("post one"),
		5002: editBodyFor("post two"),
		5003: editBodyFor("post three"),
		5004: editBodyFor("post four"),
		5005: editBodyFor("post five"),
	}
	// Fail the 3rd of 5 imports.
	srv := importStubServerFailingNth(t, editBodies, 3)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postIDs:  "5001,5002,5003,5004,5005",
		whenType: 1,
		howType:  1,
		stripVK:  true,
	})
	// Exit non-zero — a post failed.
	if code == 0 {
		t.Fatalf("exit 0; want non-zero (one import failed); stderr=%s", errOut.String())
	}
	// Parse stdout (NOT substring) — the shape contract is the point.
	var parsed struct {
		StripVKMarkup bool `json:"strip_vk_markup"`
		PerPost       []struct {
			SearchPostID int    `json:"search_post_id"`
			Status       string `json:"status"`
			PostID       int    `json:"post_id,omitempty"`
			Error        string `json:"error,omitempty"`
		} `json:"per_post"`
	}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout=%s", err, out.String())
	}
	if !parsed.StripVKMarkup {
		t.Errorf("strip_vk_markup = false, want true")
	}
	if got, want := len(parsed.PerPost), 5; got != want {
		t.Fatalf("per_post len = %d, want %d (every post attempted must be listed); stdout=%s", got, want, out.String())
	}
	// Count outcomes: 2 created before the failure, 1 failed, 2 created after.
	var created, failed int
	createdIDs := map[int]bool{}
	for _, r := range parsed.PerPost {
		switch r.Status {
		case "created":
			created++
			createdIDs[r.PostID] = true
		case "failed":
			failed++
		default:
			t.Errorf("per_post status = %q, want created or failed", r.Status)
		}
	}
	if created != 4 {
		t.Errorf("created count = %d, want 4 (2 before failure + 2 after)", created)
	}
	if failed != 1 {
		t.Errorf("failed count = %d, want 1 (the 3rd import)", failed)
	}
	// The failed entry must be the 3rd post and carry an error.
	if parsed.PerPost[2].Status != "failed" || parsed.PerPost[2].SearchPostID != 5003 {
		t.Errorf("per_post[2] = {id:%d status:%q}, want {id:5003 status:failed}", parsed.PerPost[2].SearchPostID, parsed.PerPost[2].Status)
	}
	if parsed.PerPost[2].Error == "" {
		t.Errorf("per_post[2] error empty, want the failure reason")
	}
	// The 2 successful ids BEFORE the failure must be present and parseable.
	for _, wantID := range []int{7001, 7002} {
		if !createdIDs[wantID] {
			t.Errorf("created post_id %d not in stdout per_post (already-published posts must be recorded); got %v", wantID, createdIDs)
		}
	}
	// The 2 attempted-after must also be present and created.
	for _, wantID := range []int{7004, 7005} {
		if !createdIDs[wantID] {
			t.Errorf("created post_id %d not in stdout per_post (posts after the failure must still be attempted); got %v", wantID, createdIDs)
		}
	}
	// stderr must name the failed publish.
	if !strings.Contains(errOut.String(), "PublishPost(5003)") {
		t.Errorf("stderr does not name the failed publish; got:\n%s", errOut.String())
	}
}

// TestRunImport_StripBatch_EditFetchFailurePreservesPublished pins the OTHER
// half of F3: the strip-batch path's GetSearchPostEdit failure branch. The
// existing F3 test (TestRunImport_StripBatch_PartialFailurePreservesPublished)
// fails the IMPORT (the second API call); this test fails the EDIT FETCH (the
// first). The two branches are separate code and can diverge in one edit, so
// both must be pinned. The edit-fetch branch is symmetric with the import
// branch: it records a "failed" result and continues, so the other four posts
// are still attempted, the successful ones are created, and stdout lists ALL
// FIVE with their outcomes. Asserted by PARSING stdout (not substring).
func TestRunImport_StripBatch_EditFetchFailurePreservesPublished(t *testing.T) {
	editBodies := map[int]string{
		5101: editBodyFor("post one"),
		5102: editBodyFor("post two"),
		5104: editBodyFor("post four"),
		5105: editBodyFor("post five"),
	}
	// Fail the edit GET for post 5103 (the 3rd of 5).
	srv, cap := importStubServerEditFailing(t, editBodies, 5103)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postIDs:  "5101,5102,5103,5104,5105",
		whenType: 1,
		howType:  1,
		stripVK:  true,
	})
	// Exit non-zero — a post failed.
	if code == 0 {
		t.Fatalf("exit 0; want non-zero (one edit fetch failed); stderr=%s", errOut.String())
	}
	// Parse stdout (NOT substring) — the shape contract is the point.
	var parsed struct {
		StripVKMarkup bool `json:"strip_vk_markup"`
		PerPost       []struct {
			SearchPostID int    `json:"search_post_id"`
			Status       string `json:"status"`
			PostID       int    `json:"post_id,omitempty"`
			Error        string `json:"error,omitempty"`
		} `json:"per_post"`
	}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout=%s", err, out.String())
	}
	if !parsed.StripVKMarkup {
		t.Errorf("strip_vk_markup = false, want true")
	}
	// ALL FIVE posts must be listed — the edit-fetch failure must not
	// discard the other four.
	if got, want := len(parsed.PerPost), 5; got != want {
		t.Fatalf("per_post len = %d, want %d (every post attempted must be listed, even one whose edit fetch failed); stdout=%s", got, want, out.String())
	}
	// The failed entry must be the 3rd post and carry an error.
	if parsed.PerPost[2].Status != "failed" || parsed.PerPost[2].SearchPostID != 5103 {
		t.Errorf("per_post[2] = {id:%d status:%q}, want {id:5103 status:failed}", parsed.PerPost[2].SearchPostID, parsed.PerPost[2].Status)
	}
	if parsed.PerPost[2].Error == "" {
		t.Errorf("per_post[2] error empty, want the failure reason")
	}
	// The other four must be "created" with non-zero post ids.
	var created, failed int
	for i, r := range parsed.PerPost {
		switch r.Status {
		case "created":
			created++
			if r.PostID == 0 {
				t.Errorf("per_post[%d] created but post_id = 0, want the published id", i)
			}
		case "failed":
			failed++
		default:
			t.Errorf("per_post[%d] status = %q, want created or failed", i, r.Status)
		}
	}
	if created != 4 {
		t.Errorf("created count = %d, want 4 (the other four posts must still be attempted and created)", created)
	}
	if failed != 1 {
		t.Errorf("failed count = %d, want 1 (only the 3rd edit fetch failed)", failed)
	}
	// Only 4 import calls reach the wire (the 3rd post's import is never
	// attempted because its edit fetch failed first).
	if got, want := cap.count(), 4; got != want {
		t.Errorf("import call count = %d, want %d (the edit-fetch-failed post is not imported)", got, want)
	}
	// stderr must name the failed edit fetch.
	if !strings.Contains(errOut.String(), "ResolveSearchPost(5103)") {
		t.Errorf("stderr does not name the failed edit fetch; got:\n%s", errOut.String())
	}
}

// importStubServerEditFailing is like importStubServer but the GET edit for
// failID returns 500. Used to pin F2: a detection-read failure in batch
// flag-off mode must NOT abort the import (the GET exists only to produce a
// warning; the import itself does not need it).
func importStubServerEditFailing(t *testing.T, editBodies map[int]string, failID int) (*httptest.Server, *importCapturer) {
	t.Helper()
	cap := &importCapturer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/posts-search/"), "/edit")
			id := 0
			for _, ch := range idStr {
				id = id*10 + int(ch-'0')
			}
			if id == failID {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"simulated edit fetch failure"}`))
				return
			}
			body, ok := editBodies[id]
			if !ok {
				t.Errorf("stub: no edit body for id %d (path %s)", id, r.URL.Path)
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			raw, _ := io.ReadAll(r.Body)
			cap.add(raw)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":7001}`))
		default:
			t.Errorf("stub: unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

// TestRunImport_BatchFlagOff_DetectionReadFailureNonFatal pins F2: in batch
// mode, a failed resolve (GetSearchPostEdit) for one post must NOT abort the
// entire batch — the loop records a "failed" result and continues with the
// remaining posts. The exit is 2 (partial: some succeeded, some failed).
func TestRunImport_BatchFlagOff_DetectionReadFailureNonFatal(t *testing.T) {
	editBodies := map[int]string{
		6001: editBodyFor("post one"),
		6002: editBodyFor("post two"),
		6003: editBodyFor("post three"),
	}
	// Fail the edit GET for post 6002.
	srv, cap := importStubServerEditFailing(t, editBodies, 6002)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postIDs:  "6001,6002,6003",
		whenType: 1,
		howType:  1,
		stripVK:  false,
	})
	// Partial: 2 succeeded, 1 failed → exit 2.
	if code != 2 {
		t.Fatalf("exit %d; want 2 (partial: 2 succeeded, 1 resolve failed); stderr=%s", code, errOut.String())
	}
	// 2 import calls (6002's resolve failed, so no PublishPost for it).
	if got, want := cap.count(), 2; got != want {
		t.Fatalf("import call count = %d, want %d (the failed-resolve post is not published)", got, want)
	}
	// stderr must name the failed resolve.
	stderr := errOut.String()
	if !strings.Contains(stderr, "ResolveSearchPost(6002)") {
		t.Errorf("stderr does not name the failed resolve for post 6002; got:\n%s", stderr)
	}
	// stdout must still be valid JSON with per_post results.
	var parsed struct {
		PerPost []struct {
			SearchPostID int    `json:"search_post_id"`
			Status       string `json:"status"`
		} `json:"per_post"`
	}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout=%s", err, out.String())
	}
	if len(parsed.PerPost) != 3 {
		t.Fatalf("per_post len = %d, want 3 (every post attempted)", len(parsed.PerPost))
	}
	// The 2nd post (6002) must be "failed".
	if parsed.PerPost[1].Status != "failed" || parsed.PerPost[1].SearchPostID != 6002 {
		t.Errorf("per_post[1] = {id:%d status:%q}, want {id:6002 status:failed}", parsed.PerPost[1].SearchPostID, parsed.PerPost[1].Status)
	}
}

// importStubServerZeroID is like importStubServer but POST /posts
// returns {"id":0} — a create response that carries no identity. Used to pin
// that a zero-id create is not silently lossy on stdout: the record must make
// the missing identity visible (post_id field present, distinct status) rather
// than omitting the field.
func importStubServerZeroID(t *testing.T, editBodies map[int]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/posts-search/"), "/edit")
			id := 0
			for _, ch := range idStr {
				id = id*10 + int(ch-'0')
			}
			body, ok := editBodies[id]
			if !ok {
				t.Errorf("stub: no edit body for id %d (path %s)", id, r.URL.Path)
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":0}`))
		default:
			t.Errorf("stub: unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunImport_StripBatch_ZeroIDCreateIsVisible pins that a create response
// with id 0 is not silently lossy on stdout. The whole premise of the F3
// per-post record is that stdout is a complete record of what landed, so a
// re-run can deduplicate; a "created" entry whose post_id field was omitted
// (the old omitempty encoding) would be un-deduplicatable. The fix has two
// parts, both asserted here by PARSING stdout (not substring):
//   - the post_id field is PRESENT in the JSON object (not omitted), so a
//     parser sees the key even when the value is 0;
//   - the status is the distinct "created_no_id", not "created" — a created
//     post whose identity is unknown is not the same thing as a created post.
func TestRunImport_StripBatch_ZeroIDCreateIsVisible(t *testing.T) {
	editBodies := map[int]string{7001: editBodyFor("post one")}
	srv := importStubServerZeroID(t, editBodies)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postIDs:  "7001",
		whenType: 1,
		howType:  1,
		stripVK:  true,
	})
	// A zero-id create is NOT a failure — the post was created, the server
	// just did not return its id. Exit zero.
	if code != 0 {
		t.Fatalf("exit %d; want 0 (zero-id create is not a failure); stderr=%s", code, errOut.String())
	}
	// Parse into a generic shape so we can assert KEY PRESENCE (omitempty
	// would drop the key; a struct decode cannot distinguish 0-from-absent
	// without a pointer field).
	var parsed struct {
		StripVKMarkup bool             `json:"strip_vk_markup"`
		PerPost       []map[string]any `json:"per_post"`
	}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout=%s", err, out.String())
	}
	if !parsed.StripVKMarkup {
		t.Errorf("strip_vk_markup = false, want true")
	}
	if got, want := len(parsed.PerPost), 1; got != want {
		t.Fatalf("per_post len = %d, want %d; stdout=%s", got, want, out.String())
	}
	rec := parsed.PerPost[0]
	// The post_id key MUST be present — omitempty would have dropped it.
	if _, ok := rec["post_id"]; !ok {
		t.Errorf("per_post[0] has no post_id key (omitempty dropped it); got %v; stdout=%s", rec, out.String())
	}
	// The status MUST be the distinct "created_no_id", not "created".
	if got, want := rec["status"], "created_no_id"; got != want {
		t.Errorf("per_post[0] status = %v, want %q (a zero-id create is not reported as created); stdout=%s", got, want, out.String())
	}
	// And the post_id value MUST be 0 (present, not omitted).
	if got, want := rec["post_id"], float64(0); got != want {
		t.Errorf("per_post[0] post_id = %v, want 0 (present, not omitted); stdout=%s", got, out.String())
	}
}

// --- F6: the three import paths agree on *CreateNoIDError -------------------
//
// Review finding 2: *CreateNoIDError was handled in 1 of 3 import paths. The
// strip-batch per-post loop mapped it to "created_no_id" exit 0; the single
// path and the batch-flag-off path did NOT type-assert, so the same server
// behaviour exited 1 there and 0 here. The single path is the one that
// invites the duplicate re-run the strip path exists to prevent.
//
// Fix: pick the strip-batch behaviour (CreateNoIDError → exit 0 with a
// created_no_id signal) and apply it to all three. This test drives all three
// paths against a server that returns {"id":0} (a create with no handle) and
// asserts the SAME outcome: exit 0, and a stdout record that signals
// created_no_id so a re-run can tell a published-but-unidentified post from a
// real failure.
//
// RED-on-revert: break one path's handling (drop the type-assert so it falls
// through to exit 1) and that path's subtest goes RED — exit 1 instead of 0.
func TestRunImport_F6_ThreePathsAgreeOnCreateNoID(t *testing.T) {
	editBodies := map[int]string{8001: editBodyFor("post one")}

	// pathOutcome runs one import path and returns (exitCode, stdoutParsed).
	pathOutcome := func(t *testing.T, args importArgs) (int, map[string]interface{}) {
		srv := importStubServerZeroID(t, editBodies)
		c := newImportTestClient(t, srv)
		var out, errOut strings.Builder
		code := runImport(context.Background(), c, &out, &errOut, args)
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
			t.Fatalf("stdout not valid JSON: %v\nstdout=%s", err, out.String())
		}
		return code, parsed
	}

	// --- strip-batch path (the reference behaviour) ---
	t.Run("strip_batch", func(t *testing.T) {
		code, parsed := pathOutcome(t, importArgs{
			postIDs:  "8001",
			whenType: 1,
			howType:  1,
			stripVK:  true,
		})
		if code != 0 {
			t.Fatalf("strip_batch: exit %d, want 0 (CreateNoIDError → created_no_id, not a failure)", code)
		}
		perPost, _ := parsed["per_post"].([]interface{})
		if len(perPost) != 1 {
			t.Fatalf("strip_batch: per_post len = %d, want 1", len(perPost))
		}
		rec, _ := perPost[0].(map[string]interface{})
		if got, want := rec["status"], "created_no_id"; got != want {
			t.Errorf("strip_batch: status = %v, want %q", got, want)
		}
	})

	// --- single-post path (round 1 exited 1 here) ---
	t.Run("single", func(t *testing.T) {
		code, parsed := pathOutcome(t, importArgs{
			postID:   8001,
			whenType: 1,
			howType:  1,
			stripVK:  false,
		})
		if code != 0 {
			t.Fatalf("single: exit %d, want 0 (CreateNoIDError → created_no_id, same as strip-batch; round 1 exited 1 here — the duplicate-re-run hazard)", code)
		}
		// The single path emits a bare object (not the per_post array). It
		// MUST signal created_no_id so a re-run can tell a
		// published-but-unidentified post from a real failure.
		if got, want := parsed["status"], "created_no_id"; got != want {
			t.Errorf("single: status = %v, want %q (must agree with strip-batch)", got, want)
		}
	})

	// --- batch-flag-off path (round 1 exited 1 here) ---
	t.Run("batch_off", func(t *testing.T) {
		code, parsed := pathOutcome(t, importArgs{
			postIDs:  "8001",
			whenType: 1,
			howType:  1,
			stripVK:  false,
		})
		if code != 0 {
			t.Fatalf("batch_off: exit %d, want 0 (CreateNoIDError → created_no_id, same as strip-batch; round 1 exited 1 here)", code)
		}
		// The batch path emits a per_post array (not a bare object).
		perPost, _ := parsed["per_post"].([]interface{})
		if len(perPost) != 1 {
			t.Fatalf("batch_off: per_post len = %d, want 1", len(perPost))
		}
		rec, _ := perPost[0].(map[string]interface{})
		if got, want := rec["status"], "created_no_id"; got != want {
			t.Errorf("batch_off: status = %v, want %q (must agree with strip-batch)", got, want)
		}
	})
}

// TestRunImport_BatchAllFailed_Exits1 is the CLI half of F15: a batch where
// EVERY post fails to publish exits 1 (error), NOT 2 (partial). runImport's
// own loop already distinguishes all-failed (exit 1) from partial (exit 2);
// this test pins that distinction so a future change cannot collapse them.
// The library half (resolvePublishBatch returns a plain error, not
// *PartialPostError, on all-failed) is pinned in TestF15_AllFailedBatchReturnsPlainErrorNotPartial.
//
// RED-on-revert: change runImport's `if anyFailed && !anySucceeded { return 1 }`
// to return 2 (or remove it so all-failed falls through to the partial return 2)
// → exit 2 → this test fails.
func TestRunImport_BatchAllFailed_Exits1(t *testing.T) {
	editBodies := map[int]string{
		6101: editBodyFor("post one"),
		6102: editBodyFor("post two"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/posts-search/"), "/edit")
			id := 0
			for _, ch := range idStr {
				id = id*10 + int(ch-'0')
			}
			body, ok := editBodies[id]
			if !ok {
				t.Errorf("stub: no edit body for id %d", id)
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			// Every publish fails — a total batch failure.
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"boom"}`))
		default:
			t.Errorf("stub: unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	code := runImport(context.Background(), c, &out, &errOut, importArgs{
		postIDs:  "6101,6102",
		whenType: 1,
		howType:  1,
	})
	if code != 1 {
		t.Fatalf("exit %d; want 1 (every post failed = error, NOT partial/exit 2); stderr=%s", code, errOut.String())
	}
	// stdout still carries a per_post record (the operator's record of what
	// landed — here, nothing), so a re-run can dedup. Both entries failed.
	var parsed struct {
		PerPost []struct {
			SearchPostID int    `json:"search_post_id"`
			Status       string `json:"status"`
		} `json:"per_post"`
	}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout=%s", err, out.String())
	}
	if len(parsed.PerPost) != 2 {
		t.Fatalf("per_post len = %d, want 2 (every post attempted)", len(parsed.PerPost))
	}
	for _, r := range parsed.PerPost {
		if r.Status != "failed" {
			t.Errorf("per_post[id=%d].status = %q, want \"failed\" (every post failed)", r.SearchPostID, r.Status)
		}
	}
}
