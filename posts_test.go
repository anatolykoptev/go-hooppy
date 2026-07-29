package hooppy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListPosts_NilIsPublished(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListPosts(context.Background(), ListPostsFilter{IsPublished: nil})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	// is_published should NOT be in the query string when nil.
	if contains(capturedURL, "is_published") {
		t.Errorf("URL should not contain is_published, got: %s", capturedURL)
	}
}

func TestListPosts_TrueIsPublished(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	pub := true
	_, err := c.ListPosts(context.Background(), ListPostsFilter{IsPublished: &pub})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	if !contains(capturedURL, "is_published=1") {
		t.Errorf("URL should contain is_published=1, got: %s", capturedURL)
	}
}

func TestListPosts_FalseIsPublished(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	pub := false
	_, err := c.ListPosts(context.Background(), ListPostsFilter{IsPublished: &pub})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	if !contains(capturedURL, "is_published=0") {
		t.Errorf("URL should contain is_published=0, got: %s", capturedURL)
	}
}

func TestListPosts_ZeroValuesSkipped(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListPosts(context.Background(), ListPostsFilter{
		SourceID:  0,
		AccountID: 0,
		PageID:    0,
		Page:      0,
	})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	if contains(capturedURL, "source_id") || contains(capturedURL, "account_id") || contains(capturedURL, "page_id") {
		t.Errorf("zero values should be skipped, got: %s", capturedURL)
	}
}

// TestListPosts_NegativeRejected covers issue #65 item 1: the ListPosts
// ID/page filters that are still WORKING filters (SourceID, ScheduleID,
// ProjectID, Page) were gated on `> 0` — the same silent-negative hole
// this PR closed across the search/accounts/pages filters. A negative
// took neither branch: no error, no parameter, an unfiltered result that
// looks filtered. Reachable from the shipped CLI (cmd/hooppy binds these
// with IntVar; pflag accepts negatives). The guard now rejects negatives
// before any request; zero stays the unset sentinel.
//
// AccountID and PageID ARE included here even though they are phantom
// parameters (issues #67, #73): the phantom guard fires on != 0 today,
// so a negative is refused by it — but that is a property of the CURRENT
// guard. The observable these cases assert (a negative value errors
// before any request) stays true and stays worth asserting regardless of
// which internal guard produces the refusal. They are the only thing
// that notices if the phantom guard is weakened from != 0 to > 0: a
// negative would then take neither branch — no error, no parameter, an
// unfiltered result that looks filtered — which is issue #65 item 1
// verbatim and reachable from the shipped CLI. The structural sweep in
// TestPhantomFilterSweep now also runs both signs on every phantom field
// (see its negVal arm), so this is belt-and-braces with that gate.
func TestListPosts_NegativeRejected(t *testing.T) {
	cases := []struct {
		name string
		f    ListPostsFilter
	}{
		{"SourceID negative", ListPostsFilter{SourceID: -1}},
		{"AccountID negative", ListPostsFilter{AccountID: -1}},
		{"PageID negative", ListPostsFilter{PageID: -1}},
		{"ScheduleID negative", ListPostsFilter{ScheduleID: -1}},
		{"ProjectID negative", ListPostsFilter{ProjectID: -1}},
		{"Page negative", ListPostsFilter{Page: -1}},
	}
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			_, err := c.ListPosts(context.Background(), tc.f)
			if err == nil {
				t.Fatalf("ListPosts with %s: expected an error, got nil — a negative ID/page value must be rejected before any request (issue #65 item 1)", tc.name)
			}
			if reached {
				t.Fatalf("ListPosts with %s: the guard issued a request before erroring — rejection MUST happen before any request is issued", tc.name)
			}
		})
	}
}

func TestListPosts_PublicationDate(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListPosts(context.Background(), ListPostsFilter{PublicationDate: "01.01.2026"})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	if !contains(capturedURL, "publication_date=01.01.2026") {
		t.Errorf("URL should contain publication_date, got: %s", capturedURL)
	}
}

