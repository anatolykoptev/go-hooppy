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
// The 16 fixtures in testdata/live/ were recorded from live authenticated GETs
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
	m := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			for k, v := range jsonFields(derefType(f.Type)) {
				m[k] = v
			}
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name := f.Name
		if tag != "" {
			parts := strings.SplitN(tag, ",", 2)
			if parts[0] != "" {
				name = parts[0]
			}
		}
		if _, exists := m[name]; !exists {
			m[name] = f.Type
		}
	}
	return m
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

// walkJSON walks a generic-decoded JSON tree against a target Go struct type
// via reflection, collecting every JSON key that has no corresponding struct
// field. Honours json tags, embedded structs, map[string]… element types, and
// slices. Types implementing json.Unmarshaler (Metric, FlexInt, PhotoID) are
// treated as leaves — their custom unmarshaller owns the whole value, so
// there are no "unmodelled keys" below them. interface{} fields are leaves
// too (anything is accepted).
// traversableUnmarshalers lists types that implement json.Unmarshaler purely
// to accept an alternate ENCODING, and whose contents remain fully modelled.
// walkJSON must descend into these instead of stopping at them.
var traversableUnmarshalers = map[reflect.Type]bool{
	reflect.TypeOf(ScheduleCalendar{}): true,
}

func walkJSON(node interface{}, targetType reflect.Type, path string, results *[]unmodelledKey) {
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
	if !traversableUnmarshalers[targetType] &&
		(targetType.Implements(jsonUnmarshalerType) || reflect.PointerTo(targetType).Implements(jsonUnmarshalerType)) {
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
					walkJSON(n[k], ft, childPath, results)
				} else {
					*results = append(*results, unmodelledKey{
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
				walkJSON(n[k], elemType, childPath, results)
			}
		}
		// interface{} or other — modelled, stop.
	case []interface{}:
		if targetType.Kind() == reflect.Slice {
			elemType := derefType(targetType.Elem())
			for i, elem := range n {
				walkJSON(elem, elemType, fmt.Sprintf("%s[%d]", path, i), results)
			}
		}
		// non-slice target (interface{}, RawMessage) — modelled, stop.
	default:
		// leaf scalar — stop.
	}
}

// unmodelledKeys parses a fixture and returns every unmodelled key with its
// JSON path and JSON type.
func unmodelledKeys(data []byte, targetType reflect.Type) []unmodelledKey {
	var root interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return []unmodelledKey{{path: "<parse-error>", jsonType: err.Error()}}
	}
	var results []unmodelledKey
	walkJSON(root, targetType, "", &results)
	return results
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
}

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
	"GET /posts-search/source-resources": {
		"is_has_more": "boolean",
		"rows_limit":  "number",
		"total_rows":  "number",
	},
	"GET /posts-search/parsing/form": {},
	"GET /proxies":                   {},
	"GET /watermarks":                {},
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
				t.Logf("non-structural decode error, not a shape failure (a custom UnmarshalJSON rejected a placeholder value): %v", err)
			}
		})
	}
}

// TestLiveFixtureDecodes_DetectsAWrongShape is the gate on the gate above.
//
// TestLiveFixtureDecodes deliberately ignores non-structural decode errors,
// and schedule_posts.json produces one: a FlexInt field reads the "str"
// placeholder and its custom UnmarshalJSON rejects the value. If that error
// were returned BEFORE the structural one, the shape check would be silently
// blind on the very fixture it was written for — and blind in the direction
// that looks green.
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

	// And the correct declaration decodes the same bytes without a structural
	// error, so the assertion above is discriminating rather than universal.
	err = json.Unmarshal(data, &SchedulePostsResponse{})
	if errors.As(err, &typeErr) {
		t.Errorf("the CORRECT shape still reports a structural error: %v", err)
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
