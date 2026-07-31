package hooppy

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// successFalseBody is the canonical 2xx failure the transport layer does not
// surface: a decodable 2xx carrying {"success":false}. Every list-A method
// MUST turn this into an error, not a silent nil.
const successFalseBody = `{"success":false,"message":"operation rejected by server"}`

// successTrueBody is the canonical 2xx success. Used by F4 to prove the gate
// is reached and discriminates on the field value (success:true → nil), not
// bypassed.
const successTrueBody = `{"success":true}`

// --- F1: a success:false fixture test for EVERY method in list A ----------

// TestSuccessGate_F1_SuccessFalseEveryListAMethod drives each list-A method
// through its real code path against a 2xx {"success":false} and asserts the
// method returns an error. The gate lives in client.do/doWithRetry (one
// place), so removing it must take ALL of these RED together — that is the
// point of it being shared. A method that stays GREEN with the gate removed
// is not actually gated and is reported as a hole.
//
// RED-on-revert: remove the checkSuccess call in client.do (and the
// retry.Permanent(checkSuccess) call in doWithRetry) and every subtest
// fails — err becomes nil for the success:false response.
func TestSuccessGate_F1_SuccessFalseEveryListAMethod(t *testing.T) {
	// each method calls the client against a server that returns success:false
	// for the mutation path (and any required pre-response for methods that
	// need a read first). The handler is shared; each case names the method.
	type Case struct {
		name string
		// call invokes the method against c; must issue at least one
		// mutation request the server will answer with successFalseBody.
		call func(c *Client) error
		// handler returns the response for a given method+path. Methods that
		// need a pre-read (UpdateScheduleFromEdit, UpdatePostText) get the
		// pre-response from here too.
		handler func(w http.ResponseWriter, r *http.Request)
	}

	// falseOnMutation returns successFalseBody for any non-GET request and a
	// 405 for unmatched GETs. Used by methods with no pre-read.
	falseOnMutation := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Write([]byte(successFalseBody))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}

	cases := []Case{
		{
			name: "CreateSchedule",
			call: func(c *Client) error {
				p := NewSchedulePayload("x")
				p.SelectedPagesBySourceIDs = map[int][]int{1: {10}} // satisfy how_type=1 invariant
				_, err := c.CreateSchedule(context.Background(), p)
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "UpdateSchedule",
			call: func(c *Client) error {
				_, err := c.UpdateSchedule(context.Background(), 7, NewSchedulePayload("x"))
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "UpdateScheduleFromEdit",
			call: func(c *Client) error {
				ov, _ := ScheduleOverride("name", "New")
				_, err := c.UpdateScheduleFromEdit(context.Background(), 42, ov)
				return err
			},
			// needs a recognisable /edit response first, then success:false on PUT
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/posts/schedules/42/edit":
					w.Write([]byte(scheduleEditFullResponse))
				case r.Method == http.MethodPut:
					w.Write([]byte(successFalseBody))
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			},
		},
		{
			name: "DeleteSchedule",
			call: func(c *Client) error {
				_, err := c.DeleteSchedule(context.Background(), 7)
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "UpdateProject",
			call: func(c *Client) error {
				_, err := c.UpdateProject(context.Background(), 5, "New")
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "DeleteProject",
			call: func(c *Client) error {
				_, err := c.DeleteProject(context.Background(), 5)
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "DisconnectPage",
			call: func(c *Client) error {
				_, err := c.DisconnectPage(context.Background(), 9)
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "CreateWatermark",
			call: func(c *Client) error {
				_, err := c.CreateWatermark(context.Background(), WatermarkPayload{Name: "w"})
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "UpdateWatermark",
			call: func(c *Client) error {
				_, err := c.UpdateWatermark(context.Background(), 3, WatermarkPayload{Name: "w"})
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "DeleteWatermark",
			call: func(c *Client) error {
				_, err := c.DeleteWatermark(context.Background(), 3)
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "CreateProxy",
			call: func(c *Client) error {
				_, err := c.CreateProxy(context.Background(), ProxyPayload{Name: "p", IP: "1.2.3.4", Port: "8080"})
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "UpdateProxy",
			call: func(c *Client) error {
				_, err := c.UpdateProxy(context.Background(), 3, ProxyPayload{Name: "p", IP: "1.2.3.4", Port: "8080"})
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "DeleteProxy",
			call: func(c *Client) error {
				_, err := c.DeleteProxy(context.Background(), 3)
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "UpdatePost",
			call: func(c *Client) error {
				_, err := c.UpdatePost(context.Background(), 42, PostPublishNowPayload{
					PublicationWhenType: 1, PublicationHowType: 1,
					SelectedPagesIDs: []int{1}, Texts: []PostText{{Text: "x"}},
				})
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "UpdatePostText",
			call: func(c *Client) error {
				_, err := c.UpdatePostText(context.Background(), 42, "new text")
				return err
			},
			// needs a valid /edit (schedule-driven, when_type=3) then success:false on PUT
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/edit"):
					w.Write([]byte(scheduleDrivenEditBody))
				case r.Method == http.MethodPut:
					w.Write([]byte(successFalseBody))
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			},
		},
		{
			name: "DeletePost",
			call: func(c *Client) error {
				_, err := c.DeletePost(context.Background(), 42)
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "BatchDeletePosts",
			call: func(c *Client) error {
				_, err := c.BatchDeletePosts(context.Background(), []int{1, 2})
				return err
			},
			handler: falseOnMutation,
		},
		{
			name: "StartParsing",
			call: func(c *Client) error {
				_, err := c.StartParsing(context.Background(), ParsingStartPayload{
					SourceResourceID: 1, SocialAccountForParsingID: 1,
				})
				return err
			},
			handler: falseOnMutation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(tc.handler))
			defer srv.Close()
			c := newTestClient(t, srv)
			err := tc.call(c)
			if err == nil {
				t.Fatalf("%s with {\"success\":false}: expected an error, got nil — a 2xx with success=false is a failed operation, not a silent success (the gate in client.do/doWithRetry must surface it)", tc.name)
			}
			// The error must be the typed SuccessFalseError carrying the
			// endpoint, so the CLI/MCP can act on it. Confirm the endpoint
			// and the server message are present.
			var sfe *SuccessFalseError
			if !errorsAs(err, &sfe) {
				t.Fatalf("%s: error is not a *SuccessFalseError (got %T): %v — the gate must return the typed error so callers can distinguish a decided-false from a transport failure", tc.name, err, err)
			}
			if sfe.Endpoint == "" {
				t.Errorf("%s: SuccessFalseError.Endpoint is empty — must carry the endpoint path so an operator can act on it", tc.name)
			}
			if !strings.Contains(sfe.Message, "operation rejected by server") {
				t.Errorf("%s: SuccessFalseError.Message = %q — must carry the server's message extracted from the body", tc.name, sfe.Message)
			}
		})
	}
}

// TestSuccessGate_F1_SuccessFalseNotRetried confirms a 2xx {"success":false}
// is a DECIDED outcome that never re-enters the retry ladder. doWithRetry
// wraps it with retry.Permanent. A server that would otherwise be hit 4 times
// (defaultRetryOptions MaxAttempts=4) is hit exactly once when it answers
// success:false on a retryable path (DELETE /posts/schedules/{id} is
// retryable=true).
//
// RED-on-revert: drop the retry.Permanent wrap around checkSuccess in
// doWithRetry and the call count rises (the success:false would be treated as
// a non-permanent error and retried — though it is not an APIError so retry.Do
// may still stop; the robust assertion is that the error is a
// *SuccessFalseError, which only the Permanent-wrapped path preserves as the
// terminal error).
func TestSuccessGate_F1_SuccessFalseNotRetried(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(successFalseBody))
	}))
	defer srv.Close()
	// Retry enabled (NewClientFromEnv shape): 4 attempts available.
	c, err := NewClient(Config{Token: "test-token", BaseURL: srv.URL, RetryOptions: defaultRetryOptions()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.DeleteSchedule(context.Background(), 7)
	if err == nil {
		t.Fatal("DeleteSchedule with success:false: expected error, got nil")
	}
	var sfe *SuccessFalseError
	if !errorsAs(err, &sfe) {
		t.Fatalf("error is not *SuccessFalseError (got %T): %v — a 2xx success:false must surface as the typed decided-false, not a retry-exhausted transport error", err, err)
	}
	if calls != 1 {
		t.Errorf("server was hit %d times, want 1 — a 2xx {\"success\":false} is a decided outcome and MUST NOT be retried (doWithRetry wraps it with retry.Permanent)", calls)
	}
}

// --- F2: create-shaped id guard ------------------------------------------

// TestSuccessGate_F2_CreateIDGuard drives each of the five create-shaped
// methods against a 2xx with the id key ABSENT and with {"id":0}. Both must
// return an error — a zero id flows into posts move/update/delete as a
// real-looking handle (issue #131).
//
// RED-on-revert: remove the checkCreateID call at any create site and that
// site's subtests go GREEN (err becomes nil with id 0).
func TestSuccessGate_F2_CreateIDGuard(t *testing.T) {
	// bodies: id absent and id explicitly zero. Both decode to ID=0.
	bodies := []struct {
		name string
		body string
	}{
		{"id_absent", `{}`},
		{"id_zero", `{"id":0}`},
	}

	type Case struct {
		name string
		call func(c *Client) error
	}
	// All create-shaped methods use when_type=1 so fillScheduleSlots is a
	// no-op (no extra requests); the id guard runs right after decode (or
	// after the no-op fillScheduleSlots for the search trio).
	cases := []Case{
		{
			name: "CreatePost",
			call: func(c *Client) error {
				_, err := c.CreatePost(context.Background(), PostPublishNowPayload{
					PublicationWhenType: 1, PublicationHowType: 1,
					SelectedPagesIDs: []int{1}, Texts: []PostText{{Text: "x"}},
				})
				return err
			},
		},
		{
			name: "createPostWithMode_SearchPosts",
			call: func(c *Client) error {
				_, err := c.SearchPosts(context.Background(), PostPublishNowPayload{
					PublicationWhenType: 1, PublicationHowType: 1,
					SelectedPagesIDs: []int{1}, Texts: []PostText{{Text: "x"}},
				})
				return err
			},
		},
		{
			name: "CopySearchPost",
			call: func(c *Client) error {
				_, err := c.CopySearchPost(context.Background(), CopySearchPostPayload{
					SearchPostID: 1001, PublicationWhenType: 1, PublicationHowType: 1,
					SelectedPagesIDs: []int{1},
				})
				return err
			},
		},
		{
			name: "RewriteSearchPost",
			call: func(c *Client) error {
				_, err := c.RewriteSearchPost(context.Background(), CopySearchPostPayload{
					SearchPostID: 1001, PublicationWhenType: 1, PublicationHowType: 1,
					SelectedPagesIDs: []int{1}, Texts: []PostText{{Text: "x"}},
				})
				return err
			},
		},
		{
			name: "ImportSearchPost",
			call: func(c *Client) error {
				_, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
					SearchPostID: 2003, PublicationWhenType: 1, PublicationHowType: 1,
					SelectedPagesIDs: []int{1},
				})
				return err
			},
		},
	}

	for _, tc := range cases {
		for _, b := range bodies {
			t.Run(tc.name+"/"+b.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte(b.body))
				}))
				defer srv.Close()
				c := newTestClient(t, srv)
				err := tc.call(c)
				if err == nil {
					t.Fatalf("%s with %s: expected an error (id is 0/absent — no usable handle), got nil — a zero id flows into posts move/update/delete as a real-looking handle", tc.name, b.name)
				}
				if !strings.Contains(err.Error(), "no post id") {
					t.Errorf("%s with %s: error does not name the missing-id cause: %q", tc.name, b.name, err.Error())
				}
			})
		}
	}
}

