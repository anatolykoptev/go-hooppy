package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
)

// --- runImport integration tests with httptest ---

// impIn builds the ops.ImportSearchPostInput for a runImport test call —
// the test-side analogue of what FillOpInput produces from parsed flags:
// every field the test "passes" is a non-nil pointer (the flag was given);
// fields left out are nil (not given).
func impIn(postID int, postIDs []int, whenType, howType int, stripVK bool) ops.ImportSearchPostInput {
	in := ops.ImportSearchPostInput{
		PublishSearchTarget: ops.PublishSearchTarget{
			PublicationWhenType: &whenType,
			PublicationHowType:  &howType,
		},
		StripVKMarkup: &stripVK,
	}
	if postID != 0 {
		in.SearchPostID = &postID
	}
	if len(postIDs) > 0 {
		ids := ops.IDList(postIDs)
		in.SearchPostIDs = &ids
	}
	return in
}

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
	code := runImport(context.Background(), c, &out, &errOut, impIn(1001, nil, 1, 1, false))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(0, []int{3001, 3002, 3003}, 1, 1, true))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(0, []int{4001, 4002, 4003}, 1, 1, false))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(0, []int{3001, 3002}, 1, 1, false))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(1001, nil, 1, 1, true))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(1001, nil, 1, 1, true))
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
	code := runImport(context.Background(), c1, &out1, &err1, impIn(0, []int{4001, 4002}, 1, 1, false))
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
	code = runImport(context.Background(), c2, &out2, &err2, impIn(0, []int{4001, 4002}, 1, 1, true))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(1001, nil, 1, 1, false))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(0, []int{5001, 5002, 5003, 5004, 5005}, 1, 1, true))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(0, []int{5101, 5102, 5103, 5104, 5105}, 1, 1, true))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(0, []int{6001, 6002, 6003}, 1, 1, false))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(0, []int{7001}, 1, 1, true))
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
	pathOutcome := func(t *testing.T, args ops.ImportSearchPostInput) (int, map[string]interface{}) {
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
		code, parsed := pathOutcome(t, impIn(0, []int{8001}, 1, 1, true))
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
		code, parsed := pathOutcome(t, impIn(8001, nil, 1, 1, false))
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
		code, parsed := pathOutcome(t, impIn(0, []int{8001}, 1, 1, false))
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
	code := runImport(context.Background(), c, &out, &errOut, impIn(0, []int{6101, 6102}, 1, 1, false))
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

// The third single-post runner joins F24's agreement. runImport's arm was the
// odd one out on the two details reportCreateNoID added — no stderr warning,
// and a discarded encode error — while being the command most likely reached
// from a batch workflow, so it was the one saying least.
//
// RED-on-revert: inline the old arm back (bare enc.Encode + return 0) and the
// stderr assertion fails.
func TestRunImport_SinglePost_CreateNoID_MatchesSiblings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts" {
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"id":"7001","texts":[{"text":"t"}],"attachments":[]}`))
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runImport(context.Background(), newStubClient(t, srv), &out, &errOut,
		impIn(7001, nil, 1, 1, false))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "created_no_id") {
		t.Errorf("stdout must carry the created_no_id record, got:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "no id") {
		t.Errorf("stderr must warn the post cannot be addressed — this arm was silent while its two siblings warned, got:\n%s", errOut.String())
	}
}

// ---------------------------------------------------------------------------
// F1/F2/F3 — the task-spec journey tests at the CLI adapter level.
// Each mutation below is a FILE:LINE edit inside ops.resolveSearchTarget or
// the op's target construction; the CLI runner and the MCP tool-call test of
// the same name go RED together because the op is the shared guard.
// ---------------------------------------------------------------------------

// countingImportStub is a stub that fails on ANY request — for refusal tests
// the assertion is that nothing was even attempted (a resolve GET included).
func countingImportStub(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.Write([]byte(`{"id":5001}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

// F1 — `hooppy search import --when-type 3 --to <page>`: the journey. Under
// when-type 3 the schedule owns its page set, so --to was silently dropped
// and the post published to the schedule's pages instead (issue #139). The
// op refuses it BEFORE any HTTP call; the runner maps the refusal to exit 1.
//
// Mutation: delete the `whenType == 3 && len(pages) > 0` check in
// ops/publish_search.go (resolveSearchTarget) → the op proceeds to resolve →
// reqCount > 0 → this test AND TestF1_MCP_ImportPagesUnderWhenType3
// (cmd/hooppy-mcp) both go RED.
func TestF1_CLI_ImportPagesUnderWhenType3(t *testing.T) {
	srv, count := countingImportStub(t)
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	in := impIn(2001, nil, 3, 2, false)
	in.SchedulesIDs = pids(10)
	in.SelectedPagesIDs = pids(99)
	code := runImport(context.Background(), c, &out, &errOut, in)

	if code != 1 {
		t.Fatalf("exit = %d, want %d (local validation refusal — exit 1 like every sibling ops adapter) — --to with --when-type 3 must be refused (issue #139). stderr: %s", code, 1, errOut.String())
	}
	if !strings.Contains(errOut.String(), "--to") || !strings.Contains(errOut.String(), "when-type 3") {
		t.Errorf("stderr should name the flag and the when-type, got: %s", errOut.String())
	}
	if got := atomic.LoadInt32(count); got != 0 {
		t.Fatalf("reqCount = %d, want 0 — the refusal must fire BEFORE any request (a request means the post published to the wrong target, issue #139)", got)
	}
}

