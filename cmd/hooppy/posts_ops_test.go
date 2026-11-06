package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
)

// deleteStub is a Hooppy API stub for the delete flow. batch is the status and
// body POST /posts/batch/delete answers; single maps a post id to the status
// DELETE /posts/{id} answers (default 200 {"success":true}). It records every
// request so a test can assert what did, and did not, reach the server.
type deleteStub struct {
	*httptest.Server
	mu        sync.Mutex
	requests  []string
	batchBody string
}

func newDeleteStub(t *testing.T, batchStatus int, batchBody string, singleStatus map[string]int) *deleteStub {
	t.Helper()
	st := &deleteStub{}
	st.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.requests = append(st.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/posts/batch/delete":
			var buf bytes.Buffer
			buf.ReadFrom(r.Body)
			st.batchBody = buf.String()
			w.WriteHeader(batchStatus)
			w.Write([]byte(batchBody))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/posts/"):
			id := strings.TrimPrefix(r.URL.Path, "/posts/")
			if code, ok := singleStatus[id]; ok && code != 200 {
				w.WriteHeader(code)
				w.Write([]byte(`{"error":"cannot delete post ` + id + `"}`))
				return
			}
			w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return st
}

// deleteCmdInput drives the real `posts delete` command through flag parsing
// and the positional/--ids resolution, the way the cobra Run closure does
// (Run itself calls os.Exit and cannot be executed in-process).
func deleteCmdInput(t *testing.T, args ...string) (ops.DeletePostsInput, error) {
	t.Helper()
	cmd := resolveCLIPath(buildRootCommand(), "posts delete")
	if cmd == nil {
		t.Fatal("`posts delete` is not registered")
	}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags %v: %v", args, err)
	}
	return deleteInputFromCmd(cmd, cmd.Flags().Args())
}

// TestPostsDelete_F1_InvalidIDsRefusedBeforeAnyRequest is F1 on the CLI:
// `posts delete --ids 5,0,7` (an impossible id, which the server accepts and
// answers with a fabricated success), a negative id and a repeated id are all
// refused with exit 1 and ZERO HTTP requests; the refusal is the op's own
// ValidationError (the "hooppy: posts delete:" form), not the library's.
//
// Mutation: drop the `id <= 0` check in validatePostIDs (ops/post_ids.go) and
// the refusal comes from the library instead ("error: hooppy:
// BatchDeletePosts…") — the stderr prefix assertion goes RED.
func TestPostsDelete_F1_InvalidIDsRefusedBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"zero in the list", []string{"--ids", "5,0,7"}, "positive integer (got 0)"},
		{"negative in the list", []string{"--ids", "5,-3"}, "positive integer (got -3)"},
		{"single zero positional", []string{"0"}, "positive integer (got 0)"},
		{"repeated id", []string{"--ids", "5,6,5"}, "listed more than once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newDeleteStub(t, 200, `{"success":true}`, nil)
			defer st.Close()
			in, err := deleteCmdInput(t, tc.args...)
			if err != nil {
				t.Fatalf("deleteInputFromCmd: %v", err)
			}
			var out, errOut bytes.Buffer
			code := runDeletePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, in)
			if code != 1 {
				t.Fatalf("exit = %d, want 1; stderr: %s", code, errOut.String())
			}
			if len(st.requests) != 0 {
				t.Fatalf("%d request(s) reached the server %v — the refusal must fire before any request", len(st.requests), st.requests)
			}
			if !strings.HasPrefix(errOut.String(), "hooppy: posts delete: ") || !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("stderr = %q, want the op's refusal %q", errOut.String(), tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("stdout must stay empty on a refusal, got %q", out.String())
			}
		})
	}
}

// TestPostsDelete_CompleteBatchAndSingle: a clean batch is one POST with ids
// as a comma-joined STRING (openapi.yaml POST /posts/batch/delete) and exit 0
// with every id under `deleted`; a single positional id is one DELETE.
func TestPostsDelete_CompleteBatchAndSingle(t *testing.T) {
	st := newDeleteStub(t, 200, `{"success":true}`, nil)
	defer st.Close()
	in, err := deleteCmdInput(t, "--ids", "5,6,7")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runDeletePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, in); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	if !reflect.DeepEqual(st.requests, []string{"POST /posts/batch/delete"}) || strings.TrimSpace(st.batchBody) != `{"ids":"5,6,7"}` {
		t.Errorf("requests = %v body = %s, want one POST with the ids as a comma-joined string", st.requests, st.batchBody)
	}
	var res ops.DeletePostsResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || !res.Success || !reflect.DeepEqual(res.Deleted, []int{5, 6, 7}) || len(res.Failed) != 0 {
		t.Errorf("stdout = %s (err %v), want success with deleted [5 6 7]", out.String(), err)
	}

	st2 := newDeleteStub(t, 200, `{"success":true}`, nil)
	defer st2.Close()
	in, err = deleteCmdInput(t, "9")
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runDeletePosts(context.Background(), newCLITestClient(t, st2.Server), &out, &errOut, in); code != 0 {
		t.Fatalf("single exit = %d, want 0", code)
	}
	if !reflect.DeepEqual(st2.requests, []string{"DELETE /posts/9"}) {
		t.Errorf("single-id requests = %v, want one DELETE /posts/9", st2.requests)
	}
	if !strings.Contains(out.String(), `"success": true`) {
		t.Errorf("single-id stdout must keep the {\"success\": true} field: %s", out.String())
	}
}

