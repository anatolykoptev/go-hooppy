# Changelog

## Unreleased


### Added

* **Retry for transient failures (429/5xx)**: opt-in retry for GET and DELETE requests via `Config.RetryOptions` (uses `go-kit/retry`). POST and streaming uploads NEVER retry (non-idempotent). Context is the sole deadline authority. `MaxElapsedTime` defaults to 30s. `APIError.RetryAfter` parsed from the `Retry-After` header (RFC 7231: seconds + HTTP-date).
* **HTTP client configurability**: `Config.HTTPClient` lets callers inject a pre-configured `*http.Client` (custom transport, pool sizing, TLS, proxies). Follows the go-kit `WithHTTPClient` pattern.
* **Schedule CRUD (UNDOCUMENTED)**: `CreateSchedule`, `UpdateSchedule`, `DeleteSchedule` via POST/PUT/DELETE `/posts/schedules[/{id}]`. Discovered via API probing — not in OpenAPI spec v0.1.0. `SchedulePayload` with 34 required fields; `NewSchedulePayload(name)` provides sensible defaults.
* **Project CRUD (UNDOCUMENTED)**: `CreateProject`, `UpdateProject`, `DeleteProject` via POST/PUT/DELETE `/posts/projects[/{id}]`. `ProjectPayload` with 56 required fields; `NewProjectPayload(name, pageID)` provides sensible defaults.
* **Watermarks CRUD (UNDOCUMENTED)**: `ListWatermarks`, `CreateWatermark`, `UpdateWatermark`, `DeleteWatermark` via GET/POST/PUT/DELETE `/watermarks[/{id}]`. `WatermarkPayload` with 6 fields (name, file, space, position, opacity, size).
* **Proxies CRUD (UNDOCUMENTED)**: `ListProxies`, `CreateProxy`, `UpdateProxy`, `DeleteProxy` via GET/POST/PUT/DELETE `/proxies[/{id}]`. `ProxyPayload` with 5 fields (name, ip, port, login, password).
* **User profile (UNDOCUMENTED)**: `GetUser` via GET `/users/me`. Sensitive fields (api_token, ord, passwords) intentionally excluded from the `User` struct.
* **Notifications (UNDOCUMENTED)**: `ListNotifications` via GET `/notifications`.
* **Posts search / scraping (UNDOCUMENTED)**: `ListSearchPosts`, `ListSourceResources`, `GetParsingForm`, `StartParsing`, `StopParsing`, `CopySearchPost`, `RewriteSearchPost`, `GetSearchPostEdit` via GET `/posts-search`, GET `/posts-search/source-resources`, GET `/posts-search/parsing/form`, POST `/posts-search/parsing/start`, DELETE `/posts-search/parsing`, PUT `/posts/copy`, POST `/posts` (with `as_copy=1`), GET `/posts-search/{id}/edit`. Scrape posts from external social media pages (VK, Instagram, etc.) and filter by text, date, source. `CopySearchPost` copies a scraped post as-is; `RewriteSearchPost` lets you override the text and keep original photos via `--keep-photos` (downloads from edit endpoint URLs, re-uploads via `UploadMedia`, passes full MediaItem objects in attachments). `GetSearchPostEdit` returns a scraped post's data in re-publishable format. CLI: `hooppy search sources|posts|status|parse|stop|copy|rewrite`. MCP: `hooppy_list_search_posts`, `hooppy_list_source_resources`, `hooppy_parsing_status`, `hooppy_start_parsing`, `hooppy_stop_parsing`, `hooppy_copy_search_post`, `hooppy_rewrite_search_post`.
* **Post update (UNDOCUMENTED)**: `UpdatePost` via PUT `/posts/{id}`. Accepts the same payload types as `CreatePost`.
* **Page disconnect (UNDOCUMENTED)**: `DisconnectPage` via DELETE `/accounts/pages/{id}`. Idempotent.
* **Cross-posting (UNDOCUMENTED)**: 15 methods for alternative post creation modes via PUT `/posts/{mode}`: `SearchPosts`, `CopyPost`, `SourcesPost`, `ImportPost`, `CrossPost`, `RewritePost`, `TranslatePost`, `QueuePost`, `DraftPost`, `TemplatePost`, `RSSPost`, `FeedPost`, `TagPost`, `WatermarkPost`, `BatchPost`. All accept the same payload as `CreatePost` and return `{"id":...}`.
* **MCP tools**: 23 new tools (total 36): `hooppy_create_project`, `hooppy_update_project`, `hooppy_create_schedule`, `hooppy_update_schedule`, `hooppy_update_post`, `hooppy_get_user`, `hooppy_list_watermarks`, `hooppy_create_watermark`, `hooppy_update_watermark`, `hooppy_delete_watermark`, `hooppy_list_proxies`, `hooppy_create_proxy`, `hooppy_update_proxy`, `hooppy_delete_proxy`, `hooppy_list_notifications`, `hooppy_disconnect_page`, `hooppy_list_search_posts`, `hooppy_list_source_resources`, `hooppy_parsing_status`, `hooppy_start_parsing`, `hooppy_stop_parsing`, `hooppy_copy_search_post`, `hooppy_rewrite_search_post`.
* **CLI commands**: 23 new subcommands covering all new endpoints: `user`, `watermarks {list,create,update,delete}`, `proxies {list,create,update,delete}`, `notifications`, `projects {create,update,delete}`, `schedules {create,update,delete}`, `pages disconnect`, `posts update`, `posts crosspost` (all 15 cross-posting modes via `--mode`), `search {sources,posts,status,parse,stop,copy,rewrite}`.
* **CrossPostWithMode**: generic dispatcher method for cross-posting — accepts `CrossPostMode` enum + any post payload. The mode-specific methods (`SearchPosts`, `CopyPost`, etc.) are thin wrappers around it.

### Changed

* `APIError` struct gains a `RetryAfter time.Duration` field (additive, zero value = no behavior change).
* `Schedule` struct expanded with `UserID`, `Position`, `State`, `StopDate`, `StartDate`, `IsDeleted`, `PublicationHowType`, `PublicationWhereType` fields (additive, omitempty).
* `ScheduleResponse` includes `Success` bool field (returned by DELETE).

### Fixed

* **TOCTOU in `openFileForUpload`** (#40): file is now opened BEFORE the stat/size check (was stat-then-open, allowing symlink swap between calls).
* **Goroutine leak in `doMultipartStream`** (#41): `pr.Close()` is now called on `NewRequestWithContext` failure to unblock the writer goroutine (was leaking on the request-build error path).
## 1.0.0 (2026-07-28)


### Added

* Go client library, CLI, and MCP server for Hooppy API ([03a6dc8](https://github.com/anatolykoptev/go-hooppy/commit/03a6dc89218c55a37c976eced33e0017bfa3f488))