func TestListPosts_AllFilters(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Write([]byte(`{"total_rows":0,"list":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	pub := true
	_, err := c.ListPosts(context.Background(), ListPostsFilter{
		IsPublished:     &pub,
		PublicationDate: "15.06.2026",
		SourceID:        6,
		ScheduleID:      300,
		ProjectID:       400,
		Page:            2,
	})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	// account_id and page_id are phantom on /posts (issues #67, #73) and
	// are refused — they are not on the wire. See TestPhantomFilterSweep.
	for _, param := range []string{"is_published=1", "publication_date=15.06.2026", "source_id=6", "schedule_id=300", "project_id=400", "page=2"} {
		if !contains(capturedURL, param) {
			t.Errorf("URL should contain %s, got: %s", param, capturedURL)
		}
	}
}

func TestCreatePost_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":12345,"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	resp, err := c.CreatePost(context.Background(), PostPublishNowPayload{
		PublicationWhenType: 1,
		PublicationHowType:  1,
		SelectedPagesIDs:    []int{1, 2},
		Texts:               []PostText{{Text: "hello", SourceID: 0}},
	})
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if resp.ID != 12345 {
		t.Errorf("ID = %d, want 12345", resp.ID)
	}
}

func TestDeletePost_Success(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.DeletePost(context.Background(), 42)
	if err != nil {
		t.Fatalf("DeletePost: %v", err)
	}
	if !contains(capturedPath, "/42") {
		t.Errorf("path should contain /42, got: %s", capturedPath)
	}
}

func TestBatchDeletePosts_EmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.BatchDeletePosts(context.Background(), []int{})
	if err == nil {
		t.Fatal("expected error for empty ID slice")
	}
}

func TestBatchDeletePosts_ExceedsMax(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ids := make([]int, MaxBatchDeleteIDs+1)
	for i := range ids {
		ids[i] = i + 1
	}
	_, err := c.BatchDeletePosts(context.Background(), ids)
	if err == nil {
		t.Fatal("expected error for exceeding max batch size")
	}
}

func TestBatchDeletePosts_SingleID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.BatchDeletePosts(context.Background(), []int{42})
	if err != nil {
		t.Fatalf("BatchDeletePosts: %v", err)
	}
}

func TestBatchDeletePosts_MultipleIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.BatchDeletePosts(context.Background(), []int{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatalf("BatchDeletePosts: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestUpdatePostText_ScheduleDriven_PreservesTextsAndPageSelection verifies
// that for a schedule-driven post (publication_where_type=1) UpdatePostText
// preserves per-network text variants (only swapping .Text, keeping
// SourceID) and sends back the page selection it read from the edit
// response instead of fabricating an empty selected_pages_ids list.
//
// Without the fix: the PUT body contains "selected_pages_ids":[] (a field
// absent from the edit response) and a single texts entry with SourceID 0,
// discarding the per-network variants.
func TestUpdatePostText_ScheduleDriven_PreservesTextsAndPageSelection(t *testing.T) {
	var putBody []byte
	var putCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// GET /posts/{id}/edit — schedule-driven post: where_type=1,
			// two per-network text variants, empty page selection (schedule
			// provides pages).
			w.Write([]byte(`{
				"id":42,
				"publication_when_type":3,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old-vk","source_id":1},{"text":"old-tg","source_id":9}],
				"attachments":[],
				"selected_pages_by_source_ids":{},
				"all_pages_ids_by_source_ids":{"1":[10,11],"9":[20]},
				"schedule_id":7,
				"project_id":0
			}`))
		case http.MethodPut:
			putCalled = true
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	resp, err := c.UpdatePostText(context.Background(), 42, "new text")
	if err != nil {
		t.Fatalf("UpdatePostText: %v", err)
	}
	if !resp.Success {
		t.Error("resp.Success = false, want true")
	}
	if !putCalled {
		t.Fatal("PUT /posts/{id} was never issued")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}

	// Must NOT send the non-existent selected_pages_ids field.
	if _, ok := body["selected_pages_ids"]; ok {
		t.Error("PUT body contains \"selected_pages_ids\" — this field does not exist in the edit response; page selection must be sent as selected_pages_by_source_ids")
	}
	// Must send back the page selection it read (here: empty object, schedule-driven).
	sel, ok := body["selected_pages_by_source_ids"]
	if !ok {
		t.Error("PUT body missing \"selected_pages_by_source_ids\"")
	} else if m, ok := sel.(map[string]interface{}); !ok || len(m) != 0 {
		t.Errorf("selected_pages_by_source_ids = %v, want empty object {}", sel)
	}

	// Must preserve BOTH per-network text variants, only swapping .Text,
	// keeping each entry's SourceID.
	texts, ok := body["texts"].([]interface{})
	if !ok {
		t.Fatalf("texts = %v, want array", body["texts"])
	}
	if len(texts) != 2 {
		t.Fatalf("len(texts) = %d, want 2 (per-network variants preserved)", len(texts))
	}
	for _, tx := range texts {
		entry, ok := tx.(map[string]interface{})
		if !ok {
			t.Fatalf("text entry = %v, want object", tx)
		}
		if entry["text"] != "new text" {
			t.Errorf("text = %v, want \"new text\"", entry["text"])
		}
		if entry["source_id"] == nil {
			t.Error("source_id is nil — must be preserved per variant")
		}
	}
	// Distinct SourceIDs preserved.
	srcs := map[interface{}]bool{}
	for _, tx := range texts {
		entry := tx.(map[string]interface{})
		if srcs[entry["source_id"]] {
			t.Errorf("source_id %v duplicated — variants collapsed", entry["source_id"])
		}
		srcs[entry["source_id"]] = true
	}
	if !srcs[float64(1)] || !srcs[float64(9)] {
		t.Errorf("expected source_ids {1,9} preserved, got %v", srcs)
	}

	// Schedule id must be preserved.
	if body["schedule_id"] != float64(7) {
		t.Errorf("schedule_id = %v, want 7", body["schedule_id"])
	}
}

// TestUpdatePostText_FailClosed_NoPageSelection verifies that when
// publication_where_type is NOT the verified schedule-driven value (1) and
// no page selection can be recovered from the edit response,
// UpdatePostText returns an error and issues NO PUT (refusing to publish
// to nothing rather than silently clearing page targets).
//
// Without the fix: the helper sends selected_pages_ids:[] unconditionally,
// a plausible target-wipe for a post that carries its own page targets.
func TestUpdatePostText_FailClosed_NoPageSelection(t *testing.T) {
	var putCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// where_type=2 (NOT verified schedule-driven) and no page
			// selection recoverable (selected_pages_by_source_ids absent).
			w.Write([]byte(`{
				"id":99,
				"publication_when_type":1,
				"publication_how_type":1,
				"publication_where_type":2,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[]
			}`))
		case http.MethodPut:
			putCalled = true
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.UpdatePostText(context.Background(), 99, "new text")
	if err == nil {
		t.Fatal("expected fail-closed error for where_type=2 with no recoverable page selection, got nil")
	}
	if putCalled {
		t.Fatal("PUT was issued despite fail-closed — must refuse to send a request that clears page targets")
	}
	if !contains(err.Error(), "99") {
		t.Errorf("error must name the post ID (99), got: %v", err)
	}
}

// TestUpdatePostText_NonScheduleDriven_RecoveredSelection verifies that
// when publication_where_type != 1 but a non-empty page selection IS
// recovered from the edit response, the helper sends it back verbatim
// rather than failing closed.
func TestUpdatePostText_NonScheduleDriven_RecoveredSelection(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{
				"id":77,
				"publication_when_type":1,
				"publication_how_type":1,
				"publication_where_type":2,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[],
				"selected_pages_by_source_ids":{"1":[10,11],"9":[20]}
			}`))
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.UpdatePostText(context.Background(), 77, "new text"); err != nil {
		t.Fatalf("UpdatePostText: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}
	sel, ok := body["selected_pages_by_source_ids"].(map[string]interface{})
	if !ok {
		t.Fatalf("selected_pages_by_source_ids = %v, want object", body["selected_pages_by_source_ids"])
	}
	if len(sel) != 2 {
		t.Errorf("expected 2 source groups recovered verbatim, got %d (%v)", len(sel), sel)
	}
	if _, ok := body["selected_pages_ids"]; ok {
		t.Error("must not send selected_pages_ids")
	}
}

// TestUpdatePostText_AttachmentsGrouped verifies that UpdatePostText groups
// mixed singular photo/video attachments (the vocabulary GET /posts/{id}/edit
// returns for own posts — measured: 20 photo + 1 video across 11 real posts,
// no pre-grouped "photos" type) into a SINGLE {type: "photos"} attachment in
// the PUT body, which is what the server accepts.
//
// Without SearchPostEditAttachments (e.g. if posts.go used
// `attachments := edit.Attachments`): the PUT body carries 3 separate
// {type:"photo"}/{type:"video"} entries and the server rejects them — the
// bypass test below fails.
func TestUpdatePostText_AttachmentsGrouped(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Schedule-driven own post with 2 photos + 1 video as SINGULAR
			// attachment types (the measured GET /posts/{id}/edit vocabulary).
			w.Write([]byte(`{
				"id":42,
				"publication_when_type":3,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[
					{"type":"photo","data":{"id":"p1","url":"https://example.com/1.jpg","type":"photo"}},
					{"type":"photo","data":{"id":"p2","url":"https://example.com/2.jpg","type":"photo"}},
					{"type":"video","data":{"id":"v1","url":"https://example.com/v.mp4","type":"video"}}
				],
				"selected_pages_by_source_ids":{},
				"all_pages_ids_by_source_ids":{},
				"schedule_id":7,
				"project_id":0
			}`))
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.UpdatePostText(context.Background(), 42, "new text"); err != nil {
		t.Fatalf("UpdatePostText: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}
	atts, ok := body["attachments"].([]interface{})
	if !ok {
		t.Fatalf("attachments = %v, want array", body["attachments"])
	}
	// Must be exactly ONE attachment: the grouped {type: "photos"}.
	if len(atts) != 1 {
		t.Fatalf("len(attachments) = %d, want 1 (photos+video grouped into a single {type:photos}); got %v", len(atts), atts)
	}
	grouped, ok := atts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("attachments[0] = %v, want object", atts[0])
	}
	if grouped["type"] != "photos" {
		t.Errorf("attachments[0].type = %v, want \"photos\" (grouped)", grouped["type"])
	}
	// The grouped attachment must carry all 3 data items (2 photos + 1 video).
	items, ok := grouped["data"].([]interface{})
	if !ok {
		t.Fatalf("grouped data = %v, want array", grouped["data"])
	}
	if len(items) != 3 {
		t.Errorf("grouped photos data len = %d, want 3 (2 photos + 1 video)", len(items))
	}
	// No bare "photo" or "video" type may survive in the top-level array.
	for _, a := range atts {
		if at, _ := a.(map[string]interface{})["type"].(string); at == "photo" || at == "video" {
			t.Errorf("bare %q attachment leaked into PUT body — must be grouped under {type:photos}", at)
		}
	}
}

// TestUpdatePostText_AttachmentsBypassFails is the falsification guard for
// the grouping transform: if UpdatePostText is changed to bypass
// SearchPostEditAttachments (e.g. `attachments := edit.Attachments`), this
// test fails because the PUT body would carry separate photo/video entries
// instead of one grouped {type:photos} attachment. It shares the fixture
// shape with TestUpdatePostText_AttachmentsGrouped but asserts the
// invariant from the negative direction.
func TestUpdatePostText_AttachmentsBypassFails(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{
				"id":43,
				"publication_when_type":3,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[
					{"type":"photo","data":{"id":"p1"}},
					{"type":"video","data":{"id":"v1"}}
				],
				"selected_pages_by_source_ids":{},
				"schedule_id":7,
				"project_id":0
			}`))
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.UpdatePostText(context.Background(), 43, "new text"); err != nil {
		t.Fatalf("UpdatePostText: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}
	atts, _ := body["attachments"].([]interface{})
	// The transform MUST group: a bypass yields len>1 with bare photo/video.
	if len(atts) != 1 {
		t.Fatalf("attachments bypassed: len=%d (separate photo/video entries) — SearchPostEditAttachments must group into one {type:photos}; got %v", len(atts), atts)
	}
	if at, _ := atts[0].(map[string]interface{})["type"].(string); at != "photos" {
		t.Errorf("attachments[0].type = %q, want \"photos\" (grouped)", at)
	}
}

// TestUpdatePostText_PreservesProjectID verifies that UpdatePostText sends
// project_id back in the PUT body, sourced from edit.ProjectID. A
// project-scoped post must not lose its association through the full-state
// PUT — the same class of wipe the schedule_id guard exists to prevent.
func TestUpdatePostText_PreservesProjectID(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{
				"id":55,
				"publication_when_type":3,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[],
				"selected_pages_by_source_ids":{},
				"schedule_id":7,
				"project_id":31
			}`))
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.UpdatePostText(context.Background(), 55, "new text"); err != nil {
		t.Fatalf("UpdatePostText: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}
	if body["project_id"] != float64(31) {
		t.Errorf("PUT body project_id = %v, want 31 (must preserve edit.ProjectID)", body["project_id"])
	}
	if body["schedule_id"] != float64(7) {
		t.Errorf("PUT body schedule_id = %v, want 7", body["schedule_id"])
	}
}

// TestUpdatePostText_ScheduleDriven_ZeroScheduleID_RefusesRequest verifies
// that a schedule-driven post (publication_when_type=3) whose edit response
// carries schedule_id=0 is refused BEFORE any PUT is issued — mirroring the
// create-path guard that refuses when_type=3 with an empty schedules_ids.
//
// Without the guard: the PUT body is sent with schedule_id omitted
// (omitempty elides the zero), so the server receives a by-schedule post
// targeted at no schedule — the publish-to-nothing hole.
func TestUpdatePostText_ScheduleDriven_ZeroScheduleID_RefusesRequest(t *testing.T) {
	var putCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Schedule-driven post (where_type=1, when_type=3) but the edit
			// response carries schedule_id=0 — the association was lost.
			w.Write([]byte(`{
				"id":88,
				"publication_when_type":3,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[],
				"selected_pages_by_source_ids":{},
				"schedule_id":0,
				"project_id":0
			}`))
		case http.MethodPut:
			putCalled = true
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.UpdatePostText(context.Background(), 88, "new text")
	if err == nil {
		t.Fatal("expected fail-closed error for when_type=3 with schedule_id=0, got nil")
	}
	if putCalled {
		t.Fatal("PUT was issued despite zero schedule_id — must refuse to send a request that targets no schedule")
	}
	if !contains(err.Error(), "88") {
		t.Errorf("error must name the post ID (88), got: %v", err)
	}
}

// TestUpdatePostText_ScheduleID_NoOmitempty verifies that the schedule_id
// field is serialized even when its value is ZERO — confirming the json tag
// has no omitempty. This is the falsifying case for the omitempty revert:
// a post where the schedule guard does NOT fire (publication_when_type != 3)
// but schedule_id IS zero. With omitempty the key vanishes from the PUT
// body; without it the key is present with value 0.
//
// The fixture uses a NON-EMPTY page selection so the page-target guard
// (when_type != 3 && empty selection → refuse) does NOT fire — the test's
// job is the omitempty check, not the guard. Using an empty selection here
// would hit the guard and never reach the PUT, which would make the test
// pass for the wrong reason (codifying the fail-open bypass instead of
// testing omitempty).
//
// Asserts on the DECODED body (json.Unmarshal into map[string]any) and
// checks KEY PRESENCE — "schedule_id":0 and an absent key differ only by
// presence, so a substring check on the raw bytes would not catch it.
//
// With omitempty restored (the revert): the key is absent from the decoded
// body and this test fails — the server receives a by-schedule post with
// no schedule field at all.
func TestUpdatePostText_ScheduleID_NoOmitempty(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// when_type=1 (publish now, NOT by schedule) so the schedule
			// guard does not fire. A NON-EMPTY selection so the page-target
			// guard does not fire either. schedule_id=0 — the zero that
			// omitempty would silently drop.
			w.Write([]byte(`{
				"id":42,
				"publication_when_type":1,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[],
				"selected_pages_by_source_ids":{"1":[10]},
				"schedule_id":0,
				"project_id":0
			}`))
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.UpdatePostText(context.Background(), 42, "new text"); err != nil {
		t.Fatalf("UpdatePostText: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}
	// The key must be PRESENT in the decoded body — with omitempty a zero
	// value is elided entirely and the key vanishes. Check presence, not
	// the value (0 vs absent differs only by key existence).
	if _, ok := body["schedule_id"]; !ok {
		t.Errorf("PUT body is missing the schedule_id key — omitempty elided the zero value; the field must be serialized even when 0 so the server sees an explicit schedule_id")
	}
}

// TestListAllPosts_TwoPages verifies the --all walk starts at page 1 (not 0),
// accumulates both pages, and produces no duplicate IDs. Without the fix
// (walk starting at page 0) the first page is fetched twice because page=0
// and page=1 are byte-identical on the server, yielding duplicates and a
// length that exceeds total_rows.
func TestListAllPosts_TwoPages(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "2":
			w.Write([]byte(`{"list":[{"id":3}],"total_rows":3,"is_has_more":false,"rows_limit":20}`))
		default: // page 1 (and the buggy page 0 which omits the param)
			w.Write([]byte(`{"list":[{"id":1},{"id":2}],"total_rows":3,"is_has_more":true,"rows_limit":20}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	all, err := c.ListAllPosts(context.Background(), ListPostsFilter{})
	if err != nil {
		t.Fatalf("ListAllPosts: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3 (server total_rows)", len(all))
	}
	seen := map[int]bool{}
	for _, p := range all {
		if seen[p.ID] {
			t.Errorf("duplicate post ID %d in accumulated result", p.ID)
		}
		seen[p.ID] = true
	}
	if len(pages) != 2 {
		t.Fatalf("handler received %d requests, want 2 (pages=%v)", len(pages), pages)
	}
	if pages[0] != "1" {
		t.Errorf("first request page = %q, want \"1\"", pages[0])
	}
	if pages[1] != "2" {
		t.Errorf("second request page = %q, want \"2\"", pages[1])
	}
}

// TestListAllPosts_SanityCap verifies the walk returns an error instead of
// looping forever when is_has_more never goes false.
func TestListAllPosts_SanityCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"list":[{"id":1}],"total_rows":1000000,"is_has_more":true,"rows_limit":20}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.ListAllPosts(context.Background(), ListPostsFilter{})
	if err == nil {
		t.Fatal("expected error when is_has_more never goes false, got nil")
	}
	if !contains(err.Error(), "exceeded") {
		t.Errorf("expected cap error mentioning 'exceeded', got: %v", err)
	}
}

// TestListAllPosts_DistinctPageParams is the RED-on-revert test: consecutive
// requests in the walk must emit DISTINCT page= values. If the walk start
// index goes back to 0 (the off-by-one this PR exists to fix), page 0 and
// page 1 both hit server page 1, the distinctness assertion fails.
func TestListAllPosts_DistinctPageParams(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "3":
			w.Write([]byte(`{"list":[{"id":5}],"total_rows":5,"is_has_more":false,"rows_limit":20}`))
		case "2":
			w.Write([]byte(`{"list":[{"id":3},{"id":4}],"total_rows":5,"is_has_more":true,"rows_limit":20}`))
		default: // page 1 (and the buggy page 0 which omits the param)
			w.Write([]byte(`{"list":[{"id":1},{"id":2}],"total_rows":5,"is_has_more":true,"rows_limit":20}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.ListAllPosts(context.Background(), ListPostsFilter{}); err != nil {
		t.Fatalf("ListAllPosts: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("handler received %d requests, want 3 (pages=%v)", len(pages), pages)
	}
	// Distinctness: every page= value must be distinct — a walk starting at
	// page 0 fetches page 1 twice (page=0 and page=1 both hit server page 1).
	seen := map[string]bool{}
	for _, p := range pages {
		if seen[p] {
			t.Errorf("page param %q emitted twice — double-fetch (walk start index reverted to 0?)", p)
		}
		seen[p] = true
	}
	// First request must be page=1, not page="" (the buggy page 0).
	if pages[0] != "1" {
		t.Errorf("first request page = %q, want \"1\" (walk must start at page 1, not 0)", pages[0])
	}
}

// TestListAllPosts_FilterPreservedAcrossPages verifies that EVERY non-page
// field of ListPostsFilter survives on EVERY request in the walk — a filter
// carrying a schedule id must send that schedule id on page 2 and page 3,
// not just page 1. Asserts on each captured request, not only the first.
//
// Without filter preservation (e.g. if the walk mutated the filter struct
// and zeroed non-page fields between iterations): page 2+ would fetch
// unfiltered rows, silently mixing in posts from other schedules.
func TestListAllPosts_FilterPreservedAcrossPages(t *testing.T) {
	type captured struct {
		page       string
		scheduleID string
		projectID  string
		sourceID   string
	}
	var capturedReqs []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		capturedReqs = append(capturedReqs, captured{
			page:       q.Get("page"),
			scheduleID: q.Get("schedule_id"),
			projectID:  q.Get("project_id"),
			sourceID:   q.Get("source_id"),
		})
		switch q.Get("page") {
		case "3":
			w.Write([]byte(`{"list":[{"id":5}],"total_rows":5,"is_has_more":false,"rows_limit":20}`))
		case "2":
			w.Write([]byte(`{"list":[{"id":3},{"id":4}],"total_rows":5,"is_has_more":true,"rows_limit":20}`))
		default:
			w.Write([]byte(`{"list":[{"id":1},{"id":2}],"total_rows":5,"is_has_more":true,"rows_limit":20}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	pub := true
	_, err := c.ListAllPosts(context.Background(), ListPostsFilter{
		IsPublished: &pub,
		ScheduleID:  777,
		ProjectID:   42,
		SourceID:    6,
	})
	if err != nil {
		t.Fatalf("ListAllPosts: %v", err)
	}
	if len(capturedReqs) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(capturedReqs))
	}
	// Assert on EACH request — not only the first.
	for i, req := range capturedReqs {
		if req.scheduleID != "777" {
			t.Errorf("request %d: schedule_id = %q, want \"777\" (filter must survive on every page)", i, req.scheduleID)
		}
		if req.projectID != "42" {
			t.Errorf("request %d: project_id = %q, want \"42\" (filter must survive on every page)", i, req.projectID)
		}
		if req.sourceID != "6" {
			t.Errorf("request %d: source_id = %q, want \"6\" (filter must survive on every page)", i, req.sourceID)
		}
	}
}

// TestUpdatePostText_FailClosed_NotBySchedule_EmptySelection_Refuses is the
// RED test for the guard re-key from publication_where_type to
// publication_when_type. A post that is NOT schedule-driven (when_type=1,
// publish now) but carries where_type=1 (the value the OLD guard treated as
// the schedule-driven marker) with an empty page selection must REFUSE and
// issue NO PUT — the post carries its own page targets, and sending an empty
// selection would clear them (the exact target-wipe this guard exists to
// prevent).
//
// Against the OLD guard (keyed on where_type != 1): where_type=1 → guard
// does NOT fire → PUT is issued → this test FAILS (it expects refusal).
// Against the NEW guard (keyed on when_type != 3): when_type=1 != 3 → guard
// FIRES → refuse → this test PASSES.
//
// The discriminator is when_type (3=by schedule), NOT where_type: measured
// on a live account, where_type=1 appears on both schedule-driven and
// non-schedule-driven posts alike — the field that actually separates them
// is when_type.
func TestUpdatePostText_FailClosed_NotBySchedule_EmptySelection_Refuses(t *testing.T) {
	var putCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// when_type=1 (publish now, NOT by schedule) but where_type=1
			// (the value the old guard mis-keyed on as schedule-driven).
			// Empty page selection — the post's own targets cannot be
			// recovered, so the guard must refuse.
			w.Write([]byte(`{
				"id":71,
				"publication_when_type":1,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[],
				"selected_pages_by_source_ids":{},
				"schedule_id":0,
				"project_id":0
			}`))
		case http.MethodPut:
			putCalled = true
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.UpdatePostText(context.Background(), 71, "new text")
	if err == nil {
		t.Fatal("expected fail-closed error for when_type=1 (not by schedule) with empty selection, got nil — the guard must key on when_type, not where_type")
	}
	if putCalled {
		t.Fatal("PUT was issued despite when_type != 3 and empty selection — must refuse to send a request that clears page targets")
	}
	if !contains(err.Error(), "71") {
		t.Errorf("error must name the post ID (71), got: %v", err)
	}
}

// TestUpdatePostText_NullNormalization verifies that UpdatePostText
// normalizes nil attachments and nil page selections to [] and {} (not
// null) in the PUT body — matching the three sibling writers
// (CopySearchPost, RewriteSearchPost, ImportSearchPost) which all open with
// "Server expects arrays, not null". A text-only post (zero attachments)
// with an edit response that omits the selected_pages_by_source_ids key
// yields nil values that encoding/json marshals as null; the server may
// interpret null as "clear" (UpdatePost's own doc says a wrong payload
// shape returns 500).
//
// Asserts on the DECODED body (map[string]any + presence/type), not on a
// substring — "null" vs "[]" differs in JSON type, not just bytes.
func TestUpdatePostText_NullNormalization(t *testing.T) {
	var putBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Schedule-driven post (when_type=3) with schedule_id=7 so
			// neither guard fires. No attachments, no
			// selected_pages_by_source_ids key — both decode as nil.
			w.Write([]byte(`{
				"id":42,
				"publication_when_type":3,
				"publication_how_type":1,
				"publication_where_type":1,
				"created_by":1,
				"texts":[{"text":"old","source_id":0}],
				"attachments":[],
				"schedule_id":7,
				"project_id":0
			}`))
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.UpdatePostText(context.Background(), 42, "new text"); err != nil {
		t.Fatalf("UpdatePostText: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}
	// attachments must be a JSON array ([]), not null.
	atts, ok := body["attachments"].([]interface{})
	if !ok {
		t.Fatalf("attachments = %v, want JSON array (not null) — sibling writers normalize nil to []; UpdatePostText must match", body["attachments"])
	}
	if len(atts) != 0 {
		t.Errorf("attachments len = %d, want 0 (text-only post)", len(atts))
	}
	// selected_pages_by_source_ids must be a JSON object ({}), not null.
	sel, ok := body["selected_pages_by_source_ids"].(map[string]interface{})
	if !ok {
		t.Fatalf("selected_pages_by_source_ids = %v, want JSON object (not null) — a nil map marshals as null; must be normalized to {}", body["selected_pages_by_source_ids"])
	}
	if len(sel) != 0 {
		t.Errorf("selected_pages_by_source_ids len = %d, want 0 (schedule-driven, empty selection)", len(sel))
	}
}