// F2 — date flags under when-type 1 and 3: `hooppy search import
// --when-type 1 --date 01.01.2027 --to <page>` published NOW, discarding the
// caller's date (issue #140); the when-type-3 shape queued the post on the
// schedule's slots for the same reason. Both must refuse before any request.
//
// Mutation: delete the `whenType != 2 && len(dateFlags) > 0` check in
// ops/publish_search.go (resolveSearchTarget) → the op proceeds → reqCount >
// 0 → this test AND TestF2_MCP_ImportDateUnderWhenType1And3 both go RED.
func TestF2_CLI_ImportDateUnderWhenType1And3(t *testing.T) {
	cases := []struct {
		name  string
		build func() ops.ImportSearchPostInput
	}{
		{"date under when-type 1", func() ops.ImportSearchPostInput {
			in := impIn(2001, nil, 1, 2, false)
			in.SelectedPagesIDs = pids(123)
			d := "01.01.2027"
			in.PublishDate = &d
			return in
		}},
		{"date under when-type 3", func() ops.ImportSearchPostInput {
			in := impIn(2001, nil, 3, 2, false)
			in.SchedulesIDs = pids(10)
			d, h := "01.01.2027", "10"
			in.PublishDate, in.PublishHours = &d, &h
			return in
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, count := countingImportStub(t)
			c := newImportTestClient(t, srv)

			var out, errOut strings.Builder
			code := runImport(context.Background(), c, &out, &errOut, tc.build())

			if code != 1 {
				t.Fatalf("exit = %d, want %d (local validation refusal — exit 1 like every sibling ops adapter) — date flags under this when-type must be refused (issue #140). stderr: %s", code, 1, errOut.String())
			}
			if !strings.Contains(errOut.String(), "--date") && !strings.Contains(errOut.String(), "--hours") {
				t.Errorf("stderr should name the date flags, got: %s", errOut.String())
			}
			if got := atomic.LoadInt32(count); got != 0 {
				t.Fatalf("reqCount = %d, want 0 — the refusal must fire BEFORE any request (issue #140)", got)
			}
		})
	}
}

// F3 — `hooppy search import --when-type 2 --date 01.02.2027 --hours 10
// --minutes 30 --to 99` produces THE SAME request body as the MCP tool with
// the equivalent input. The op builds the body, so the assertion is
// byte-equality against one literal on both sides: this test asserts it from
// the CLI runner's recorded POST, TestF3_MCP_ImportBodyMatchesCLI
// (cmd/hooppy-mcp) and TestF3_ImportWhenType2_DatePages_GoldenBody (ops)
// assert the identical literal.
//
// Mutation: drop publication_date from the op's target (case 2 branch in
// resolveSearchTarget) → all three go RED.
func TestF3_CLI_ImportBodyMatchesMCPTool(t *testing.T) {
	// Author-written expectation, derived from openapi.yaml POST /posts
	// (publication_how_type, line 2020-2023): direct pages (selected_pages_ids)
	// under when-type 2 is how_type 1; 2 would need a project_id. Not a
	// recorded server exchange. Same literal as ops.goldenImportWhenType2Body.
	const golden = `{"as_copy":1,"publication_when_type":2,"publication_how_type":1,"publication_where_type":1,"selected_pages_ids":[99],"schedules_ids":[],"publication_date":{"date":"01.02.2027","hours":"10","minutes":"30"},"texts":[{"text":"x","source_id":0}],"attachments":[],"ids":"1001"}`

	srv, cap := importStubServer(t, map[int]string{1001: editBodyFor("x")})
	c := newImportTestClient(t, srv)

	var out, errOut strings.Builder
	in := impIn(1001, nil, 2, 2, false)
	in.PublicationHowType = nil // omitted: the op derives it from the target
	in.SelectedPagesIDs = pids(99)
	d, h, m := "01.02.2027", "10", "30"
	in.PublishDate, in.PublishHours, in.PublishMinutes = &d, &h, &m
	code := runImport(context.Background(), c, &out, &errOut, in)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a valid when-type-2 import. stderr: %s", code, errOut.String())
	}
	if len(cap.rawJSON) != 1 {
		t.Fatalf("POST count = %d, want 1", len(cap.rawJSON))
	}
	// The client's request encoder appends one trailing "\n" to every body
	// (client.go json.NewEncoder().Encode) — identical framing on both
	// surfaces, so the document-level comparison trims exactly that one.
	if got := strings.TrimSuffix(cap.rawJSON[0], "\n"); got != golden {
		t.Errorf("CLI POST /posts body =\n%s\nwant byte-equal to the MCP tool's body\n%s\n— both surfaces emit the identical body because the op builds it (F3)", got, golden)
	}
}
