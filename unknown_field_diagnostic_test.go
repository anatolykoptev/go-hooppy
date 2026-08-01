// TestUnknownFieldDiagnostic is the gate for issue #82: both decode paths
// (client.go do at line 387 and doWithRetry at line 439) use a bare
// json.Unmarshal with no decoder options, so a field the server sends that no
// struct models is dropped silently — nothing errors, nothing is logged,
// nothing counts it. GET /posts/schedules/{id}/edit returns 72 top-level keys
// while ScheduleEditResponse models 12. Each prior gap (#68, #72, #74) was
// found by hand, one field at a time.
//
// This test is a DIAGNOSTIC over fixtures, never a decode policy. It does NOT
// enable DisallowUnknownFields in do or doWithRetry — hooppy.ru is a
// third-party API we do not control, and strict decoding live would make every
// call using a response fail hard the day the vendor adds one field. The
// diagnostic runs only here, against recorded fixtures, with its own strict
// walker.
//
// # Fixtures
//
// The 18 fixtures in testdata/live/ were recorded from live authenticated GETs
// on 2026-07-29 and then mechanically reduced: every scalar value is replaced
// by a type placeholder ("str", 0, 0.0, true, null) while key names, nesting,
// and JSON types are preserved exactly. Zero non-placeholder values are
// present — api_token is "str", gpt_key is null — so no account data or
// credentials are in the repo. The reduction is deliberate and sufficient: the
// diagnostic needs the server's key set and types, not its values.
//
// The fixtures are the oracle. If a test fails, fix the struct or the
// declaration — NEVER edit a fixture to make it pass. A fixture edited to
// agree with the struct can no longer contradict it, which is precisely how a
// wrong struct stays invisible to a green suite.
//
// # Mechanism
//
// encoding/json reports only the FIRST unknown field per decode, so a single
// strict Unmarshal will not enumerate them. Instead, walkJSON parses each
// fixture as generic JSON and walks its key tree against the target Go struct
// type via reflection — honouring json tags, embedded structs, map[string]…
// element types, and slices — collecting every JSON key that has no
// corresponding struct field, with its JSON path (e.g. list[0].publication_date)
// and JSON type. Types implementing json.Unmarshaler (Metric, FlexInt, PhotoID)
// are treated as leaves: their custom unmarshaller owns the whole value, so
// there are no "unmodelled keys" below them. These three declare UnmarshalJSON
// on a POINTER receiver while the struct fields hold VALUE types, so the leaf
// check tests both the type and reflect.PointerTo(type) — a value type does
// not satisfy an interface a pointer-to-it satisfies. interface{} fields are
// leaves too — anything is accepted.
//
// # What this gate detects — and what it does NOT
//
// The gate detects a NEW server field appearing at a MODELLED depth: a key the
// fixture carries that no struct field models and no baseline entry declares.
// That is the regression that matters — a field the struct used to capture
// silently going unmodelled, or a brand-new field nobody classified.
//
// The gate does NOT detect a new server field appearing INSIDE an already-
// unmodelled subtree. When a key is unmodelled, walkJSON records ONE entry for
// its root and never recurses into it: the whole subtree is dropped at decode
// anyway (encoding/json discards it), so enumerating its children would only
// bloat the baseline without changing what the runtime drops. A field added
// beneath an unmodelled root is therefore invisible to this gate — and that is
// acceptable, because the parent is already lost wholesale. The count reported
// per unmodelled root (e.g. "projects[0] (object, 62 keys beneath)") quantifies
// what is hidden so the floor is not mistaken for a total; the hidden keys are
// NOT enumerated into the baselines.
//
// There is a second class of blind spot: opaque leaves whose contents are
// never inspected, so a new server field inside one is invisible to this
// gate for the same reason as an unmodelled root — the parent is already
// opaque. Two Go field types produce such leaves in the response types:
//
//   - interface{}: anything decodes, so the value is never walked. The only
//     one in the response types is Attachment.Data (GET /posts/{id}/edit and
//     GET /posts-search/{id}/edit). As of the 2026-07-29 fixtures it hides 7
//     keys beneath it in post_edit.json (file_path, folder, id, name, text,
//     type, updated_date) and 2 keys beneath it in search_post_edit.json
//     (link, title).
//
//   - json.RawMessage: the bytes are retained verbatim and never parsed into
//     a struct, so the keys inside are not walked either. Three RawMessage
//     leaves carry non-empty payloads in the 2026-07-29 fixtures, hiding 11
//     keys in total: posts_search.json filters_plug (array, 4 keys —
//     SearchPostsResponse.FiltersPlug), schedule_edit.json posts_hashtags
//     (object, 3 keys — ScheduleEditResponse.PostsHashtags), and
//     schedule_edit.json posts_links (object, 4 keys —
//     ScheduleEditResponse.PostsLinks). Three further RawMessage leaves are
//     empty arrays in these fixtures and hide 0 keys today —
//     schedule_edit.json social_albums_by_pages (ScheduleEditResponse) and
//     posts_search.json list[].videos/audios/documents (SearchPost) — but a
//     server field added inside any of them once populated would be equally
//     invisible.
//
// The three blind-spot classes this gate does not see beneath are therefore:
// unmodelled subtree roots (above), interface{} leaves, and json.RawMessage
// leaves.
//
// # The gate — declared baseline, not a report
//
// A test that merely prints the unmodelled keys fails on day one and gets
// ignored. Following the pattern this repo already has (phantom_filter_test.go,
// TestRetryPolicySweep), each endpoint DECLARES its directly-unmodelled keys
// in unmodelledBaselines. The test:
//
//   - FAILS when a fixture contains an unmodelled key NOT in the baseline — a
//     newly-appeared server field nobody classified.
//   - FAILS when a declared key is now modelled by a struct field — a stale
//     declaration (the struct gained a field; remove it from the baseline).
//   - FAILS when a declared key is no longer present in the fixture — a stale
//     declaration (the fixture changed; remove it from the baseline).
//
// The point is that the list becomes explicit and enforced, not that it
// becomes empty. Some fields are deliberately excluded (User omits api_token
// and other credentials on purpose; SettingsResponse omits api_token,
// gpt_key, ru_captcha_key) and that exclusion must stay — the baseline records
// those omissions so a future struct change that models them is flagged.
//
// Do NOT model the unmodelled fields in this PR. Do NOT change the runtime
// decode path. No exported-signature changes (tagged module, v1.1.1).
package hooppy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// unmodelledKey is one JSON key present in a fixture but not modelled by the
// target Go struct, with its JSON path and JSON type. keysBeneath is the count
// of JSON keys sitting beneath this key's value when that value is an object or
// a non-empty array — those keys are hidden (the subtree is dropped wholesale
// at decode and never enumerated), so the count quantifies the floor rather
// than enumerating them into the baseline. It is 0 for scalar/empty values.
type unmodelledKey struct {
	path        string
	jsonType    string
	keysBeneath int
}

// jsonUnmarshalerType is reflect.Type for encoding/json.Unmarshaler, used to
// detect types that own their decoding (Metric, FlexInt, PhotoID) and stop
// recursing — a custom unmarshaller's struct fields are irrelevant to the
// wire shape.
var jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// jsonFields returns a map from JSON key name to field type for a struct,
// honouring `json` tags and promoting embedded (anonymous) struct fields.
// A `json:"-"` field is excluded. A field with no tag uses its Go name.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	candidates := map[string][]fieldCandidate{}
	collectJSONFields(t, 0, candidates)
	out := make(map[string]reflect.Type, len(candidates))
	for name, cs := range candidates {
		if winner, ok := dominantField(cs); ok {
			out[name] = winner.typ
		}
	}
	return out
}

// fieldCandidate is one struct field competing for a JSON name. Depth 0 is the
// outer struct; each untagged embedded struct adds one.
type fieldCandidate struct {
	typ    reflect.Type
	depth  int
	tagged bool
}

// collectJSONFields gathers every candidate for every name, without resolving
// conflicts — resolution needs to see the whole set.
//
// The anonymous-field rules are measured against encoding/json, not assumed:
//
//	untagged embedded struct, exported OR unexported type -> promoted
//	tagged embedded struct,   exported OR unexported type -> a NAMED field
//	                                     under the tag, and still populated
//	embedded unexported scalar                            -> ignored
//
// An embedded NON-struct must not be recursed into at all: that panics with
// "reflect: NumField of non-struct type".
func collectJSONFields(t reflect.Type, depth int, out map[string][]fieldCandidate) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		ft := derefType(f.Type)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		embeddedStruct := f.Anonymous && ft.Kind() == reflect.Struct
		if embeddedStruct && tag == "" {
			collectJSONFields(ft, depth+1, out)
			continue
		}
		// Everything else is a named field. encoding/json never populates an
		// unexported one, and calling it modelled makes the walker report a
		// key as covered while the runtime drops it — a false-clean gate, the
		// exact failure this file exists to prevent. The exception is a tagged
		// embedded struct: its field name is its type name and so reads as
		// unexported, while the decoder still fills it.
		if !f.IsExported() && !embeddedStruct {
			continue
		}
		name, tagged := f.Name, false
		if tag != "" {
			if parts := strings.SplitN(tag, ",", 2); parts[0] != "" {
				name, tagged = parts[0], true
			}
		}
		out[name] = append(out[name], fieldCandidate{typ: f.Type, depth: depth, tagged: tagged})
	}
}

// dominantField applies encoding/json's rule for a contested name: the
// SHALLOWEST depth wins, a tagged field beats untagged ones at that depth, and
// a remaining tie means the key is DROPPED — encoding/json ignores it entirely
// rather than picking one.
//
// The last clause is the one that matters here. First-wins or last-wins both
// report a key as modelled that the decoder silently discards, which is a
// false-clean gate pointing the wrong way. Measured: struct{C1; C2} where both
// carry json:"x" decodes {"x":…} into NEITHER field, with no error.
func dominantField(cs []fieldCandidate) (fieldCandidate, bool) {
	shallowest := cs[0].depth
	for _, c := range cs[1:] {
		if c.depth < shallowest {
			shallowest = c.depth
		}
	}
	var atDepth, tagged []fieldCandidate
	for _, c := range cs {
		if c.depth != shallowest {
			continue
		}
		atDepth = append(atDepth, c)
		if c.tagged {
			tagged = append(tagged, c)
		}
	}
	if len(atDepth) == 1 {
		return atDepth[0], true
	}
	if len(tagged) == 1 {
		return tagged[0], true
	}
	return fieldCandidate{}, false
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// jsonTypeName returns the JSON type label for a generic-decoded value.
func jsonTypeName(v interface{}) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	default:
		return fmt.Sprintf("unknown(%T)", v)
	}
}

// countKeysBeneath returns the total number of JSON keys recursively contained
// in a generic-decoded value: an object counts its own keys plus the keys of
// every value; an array counts the keys of every element (array elements have
// no keys of their own); a scalar counts 0. Used to quantify how many keys are
// hidden beneath an unmodelled root whose subtree is dropped wholesale.
func countKeysBeneath(v interface{}) int {
	switch n := v.(type) {
	case map[string]interface{}:
		count := len(n)
		for _, val := range n {
			count += countKeysBeneath(val)
		}
		return count
	case []interface{}:
		count := 0
		for _, elem := range n {
			count += countKeysBeneath(elem)
		}
		return count
	default:
		return 0
	}
}

// shapeMismatch is a JSON container landing on a Go type that cannot receive
// it — an object on a slice, an array on a struct. encoding/json aborts the
// WHOLE decode on one of these, so unlike an unmodelled key it does not lose
// one field, it loses the response.
//
// This class is collected by the walker rather than by decoding because a
// decode reports only its FIRST error, and a custom UnmarshalJSON error is
// returned immediately — discarding the *json.UnmarshalTypeError that
// encoding/json had already saved. Measured on schedule_posts.json: drift in
// total_rows or is_has_more is invisible to a decode because a FlexInt
// placeholder error fires first. The walker never reaches those leaves, so it
// sees the mismatch regardless.
type shapeMismatch struct {
	path     string
	jsonKind string
	goType   string
}

// walkOut collects both classes the walker finds.
type walkOut struct {
	unmodelled []unmodelledKey
	mismatches []shapeMismatch
}

func (o *walkOut) mismatch(path, jsonKind string, t reflect.Type) {
	if path == "" {
		path = "<root>"
	}
	o.mismatches = append(o.mismatches, shapeMismatch{path: path, jsonKind: jsonKind, goType: t.String()})
}

