// Command specgen produces api/openapi-measured.yaml — an OpenAPI 3.1 document
// describing the Hooppy API as MEASURED, not as advertised.
//
// Response schemas are DERIVED from the recorded fixtures in testdata/live/ by
// JSON-to-JSON-Schema inference, not authored by hand. Paths, operations, and
// parameters are encoded as data structures below, grounded in the client
// source (endpoints.go, posts.go, posts_search.go, accounts.go, etc.) and the
// measurement comments therein.
//
// Usage:
//
//	GOWORK=off go run ./cmd/specgen
//
// Determinism: file walk is sorted, map keys are sorted by yaml.v3, all
// annotations are keyed by field name from a fixed set. Running twice produces
// identical output (F3).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// fixtureDir is the directory of recorded live responses (reduced placeholders).
const fixtureDir = "testdata/live"

// outputFile is the measured spec destination.
const outputFile = "api/openapi-measured.yaml"

// ============================================================================
// SENSITIVE FIELDS — credential-bearing fields that must not be surfaced.
// Marked x-sensitive: true in the schema. No example values are ever emitted.
// ============================================================================

var sensitiveFields = map[string]bool{
	"access_token":                    true,
	"access_token_secret":             true,
	"access_web_token":                true,
	"password":                        true,
	"bot_token":                       true,
	"refresh_token":                   true,
	"tw_app_secret":                   true,
	"wp_app_password":                 true,
	"api_token":                       true,
	"tw_app_id":                       true,
	"client_id":                       true,
	"device_seed_prefix":              true,
	"wa_instance_api_token":           true,
	"vk_clips_token":                  true,
	"jm_token":                        true,
	"tw_last_oauth_token":             true,
	"ru_captcha_key":                  true,
	"gpt_key":                         true,
	"temporary_password_for_max":      true,
	"temporary_password_for_telegram": true,
}

// ============================================================================
// HOSTILE TYPES — fields declared int in the Go payload struct but returned as
// string by the server. Marked x-write-type: integer in the schema property.
// 12 on ProjectPayload, 2 on SchedulePayload (see task spec §7).
// ============================================================================

var hostileTypeFields = map[string]bool{
	// Declared int on the Go payload struct, returned as string by the server.
	// The key is the JSON field name, so one entry can cover the same field on
	// more than one struct — the headings say where each is DECLARED, which is
	// the thing that was measured.
	//
	// Shared by ProjectPayload (types.go:513-571) and SchedulePayload
	// (types.go:104-153); both mark them KNOWN-HOSTILE:
	"publish_as_story_source_ids":      true,
	"share_stories_to_feed_source_ids": true,
	// ProjectPayload only (types.go:513-571):
	"publish_by_account_source_ids": true,
	"posts_caption":                 true,
	"photos_caption":                true,
	"tg_buttons":                    true,
	"videos_title":                  true,
	"posts_comment":                 true,
	"posts_rewrite":                 true,
	"posts_location":                true,
	"posts_location_vk":             true,
	"posts_photo":                   true,
	// ProjectPayload (types.go:564-565); reached only through the project and
	// schedule objects nested in search_post_edit, never on a schedule row:
	"posts_hashtags": true,
	"posts_links":    true,
}

// ============================================================================
// PHANTOM PARAMETERS — accepted by the server, silently ignored.
// Marked x-phantom: true with a description of observed behaviour.
// ============================================================================

type phantomParam struct {
	name        string
	description string
	provenance  string // code-comment citation
}

var phantomParamsPosts = []phantomParam{
	{name: "page_id", description: "Accepted by the server and silently ignored — an impossible id returns the full collection that looks filtered. Measured by row content, not total_rows (which caps at 10000). Use schedule_id, source_id, or project_id to narrow.", provenance: "code-comment: posts.go:63"},
	{name: "account_id", description: "Accepted by the server and silently ignored on /posts — an impossible id returns the full collection that looks filtered. Note: account_id IS honoured on /accounts/pages (opposite behaviour, same name, different endpoint). Measured by row content.", provenance: "code-comment: posts.go:63"},
}

var phantomParamsPostsSearch = []phantomParam{
	{name: "source_id", description: "Accepted by the server and silently ignored on /posts-search — returns an unfiltered result set that looks filtered. Note: source_id IS honoured on /posts and /accounts (opposite behaviour, same name, different endpoint). Measured by row content, not total_rows (which caps at 10000).", provenance: "code-comment: posts_search.go:78"},
	{name: "source_resource_id", description: "Accepted by the server and silently ignored — returns an unfiltered result set that looks filtered. Measured by row content.", provenance: "code-comment: posts_search.go:78"},
	{name: "owner_id", description: "Accepted by the server and silently ignored — returns rows from multiple different owners. Measured by row content.", provenance: "code-comment: posts_search.go:78"},
}

// ============================================================================
// PATH/OPERATION DEFINITIONS
// Each path carries its methods, parameters, provenance, and fixture mapping.
// ============================================================================

type paramDef struct {
	name        string
	in          string // query, path
	schemaType  string // integer, string, boolean
	description string
	provenance  string
	phantom     bool
}

type responseDef struct {
	statusCode  int
	fixtures    map[string]string // fixture filename → schema name
	provenance  string
	description string
}

type operationDef struct {
	method      string
	operationID string
	summary     string
	description string
	tags        []string
	params      []paramDef
	requestBody string // schema name or ""
	responses   []responseDef
	provenance  string
}

type pathDef struct {
	path       string
	operations []operationDef
	provenance string
	vendorSpec bool // true if in the vendor's 9-path OpenAPI
}