// TestSuccessGate_F2_CreateIDGuard_BatchRecoveredIDPasses confirms the id
// guard does NOT false-alarm on a batch create where the wire id is 0 but
// fillScheduleSlots recovers ids from a schedule snapshot diff. The guard
// checks id == 0 AND len(ids) == 0, so a recovered id satisfies it. This is
// the non-vacuity complement for the batch path: the guard passes a real
// batch, not just rejects an empty one.
//
// Uses when_type=3 so fillScheduleSlots runs. The before-snapshot sees one
// post; the after-snapshot sees two (the created one) — the diff recovers the
// new id, sets resp.ID, and the guard passes.
func TestSuccessGate_F2_CreateIDGuard_BatchRecoveredIDPasses(t *testing.T) {
	// Schedule list: before create = [10]; after create = [10, 9001].
	// The server answers the create with {"success":true} (no id) — the
	// batch shape. fillScheduleSlots diffs and recovers 9001.
	var createCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			createCalled = true
			w.Write([]byte(`{"success":true}`)) // batch: no id on the wire
			return
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			// ListPosts for the schedule snapshot. Return before or after
			// depending on whether the create has happened.
			w.Header().Set("Content-Type", "application/json")
			if createCalled {
				w.Write([]byte(`{"list":[{"id":10},{"id":9001}],"total_rows":2,"is_has_more":false,"rows_limit":20}`))
			} else {
				w.Write([]byte(`{"list":[{"id":10}],"total_rows":1,"is_has_more":false,"rows_limit":20}`))
			}
			return
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID: 2003, PublicationWhenType: 3, PublicationHowType: 1,
		SchedulesIDs: []int{7},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost batch with recovered id: expected nil error, got %v — the id guard must NOT false-alarm when fillScheduleSlots recovers an id", err)
	}
	if resp.ID == 0 && len(resp.IDs) == 0 {
		t.Fatal("recovered id is 0 and IDs is empty — the snapshot diff did not recover the created id; the guard would have errored, so this proves the guard saw a recovered id and passed")
	}
}