// unmarshalerContract describes what a json.Unmarshaler in this package
// accepts, so the walker can classify a value it would otherwise have to skip.
//
// A hand-written table is the very thing this file refuses to trust elsewhere,
// so TestUnmarshalerContractsAreMeasured probes every entry against the real
// UnmarshalJSON and fails when a declaration and the code disagree. The table
// is a cache of a measurement, not a belief.
type unmarshalerContract struct {
	// traverse: the unmarshaller only accepts an alternate ENCODING of a
	// structure that is still fully modelled, so the walker must descend
	// instead of stopping. Treating such a type as a leaf drops everything
	// beneath it out of the diagnostic, silently and in the direction that
	// looks clean.
	traverse bool
	// acceptsEmptyArray: a JSON `[]` is legal here even though the Go kind is
	// a map. Without this the walker reports a mismatch on a body that decodes
	// perfectly — and the failure message tells the reader to change the
	// struct, which is the opposite of what is needed.
	acceptsEmptyArray bool
	// scalarOnly: containers are rejected outright, so an object or array here
	// is a real shape error the walker can name even though the value is a
	// leaf to it.
	scalarOnly bool
}

var unmarshalerContracts = map[reflect.Type]unmarshalerContract{
	reflect.TypeOf(ScheduleCalendar{}): {traverse: true, acceptsEmptyArray: true},
	reflect.TypeOf(FlexInt{}):          {scalarOnly: true},
	reflect.TypeOf(Metric{}):           {scalarOnly: true},
	reflect.TypeOf(PhotoID("")):        {scalarOnly: true},
	// Cross-posting integer enums (issue #51): accept a JSON number only;
	// encoding/json itself returns *json.UnmarshalTypeError for a container
	// passed where an int is expected, so scalarOnly is accurate. The decoded
	// name lives only on the Go value — the wire value is the raw integer.
	reflect.TypeOf(SearchMode{}):          {scalarOnly: true},
	reflect.TypeOf(SearchModeDirection{}): {scalarOnly: true},
	reflect.TypeOf(DetermineBestBy{}):     {scalarOnly: true},
	reflect.TypeOf(CheckWhenType{}):       {scalarOnly: true},
	reflect.TypeOf(CheckInterval{}):       {scalarOnly: true},
	// CrossPostingEditResponse is no longer leaf-skipped: the diagnostic
	// uses crossPostingEditWalker (same struct, no UnmarshalJSON) as the
	// walk target, so the walker descends into it and the unmodelled keys
	// are tracked in the baseline. The real CrossPostingEditResponse is
	// not reachable from liveFixtureSpecs, so no contract is needed here.
}

// walkJSON walks a generic-decoded JSON tree against a target Go struct type
// via reflection, collecting every JSON key that has no corresponding struct
// field. Honours json tags, embedded structs, map[string]… element types, and
// slices. Types implementing json.Unmarshaler (Metric, FlexInt, PhotoID) are
// treated as leaves — their custom unmarshaller owns the whole value, so
// there are no "unmodelled keys" below them. interface{} fields are leaves
// too (anything is accepted).
func walkJSON(node interface{}, targetType reflect.Type, path string, out *walkOut) {
	targetType = derefType(targetType)

	// A custom UnmarshalJSON owns the entire value — stop. Metric, FlexInt
	// and PhotoID declare UnmarshalJSON on a POINTER receiver, so a VALUE
	// field of those types does not satisfy the interface directly; test the
	// pointer type too so they are still recognised as leaves.
	//
	// EXCEPT for a type whose unmarshaller only tolerates an alternate
	// ENCODING of a structure it still models fully (ScheduleCalendar accepts
	// PHP's `[]` for an empty map). Treating those as leaves would drop
	// everything beneath them — all 24 keys of a scheduled Post — out of the
	// diagnostic's coverage, silently and in the direction that looks clean.
	contract := unmarshalerContracts[targetType]
	if !contract.traverse &&
		(targetType.Implements(jsonUnmarshalerType) || reflect.PointerTo(targetType).Implements(jsonUnmarshalerType)) {
		// A leaf — but a leaf with a declared contract can still be judged.
		if contract.scalarOnly {
			switch node.(type) {
			case map[string]interface{}:
				out.mismatch(path, "object", targetType)
			case []interface{}:
				out.mismatch(path, "array", targetType)
			}
		}
		return
	}

	switch n := node.(type) {
	case map[string]interface{}:
		switch targetType.Kind() {
		case reflect.Struct:
			fieldMap := jsonFields(targetType)
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				childPath := k
				if path != "" {
					childPath = path + "." + k
				}
				if ft, ok := fieldMap[k]; ok {
					walkJSON(n[k], ft, childPath, out)
				} else {
					out.unmodelled = append(out.unmodelled, unmodelledKey{
						path:        childPath,
						jsonType:    jsonTypeName(n[k]),
						keysBeneath: countKeysBeneath(n[k]),
					})
				}
			}
		case reflect.Map:
			// Map keys are dynamic — all modelled. Recurse into values.
			elemType := derefType(targetType.Elem())
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				childPath := k
				if path != "" {
					childPath = path + "." + k
				}
				walkJSON(n[k], elemType, childPath, out)
			}
		case reflect.Interface:
			// interface{} accepts anything — modelled, stop.
		default:
			// A JSON object cannot go into a slice, a string, an int.
			out.mismatch(path, "object", targetType)
		}
	case []interface{}:
		switch targetType.Kind() {
		case reflect.Slice, reflect.Array:
			elemType := derefType(targetType.Elem())
			for i, elem := range n {
				walkJSON(elem, elemType, fmt.Sprintf("%s[%d]", path, i), out)
			}
		case reflect.Interface:
			// interface{} accepts anything — modelled, stop.
		default:
			// A JSON array cannot go into a struct, a map, a scalar — UNLESS
			// the target declared that it accepts the EMPTY one. Without that
			// exemption the walker contradicts ScheduleCalendar.UnmarshalJSON
			// and reports a mismatch on a body that decodes perfectly, with a
			// message telling the reader to change the struct.
			// json.RawMessage never reaches here: it implements Unmarshaler
			// and is stopped by the leaf rule above.
			if contract.acceptsEmptyArray && len(n) == 0 {
				break
			}
			out.mismatch(path, "array", targetType)
		}
	case nil:
		// JSON null goes into anything — stop.
	default:
		// A JSON scalar cannot go into a struct, a map or a slice. The
		// converse — a scalar on the WRONG scalar kind, a string on an int —
		// is deliberately not reported: FlexInt-style tolerance makes that
		// legitimate here, and this walker only claims the unambiguous class.
		switch targetType.Kind() {
		case reflect.Struct, reflect.Map, reflect.Array:
			out.mismatch(path, jsonTypeName(node), targetType)
		case reflect.Slice:
			// []byte is exempt: encoding/json decodes a base64 STRING into it.
			// The exemption belongs to THIS arm only, and only for a string.
			// Measured 2026-07-30: []byte also accepts a JSON ARRAY of numbers
			// ([1,2] -> [1 2]), so the array arm must keep recursing and must
			// NOT report a mismatch there.
			if targetType.Elem().Kind() != reflect.Uint8 {
				out.mismatch(path, jsonTypeName(node), targetType)
			}
		}
	}
}

// unmodelledKeys parses a fixture and returns every unmodelled key with its
// JSON path and JSON type.
func unmodelledKeys(data []byte, targetType reflect.Type) []unmodelledKey {
	var root interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return []unmodelledKey{{path: "<parse-error>", jsonType: err.Error()}}
	}
	var out walkOut
	walkJSON(root, targetType, "", &out)
	return out.unmodelled
}

// shapeMismatches parses a fixture and returns every container whose JSON kind
// the target type cannot hold. See shapeMismatch for why this is not done by
// decoding.
func shapeMismatches(data []byte, targetType reflect.Type) []shapeMismatch {
	var root interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	var out walkOut
	walkJSON(root, targetType, "", &out)
	return out.mismatches
}

// pathExistsInFixture checks whether a dotted JSON path (with [i] array
// indices) exists in the generic-decoded fixture. Used to distinguish a stale
// declaration where the key is now modelled (path still in fixture) from one
// where the key is no longer present (path gone).
func pathExistsInFixture(root interface{}, path string) bool {
	return resolvePath(root, path) != nil
}

// resolvePath navigates a generic-decoded JSON tree by a dotted path with
// [i] array indices. Returns nil if any segment is absent.
func resolvePath(node interface{}, path string) interface{} {
	rest := path
	cur := node
	for rest != "" {
		// Array index segment: [N]
		if rest[0] == '[' {
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil
			}
			idxStr := rest[1:end]
			idx := 0
			for _, c := range idxStr {
				if c < '0' || c > '9' {
					return nil
				}
				idx = idx*10 + int(c-'0')
			}
			arr, ok := cur.([]interface{})
			if !ok || idx >= len(arr) {
				return nil
			}
			cur = arr[idx]
			rest = rest[end+1:]
			// After an array index the remainder begins with '.' (e.g.
			// "list[0].access_token"); strip it so the object-key segment
			// below does not see an empty key. A following '[' (nested
			// arrays) or end-of-path needs no strip.
			if len(rest) > 0 && rest[0] == '.' {
				rest = rest[1:]
			}
			continue
		}
		// Object key segment: key or key.rest
		dot := strings.IndexByte(rest, '.')
		bracket := strings.IndexByte(rest, '[')
		var seg string
		switch {
		case dot < 0 && bracket < 0:
			seg = rest
			rest = ""
		case dot < 0:
			seg = rest[:bracket]
			rest = rest[bracket:]
		case bracket < 0:
			seg = rest[:dot]
			rest = rest[dot+1:]
		default:
			if dot < bracket {
				seg = rest[:dot]
				rest = rest[dot+1:]
			} else {
				seg = rest[:bracket]
				rest = rest[bracket:]
			}
		}
		obj, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		val, exists := obj[seg]
		if !exists {
			return nil
		}
		cur = val
	}
	return cur
}

type fixtureSpec struct {
	file      string
	endpoint  string
	targetTyp reflect.Type
}

// liveFixtureSpecs maps each recorded fixture to the Go response type its
// endpoint decodes into. The type is the one the production doGET call site
// passes to do/doWithRetry (the bare json.Unmarshal target).
var liveFixtureSpecs = []fixtureSpec{
	{"accounts.json", "GET /accounts", reflect.TypeOf(AccountsResponse{})},
	{"accounts_pages.json", "GET /accounts/pages", reflect.TypeOf(PagesResponse{})},
	{"projects.json", "GET /posts/projects", reflect.TypeOf(ProjectsResponse{})},
	{"schedules.json", "GET /posts/schedules", reflect.TypeOf(SchedulesResponse{})},
	{"posts.json", "GET /posts", reflect.TypeOf(PostsResponse{})},
	{"posts_search.json", "GET /posts-search", reflect.TypeOf(SearchPostsResponse{})},
	{"source_resources.json", "GET /posts-search/source-resources", reflect.TypeOf(SourceResourcesResponse{})},
	{"parsing_form.json", "GET /posts-search/parsing/form", reflect.TypeOf(ParsingFormResponse{})},
	{"proxies.json", "GET /proxies", reflect.TypeOf(ProxiesResponse{})},
	{"watermarks.json", "GET /watermarks", reflect.TypeOf(WatermarksResponse{})},
	{"notifications.json", "GET /notifications", reflect.TypeOf(NotificationsResponse{})},
	{"users_me.json", "GET /users/me", reflect.TypeOf(UserResponse{})},
	{"users_settings.json", "GET /users/settings", reflect.TypeOf(SettingsResponse{})},
	{"schedule_edit.json", "GET /posts/schedules/{id}/edit", reflect.TypeOf(ScheduleEditResponse{})},
	{"post_edit.json", "GET /posts/{id}/edit", reflect.TypeOf(PostEditResponse{})},
	{"search_post_edit.json", "GET /posts-search/{id}/edit", reflect.TypeOf(SearchPostEditResponse{})},
	{"schedule_posts.json", "GET /posts/schedules/{id}/posts", reflect.TypeOf(SchedulePostsResponse{})},
	// The empty-calendar capture pins the SHAPE only: the reduction replaces
	// every scalar with a placeholder, so its total_rows is 0 rather than the 96
	// the live page-past-the-end returned. It carries the `[]` form through the
	// whole gate; the total_rows-survives-an-overrun property is asserted
	// separately, against the raw body.
	{"schedule_posts_empty.json", "GET /posts/schedules/{id}/posts (empty)", reflect.TypeOf(SchedulePostsResponse{})},
	// Cross-posting rule engine (the /cross-posting subsystem, #57) — UNDOCUMENTED.
	{"cross_postings.json", "GET /cross-posting", reflect.TypeOf(CrossPostingsResponse{})},
	// crossPostingEditWalker is CrossPostingEditResponse WITHOUT the custom
	// UnmarshalJSON — a named type with the same underlying struct but no
	// methods, so the walker does NOT leaf-skip it and can see its modelled
	// vs unmodelled keys. CrossPostingEditResponse.UnmarshalJSON stashes Raw
	// and decodes typed fields; the walker needs to see the typed fields to
	// detect a newly-modelled key going stale (F2: remove is_search_started
	// → the walker reports it as unmodelled → RED). The Raw field (json:"-")
	// is invisible to the walker; the json.RawMessage fields (PostsHashtags
	// etc.) are exempt leaves. The ~69 unmodelled keys are in the baseline.
	{"cross_posting_edit.json", "GET /cross-posting/{id}/edit", reflect.TypeOf(crossPostingEditWalker{})},
	{"cross_posting_statistics.json", "GET /cross-posting/{id}/statistics", reflect.TypeOf(CrossPostingStatisticsResponse{})},
	{"cross_posting_statistics_empty.json", "GET /cross-posting/{id}/statistics (empty)", reflect.TypeOf(CrossPostingStatisticsResponse{})},
}

