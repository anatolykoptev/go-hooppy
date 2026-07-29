package main

import (
	"context"
	"encoding/json"
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
			name:      "lower-case реклама. NOT matched (Cyrillic, false-positive risk)",
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

// importStubServer serves GET /posts-search/{id}/edit and PUT /posts/import,
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
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
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
	// stderr must say it is routing through the per-post path.
	if !strings.Contains(errOut.String(), "per-post") && !strings.Contains(errOut.String(), "3 import") {
		t.Errorf("stderr does not announce per-post routing; got:\n%s", errOut.String())
	}
	// stdout must be valid JSON.
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout=%s", err, out.String())
	}
}

// TestRunImport_NoStrip_BatchUnchanged verifies that with the flag OFF, a
// batch import is exactly today's behaviour: ONE import call with an empty
// texts slice (server copies original text), no per-post routing.
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
	// Exactly ONE import call (the batch), not N.
	if got, want := cap.count(), 1; got != want {
		t.Fatalf("import call count = %d, want %d (flag off = one batch call)", got, want)
	}
	// The texts slice on the wire is empty (server copies original text).
	if got := cap.texts(0); got != "" {
		t.Errorf("import text = %q, want empty (flag off = server copies original, no text on wire)", got)
	}
	// The ids field is the comma-joined batch, not a scalar.
	if got, want := cap.ids(0), "3001,3002"; got != want {
		t.Errorf("import ids = %q, want %q (batch comma-joined)", got, want)
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

// TestRunImport_AttachmentDelivery_BothModes pins the attachment-delivery
// divergence between the two batch modes, asserted on the DECODED wire bodies
// the stub receives. A batch WITHOUT --strip-vk-markup sends NO attachments
// (the server downloads photos async from the source ids); a batch WITH the
// flag sends each post's attachments explicitly from the edit response,
// because the per-post single-id import form cannot express "let the server
// fetch them" the way the batch form does (see the runImport doc comment).
// The strip-mode stderr states this divergence so it is never hit unknowingly.
func TestRunImport_AttachmentDelivery_BothModes(t *testing.T) {
	photo := []map[string]interface{}{
		{"type": "photo", "data": map[string]interface{}{"id": 555, "url": "https://vk.com/p.jpg"}},
	}
	editBodies := map[int]string{
		4001: editBodyWithAttachments("post one", photo),
		4002: editBodyWithAttachments("post two", photo),
	}

	// --- NON-STRIP batch: no attachments on the wire (server fetches) ---
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
	if got := cap1.count(); got != 1 {
		t.Fatalf("non-strip import calls = %d, want 1 (batch)", got)
	}
	if atts := cap1.attachments(0); len(atts) != 0 {
		t.Errorf("non-strip batch wire attachments = %v, want empty (server fetches from ids)", atts)
	}
	if got, want := cap1.ids(0), "4001,4002"; got != want {
		t.Errorf("non-strip ids = %q, want %q (batch comma-joined)", got, want)
	}

	// --- STRIP batch: attachments sent explicitly per post ---
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
	// stderr must state the attachment divergence so it is never hit unknowingly.
	if !strings.Contains(err2.String(), "attachment") {
		t.Errorf("strip stderr does not mention attachment divergence; got:\n%s", err2.String())
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
