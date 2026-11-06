# go-hooppy

Go client library, CLI, and MCP server for the [Hooppy](https://hooppy.ru) social media auto-posting API.

Hooppy publishes posts to 20+ social networks (VK, OK, Telegram, Instagram, Twitter/X, TikTok, YouTube, LinkedIn, Dzen, Max, Threads, and more) from a single API. This package provides:

- **Client library** — pure Go API client with bearer JWT auth, zero heavy deps
- **CLI** — `hooppy` command for scripts and manual use
- **MCP server** — `hooppy-mcp` for LLM agents (Claude, etc.) to publish via Model Context Protocol

## Install

```bash
go get github.com/anatolykoptev/go-hooppy@latest
```

Or install the CLI tools:

```bash
go install github.com/anatolykoptev/go-hooppy/cmd/hooppy@latest
go install github.com/anatolykoptev/go-hooppy/cmd/hooppy-mcp@latest
```

## Authentication

Get your API token from **Hooppy → Settings → API-token** (`https://hooppy.ru/settings/api`).

Set it via environment variable:

```bash
export HOOPPY_TOKEN="eyJ0eXAiOiJKV1Qi..."
```

Or save to a file (the client checks both):

```bash
mkdir -p ~/.config/hooppy
echo -n "eyJ0eXAiOiJKV1Qi..." > ~/.config/hooppy/token
chmod 600 ~/.config/hooppy/token
```

## Client library usage

```go
package main

import (
    "context"
    "fmt"
    "log"
    
    "github.com/anatolykoptev/go-hooppy"
)

func main() {
    client, err := hooppy.NewClientFromEnv()
    if err != nil {
        log.Fatal(err)
    }

    // List connected accounts
    resp, err := client.ListAccounts(context.Background(), hooppy.ListAccountsFilter{})
    if err != nil {
        log.Fatal(err)
    }
    for _, acc := range resp.List {
        fmt.Printf("ID=%d  %s (source=%d)\n", acc.ID, acc.SocialAccountName, acc.SourceID)
    }

    // Publish a post immediately to selected pages
    result, err := client.CreatePost(context.Background(), hooppy.PostPublishNowPayload{
        PublicationWhenType: 1,
        PublicationHowType:  1,
        SelectedPagesIDs:    []int{123456, 789012}, // your page IDs from hooppy pages list
        Texts:               []hooppy.PostText{{Text: "Hello from go-hooppy!", SourceID: 0}},
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Post created: ID=%d\n", result.ID)
}
```

### Retry and HTTP client configuration

By default, the client does NOT retry failed requests. Enable retry for transient failures (429/5xx) on safe methods (GET, DELETE) via `Config.RetryOptions`:

```go
client, err := hooppy.NewClient(hooppy.Config{
    Token: "your-jwt-token",
    RetryOptions: &retry.Options{
        MaxAttempts:  3,                   // default: 3
        InitialDelay: 500 * time.Millisecond, // default: 500ms
        MaxDelay:     5 * time.Second,     // default: 5s
        // MaxElapsedTime defaults to 30s if unset.
        // OnRetry: func(attempt int, err error) { /* observe retries */ },
    },
})
```

Retry behavior:
- **GET and DELETE** are retried on 429/500/502/503/504 with exponential backoff.
- **POST and streaming uploads** (CreatePost, BatchDeletePosts, UploadMedia, UploadDocument) NEVER retry — they are non-idempotent.
- The `Retry-After` header (RFC 7231: seconds or HTTP-date) overrides the backoff delay.
- **Context is the sole deadline authority** — retry stops when the context is cancelled.
- `APIError.RetryAfter` exposes the parsed header value (0 if absent).

To customize the HTTP transport (connection pool, TLS, proxies), inject your own `*http.Client`:

```go
client, err := hooppy.NewClient(hooppy.Config{
    Token:      "your-jwt-token",
    HTTPClient: &http.Client{Transport: &http.Transport{
        MaxIdleConnsPerHost: 30, // tune for high-concurrency (e.g. MCP server)
        // ... other transport fields
    }},
})
```

## CLI usage

```bash
# List accounts
hooppy accounts list
hooppy accounts list --source 6   # Pinterest only

# List pages (groups)
hooppy pages list --source 1      # VK pages

# Create and publish a post
hooppy posts create --text "Hello world" --to <page-id>,<page-id>

# List unpublished posts
hooppy posts list --unpublished

# Delete a post, or several (--ids: positive ids, none repeated). Exit 0 = all
# deleted, 1 = error, 2 = partial (some deleted, some failed; stdout lists both)
hooppy posts delete <post-id>
hooppy posts delete --ids 101,102,103

# A post's full editable state (texts, attachments, schedule, page selection)
hooppy posts edit <post-id>

# Move posts to another schedule (only schedule-driven posts; one that is not
# refuses the whole batch before anything moves)
hooppy posts move <post-id> --to-schedule <schedule-id>
hooppy posts move --ids 101,102 --to-schedule <schedule-id>

# Where is a post published? Per page of one network (default 1 = VK):
# the page, the public URL of the published copy, scheduled-by-network marker, last errors
hooppy posts where <post-id> [--source 1]

# Upload media
hooppy files upload-media ./photo.jpg

# List projects and schedules
hooppy projects list
hooppy schedules list

# Create / update / delete projects
hooppy projects create --name "My Project" --page <page-id>
hooppy projects update <project-id> --name "Renamed Project"
hooppy projects delete <project-id>

# Create / update / delete schedules
hooppy schedules create --name "Daily 09:00"
hooppy schedules show <schedule-id>            # state, is_posts_repeated, delete_posts_day/hour as JSON
hooppy schedules update <schedule-id> --name "Renamed" --state 0
hooppy schedules update <schedule-id> --delete-after-days 30 [--delete-after-hours 9]  # auto-delete posts N days after publication
hooppy schedules list --repeated               # only schedules with is_posts_repeated=1 (reads each schedule's /edit state)
hooppy schedules delete <schedule-id>

# Update an existing post
hooppy posts update <post-id> --text "Updated text" --to <page-id>

# Create a post via an alternative mode (undocumented)
# Modes: search, copy, sources, import, crosspost, rewrite, translate,
#         queue, drafts, templates, rss, feeds, tags, watermarks, batch
hooppy posts crosspost --mode search --text "Found via search" --to <page-id>
hooppy posts crosspost --mode copy --text "Copied post" --to <page-id>
hooppy posts crosspost --mode rss --text "From RSS feed" --to <page-id>

# Disconnect a page (undocumented)
hooppy pages disconnect <page-id>

# User profile / watermarks / proxies (undocumented) and notifications
hooppy user
hooppy watermarks list
hooppy watermarks create --name "WM1" --file /path/to/wm.png --opacity 50
hooppy watermarks update <id> --name "Renamed" --opacity 80
hooppy watermarks delete <id>
hooppy proxies list
hooppy proxies create --ip 1.2.3.4 --port 8080 --login user --password pass
hooppy proxies update <id> --name "Renamed proxy" --ip 5.6.7.8
hooppy proxies delete <id>
hooppy notifications

# Search and scrape posts from external social media pages (UNDOCUMENTED)
hooppy search sources                          # list configured source resources
hooppy search posts --source-type 1  # list scraped posts (1=social, 2=RSS)
hooppy search posts --source-type 1 --text "search query" --date-from 01.01.2026
hooppy search status                           # check if parsing is in progress
hooppy search parse --source-resource-id <id> --account-id <id>  # start scraping
hooppy search stop                             # stop in-progress scraping
hooppy search copy --post-id <id> --to <page-id>  # DEPRECATED: alias for 'search import' (resolve+publish); use 'search import' instead
hooppy search rewrite --post-id <id> --text "..." --to <page-id>  # rewrite with custom text (attachments preserved)
hooppy search rewrite --post-id <id> --text "..." --to <page-id> --no-attachments  # rewrite, strip attachments
hooppy search rewrite --post-ids "id1,id2,id3" --to <page-id>  # batch rewrite (each post resolved+published independently; attachments preserved per post; no text override in batch)
hooppy search import --post-id <id> --schedules <sched-id>  # copy a scraped post (resolve+publish: text + photos/videos preserved from the resolve step)
hooppy search import --post-ids "id1,id2,id3" --schedules <sched-id>  # batch import (keeps each post's original text; each post resolved+published independently)

# Diagnose broken connections (read-only). GET /accounts reports status: 1
# even when an account's OAuth token is dead — the notification log is the
# only endpoint that reports publication failures. doctor walks it, groups
# errors by (page, error message), and classifies them so you can tell at a
# glance which connections need a human to reconnect them.
hooppy doctor                              # last 7 days, exit 1 if any error in window
hooppy doctor --since 30                   # widen the window to 30 days
hooppy doctor --exit-code=false            # print the report without the non-zero exit

# Print MCP setup instructions
hooppy mcp-config
```

### `hooppy doctor`

`doctor` is a read-only diagnostic that answers one question: **which connections are broken right now?** It exists because `GET /accounts` reports `status: 1` for an account whose OAuth token is dead — measured on a live account where five networks had been failing every publish for weeks while every account still read `status: 1`. The notification log (`GET /notifications`) is the only endpoint that reports publication failures, and `doctor` makes it legible.

It walks the full notification log, filters to error rows whose `operation_date` falls inside the `--since` window (default 7 days), groups them by (page, error message), and prints a JSON report on stdout. Each group carries the page id, page name, network, the vendor's error string verbatim, an occurrence count, the first and last `operation_date` in the window, and a classification:

- `expired_credential` — the vendor's text asks the user to reconnect the account. The tool cannot fix this; it must be named loudly.
- `missing_media` — a text-only post routed to a network that requires an image. A targeting mistake in a schedule; user-fixable.
- `upstream_error` — a 5xx from the social network. Transient; must not carry the same weight as the first two.
- `unknown` — anything unmatched, carrying the raw vendor string. Never forced into a known bucket.

A row whose `operation_date` fails to parse is reported in `unparseable_rows` — never silently dropped, since dropping a row hides exactly the failure the command exists to surface. Unparseable-date rows are reported **regardless of `--since`** — they cannot be dated, so the window check cannot be applied to them. One permanently malformed historical row therefore wedges exit 1 until the row ages out of the log; narrowing `--since` cannot clear it.

**Exit code:** `--exit-code` (default `true`) — exit 1 if any error signal is present: grouped errors inside the `--since` window, unparseable-date rows (reported regardless of `--since` because they cannot be dated), or a truncated walk (`walk_incomplete`). Exit 0 otherwise. This lets `doctor` run in cron or as a pre-flight before a bulk import. Pass `--exit-code=false` to get the report without the non-zero exit.

**`--since` semantics:** `0` means "no window" (every dated row is included); a negative value is rejected with an error (never silently clamped — clamping a bad value into the quietest configuration hides exactly the failures the command exists to surface).

## MCP server setup

### Claude Code (stdio)

```bash
claude mcp add hooppy --transport stdio -- hooppy-mcp
```

### Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "hooppy": {
      "command": "hooppy-mcp",
      "args": ["--stdio"]
    }
  }
}
```

### HTTP mode (for remote agents)

```bash
hooppy-mcp   # starts on :8080 with /mcp endpoint
```

### Available MCP tools

| Tool | Description |
|------|-------------|
| `hooppy_list_accounts` | List connected social network accounts |
| `hooppy_list_pages` | List groups/pages for publishing |
| `hooppy_list_posts` | List posts with filters (status, date, source, etc.) |
| `hooppy_create_post` | Create and publish a post (immediate, scheduled, by project, or by schedule) |
| `hooppy_delete_post` | Delete a single post |
| `hooppy_get_post_edit` | A post's full editable state: texts, attachments, schedule_id, project_id, publication type and date, page selection |
| `hooppy_post_where` | Per page of one network (default VK): where a post is published, with the public URL of each published copy |
| `hooppy_batch_delete_posts` | Delete multiple posts; reports every id (`deleted` / `failed`) |
| `hooppy_upload_media` | Upload a photo or video file |
| `hooppy_upload_document` | Upload a document file |
| `hooppy_list_projects` | List post projects |
| `hooppy_list_schedules` | List publication schedules |
| `hooppy_create_project` | Create a post project |
| `hooppy_update_project` | Update a project name |
| `hooppy_delete_project` | Delete a project |
| `hooppy_create_schedule` | Create a publication schedule |
| `hooppy_update_schedule` | Update a publication schedule |
| `hooppy_delete_schedule` | Delete a publication schedule |
| `hooppy_update_post` | Update an existing post |
| `hooppy_get_user` | Get current user profile |
| `hooppy_list_watermarks` | List watermarks |
| `hooppy_create_watermark` | Create a watermark |
| `hooppy_update_watermark` | Update a watermark |
| `hooppy_delete_watermark` | Delete a watermark |
| `hooppy_list_proxies` | List proxy servers |
| `hooppy_create_proxy` | Create a proxy server |
| `hooppy_update_proxy` | Update a proxy server |
| `hooppy_delete_proxy` | Delete a proxy server |
| `hooppy_list_notifications` | List publication notifications |
| `hooppy_disconnect_page` | Disconnect a social media page |
| `hooppy_list_search_posts` | List posts scraped from external pages (UNDOCUMENTED) |
| `hooppy_list_source_resources` | List configured source resources to scrape from (UNDOCUMENTED) |
| `hooppy_parsing_status` | Check scraping status + available parsers (UNDOCUMENTED) |
| `hooppy_start_parsing` | Start scraping posts from an external source (UNDOCUMENTED) |
| `hooppy_stop_parsing` | Stop in-progress scraping job (UNDOCUMENTED) |
| `hooppy_copy_search_post` | Deprecated: copy a scraped post to your pages (now delegates to resolve+publish; use `hooppy_import_search_post` instead) (UNDOCUMENTED) |
| `hooppy_rewrite_search_post` | Rewrite one or more scraped posts with custom text (single) or batch keeping original text (UNDOCUMENTED) |
| `hooppy_import_search_post` | Import one or more scraped posts (single or batch; keeps original text + attachments) (UNDOCUMENTED) |

> **Note — `hooppy_copy_search_post` is deprecated.** The historical `CopySearchPost` (PUT /posts/copy) created empty posts; the restored shim now delegates to resolve+publish (the same path `hooppy_import_search_post` uses), preserving text + attachments. Use `hooppy_import_search_post` for the non-deprecated name, or `hooppy_rewrite_search_post` for a text override. Batch import is fully exposed via MCP (`hooppy_import_search_post` with `search_post_ids`).

> **Id lists accept an int array or a comma string.** `search_post_ids`, `selected_pages_ids`, `schedules_ids` and the `ids` of `hooppy_batch_delete_posts` / `hooppy_batch_move_posts` take `[10, 11]` or `"10,11"` — both reach the API as the same list (the tools used to take only the comma string; old agent calls keep working). Examples:
>
> ```json
> {"search_post_ids": [2001, 2002], "schedules_ids": [10, 11]}
> {"search_post_ids": "2001,2002", "schedules_ids": "10,11"}
> ```
>
> `publication_when_type` defaults to 3 (by schedule) when omitted, so `selected_pages_ids` needs an explicit `publication_when_type` of 1 or 2. `publication_how_type` is derived from the target (1 for direct pages, per the API reference); an explicit 2 (a page set) needs a `project_id` these tools do not send and is refused under `publication_when_type` 1 or 2.

## The ops layer (for contributors)

The CLI (`cmd/hooppy`) and the MCP server (`cmd/hooppy-mcp`) share one
operation layer in `ops/`. A capability belongs there — not duplicated per
surface — so both surfaces get it from a single change:

1. `ops/<op>.go` — one exported function per operation
   (`ops.UpdateSchedule(ctx, client, id, in)`), owning all validation,
   presence semantics and request building. Optional input fields are
   pointers: `nil` = not given, pointed-at zero = a real value.
2. The input struct is the single source of the parameter set. Field tags:
   `json` (wire/MCP property), `jsonschema` (tool description),
   `flag` + `flagusage` (CLI flag). `flag:"-"` opts a field out of the CLI.
3. `ops.Registry` — one entry per migrated op (`CLIPath`, `MCPTool`, an
   optional single-id `MCPSingle` tool, `Input` type). The CLI command binds its flags from the Input type via
   `RegisterOpFlags`/`FillOpInput`; the MCP tool uses the Input type as its
   `AddTool` schema. Both register **from** the registry entry.
4. Parity tests (`TestOpsRegistry_*` in both `cmd/` packages) derive from
   the registry and the real registrations: an entry missing on either
   surface, or a field without a `flag` tag / schema property, fails the
   gate. The `*BacklogReport` tests log how many commands/tools are not yet
   migrated — adding one to the registry shrinks the count.

Piloted on `schedules update` / `hooppy_update_schedule`; search copy/rewrite/import,
`posts where`, `posts delete`, `posts move` and `posts edit` have followed. Everything
else still lives per-surface and is migrated one op at a time.

## Social network source IDs

| ID | Network | ID | Network |
|----|---------|----|---------| 
| 1 | VKontakte | 14 | TikTok |
| 2 | Odnoklassniki | 17 | YouTube |
| 3 | Facebook | 18 | LinkedIn |
| 4 | Twitter/X | 22 | WhatsApp |
| 5 | My World | 23 | Rutube |
| 6 | Pinterest | 29 | Instagram |
| 8 | Tumblr | 32 | Yappy |
| 9 | Telegram (channels) | 33 | Max |
| 10 | Instagram FB | 34 | Threads |
| 11 | Telegram (accounts) | 35 | VK Chats |
| 13 | Dzen | | |

Use `hooppy.SourceVK`, `hooppy.SourcePinterest`, etc. in Go code.

## API reference

The OpenAPI 3.0 specification in [`openapi.yaml`](openapi.yaml) is generated from Hooppy's official API reference ([hooppy.ru/api-docs](https://hooppy.ru/api-docs)); the chunk URL and sha256 it was built from are in the file header. Rebuild it with one command (stdlib Python, no execution of the site's JS):

```bash
python3 -I scripts/fetch_api_docs.py          # rewrite openapi.yaml
python3 -I scripts/fetch_api_docs.py --check  # exit 1 if upstream docs changed
```

Base URL: `https://api.hooppy.ru/api`  
Auth: `Authorization: Bearer <JWT token>`

