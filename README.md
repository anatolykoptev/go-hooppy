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
        SelectedPagesIDs:    []int{822454, 22543},
        Texts:               []hooppy.PostText{{Text: "Hello from go-hooppy!", SourceID: 0}},
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Post created: ID=%d\n", result.ID)
}
```

## CLI usage

```bash
# List accounts
hooppy accounts list
hooppy accounts list --source 6   # Pinterest only

# List pages (groups)
hooppy pages list --source 1      # VK pages

# Create and publish a post
hooppy posts create --text "Hello world" --to 822454,22543

# List unpublished posts
hooppy posts list --unpublished

# Delete a post
hooppy posts delete 533241

# Upload media
hooppy files upload-media ./photo.jpg

# List projects and schedules
hooppy projects
hooppy schedules

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

## License

Apache 2.0 — same as the Hooppy OpenAPI specification.
