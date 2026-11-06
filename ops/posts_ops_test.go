package ops

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// postsAPI is a stub of the posts delete/move endpoints. batchStatus/batchBody
// answer POST /posts/batch/delete; failSingle lists ids whose DELETE answers
// 500. It records every request.
type postsAPI struct {
	*httptest.Server
	mu          sync.Mutex
	requests    []string
	batchBody   string
	batchStatus int
	batchResp   string
	failSingle  map[string]bool
	singleCode  map[string]int // DELETE /posts/{id} answers this status when set
}

func newPostsAPI(t *testing.T) *postsAPI {
	t.Helper()
	a := &postsAPI{batchStatus: 200, batchResp: `{"success":true}`, failSingle: map[string]bool{}, singleCode: map[string]int{}}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.requests = append(a.requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/posts/batch/delete":
			b, _ := io.ReadAll(r.Body)
			a.batchBody = string(b)
			w.WriteHeader(a.batchStatus)
			w.Write([]byte(a.batchResp))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/posts/"):
			if code, ok := a.singleCode[strings.TrimPrefix(r.URL.Path, "/posts/")]; ok {
				w.WriteHeader(code)
				w.Write([]byte(`{"error":"no"}`))
				return
			}
			if a.failSingle[strings.TrimPrefix(r.URL.Path, "/posts/")] {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"no"}`))
				return
			}
			w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(a.Close)
	return a
}

// TestDeletePosts_RefusalsAreValidationErrorsBeforeAnyRequest: an empty list,
// an id <= 0, a repeated id and an over-long list are *ValidationError with
// zero requests.
func TestDeletePosts_RefusalsAreValidationErrorsBeforeAnyRequest(t *testing.T) {
	tooMany := make(IDList, hooppy.MaxBatchDeleteIDs+1)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	for name, ids := range map[string]IDList{
		"nil":       nil,
		"empty":     {},
		"zero":      {5, 0, 7},
		"negative":  {-1},
		"duplicate": {5, 6, 5},
		"too many":  tooMany,
	} {
		t.Run(name, func(t *testing.T) {
			api := newPostsAPI(t)
			res, err := DeletePosts(context.Background(), newTestClient(t, api.URL), DeletePostsInput{IDs: ids})
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v (%T), want *ValidationError", err, err)
			}
			if res != nil || len(api.requests) != 0 {
				t.Errorf("res = %v requests = %v, want nil and none", res, api.requests)
			}
		})
	}
}

// TestDeletePosts_ServerAnswerKinds: what the op does with each way the batch
// request can come back.
func TestDeletePosts_ServerAnswerKinds(t *testing.T) {
	ctx := context.Background()

	t.Run("success:true is every id deleted, one request", func(t *testing.T) {
		api := newPostsAPI(t)
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6}})
		if err != nil || !res.Success || !reflect.DeepEqual(res.Deleted, []int{5, 6}) || res.Note != "" {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		if !reflect.DeepEqual(api.requests, []string{"POST /posts/batch/delete"}) || strings.TrimSpace(api.batchBody) != `{"ids":"5,6"}` {
			t.Errorf("requests = %v body = %s", api.requests, api.batchBody)
		}
	})

	t.Run("a refused batch whose ids all delete singly is a success with a note", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6}})
		if err != nil || !res.Success || !reflect.DeepEqual(res.Deleted, []int{5, 6}) || res.Note == "" {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})

	t.Run("2xx without success:true is treated as refused", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchResp = `{}`
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6}})
		if err != nil || !reflect.DeepEqual(res.Deleted, []int{5, 6}) || res.Note == "" {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		if len(api.requests) != 3 {
			t.Errorf("requests = %v, want the batch POST then one DELETE per id", api.requests)
		}
	})

	t.Run("explicit success:false is treated as refused", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchResp = `{"success":false,"message":"nope"}`
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6}})
		if err != nil || !reflect.DeepEqual(res.Deleted, []int{5, 6}) {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})

	t.Run("some fail: *PartialDeleteError with the record", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
		api.failSingle["6"] = true
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6, 7}})
		var pde *PartialDeleteError
		if !errors.As(err, &pde) || pde.Result != res {
			t.Fatalf("err = %v, want *PartialDeleteError carrying the result", err)
		}
		if res.Success || !reflect.DeepEqual(res.Deleted, []int{5, 7}) || len(res.Failed) != 1 || res.Failed[0].ID != 6 {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("all fail: a plain error, the record kept", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
		api.failSingle["5"], api.failSingle["6"] = true, true
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6}})
		var pde *PartialDeleteError
		if err == nil || errors.As(err, &pde) || res == nil || len(res.Failed) != 2 || len(res.Deleted) != 0 {
			t.Fatalf("res = %+v err = %v, want every id failed and a non-partial error", res, err)
		}
	})

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status)+" is not repeated per id", func(t *testing.T) {
			api := newPostsAPI(t)
			api.batchStatus, api.batchResp = status, `{"error":"no"}`
			_, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6}})
			if err == nil || len(api.requests) != 1 {
				t.Fatalf("err = %v requests = %v, want an error after the single batch request", err, api.requests)
			}
		})
	}

	t.Run("a rate limit mid-run stops the per-id loop", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
		api.singleCode["6"] = http.StatusTooManyRequests
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6, 7, 8}})
		var pde *PartialDeleteError
		if !errors.As(err, &pde) || !reflect.DeepEqual(res.Deleted, []int{5}) || len(res.Failed) != 3 {
			t.Fatalf("res = %+v err = %v, want 5 deleted and 6, 7, 8 failed", res, err)
		}
		if !strings.Contains(res.Failed[1].Error, "not attempted") || !strings.Contains(res.Failed[2].Error, "not attempted") {
			t.Errorf("ids after the 429 must be marked not attempted: %+v", res.Failed)
		}
		if got := api.requests[len(api.requests)-1]; got != "DELETE /posts/6" {
			t.Errorf("last request = %s, want DELETE /posts/6 (no DELETE for 7 or 8)", got)
		}
	})

	t.Run("no server answer: unknown outcome, nothing repeated", func(t *testing.T) {
		api := newPostsAPI(t)
		c := newTestClient(t, api.URL)
		api.Close()
		res, err := DeletePosts(ctx, c, DeletePostsInput{IDs: IDList{5, 6}})
		if err == nil || res != nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("res = %v err = %v, want an unknown-outcome error and no record", res, err)
		}
	})

	t.Run("single id: DELETE, the error verbatim on failure", func(t *testing.T) {
		api := newPostsAPI(t)
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{9}})
		if err != nil || !res.Success || !reflect.DeepEqual(api.requests, []string{"DELETE /posts/9"}) {
			t.Fatalf("res = %+v err = %v requests = %v", res, err, api.requests)
		}
		api.failSingle["9"] = true
		res, err = DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{9}})
		var ae *hooppy.APIError
		if !errors.As(err, &ae) || res == nil || len(res.Failed) != 1 {
			t.Fatalf("res = %+v err = %v, want the API error and a failed record", res, err)
		}
	})
}

// deletes returns the ids of the DELETE /posts/{id} requests the stub saw.
func (a *postsAPI) deletes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, r := range a.requests {
		if strings.HasPrefix(r, "DELETE /posts/") {
			out = append(out, strings.TrimPrefix(r, "DELETE /posts/"))
		}
	}
	return out
}

// TestDeletePosts_FallbackStopsAfterConsecutiveServerErrors: once the batch is
// refused, three 5xx in a row end the one-by-one run; the ids after them are
// marked "not attempted" and no DELETE is sent for them.
//
// RED-on-revert: bulk_stop.go BulkStop.Observe, make the 5xx branch never stop
// — all six ids are sent (deletes() has 6 entries) and nothing is "not
// attempted".
func TestDeletePosts_FallbackStopsAfterConsecutiveServerErrors(t *testing.T) {
	api := newPostsAPI(t)
	api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
	for _, id := range []string{"5", "6", "7"} {
		api.failSingle[id] = true
	}
	res, err := DeletePosts(context.Background(), newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6, 7, 8, 9, 10}})
	if err == nil || res == nil {
		t.Fatalf("res = %+v err = %v, want an error with the record", res, err)
	}
	if got := api.deletes(); !reflect.DeepEqual(got, []string{"5", "6", "7"}) {
		t.Fatalf("DELETEs = %v, want [5 6 7] — the run must stop after 3 consecutive 5xx", got)
	}
	if len(res.Failed) != 6 || len(res.Deleted) != 0 {
		t.Fatalf("res = %+v, want 6 failed", res)
	}
	for _, f := range res.Failed[3:] {
		if !strings.Contains(f.Error, "not attempted") || !strings.Contains(f.Error, "consecutive server errors") {
			t.Errorf("id %d: %q, want 'not attempted' with the reason", f.ID, f.Error)
		}
	}
}

// TestDeletePosts_FallbackServerErrorsMustBeConsecutive: 5xx separated by a
// success do not add up — every id is attempted.
//
// RED-on-revert: bulk_stop.go BulkStop.Observe, drop the `b.serverErrors = 0`
// in the nil branch — the third 5xx (id 9) trips the stop and 10 is not sent.
func TestDeletePosts_FallbackServerErrorsMustBeConsecutive(t *testing.T) {
	api := newPostsAPI(t)
	api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
	for _, id := range []string{"5", "7", "9"} {
		api.failSingle[id] = true
	}
	res, _ := DeletePosts(context.Background(), newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6, 7, 8, 9, 10}})
	if got := api.deletes(); len(got) != 6 {
		t.Fatalf("DELETEs = %v, want all 6 attempted", got)
	}
	if !reflect.DeepEqual(res.Deleted, []int{6, 8, 10}) {
		t.Errorf("Deleted = %v, want [6 8 10]", res.Deleted)
	}
}

// TestDeletePosts_FallbackNotFoundIsAlreadyGone: after a refused batch an id the
// server no longer knows (404) was most likely removed by that batch: it is
// AlreadyGone and counts as success. The single-id form has no batch before it,
// so there a 404 stays a failure.
//
// RED-on-revert: posts_delete.go, remove the `case hooppy.IsNotFound(err):`
// arm — id 6 lands in Failed, Success is false and the error is non-nil.
func TestDeletePosts_FallbackNotFoundIsAlreadyGone(t *testing.T) {
	ctx := context.Background()
	t.Run("batch fallback", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
		api.singleCode["6"] = http.StatusNotFound
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6, 7}})
		if err != nil || !res.Success {
			t.Fatalf("res = %+v err = %v, want success: a 404 after a refused batch is already gone", res, err)
		}
		if !reflect.DeepEqual(res.Deleted, []int{5, 7}) || !reflect.DeepEqual(res.AlreadyGone, []int{6}) || len(res.Failed) != 0 {
			t.Errorf("res = %+v, want deleted [5 7], already_gone [6]", res)
		}
	})
	t.Run("a real failure beside it is still partial", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
		api.singleCode["6"] = http.StatusNotFound
		api.failSingle["7"] = true
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6, 7}})
		var pde *PartialDeleteError
		if !errors.As(err, &pde) || !reflect.DeepEqual(res.AlreadyGone, []int{6}) || len(res.Failed) != 1 || res.Failed[0].ID != 7 {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
	t.Run("every id gone is not 'every post failed'", func(t *testing.T) {
		api := newPostsAPI(t)
		api.batchStatus, api.batchResp = 500, `{"error":"boom"}`
		api.singleCode["5"], api.singleCode["6"] = http.StatusNotFound, http.StatusNotFound
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{5, 6}})
		if err != nil || !res.Success || len(res.AlreadyGone) != 2 || len(res.Deleted) != 0 {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
	t.Run("single id: a 404 stays a failure", func(t *testing.T) {
		api := newPostsAPI(t)
		api.singleCode["9"] = http.StatusNotFound
		res, err := DeletePosts(ctx, newTestClient(t, api.URL), DeletePostsInput{IDs: IDList{9}})
		var ae *hooppy.APIError
		if !errors.As(err, &ae) || ae.StatusCode != http.StatusNotFound || res.Success || len(res.Failed) != 1 || len(res.AlreadyGone) != 0 {
			t.Fatalf("res = %+v err = %v, want the 404 as a failure", res, err)
		}
	})
}

// TestMovePosts_RefusalsAreValidationErrorsBeforeAnyRequest: ids and target
// are checked locally, with zero requests.
func TestMovePosts_RefusalsAreValidationErrorsBeforeAnyRequest(t *testing.T) {
	for name, in := range map[string]MovePostsInput{
		"no ids":          {ToScheduleID: 7},
		"id 0":            {IDs: IDList{5, 0}, ToScheduleID: 7},
		"target 0":        {IDs: IDList{5}},
		"target negative": {IDs: IDList{5}, ToScheduleID: -1},
	} {
		t.Run(name, func(t *testing.T) {
			api := newPostsAPI(t)
			_, err := MovePosts(context.Background(), newTestClient(t, api.URL), in)
			var ve *ValidationError
			if !errors.As(err, &ve) || len(api.requests) != 0 {
				t.Fatalf("err = %v (%T) requests = %v, want *ValidationError and none", err, err, api.requests)
			}
		})
	}
	api := newPostsAPI(t)
	_, err := MovePosts(context.Background(), newTestClient(t, api.URL), MovePostsInput{IDs: IDList{5}})
	if err == nil || !strings.Contains(err.Error(), "--to-schedule") {
		t.Errorf("target refusal = %v, want it to name --to-schedule", err)
	}
}

func TestGetPostEdit_RefusesNonPositiveID(t *testing.T) {
	for _, id := range []int{0, -4} {
		api := newPostsAPI(t)
		_, err := GetPostEdit(context.Background(), newTestClient(t, api.URL), id, GetPostEditInput{})
		var ve *ValidationError
		if !errors.As(err, &ve) || len(api.requests) != 0 {
			t.Errorf("id %d: err = %v requests = %v, want *ValidationError and none", id, err, api.requests)
		}
	}
}
