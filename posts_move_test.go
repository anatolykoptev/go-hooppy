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
// state (the when_type guard reads publication_when_type=3 from it). The
// post-move read returns the same body but with schedule_id swapped to the
// target and a publication_date the server assigned.
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
// now on the target schedule, with the server-assigned publication_date
// 15.01.2027 (a future date — the measured tail-of-queue slot for a running
// schedule).
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

// epochEditBody is a post-move edit fixture whose server-assigned
// publication_date is 01.01.1970 — the signature of a move into a STOPPED
// schedule (the server parks the post at the epoch). Used by the
// stopped-schedule warning tests.
const epochEditBody = `{
	"id":42,
	"publication_when_type":3,
	"publication_how_type":1,
	"publication_where_type":1,
	"created_by":1,
	"texts":[{"text":"old-vk","source_id":1}],
	"attachments":[{"type":"link","data":{"url":"https://example.com/a"}}],
	"selected_pages_by_source_ids":{},
	"all_pages_ids_by_source_ids":{},
	"schedule_id":55576,
	"project_id":0,
	"publication_date":{"date":"01.01.1970","hours":"00","minutes":"00"}
}`

// photosVideoEditBody is a pre-move edit fixture carrying BOTH a photo and a
// video attachment. The former PUT move path grouped these into a single
// {type:"photos"} attachment via SearchPostEditAttachments; the current
// POST /posts/batch/move path does NOT send attachments at all (the server
// preserves them), so this fixture guards that a photos+video post moves
// without the client stripping or mis-grouping them — the batch body must
// carry no attachments field.
const photosVideoEditBody = `{
	"id":42,
	"publication_when_type":3,
	"publication_how_type":1,
	"publication_where_type":1,
	"created_by":1,
	"texts":[{"text":"old-vk","source_id":1}],
	"attachments":[
		{"type":"photo","data":{"id":"p1","type":"photo","file_path":"/a.jpg"}},
		{"type":"video","data":{"id":"v1","type":"video","file_path":"/v.mp4","seconds":12}}
	],
	"selected_pages_by_source_ids":{},
	"all_pages_ids_by_source_ids":{},
	"schedule_id":7,
	"project_id":0
}`

// photosVideoMovedEditBody is the post-move read for the photos+video post:
// the photo AND video are preserved (the server kept them), the schedule is
// swapped to the target, and the server assigned a publication_date.
const photosVideoMovedEditBody = `{
	"id":42,
	"publication_when_type":3,
	"publication_how_type":1,
	"publication_where_type":1,
	"created_by":1,
	"texts":[{"text":"old-vk","source_id":1}],
	"attachments":[
		{"type":"photo","data":{"id":"p1","type":"photo","file_path":"/a.jpg"}},
		{"type":"video","data":{"id":"v1","type":"video","file_path":"/v.mp4","seconds":12}}
	],
	"selected_pages_by_source_ids":{},
	"all_pages_ids_by_source_ids":{},
	"schedule_id":55576,
	"project_id":0,
	"publication_date":{"date":"15.01.2027","hours":"12","minutes":"25"}
}`

