package hooppy

// API endpoint paths. Base URL is https://api.hooppy.ru/api (see DefaultBaseURL).
const (
	pathAccounts         = "/accounts"
	pathPages            = "/accounts/pages"
	pathProjects         = "/posts/projects"
	pathSchedules        = "/posts/schedules"
	pathUploadMedia      = "/files/media/upload"
	pathUploadDocument   = "/files/documents/upload"
	pathPosts            = "/posts"
	pathPostDelete       = "/posts/%d"
	pathPostsBatchDelete = "/posts/batch/delete"
)

// DefaultBaseURL is the production Hooppy API base URL.
const DefaultBaseURL = "https://api.hooppy.ru/api"
