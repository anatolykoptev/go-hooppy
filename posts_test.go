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
		AccountID:       100,
		PageID:          200,
		ScheduleID:      300,
		ProjectID:       400,
		Page:            2,
	})
	if err != nil {
		t.Fatalf("ListPosts: %v", err)
	}
	for _, param := range []string{"is_published=1", "publication_date=15.06.2026", "source_id=6", "account_id=100", "page_id=200", "schedule_id=300", "project_id=400", "page=2"} {
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