func buildPaths() []pathDef {
	honoured := "measured-live: 2026-07-30"
	fixtureProv := func(f string) string { return "fixture: testdata/live/" + f }

	return []pathDef{
		// ---- /accounts (vendor spec) ----
		{
			path: "/accounts", vendorSpec: true, provenance: "code-comment: endpoints.go:5",
			operations: []operationDef{
				{
					method: "get", operationID: "getAccounts", tags: []string{"Accounts"},
					summary:     "List connected social network accounts",
					description: "Returns the paging envelope {list, total_rows, is_has_more, rows_limit}. The list items carry credential-bearing fields (access_token, access_token_secret, access_web_token, password, bot_token, refresh_token, tw_app_secret, wp_app_password, and more) — a client must not surface them.",
					params: []paramDef{
						{name: "source_id", in: "query", schemaType: "integer", description: "Filter by social network type. Honoured: a non-zero value narrows the result set.", provenance: honoured},
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "unverified"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size. The response echoes this as rows_limit. Server default is 20. Honoured to at least 1000 (measured). This is why every walk was 50× more expensive than needed (#125).", provenance: honoured},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"accounts.json": "AccountsListResponse"}, provenance: fixtureProv("accounts.json"), description: "List of accounts with paging envelope."},
					},
					provenance: fixtureProv("accounts.json"),
				},
			},
		},
		// ---- /accounts/pages (vendor spec) ----
		{
			path: "/accounts/pages", vendorSpec: true, provenance: "code-comment: endpoints.go:6",
			operations: []operationDef{
				{
					method: "get", operationID: "getPages", tags: []string{"Accounts"},
					summary:     "List connected social network pages/groups",
					description: "Returns the paging envelope. Each page item carries access_token and access_token_secret — a client must not surface them. Each item also embeds the full parent account object under .account, which carries all credential fields.",
					params: []paramDef{
						{name: "source_id", in: "query", schemaType: "integer", description: "Filter by social network type.", provenance: "unverified"},
						{name: "account_id", in: "query", schemaType: "integer", description: "Filter by account. Honoured: a non-zero value narrows the result set.", provenance: honoured},
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "unverified"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size. Response echoes as rows_limit. Server default 20.", provenance: honoured},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"accounts_pages.json": "PagesListResponse"}, provenance: fixtureProv("accounts_pages.json"), description: "List of pages with paging envelope."},
					},
					provenance: fixtureProv("accounts_pages.json"),
				},
			},
		},
		// ---- /accounts/pages/{id} (undocumented) ----
		{
			path: "/accounts/pages/{id}", provenance: "code-comment: endpoints.go:38",
			operations: []operationDef{
				{
					method: "delete", operationID: "disconnectPage", tags: []string{"Accounts"},
					summary:     "Disconnect a social network page",
					description: "Undocumented. A non-existent page returns success.",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Page ID.", provenance: "code-comment: endpoints.go:38"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "Delete response (unverified — no fixture)."},
					},
					provenance: "code-comment: accounts.go:187",
				},
			},
		},
		// ---- /posts/projects (vendor spec) ----
		{
			path: "/posts/projects", vendorSpec: true, provenance: "code-comment: endpoints.go:7",
			operations: []operationDef{
				{
					method: "get", operationID: "getProjects", tags: []string{"Projects"},
					summary:     "List projects",
					description: "Returns the paging envelope. Project objects carry 12+ fields declared int in the write-side payload but returned as string by the server (posts_caption, photos_caption, tg_buttons, videos_title, posts_comment, posts_rewrite, posts_location, posts_location_vk, posts_photo, publish_as_story_source_ids, share_stories_to_feed_source_ids, publish_by_account_source_ids). See x-write-type on each property.",
					params: []paramDef{
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "unverified"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size. Response echoes as rows_limit. Server default 20.", provenance: honoured},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"projects.json": "ProjectsListResponse"}, provenance: fixtureProv("projects.json"), description: "List of projects with paging envelope."},
					},
					provenance: fixtureProv("projects.json"),
				},
				{
					method: "post", operationID: "createProject", tags: []string{"Projects"},
					summary:     "Create a project",
					description: "Undocumented behaviour: the API requires ALL fields of ProjectPayload to be present (discovered via iterative 500-error probing). Use NewProjectPayload(name, pageID) for sensible defaults. The write-side declares 12 fields as int that the server returns as string — see the GET response schema for x-write-type annotations.",
					requestBody: "ProjectPayload",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "Project response with id and projects array (unverified — no fixture)."},
					},
					provenance: "code-comment: types.go:507",
				},
			},
		},
		// ---- /posts/projects/{id} (undocumented: vendor spec declares /posts/projects but not the {id} sub-path) ----
		{
			path: "/posts/projects/{id}", vendorSpec: false, provenance: "code-comment: endpoints.go:8",
			operations: []operationDef{
				{
					method: "put", operationID: "updateProject", tags: []string{"Projects"},
					summary:     "Update a project name",
					description: "Only the name is updatable. Irreversible, reported as success.",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Project ID.", provenance: "code-comment: endpoints.go:8"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "DeleteResponse shape (unverified — no fixture)."},
					},
					provenance: "code-comment: projects.go:483",
				},
				{
					method: "delete", operationID: "deleteProject", tags: []string{"Projects"},
					summary: "Delete a project",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Project ID.", provenance: "code-comment: endpoints.go:8"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "DeleteResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:8",
				},
			},
		},
		// ---- /posts/schedules (vendor spec) ----
		{
			path: "/posts/schedules", vendorSpec: true, provenance: "code-comment: endpoints.go:9",
			operations: []operationDef{
				{
					method: "get", operationID: "getSchedules", tags: []string{"Schedules"},
					summary:     "List schedules",
					description: "Returns the paging envelope. The schedule row carries 9 properties annotated x-write-type — declared int somewhere in the Go payload structs, returned as string here (photos_caption, posts_caption, posts_comment, posts_location, posts_photo, publish_as_story_source_ids, publish_by_account_source_ids, share_stories_to_feed_source_ids, videos_title). Only publish_as_story_source_ids and share_stories_to_feed_source_ids are declared on SchedulePayload itself (types.go:121, 129); the other seven are ProjectPayload declarations that the server also returns on this row.",
					params: []paramDef{
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "unverified"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size. Response echoes as rows_limit. Server default 20.", provenance: honoured},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"schedules.json": "SchedulesListResponse"}, provenance: fixtureProv("schedules.json"), description: "List of schedules with paging envelope."},
					},
					provenance: fixtureProv("schedules.json"),
				},
				{
					method: "post", operationID: "createSchedule", tags: []string{"Schedules"},
					summary:     "Create a schedule",
					description: "The API requires all SchedulePayload fields present. Use NewSchedulePayload(name).",
					requestBody: "SchedulePayload",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "ScheduleResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: types.go:200",
				},
			},
		},
		// ---- /posts/schedules/{id} (undocumented: vendor spec declares /posts/schedules but not the {id} sub-path) ----
		{
			path: "/posts/schedules/{id}", vendorSpec: false, provenance: "code-comment: endpoints.go:10",
			operations: []operationDef{
				{
					method: "put", operationID: "updateSchedule", tags: []string{"Schedules"},
					summary: "Update a schedule",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Schedule ID.", provenance: "code-comment: endpoints.go:10"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "ScheduleEditResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:10",
				},
				{
					method: "delete", operationID: "deleteSchedule", tags: []string{"Schedules"},
					summary: "Delete a schedule",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Schedule ID.", provenance: "code-comment: endpoints.go:10"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "DeleteResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:10",
				},
			},
		},
		// ---- /posts/schedules/{id}/edit (undocumented) ----
		{
			path: "/posts/schedules/{id}/edit", provenance: "code-comment: endpoints.go:11",
			operations: []operationDef{
				{
					method: "get", operationID: "getScheduleEdit", tags: []string{"Schedules"},
					summary:     "Get schedule edit form",
					description: "Returns the full schedule edit form with all fields, nested objects (posts_hashtags, posts_links, posts_rewrite as objects), calendar, and page/account selectors. 72 top-level keys while the Go struct models 12 (issue #82).",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Schedule ID.", provenance: "code-comment: endpoints.go:11"},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"schedule_edit.json": "ScheduleEditResponse"}, provenance: fixtureProv("schedule_edit.json"), description: "Schedule edit form."},
					},
					provenance: fixtureProv("schedule_edit.json"),
				},
			},
		},
		// ---- /posts/schedules/{id}/posts (undocumented) ----
		{
			path: "/posts/schedules/{id}/posts", provenance: "code-comment: endpoints.go:28",
			operations: []operationDef{
				{
					method: "get", operationID: "getSchedulePosts", tags: []string{"Schedules"},
					summary:     "Get a schedule's queue depth and per-day calendar",
					description: "Returns posts_by_days — a map of day objects keyed dd.mm.yyyy. The empty form arrives as a JSON array [] (#119), not an empty object {}. total_rows is the real depth regardless of truncation; is_has_more signals a truncated first page. One call returns the whole calendar; this endpoint does NOT page (issue #106).",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Schedule ID (required).", provenance: "code-comment: endpoints.go:28"},
						{name: "date_from", in: "query", schemaType: "string", description: "dd.mm.yyyy — narrows the calendar start.", provenance: "code-comment: projects.go:636"},
						{name: "date_to", in: "query", schemaType: "string", description: "dd.mm.yyyy — narrows the calendar end.", provenance: "code-comment: projects.go:639"},
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page for truncated results.", provenance: "code-comment: projects.go:642"},
					},
					responses: []responseDef{
						{
							statusCode: 200,
							fixtures: map[string]string{
								"schedule_posts.json":       "SchedulePostsResponse",
								"schedule_posts_empty.json": "SchedulePostsEmptyResponse",
							},
							provenance:  fixtureProv("schedule_posts.json"),
							description: "Schedule queue with per-day calendar. posts_by_days is a map when non-empty, an array when empty (#119).",
						},
					},
					provenance: fixtureProv("schedule_posts.json"),
				},
			},
		},
		// ---- /posts (vendor spec) ----
		{
			path: "/posts", vendorSpec: true, provenance: "code-comment: endpoints.go:14",
			operations: []operationDef{
				{
					method: "get", operationID: "getPosts", tags: []string{"Posts"},
					summary:     "List posts",
					description: "Returns the paging envelope {list, total_rows, is_has_more, rows_limit} plus filters_plug and selected_filters_placeholders. publication_date.timestamp is 10 hours behind the displayed slot while source_timestamp is the real one (#110). Phantom parameters page_id and account_id are accepted and silently ignored.",
					params: []paramDef{
						{name: "schedule_id", in: "query", schemaType: "integer", description: "Filter by schedule. Honoured.", provenance: honoured},
						{name: "project_id", in: "query", schemaType: "integer", description: "Filter by project. Honoured.", provenance: honoured},
						{name: "is_published", in: "query", schemaType: "integer", description: "0 = unpublished, 1 = published. Honoured.", provenance: honoured},
						{name: "publication_date", in: "query", schemaType: "string", description: "dd.mm.yyyy. Honoured.", provenance: honoured},
						{name: "source_id", in: "query", schemaType: "integer", description: "Filter by social network. Honoured on /posts (but phantom on /posts-search).", provenance: honoured},
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "unverified"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size. Response echoes as rows_limit. Server default 20. Honoured to at least 1000.", provenance: honoured},
						{name: "page_id", in: "query", schemaType: "integer", description: "PHANTOM: accepted and silently ignored. An impossible id returns the full collection that looks filtered.", provenance: "code-comment: posts.go:63", phantom: true},
						{name: "account_id", in: "query", schemaType: "integer", description: "PHANTOM on /posts: accepted and silently ignored. (account_id IS honoured on /accounts/pages — opposite behaviour, same name.)", provenance: "code-comment: posts.go:63", phantom: true},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"posts.json": "PostsListResponse"}, provenance: fixtureProv("posts.json"), description: "List of posts with paging envelope."},
					},
					provenance: fixtureProv("posts.json"),
				},
				{
					method: "post", operationID: "createPost", tags: []string{"Posts"},
					summary:     "Create a post",
					description: "Accepts PostPublishNowPayload, PostPublishAtPayload, PostPublishBySchedulePayload, or PostPublishByProjectPayload.",
					requestBody: "PostPublishPayload",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "CreatePostResponse with id (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:14",
				},
			},
		},
		// ---- /posts/{id} (vendor spec) ----
		{
			path: "/posts/{id}", vendorSpec: true, provenance: "code-comment: endpoints.go:15",
			operations: []operationDef{
				{
					method: "put", operationID: "updatePost", tags: []string{"Posts"},
					summary:     "Update a post",
					description: "The final segment is overloaded: an integer is a post id, while a non-integer selects a cross-post MODE (search, copy, sources, import, crosspost, rewrite, translate, queue, drafts, templates, rss, feeds, tags), which accepts the POST /posts payload and returns {id}. OpenAPI cannot express both — /posts/{id} and /posts/{mode} are the same template and 3.1 §4.8.8.2 forbids declaring both — so the mode form is recorded here rather than as its own path. Mode dispatch is from a code comment (types.go:590), unverified against a live response. Create-shaped PUTs are non-idempotent.",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Post ID.", provenance: "code-comment: endpoints.go:15"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "Update response (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:39",
				},
				{
					method: "delete", operationID: "deletePost", tags: []string{"Posts"},
					summary: "Delete a post",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Post ID.", provenance: "code-comment: endpoints.go:15"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "DeletePostResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:15",
				},
			},
		},
		// ---- /posts/{id}/edit (undocumented) ----
		{
			path: "/posts/{id}/edit", provenance: "code-comment: endpoints.go:52",
			operations: []operationDef{
				{
					method: "get", operationID: "getPostEdit", tags: []string{"Posts"},
					summary:     "Get post edit form",
					description: "Returns the full post edit form with attachments, projects, schedules, publication_date, texts, and page/account selectors.",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Post ID.", provenance: "code-comment: endpoints.go:52"},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"post_edit.json": "PostEditResponse"}, provenance: fixtureProv("post_edit.json"), description: "Post edit form."},
					},
					provenance: fixtureProv("post_edit.json"),
				},
			},
		},
		// ---- /posts/batch/delete (vendor spec) ----
		{
			path: "/posts/batch/delete", vendorSpec: true, provenance: "code-comment: endpoints.go:16",
			operations: []operationDef{
				{
					method: "post", operationID: "batchDeletePosts", tags: []string{"Posts"},
					summary:     "Batch delete posts",
					description: "posts_ids is a comma-joined STRING, not a JSON array. Max 1000 IDs per call.",
					requestBody: "BatchDeletePostsRequest",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "BatchDeleteResponse with success field (unverified — no fixture). A 2xx with {success:false} is a failure."},
					},
					provenance: "code-comment: posts.go:694",
				},
			},
		},
		// ---- /posts/batch/move (undocumented) ----
		{
			path: "/posts/batch/move", provenance: "code-comment: endpoints.go:23",
			operations: []operationDef{
				{
					method: "post", operationID: "batchMovePosts", tags: []string{"Posts"},
					summary:     "Batch move posts to another schedule",
					description: "Undocumented. posts_ids is a comma-joined STRING, not a JSON array (a JSON array makes the server throw ErrorException: explode(...) and return 500 — measured live 2026-07-30, issue #105). Max 1000 IDs. A 2xx with {success:false} is a real failure the transport layer does not surface.",
					requestBody: "BatchMovePostsRequest",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "BatchMovePostsResponse with success field (unverified — no fixture)."},
					},
					provenance: "code-comment: posts.go:580",
				},
			},
		},
		// ---- /posts/copy (undocumented) ----
		{
			path: "/posts/copy", provenance: "code-comment: endpoints.go:41",
			operations: []operationDef{
				{
					method: "post", operationID: "copySearchPost", tags: []string{"Posts"},
					summary:     "Copy a scraped post",
					description: "Undocumented. Copies a post from the search index to the user's queue.",
					requestBody: "CopySearchPostPayload",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "PostIDResponse with id (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:41",
				},
			},
		},
		// ---- /posts/rewrite (undocumented) ----
		{
			path: "/posts/rewrite", provenance: "code-comment: endpoints.go:50",
			operations: []operationDef{
				{
					method: "post", operationID: "rewriteSearchPost", tags: []string{"Posts"},
					summary:     "Rewrite a scraped post",
					description: "Undocumented. Rewrites and copies a post from the search index.",
					requestBody: "CopySearchPostPayload",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "PostIDResponse with id (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:50",
				},
			},
		},
		// ---- /posts/import (undocumented) ----
		{
			path: "/posts/import", provenance: "code-comment: endpoints.go:43",
			operations: []operationDef{
				{
					method: "post", operationID: "importSearchPost", tags: []string{"Posts"},
					summary:     "Import a scraped post",
					description: "Undocumented. Imports a post from the search index without rewriting.",
					requestBody: "CopySearchPostPayload",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "PostIDResponse with id (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:43",
				},
			},
		},
		// ---- /posts/sources (undocumented) ----
		{
			path: "/posts/sources", provenance: "code-comment: endpoints.go:42",
			operations: []operationDef{
				{
					method: "post", operationID: "addPostSource", tags: []string{"Posts"},
					summary:     "Add a post source",
					description: "Undocumented.",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "Unverified — no fixture."},
					},
					provenance: "code-comment: endpoints.go:42",
				},
			},
		},
		// ---- /posts-search (undocumented) ----
		{
			path: "/posts-search", provenance: "code-comment: endpoints.go:45",
			operations: []operationDef{
				{
					method: "get", operationID: "searchPosts", tags: []string{"PostsSearch"},
					summary:     "Search scraped posts",
					description: "Elasticsearch-backed search. The result window is from + size <= 10000; past it the server returns 500 'Result window is too large'. total_rows reports 10000 as a CEILING, not a count — is_has_more never clears past the window, so the envelope cannot detect the end. Three phantom parameters (source_id, source_resource_id, owner_id) are accepted and silently ignored. The paging envelope DROPS is_has_more on this endpoint (see fixture — it carries is_has_more but total_rows is capped).",
					params: []paramDef{
						{name: "text", in: "query", schemaType: "string", description: "Full-text search.", provenance: "code-comment: posts_search.go:82"},
						{name: "date_from", in: "query", schemaType: "string", description: "dd.mm.yyyy.", provenance: "code-comment: posts_search.go:85"},
						{name: "date_to", in: "query", schemaType: "string", description: "dd.mm.yyyy.", provenance: "code-comment: posts_search.go:88"},
						{name: "source_type", in: "query", schemaType: "integer", description: "1=social, 2=RSS. An impossible enum value is NOT a probe — the server ignores unrecognised values and returns everything.", provenance: "code-comment: posts_search.go:106"},
						{name: "sort_by", in: "query", schemaType: "string", description: "publication_date, likes, reposts, comments, views, involvement. Honoured.", provenance: honoured},
						{name: "sort_direction", in: "query", schemaType: "string", description: "desc (default) or asc.", provenance: "code-comment: posts_search.go:118"},
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed. Offset paging past the ES window (from + size > 10000) returns 500.", provenance: "code-comment: posts_search.go:109"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size (ES size). Response echoes as rows_limit. Honoured to at least 1000. Server default 20. from + size <= 10000 enforced by Elasticsearch.", provenance: honoured},
						{name: "photos_amount", in: "query", schemaType: "integer", description: "Photo-count bucket key. Pass-through: valid key space not enumerable client-side.", provenance: "code-comment: posts_search.go:179"},
						{name: "video_duration", in: "query", schemaType: "integer", description: "Video-duration bucket key. Keys 1-8 work; 9,10 error. Do NOT hardcode an upper bound.", provenance: "code-comment: posts_search.go:185"},
						{name: "content_types", in: "query", schemaType: "string", description: "Comma-separated: photos, videos, audios, documents, links (AND filter).", provenance: "code-comment: posts_search.go:188"},
						{name: "content_types_exclude", in: "query", schemaType: "string", description: "Comma-separated — exclude posts with these types.", provenance: "code-comment: posts_search.go:191"},
						{name: "source_id", in: "query", schemaType: "integer", description: "PHANTOM: accepted and silently ignored on /posts-search. (source_id IS honoured on /posts and /accounts.)", provenance: "code-comment: posts_search.go:78", phantom: true},
						{name: "source_resource_id", in: "query", schemaType: "integer", description: "PHANTOM: accepted and silently ignored.", provenance: "code-comment: posts_search.go:78", phantom: true},
						{name: "owner_id", in: "query", schemaType: "integer", description: "PHANTOM: accepted and silently ignored — returns rows from multiple owners.", provenance: "code-comment: posts_search.go:78", phantom: true},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"posts_search.json": "SearchPostsListResponse"}, provenance: fixtureProv("posts_search.json"), description: "Search results with paging envelope. total_rows caps at 10000."},
						{statusCode: 500, provenance: "code-comment: posts_search.go:319", description: "Result window is too large — from + size > 10000."},
					},
					provenance: fixtureProv("posts_search.json"),
				},
			},
		},
		// ---- /posts-search/source-resources (undocumented) ----
		{
			path: "/posts-search/source-resources", provenance: "code-comment: endpoints.go:46",
			operations: []operationDef{
				{
					method: "get", operationID: "getSourceResources", tags: []string{"PostsSearch"},
					summary:     "List configured source resources for scraping",
					description: "Returns the paging envelope {list, total_rows, is_has_more, rows_limit}.",
					params: []paramDef{
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "code-comment: posts_search.go:378"},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"source_resources.json": "SourceResourcesListResponse"}, provenance: fixtureProv("source_resources.json"), description: "List of source resources with paging envelope."},
					},
					provenance: fixtureProv("source_resources.json"),
				},
			},
		},
		// ---- /posts-search/parsing/form (undocumented) ----
		{
			path: "/posts-search/parsing/form", provenance: "code-comment: endpoints.go:47",
			operations: []operationDef{
				{
					method: "get", operationID: "getParsingForm", tags: []string{"PostsSearch"},
					summary:     "Get parsing form state",
					description: "Returns is_parsing_in_progress, social_accounts, and source_resources. Does NOT carry the paging envelope.",
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"parsing_form.json": "ParsingFormResponse"}, provenance: fixtureProv("parsing_form.json"), description: "Parsing form state."},
					},
					provenance: fixtureProv("parsing_form.json"),
				},
			},
		},
		// ---- /posts-search/parsing/start (undocumented) ----
		{
			path: "/posts-search/parsing/start", provenance: "code-comment: endpoints.go:48",
			operations: []operationDef{
				{
					method: "post", operationID: "startParsing", tags: []string{"PostsSearch"},
					summary:     "Start parsing/scraping",
					description: "Undocumented. ParsingStartPayload marshals with a custom wire format (parsingWireDate).",
					requestBody: "ParsingStartPayload",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "ParsingStartResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:48",
				},
			},
		},
		// ---- /posts-search/parsing/stop (undocumented) ----
		{
			path: "/posts-search/parsing/stop", provenance: "code-comment: endpoints.go:49",
			operations: []operationDef{
				{
					method: "delete", operationID: "stopParsing", tags: []string{"PostsSearch"},
					summary:     "Stop parsing/scraping",
					description: "Undocumented. The suffix-less DELETE /posts-search/parsing was the wrong path — both it and /stop answer {success:true} but only /stop actually cancels (measured).",
					responses: []responseDef{
						{statusCode: 200, provenance: "code-comment: posts_search.go:466", description: "{success: true}. A 2xx with {success:false} is a failure. A success response is NOT evidence the parsing stopped — only /stop cancels."},
					},
					provenance: "code-comment: posts_search.go:466",
				},
			},
		},
		// ---- /posts-search/{id}/edit (undocumented) ----
		{
			path: "/posts-search/{id}/edit", provenance: "code-comment: endpoints.go:51",
			operations: []operationDef{
				{
					method: "get", operationID: "getSearchPostEdit", tags: []string{"PostsSearch"},
					summary:     "Get scraped post edit form",
					description: "Returns the edit form for a scraped post. The id field is a STRING here (unlike /posts/{id}/edit where it is an integer).",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "string", description: "Search post ID (string, not integer).", provenance: "code-comment: endpoints.go:51"},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"search_post_edit.json": "SearchPostEditResponse"}, provenance: fixtureProv("search_post_edit.json"), description: "Search post edit form."},
					},
					provenance: fixtureProv("search_post_edit.json"),
				},
			},
		},
		// ---- /files/media/upload (vendor spec) ----
		{
			path: "/files/media/upload", vendorSpec: true, provenance: "code-comment: endpoints.go:12",
			operations: []operationDef{
				{
					method: "post", operationID: "uploadMedia", tags: []string{"Files"},
					summary:     "Upload a media file (multipart)",
					description: "Multipart form upload. Max 50 MB (MaxUploadBytes).",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "UploadMediaResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:12",
				},
			},
		},
		// ---- /files/documents/upload (vendor spec) ----
		{
			path: "/files/documents/upload", vendorSpec: true, provenance: "code-comment: endpoints.go:13",
			operations: []operationDef{
				{
					method: "post", operationID: "uploadDocument", tags: []string{"Files"},
					summary:     "Upload a document file (multipart)",
					description: "Multipart form upload. Max 50 MB (MaxUploadBytes).",
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "UploadDocumentResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:13",
				},
			},
		},
		// ---- /users/me (undocumented) ----
		{
			path: "/users/me", provenance: "code-comment: endpoints.go:31",
			operations: []operationDef{
				{
					method: "get", operationID: "getUser", tags: []string{"Users"},
					summary:     "Get the current user",
					description: "Returns the user object wrapped in {user: ...}. Carries api_token (credential — must not be surfaced). Does NOT carry the paging envelope.",
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"users_me.json": "UserResponse"}, provenance: fixtureProv("users_me.json"), description: "User object."},
					},
					provenance: fixtureProv("users_me.json"),
				},
			},
		},
		// ---- /users/settings (undocumented) ----
		{
			path: "/users/settings", provenance: "code-comment: endpoints.go:32",
			operations: []operationDef{
				{
					method: "get", operationID: "getUserSettings", tags: []string{"Users"},
					summary:     "Get user settings",
					description: "Returns settings flat (not wrapped). Carries api_token (credential). Does NOT carry the paging envelope.",
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"users_settings.json": "SettingsResponse"}, provenance: fixtureProv("users_settings.json"), description: "User settings."},
					},
					provenance: fixtureProv("users_settings.json"),
				},
			},
		},
		// ---- /watermarks (undocumented) ----
		{
			path: "/watermarks", provenance: "code-comment: endpoints.go:33",
			operations: []operationDef{
				{
					method: "get", operationID: "getWatermarks", tags: []string{"Watermarks"},
					summary:     "List watermarks",
					description: "Returns the paging envelope. The list can be empty (see watermarks.json fixture).",
					params: []paramDef{
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "code-comment: watermarks.go:25"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size. Response echoes as rows_limit. Server default 20.", provenance: honoured},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"watermarks.json": "WatermarksListResponse"}, provenance: fixtureProv("watermarks.json"), description: "List of watermarks with paging envelope (can be empty)."},
					},
					provenance: fixtureProv("watermarks.json"),
				},
			},
		},
		// ---- /watermarks/{id} (undocumented) ----
		{
			path: "/watermarks/{id}", provenance: "code-comment: endpoints.go:34",
			operations: []operationDef{
				{
					method: "delete", operationID: "deleteWatermark", tags: []string{"Watermarks"},
					summary: "Delete a watermark",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Watermark ID.", provenance: "code-comment: endpoints.go:34"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "WatermarkResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:34",
				},
			},
		},
		// ---- /proxies (undocumented) ----
		{
			path: "/proxies", provenance: "code-comment: endpoints.go:35",
			operations: []operationDef{
				{
					method: "get", operationID: "getProxies", tags: []string{"Proxies"},
					summary:     "List proxies",
					description: "Returns the paging envelope. Proxy objects carry password (credential — must not be surfaced).",
					params: []paramDef{
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "code-comment: proxies.go:25"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size. Response echoes as rows_limit. Server default 20.", provenance: honoured},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"proxies.json": "ProxiesListResponse"}, provenance: fixtureProv("proxies.json"), description: "List of proxies with paging envelope."},
					},
					provenance: fixtureProv("proxies.json"),
				},
			},
		},
		// ---- /proxies/{id} (undocumented) ----
		{
			path: "/proxies/{id}", provenance: "code-comment: endpoints.go:36",
			operations: []operationDef{
				{
					method: "put", operationID: "updateProxy", tags: []string{"Proxies"},
					summary: "Update a proxy",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Proxy ID.", provenance: "code-comment: endpoints.go:36"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "ProxyResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:36",
				},
				{
					method: "delete", operationID: "deleteProxy", tags: []string{"Proxies"},
					summary: "Delete a proxy",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Proxy ID.", provenance: "code-comment: endpoints.go:36"},
					},
					responses: []responseDef{
						{statusCode: 200, provenance: "unverified", description: "ProxyResponse (unverified — no fixture)."},
					},
					provenance: "code-comment: endpoints.go:36",
				},
			},
		},
		// ---- /notifications (undocumented) ----
		{
			path: "/notifications", provenance: "code-comment: endpoints.go:37",
			operations: []operationDef{
				{
					method: "get", operationID: "getNotifications", tags: []string{"Notifications"},
					summary:     "List notifications",
					description: "Returns the paging envelope plus filters_plug. Notification objects embed a page object that carries access_token and access_token_secret (credentials).",
					params: []paramDef{
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "code-comment: notifications.go:25"},
						{name: "limit", in: "query", schemaType: "integer", description: "Page size. Response echoes as rows_limit. Server default 20.", provenance: honoured},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"notifications.json": "NotificationsListResponse"}, provenance: fixtureProv("notifications.json"), description: "List of notifications with paging envelope."},
					},
					provenance: fixtureProv("notifications.json"),
				},
			},
		},
		// ---- /cross-posting (undocumented) ----
		{
			path: "/cross-posting", provenance: "code-comment: endpoints.go:56",
			operations: []operationDef{
				{
					method: "get", operationID: "getCrossPostings", tags: []string{"CrossPosting"},
					summary:     "List cross-posting connections",
					description: "The cross-posting rule engine: collects from a source on a timer, ranks by engagement, filters by threshold, deduplicates and publishes into a schedule. Integer enums (search_mode, search_mode_direction, determine_best_by, check_when_type, check_interval) carry no names on the wire; the client decodes them alongside the raw value. last_check_date and instagram_last_check_date arrive as a number, a numeric string, or null.",
					params: []paramDef{
						{name: "page", in: "query", schemaType: "integer", description: "1-indexed page number.", provenance: "code-comment: crossposting.go:24"},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"cross_postings.json": "CrossPostingsListResponse"}, provenance: fixtureProv("cross_postings.json"), description: "Cross-posting connections with paging envelope."},
					},
					provenance: fixtureProv("cross_postings.json"),
				},
			},
		},
		// ---- /cross-posting/{id}/edit (undocumented) ----
		{
			path: "/cross-posting/{id}/edit", provenance: "code-comment: endpoints.go:57",
			operations: []operationDef{
				{
					method: "get", operationID: "getCrossPostingEdit", tags: []string{"CrossPosting"},
					summary:     "Get a cross-posting connection's full editable state",
					description: "Carries the whole rule: source, targets, thresholds, schedule binding and filters. Fields that are null on the recording account have unmeasured types — a null here means not observed, not nullable by contract.",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Cross-posting connection ID.", provenance: "code-comment: endpoints.go:57"},
					},
					responses: []responseDef{
						{statusCode: 200, fixtures: map[string]string{"cross_posting_edit.json": "CrossPostingEditResponse"}, provenance: fixtureProv("cross_posting_edit.json"), description: "Full editable state of one cross-posting connection."},
					},
					provenance: fixtureProv("cross_posting_edit.json"),
				},
			},
		},
		// ---- /cross-posting/{id}/statistics (undocumented) ----
		{
			path: "/cross-posting/{id}/statistics", provenance: "code-comment: endpoints.go:58",
			operations: []operationDef{
				{
					method: "get", operationID: "getCrossPostingStatistics", tags: []string{"CrossPosting"},
					summary:     "Get a cross-posting connection's statistics",
					description: "Per-connection collection and publication counters. The empty form returns statistics as an empty array, so a consumer must not assume the populated shape.",
					params: []paramDef{
						{name: "id", in: "path", schemaType: "integer", description: "Cross-posting connection ID.", provenance: "code-comment: endpoints.go:58"},
					},
					responses: []responseDef{
						{
							statusCode: 200,
							fixtures: map[string]string{
								"cross_posting_statistics.json":       "CrossPostingStatisticsResponse",
								"cross_posting_statistics_empty.json": "CrossPostingStatisticsEmptyResponse",
							},
							provenance:  fixtureProv("cross_posting_statistics.json"),
							description: "Statistics for one connection. The empty form is a distinct shape, recorded separately.",
						},
					},
					provenance: fixtureProv("cross_posting_statistics.json"),
				},
			},
		},
	}
}