// TestPostsDelete_F2_PartialBatchExits2WithPerIDReport is F2 on the CLI: the
// server refuses the batch request and, id by id, fails post 6 — the others
// must be reported as deleted, post 6 as failed with the server's error, and
// the exit code must be 2 (partial), not 0.
//
// Mutation: in DeletePosts (ops/posts_delete.go) return `res, nil` instead of
// `res, &PartialDeleteError{Result: res}` — a partial result is treated as
// complete and the exit code is 0.
func TestPostsDelete_F2_PartialBatchExits2WithPerIDReport(t *testing.T) {
	st := newDeleteStub(t, 500, `{"error":"boom"}`, map[string]int{"6": 500})
	defer st.Close()
	in, err := deleteCmdInput(t, "--ids", "5,6,7")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := runDeletePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, in)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (partial); stdout: %s stderr: %s", code, out.String(), errOut.String())
	}
	var res ops.DeletePostsResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not the per-id record: %v: %s", err, out.String())
	}
	if res.Success || !reflect.DeepEqual(res.Deleted, []int{5, 7}) || len(res.Failed) != 1 || res.Failed[0].ID != 6 || !strings.Contains(res.Failed[0].Error, "cannot delete post 6") {
		t.Errorf("record = %+v, want deleted [5 7] and failed [6: cannot delete post 6]", res)
	}
	if res.Note == "" {
		t.Error("the record must say the batch request was refused and the ids were deleted one by one")
	}
	if !strings.Contains(errOut.String(), "partial") {
		t.Errorf("stderr must say partial: %s", errOut.String())
	}
}

// TestPostsDelete_EveryIDFailed_Exits1: nothing deleted is an error (1), not a
// partial (2).
func TestPostsDelete_EveryIDFailed_Exits1(t *testing.T) {
	st := newDeleteStub(t, 500, `{"error":"boom"}`, map[string]int{"5": 500, "6": 500})
	defer st.Close()
	in, _ := deleteCmdInput(t, "--ids", "5,6")
	var out, errOut bytes.Buffer
	if code := runDeletePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, in); code != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", code, errOut.String())
	}
	var res ops.DeletePostsResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Failed) != 2 {
		t.Errorf("stdout must carry the per-id record of the failures (as the MCP tool does): %q (err %v)", out.String(), err)
	}
}

// TestPostsDelete_AlreadyGoneAfterRefusedBatchExits0: the server refuses the
// batch but removed post 6 before it errored, so the one-by-one run gets a 404
// for it. That is success (exit 0) with the id under already_gone. The same 404
// on the single-id form is a failure (exit 1) and still prints the record.
//
// Mutation: ops/posts_delete.go, remove the `case hooppy.IsNotFound(err):` arm
// — the batch case exits 2 instead of 0.
func TestPostsDelete_AlreadyGoneAfterRefusedBatchExits0(t *testing.T) {
	st := newDeleteStub(t, 500, `{"error":"boom"}`, map[string]int{"6": 404})
	defer st.Close()
	in, _ := deleteCmdInput(t, "--ids", "5,6,7")
	var out, errOut bytes.Buffer
	if code := runDeletePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, in); code != 0 {
		t.Fatalf("exit = %d, want 0; stdout: %s stderr: %s", code, out.String(), errOut.String())
	}
	var res ops.DeletePostsResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || !res.Success || !reflect.DeepEqual(res.Deleted, []int{5, 7}) || !reflect.DeepEqual(res.AlreadyGone, []int{6}) {
		t.Errorf("stdout = %s (err %v), want success, deleted [5 7], already_gone [6]", out.String(), err)
	}

	st2 := newDeleteStub(t, 200, `{"success":true}`, map[string]int{"9": 404})
	defer st2.Close()
	in, _ = deleteCmdInput(t, "9")
	out.Reset()
	errOut.Reset()
	if code := runDeletePosts(context.Background(), newCLITestClient(t, st2.Server), &out, &errOut, in); code != 1 {
		t.Fatalf("single-id 404 exit = %d, want 1", code)
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || len(res.Failed) != 1 || res.Failed[0].ID != 9 {
		t.Errorf("single-id failure must print the failed record on stdout: %q (err %v)", out.String(), err)
	}
}

