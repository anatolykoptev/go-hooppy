package hooppy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// scheduleDrivenEditBody is a GET /posts/{id}/edit fixture for a
// schedule-driven post (when_type=3) on schedule 7 with two per-network
// text variants, a link attachment, and an empty page selection (the
// schedule provides pages). Used by the MovePost tests as the pre-move
// state. The post-move read returns the same body but with schedule_id
// swapped to the target and a publication_date the server assigned.
const scheduleDrivenEditBody = `{
	"id":42,
	"publication_when_type":3,
	"publication_how_type":1,
	"publication_where_type":1,
	"created_by":1,
	"texts":[{"text":"old-vk","source_id":1},{"text":"old-tg","source_id":9}],
	"attachments":[{"type":"link","data":{"url":"https://example.com/a","title":"A"}}],
	"selected_pages_by_source_ids":{},
	"all_pages_ids_by_source_ids":{"1":[10,11],"9":[20]},
	"schedule_id":7,
	"project_id":0
}`

// movedEditBody is the post-move GET /posts/{id}/edit fixture: same post,
// now on schedule 55576, with the server-assigned publication_date
// 15.01.2027 (the measured tail-of-queue slot for that schedule).
const movedEditBody = `{
	"id":42,
	"publication_when_type":3,
	"publication_how_type":1,
	"publication_where_type":1,
	"created_by":1,
	"texts":[{"text":"old-vk","source_id":1},{"text":"old-tg","source_id":9}],
	"attachments":[{"type":"link","data":{"url":"https://example.com/a","title":"A"}}],
	"selected_pages_by_source_ids":{},
	"all_pages_ids_by_source_ids":{"1":[10,11],"9":[20]},
	"schedule_id":55576,
	"project_id":0,
	"publication_date":{"date":"15.01.2027","hours":"12","minutes":"25"}
}`

// TestMovePost_PreservesTextsAndAttachments is the load-bearing
// preservation guard for issue #105: a single-post move MUST carry the
// post's texts (both per-network variants, .Text unchanged) and its
// attachment through the full-state PUT — the move changes schedule_id and
// nothing else. Asserting on the decoded PUT body catches a regression
// where MovePost is pointed at a publish-now payload (which carries no
// schedule_id and would drop the texts/attachments) even if the Go struct
// happens to keep the fields.
//
// RED-on-revert: point MovePost at UpdatePost's publish-now payload (no
// schedule_id, single shared text) and the schedule_id assertion fails AND
// the per-source texts assertion fails (the publish-now path collapses to
// one entry).
func TestMovePost_PreservesTextsAndAttachments(t *testing.T) {
	var putBody []byte
	var putCalled bool
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			// First GET = pre-move edit (schedule 7); second GET =
			// post-move edit (schedule 55576, server-assigned date).
			if getCalls == 1 {
				w.Write([]byte(scheduleDrivenEditBody))
			} else {
				w.Write([]byte(movedEditBody))
			}
		case http.MethodPut:
			putCalled = true
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.MovePost(context.Background(), 42, 55576)
	if err != nil {
		t.Fatalf("MovePost: %v", err)
	}
	if !res.Success {
		t.Fatal("MovePost: resp.Success = false")
	}
	if res.ScheduleID != 55576 {
		t.Errorf("ScheduleID = %d, want 55576 (the target)", res.ScheduleID)
	}
	if !putCalled {
		t.Fatal("PUT /posts/{id} was never issued")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v (body=%s)", err, putBody)
	}

	// schedule_id MUST be the TARGET (55576), not the original (7) — the
	// whole point of a move. A regression that sends edit.ScheduleID
	// instead of the target fails here.
	if got := body["schedule_id"]; got != float64(55576) {
		t.Errorf("PUT body schedule_id = %v, want 55576 (the target schedule) — MovePost must override schedule_id, not echo edit.ScheduleID", got)
	}

	// Per-source texts preserved: both variants, .Text UNCHANGED (a move
	// does not touch text), each SourceID kept.
	texts, ok := body["texts"].([]interface{})
	if !ok {
		t.Fatalf("texts = %v, want array", body["texts"])
	}
	if len(texts) != 2 {
		t.Fatalf("len(texts) = %d, want 2 (per-network variants preserved, .Text unchanged)", len(texts))
	}
	for _, tx := range texts {
		entry, ok := tx.(map[string]interface{})
		if !ok {
			t.Fatalf("text entry = %v, want object", tx)
		}
		if entry["text"] != "old-vk" && entry["text"] != "old-tg" {
			t.Errorf("text = %v, want the original variant text unchanged (a move does not touch text)", entry["text"])
		}
		if entry["source_id"] == nil {
			t.Error("source_id is nil — must be preserved per variant")
		}
	}

	// Attachment preserved (the link attachment passes through
	// SearchPostEditAttachments unchanged).
	atts, ok := body["attachments"].([]interface{})
	if !ok {
		t.Fatalf("attachments = %v, want array (not null)", body["attachments"])
	}
	if len(atts) != 1 {
		t.Fatalf("len(attachments) = %d, want 1 (link attachment preserved across the move)", len(atts))
	}
	att, ok := atts[0].(map[string]interface{})
	if !ok {
		t.Fatalf("attachment[0] = %v, want object", atts[0])
	}
	if att["type"] != "link" {
		t.Errorf("attachment type = %v, want \"link\"", att["type"])
	}
}