// ============================================================================
// SCHEMA INFERENCE — JSON value → JSON Schema (2020-12)
// Walks a decoded JSON value and produces a schema map. The fixture reducer
// replaces every scalar with a type placeholder ("str", 0, 0.0, true, null),
// so the inferred type is the RECORDED type, not a guess at the real value.
// ============================================================================

func inferSchema(v interface{}) map[string]interface{} {
	switch val := v.(type) {
	case string:
		return map[string]interface{}{"type": "string"}
	case bool:
		return map[string]interface{}{"type": "boolean"}
	case float64:
		if val == float64(int64(val)) {
			return map[string]interface{}{"type": "integer"}
		}
		return map[string]interface{}{"type": "number"}
	case nil:
		return map[string]interface{}{"type": "null"}
	case []interface{}:
		schema := map[string]interface{}{"type": "array"}
		if len(val) > 0 {
			schema["items"] = inferSchema(val[0])
		} else {
			schema["items"] = map[string]interface{}{}
		}
		return schema
	case map[string]interface{}:
		props := make(map[string]interface{})
		var required []string
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			propSchema := inferSchema(val[k])
			// Annotate sensitive fields.
			if sensitiveFields[k] {
				propSchema["x-sensitive"] = true
				propSchema["description"] = "Credential-bearing field. A client must not surface this value. No example is provided."
			}
			// Annotate hostile types (int declared, string returned).
			if hostileTypeFields[k] {
				if t, ok := propSchema["type"]; ok && t == "string" {
					propSchema["x-write-type"] = "integer"
					propSchema["description"] = "Hostile type: declared int in the Go payload struct, returned as string by the server. The write-side type is integer."
				}
			}
			// Provenance: every property derived from a fixture.
			propSchema["x-provenance"] = "fixture"
			props[k] = propSchema
			required = append(required, k)
		}
		return map[string]interface{}{
			"type":       "object",
			"properties": props,
			"required":   required,
		}
	default:
		return map[string]interface{}{}
	}
}