// crossPostingEditWalker is CrossPostingEditResponse with the custom
// UnmarshalJSON/MarshalJSON stripped — a named type with the same underlying
// struct but no methods. The diagnostic walker does NOT leaf-skip it, so it
// can see which keys the struct models vs which are unmodelled (preserved in
// Raw). This is what makes F2 possible: remove a modelled field from the
// struct and the walker reports it as unmodelled → RED.
type crossPostingEditWalker CrossPostingEditResponse

// unmodelledBaselines is the declared baseline: for each endpoint, the set of
// JSON keys the fixture carries that the response struct does NOT model,
// keyed by JSON path and valued by JSON type. The gate test fails on any
// divergence:
//
//   - a key in the fixture but not here → newly-appeared, unclassified server field.
//   - a key here but now modelled by a struct field → stale declaration (struct grew).
//   - a key here but no longer in the fixture → stale declaration (fixture changed).
//
// Some exclusions are deliberate (User omits api_token and other credentials;
// SettingsResponse omits api_token, gpt_key, ru_captcha_key) and MUST stay —
// the baseline records them so a future struct change that models them is
// flagged for review. Do NOT model these fields in this PR; the point is that
// the list becomes explicit and enforced, not that it becomes empty.
//
// Generated by walking each fixture against its response type via reflection
// (see walkJSON). This is the DIRECT count — one entry per unmodelled key at
// the depth the struct models. When an unmodelled key's value is an object or
// non-empty array, its subtree is NOT enumerated here (it is dropped wholesale
// at decode); the test logs how many keys are hidden beneath those roots so
// the direct count is not mistaken for a total. 2026-07-29 fixtures.
var unmodelledBaselines = map[string]map[string]string{
	"GET /accounts": {
		"list[0].access_token":                             "string",
		"list[0].access_token_last_updated_date":           "number",
		"list[0].access_token_last_updated_proxy":          "null",
		"list[0].access_token_secret":                      "string",
		"list[0].access_web_token":                         "string",
		"list[0].access_web_token_last_updated_date":       "number",
		"list[0].access_web_token_last_updated_proxy":      "null",
		"list[0].access_web_token_last_updated_user_agent": "null",
		"list[0].account_type":                             "number",
		"list[0].additional_info":                          "null",
		"list[0].bot_token":                                "string",
		"list[0].client_id":                                "string",
		"list[0].clips_errors_count":                       "number",
		"list[0].clips_flood_date_from":                    "null",
		"list[0].connection_checked":                       "number",
		"list[0].device_seed_prefix":                       "string",
		"list[0].email":                                    "null",
		"list[0].is_private":                               "number",
		"list[0].jm_token":                                 "null",
		"list[0].ownership":                                "number",
		"list[0].parent_account_id":                        "number",
		"list[0].parent_page_id":                           "number",
		"list[0].password":                                 "null",
		"list[0].phone":                                    "null",
		"list[0].pinterest_last_publish_operation_date":    "number",
		"list[0].pinterest_publish_operations_count":       "number",
		"list[0].pinterest_publish_operations_limit":       "number",
		"list[0].proxy_id":                                 "number",
		"list[0].proxy_type":                               "number",
		"list[0].rate_limit":                               "null",
		"list[0].refresh_token":                            "string",
		"list[0].social_account_alias":                     "string",
		"list[0].social_account_link":                      "string",
		"list[0].social_account_privacy":                   "null",
		"list[0].social_account_type":                      "string",
		"list[0].status":                                   "number",
		"list[0].thread_id":                                "number",
		"list[0].thread_name":                              "null",
		"list[0].tw_app_id":                                "string",
		"list[0].tw_app_secret":                            "string",
		"list[0].tw_last_oauth_token":                      "null",
		"list[0].user_id":                                  "number",
		"list[0].vk_clips_token":                           "null",
		"list[0].wa_api_type":                              "number",
		"list[0].wa_instance_api_token":                    "null",
		"list[0].wa_instance_id":                           "null",
		"list[0].wp_app_password":                          "null",
		"list[0].wp_profile_login":                         "null",
		"list[0].youtube_last_publish_operation_date":      "number",
		"list[0].youtube_publish_operations_count":         "number",
	},
	"GET /accounts/pages": {
		"list[0].access_token":                "string",
		"list[0].access_token_secret":         "string",
		"list[0].account":                     "object",
		"list[0].additional_clips_caption":    "null",
		"list[0].channel_url":                 "null",
		"list[0].clips_flood_date_from":       "number",
		"list[0].combine_big_text_with_photo": "number",
		"list[0].custom_account_id":           "number",
		"list[0].is_admin":                    "number",
		"list[0].photos_crop_type":            "number",
		"list[0].social_account_id":           "string",
		"list[0].social_page_alias":           "string",
		"list[0].social_page_link":            "string",
		"list[0].social_page_type":            "string",
		"list[0].user_id":                     "number",
		"list[0].utm_tags":                    "string",
		"list[0].watermark_id":                "number",
	},
	"GET /posts/projects": {
		"list[0].add_link_to_user":                 "number",
		"list[0].delete_posts_day":                 "number",
		"list[0].delete_posts_hour":                "number",
		"list[0].donut_paid_duration":              "number",
		"list[0].download_vk_videos":               "number",
		"list[0].expand_clips_title":               "number",
		"list[0].is_comments_disabled":             "number",
		"list[0].is_deleted":                       "number",
		"list[0].is_unique_content":                "number",
		"list[0].message_to_channel":               "number",
		"list[0].message_to_community":             "number",
		"list[0].not_publish_in_videos":            "number",
		"list[0].pages":                            "array",
		"list[0].parse_links":                      "number",
		"list[0].photos_caption":                   "string",
		"list[0].plan_by_network":                  "number",
		"list[0].position":                         "number",
		"list[0].posts_caption":                    "string",
		"list[0].posts_caption_position_type":      "number",
		"list[0].posts_caption_space_type":         "number",
		"list[0].posts_comment":                    "string",
		"list[0].posts_count":                      "number",
		"list[0].posts_location":                   "string",
		"list[0].posts_location_vk":                "string",
		"list[0].posts_photo":                      "string",
		"list[0].posts_photo_always":               "number",
		"list[0].posts_rewrite":                    "string",
		"list[0].privacy_level":                    "number",
		"list[0].publication_where_type":           "number",
		"list[0].publish_as_article":               "number",
		"list[0].publish_as_article_by_link":       "number",
		"list[0].publish_as_carousel":              "number",
		"list[0].publish_as_clips":                 "number",
		"list[0].publish_as_reels":                 "number",
		"list[0].publish_as_shorts":                "number",
		"list[0].publish_as_story":                 "number",
		"list[0].publish_as_story_source_ids":      "string",
		"list[0].publish_as_user":                  "number",
		"list[0].publish_by_account":               "number",
		"list[0].publish_by_account_source_ids":    "string",
		"list[0].publish_comment_by_account":       "number",
		"list[0].publish_in_channel":               "number",
		"list[0].publish_only_in_videos":           "number",
		"list[0].publish_reels_as_trial":           "number",
		"list[0].repeat_video":                     "number",
		"list[0].save_vk_videos_names":             "number",
		"list[0].share_channel_to_feed":            "number",
		"list[0].share_clips_to_feed":              "number",
		"list[0].share_clips_to_feed_if_no_video":  "number",
		"list[0].share_clips_to_feed_with_text":    "number",
		"list[0].share_reels_to_feed":              "number",
		"list[0].share_shorts_to_feed":             "null",
		"list[0].share_stories_to_feed":            "number",
		"list[0].share_stories_to_feed_source_ids": "string",
		"list[0].tg_buttons":                       "string",
		"list[0].user_id":                          "number",
		"list[0].utm_tags":                         "string",
		"list[0].videos_title":                     "string",
		"list[0].watermark_id":                     "number",
		"list[0].youtube_category":                 "number",
	},
	"GET /posts/schedules": {
		"list[0].add_link_to_user":                 "number",
		"list[0].ads_times":                        "array",
		"list[0].delete_posts_day":                 "number",
		"list[0].delete_posts_hour":                "number",
		"list[0].donut_paid_duration":              "number",
		"list[0].download_vk_videos":               "number",
		"list[0].expand_clips_title":               "number",
		"list[0].is_comments_disabled":             "number",
		"list[0].is_posts_repeated":                "number",
		"list[0].is_random_content":                "number",
		"list[0].is_unique_content":                "number",
		"list[0].message_to_channel":               "null",
		"list[0].message_to_community":             "null",
		"list[0].not_publish_in_videos":            "null",
		"list[0].pages":                            "array",
		"list[0].parse_links":                      "number",
		"list[0].photos_caption":                   "string",
		"list[0].plan_by_network":                  "number",
		"list[0].posts_caption":                    "string",
		"list[0].posts_caption_position_type":      "number",
		"list[0].posts_caption_space_type":         "number",
		"list[0].posts_comment":                    "string",
		"list[0].posts_count":                      "number",
		"list[0].posts_location":                   "string",
		"list[0].posts_location_vk":                "null",
		"list[0].posts_photo":                      "string",
		"list[0].posts_photo_always":               "null",
		"list[0].posts_rewrite":                    "null",
		"list[0].privacy_level":                    "number",
		"list[0].publish_as_article":               "number",
		"list[0].publish_as_article_by_link":       "number",
		"list[0].publish_as_carousel":              "number",
		"list[0].publish_as_clips":                 "number",
		"list[0].publish_as_reels":                 "number",
		"list[0].publish_as_shorts":                "number",
		"list[0].publish_as_story":                 "number",
		"list[0].publish_as_story_source_ids":      "string",
		"list[0].publish_as_user":                  "number",
		"list[0].publish_by_account":               "number",
		"list[0].publish_by_account_source_ids":    "string",
		"list[0].publish_comment_by_account":       "null",
		"list[0].publish_in_channel":               "null",
		"list[0].publish_only_in_videos":           "number",
		"list[0].publish_reels_as_trial":           "null",
		"list[0].repeat_video":                     "number",
		"list[0].save_vk_videos_names":             "number",
		"list[0].share_channel_to_feed":            "null",
		"list[0].share_clips_to_feed":              "number",
		"list[0].share_clips_to_feed_if_no_video":  "null",
		"list[0].share_clips_to_feed_with_text":    "number",
		"list[0].share_reels_to_feed":              "number",
		"list[0].share_shorts_to_feed":             "null",
		"list[0].share_stories_to_feed":            "number",
		"list[0].share_stories_to_feed_source_ids": "string",
		"list[0].tg_buttons":                       "null",
		"list[0].utm_tags":                         "string",
		"list[0].videos_title":                     "string",
		"list[0].watermark_id":                     "number",
		"list[0].youtube_category":                 "number",
	},
	"GET /posts": {
		"all_script_time":               "number",
		"filters_plug":                  "array",
		"posts_time":                    "number",
		"selected_filters_placeholders": "array",
	},
	"GET /posts-search": {
		"list[0].comments_source":       "number",
		"list[0].likes_source":          "number",
		"list[0].reposts_source":        "number",
		"list[0].views_source":          "number",
		"selected_filters_placeholders": "array",
	},
	"GET /posts-search/source-resources": {},
	"GET /posts-search/parsing/form":     {},
	// Cross-posting rule engine (the /cross-posting subsystem, #57) — UNDOCUMENTED.
	// The list row carries 89 keys; CrossPosting models the operator-facing
	// subset (identity, state, the five enums, the four thresholds,
	// take_amount, the check schedule, last_check_date and
	// instagram_last_check_date as FlexInt). The 69 keys below (68 list-row +
	// filters_plug top-level) are the publication-format toggles, captions,
	// scheduling knobs, and source metadata the read surface does not need.
	// The /edit response is walked via crossPostingEditWalker (same struct,
	// no UnmarshalJSON) so the walker descends into it; 74 keys are
	// unmodelled (preserved in Raw for the lossless round-trip). Statistics
	// has 0 unmodelled (6/6 day fields modelled). 2026-07-31 fixtures.
	"GET /cross-posting": {
		"filters_plug":                             "array",
		"list[0].add_link_to_user":                 "null",
		"list[0].check_step":                       "number",
		"list[0].check_times":                      "null",
		"list[0].copy_mode":                        "number",
		"list[0].delete_posts_day":                 "null",
		"list[0].delete_posts_hour":                "null",
		"list[0].donut_paid_duration":              "number",
		"list[0].download_vk_videos":               "null",
		"list[0].expand_clips_title":               "null",
		"list[0].instagram_ready_for_parse":        "number",
		"list[0].is_comments_disabled":             "null",
		"list[0].is_unique_content":                "null",
		"list[0].message_to_channel":               "null",
		"list[0].message_to_community":             "null",
		"list[0].not_publish_in_videos":            "null",
		"list[0].pages":                            "array",
		"list[0].parse_links":                      "null",
		"list[0].photos_caption":                   "string",
		"list[0].plan_by_network":                  "null",
		"list[0].posts_caption":                    "string",
		"list[0].posts_caption_position_type":      "number",
		"list[0].posts_caption_space_type":         "number",
		"list[0].posts_comment":                    "null",
		"list[0].posts_location":                   "string",
		"list[0].posts_location_vk":                "null",
		"list[0].posts_pagination":                 "null",
		"list[0].posts_photo":                      "null",
		"list[0].posts_photo_always":               "null",
		"list[0].posts_rewrite":                    "null",
		"list[0].privacy_level":                    "number",
		"list[0].publication_how_type":             "number",
		"list[0].publication_interval":             "number",
		"list[0].publication_interval_from":        "number",
		"list[0].publication_interval_to":          "number",
		"list[0].publication_interval_type":        "number",
		"list[0].publication_when_type":            "number",
		"list[0].publication_where_type":           "number",
		"list[0].publish_as_article":               "null",
		"list[0].publish_as_article_by_link":       "null",
		"list[0].publish_as_carousel":              "null",
		"list[0].publish_as_clips":                 "null",
		"list[0].publish_as_reels":                 "null",
		"list[0].publish_as_shorts":                "number",
		"list[0].publish_as_story":                 "null",
		"list[0].publish_as_story_source_ids":      "string",
		"list[0].publish_as_user":                  "null",
		"list[0].publish_by_account":               "null",
		"list[0].publish_by_account_source_ids":    "string",
		"list[0].publish_comment_by_account":       "null",
		"list[0].publish_in_channel":               "null",
		"list[0].publish_only_in_videos":           "null",
		"list[0].publish_reels_as_trial":           "null",
		"list[0].repeat_video":                     "null",
		"list[0].save_vk_videos_names":             "number",
		"list[0].search_pagination":                "null",
		"list[0].search_with_pagination":           "number",
		"list[0].share_channel_to_feed":            "null",
		"list[0].share_clips_to_feed":              "null",
		"list[0].share_clips_to_feed_if_no_video":  "null",
		"list[0].share_clips_to_feed_with_text":    "null",
		"list[0].share_reels_to_feed":              "null",
		"list[0].share_shorts_to_feed":             "null",
		"list[0].share_stories_to_feed":            "null",
		"list[0].share_stories_to_feed_source_ids": "string",
		"list[0].tg_buttons":                       "null",
		"list[0].videos_title":                     "null",
		"list[0].watermark_id":                     "number",
		"list[0].youtube_category":                 "number",
	},
	// 74 unmodelled keys (95 fixture - 25 modelled = 74). Walked via
	// crossPostingEditWalker (same struct, no UnmarshalJSON) so the walker
	// descends. These keys survive in Raw for the lossless round-trip.
	"GET /cross-posting/{id}/edit": {
		"accounts_for_parsing":             "object",
		"add_link_to_user":                 "number",
		"check_times":                      "array",
		"copy_mode":                        "number",
		"delete_posts_day":                 "number",
		"delete_posts_hour":                "number",
		"donut_paid_duration":              "number",
		"download_vk_videos":               "number",
		"expand_clips_title":               "number",
		"is_comments_disabled":             "number",
		"is_unique_content":                "number",
		"message_to_channel":               "number",
		"message_to_community":             "number",
		"not_publish_in_videos":            "number",
		"parse_links":                      "number",
		"photos_caption":                   "string",
		"plan_by_network":                  "number",
		"posts_caption":                    "string",
		"posts_caption_position_type":      "number",
		"posts_caption_space_type":         "number",
		"posts_comment":                    "string",
		"posts_filter":                     "object",
		"posts_location":                   "object",
		"posts_location_vk":                "object",
		"posts_photo":                      "null",
		"posts_photo_always":               "number",
		"posts_rewrite":                    "object",
		"posts_text":                       "object",
		"posts_upgrade":                    "object",
		"privacy_level":                    "number",
		"projects":                         "array",
		"publication_how_type":             "number",
		"publication_interval":             "number",
		"publication_interval_from":        "number",
		"publication_interval_to":          "number",
		"publication_interval_type":        "number",
		"publication_when_type":            "number",
		"publication_where_type":           "number",
		"publish_as_article":               "number",
		"publish_as_article_by_link":       "number",
		"publish_as_carousel":              "number",
		"publish_as_clips":                 "number",
		"publish_as_reels":                 "number",
		"publish_as_shorts":                "number",
		"publish_as_story":                 "number",
		"publish_as_story_source_ids":      "string",
		"publish_as_user":                  "number",
		"publish_by_account":               "number",
		"publish_by_account_source_ids":    "string",
		"publish_comment_by_account":       "number",
		"publish_in_channel":               "number",
		"publish_only_in_videos":           "number",
		"publish_reels_as_trial":           "number",
		"repeat_video":                     "number",
		"save_vk_videos_names":             "number",
		"schedule_id":                      "number",
		"schedules":                        "array",
		"selected_albums_by_source_ids":    "object",
		"selected_pages_by_source_ids":     "object",
		"share_channel_to_feed":            "number",
		"share_clips_to_feed":              "number",
		"share_clips_to_feed_if_no_video":  "number",
		"share_clips_to_feed_with_text":    "number",
		"share_reels_to_feed":              "number",
		"share_stories_to_feed":            "number",
		"share_stories_to_feed_source_ids": "string",
		"social_albums_by_pages":           "array",
		"social_pages_by_accounts":         "array",
		"source_resources":                 "array",
		"tg_buttons":                       "object",
		"videos_title":                     "string",
		"watermark_id":                     "number",
		"watermarks":                       "array",
		"youtube_category":                 "number",
	},
	"GET /cross-posting/{id}/statistics":         {},
	"GET /cross-posting/{id}/statistics (empty)": {},
	"GET /proxies":    {},
	"GET /watermarks": {},
	"GET /notifications": {
		"filters_plug":                  "array",
		"list[0].page":                  "object",
		"selected_filters_placeholders": "array",
	},
	"GET /users/me": {
		"user.api_token":                          "string",
		"user.coupon_code":                        "null",
		"user.current_video_traffic_usage":        "number",
		"user.director_id":                        "number",
		"user.email_to_confirm":                   "null",
		"user.email_verify_token":                 "null",
		"user.gpt_key":                            "null",
		"user.gpt_type":                           "number",
		"user.gpt_version":                        "number",
		"user.is_add_author_link":                 "number",
		"user.is_add_copyright":                   "number",
		"user.is_commit_delete_for_planned_posts": "number",
		"user.is_commit_edit_for_planned_posts":   "number",
		"user.is_delete_used_posts_from_search":   "number",
		"user.is_has_news":                        "number",
		"user.is_has_notifications":               "number",
		"user.is_has_publication_notifications":   "number",
		"user.is_has_special_offer":               "number",
		"user.is_keep_passwords":                  "number",
		"user.is_parse_instagram_posts_by_us":     "number",
		"user.is_pinterest_activated":             "number",
		"user.is_private_offers":                  "number",
		"user.is_save_posts_settings":             "number",
		"user.is_save_source_photos_links":        "number",
		"user.is_save_source_videos_names":        "number",
		"user.is_show_errors":                     "number",
		"user.is_vk_extended_activated":           "number",
		"user.last_payment_date":                  "number",
		"user.ord":                                "string",
		"user.partner_balance":                    "number",
		"user.pro_subscription_days":              "number",
		"user.read_more_lang":                     "number",
		"user.registration_domain":                "string",
		"user.registration_ip":                    "null",
		"user.ru_captcha_key":                     "null",
		"user.start_subscription_days":            "number",
		"user.subscriptions":                      "null",
		"user.temporary_code_for_dzen":            "null",
		"user.temporary_code_for_tenchat":         "null",
		"user.temporary_code_for_vkontakte":       "null",
		"user.temporary_code_for_yappy":           "null",
		"user.temporary_email_code_for_yappy":     "null",
		"user.temporary_email_for_yappy":          "null",
		"user.temporary_password_for_max":         "null",
		"user.temporary_password_for_telegram":    "null",
		"user.video_quality":                      "number",
		"user.video_traffic":                      "number",
		"user.vip_2_subscription_days":            "number",
		"user.vip_3_subscription_days":            "number",
		"user.vip_4_subscription_days":            "number",
		"user.vip_5_subscription_days":            "number",
		"user.vip_6_subscription_days":            "number",
		"user.vip_7_subscription_days":            "number",
		"user.vip_subscription_days":              "number",
	},
	"GET /users/settings": {
		"api_token":                          "string",
		"coupon_code":                        "null",
		"director":                           "null",
		"gpt_key":                            "null",
		"gpt_type":                           "number",
		"gpt_version":                        "number",
		"id":                                 "number",
		"is_commit_delete_for_planned_posts": "number",
		"is_commit_edit_for_planned_posts":   "number",
		"is_has_email":                       "boolean",
		"is_has_news":                        "number",
		"is_has_notifications":               "number",
		"is_has_publication_notifications":   "number",
		"is_has_special_offer":               "number",
		"is_keep_passwords":                  "number",
		"is_parse_instagram_posts_by_us":     "number",
		"is_private_offers":                  "number",
		"is_save_posts_settings":             "number",
		"is_vk_extended_activated":           "number",
		"navigation":                         "array",
		"partners_count":                     "number",
		"plan_type":                          "number",
		"posts_count":                        "number",
		"pro_subscription_days":              "number",
		"ru_captcha_key":                     "null",
		"start_subscription_days":            "number",
		"subscriptions":                      "null",
		"vip_2_subscription_days":            "number",
		"vip_3_subscription_days":            "number",
		"vip_4_subscription_days":            "number",
		"vip_5_subscription_days":            "number",
		"vip_6_subscription_days":            "number",
		"vip_7_subscription_days":            "number",
		"vip_subscription_days":              "number",
	},
	"GET /posts/schedules/{id}/edit": {
		"add_link_to_user":                             "number",
		"delete_posts_day":                             "number",
		"delete_posts_hour":                            "number",
		"donut_paid_duration":                          "number",
		"download_vk_videos":                           "number",
		"expand_clips_title":                           "number",
		"is_comments_disabled":                         "number",
		"is_posts_repeated":                            "number",
		"is_random_content":                            "number",
		"is_unique_content":                            "number",
		"message_to_channel":                           "number",
		"message_to_community":                         "number",
		"not_publish_in_videos":                        "number",
		"parse_links":                                  "number",
		"photos_caption":                               "string",
		"plan_by_network":                              "number",
		"posts_caption":                                "string",
		"posts_caption_position_type":                  "number",
		"posts_caption_space_type":                     "number",
		"posts_comment":                                "string",
		"posts_location":                               "null",
		"posts_location_vk":                            "object",
		"posts_photo":                                  "null",
		"posts_photo_always":                           "number",
		"posts_rewrite":                                "object",
		"privacy_level":                                "number",
		"projects[0].add_link_to_user":                 "number",
		"projects[0].delete_posts_day":                 "number",
		"projects[0].delete_posts_hour":                "number",
		"projects[0].donut_paid_duration":              "number",
		"projects[0].download_vk_videos":               "number",
		"projects[0].expand_clips_title":               "number",
		"projects[0].is_comments_disabled":             "number",
		"projects[0].is_deleted":                       "number",
		"projects[0].is_unique_content":                "number",
		"projects[0].message_to_channel":               "number",
		"projects[0].message_to_community":             "number",
		"projects[0].not_publish_in_videos":            "number",
		"projects[0].parse_links":                      "number",
		"projects[0].photos_caption":                   "string",
		"projects[0].plan_by_network":                  "number",
		"projects[0].position":                         "number",
		"projects[0].posts_caption":                    "string",
		"projects[0].posts_caption_position_type":      "number",
		"projects[0].posts_caption_space_type":         "number",
		"projects[0].posts_comment":                    "string",
		"projects[0].posts_count":                      "number",
		"projects[0].posts_location":                   "string",
		"projects[0].posts_location_vk":                "string",
		"projects[0].posts_photo":                      "string",
		"projects[0].posts_photo_always":               "number",
		"projects[0].posts_rewrite":                    "string",
		"projects[0].privacy_level":                    "number",
		"projects[0].publication_where_type":           "number",
		"projects[0].publish_as_article":               "number",
		"projects[0].publish_as_article_by_link":       "number",
		"projects[0].publish_as_carousel":              "number",
		"projects[0].publish_as_clips":                 "number",
		"projects[0].publish_as_reels":                 "number",
		"projects[0].publish_as_shorts":                "number",
		"projects[0].publish_as_story":                 "number",
		"projects[0].publish_as_story_source_ids":      "string",
		"projects[0].publish_as_user":                  "number",
		"projects[0].publish_by_account":               "number",
		"projects[0].publish_by_account_source_ids":    "string",
		"projects[0].publish_comment_by_account":       "number",
		"projects[0].publish_in_channel":               "number",
		"projects[0].publish_only_in_videos":           "number",
		"projects[0].publish_reels_as_trial":           "number",
		"projects[0].repeat_video":                     "number",
		"projects[0].save_vk_videos_names":             "number",
		"projects[0].share_channel_to_feed":            "number",
		"projects[0].share_clips_to_feed":              "number",
		"projects[0].share_clips_to_feed_if_no_video":  "number",
		"projects[0].share_clips_to_feed_with_text":    "number",
		"projects[0].share_reels_to_feed":              "number",
		"projects[0].share_shorts_to_feed":             "null",
		"projects[0].share_stories_to_feed":            "number",
		"projects[0].share_stories_to_feed_source_ids": "string",
		"projects[0].tg_buttons":                       "string",
		"projects[0].user_id":                          "number",
		"projects[0].utm_tags":                         "string",
		"projects[0].videos_title":                     "string",
		"projects[0].watermark_id":                     "number",
		"projects[0].youtube_category":                 "number",
		"publication_how_type":                         "number",
		"publication_where_type":                       "number",
		"publish_as_article":                           "number",
		"publish_as_article_by_link":                   "number",
		"publish_as_carousel":                          "number",
		"publish_as_clips":                             "number",
		"publish_as_reels":                             "number",
		"publish_as_shorts":                            "number",
		"publish_as_story":                             "number",
		"publish_as_story_source_ids":                  "string",
		"publish_as_user":                              "number",
		"publish_by_account":                           "number",
		"publish_by_account_source_ids":                "string",
		"publish_comment_by_account":                   "number",
		"publish_in_channel":                           "number",
		"publish_only_in_videos":                       "number",
		"publish_reels_as_trial":                       "number",
		"repeat_video":                                 "number",
		"save_vk_videos_names":                         "number",
		"share_channel_to_feed":                        "number",
		"share_clips_to_feed":                          "number",
		"share_clips_to_feed_if_no_video":              "number",
		"share_clips_to_feed_with_text":                "number",
		"share_reels_to_feed":                          "number",
		"share_stories_to_feed":                        "number",
		"share_stories_to_feed_source_ids":             "string",
		"start_date":                                   "object",
		"state":                                        "number",
		"stop_date":                                    "object",
		"tg_buttons":                                   "object",
		"utm_tags":                                     "string",
		"videos_title":                                 "string",
		"watermark_id":                                 "number",
		"youtube_category":                             "number",
	},
	"GET /posts/{id}/edit": {
		"attachments[0].id":             "number",
		"attachments[0].post_id":        "number",
		"ord_contracts":                 "array",
		"projects":                      "array",
		"publication_dates":             "object",
		"schedules":                     "array",
		"selected_albums_by_source_ids": "object",
		"social_albums_by_pages":        "array",
		"social_pages_by_accounts":      "array",
		"texts[0].id":                   "number",
		"texts[0].post_id":              "number",
	},
	"GET /posts-search/{id}/edit": {
		"all_pages_ids_by_source_ids":   "object",
		"ord_contracts":                 "array",
		"project_id":                    "number",
		"projects":                      "array",
		"schedule_id":                   "number",
		"schedules":                     "array",
		"selected_albums_by_source_ids": "object",
		"selected_pages_by_source_ids":  "object",
		"social_albums_by_pages":        "array",
		"social_pages_by_accounts":      "array",
	},
}