// moveTestServer builds a httptest server that drives the MovePost flow:
// GET #1 (pre-move guard) → preBody; POST /posts/batch/move → postResp;
// GET #2 (post-move date) → postBody. It records every request method+path
// so a test can assert the move issues POST /posts/batch/move and NO PUT.
func moveTestServer(t *testing.T, preBody, postResp, postBody string,
	onPostMoveGetFail bool,
) (*httptest.Server, *[]string, *bool, *int) {
	t.Helper()
	var requests []string
	var postCalled bool
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.Write([]byte(preBody))
				return
			}
			if onPostMoveGetFail {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"unavailable"}`))
				return
			}
			w.Write([]byte(postBody))
		case r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move":
			postCalled = true
			w.Write([]byte(postResp))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	return srv, &requests, &postCalled, &getCalls
}

// TestMovePost_IssuesBatchMoveAndNoPUT is THE architectural guard: MovePost
// MUST move via POST /posts/batch/move and MUST NOT issue a full-state
// PUT /posts/{id}. Reintroducing the read-modify-write PUT (which
// round-trips the whole edit response and can wipe fields the edit response
// omits) is RED here — the recorded requests would contain a PUT and lack
// the POST.
//
// RED-on-revert: point MovePost back at UpdatePost (PUT) and putCalled stays
// false, a PUT appears in requests, and the posts_ids/schedule_id assertions
// on the POST body fail (no POST body was captured).
func TestMovePost_IssuesBatchMoveAndNoPUT(t *testing.T) {
	var postBody []byte
	var postCalled, putCalled bool
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
		case http.MethodPost:
			if r.URL.Path != "/posts/batch/move" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			postCalled = true
			postBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		case http.MethodPut:
			putCalled = true
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
		t.Fatal("resp.Success = false")
	}
	if !postCalled {
		t.Fatal("POST /posts/batch/move was never issued — MovePost must move via the batch endpoint, not the full-state PUT")
	}
	if putCalled {
		t.Fatal("PUT /posts/{id} was issued — MovePost must NOT round-trip the full edit state; the server-side batch move preserves texts/attachments and avoids the wipe class the PUT path carries")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(postBody, &body); err != nil {
		t.Fatalf("unmarshal POST body: %v (body=%s)", err, postBody)
	}
	// posts_ids is the single id as a comma-joined STRING (the batch
	// convention); schedule_id is the target.
	if got, ok := body["posts_ids"].(string); !ok || got != "42" {
		t.Errorf("POST body posts_ids = %v, want string \"42\" (the single id, comma-joined-string convention)", body["posts_ids"])
	}
	if body["schedule_id"] != float64(55576) {
		t.Errorf("POST body schedule_id = %v, want 55576 (the target)", body["schedule_id"])
	}
	// The batch move body carries ONLY posts_ids + schedule_id — no
	// texts/attachments/project_id that would overwrite the post's existing
	// content. The server preserves them when the fields are absent.
	for _, field := range []string{"texts", "attachments", "project_id", "selected_pages_by_source_ids"} {
		if _, ok := body[field]; ok {
			t.Errorf("POST body contains %q — a move must NOT send %s (it would overwrite the post's existing %s); the server keeps it when the field is absent", field, field, field)
		}
	}
	// Two GETs: pre-move when_type guard + post-move date recovery.
	if getCalls != 2 {
		t.Errorf("GET /posts/{id}/edit issued %d times, want 2 (pre-move when_type guard + post-move date recovery)", getCalls)
	}
}

// TestMovePost_ReportsNewPublicationDate is the date-reporting guard: a
// move re-slots the post to the TAIL of the target queue, and the server
// assigns the new publication_date. The batch endpoint returns just
// {"success":true}, so the new date MUST be recovered from a post-move
// GET /posts/{id}/edit. Without this read, moving into a booked schedule
// is a silent months-long delay.
//
// RED-on-revert: drop the post-move GetPostEdit read and
// res.PublicationDate is nil — the assertion fails.
func TestMovePost_ReportsNewPublicationDate(t *testing.T) {
	srv, _, _, getCalls := moveTestServer(t, scheduleDrivenEditBody, `{"success":true}`, movedEditBody, false)
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
		t.Errorf("PublicationDate.Date = %q, want \"15.01.2027\" (the measured tail-of-queue slot)", res.PublicationDate.Date)
	}
	if *getCalls != 2 {
		t.Errorf("GET /posts/{id}/edit issued %d times, want 2 (pre-move when_type guard + post-move date recovery)", *getCalls)
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

// TestMovePost_NonPositivePostID_RefusesRequest is the impossible-id guard
// (item D): an id <= 0 is accepted by the server, which fabricates a success
// entry for it. The refusal MUST happen before any request — no GET, no POST.
//
// RED-on-revert: drop the postID <= 0 guard and the test server is reached
// (a GET /posts/0/edit is issued) — the reached assertion fails.
func TestMovePost_NonPositivePostID_RefusesRequest(t *testing.T) {
	for _, postID := range []int{0, -5} {
		reached := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = true
			w.Write([]byte(`{}`))
		}))
		c := newTestClient(t, srv)
		if _, err := c.MovePost(context.Background(), postID, 55576); err == nil {
			t.Errorf("MovePost with postID=%d: expected an error, got nil — an impossible id is accepted by the server and fabricates a success entry", postID)
		}
		if reached {
			t.Errorf("MovePost with postID=%d: a request was issued before the guard errored", postID)
		}
		srv.Close()
	}
}

// TestMovePost_NonScheduleWhenType_RefusesBeforeMove is the missing
// when_type==3 guard: only a schedule-driven post (when_type=3) can be
// moved between schedules. A when_type=2 (publish-at-a-fixed-date) post is
// not schedule-bound; "moving" it is not meaningful and was previously
// indistinguishable from a real move (the old date echoed back). The refusal
// MUST happen BEFORE the move — no POST /posts/batch/move is issued.
//
// RED-on-revert: drop the when_type guard and the POST is issued (postCalled
// = true) — the assertion fails.
func TestMovePost_NonScheduleWhenType_RefusesBeforeMove(t *testing.T) {
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// when_type=2 — publish at a fixed date, NOT schedule-driven.
			w.Write([]byte(`{
				"id":42,"publication_when_type":2,"publication_how_type":1,"publication_where_type":1,
				"created_by":1,"texts":[{"text":"old","source_id":0}],"attachments":[],
				"selected_pages_by_source_ids":{"1":[10]},"all_pages_ids_by_source_ids":{"1":[10]},
				"schedule_id":0,"project_id":0
			}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			postCalled = true
			w.Write([]byte(`{"success":true}`))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	_, err := c.MovePost(context.Background(), 42, 55576)
	if err == nil {
		t.Fatal("MovePost with when_type=2: expected an error, got nil — only a schedule-driven post (when_type=3) can be moved between schedules")
	}
	if postCalled {
		t.Fatal("MovePost with when_type=2: POST /posts/batch/move was issued — the when_type guard MUST refuse BEFORE the move")
	}
	// The error MUST name the actual when_type so the caller learns why.
	if !strings.Contains(err.Error(), "publication_when_type=2") {
		t.Errorf("error does not name the actual when_type: %q", err.Error())
	}
}