// TestSuccessGate_F2_CreateIDGuard_SlotLookupErrorSuppresses confirms the
// id guard does NOT fire when fillScheduleSlots attempted recovery but failed
// (SlotLookupError set, ids empty). This is the existing contract: a batch
// create where the after-snapshot read fails SUCCEEDED — only id recovery
// failed, and the client returns nil with SlotLookupError set rather than
// erroring (TestImportSearchPost_BatchAfterSnapshotFails). The guard defers
// to SlotLookupError: a non-empty SlotLookupError means recovery was
// attempted, so the missing id is a recovery failure, not a server omission.
//
// RED-on-revert: if the guard ignored SlotLookupError and fired on id==0 &&
// len(ids)==0 alone, this test would fail (the batch create would error
// instead of returning nil with SlotLookupError).
func TestSuccessGate_F2_CreateIDGuard_SlotLookupErrorSuppresses(t *testing.T) {
	var createCalled int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/posts/import":
			atomic.StoreInt32(&createCalled, 1)
			w.Write([]byte(`{"success":true}`)) // batch: no id on the wire
		case r.Method == http.MethodGet && r.URL.Path == "/posts":
			if atomic.LoadInt32(&createCalled) == 0 {
				// Before snapshot: one post.
				w.Write([]byte(`{"list":[{"id":10}],"total_rows":1,"is_has_more":false,"rows_limit":20}`))
			} else {
				// After snapshot fails — recovery attempted, failed.
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"server error"}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	resp, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostIDs:       []int{2001, 2002},
		PublicationWhenType: 3,
		PublicationHowType:  2,
		SchedulesIDs:        []int{55},
	})
	if err != nil {
		t.Fatalf("ImportSearchPost batch with after-snapshot failure: expected nil error (create succeeded, only id recovery failed), got %v — the id guard must NOT fire when SlotLookupError is set (recovery was attempted)", err)
	}
	if resp.ID != 0 || len(resp.IDs) != 0 {
		t.Errorf("ID=%d IDs=%v — want empty (recovery failed)", resp.ID, resp.IDs)
	}
	if resp.SlotLookupError == "" {
		t.Fatal("SlotLookupError is empty — fillScheduleSlots did not record the recovery failure; the guard would have no signal to defer to")
	}
}