// formatUnmodelledKey renders one unmodelled key for a failure message:
// "path (type)", or "path (type, N keys beneath)" when the key's value hides a
// non-empty subtree (object or non-empty array) that is dropped wholesale and
// never enumerated into the baseline.
func formatUnmodelledKey(k unmodelledKey) string {
	if k.keysBeneath > 0 {
		return fmt.Sprintf("%s (%s, %d keys beneath)", k.path, k.jsonType, k.keysBeneath)
	}
	return fmt.Sprintf("%s (%s)", k.path, k.jsonType)
}

// staleDeclarations returns the stale baseline entries — declared keys that
// are no longer in the actual unmodelled set — each annotated with its reason:
// "now modelled by a struct field" when the path still exists in the fixture
// (the struct grew a field), or "no longer present in fixture" when the path
// is gone (the fixture changed). Sorted for stable output.
func staleDeclarations(declared, actualMap map[string]string, root interface{}) []string {
	var stale []string
	for path, typ := range declared {
		if _, ok := actualMap[path]; ok {
			continue
		}
		if pathExistsInFixture(root, path) {
			stale = append(stale, fmt.Sprintf("%s (%s) — now modelled by a struct field; remove from baseline", path, typ))
		} else {
			stale = append(stale, fmt.Sprintf("%s (%s) — no longer present in fixture; remove from baseline", path, typ))
		}
	}
	sort.Strings(stale)
	return stale
}