// schemaNameFromFixture converts a fixture filename to a PascalCase schema name.
// e.g. "accounts.json" → "AccountsListResponse"
//
//	"schedule_posts_empty.json" → "SchedulePostsEmptyResponse"
var fixtureSchemaNames = map[string]string{
	"accounts.json":                       "AccountsListResponse",
	"cross_posting_edit.json":             "CrossPostingEditResponse",
	"cross_posting_statistics.json":       "CrossPostingStatisticsResponse",
	"cross_posting_statistics_empty.json": "CrossPostingStatisticsEmptyResponse",
	"cross_postings.json":                 "CrossPostingsListResponse",
	"accounts_pages.json":                 "PagesListResponse",
	"notifications.json":                  "NotificationsListResponse",
	"parsing_form.json":                   "ParsingFormResponse",
	"post_edit.json":                      "PostEditResponse",
	"posts.json":                          "PostsListResponse",
	"posts_search.json":                   "SearchPostsListResponse",
	"projects.json":                       "ProjectsListResponse",
	"proxies.json":                        "ProxiesListResponse",
	"schedule_edit.json":                  "ScheduleEditResponse",
	"schedule_posts.json":                 "SchedulePostsResponse",
	"schedule_posts_empty.json":           "SchedulePostsEmptyResponse",
	"schedules.json":                      "SchedulesListResponse",
	"search_post_edit.json":               "SearchPostEditResponse",
	"source_resources.json":               "SourceResourcesListResponse",
	"users_me.json":                       "UserResponse",
	"users_settings.json":                 "SettingsResponse",
	"watermarks.json":                     "WatermarksListResponse",
}