// --- F3: reflection test — every Success-field type implements the gate ---

// TestSuccessGate_F3_AllSuccessFieldTypesImplementInterface parses the
// package source (non-test files) and asserts that EVERY struct with a field
// named "Success" has a successState() bool method (i.e. implements
// successReporter). This is the test that keeps the gate honest: adding a
// response type with a Success field but no method fails the BUILD, not the
// operator's account. A hand-maintained list of type names is NOT used — the
// set is discovered by walking the AST, so it cannot drift.
//
// RED-on-revert: add a struct with a Success field and no successState method
// — this test goes RED naming the ungated type.
func TestSuccessGate_F3_AllSuccessFieldTypesImplementInterface(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		// skip _test.go files — test-only types are not production response
		// types and must not be forced through the gate.
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no packages parsed in .")
	}

	successFieldTypes := map[string]bool{}     // struct type name → has a Success field
	successStateReceivers := map[string]bool{} // receiver type name → has successState() bool

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			// Structs with a Success field.
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, f := range st.Fields.List {
						for _, n := range f.Names {
							if n.Name == "Success" {
								successFieldTypes[ts.Name.Name] = true
							}
						}
					}
				}
			}
			// successState method declarations.
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Name == nil || fd.Name.Name != "successState" {
					continue
				}
				if fd.Recv == nil || len(fd.Recv.List) == 0 {
					continue
				}
				// Receiver type: unwrap pointer/star.
				recvType := ""
				switch rt := fd.Recv.List[0].Type.(type) {
				case *ast.StarExpr:
					if id, ok := rt.X.(*ast.Ident); ok {
						recvType = id.Name
					}
				case *ast.Ident:
					recvType = rt.Name
				}
				if recvType == "" {
					continue
				}
				// Verify the signature: no params, one bool result.
				if fd.Type.Params != nil && len(fd.Type.Params.List) > 0 {
					continue
				}
				if fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
					continue
				}
				if id, ok := fd.Type.Results.List[0].Type.(*ast.Ident); ok && id.Name == "bool" {
					successStateReceivers[recvType] = true
				}
			}
		}
	}

	if len(successFieldTypes) == 0 {
		t.Fatal("found no struct types with a Success field — the AST scan is broken (the package has several); fix the test before trusting it")
	}

	// Forward: every Success-field type MUST have a successState method.
	for typ := range successFieldTypes {
		if !successStateReceivers[typ] {
			t.Errorf("type %s has a Success field but NO successState() bool method — it is UNGATED: a 2xx {\"success\":false} decoding into it exits 0 silently. Add `func (r *%s) successState() bool { return r.Success }` to success_gate.go. (TestSuccessGate_F3 keeps the gate honest; this failure means a response type was added without wiring it to the shared gate.)", typ, typ)
		}
	}
	// Reverse: every successState receiver MUST have a Success field —
	// catches a stale method left on a type that lost its field (a drifted
	// gate that gates nothing).
	for typ := range successStateReceivers {
		if !successFieldTypes[typ] {
			t.Errorf("type %s has a successState() bool method but NO Success field — a stale gate method that gates nothing; remove it or restore the Success field", typ)
		}
	}
}