// TestUnknownFieldDiagnostic is the gate: for each fixture, walk the JSON key
// tree against the response type and compare the unmodelled set against the
// declared baseline. Fails on any divergence — see unmodelledBaselines.
//
// It also logs a per-endpoint and total summary distinguishing DIRECTLY
// unmodelled keys (one entry per unmodelled key at the depth the struct
// models — the baseline count) from keys HIDDEN beneath unmodelled roots
// (the keys inside an unmodelled object/non-empty-array value, which are
// dropped wholesale and never enumerated). The direct count is a floor, not a
// total; the hidden count quantifies what is invisible to the gate.
func TestUnknownFieldDiagnostic(t *testing.T) {
	var totalDirect, totalHidden int
	for _, spec := range liveFixtureSpecs {
		t.Run(spec.endpoint, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "live", spec.file))
			if err != nil {
				t.Fatalf("read testdata/live/%s: %v", spec.file, err)
			}
			actual := unmodelledKeys(data, spec.targetTyp)
			actualMap := make(map[string]string, len(actual))
			byPath := make(map[string]unmodelledKey, len(actual))
			var hidden int
			for _, k := range actual {
				actualMap[k.path] = k.jsonType
				byPath[k.path] = k
				hidden += k.keysBeneath
			}
			declared := unmodelledBaselines[spec.endpoint]
			if declared == nil {
				declared = map[string]string{}
			}

			// Newly-appeared: in fixture but not declared.
			var newKeys []string
			for path := range actualMap {
				if _, ok := declared[path]; !ok {
					newKeys = append(newKeys, formatUnmodelledKey(byPath[path]))
				}
			}
			sort.Strings(newKeys)
			for _, k := range newKeys {
				t.Errorf("unmodelled key not in baseline (newly-appeared server field): %s — %s", spec.endpoint, k)
			}

			// Stale: declared but not in actual unmodelled set.
			var root interface{}
			_ = json.Unmarshal(data, &root)
			for _, k := range staleDeclarations(declared, actualMap, root) {
				t.Errorf("stale baseline declaration: %s — %s", spec.endpoint, k)
			}

			t.Logf("summary: %d directly unmodelled, %d hidden beneath unmodelled roots", len(actualMap), hidden)
			totalDirect += len(actualMap)
			totalHidden += hidden
		})
	}
	t.Logf("TOTAL: %d directly unmodelled, %d hidden beneath unmodelled roots (direct is a floor, not a total)", totalDirect, totalHidden)
}

// TestLiveFixtureDecodes is the SHAPE half of the fixture gate, and it exists
// because the key-walker above is structurally blind to a wrong shape.
// walkJSON dispatches on the JSON node kind and then switches on the target's
// reflect.Kind: a JSON OBJECT landing on a Go SLICE matches neither the Struct
// arm nor the Map arm, so it falls through and reports nothing at all.
//
// The walker asks "does the struct model every key the server sends" — the
// QUIET failure, where one field is silently dropped. A wrong SHAPE is the
// LOUD one: encoding/json aborts the entire decode, so every other field is
// lost too and the call returns an error to the user.
//
// Measured 2026-07-30: SchedulePostsResponse.PostsByDays was declared
// map[string][]Post while the server sends
// map[string]{day_name, day_date, posts[]}, so `hooppy schedules queue` failed
// on every single invocation. Registering the real fixture did NOT catch it —
// the key-walker stayed green. Only decoding does.
//
// The oracle is deliberately not a hand-written table of which reflect.Kinds
// are compatible; that table would encode the same guess the struct already
// encodes. It is encoding/json itself, the decoder the client actually runs.
// fixturesBlindToDecode names the fixtures whose decode aborts on a custom
// UnmarshalJSON rejecting a type PLACEHOLDER rather than on a shape problem —
// FlexInt reading "str". encoding/json returns that error immediately and
// discards the *json.UnmarshalTypeError it had already saved, so for these
// fixtures the decode oracle reports nothing about shape at all.
//
// Measured 2026-07-30 — 3 of 18. The list is checked in BOTH directions: an
// undeclared fixture that goes blind fails, and a declared one that starts
// decoding cleanly fails as stale. A suppression list that cannot expire
// outlives the reason it was written.
//
// Their remaining cover is shapeMismatches, which the walker computes without
// decoding and is therefore immune to this masking. To shrink this list, make
// the fixture reducer emit a type-valid placeholder (0) for FlexInt- and
// PhotoID-typed leaves.
var fixturesBlindToDecode = map[string]bool{
	"posts.json":          true,
	"schedule_edit.json":  true,
	"schedule_posts.json": true,
}

