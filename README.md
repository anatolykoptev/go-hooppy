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

# Delete a post
hooppy posts delete <post-id>

# Upload media
hooppy files upload-media ./photo.jpg

# List projects and schedules
hooppy projects list
hooppy schedules list

# Create / update / delete projects (undocumented)
hooppy projects create --name "My Project" --page <page-id>
hooppy projects update <project-id> --name "Renamed Project"
hooppy projects delete <project-id>

# Create / update / delete schedules (undocumented)
hooppy schedules create --name "Daily 09:00"
hooppy schedules update <schedule-id> --name "Renamed" --state 0
hooppy schedules delete <schedule-id>

# Update an existing post (undocumented)
hooppy posts update <post-id> --text "Updated text" --to <page-id>

# Create a post via an alternative mode (undocumented)
# Modes: search, copy, sources, import, crosspost, rewrite, translate,
#         queue, drafts, templates, rss, feeds, tags, watermarks, batch
hooppy posts crosspost --mode search --text "Found via search" --to <page-id>
hooppy posts crosspost --mode copy --text "Copied post" --to <page-id>
hooppy posts crosspost --mode rss --text "From RSS feed" --to <page-id>

# Disconnect a page (undocumented)
hooppy pages disconnect <page-id>

# User profile / watermarks / proxies / notifications (undocumented)
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
hooppy search posts --source-resource-id <id>  # list scraped posts from a source
hooppy search posts --source-resource-id <id> --text "search query" --date-from 01.01.2026
hooppy search status                           # check if parsing is in progress
hooppy search parse --source-resource-id <id> --account-id <id>  # start scraping
hooppy search stop                             # stop in-progress scraping
hooppy search copy --post-id <id> --to <page-id>  # copy scraped post to your page

# Print MCP setup instructions
hooppy mcp-config
```

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
| `hooppy_batch_delete_posts` | Delete multiple posts |
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
| `hooppy_copy_search_post` | Copy a scraped post to your own pages (UNDOCUMENTED) |

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

The full OpenAPI 3.0 specification is in [`openapi.yaml`](openapi.yaml) (copied from [hooppy.ru/openapi.yaml](https://hooppy.ru/openapi.yaml)).

Base URL: `https://api.hooppy.ru/api`  
Auth: `Authorization: Bearer <JWT token>`

### Undocumented endpoints

The following endpoints are NOT in the public OpenAPI spec (v0.1.0) but were discovered via live API probing. They may change without notice. Use with caution.

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
| `GET` | `/notifications` | `ListNotifications` | Publication status notifications |
| `POST` | `/posts/schedules` | `CreateSchedule` | 34 required fields; use `NewSchedulePayload(name)` |
| `PUT` | `/posts/schedules/{id}` | `UpdateSchedule` | Same 34 fields |
| `DELETE` | `/posts/schedules/{id}` | `DeleteSchedule` | Returns `{"success":true,"schedules":[...]}` |
| `POST` | `/posts/projects` | `CreateProject` | 56 required fields; use `NewProjectPayload(name, pageID)` |
| `PUT` | `/posts/projects/{id}` | `UpdateProject` | Body: `{"name":"..."}` |
| `DELETE` | `/posts/projects/{id}` | `DeleteProject` | Returns `{"success":true}` |
| `PUT` | `/posts/{id}` | `UpdatePost` | Same payload as `CreatePost` |
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

All cross-posting modes (PUT /posts/{mode}) accept the same payload as `CreatePost` and return `{"id":...}`.

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