// ============================================================================
// DOCUMENT BUILDER
// ============================================================================

func main() {
	// -check regenerates into memory and asserts the committed spec is
	// byte-identical, instead of overwriting it. The endpoint table above is
	// hand-maintained while the schemas are derived, so editing the table and
	// forgetting to regenerate leaves a spec that still validates every
	// fixture — the conformance test stays green — while no longer describing
	// what the generator produces. Same shape as
	// scripts/record_fixture.py --self-check.
	checkOnly := flag.Bool("check", false, "regenerate into memory and fail if the committed spec differs; do not write")
	flag.Parse()

	// 1. Walk fixtures and infer schemas.
	fixtureSchemas := make(map[string]map[string]interface{})
	fixtureFiles, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "specgen: glob fixtures: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(fixtureFiles)

	for _, fpath := range fixtureFiles {
		fname := filepath.Base(fpath)
		data, err := os.ReadFile(fpath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "specgen: read %s: %v\n", fname, err)
			os.Exit(1)
		}
		var v interface{}
		if err := json.Unmarshal(data, &v); err != nil {
			fmt.Fprintf(os.Stderr, "specgen: parse %s: %v\n", fname, err)
			os.Exit(1)
		}
		schemaName, ok := fixtureSchemaNames[fname]
		if !ok {
			fmt.Fprintf(os.Stderr, "specgen: no schema name for fixture %s — add to fixtureSchemaNames\n", fname)
			os.Exit(1)
		}
		schema := inferSchema(v)
		schema["x-provenance"] = "fixture: testdata/live/" + fname
		schema["x-fixture-file"] = fname
		fixtureSchemas[schemaName] = schema
	}

	// 2. Build paths.
	paths := buildPaths()
	pathMap := make(map[string]interface{})
	for _, p := range paths {
		ops := make(map[string]interface{})
		for _, op := range p.operations {
			opMap := buildOperationMap(op, p)
			ops[op.method] = opMap
		}
		pathEntry := map[string]interface{}{
			"x-provenance": p.provenance,
		}
		if p.vendorSpec {
			pathEntry["x-vendor-spec"] = true
		}
		for k, v := range ops {
			pathEntry[k] = v
		}
		pathMap[p.path] = pathEntry
	}

	// 3. Build components.schemas.
	schemas := make(map[string]interface{})
	// Fixture-derived schemas.
	for name, schema := range fixtureSchemas {
		schemas[name] = schema
	}
	// Placeholder schemas for request bodies (unverified).
	for _, p := range paths {
		for _, op := range p.operations {
			if op.requestBody != "" {
				if _, exists := schemas[op.requestBody]; !exists {
					schemas[op.requestBody] = map[string]interface{}{
						"x-provenance": "unverified",
						"description":  "Request body schema not derived from fixtures — see Go payload struct in types.go.",
					}
				}
			}
		}
	}

	// 4. Assemble the OpenAPI document.
	doc := map[string]interface{}{
		"openapi": "3.1.0",
		"info": map[string]interface{}{
			"title":       "Hooppy API — Measured Specification",
			"description": "OpenAPI 3.1 document describing the Hooppy API as MEASURED, not as advertised. Response schemas are derived from recorded fixtures by a generator (cmd/specgen). Every entry carries x-provenance. The vendor's official spec (hooppy.ru/openapi.yaml v0.1.0) declares 9 paths; this spec covers those plus undocumented endpoints discovered via API probing.\n\nLIMITS, so a consumer does not over-trust this document. Each schema is derived from ONE recorded response, and the recorder reduces every array to a single element: `required` therefore lists the keys that response happened to carry, and `items` describes that one element. A field the server omits sometimes is still listed as required here, so a strictly-validating generated client will reject real traffic. A field that was null on the recording account has an unmeasured type. Where an endpoint has two recorded shapes the response schema is an anyOf over both — anyOf, not oneOf, because a derived empty form is a subset of the populated one rather than disjoint from it. Treat this as a floor on what the API returns, not a contract.",
			"version":     "0.1.0-measured",
			"contact": map[string]interface{}{
				"name": "go-hooppy client",
				"url":  "https://github.com/anatolykoptev/go-hooppy",
			},
			"license": map[string]interface{}{
				"name": "Apache 2.0",
				"url":  "https://www.apache.org/licenses/LICENSE-2.0.html",
			},
		},
		"servers": []interface{}{
			map[string]interface{}{"url": "https://api.hooppy.ru/api"},
		},
		"paths": pathMap,
		"components": map[string]interface{}{
			"schemas": schemas,
		},
		"x-measured-notes": buildMeasuredNotes(),
	}

	// 5. Marshal to YAML.
	out, err := yaml.Marshal(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "specgen: marshal yaml: %v\n", err)
		os.Exit(1)
	}

	// 6. Write, or compare when -check.
	if *checkOnly {
		committed, err := os.ReadFile(outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "specgen -check: read %s: %v\n(run `GOWORK=off go run ./cmd/specgen` to generate it)\n", outputFile, err)
			os.Exit(1)
		}
		if !bytes.Equal(committed, out) {
			fmt.Fprintf(os.Stderr, "specgen -check: %s is stale — it differs from what the generator produces now (committed %d bytes, generated %d).\nRun `GOWORK=off go run ./cmd/specgen` and commit the result.\n", outputFile, len(committed), len(out))
			os.Exit(1)
		}
		fmt.Printf("specgen -check: %s is up to date (%d bytes)\n", outputFile, len(out))
		return
	}
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "specgen: mkdir %s: %v\n", filepath.Dir(outputFile), err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputFile, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "specgen: write %s: %v\n", outputFile, err)
		os.Exit(1)
	}

	// 7. Report.
	var fixtureCount, pathCount, opCount int
	fixtureCount = len(fixtureSchemas)
	pathCount = len(paths)
	for _, p := range paths {
		opCount += len(p.operations)
	}
	fmt.Fprintf(os.Stderr, "specgen: wrote %s — %d paths, %d operations, %d fixture-derived schemas\n",
		outputFile, pathCount, opCount, fixtureCount)
}