// TestMovePost_SuccessFalse_IsError is the {"success":false} guard (item A):
// the transport layer treats any decodable 2xx as success and nothing
// repo-wide inspects a "success" field, so a 2xx answering {"success":false}
// is a failed move that used to exit 0 silently. MovePost MUST surface it as
// an error.
//
// RED-on-revert: drop the !resp.Success guard in BatchMovePosts and err is
// nil — the assertion fails.
func TestMovePost_SuccessFalse_IsError(t *testing.T) {
	srv, _, _, _ := moveTestServer(t, scheduleDrivenEditBody, `{"success":false}`, movedEditBody, false)
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.MovePost(context.Background(), 42, 55576); err == nil {
		t.Fatal("MovePost with {\"success\":false}: expected an error, got nil — a 2xx with success=false is a failed move, not a silent exit 0")
	}
}

// TestMovePost_EpochDate_PopulatesWarning is the stopped-schedule guard
// (item C): a move into a stopped schedule dates the post 01.01.1970 and
// used to exit 0 silently. The recovered epoch (or any past) date MUST
// populate Warning naming the likely cause (a stopped schedule).
//
// RED-on-revert: drop the moveDateWarning call and res.Warning is empty —
// the assertion fails.
func TestMovePost_EpochDate_PopulatesWarning(t *testing.T) {
	srv, _, _, _ := moveTestServer(t, scheduleDrivenEditBody, `{"success":true}`, epochEditBody, false)
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.MovePost(context.Background(), 42, 55576)
	if err != nil {
		t.Fatalf("MovePost: %v — an epoch date is a successful move into a stopped schedule, not an error", err)
	}
	if res.Warning == "" {
		t.Fatal("Warning is empty — a recovered publication_date of 01.01.1970 MUST populate Warning (the signature of a move into a stopped schedule, which parks posts at the epoch and would otherwise exit silently)")
	}
	if !strings.Contains(res.Warning, "01.01.1970") {
		t.Errorf("Warning does not name the epoch date: %q", res.Warning)
	}
	if !strings.Contains(res.Warning, "STOPPED") {
		t.Errorf("Warning does not name the likely cause (a stopped schedule): %q", res.Warning)
	}
}

