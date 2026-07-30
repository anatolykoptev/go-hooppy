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
// there are no "unmodelled keys" below them. interface{} fields (Attachment.Data)
// are leaves too — anything is accepted.
//
// # The gate — declared baseline, not a report
//
// A test that merely prints 429 unmodelled keys fails on day one and gets
// ignored. Following the pattern this repo already has (phantom_filter_test.go,
// TestRetryPolicySweep), each endpoint DECLARES its currently-unmodelled keys
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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// unmodelledKey is one JSON key present in a fixture but not modelled by the
// target Go struct, with its JSON path and JSON type.
type unmodelledKey struct {
	path     string
	jsonType string
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

// walkJSON walks a generic-decoded JSON tree against a target Go struct type
// via reflection, collecting every JSON key that has no corresponding struct
// field. Honours json tags, embedded structs, map[string]… element types, and
// slices. Types implementing json.Unmarshaler (Metric, FlexInt, PhotoID) are
// treated as leaves — their custom unmarshaller owns the whole value, so
// there are no "unmodelled keys" below them. interface{} fields are leaves
// too (anything is accepted).
func walkJSON(node interface{}, targetType reflect.Type, path string, results *[]unmodelledKey) {
	targetType = derefType(targetType)

	// A custom UnmarshalJSON owns the entire value — stop.
	if targetType.Implements(jsonUnmarshalerType) {
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
					*results = append(*results, unmodelledKey{childPath, jsonTypeName(n[k])})
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
		return []unmodelledKey{{"<parse-error>", err.Error()}}
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
// (see walkJSON). 429 unmodelled keys across 16 fixtures (2026-07-29).
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

// TestUnknownFieldDiagnostic is the gate: for each fixture, walk the JSON key
// tree against the response type and compare the unmodelled set against the
// declared baseline. Fails on any divergence — see unmodelledBaselines.
func TestUnknownFieldDiagnostic(t *testing.T) {
	for _, spec := range liveFixtureSpecs {
		t.Run(spec.endpoint, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "live", spec.file))
			if err != nil {
				t.Fatalf("read testdata/live/%s: %v", spec.file, err)
			}
			actual := unmodelledKeys(data, spec.targetTyp)
			actualMap := make(map[string]string, len(actual))
			for _, k := range actual {
				actualMap[k.path] = k.jsonType
			}
			declared := unmodelledBaselines[spec.endpoint]
			if declared == nil {
				declared = map[string]string{}
			}

			// Newly-appeared: in fixture but not declared.
			var newKeys []string
			for path, typ := range actualMap {
				if _, ok := declared[path]; !ok {
					newKeys = append(newKeys, fmt.Sprintf("%s (%s)", path, typ))
				}
			}
			sort.Strings(newKeys)
			for _, k := range newKeys {
				t.Errorf("unmodelled key not in baseline (newly-appeared server field): %s — %s", spec.endpoint, k)
			}

			// Stale: declared but not in actual unmodelled set.
			var root interface{}
			_ = json.Unmarshal(data, &root)
			var staleKeys []string
			for path, typ := range declared {
				if _, ok := actualMap[path]; !ok {
					if pathExistsInFixture(root, path) {
						staleKeys = append(staleKeys, fmt.Sprintf("%s (%s) — now modelled by a struct field; remove from baseline", path, typ))
					} else {
						staleKeys = append(staleKeys, fmt.Sprintf("%s (%s) — no longer present in fixture; remove from baseline", path, typ))
					}
				}
			}
			sort.Strings(staleKeys)
			for _, k := range staleKeys {
				t.Errorf("stale baseline declaration: %s — %s", spec.endpoint, k)
			}
		})
	}
}
