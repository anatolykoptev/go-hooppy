package hooppy

import (
	"encoding/json"
	"fmt"
)

// successReporter is satisfied by every response type that carries a
// server-sent "success" boolean. client.do and doWithRetry consult it after a
// successful decode: a success:false is a DECIDED failure (the operation did
// NOT happen) and becomes a *SuccessFalseError, not a silent nil. The
// transport layer treats any decodable 2xx as success, so without this gate a
// 2xx {"success":false} exits 0 — the defect this gate closes (issue #118).
//
// OPT-IN IS THE TRAP this repo keeps falling into: a response type that
// carries "success" but does NOT implement this interface is silently skipped
// by the gate, and the next type someone adds is ungated with no error, no
// warning, and a green suite — exactly how the defect existed across 18 call
// sites. TestSuccessGate_AllSuccessFieldTypesImplementInterface closes that
// hole by parsing the package source and asserting EVERY struct with a
// "Success" field implements this interface, so adding an ungated response
// type fails the build instead of passing quietly.
type successReporter interface {
	successState() bool
}

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
// {"success":false} body, mirroring newAPIError's extraction
// ({"message":"..."} or {"error":"..."}). Returns "" when neither is present —
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
	if v, ok := m["error"].(string); ok {
		return v
	}
	return ""
}

// checkSuccess is the shared gate consulted by client.do and doWithRetry
// after a successful decode. When out implements successReporter (i.e. the
// response type carries a "success" field) and the RAW BODY carries an
// EXPLICIT {"success":false}, it returns a *SuccessFalseError carrying the
// endpoint and any server message extracted from data.
//
// ABSENT vs EXPLICIT-false: a Go bool decodes both an absent "success" key and
// an explicit false to false — the decoded field alone cannot tell them apart.
// The defect is the EXPLICIT false being ignored (the server said "no"); an
// ABSENT field is no failure signal at all (the operation's success is simply
// unknown, the status quo before this gate). So the gate fires ONLY on an
// explicit success:false in the raw body, never on an absent field. This makes
// the gate robust to endpoints whose success response omits the flag without
// falsely erroring on a real success.
//
// Returns nil when out does not implement successReporter (list-only response
// types with no success flag are reads, not mutation outcomes).
func checkSuccess(out interface{}, data []byte, endpoint string) error {
	sr, ok := out.(successReporter)
	if !ok {
		return nil
	}
	// Fast path: the decoded field is true → an explicit success:true (or a
	// present true). Success, no need to re-parse.
	if sr.successState() {
		return nil
	}
	// The decoded field is false. Distinguish explicit-false from absent by
	// consulting the raw body — the one place the distinction is visible.
	var m map[string]interface{}
	if json.Unmarshal(data, &m) != nil {
		return nil // unparseable body — no signal, let the decoded value stand
	}
	v, present := m["success"]
	if !present {
		return nil // absent — no failure signal (not the defect this gate closes)
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

// successState implementations for every response type carrying a Success
// field. TestSuccessGate_AllSuccessFieldTypesImplementInterface asserts this
// set stays complete — adding a Success field to a response type without a
// method here fails the build, not the operator's account.
func (r *ScheduleResponse) successState() bool     { return r.Success }
func (r *DeleteResponse) successState() bool       { return r.Success }
func (r *WatermarkResponse) successState() bool    { return r.Success }
func (r *ProxyResponse) successState() bool        { return r.Success }
func (r *DeletePostResponse) successState() bool   { return r.Success }
func (r *ParsingStartResponse) successState() bool { return r.Success }
func (r *PostMoveResult) successState() bool       { return r.Success }
func (r *BatchMovePostsResult) successState() bool { return r.Success }