// TestMovePost_PastDate_PopulatesWarning extends the stopped-schedule guard
// to ANY past date (not just the epoch): a running schedule's tail is always
// in the future, so a past recovered date means the target is stopped or
// computed no slot.
func TestMovePost_PastDate_PopulatesWarning(t *testing.T) {
	pastBody := strings.Replace(epochEditBody, "01.01.1970", "01.01.2020", 1)
	pastBody = strings.Replace(pastBody, `"hours":"00","minutes":"00"`, `"hours":"09","minutes":"30"`, 1)
	srv, _, _, _ := moveTestServer(t, scheduleDrivenEditBody, `{"success":true}`, pastBody, false)
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.MovePost(context.Background(), 42, 55576)
	if err != nil {
		t.Fatalf("MovePost: %v", err)
	}
	if res.Warning == "" {
		t.Fatal("Warning is empty — a recovered publication_date in the past MUST populate Warning (a running schedule's tail is always in the future)")
	}
}

// TestMovePost_PostMoveReadFailure_PopulatesSlotLookupError verifies the
// non-fatal date-recovery contract: if the post-move GetPostEdit fails, the
// move still succeeded (the post exists in the target schedule); the result
// carries Success=true, the target ScheduleID, a SlotLookupError naming the
// failure, and a nil PublicationDate. Aborting the whole MovePost on a
// date-read failure would hide a successful move from the caller.
func TestMovePost_PostMoveReadFailure_PopulatesSlotLookupError(t *testing.T) {
	srv, _, _, _ := moveTestServer(t, scheduleDrivenEditBody, `{"success":true}`, "", true)
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

// TestMovePost_PhotosAndVideo_PreservesViaCleanBatchBody is the photos+video
// fixture guard. The former PUT move path grouped photo+video into a single
// {type:"photos"} attachment via SearchPostEditAttachments, and no move test
// covered that grouping. The current POST /posts/batch/move path does NOT
// send attachments (the server preserves them), so this test guards that a
// photos+video post moves with a clean batch body (no attachments field,
// no PUT) — the server keeps both attachments unchanged.
//
// RED-on-revert: reintroduce the full-state PUT and a PUT is issued AND the
// POST body would carry a grouped attachments field — both assertions fail.
func TestMovePost_PhotosAndVideo_PreservesViaCleanBatchBody(t *testing.T) {
	var postBody []byte
	var postCalled, putCalled bool
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.Write([]byte(photosVideoEditBody))
			} else {
				w.Write([]byte(photosVideoMovedEditBody))
			}
		case http.MethodPost:
			if r.URL.Path != "/posts/batch/move" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			postCalled = true
			postBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
		case http.MethodPut:
			putCalled = true
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.MovePost(context.Background(), 42, 55576)
	if err != nil {
		t.Fatalf("MovePost of a photos+video post: %v", err)
	}
	if !postCalled {
		t.Fatal("POST /posts/batch/move was never issued")
	}
	if putCalled {
		t.Fatal("PUT /posts/{id} was issued — a photos+video move must NOT round-trip attachments through a full-state PUT (the server preserves them via the batch move)")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(postBody, &body); err != nil {
		t.Fatalf("unmarshal POST body: %v (body=%s)", err, postBody)
	}
	if _, ok := body["attachments"]; ok {
		t.Error("POST body contains \"attachments\" — a move must NOT send attachments (it would overwrite the post's photo+video); the server keeps them when the field is absent")
	}
	if _, ok := body["texts"]; ok {
		t.Error("POST body contains \"texts\" — a move must NOT send texts; the server keeps them when the field is absent")
	}
	if res.PublicationDate == nil {
		t.Error("PublicationDate is nil — the post-move date read must still run for a photos+video post")
	}
}

// TestBatchMovePosts_PostsIDsIsCommaJoinedString is THE red test for the
// issue #105 wire-format bug: POST /posts/batch/move takes posts_ids as a
// comma-joined STRING, not a JSON array. A JSON array makes the server
// throw ErrorException: explode(...) and return 500. This test asserts the
// wire body carries posts_ids as a JSON STRING ("1,2,3"), not a JSON array.
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
	if body["schedule_id"] != float64(55576) {
		t.Errorf("schedule_id = %v, want 55576", body["schedule_id"])
	}
}