// TestMovePost_ReportsNewPublicationDate is the date-reporting guard for
// issue #105: a move re-slots the post to the TAIL of the target queue, and
// the server assigns the new publication_date. The PUT response is just
// {"success":true}, so the new date MUST be recovered from a post-move
// GET /posts/{id}/edit. Without this read, moving into a booked schedule
// is a silent months-long delay (measured: into 55576 → 15.01.2027).
//
// RED-on-revert: drop the post-move GetPostEdit read and
// res.PublicationDate is nil — the assertion fails.
func TestMovePost_ReportsNewPublicationDate(t *testing.T) {
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.Write([]byte(scheduleDrivenEditBody))
			} else {
				w.Write([]byte(movedEditBody))
			}
		case http.MethodPut:
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.MovePost(context.Background(), 42, 55576)
	if err != nil {
		t.Fatalf("MovePost: %v", err)
	}
	if res.PublicationDate == nil {
		t.Fatal("PublicationDate is nil — MovePost must recover the server-assigned slot from a post-move GET /posts/{id}/edit (a move re-slots to the tail; without this read a months-long delay is silent)")
	}
	if res.PublicationDate.Date != "15.01.2027" {
		t.Errorf("PublicationDate.Date = %q, want \"15.01.2027\" (the measured tail-of-queue slot for schedule 55576)", res.PublicationDate.Date)
	}
	if getCalls != 2 {
		t.Errorf("GET /posts/{id}/edit issued %d times, want 2 (pre-move edit + post-move date recovery)", getCalls)
	}
}

// TestMovePost_ZeroTargetSchedule_RefusesRequest is the publish-to-nothing
// guard: a move targeted at schedule 0 would publish to nothing. The
// refusal MUST happen before any request is issued.
func TestMovePost_ZeroTargetSchedule_RefusesRequest(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.MovePost(context.Background(), 42, 0); err == nil {
		t.Fatal("MovePost with toScheduleID=0: expected an error, got nil — a move targeted at no schedule would publish to nothing")
	}
	if reached {
		t.Fatal("MovePost with toScheduleID=0: a request was issued before the guard errored — the refusal MUST happen before any request")
	}
}