func buildOperationMap(op operationDef, p pathDef) map[string]interface{} {
	opMap := map[string]interface{}{
		"operationId":  op.operationID,
		"summary":      op.summary,
		"description":  op.description,
		"x-provenance": op.provenance,
	}
	if len(op.tags) > 0 {
		opMap["tags"] = op.tags
	}

	// Parameters.
	var params []interface{}
	// Add phantom params for specific paths.
	phantomSet := make(map[string]bool)
	for _, pp := range op.params {
		pm := map[string]interface{}{
			"name":         pp.name,
			"in":           pp.in,
			"required":     pp.in == "path",
			"schema":       map[string]interface{}{"type": pp.schemaType},
			"x-provenance": pp.provenance,
		}
		if pp.phantom {
			pm["x-phantom"] = true
			phantomSet[pp.name] = true
		}
		if pp.description != "" {
			pm["description"] = pp.description
		}
		params = append(params, pm)
	}
	if len(params) > 0 {
		opMap["parameters"] = params
	}

	// Request body.
	if op.requestBody != "" {
		opMap["requestBody"] = map[string]interface{}{
			"required": true,
			"content": map[string]interface{}{
				"application/json": map[string]interface{}{
					"schema": map[string]interface{}{
						"$ref": "#/components/schemas/" + op.requestBody,
					},
				},
			},
		}
	}

	// Responses.
	responses := make(map[string]interface{})
	for _, resp := range op.responses {
		respMap := map[string]interface{}{
			"description":  resp.description,
			"x-provenance": resp.provenance,
		}
		if len(resp.fixtures) > 0 {
			fixtureMap := make(map[string]interface{})
			for fname, schemaName := range resp.fixtures {
				fixtureMap["testdata/live/"+fname] = "#/components/schemas/" + schemaName
			}
			respMap["x-fixture"] = fixtureMap
		}
		// Response content. Iterate the fixture names in sorted order, never in
		// map order: a Go map has no first element, so `for … range … break`
		// picked a different schema on different runs and the generated spec
		// was not reproducible. Measured: 1 run in 6 emitted a different $ref
		// for the same input, which makes -check unusable as a gate.
		//
		// An endpoint with more than one recorded fixture genuinely returns
		// more than one shape (a populated body and its empty form), so both
		// are emitted rather than one being picked and the other silently
		// dropped. Which keyword, and why it is not oneOf, is below.
		if len(resp.fixtures) > 0 {
			fnames := make([]string, 0, len(resp.fixtures))
			for fname := range resp.fixtures {
				fnames = append(fnames, fname)
			}
			sort.Strings(fnames)

			refs := make([]interface{}, 0, len(fnames))
			for _, fname := range fnames {
				refs = append(refs, map[string]interface{}{
					"$ref": "#/components/schemas/" + resp.fixtures[fname],
				})
			}
			var schema map[string]interface{}
			if len(refs) == 1 {
				schema = refs[0].(map[string]interface{})
			} else {
				// anyOf, never oneOf. The recorded shapes are not disjoint in
				// general: an empty form derived by the same reducer is a
				// SUBSET of the populated one, because an empty array
				// vacuously satisfies any items constraint. Measured on
				// /cross-posting/{id}/statistics — both fixtures validated
				// against both branches, so oneOf ("exactly one") rejected
				// the very bodies it was generated from.
				schema = map[string]interface{}{"anyOf": refs}
			}
			respMap["content"] = map[string]interface{}{
				"application/json": map[string]interface{}{"schema": schema},
			}
		}
		responses[fmt.Sprintf("%d", resp.statusCode)] = respMap
	}
	opMap["responses"] = responses

	return opMap
}