// TestBatchMovePosts_ReportsPerPostPublicationDate verifies the per-post
// date-reporting contract: the batch endpoint returns {"success":true}
// with no per-post dates, so each post's new publication_date MUST be
// recovered from a post-move GET /posts/{id}/edit (one read per id).
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
	for _, m := range res.Moved {
		if m.ScheduleID != 55576 {
			t.Errorf("Moved[%d].ScheduleID = %d, want 55576", m.ID, m.ScheduleID)
		}
		if m.PublicationDate == nil {
			t.Errorf("Moved[%d].PublicationDate = nil — the per-post date-recovery read did not run", m.ID)
			continue
		}
		if m.PublicationDate.Date != "15.01.2027" {
			t.Errorf("Moved[%d].PublicationDate.Date = %q, want \"15.01.2027\"", m.ID, m.PublicationDate.Date)
		}
	}
	if getCalls != 3 {
		t.Errorf("post-move GET /posts/{id}/edit issued %d times, want 3 (one per id, no paged walk)", getCalls)
	}
}

// TestBatchMovePosts_PostMoveReadFailure_ContinuesAndRecordsError
// verifies the non-fatal per-post date-recovery contract: a read failure
// for one post MUST NOT abort the remaining reads.
func TestBatchMovePosts_PostMoveReadFailure_ContinuesAndRecordsError(t *testing.T) {
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet {
			getCalls++
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
		t.Fatalf("BatchMovePosts: %v — a per-post date-read failure must NOT abort the whole call", err)
	}
	if !res.Success {
		t.Fatal("Success = false, want true — the POST committed before any reads")
	}
	if len(res.Moved) != 3 {
		t.Fatalf("len(Moved) = %d, want 3", len(res.Moved))
	}
	failed := res.Moved[1]
	if failed.ID != 20 {
		t.Errorf("Moved[1].ID = %d, want 20", failed.ID)
	}
	if failed.PublicationDate != nil {
		t.Errorf("Moved[1].PublicationDate = %v, want nil", failed.PublicationDate)
	}
	if failed.SlotLookupError == "" {
		t.Error("Moved[1].SlotLookupError is empty — a failed read must populate it")
	}
	if res.Moved[0].PublicationDate == nil {
		t.Error("Moved[0].PublicationDate = nil — the first read succeeded")
	}
	if res.Moved[2].PublicationDate == nil {
		t.Error("Moved[2].PublicationDate = nil — the third read succeeded (a mid-batch read failure must NOT abort the remaining reads)")
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
		t.Fatal("BatchMovePosts with toScheduleID=0: expected an error, got nil")
	}
	if reached {
		t.Fatal("BatchMovePosts with toScheduleID=0: a request was issued before the guard errored")
	}
}

// TestBatchMovePosts_NonPositiveID_RefusesRequest is the impossible-id guard
// (item D): []int{0,-5,7} used to reach the wire as posts_ids:"0,-5,7" and
// the server fabricated three success entries. The refusal MUST happen
// before any request.
//
// RED-on-revert: drop the id <= 0 guard and the test server is reached —
// the reached assertion fails.
func TestBatchMovePosts_NonPositiveID_RefusesRequest(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.BatchMovePosts(context.Background(), []int{0, -5, 7}, 55576); err == nil {
		t.Fatal("BatchMovePosts with ids {0,-5,7}: expected an error, got nil — an impossible id is accepted by the server and fabricates a success entry")
	}
	if reached {
		t.Fatal("BatchMovePosts with ids {0,-5,7}: a request was issued before the guard errored — the id <= 0 guard MUST refuse before the wire")
	}
}

// TestBatchMovePosts_SuccessFalse_IsError is the {"success":false} guard
// (item A) for the batch path: a 2xx answering {"success":false} is a
// failed move that used to exit 0 silently. BatchMovePosts MUST surface it
// as an error.
//
// RED-on-revert: drop the !resp.Success guard and err is nil — the
// assertion fails.
func TestBatchMovePosts_SuccessFalse_IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			w.Write([]byte(`{"success":false}`))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.BatchMovePosts(context.Background(), []int{1, 2}, 55576); err == nil {
		t.Fatal("BatchMovePosts with {\"success\":false}: expected an error, got nil — a 2xx with success=false is a failed move, not a silent exit 0")
	}
}