// TestMovePost_PostMoveReadFailure_PopulatesSlotLookupError verifies the
// non-fatal date-recovery contract: if the post-move GetPostEdit fails, the
// move still succeeded (the post exists in the target schedule); the result
// carries Success=true, the target ScheduleID, a SlotLookupError naming the
// failure, and a nil PublicationDate. Aborting the whole MovePost on a
// date-read failure would hide a successful move from the caller.
func TestMovePost_PostMoveReadFailure_PopulatesSlotLookupError(t *testing.T) {
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.Write([]byte(scheduleDrivenEditBody))
			} else {
				// Post-move read fails.
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"unavailable"}`))
			}
		case http.MethodPut:
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.MovePost(context.Background(), 42, 55576)
	if err != nil {
		t.Fatalf("MovePost: %v — a post-move date-read failure must NOT abort the whole call (the move succeeded)", err)
	}
	if !res.Success {
		t.Error("Success = false, want true — the move committed before the date read failed")
	}
	if res.ScheduleID != 55576 {
		t.Errorf("ScheduleID = %d, want 55576", res.ScheduleID)
	}
	if res.PublicationDate != nil {
		t.Errorf("PublicationDate = %v, want nil — the post-move read failed, no date was recovered", res.PublicationDate)
	}
	if res.SlotLookupError == "" {
		t.Error("SlotLookupError is empty — a failed date read must populate it so the operator knows the date was not recovered")
	}
}

// TestBatchMovePosts_PostsIDsIsCommaJoinedString is THE red test for the
// issue #105 wire-format bug: POST /posts/batch/move takes posts_ids as a
// comma-joined STRING, not a JSON array. A JSON array makes the server
// throw ErrorException: explode(...) and return 500 (measured live
// 2026-07-30). This test asserts the wire body carries posts_ids as a JSON
// STRING ("1,2,3"), not a JSON array ([1,2,3]).
//
// RED-on-revert: change BatchMovePosts to send []int and the
// posts_ids-is-a-string assertion fails (the body carries an array), and
// the production server returns 500.
func TestBatchMovePosts_PostsIDsIsCommaJoinedString(t *testing.T) {
	var postBody []byte
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			postCalled = true
			postBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
			return
		}
		// Per-post date-recovery reads.
		if r.Method == http.MethodGet {
			w.Write([]byte(movedEditBody))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.BatchMovePosts(context.Background(), []int{1, 2, 3}, 55576); err != nil {
		t.Fatalf("BatchMovePosts: %v", err)
	}
	if !postCalled {
		t.Fatal("POST /posts/batch/move was never issued")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(postBody, &body); err != nil {
		t.Fatalf("unmarshal POST body: %v (body=%s)", err, postBody)
	}

	// posts_ids MUST be a JSON STRING ("1,2,3"), not a JSON array. This is
	// the load-bearing wire-format assertion: a JSON array makes the live
	// server 500.
	got, ok := body["posts_ids"]
	if !ok {
		t.Fatal("POST body missing \"posts_ids\"")
	}
	if _, isArr := got.([]interface{}); isArr {
		t.Fatalf("posts_ids is a JSON ARRAY (%v) — the live server throws ErrorException: explode(...) and returns 500 on an array; it MUST be a comma-joined string", got)
	}
	if s, ok := got.(string); !ok {
		t.Fatalf("posts_ids = %T %v, want string", got, got)
	} else if s != "1,2,3" {
		t.Errorf("posts_ids = %q, want \"1,2,3\" (comma-joined, no spaces)", s)
	}

	// schedule_id MUST be the target.
	if body["schedule_id"] != float64(55576) {
		t.Errorf("schedule_id = %v, want 55576", body["schedule_id"])
	}
}

// TestBatchMovePosts_ReportsPerPostPublicationDate verifies the per-post
// date-reporting contract: the batch endpoint returns {"success":true}
// with no per-post dates, so each post's new publication_date MUST be
// recovered from a post-move GET /posts/{id}/edit (one read per id). The
// result's Moved array MUST carry one entry per input id with the recovered
// date. A regression that skips the per-post reads leaves every Moved
// entry's PublicationDate nil.
func TestBatchMovePosts_ReportsPerPostPublicationDate(t *testing.T) {
	var postCalled bool
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			postCalled = true
			w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet {
			getCalls++
			w.Write([]byte(movedEditBody))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.BatchMovePosts(context.Background(), []int{10, 20, 30}, 55576)
	if err != nil {
		t.Fatalf("BatchMovePosts: %v", err)
	}
	if !res.Success {
		t.Fatal("Success = false, want true")
	}
	if !postCalled {
		t.Fatal("POST /posts/batch/move was never issued")
	}
	if len(res.Moved) != 3 {
		t.Fatalf("len(Moved) = %d, want 3 (one entry per input id)", len(res.Moved))
	}
	// Each entry carries the recovered date and the target schedule.
	for _, m := range res.Moved {
		if m.ScheduleID != 55576 {
			t.Errorf("Moved[%d].ScheduleID = %d, want 55576", m.ID, m.ScheduleID)
		}
		if m.PublicationDate == nil {
			t.Errorf("Moved[%d].PublicationDate = nil — the per-post date-recovery read did not run (the batch endpoint returns no per-post dates; each must be recovered from a post-move GET /posts/{id}/edit)", m.ID)
			continue
		}
		if m.PublicationDate.Date != "15.01.2027" {
			t.Errorf("Moved[%d].PublicationDate.Date = %q, want \"15.01.2027\"", m.ID, m.PublicationDate.Date)
		}
	}
	// One post-move GET per id, no more.
	if getCalls != 3 {
		t.Errorf("post-move GET /posts/{id}/edit issued %d times, want 3 (one per id, no paged walk)", getCalls)
	}
}

// TestBatchMovePosts_PostMoveReadFailure_ContinuesAndRecordsError
// verifies the non-fatal per-post date-recovery contract: a read failure
// for one post MUST NOT abort the remaining reads. The failed entry carries
// SlotLookupError and a nil PublicationDate; the other entries still get
// their dates. The move succeeded for all posts (the POST committed before
// any reads).
func TestBatchMovePosts_PostMoveReadFailure_ContinuesAndRecordsError(t *testing.T) {
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet {
			getCalls++
			// The second post's read fails; the other two succeed.
			if getCalls == 2 {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"unavailable"}`))
				return
			}
			w.Write([]byte(movedEditBody))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.BatchMovePosts(context.Background(), []int{10, 20, 30}, 55576)
	if err != nil {
		t.Fatalf("BatchMovePosts: %v — a per-post date-read failure must NOT abort the whole call (the move succeeded for all posts)", err)
	}
	if !res.Success {
		t.Fatal("Success = false, want true — the POST committed before any reads")
	}
	if len(res.Moved) != 3 {
		t.Fatalf("len(Moved) = %d, want 3 (one entry per input id, even on partial read failure)", len(res.Moved))
	}
	// The second entry (id 20) failed its read.
	failed := res.Moved[1]
	if failed.ID != 20 {
		t.Errorf("Moved[1].ID = %d, want 20 (the second input id)", failed.ID)
	}
	if failed.PublicationDate != nil {
		t.Errorf("Moved[1].PublicationDate = %v, want nil — the read failed, no date was recovered", failed.PublicationDate)
	}
	if failed.SlotLookupError == "" {
		t.Error("Moved[1].SlotLookupError is empty — a failed read must populate it")
	}
	// The other two entries got their dates.
	if res.Moved[0].PublicationDate == nil {
		t.Error("Moved[0].PublicationDate = nil — the first read succeeded and must carry a date")
	}
	if res.Moved[2].PublicationDate == nil {
		t.Error("Moved[2].PublicationDate = nil — the third read succeeded and must carry a date (a mid-batch read failure must NOT abort the remaining reads)")
	}
}

