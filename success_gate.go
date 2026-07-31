package hooppy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SuccessFalseError is the typed error returned when the API answers a 2xx
// with {"success":false} — a decided failure the HTTP transport (2xx) does
// not surface. It carries the endpoint and the server's message (when the
// body includes a "message"/"error" string), so the CLI/MCP can print
// something an operator can act on. It is NOT retryable: doWithRetry wraps it
// with retry.Permanent so a 2xx success:false never re-enters the retry
// ladder (it is a decided outcome, not a transient failure).
type SuccessFalseError struct {
	Endpoint string
	Message  string
}

func (e *SuccessFalseError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("hooppy: %s: server returned 2xx with {\"success\":false}: %s — the operation did NOT happen (a 2xx with success=false is a real failure, not a silent success)", e.Endpoint, e.Message)
	}
	return fmt.Sprintf("hooppy: %s: server returned 2xx with {\"success\":false} — the operation did NOT happen (a 2xx with success=false is a real failure, not a silent success)", e.Endpoint)
}

// extractSuccessFalseMessage pulls a human-readable message from a 2xx
// {"success":false} body. It mirrors newAPIError's extraction
// ({"message":"..."} or {"error":"..."}) and additionally handles the nested
// ES error shape evidenced in this repo ({"error":{"reason":"..."}} — see
// isResultWindowError / resultWindowErrorBody), since a 2xx success:false body
// can carry the same nested error object. Returns "" when none is present —
// the success:false flag itself is the failure signal. data is the raw decoded
// body the caller already read.
func extractSuccessFalseMessage(data []byte) string {
	var m map[string]interface{}
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	if v, ok := m["message"].(string); ok {
		return v
	}
	// "error" may be a string (newAPIError shape) or an object carrying a
	// "reason" string (the ES result-window shape evidenced in
	// posts_search_test.go:1830 / errors.go:144-146).
	if v, ok := m["error"].(string); ok {
		return v
	}
	if obj, ok := m["error"].(map[string]interface{}); ok {
		if r, ok := obj["reason"].(string); ok {
			return r
		}
	}
	return ""
}

// successKey is the literal byte sequence the pre-filter scans for. Scanning
// for the quoted key (not the bare word) avoids matching the value or a
// substring of another key, and is the cheapest possible gate: a single
// bytes.Contains over the raw body, no allocation.
var successKey = []byte(`"success"`)

// checkSuccess is the shared gate consulted by client.do and doWithRetry
// after a successful decode. It is TYPE-INDEPENDENT: the explicit-
// {"success":false} check applies to EVERY response, decided by the raw body
// alone, regardless of the response type's Go fields. A type cannot opt out
// of a check that does not consult the type.
//
// This closes the round-1 hole: PostIDResponse / CreatePostResponse carry no
// Success field, so under the old opt-in successReporter shape they fell out
// of the gate entirely — a 2xx {"success":false} on a create was swallowed,
// and the worst variant {"id":123,"success":false} passed both checkSuccess
// (no Success field) and checkCreateID (id non-zero) and was reported as a
// clean success carrying a real handle. The universal gate catches both.
//
// ABSENT vs EXPLICIT-false: a Go bool decodes both an absent "success" key and
// an explicit false to false — the decoded field alone cannot tell them apart.
// The defect is the EXPLICIT false being ignored (the server said "no"); an
// ABSENT field is no failure signal at all. So the gate fires ONLY on an
// explicit success:false in the raw body, never on an absent field.
//
// Cost: a large list response (search posts --all returns ~23 MB, 10000 rows)
// carries no "success" key at all. The byte pre-filter (bytes.Contains) finds
// no match and returns nil WITHOUT allocating a map — the common read path
// pays only a single linear scan. Only a body that contains the literal
// "success" (a small mutation response, or a constructed edge case) proceeds
// to the map parse. See BenchmarkCheckSuccess_LargeListBody for the measured
// cost on a ~23 MB body with no success key.
func checkSuccess(out interface{}, data []byte, endpoint string) error {
	_ = out // the gate is type-independent; out is kept for call-site symmetry
	// Pre-filter: a cheap byte scan for the literal "success" key. A body
	// without it cannot carry an explicit success:false, so short-circuit
	// without allocating a map. This is the fast path for large list reads.
	if !bytes.Contains(data, successKey) {
		return nil
	}
	// The body contains "success" somewhere (as a key, or inside a nested
	// string value). The map parse is the authority: only an explicit
	// top-level "success":false fires.
	var m map[string]interface{}
	if json.Unmarshal(data, &m) != nil {
		return nil // unparseable body — no signal, let the decoded value stand
	}
	v, present := m["success"]
	if !present {
		return nil // absent at top level — no failure signal (the byte hit was
		// inside a nested string value, not a top-level key)
	}
	if b, ok := v.(bool); ok && !b {
		return &SuccessFalseError{
			Endpoint: endpoint,
			Message:  extractSuccessFalseMessage(data),
		}
	}
	return nil
}