### API coverage

Which documented operation has a client method, a CLI command and an MCP tool. The table is derived, never typed: operations from `openapi.yaml`, the client column from the path argument of each request call in the Go sources, the CLI and MCP columns from the real `RegisterSubcommand` / `AddTool` registrations followed through to the client methods they call (`internal/apicov`). `—` means not implemented. A test fails when the table is stale; regenerate with `go test -run TestCoverageTable -update-readme .`.

<!-- apicov:begin (generated by `go test -run TestCoverageTable -update-readme .`; do not edit) -->

82 documented operations, 25 with a client method.

| Endpoint | Client method | CLI command | MCP tool |
|---|---|---|---|
| `GET /accounts/pages` | `ListPages` | `hooppy doctor`, `hooppy pages list` | `hooppy_list_pages` |
| `GET /ai-posting` | — | — | — |
| `POST /ai-posting` | — | — | — |
| `DELETE /ai-posting/{id}` | — | — | — |
| `PUT /ai-posting/{id}` | — | — | — |
| `PUT /ai-posting/{id}/check` | — | — | — |
| `GET /ai-posting/{id}/edit` | — | — | — |
| `PUT /ai-posting/{id}/state` | — | — | — |
| `GET /cross-posting` | `ListCrossPostings` | `hooppy crossposting list` | `hooppy_list_cross_postings` |
| `POST /cross-posting` | — | — | — |
| `POST /cross-posting/batch/delete` | — | — | — |
| `POST /cross-posting/batch/start` | — | — | — |
| `POST /cross-posting/batch/stop` | — | — | — |
| `GET /cross-posting/statistics/fast` | — | — | — |
| `GET /cross-posting/statistics/full` | — | — | — |
| `DELETE /cross-posting/{id}` | — | — | — |
| `PUT /cross-posting/{id}` | — | — | — |
| `PUT /cross-posting/{id}/check` | — | — | — |
| `GET /cross-posting/{id}/edit` | `GetCrossPostingEdit` | `hooppy crossposting show` | `hooppy_get_cross_posting_edit` |
| `GET /cross-posting/{id}/errors_logs` | — | — | — |
| `GET /cross-posting/{id}/filters_logs` | — | — | — |
| `GET /cross-posting/{id}/pagination` | — | — | — |
| `PUT /cross-posting/{id}/reset` | — | — | — |
| `PUT /cross-posting/{id}/state` | — | — | — |
| `GET /cross-posting/{id}/statistics` | `GetCrossPostingStatistics` | `hooppy crossposting stats` | `hooppy_get_cross_posting_statistics` |
| `POST /files/documents/upload` | `UploadDocument` | `hooppy files upload-document` | `hooppy_upload_document` |
| `POST /files/media/upload` | `UploadMedia` | `hooppy files upload-media` | `hooppy_upload_media` |
| `POST /files/media/upload_from_url` | — | — | — |
| `PUT /files/photos/crop` | — | — | — |
| `GET /items/social/pages` | `GetPostPublishedPages` | `hooppy posts where` | `hooppy_post_where` |
| `GET /notifications` | `ListNotifications` | `hooppy doctor`, `hooppy notifications` | `hooppy_list_notifications` |
| `PUT /notifications/{id}/is_viewed` | — | — | — |
| `GET /posts` | `ListPosts` | `hooppy posts list`, `hooppy search copy`, `hooppy search import`, `hooppy search rewrite` | `hooppy_copy_search_post`, `hooppy_import_search_post`, `hooppy_list_posts`, `hooppy_rewrite_search_post` |
| `POST /posts` | `CreatePost`, `PublishPost` | `hooppy posts create`, `hooppy search copy`, `hooppy search import`, `hooppy search rewrite` | `hooppy_copy_search_post`, `hooppy_create_post`, `hooppy_import_search_post`, `hooppy_rewrite_search_post` |
| `POST /posts/batch/delete` | `BatchDeletePosts` | `hooppy posts delete` | `hooppy_batch_delete_posts`, `hooppy_delete_post` |
| `POST /posts/batch/move` | `BatchMovePosts` | `hooppy posts move` | `hooppy_batch_move_posts`, `hooppy_move_post` |
| `POST /posts/batch_uploading` | — | — | — |
| `GET /posts/projects` | `ListProjects` | `hooppy projects list` | `hooppy_list_projects` |
| `POST /posts/projects` | `CreateProject` | `hooppy projects create` | `hooppy_create_project` |
| `POST /posts/projects/batch/delete` | — | — | — |
| `GET /posts/projects/json` | — | — | — |
| `PUT /posts/projects/sort` | — | — | — |
| `DELETE /posts/projects/{id}` | `DeleteProject` | `hooppy projects delete` | `hooppy_delete_project` |
| `PUT /posts/projects/{id}` | `UpdateProject` | `hooppy projects update` | `hooppy_update_project` |
| `GET /posts/projects/{id}/edit` | — | — | — |
| `GET /posts/schedules` | `ListSchedules` | `hooppy schedules list` | `hooppy_list_schedules` |
| `POST /posts/schedules` | `CreateSchedule` | `hooppy schedules create` | `hooppy_create_schedule` |
| `POST /posts/schedules/batch/clear` | — | — | — |
| `POST /posts/schedules/batch/copy` | — | — | — |
| `POST /posts/schedules/batch/delete` | — | — | — |
| `POST /posts/schedules/batch/shuffle` | — | — | — |
| `POST /posts/schedules/batch/start` | — | — | — |
| `POST /posts/schedules/batch/stop` | — | — | — |
| `GET /posts/schedules/json` | — | — | — |
| `PUT /posts/schedules/sort` | — | — | — |
| `DELETE /posts/schedules/{id}` | `DeleteSchedule` | `hooppy schedules delete` | `hooppy_delete_schedule` |
| `PUT /posts/schedules/{id}` | `UpdateSchedule`, `UpdateScheduleFromEdit` | `hooppy schedules update` | `hooppy_update_schedule` |
| `GET /posts/schedules/{id}/ads/times` | — | — | — |
| `PUT /posts/schedules/{id}/ads/times` | — | — | — |
| `PUT /posts/schedules/{id}/clear` | — | — | — |
| `GET /posts/schedules/{id}/edit` | `GetScheduleEdit`, `UpdateScheduleFromEdit` | `hooppy schedules show`, `hooppy schedules times`, `hooppy schedules update` | `hooppy_get_schedule_edit`, `hooppy_update_schedule` |
| `GET /posts/schedules/{id}/posts` | `ListSchedulePosts` | `hooppy schedules queue` | `hooppy_list_schedule_posts` |
| `POST /posts/schedules/{id}/posts/replace` | — | — | — |
| `PUT /posts/schedules/{id}/shuffle` | — | — | — |
| `GET /posts/social` | — | — | — |
| `GET /posts/statistics/fast` | — | — | — |
| `GET /posts/statistics/full` | — | — | — |
| `DELETE /posts/{id}` | `DeletePost` | `hooppy posts delete` | `hooppy_batch_delete_posts`, `hooppy_delete_post` |
| `PUT /posts/{id}` | `UpdatePost` | `hooppy posts update` | `hooppy_update_post`, `hooppy_update_post_text` |
| `GET /posts/{id}/edit` | `GetPostEdit` | `hooppy posts edit`, `hooppy posts move`, `hooppy posts update` | `hooppy_batch_move_posts`, `hooppy_get_post_edit`, `hooppy_move_post`, `hooppy_update_post_text` |
| `GET /rss-import` | — | — | — |
| `POST /rss-import` | — | — | — |
| `POST /rss-import/batch/delete` | — | — | — |
| `POST /rss-import/batch/start` | — | — | — |
| `POST /rss-import/batch/stop` | — | — | — |
| `DELETE /rss-import/{id}` | — | — | — |
| `PUT /rss-import/{id}` | — | — | — |
| `PUT /rss-import/{id}/check` | — | — | — |
| `GET /rss-import/{id}/edit` | — | — | — |
| `GET /rss-import/{id}/logs` | — | — | — |
| `PUT /rss-import/{id}/state` | — | — | — |
| `GET /rss-import/{id}/statistics` | — | — | — |

