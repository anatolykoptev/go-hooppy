package hooppy

// API endpoint paths. Base URL is https://api.hooppy.ru/api (see DefaultBaseURL).
const (
	pathAccounts         = "/accounts"
	pathPages            = "/accounts/pages"
	pathProjects         = "/posts/projects"
	pathProjectDelete    = "/posts/projects/%d"
	pathSchedules        = "/posts/schedules"
	pathScheduleDelete   = "/posts/schedules/%d"
	pathUploadMedia      = "/files/media/upload"
	pathUploadDocument   = "/files/documents/upload"
	pathPosts            = "/posts"
	pathPostDelete       = "/posts/%d"
	pathPostsBatchDelete = "/posts/batch/delete"
	// Undocumented endpoints (not in OpenAPI spec v0.1.0).
	// Discovered via API probing — may change without notice.
	pathUser           = "/users/me"
	pathWatermarks     = "/watermarks"
	pathWatermarkByID  = "/watermarks/%d"
	pathProxies        = "/proxies"
	pathProxyByID      = "/proxies/%d"
	pathNotifications  = "/notifications"
	pathPageDisconnect = "/accounts/pages/%d"
	pathPostUpdate     = "/posts/%d"
	pathPostsSearch    = "/posts/search"
	pathPostsCopy      = "/posts/copy"
	pathPostsSources   = "/posts/sources"
	pathPostsImport    = "/posts/import"
)

// DefaultBaseURL is the production Hooppy API base URL.
const DefaultBaseURL = "https://api.hooppy.ru/api"