// CreateNoIDError is the typed error returned when a create-shaped response
// carries no usable id (id:0/absent) and no batch recovery was attempted. The
// post may exist on the server — the server simply did not return its id — so
// a caller CAN distinguish "create succeeded but id unknown" from "create
// failed" by type-asserting to *CreateNoIDError. The CLI import path does
// exactly this: it maps a *CreateNoIDError to the "created_no_id" status
// (exit 0, post_id:0 present in stdout) instead of "failed", preserving the
// dedup-via-stdout design. A caller that does NOT type-assert treats it as a
// generic error (exit 1) — the safe default, since a zero id must not flow
// into posts move/update/delete as a real-looking handle (issue #131).
type CreateNoIDError struct {
	Endpoint string
}

func (e *CreateNoIDError) Error() string {
	return fmt.Sprintf("hooppy: %s: server returned 2xx with no post id (id is 0 or absent) — the create did not return a usable handle; the returned id must not be treated as real (a zero flows into posts move/update/delete as a real-looking handle)", e.Endpoint)
}

// PartialPostError is the typed error returned by RewriteSearchPost and
// ImportSearchPost when a batch (SearchPostIDs with >1 element) completes
// some posts but fails on others. It carries the populated *PostIDResponse
// (with every id that DID land) alongside the per-post failures, so a caller
// never loses already-published posts from the return value — the defect this
// closes is a batch partial failure discarding every post already created
// (a caller's only recourse was a re-run, which duplicates them).
//
// A caller distinguishes the three batch outcomes by type-asserting the error:
//   - err == nil                       → every post succeeded
//   - err is *PartialPostError          → some succeeded, some failed
//     (resp is NON-nil and populated with the successful ids)
//   - err is non-nil, not *PartialPostError → every post failed (resp is nil)
//
// The single-post path (SearchPostIDs empty, SearchPostID set) does NOT use
// this type — a single-post failure returns a plain wrapped error with a nil
// result, matching the pre-batch contract. PartialPostError is batch-only.
//
// Result is the accumulated PostIDResponse: IDs/Slots hold every successfully
// published post, ID is the first successful id, SlotLookupError aggregates
// per-post slot lookup failures. Failed holds the per-post errors in
// caller-order, each carrying the scraped-post id that failed and the wrapped
// error. A caller re-running after a partial failure reads Result.IDs to skip
// what already landed — the same dedup-via-stdout design runImport uses.
type PartialPostError struct {
	Result *PostIDResponse
	Failed []PostFailure
}

// PostFailure is one failed post in a PartialPostError.Failed slice.
type PostFailure struct {
	SearchPostID int
	Err          error
}

func (e *PartialPostError) Error() string {
	// e.Result is an exported field on an exported type, so the zero value
	// (*PartialPostError)(nil-result) is reachable from a caller that
	// constructs the value by hand. Guard the dereference — the typed error
	// only ever carries a non-nil Result from resolvePublishBatch, but
	// Error() must not panic on the zero value.
	succeeded := 0
	idsStr := ""
	if e.Result != nil {
		ids := make([]string, 0, len(e.Result.IDs))
		for _, id := range e.Result.IDs {
			ids = append(ids, strconv.Itoa(id))
		}
		succeeded = len(e.Result.IDs)
		idsStr = strings.Join(ids, ", ")
	}
	failed := make([]string, 0, len(e.Failed))
	for _, f := range e.Failed {
		failed = append(failed, fmt.Sprintf("%d: %v", f.SearchPostID, f.Err))
	}
	return fmt.Sprintf("hooppy: partial batch: %d succeeded (ids: [%s]), %d failed ([%s])", succeeded, idsStr, len(e.Failed), strings.Join(failed, ", "))
}

// checkCreateID errors when a create-shaped response carries no usable id AND
// no recovery was attempted. For a single create the wire id is the handle;
// for a batch (when_type=3) the server returns {"success":true} with no id BY
// DESIGN and the client recovers ids via fillScheduleSlots (which sets ID to
// the first recovered id, populates IDs, and sets SlotLookupError when the
// recovery read failed). When recovery was attempted but failed,
// SlotLookupError is non-empty — the create SUCCEEDED, only id recovery
// failed, and the existing contract (TestImportSearchPost_BatchAfterSnapshot
// Fails) returns nil with SlotLookupError set rather than erroring. So the
// guard fires ONLY when id and ids are both empty AND no recovery was
// attempted (SlotLookupError empty) — the single-create case where the server
// omitted the id it should have returned. That zero would flow into posts
// move/update/delete as a real-looking handle (issue #131).
func checkCreateID(endpoint string, id int, ids []int, slotLookupError string) error {
	if id == 0 && len(ids) == 0 && slotLookupError == "" {
		return &CreateNoIDError{Endpoint: endpoint}
	}
	return nil
}