// TestPostsDelete_R6_BatchNotFoundExits1WithoutPerIDRun: the batch endpoint
// answers 404 (wrong base URL or route): exit 1 after exactly one request, no
// DELETE, nothing on stdout that reads as success.
//
// Mutation: ops/posts_delete.go, remove the `if hooppy.IsNotFound(err)` branch
// after the transport check — DELETEs are sent and the run reports already_gone.
func TestPostsDelete_R6_BatchNotFoundExits1WithoutPerIDRun(t *testing.T) {
	st := newDeleteStub(t, 404, `{"error":"no route"}`, nil)
	defer st.Close()
	in, _ := deleteCmdInput(t, "--ids", "5,6,7")
	var out, errOut bytes.Buffer
	if code := runDeletePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, in); code != 1 {
		t.Fatalf("exit = %d, want 1; stdout: %s stderr: %s", code, out.String(), errOut.String())
	}
	if !reflect.DeepEqual(st.requests, []string{"POST /posts/batch/delete"}) {
		t.Errorf("requests = %v, want the single batch POST and no DELETE", st.requests)
	}
	if strings.Contains(out.String(), `"success": true`) {
		t.Errorf("stdout must not read as success: %s", out.String())
	}
}

// TestPostsDelete_R7_EveryIDNotFoundExits1KeepsRecord: the batch is refused (500)
// and every DELETE answers 404 — nothing was deleted in this run, so exit 1, with
// the per-id record (already_gone) and a Note naming the misconfiguration.
//
// Mutation: ops/posts_delete.go, change `case len(res.Failed) == 0 &&
// len(res.Deleted) > 0:` to `case len(res.Failed) == 0:` — exit 0.
func TestPostsDelete_R7_EveryIDNotFoundExits1KeepsRecord(t *testing.T) {
	st := newDeleteStub(t, 500, `{"error":"boom"}`, map[string]int{"5": 404, "6": 404})
	defer st.Close()
	in, _ := deleteCmdInput(t, "--ids", "5,6")
	var out, errOut bytes.Buffer
	if code := runDeletePosts(context.Background(), newCLITestClient(t, st.Server), &out, &errOut, in); code != 1 {
		t.Fatalf("exit = %d, want 1; stdout: %s stderr: %s", code, out.String(), errOut.String())
	}
	var res ops.DeletePostsResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Success || !reflect.DeepEqual(res.AlreadyGone, []int{5, 6}) || !strings.Contains(res.Note, "base URL or account") {
		t.Errorf("stdout = %s (err %v), want not success, already_gone [5 6] and a Note naming base URL or account", out.String(), err)
	}
}

// TestPostsEdit_F4_PrintsTheRecordedEditState is F4's CLI half: `posts edit`
// prints the post's editable state as JSON, and every key it prints carries
// the value of the recorded live GET /posts/{id}/edit response
// (testdata/live/post_edit.json) — an oracle the code did not produce. The
// MCP half (cmd/hooppy-mcp) asserts the same against the same fixture.
func TestPostsEdit_F4_PrintsTheRecordedEditState(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/live/post_edit.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/posts/42/edit" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Write(fixture)
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	if code := runGetPostEdit(context.Background(), newCLITestClient(t, srv), &out, &errOut, 42); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, errOut.String())
	}
	assertSubsetOfRecordedEdit(t, out.Bytes(), fixture)

	out.Reset()
	if code := runGetPostEdit(context.Background(), newCLITestClient(t, srv), &out, &errOut, 0); code != 1 {
		t.Errorf("`posts edit 0` exit = %d, want 1", code)
	}
}

// assertSubsetOfRecordedEdit checks that got is a value-for-value subset of the
// recorded response (every object key of got exists in the recorded value with
// an equal value, arrays align by index) and that the load-bearing keys are
// present. A subset, not equality: the output is the decoded PostEditResponse,
// which does not carry every key the server sends.
func assertSubsetOfRecordedEdit(t *testing.T, got, recorded []byte) {
	t.Helper()
	// Both surfaces print the decoded PostEditResponse, so the output must also
	// equal the fixture decoded into that type and indented the same way.
	var decoded hooppy.PostEditResponse
	if err := json.Unmarshal(recorded, &decoded); err != nil {
		t.Fatal(err)
	}
	want, _ := json.MarshalIndent(decoded, "", "  ")
	if strings.TrimSpace(string(got)) != string(want) {
		t.Errorf("output differs from the recorded response decoded as PostEditResponse:\n got: %s\nwant: %s", got, want)
	}
	var g, r map[string]any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("output is not a JSON object: %v: %s", err, got)
	}
	if err := json.Unmarshal(recorded, &r); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"publication_when_type", "texts", "attachments", "schedule_id", "project_id", "selected_pages_by_source_ids"} {
		if _, ok := g[key]; !ok {
			t.Errorf("output lacks %q", key)
		}
	}
	if path, ok := jsonSubset(g, r, ""); !ok {
		t.Errorf("output differs from the recorded response at %s", path)
	}
}

func jsonSubset(got, want any, path string) (string, bool) {
	switch g := got.(type) {
	case map[string]any:
		w, ok := want.(map[string]any)
		if !ok {
			return path, false
		}
		for k, gv := range g {
			wv, ok := w[k]
			if !ok {
				return path + "." + k, false
			}
			if p, ok := jsonSubset(gv, wv, path+"."+k); !ok {
				return p, false
			}
		}
		return "", true
	case []any:
		w, ok := want.([]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for i := range g {
			if p, ok := jsonSubset(g[i], w[i], path+"[]"); !ok {
				return p, false
			}
		}
		return "", true
	}
	return path, reflect.DeepEqual(got, want)
}