func TestLiveFixtureDecodes(t *testing.T) {
	for _, spec := range liveFixtureSpecs {
		t.Run(spec.endpoint, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "live", spec.file))
			if err != nil {
				t.Fatalf("read testdata/live/%s: %v", spec.file, err)
			}
			err = json.Unmarshal(data, reflect.New(spec.targetTyp).Interface())

			// Only STRUCTURAL failures count. A custom UnmarshalJSON that
			// rejects a placeholder VALUE (FlexInt reading "str") is an
			// artifact of the reduction, not a struct bug — the fixtures carry
			// no real values by construction, and failing on that would force
			// them to. json.UnmarshalTypeError is exactly the "this shape
			// cannot go into that type" error, and json.SyntaxError catches a
			// fixture corrupted in the repo.
			var typeErr *json.UnmarshalTypeError
			var syntaxErr *json.SyntaxError
			switch {
			case errors.As(err, &typeErr):
				t.Errorf("%s does not decode into %s: %v\n\nThe struct cannot parse what the server actually sends, so EVERY call to this endpoint fails: encoding/json aborts the whole decode on a shape mismatch, losing the other fields too. Fix the STRUCT; never edit the fixture to agree with it.",
					spec.file, spec.targetTyp, err)
			case errors.As(err, &syntaxErr):
				t.Errorf("testdata/live/%s is not valid JSON: %v", spec.file, err)
			case err != nil:
				if !fixturesBlindToDecode[spec.file] {
					t.Errorf("%s aborts its decode on a non-structural error and is NOT declared in fixturesBlindToDecode: %v\n\nThat error is returned before encoding/json can report a shape mismatch anywhere in this fixture, so the decode oracle is now inert for it. Add it to the list (with the shape walker as its remaining cover) or fix the placeholder.", spec.file, err)
				}
				t.Logf("decode oracle INERT for this fixture (a custom UnmarshalJSON rejected a placeholder value): %v", err)
			}
			if err == nil && fixturesBlindToDecode[spec.file] {
				t.Errorf("%s now decodes cleanly but is still declared in fixturesBlindToDecode — remove the entry, or the decode oracle stays voluntarily switched off for a fixture that no longer needs it", spec.file)
			}

			// The walker sees what the decode cannot, on every fixture. This
			// loop is also the DISCRIMINATING half of the shape assertions:
			// every recorded fixture against the type it really decodes into
			// must report zero, so a mismatch claim elsewhere cannot be
			// universal rather than specific.
			for _, m := range shapeMismatches(data, spec.targetTyp) {
				t.Errorf("shape mismatch in %s at %s: the server sends a JSON %s, the struct declares %s — encoding/json aborts the WHOLE decode on this, losing every other field. Fix the STRUCT; never edit the fixture to agree with it.",
					spec.file, m.path, m.jsonKind, m.goType)
			}
		})
	}
}

// TestLiveFixtureDecodes_DetectsAWrongShape pins ONE mutation: the exact
// declaration that shipped in 96f872a and broke every `hooppy schedules queue`
// invocation. It is not evidence that the decode oracle covers this fixture
// generally, and the first version of this comment wrongly said it was.
//
// Measured 2026-07-30: schedule_posts.json also produces a FlexInt placeholder
// error, and encoding/json returns that immediately, discarding the saved
// *json.UnmarshalTypeError. Drift in total_rows or is_has_more on this fixture
// is therefore INVISIBLE to a decode. This mutation is caught only because the
// mismatch makes the decoder skip the subtree the FlexInt lives in, so the
// FlexInt never runs. General cover for this fixture comes from
// shapeMismatches, not from here — see fixturesBlindToDecode.
//
// The obvious way to test this is to revert SchedulePostsResponse to the wrong
// declaration and watch the suite go red, but that mutation does not COMPILE
// (the consumers read .Posts), and a compile error falsifies nothing. So the
// wrong declaration lives here instead, as a local type, where it compiles and
// can be asserted on permanently.
func TestLiveFixtureDecodes_DetectsAWrongShape(t *testing.T) {
	// Verbatim the declaration that shipped in 96f872a and broke every
	// `hooppy schedules queue` invocation.
	type wrongSchedulePostsResponse struct {
		PostsByDays map[string][]Post `json:"posts_by_days"`
		TotalRows   int               `json:"total_rows"`
	}
	data, err := os.ReadFile(filepath.Join("testdata", "live", "schedule_posts.json"))
	if err != nil {
		t.Fatalf("read testdata/live/schedule_posts.json: %v", err)
	}

	var typeErr *json.UnmarshalTypeError
	err = json.Unmarshal(data, &wrongSchedulePostsResponse{})
	if !errors.As(err, &typeErr) {
		t.Fatalf("decoding the real fixture into the WRONG shape gave %v, want a *json.UnmarshalTypeError — if a custom unmarshaller's value error is returned first, TestLiveFixtureDecodes is blind on this fixture and a wrong shape ships green", err)
	}
	if typeErr.Field != "posts_by_days" {
		t.Errorf("UnmarshalTypeError.Field = %q, want \"posts_by_days\" — the error must point at the field whose shape is wrong", typeErr.Field)
	}

	// The walker must report the same mismatch independently. This is the
	// object-on-slice arm of shapeMismatches, and it is the exact class that
	// shipped broken in 96f872a.
	var walkerSaw bool
	for _, m := range shapeMismatches(data, reflect.TypeOf(wrongSchedulePostsResponse{})) {
		// The walker reports it one level deeper than the decoder does —
		// posts_by_days.<day> — because the wrong type is a MAP whose VALUE is
		// the slice, so the mismatch is on the day cell. That is the more
		// precise location, not a different finding.
		if strings.HasPrefix(m.path, "posts_by_days") && m.jsonKind == "object" {
			walkerSaw = true
		}
	}
	if !walkerSaw {
		t.Errorf("shapeMismatches did not report posts_by_days as a JSON object on a Go slice (got %+v) — the walker is the cover that survives error masking", shapeMismatches(data, reflect.TypeOf(wrongSchedulePostsResponse{})))
	}

	// And the correct declaration decodes the same bytes without a structural
	// error, so the assertion above is discriminating rather than universal.
	err = json.Unmarshal(data, &SchedulePostsResponse{})
	if errors.As(err, &typeErr) {
		t.Errorf("the CORRECT shape still reports a structural error: %v", err)
	}
}

// TestShapeMismatches_SeesWhatTheDecodeCannot is the reason shapeMismatches
// exists at all. On the three fixtures in fixturesBlindToDecode, encoding/json
// returns a custom unmarshaller's placeholder error immediately and discards
// the *json.UnmarshalTypeError it had already saved, so the decode oracle
// reports nothing about shape.
//
// The walker never reaches those leaves — it stops at every json.Unmarshaler —
// so it is immune to that masking. This test pins both halves: the decode IS
// blind here, and the walker is NOT.
func TestShapeMismatches_SeesWhatTheDecodeCannot(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "live", "schedule_posts.json"))
	if err != nil {
		t.Fatalf("read testdata/live/schedule_posts.json: %v", err)
	}
	// Both scalar-on-container arms: total_rows and rows_limit are NUMBERS on
	// the wire, declared here as a slice and as a struct. One field would
	// leave the other arm unguarded, and a mutant that happens to hit only the
	// unguarded one passes — which manufactures a false "this is covered".
	type drifted struct {
		PostsByDays ScheduleCalendar `json:"posts_by_days"`
		TotalRows   []int            `json:"total_rows"`
		RowsLimit   ScheduleDay      `json:"rows_limit"`
	}
	typ := reflect.TypeOf(drifted{})

	// Half one: the decode really is blind to this. If this ever starts
	// catching it, the masking is gone and fixturesBlindToDecode should shrink.
	var typeErr *json.UnmarshalTypeError
	if errors.As(json.Unmarshal(data, &drifted{}), &typeErr) {
		t.Errorf("premise no longer holds: the DECODE now reports this drift, so schedule_posts.json may no longer need to be in fixturesBlindToDecode — re-measure and shrink the list")
	}

	// Half two: the walker sees it.
	seen := map[string]bool{}
	for _, m := range shapeMismatches(data, typ) {
		seen[m.path] = true
	}
	for _, want := range []string{"total_rows", "rows_limit"} {
		if !seen[want] {
			t.Errorf("shapeMismatches did not report %s (got %+v) — the walker is the ONLY cover these three fixtures have, and it just lost an arm of the scalar-on-container class", want, shapeMismatches(data, typ))
		}
	}
}

// TestUnmarshalerContractsAreMeasured keeps unmarshalerContracts honest. A
// hand-written table of what a custom unmarshaller accepts is exactly the kind
// of belief this file refuses to trust — so every entry is probed against the
// real UnmarshalJSON, and a declaration that disagrees with the code fails.
//
// Written after the walker reported a mismatch on `{"posts_by_days":[]}`, a
// body that decodes perfectly: the leaf rule had been stripped wholesale by a
// bare bool, so the classifier contradicted the type's own contract and the
// failure message told the reader to change the struct.
func TestUnmarshalerContractsAreMeasured(t *testing.T) {
	probe := func(typ reflect.Type, body string) error {
		return json.Unmarshal([]byte(body), reflect.New(typ).Interface())
	}
	for typ, c := range unmarshalerContracts {
		t.Run(typ.String(), func(t *testing.T) {
			if c.acceptsEmptyArray {
				if err := probe(typ, `[]`); err != nil {
					t.Errorf("declared acceptsEmptyArray, but `[]` fails to decode: %v — the walker would exempt a value the type rejects", err)
				}
				if err := probe(typ, `[1]`); err == nil {
					t.Error("a NON-empty array decodes, so the tolerance is wider than declared — a real shape change would then pass as this quirk")
				}
			} else if err := probe(typ, `[]`); err == nil {
				t.Error("`[]` decodes but acceptsEmptyArray is false — the walker will report a mismatch on a body that works")
			}

			if c.scalarOnly {
				if c.traverse {
					t.Error("scalarOnly declared together with traverse — the walker applies scalarOnly only on the leaf path that traverse skips, so half this declaration would be silently ignored while both halves probe green here")
				}
				var typeErr *json.UnmarshalTypeError
				for _, body := range []string{`{"a":1}`, `[1,2]`} {
					if err := probe(typ, body); !errors.As(err, &typeErr) {
						t.Errorf("declared scalarOnly, but %s gives %v — a container must be reported as *json.UnmarshalTypeError, because that is the only shape signal the DECODE oracle can classify", body, err)
					}
				}
				if err := probe(typ, `0`); err != nil {
					t.Errorf("declared scalarOnly but a plain number fails: %v", err)
				}
			}

			if c.traverse {
				// The criterion is what the walker would actually FIND, not
				// the Kind: FlexInt is a struct too, so a Kind check passes it
				// while there is nothing to descend into. jsonFields is
				// exactly the walker's own view — it skips unexported fields
				// and `json:"-"` — so this asks the question in the same terms
				// the walker answers it.
				var descendable bool
				switch typ.Kind() {
				case reflect.Map, reflect.Slice, reflect.Array:
					descendable = true
				case reflect.Struct:
					descendable = len(jsonFields(typ)) > 0
				}
				if !descendable {
					t.Errorf("declared traverse but there is nothing modelled beneath %s (kind %s) — the declaration only strips the leaf rule, which is the half that can go WRONG, while buying no coverage", typ, typ.Kind())
				}
			}
		})
	}
}

// exemptFromContracts names json.Unmarshaler types that legitimately need no
// contract: they accept ANY shape, so there is nothing for the walker to judge.
//
// The reason string is NOT decoration and NOT taken on trust — every entry is
// probed in TestUnmarshalerContractsAreComplete against an object, an array
// and a scalar, and an entry whose type rejects any of them fails. Measured
// 2026-07-30: moving Metric here with a hand-waved reason kept the suite green
// while the both-oracle blind set grew from 40 to 48. An escape hatch nothing
// measures is how a gate quietly stops gating.
var exemptFromContracts = map[reflect.Type]string{
	reflect.TypeOf(json.RawMessage{}): "accepts any JSON value verbatim",
}

// verifyExemption measures the claim an exemption makes: that the type accepts
// ANY shape, so the walker leaf-skipping it costs no coverage.
//
// The predicate is ANY error, not errors.As(*json.UnmarshalTypeError). A type
// that rejects a container with a plain errors.New would otherwise be exempted
// VACUOUSLY — the probe would look for a rejection expressed one particular
// way and miss every other way of rejecting. The narrow typed predicate is
// still right for scalarOnly, where the walker genuinely needs the typed error
// to classify: different claims, different evidence.
// It reports through a callback rather than a *testing.T so it can be aimed at
// a synthetic subject without constructing a bare &testing.T{} — that relies on
// testing internals, and a Fatalf on a parentless T calls runtime.Goexit and
// kills the CALLER's goroutine, silently skipping whatever came after.
func verifyExemption(typ reflect.Type, reason string, report func(format string, args ...any)) {
	if !typ.Implements(jsonUnmarshalerType) && !reflect.PointerTo(typ).Implements(jsonUnmarshalerType) {
		report("exemptFromContracts names %s (%q), which is not a json.Unmarshaler — it was never leaf-skipped, so exempting it says nothing and hides that the entry is meaningless", typ, reason)
		return
	}
	// Every JSON shape, not just containers. Widening the error-KIND axis
	// without the SHAPE axis would still exempt a type that accepts objects,
	// arrays and numbers while rejecting strings.
	for _, body := range []string{`{"a":1}`, `[1,2]`, `0`, `"str"`, `true`, `null`} {
		if err := json.Unmarshal([]byte(body), reflect.New(typ).Interface()); err != nil {
			report("exemptFromContracts claims %s accepts any shape (%q), but %s is rejected: %v — a type that DISCRIMINATES needs a contract, not an exemption", typ, reason, body, err)
		}
	}
}