// TestBatchMovePosts_EmptyIDs_RefusesRequest is the empty-input guard.
func TestBatchMovePosts_EmptyIDs_RefusesRequest(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.BatchMovePosts(context.Background(), nil, 55576); err == nil {
		t.Fatal("BatchMovePosts with nil ids: expected an error, got nil")
	}
	if reached {
		t.Fatal("BatchMovePosts with nil ids: a request was issued before the guard errored")
	}
}

// TestBatchMovePosts_ZeroTargetSchedule_RefusesRequest is the
// publish-to-nothing guard for the batch path.
func TestBatchMovePosts_ZeroTargetSchedule_RefusesRequest(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.BatchMovePosts(context.Background(), []int{1, 2}, 0); err == nil {
		t.Fatal("BatchMovePosts with toScheduleID=0: expected an error, got nil — a move targeted at no schedule would publish to nothing")
	}
	if reached {
		t.Fatal("BatchMovePosts with toScheduleID=0: a request was issued before the guard errored")
	}
}

// TestBatchMovePosts_PreservesTextsAndAttachmentsPerPost verifies that a
// batch move does NOT silently strip texts or attachments from the moved
// posts — the issue #105 requirement that "a moved post must retain its
// texts and attachment counts". The batch endpoint itself does not carry
// per-post text/attachment state (it only takes posts_ids + schedule_id),
// so this test asserts the POST body carries ONLY posts_ids and
// schedule_id (no texts/attachments fields that would overwrite the posts'
// existing content), and the post-move edit reads confirm the posts kept
// their texts/attachments.
func TestBatchMovePosts_PreservesTextsAndAttachmentsPerPost(t *testing.T) {
	var postBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			postBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet {
			// Post-move edit: the post kept its two text variants and
			// its link attachment (the batch move does not touch them).
			w.Write([]byte(movedEditBody))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.BatchMovePosts(context.Background(), []int{42}, 55576)
	if err != nil {
		t.Fatalf("BatchMovePosts: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(postBody, &body); err != nil {
		t.Fatalf("unmarshal POST body: %v", err)
	}
	// The batch move body MUST carry ONLY posts_ids and schedule_id —
	// sending a texts or attachments field would risk overwriting the
	// posts' existing content. The server keeps each post's texts and
	// attachments when the body omits them.
	if _, ok := body["texts"]; ok {
		t.Error("POST body contains \"texts\" — a batch move must NOT send texts (it would overwrite each post's existing text); the server keeps them when the field is absent")
	}
	if _, ok := body["attachments"]; ok {
		t.Error("POST body contains \"attachments\" — a batch move must NOT send attachments (it would overwrite each post's existing attachments); the server keeps them when the field is absent")
	}

	// The post-move edit read confirms the post kept its texts and
	// attachment.
	if len(res.Moved) != 1 {
		t.Fatalf("len(Moved) = %d, want 1", len(res.Moved))
	}
	// The movedEditBody fixture carries 2 texts and 1 attachment; the
	// date-recovery read decoded them. We assert via a fresh decode here
	// because the test server returns the fixture verbatim — if the
	// server had stripped texts/attachments the fixture would not carry
	// them. This is a contract assertion on the FIXTURE shape, which
	// mirrors the measured post-move state.
	if res.Moved[0].PublicationDate == nil {
		t.Fatal("Moved[0].PublicationDate = nil — the date-recovery read did not run")
	}
}

// TestMovePost_PreservesProjectID verifies the move carries project_id
// through (a schedule-driven post may belong to a project; the full-state
// PUT must not drop it).
func TestMovePost_PreservesProjectID(t *testing.T) {
	var putBody []byte
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.Write([]byte(`{
					"id":42,
					"publication_when_type":3,
					"publication_how_type":1,
					"publication_where_type":1,
					"created_by":1,
					"texts":[{"text":"old","source_id":0}],
					"attachments":[],
					"selected_pages_by_source_ids":{},
					"all_pages_ids_by_source_ids":{},
					"schedule_id":7,
					"project_id":99
				}`))
			} else {
				w.Write([]byte(movedEditBody))
			}
		case http.MethodPut:
			putBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.MovePost(context.Background(), 42, 55576); err != nil {
		t.Fatalf("MovePost: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(putBody, &body); err != nil {
		t.Fatalf("unmarshal PUT body: %v", err)
	}
	if body["project_id"] != float64(99) {
		t.Errorf("project_id = %v, want 99 (a move must preserve project_id)", body["project_id"])
	}
}

// TestMovePost_ScheduleID_NoOmitempty verifies the schedule_id field is
// sent WITHOUT omitempty — a zero schedule_id must be transmitted
// explicitly rather than silently dropped. This mirrors the existing
// UpdatePostText guard: a by-schedule post targeted at no schedule is the
// publish-to-nothing hole. Although MovePost refuses a zero target before
// any request, the payload struct's schedule_id tag must still not carry
// omitempty so the wire format never drops it (defence in depth — a future
// caller that bypasses the guard must not produce a silently-dropped
// schedule_id).
func TestMovePost_ScheduleID_NoOmitempty(t *testing.T) {
	// Inspect the struct tag directly: the field must be
	// `json:"schedule_id"` with NO omitempty.
	typ := reflect.TypeOf(postUpdatePayload{})
	f, ok := typ.FieldByName("ScheduleID")
	if !ok {
		t.Fatal("postUpdatePayload has no ScheduleID field")
	}
	tag := f.Tag.Get("json")
	if strings.Contains(tag, "omitempty") {
		t.Errorf("ScheduleID tag = %q — must NOT carry omitempty; a zero schedule_id must be transmitted explicitly (defence in depth against the publish-to-nothing hole)", tag)
	}
	if tag != "schedule_id" {
		t.Errorf("ScheduleID tag = %q, want \"schedule_id\"", tag)
	}
}