// TestBatchMovePosts_EpochDate_PopulatesWarning is the stopped-schedule
// guard (item C) for the batch path: a recovered epoch date populates that
// entry's Warning.
//
// RED-on-revert: drop the moveDateWarning call and Moved[0].Warning is
// empty — the assertion fails.
func TestBatchMovePosts_EpochDate_PopulatesWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet {
			w.Write([]byte(epochEditBody))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	res, err := c.BatchMovePosts(context.Background(), []int{42}, 55576)
	if err != nil {
		t.Fatalf("BatchMovePosts: %v — an epoch date is a successful move into a stopped schedule, not an error", err)
	}
	if len(res.Moved) != 1 {
		t.Fatalf("len(Moved) = %d, want 1", len(res.Moved))
	}
	if res.Moved[0].Warning == "" {
		t.Fatal("Moved[0].Warning is empty — a recovered publication_date of 01.01.1970 MUST populate Warning (the stopped-schedule signature)")
	}
	if !strings.Contains(res.Moved[0].Warning, "01.01.1970") {
		t.Errorf("Warning does not name the epoch date: %q", res.Moved[0].Warning)
	}
}

// TestBatchMovePosts_PreservesTextsAndAttachmentsPerPost verifies that a
// batch move does NOT silently strip texts or attachments from the moved
// posts. The batch endpoint carries only posts_ids + schedule_id (no
// texts/attachments fields that would overwrite the posts' existing
// content); the server keeps them when the fields are absent.
func TestBatchMovePosts_PreservesTextsAndAttachmentsPerPost(t *testing.T) {
	var postBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/posts/batch/move" {
			postBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet {
			w.Write([]byte(movedEditBody))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	if _, err := c.BatchMovePosts(context.Background(), []int{42}, 55576); err != nil {
		t.Fatalf("BatchMovePosts: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(postBody, &body); err != nil {
		t.Fatalf("unmarshal POST body: %v", err)
	}
	if _, ok := body["texts"]; ok {
		t.Error("POST body contains \"texts\" — a batch move must NOT send texts; the server keeps them when the field is absent")
	}
	if _, ok := body["attachments"]; ok {
		t.Error("POST body contains \"attachments\" — a batch move must NOT send attachments; the server keeps them when the field is absent")
	}
}

// TestPostUpdatePayload_ScheduleID_NoOmitempty verifies the schedule_id
// field on postUpdatePayload (still used by UpdatePostText) is sent WITHOUT
// omitempty — a zero schedule_id must be transmitted explicitly rather than
// silently dropped. This is defence in depth against the publish-to-nothing
// hole: a future caller that bypasses the guard must not produce a
// silently-dropped schedule_id.
func TestPostUpdatePayload_ScheduleID_NoOmitempty(t *testing.T) {
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

// TestBuildPostUpdatePayload_ErrorNamesCallerIDAndOp is the item G guard:
// the fail-closed guards MUST name the caller's postID and the operation,
// NOT edit.ID (which is 0 when the live edit response omits id). A refusal
// that reads "hooppy: post 0: ..." names neither the post nor the caller.
//
// RED-on-revert: revert the guard to interpolate edit.ID and drop the op
// name, and the error contains "post 0" (not "post 42") and lacks
// "UpdatePostText" — both assertions fail.
func TestBuildPostUpdatePayload_ErrorNamesCallerIDAndOp(t *testing.T) {
	// edit.ID is 0 (the "id" key is omitted — the live response can omit it).
	edit := &PostEditResponse{
		ID:                       0,
		PublicationWhenType:      2,               // non-schedule → triggers the page-target guard
		SelectedPagesBySourceIDs: map[int][]int{}, // empty selection
	}
	_, err := buildPostUpdatePayload("UpdatePostText", 42, edit, nil, 0)
	if err == nil {
		t.Fatal("expected an error from the page-target guard (when_type=2, empty selection), got nil")
	}
	if !strings.Contains(err.Error(), "post 42") {
		t.Errorf("error does not name the caller's postID 42: %q — it MUST interpolate the caller's id, not edit.ID (which is 0 when the live response omits id)", err.Error())
	}
	if strings.Contains(err.Error(), "post 0:") {
		t.Errorf("error names \"post 0:\" — that is edit.ID, not the caller's id; the guard MUST use the caller's postID: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "UpdatePostText") {
		t.Errorf("error does not name the operation (UpdatePostText): %q — with the op name restored a refusal identifies which caller refused", err.Error())
	}
}