// TestUnmarshalerContractsAreComplete is the half TestUnmarshalerContractsAreMeasured
// structurally cannot be: that test ranges over the TABLE, so a type MISSING
// from the table is invisible to it — the guard could not fail for the one
// reason it exists.
//
// Measured 2026-07-30: the table declared FlexInt and ScheduleCalendar but not
// Metric or PhotoID, and both are container-rejecting scalar Unmarshalers. On
// the three fixtures in fixturesBlindToDecode the decode oracle is masked, so a
// container landing on list[0].views or list[0].reposts was invisible to BOTH
// oracles — the precise failure this whole change set exists to remove. Two
// other Metric fields, likes and comments, were caught only because they sort
// BEFORE the masking placeholder on the wire. Luck, not a gate.
//
// So the population is counted rather than assumed: every type reachable from
// a registered response type must be declared or explicitly exempt.
func TestUnmarshalerContractsAreComplete(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var visit func(reflect.Type)
	visit = func(t0 reflect.Type) {
		t0 = derefType(t0)
		if seen[t0] {
			return
		}
		seen[t0] = true
		switch t0.Kind() {
		case reflect.Struct:
			for i := 0; i < t0.NumField(); i++ {
				visit(t0.Field(i).Type)
			}
		case reflect.Slice, reflect.Array:
			visit(t0.Elem())
		case reflect.Map:
			// The KEY type counts too: a map[PhotoID]X would otherwise slip
			// the census entirely.
			visit(t0.Key())
			visit(t0.Elem())
		}
	}
	for _, spec := range liveFixtureSpecs {
		visit(spec.targetTyp)
	}

	var missing []string
	for typ := range seen {
		if !typ.Implements(jsonUnmarshalerType) && !reflect.PointerTo(typ).Implements(jsonUnmarshalerType) {
			continue
		}
		if _, ok := unmarshalerContracts[typ]; ok {
			continue
		}
		if _, ok := exemptFromContracts[typ]; ok {
			continue
		}
		missing = append(missing, typ.String())
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s implements json.Unmarshaler but has no unmarshalerContract and no exemption — the walker will leaf-skip it, and on a fixture in fixturesBlindToDecode that leaves NO oracle covering it", m)
	}

	// An exemption is a claim that the type accepts ANY shape. Measure it.
	for typ, reason := range exemptFromContracts {
		verifyExemption(typ, reason, t.Errorf)
	}
	// Both populations must also expire. An entry for a type nothing reaches
	// outlives whatever justified it, and nothing would ever say so.
	for typ := range unmarshalerContracts {
		if !seen[typ] {
			t.Errorf("unmarshalerContracts declares %s, which is not reachable from any registered response type — remove it, or the table records a belief about code that no longer exists", typ)
		}
	}
	for typ := range exemptFromContracts {
		if !seen[typ] {
			t.Errorf("exemptFromContracts declares %s, which is not reachable from any registered response type — remove it", typ)
		}
	}
}

// TestShapeMismatches_EmptyCalendarIsNotAMismatch is the regression for that
// false positive, against the body measured live from a page past the end.
func TestShapeMismatches_EmptyCalendarIsNotAMismatch(t *testing.T) {
	body := []byte(`{"posts_by_days":[],"total_rows":96,"rows_limit":200,"is_has_more":false}`)
	if err := json.Unmarshal(body, &SchedulePostsResponse{}); err != nil {
		t.Fatalf("premise broken — this body must decode: %v", err)
	}
	if got := shapeMismatches(body, reflect.TypeOf(SchedulePostsResponse{})); len(got) != 0 {
		t.Errorf("shapeMismatches reported %+v on a body that decodes cleanly — the walker is contradicting ScheduleCalendar.UnmarshalJSON, and its message tells the reader to change the struct", got)
	}
	// The exemption must stay narrow: a POPULATED array is a real shape error.
	populated := []byte(`{"posts_by_days":[{"id":1}],"total_rows":1}`)
	if got := shapeMismatches(populated, reflect.TypeOf(SchedulePostsResponse{})); len(got) == 0 {
		t.Error("a populated array was NOT reported — the empty-array exemption has widened into blanket tolerance of an array")
	}
}

// TestShapeMismatches_SeesDriftOnAMaskedLeaf is the measured proof for the
// claim fixturesBlindToDecode makes: where the decode oracle is masked, the
// walker still sees the shape error.
//
// Measured 2026-07-30 on schedule_posts.json — a container landing on the
// FlexInt at publication_date.timestamp:
//
//	decode structural = false   (the "str" placeholder error fires first)
//	walker mismatches = 1
//
// Before FlexInt reported a container as *json.UnmarshalTypeError, and before
// the walker judged declared-contract leaves, this drift was invisible to BOTH
// oracles at once on three fixtures.
func TestShapeMismatches_SeesDriftOnAMaskedLeaf(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "live", "schedule_posts.json"))
	if err != nil {
		t.Fatalf("read testdata/live/schedule_posts.json: %v", err)
	}
	const leaf = `"timestamp": 0`
	if !strings.Contains(string(data), leaf) {
		t.Fatalf("fixture no longer contains %s — re-anchor this test on another FlexInt leaf", leaf)
	}
	drifted := []byte(strings.Replace(string(data), leaf, `"timestamp": [1,2]`, 1))

	var typeErr *json.UnmarshalTypeError
	if errors.As(json.Unmarshal(drifted, &SchedulePostsResponse{}), &typeErr) {
		t.Log("the decode now catches this too — re-measure fixturesBlindToDecode, it may be able to shrink")
	}
	if got := shapeMismatches(drifted, reflect.TypeOf(SchedulePostsResponse{})); len(got) == 0 {
		t.Error("the walker did not report a container on a FlexInt leaf — on the blind-listed fixtures it is the only oracle left, so this drift would ship green")
	}
}

// TestScheduleCalendar_MarshalsAbsentAsAnObject covers the nil branch of
// MarshalJSON, whose only trigger is the one case UnmarshalJSON cannot reach:
// posts_by_days absent from the response entirely, leaving the field at its
// zero value. Both front-ends re-emit this envelope verbatim.
func TestScheduleCalendar_MarshalsAbsentAsAnObject(t *testing.T) {
	var resp SchedulePostsResponse
	if err := json.Unmarshal([]byte(`{"total_rows":5}`), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.PostsByDays != nil {
		t.Fatalf("premise changed: an ABSENT posts_by_days now yields a non-nil map, so this test no longer covers the nil branch")
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"posts_by_days":{}`) {
		t.Errorf("re-emitted envelope = %s, want posts_by_days as {} — a nil map ships as null and the output shape then differs from every other branch", b)
	}
}

// TestFixturesBlindToDecode_NamesRealFixtures closes the last hole in the
// self-expiry: the stale check only fires for names that appear in
// liveFixtureSpecs, so an entry naming a file that no longer exists survives
// forever.
func TestFixturesBlindToDecode_NamesRealFixtures(t *testing.T) {
	known := make(map[string]bool, len(liveFixtureSpecs))
	for _, spec := range liveFixtureSpecs {
		known[spec.file] = true
	}
	for file := range fixturesBlindToDecode {
		if !known[file] {
			t.Errorf("fixturesBlindToDecode names %q, which is not in liveFixtureSpecs — an entry for a fixture nothing runs can never be found stale", file)
		}
	}
}

// TestWalkJSON_ByteSliceAcceptsBothForms pins the []byte special case, which is
// the one place encoding/json makes a slice behave like a scalar too: it fills
// []byte from a base64 STRING **and** from a JSON array of numbers.
//
// Review round two called the array form a decode error and asked for it to be
// reported. Measured 2026-07-30 it is not:
//
//	{"b":[1,2]}   -> nil error, [1 2]
//	{"b":"aGk="}  -> nil error, [104 105]
//	{"b":[300]}   -> error, but on the ELEMENT (300 overflows uint8)
//
// So the exemption belongs to the scalar arm only, and reporting the array
// form would be a false positive on working code. Each assertion below carries
// its own decode as the premise, so this cannot rot into a belief.
func TestWalkJSON_ByteSliceAcceptsBothForms(t *testing.T) {
	type wrapper struct {
		B []byte `json:"b"`
	}
	typ := reflect.TypeOf(wrapper{})

	for _, body := range []string{`{"b":"aGk="}`, `{"b":[1,2]}`} {
		if err := json.Unmarshal([]byte(body), &wrapper{}); err != nil {
			t.Fatalf("premise broken: %s no longer decodes into []byte (%v) — if encoding/json changed, the walker must start reporting this form", body, err)
		}
		if got := shapeMismatches([]byte(body), typ); len(got) != 0 {
			t.Errorf("%s reported %+v, want none — encoding/json accepts it, so a mismatch here is a false positive on working code", body, got)
		}
	}
}

// TestEveryRecordedFixtureIsRegistered counts the population on disk against
// the population the gate runs. A fixture recorded and then never added to
// liveFixtureSpecs is checked by nothing, and its absence looks exactly like
// coverage — the directory listing says it is there.
func TestEveryRecordedFixtureIsRegistered(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "live"))
	if err != nil {
		t.Fatalf("read testdata/live: %v", err)
	}
	registered := make(map[string]bool, len(liveFixtureSpecs))
	for _, spec := range liveFixtureSpecs {
		registered[spec.file] = true
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		onDisk[e.Name()] = true
		if !registered[e.Name()] {
			t.Errorf("testdata/live/%s is recorded but not in liveFixtureSpecs — nothing runs against it, and the file's presence reads as coverage", e.Name())
		}
	}
	for _, spec := range liveFixtureSpecs {
		if !onDisk[spec.file] {
			t.Errorf("liveFixtureSpecs registers %s, which is not on disk", spec.file)
		}
	}
}

// TestJSONFields_MatchesWhatEncodingJSONPopulates pins jsonFields to the
// decoder it is meant to model. It returned UNEXPORTED fields until
// 2026-07-30, which makes the walker treat a server key as MODELLED while the
// runtime silently drops it — a false-clean gate, the exact failure this file
// exists to prevent. It was reachable only latently (the four unexported
// fields on FlexInt and Metric sit on leaf types), but "latent" is a property
// of today's structs, not of the helper.
//
// Every case carries its own decode as the premise, so this cannot rot into a
// belief about what encoding/json does.
func TestJSONFields_MatchesWhatEncodingJSONPopulates(t *testing.T) {
	type embedded struct {
		Promoted string `json:"promoted"`
	}
	type subject struct {
		Kept     string `json:"kept"`
		Excluded string `json:"-"`
		embedded        // an UNEXPORTED struct type still promotes its exported fields
		hidden   string //nolint:unused // the point of the test
	}

	got := jsonFields(reflect.TypeOf(subject{}))
	for _, want := range []string{"kept", "promoted"} {
		if _, ok := got[want]; !ok {
			t.Errorf("jsonFields lost %q (got %v) — the walker would report a key the decoder DOES populate as unmodelled", want, keysOf(got))
		}
	}
	for _, unwanted := range []string{"-", "Excluded", "hidden"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("jsonFields returned %q (got %v) — encoding/json never populates it, so the walker would call a dropped key modelled", unwanted, keysOf(got))
		}
	}

	// An anonymous field is promoted only when it is an UNTAGGED struct.
	// A tagged one is an ordinary named field to encoding/json, and an
	// anonymous non-struct has nothing to promote — recursing into it used to
	// panic with "reflect: NumField of non-struct type".
	type inner struct {
		A string `json:"a"`
	}
	type namedScalar string
	type anon struct {
		inner       `json:"inner"` // TAGGED: a field called "inner", not a promotion
		namedScalar                // anonymous NON-struct: nothing to promote
		Kept        string         `json:"kept"`
	}
	anonGot := jsonFields(reflect.TypeOf(anon{})) // must not panic
	if _, ok := anonGot["a"]; ok {
		t.Errorf("jsonFields promoted %q out of a TAGGED anonymous field (got %v) — encoding/json treats it as a field named \"inner\" instead", "a", keysOf(anonGot))
	}
	if _, ok := anonGot["inner"]; !ok {
		t.Errorf("jsonFields lost the tagged anonymous field \"inner\" (got %v)", keysOf(anonGot))
	}

	var a anon
	if err := json.Unmarshal([]byte(`{"inner":{"a":"x"},"kept":"k"}`), &a); err != nil {
		t.Fatalf("premise: %v", err)
	}
	if a.inner.A != "x" {
		t.Errorf("premise broken: encoding/json did not fill the tagged anonymous field (%+v) — the promotion rule above may no longer hold", a)
	}

	// The premise, measured rather than assumed.
	var s subject
	if err := json.Unmarshal([]byte(`{"kept":"a","promoted":"b","hidden":"c","Excluded":"d"}`), &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.Kept != "a" || s.Promoted != "b" {
		t.Fatalf("premise broken: encoding/json did not fill the exported fields (%+v)", s)
	}
	if s.Excluded != "" {
		t.Error(`premise broken: encoding/json filled a json:"-" field, so excluding it from jsonFields is now wrong`)
	}
}

func keysOf(m map[string]reflect.Type) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestVerifyExemption_CatchesAPickyType aims the exemption check at a type
// that rejects containers with a PLAIN error rather than a
// *json.UnmarshalTypeError. Nothing in the repo does that today, which is
// exactly why the check needs a subject of its own: with the narrower
// errors.As predicate this type sailed through as "accepts any shape" while
// discriminating on every container it was handed.
func TestVerifyExemption_CatchesAPickyType(t *testing.T) {
	var got []string
	collect := func(format string, args ...any) { got = append(got, fmt.Sprintf(format, args...)) }

	verifyExemption(reflect.TypeOf(pickyLeaf{}), "probe", collect)
	if len(got) == 0 {
		t.Error("verifyExemption passed a type that rejects containers — an exemption claims the type accepts ANY shape, and the probe must notice a rejection however it is expressed, not only as *json.UnmarshalTypeError")
	}

	// A type that rejects only STRINGS must be caught too: widening the
	// error-kind axis without the shape axis leaves this one exempt.
	got = nil
	verifyExemption(reflect.TypeOf(stringHatingLeaf{}), "probe", collect)
	if len(got) == 0 {
		t.Error("verifyExemption passed a type that rejects strings — the probe must cover every JSON shape, not only containers")
	}

	// And it must not fail a type that genuinely accepts everything, or the
	// assertions above are universal rather than discriminating.
	got = nil
	verifyExemption(reflect.TypeOf(json.RawMessage{}), "accepts any JSON value verbatim", collect)
	if len(got) != 0 {
		t.Errorf("verifyExemption failed json.RawMessage, which does accept any JSON value: %v", got)
	}
}

// pickyLeaf rejects containers the way most hand-written unmarshallers do:
// with an ordinary error, carrying no type information.
type pickyLeaf struct{}

func (p *pickyLeaf) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && (b[0] == '{' || b[0] == '[') {
		return errors.New("pickyLeaf: containers not supported")
	}
	return nil
}

// stringHatingLeaf discriminates on a shape the container probes never reach.
type stringHatingLeaf struct{}

func (s *stringHatingLeaf) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return errors.New("stringHatingLeaf: strings not supported")
	}
	return nil
}

// TestJSONFields_FieldDominance pins the three outcomes of a contested JSON
// name. Each case carries its own decode as the premise, so this asserts what
// encoding/json DOES rather than what the helper was written to believe.
//
// The tie case is the one that matters. A first-wins or last-wins rule — the
// helper has had both — reports a key as MODELLED that the decoder silently
// discards, which is a false-clean gate pointing the wrong way: precisely the
// failure this file exists to catch.
func TestJSONFields_FieldDominance(t *testing.T) {
	type c1 struct {
		X string `json:"x"`
	}
	type c2 struct {
		X string `json:"x"`
	}
	type deep struct {
		A string `json:"a"`
	}

	t.Run("same-depth tie is dropped", func(t *testing.T) {
		// Built with reflect.StructOf rather than declared: `go vet`'s
		// structtag check rejects a repeated json tag in source, and this
		// repo runs vet in its gate. That is a useful guard, not a reason to
		// skip the case — vet covers types declared HERE, and jsonFields is a
		// model of encoding/json, which must be right regardless of where a
		// type comes from.
		tie := reflect.StructOf([]reflect.StructField{
			{Name: "C1", Type: reflect.TypeOf(c1{}), Anonymous: true},
			{Name: "C2", Type: reflect.TypeOf(c2{}), Anonymous: true},
		})
		v := reflect.New(tie)
		if err := json.Unmarshal([]byte(`{"x":"set"}`), v.Interface()); err != nil {
			t.Fatalf("premise: %v", err)
		}
		got1 := v.Elem().Field(0).Field(0).String()
		got2 := v.Elem().Field(1).Field(0).String()
		if got1 != "" || got2 != "" {
			t.Fatalf("premise broken: encoding/json now RESOLVES a same-depth tie (%q/%q) — the drop rule below is no longer right", got1, got2)
		}
		if _, ok := jsonFields(tie)["x"]; ok {
			t.Error(`jsonFields reports "x" as modelled, but encoding/json fills NEITHER field and reports no error — the walker would call a silently discarded key covered`)
		}
	})

	t.Run("shallower depth wins, with its own type", func(t *testing.T) {
		type shadow struct {
			deep
			A int `json:"a"` // depth 0 beats the promoted depth-1 string
		}
		var v shadow
		if err := json.Unmarshal([]byte(`{"a":7}`), &v); err != nil {
			t.Fatalf("premise: %v", err)
		}
		if v.A != 7 || v.deep.A != "" {
			t.Fatalf("premise broken: encoding/json did not bind the OUTER field (%+v)", v)
		}
		got, ok := jsonFields(reflect.TypeOf(shadow{}))["a"]
		if !ok {
			t.Fatal(`jsonFields lost "a"`)
		}
		if got.Kind() != reflect.Int {
			t.Errorf(`jsonFields reports "a" as %s, want int — presence is not enough, the TYPE is what drives the walker's recursion, and the inner string would send it down the wrong branch`, got)
		}
	})

	t.Run("a tagged field beats an untagged one at the same depth", func(t *testing.T) {
		type both struct {
			X string // no tag: name "X"
			Y string `json:"X"`
		}
		var v both
		if err := json.Unmarshal([]byte(`{"X":"set"}`), &v); err != nil {
			t.Fatalf("premise: %v", err)
		}
		if v.Y != "set" || v.X != "" {
			t.Fatalf("premise broken: the TAGGED field no longer wins (%+v)", v)
		}
		if _, ok := jsonFields(reflect.TypeOf(both{}))["X"]; !ok {
			t.Error(`jsonFields dropped "X" — a tagged/untagged clash is resolved by encoding/json, not dropped`)
		}
	})
}

