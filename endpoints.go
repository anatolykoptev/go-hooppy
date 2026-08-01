package hooppy

// API endpoint paths. Base URL is https://api.hooppy.ru/api (see DefaultBaseURL).
const (
	pathAccounts         = "/accounts"
	pathPages            = "/accounts/pages"
	pathProjects         = "/posts/projects"
	pathProjectDelete    = "/posts/projects/%d"
	pathSchedules        = "/posts/schedules"
	pathScheduleDelete   = "/posts/schedules/%d"
	pathScheduleEdit     = "/posts/schedules/%d/edit"
	pathUploadMedia      = "/files/media/upload"
	pathUploadDocument   = "/files/documents/upload"
	pathPosts            = "/posts"
	pathPostDelete       = "/posts/%d"
	pathPostsBatchDelete = "/posts/batch/delete"
	// Undocumented: POST /posts/batch/move moves existing posts to another
	// schedule. Not in the public OpenAPI spec (v0.1.0). Discovered via API
	// probing — may change without notice. The posts_ids field is a
	// comma-joined STRING, not a JSON array (a JSON array makes the server
	// throw ErrorException: explode(...) and return 500 — measured live
	// 2026-07-30, issue #105).
	pathPostsBatchMove = "/posts/batch/move"
	// Undocumented: GET /posts/schedules/{id}/posts returns a schedule's
	// queue depth and per-day calendar in one call. Not in the public
	// OpenAPI spec. Discovered via API probing — may change without notice
	// (issue #106).
	pathSchedulePosts = "/posts/schedules/%d/posts"
	// Undocumented endpoints (not in OpenAPI spec v0.1.0).
	// Discovered via API probing — may change without notice.
	pathUser           = "/users/me"
	pathUserSettings   = "/users/settings"
	pathWatermarks     = "/watermarks"
	pathWatermarkByID  = "/watermarks/%d"
	pathProxies        = "/proxies"
	pathProxyByID      = "/proxies/%d"
	pathNotifications  = "/notifications"
	pathPageDisconnect = "/accounts/pages/%d"
	pathPostUpdate     = "/posts/%d"
	pathPostsSearch    = "/posts/search"
	pathPostsSources   = "/posts/sources"
	// Posts search (scraping external pages) — UNDOCUMENTED.
	pathPostsSearchIndex      = "/posts-search"
	pathPostsSearchSources    = "/posts-search/source-resources"
	pathPostsSearchParseForm  = "/posts-search/parsing/form"
	pathPostsSearchParseStart = "/posts-search/parsing/start"
	pathPostsSearchParseStop  = "/posts-search/parsing/stop"
	pathPostsSearchEdit       = "/posts-search/%d/edit"
	pathPostEdit              = "/posts/%d/edit"
	// Cross-posting rule engine (the /cross-posting subsystem, NOT the
	// /posts/{mode} cross-post dispatcher in crosspost.go). UNDOCUMENTED: not
	// in the public OpenAPI spec (v0.1.0). Discovered via API probing + the
	// hooppy.ru Nuxt bundle — may change without notice. The read surface is
	// list / {id}/edit / {id}/statistics; there is no direct GET by id (405,
	// same shape as /posts-search/{id} and /posts/schedules/{id}).
	pathCrossPostings          = "/cross-posting"
	pathCrossPostingEdit       = "/cross-posting/%d/edit"
	pathCrossPostingStatistics = "/cross-posting/%d/statistics"
)

// DefaultBaseURL is the production Hooppy API base URL.
const DefaultBaseURL = "https://api.hooppy.ru/api"