// buildMeasuredNotes encodes the knowledge the vendor spec cannot express.
func buildMeasuredNotes() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"title":       "Phantom parameters",
			"provenance":  "code-comment: posts.go:63, posts_search.go:78",
			"description": "source_id, source_resource_id, owner_id on /posts-search: accepted, silently ignored, result looks filtered. page_id, account_id on /posts: same. Same name can be honoured on one endpoint and phantom on another (source_id is honoured on /posts, phantom on /posts-search). Marked x-phantom: true on each parameter.",
		},
		map[string]interface{}{
			"title":       "Honoured parameters",
			"provenance":  "measured-live: 2026-07-30",
			"description": "Differential probe comparing row-id sets (never total_rows): /posts schedule_id, project_id, is_published, publication_date; /accounts source_id; /accounts/pages account_id; /posts-search sort_by, limit. All eight honoured.",
		},
		map[string]interface{}{
			"title":       "Page size asymmetry",
			"provenance":  "measured-live: 2026-07-30",
			"description": "The request parameter is limit; the response echoes it as rows_limit. Honoured to at least 1000. Server default is 20. This is why every walk was 50× more expensive than needed (#125).",
		},
		map[string]interface{}{
			"title":       "Paging envelope",
			"provenance":  "fixture: testdata/live/*.json",
			"description": "The envelope {list, total_rows, is_has_more, rows_limit} is carried by /accounts, /accounts/pages, /posts, /posts/projects, /posts/schedules, /posts-search, /posts-search/source-resources, /watermarks, /proxies, /notifications. It is DROPPED by /users/me (wraps in {user:...}), /users/settings (flat), /posts-search/parsing/form (custom shape), /posts/{id}/edit, /posts/schedules/{id}/edit, /posts-search/{id}/edit (edit forms).",
		},
		map[string]interface{}{
			"title":       "Elasticsearch result window on /posts-search",
			"provenance":  "code-comment: posts_search.go:319",
			"description": "from + size <= 10000. Past it the server returns 500 'Result window is too large'. total_rows reports 10000 as a CEILING, not a count, and is_has_more never clears — so the envelope cannot be used to detect the end.",
		},
		map[string]interface{}{
			"title":       "success semantics",
			"provenance":  "code-comment: posts.go:592, posts_search.go:466",
			"description": "A 2xx carrying an explicit {\"success\":false} is a failure. An absent success key is not a failure signal. Endpoints returning the success field: /posts/batch/delete, /posts/batch/move, /posts-search/parsing/stop, and all create/update/delete operations.",
		},
		map[string]interface{}{
			"title":       "Hostile types",
			"provenance":  "code-comment: types.go:513",
			"description": "Declared int on the Go payload struct, returned as string by the server. 12 such fields are declared on ProjectPayload (posts_caption, photos_caption, tg_buttons, videos_title, posts_comment, posts_rewrite, posts_location, posts_location_vk, posts_photo, publish_as_story_source_ids, share_stories_to_feed_source_ids, publish_by_account_source_ids) and 2 on SchedulePayload (publish_as_story_source_ids, share_stories_to_feed_source_ids, types.go:121 and 129) — the two are declared on BOTH. posts_hashtags and posts_links are ProjectPayload fields (types.go:564-565) and are annotated only where a project object is nested, never on a schedule row. Marked x-write-type: integer on each property.",
		},
		map[string]interface{}{
			"title":       "Credential-bearing fields",
			"provenance":  "fixture: testdata/live/accounts.json, accounts_pages.json",
			"description": "GET /accounts returns access_token, access_token_secret, access_web_token, password, bot_token, refresh_token, tw_app_secret, wp_app_password, and more. /accounts/pages returns access_token, access_token_secret. /users/me returns api_token. /proxies returns password. Marked x-sensitive: true. No example values are emitted on these fields.",
		},
		map[string]interface{}{
			"title":       "Known-wrong fields",
			"provenance":  "code-comment: types.go:788, #110, #119",
			"description": "publication_date.timestamp is 10 hours behind the displayed slot while source_timestamp is the real one (#110). posts_by_days is a map of day objects whose empty form arrives as a JSON array (#119).",
		},
	}
}