// TestJSONFields_EmbeddedUnexportedScalarIsIgnored pins the third shape the
// anonymous-field comment claims and nothing asserted. encoding/json cannot set
// it, so reporting it as modelled is a false-clean gate.
func TestJSONFields_EmbeddedUnexportedScalarIsIgnored(t *testing.T) {
	type namedScalar string
	type host struct {
		namedScalar
		Kept string `json:"kept"`
	}
	var v host
	if err := json.Unmarshal([]byte(`{"namedScalar":"q","kept":"k"}`), &v); err != nil {
		t.Fatalf("premise: %v", err)
	}
	if v.namedScalar != "" {
		t.Fatalf("premise broken: encoding/json now fills an embedded unexported scalar (%+v)", v)
	}
	if v.Kept != "k" {
		t.Fatalf("premise broken: the sibling field was not filled (%+v)", v)
	}
	got := jsonFields(reflect.TypeOf(host{}))
	if _, ok := got["namedScalar"]; ok {
		t.Errorf("jsonFields reports %q as modelled (got %v) — the decoder ignores it entirely, so the walker would call a dropped key covered", "namedScalar", keysOf(got))
	}
}

// TestUnmarshalerLeafPointerReceiver verifies Fix 1: Metric (and FlexInt,
// PhotoID) declare UnmarshalJSON on a POINTER receiver, while struct fields
// hold VALUE types. A value type does not satisfy an interface that only a
// pointer-to-it satisfies, so the leaf check must test reflect.PointerTo(t)
// too. An OBJECT at such a field must produce NO spurious unmodelled keys
// beneath it — the custom unmarshaller owns the whole value.
func TestUnmarshalerLeafPointerReceiver(t *testing.T) {
	type wrapper struct {
		Views Metric `json:"views"`
	}
	data := []byte(`{"views":{"x":1,"y":2,"z":3}}`)
	got := unmodelledKeys(data, reflect.TypeOf(wrapper{}))
	if len(got) != 0 {
		t.Fatalf("expected 0 unmodelled keys (Metric value field is a leaf via pointer receiver), got %d: %+v", len(got), got)
	}

	// FlexInt and PhotoID exercise the same pointer-receiver path.
	type flexWrapper struct {
		Hours FlexInt `json:"hours"`
	}
	flexData := []byte(`{"hours":{"a":1,"b":2}}`)
	if got := unmodelledKeys(flexData, reflect.TypeOf(flexWrapper{})); len(got) != 0 {
		t.Fatalf("expected 0 unmodelled keys for FlexInt value field, got %d: %+v", len(got), got)
	}
	type photoWrapper struct {
		ID PhotoID `json:"id"`
	}
	photoData := []byte(`{"id":{"a":1,"b":2}}`)
	if got := unmodelledKeys(photoData, reflect.TypeOf(photoWrapper{})); len(got) != 0 {
		t.Fatalf("expected 0 unmodelled keys for PhotoID value field, got %d: %+v", len(got), got)
	}
}

// TestResolvePathArrayIndex verifies Fix 3: after an array-index segment the
// remainder begins with '.', which the old splitter read as an empty object
// key and failed the lookup. Array-indexed paths that exist must resolve.
func TestResolvePathArrayIndex(t *testing.T) {
	root := map[string]interface{}{
		"list": []interface{}{
			map[string]interface{}{
				"access_token": "str",
				"nested":       map[string]interface{}{"k": 0.0},
			},
		},
	}
	cases := []struct {
		path string
		want bool
	}{
		{"list[0].access_token", true},
		{"list[0].nested.k", true},
		{"list[0]", true},
		{"list[0].missing", false},
		{"list[5].access_token", false},
		{"absent[0].x", false},
	}
	for _, c := range cases {
		if got := pathExistsInFixture(root, c.path); got != c.want {
			t.Errorf("pathExistsInFixture(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestStaleDeclarationArrayIndexReason verifies Fix 3's observable effect: a
// declared array-indexed path that still EXISTS in the fixture but is now
// modelled must report "now modelled by a struct field", not "no longer
// present in fixture". Before the resolvePath fix, pathExistsInFixture
// returned false for every [i].key path, so the reason was wrong (a message
// defect — both branches still failed, but with the wrong explanation).
func TestStaleDeclarationArrayIndexReason(t *testing.T) {
	root := map[string]interface{}{
		"list": []interface{}{
			map[string]interface{}{"access_token": "str"},
		},
	}
	declared := map[string]string{"list[0].access_token": "string"}
	actualMap := map[string]string{} // access_token now modelled → not unmodelled → stale.
	stale := staleDeclarations(declared, actualMap, root)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale entry, got %d: %v", len(stale), stale)
	}
	if !strings.Contains(stale[0], "now modelled") {
		t.Errorf("expected 'now modelled' reason for an array-indexed path that exists in the fixture, got: %s", stale[0])
	}
	if strings.Contains(stale[0], "no longer present") {
		t.Errorf("wrong reason 'no longer present' for a path that DOES exist in the fixture: %s", stale[0])
	}
}

// TestWalkJSON_TraversesScheduleCalendar guards the exemption added to
// walkJSON's leaf rule. ScheduleCalendar implements json.Unmarshaler only to
// accept PHP's `[]` for an empty map; everything beneath it is still fully
// modelled. Treating it as a leaf — which the unmodified rule does — would
// drop all 24 keys of a scheduled Post out of the diagnostic's coverage
// silently, and in the direction that looks clean.
func TestWalkJSON_TraversesScheduleCalendar(t *testing.T) {
	data := []byte(`{"posts_by_days":{"15.01.2027":{"day_name":"Пт","day_date":"15 Января","posts":[{"id":1,"brand_new_server_field":"x"}]}}}`)
	got := unmodelledKeys(data, reflect.TypeOf(SchedulePostsResponse{}))

	var found bool
	for _, k := range got {
		if strings.HasSuffix(k.path, "brand_new_server_field") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an unmodelled key beneath posts_by_days was NOT reported (got %+v) — ScheduleCalendar is being treated as an Unmarshaler leaf, which removes every Post field from the diagnostic", got)
	}
}
