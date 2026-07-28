package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/anatolykoptev/go-hooppy"
	mcpserver "github.com/anatolykoptev/go-mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	impl := &mcp.Implementation{
		Name:    "hooppy-mcp",
		Version: version,
	}
	cfg := mcpserver.Config{
		Name:    "hooppy-mcp",
		Version: version,
	}

	err := mcpserver.Serve(impl, cfg, func(server *mcp.Server) {
		registerTools(server)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "hooppy-mcp: %v\n", err)
		os.Exit(1)
	}
}

func registerTools(server *mcp.Server) {
	registerListAccounts(server)
	registerListPages(server)
	registerListPosts(server)
	registerCreatePost(server)
	registerDeletePost(server)
	registerBatchDeletePosts(server)
	registerUploadMedia(server)
	registerUploadDocument(server)
	registerListProjects(server)
	registerListSchedules(server)
}

// --- helpers ---

func client() (*hooppy.Client, error) {
	return hooppy.NewClientFromEnv()
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}, nil
}

func errResult(msg string) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}, nil
}

// --- list_accounts ---

type listAccountsInput struct {
	SourceID int `json:"source_id,omitempty" jsonschema:"Filter by social network source ID (e.g. 1=VK, 6=Pinterest, 9=Telegram channels). 0=no filter."`
}

func registerListAccounts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_accounts",
			Description: "List connected social network accounts on Hooppy. Returns account IDs, social network type (source_id), and profile info.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listAccountsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListAccounts(ctx, hooppy.ListAccountsFilter{SourceID: in.SourceID})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_pages ---

type listPagesInput struct {
	SourceID  int `json:"source_id,omitempty" jsonschema:"Filter by social network source ID. 0=no filter."`
	AccountID int `json:"account_id,omitempty" jsonschema:"Filter by parent account ID. 0=no filter."`
}

func registerListPages(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_pages",
			Description: "List connected groups/pages for social network accounts. Page IDs are needed for create_post (selected_pages_ids).",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listPagesInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListPages(ctx, hooppy.ListPagesFilter{SourceID: in.SourceID, AccountID: in.AccountID})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_posts ---

type listPostsInput struct {
	IsPublished     *bool  `json:"is_published,omitempty" jsonschema:"Filter by publication status. true=published, false=unpublished, omit=no filter."`
	PublicationDate string `json:"publication_date,omitempty" jsonschema:"Filter by date in dd.mm.yyyy format."`
	SourceID        int    `json:"source_id,omitempty" jsonschema:"Filter by social network source ID."`
	AccountID       int    `json:"account_id,omitempty" jsonschema:"Filter by account ID."`
	PageID          int    `json:"page_id,omitempty" jsonschema:"Filter by group/page ID."`
	ScheduleID      int    `json:"schedule_id,omitempty" jsonschema:"Filter by schedule ID."`
	ProjectID       int    `json:"project_id,omitempty" jsonschema:"Filter by project ID."`
}

func registerListPosts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_posts",
			Description: "List posts on Hooppy with optional filters by status, date, social network, account, page, schedule, or project.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listPostsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListPosts(ctx, hooppy.ListPostsFilter{
				IsPublished:     in.IsPublished,
				PublicationDate: in.PublicationDate,
				SourceID:        in.SourceID,
				AccountID:       in.AccountID,
				PageID:          in.PageID,
				ScheduleID:      in.ScheduleID,
				ProjectID:       in.ProjectID,
			})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- create_post ---

type createPostInput struct {
	Text             string `json:"text" jsonschema:"Post text. Published to all selected pages with source_id=0 (shared)."`
	PageIDs          []int  `json:"page_ids" jsonschema:"Page IDs from hooppy_list_pages to publish to."`
	PublishAtDate    string `json:"publish_at_date,omitempty" jsonschema:"Scheduled date in dd.mm.yyyy format. If set with publish_at_hours/minutes, post is scheduled instead of published immediately."`
	PublishAtHours   string `json:"publish_at_hours,omitempty" jsonschema:"Scheduled hour (HH). Use with publish_at_date."`
	PublishAtMinutes string `json:"publish_at_minutes,omitempty" jsonschema:"Scheduled minutes (MM). Use with publish_at_date."`
	ScheduleIDs      []int  `json:"schedule_ids,omitempty" jsonschema:"Schedule IDs for publishing via schedule. If set, page_ids and publish_at_* are ignored."`
	ProjectID        int    `json:"project_id,omitempty" jsonschema:"Project ID for publishing via project. If set, page_ids and schedule_ids are ignored."`
}