// --- F4: success:true still passes for the right reason -------------------

// TestSuccessGate_F4_SuccessTruePasses confirms the gate is REACHED and
// discriminates on the field value: a 2xx {"success":true} returns nil with
// the response populated (Success == true), proving the decode happened and
// the gate saw true (not that the gate is unreachable). Combined with F1
// (success:false → error), the gate is demonstrably the discriminator.
//
// RED-on-revert: if the gate were unreachable (e.g. checkSuccess never
// called), F1 would already be RED. This test guards the complement: a
// success:true response is not falsely rejected.
func TestSuccessGate_F4_SuccessTruePasses(t *testing.T) {
	// Representative methods covering each gated response type.
	t.Run("DeleteSchedule_ScheduleResponse", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"success":true,"schedules":[]}`))
		}))
		defer srv.Close()
		c := newTestClient(t, srv)
		resp, err := c.DeleteSchedule(context.Background(), 7)
		if err != nil {
			t.Fatalf("DeleteSchedule with success:true: expected nil error, got %v — the gate must pass a real success, not reject it", err)
		}
		if !resp.Success {
			t.Fatal("resp.Success = false — the decode did not reach the Success field; the gate cannot be discriminating on it")
		}
	})
	t.Run("DeletePost_DeletePostResponse", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(successTrueBody))
		}))
		defer srv.Close()
		c := newTestClient(t, srv)
		resp, err := c.DeletePost(context.Background(), 42)
		if err != nil {
			t.Fatalf("DeletePost with success:true: expected nil error, got %v", err)
		}
		if !resp.Success {
			t.Fatal("resp.Success = false — decode did not reach the Success field")
		}
	})
	t.Run("StartParsing_ParsingStartResponse", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(successTrueBody))
		}))
		defer srv.Close()
		c := newTestClient(t, srv)
		resp, err := c.StartParsing(context.Background(), ParsingStartPayload{
			SourceResourceID: 1, SocialAccountForParsingID: 1,
		})
		if err != nil {
			t.Fatalf("StartParsing with success:true: expected nil error, got %v", err)
		}
		if !resp.Success {
			t.Fatal("resp.Success = false — decode did not reach the Success field")
		}
	})
}

// TestSuccessGate_F4_CreateWithRealIDPasses confirms the create id guard
// does NOT false-alarm on a real create that returns a valid id. The
// complement of F2: a wire id > 0 passes the guard.
func TestSuccessGate_F4_CreateWithRealIDPasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":12345}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	resp, err := c.CreatePost(context.Background(), PostPublishNowPayload{
		PublicationWhenType: 1, PublicationHowType: 1,
		SelectedPagesIDs: []int{1}, Texts: []PostText{{Text: "x"}},
	})
	if err != nil {
		t.Fatalf("CreatePost with real id: expected nil error, got %v — the id guard must pass a valid handle, not reject it", err)
	}
	if resp.ID != 12345 {
		t.Errorf("ID = %d, want 12345", resp.ID)
	}
}