<!-- apicov:end -->

### Undocumented endpoints

The following endpoints are NOT in the official API reference (checked against the spec generated 2026-11-03; see the coverage table above for the documented ones). They were discovered via live API probing and may change without notice. Use with caution.

| Method | Endpoint | Go method | Notes |
|---|---|---|---|
| `GET` | `/users/me` | `GetUser` | Current user profile (sensitive fields excluded) |
| `GET` | `/watermarks` | `ListWatermarks` | Paginated |
| `POST` | `/watermarks` | `CreateWatermark` | 6 fields: name, file, space, position, opacity, size |
| `PUT` | `/watermarks/{id}` | `UpdateWatermark` | Same 6 fields |
| `DELETE` | `/watermarks/{id}` | `DeleteWatermark` | Returns `{"success":true}` |
| `GET` | `/proxies` | `ListProxies` | All proxies |
| `POST` | `/proxies` | `CreateProxy` | 5 fields: name, ip, port, login, password |
| `PUT` | `/proxies/{id}` | `UpdateProxy` | Same 5 fields |
| `DELETE` | `/proxies/{id}` | `DeleteProxy` | Returns `{"success":true}` |
| `DELETE` | `/accounts/pages/{id}` | `DisconnectPage` | Idempotent |
| `PUT` | `/posts/search` | `SearchPosts` | Cross-posting mode |
| `PUT` | `/posts/copy` | `CopyPost` | Cross-posting mode |
| `PUT` | `/posts/sources` | `SourcesPost` | Cross-posting mode |
| `PUT` | `/posts/import` | `ImportPost` | Cross-posting mode |
| `PUT` | `/posts/crosspost` | `CrossPost` | Cross-posting mode |
| `PUT` | `/posts/rewrite` | `RewritePost` | Cross-posting mode |
| `PUT` | `/posts/translate` | `TranslatePost` | Cross-posting mode |
| `PUT` | `/posts/queue` | `QueuePost` | Cross-posting mode |
| `PUT` | `/posts/drafts` | `DraftPost` | Cross-posting mode |
| `PUT` | `/posts/templates` | `TemplatePost` | Cross-posting mode |
| `PUT` | `/posts/rss` | `RSSPost` | Cross-posting mode |
| `PUT` | `/posts/feeds` | `FeedPost` | Cross-posting mode |
| `PUT` | `/posts/tags` | `TagPost` | Cross-posting mode |
| `PUT` | `/posts/watermarks` | `WatermarkPost` | Cross-posting mode |
| `PUT` | `/posts/batch` | `BatchPost` | Cross-posting mode |

`NewSchedulePayload` / `NewProjectPayload` (create and update of schedules and projects) are documented operations; only the field sets were measured live. All cross-posting modes (PUT /posts/{mode}) accept the same payload as `CreatePost` and return `{"id":...}`.

```go
// Create a schedule with defaults
payload := hooppy.NewSchedulePayload("My Daily Schedule")
payload.PublishAsStory = 1   // enable stories
payload.IsCommentsDisabled = 1
resp, err := client.CreateSchedule(context.Background(), payload)

// Create a project
payload := hooppy.NewProjectPayload("My Project", pageID)
resp, err := client.CreateProject(context.Background(), payload)

// Cross-post via search mode
postPayload := hooppy.PostPublishNowPayload{
    PublicationWhenType: 1,
    PublicationHowType:  1,
    SelectedPagesIDs:    []int{pageID},
    Texts:               []hooppy.PostText{{Text: "hello", SourceID: 0}},
    Attachments:         []hooppy.Attachment{},
}
resp, err := client.SearchPosts(context.Background(), postPayload)
```

## License

Apache 2.0 — same as the Hooppy OpenAPI specification.