func registerCreatePost(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_create_post",
			Description: "Create and publish a post to social networks via Hooppy. Supports immediate publish, scheduled publish (date+time), schedule-based, or project-based. Use hooppy_list_pages first to get page_ids.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createPostInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			texts := []hooppy.PostText{{Text: in.Text, SourceID: 0}}

			var payload interface{}
			switch {
			case in.ProjectID > 0:
				payload = hooppy.PostPublishByProjectPayload{
					PublicationWhenType: 3,
					PublicationHowType:  1,
					ProjectID:           in.ProjectID,
					Texts:               texts,
				}
			case len(in.ScheduleIDs) > 0:
				payload = hooppy.PostPublishBySchedulePayload{
					PublicationWhenType: 3,
					PublicationHowType:  1,
					SchedulesIDs:        in.ScheduleIDs,
					Texts:               texts,
				}
			case in.PublishAtDate != "":
				payload = hooppy.PostPublishAtPayload{
					PublicationWhenType: 2,
					PublicationHowType:  1,
					PublicationDate: hooppy.PublicationDate{
						Date:    in.PublishAtDate,
						Hours:   in.PublishAtHours,
						Minutes: in.PublishAtMinutes,
					},
					SelectedPagesIDs: in.PageIDs,
					Texts:            texts,
				}
			default:
				if len(in.PageIDs) == 0 {
					return errResult("page_ids is required for immediate publish (or use schedule_ids/project_id)")
				}
				payload = hooppy.PostPublishNowPayload{
					PublicationWhenType: 1,
					PublicationHowType:  1,
					SelectedPagesIDs:    in.PageIDs,
					Texts:               texts,
				}
			}

			resp, err := c.CreatePost(ctx, payload)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- delete_post ---

type deletePostInput struct {
	ID int `json:"id" jsonschema:"Post ID to delete."`
}

func registerDeletePost(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_delete_post",
			Description: "Delete a single post by ID from Hooppy.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deletePostInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.DeletePost(ctx, in.ID)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- batch_delete_posts ---

type batchDeletePostsInput struct {
	IDs []int `json:"ids" jsonschema:"Post IDs to delete."`
}

func registerBatchDeletePosts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_batch_delete_posts",
			Description: "Delete multiple posts by ID in a single request.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in batchDeletePostsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.BatchDeletePosts(ctx, in.IDs)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- upload_media ---

type uploadMediaInput struct {
	FilePath string `json:"file_path" jsonschema:"Local path to the photo or video file to upload."`
}

func registerUploadMedia(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_upload_media",
			Description: "Upload a photo or video file to Hooppy. Returns attachment metadata for use in create_post.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in uploadMediaInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.UploadMedia(ctx, in.FilePath, "")
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- upload_document ---

type uploadDocumentInput struct {
	FilePath string `json:"file_path" jsonschema:"Local path to the document file (PDF, archive, audio, etc.) to upload."`
}

func registerUploadDocument(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_upload_document",
			Description: "Upload a document file (PDF, archive, audio, etc.) to Hooppy. Returns attachment metadata for use in create_post.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in uploadDocumentInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.UploadDocument(ctx, in.FilePath, "")
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_projects ---

type listProjectsInput struct{}

func registerListProjects(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_projects",
			Description: "List post projects on Hooppy. Projects group posts for multi-platform publishing.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ listProjectsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListProjects(ctx, 0)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_schedules ---

type listSchedulesInput struct{}

func registerListSchedules(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_schedules",
			Description: "List publication schedules on Hooppy. Schedules define recurring publication plans.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ listSchedulesInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListSchedules(ctx, 0)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}
