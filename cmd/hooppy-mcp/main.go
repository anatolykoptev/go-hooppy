package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

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
	// Undocumented endpoints (not in OpenAPI spec v0.1.0)
	registerCreateSchedule(server)
	registerUpdateSchedule(server)
	registerDeleteSchedule(server)
	registerDeleteProject(server)
	registerCreateProject(server)
	registerUpdateProject(server)
	registerGetUser(server)
	registerListWatermarks(server)
	registerCreateWatermark(server)
	registerUpdateWatermark(server)
	registerDeleteWatermark(server)
	registerListProxies(server)
	registerCreateProxy(server)
	registerUpdateProxy(server)
	registerDeleteProxy(server)
	registerListNotifications(server)
	registerDisconnectPage(server)
	registerUpdatePost(server)
	// Posts search (scraping external pages) — UNDOCUMENTED
	registerListSearchPosts(server)
	registerListSourceResources(server)
	registerGetParsingForm(server)
	registerStartParsing(server)
	registerStopParsing(server)
	registerCopySearchPost(server)
	registerRewriteSearchPost(server)
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

func parseIntListStr(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var ids []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			// skip invalid entries — MCP input is free-text, unlike CLI which exits on error
			continue
		}
		ids = append(ids, n)
	}
	return ids
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
	ScheduleIDs      []int  `json:"schedule_ids,omitempty" jsonschema:"Schedule IDs for publishing via schedule (when_type=3). Required when project_id is set — the Hooppy API always needs schedules_ids for when_type=3."`
	ProjectID        int    `json:"project_id,omitempty" jsonschema:"Project ID for publishing via project. If set, schedule_ids is ALSO required (the API uses schedules_ids for when_type=3; project_id is an optional scope)."`
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
				if len(in.ScheduleIDs) == 0 {
					return errResult("schedule_ids is required even when project_id is set (Hooppy API requires it for when_type=3)")
				}
				payload = hooppy.PostPublishByProjectPayload{
					PublicationWhenType: 3,
					PublicationHowType:  1,
					ProjectID:           in.ProjectID,
					SchedulesIDs:        in.ScheduleIDs,
					Texts:               texts,
					Attachments:         []hooppy.Attachment{},
				}
			case len(in.ScheduleIDs) > 0:
				payload = hooppy.PostPublishBySchedulePayload{
					PublicationWhenType: 3,
					PublicationHowType:  1,
					SchedulesIDs:        in.ScheduleIDs,
					Texts:               texts,
					Attachments:         []hooppy.Attachment{},
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
					Attachments:      []hooppy.Attachment{},
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
					Attachments:         []hooppy.Attachment{},
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

// --- create_schedule (undocumented endpoint) ---

type createScheduleInput struct {
	Name string `json:"name" jsonschema:"Schedule name."`
}

func registerCreateSchedule(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_create_schedule",
			Description: "Create a publication schedule on Hooppy. Uses default settings (all flags off, state=active). UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createScheduleInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.CreateSchedule(ctx, hooppy.NewSchedulePayload(in.Name))
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- delete_schedule (undocumented endpoint) ---

type deleteScheduleInput struct {
	ID int `json:"id" jsonschema:"Schedule ID to delete."`
}

func registerDeleteSchedule(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_delete_schedule",
			Description: "Delete a publication schedule on Hooppy by ID. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteScheduleInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.DeleteSchedule(ctx, in.ID)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- delete_project (undocumented endpoint) ---

type deleteProjectInput struct {
	ID int `json:"id" jsonschema:"Project ID to delete."`
}

func registerDeleteProject(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_delete_project",
			Description: "Delete a project on Hooppy by ID. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteProjectInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.DeleteProject(ctx, in.ID)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- create_project (undocumented) ---

type createProjectInput struct {
	Name   string `json:"name" jsonschema:"Project name."`
	PageID int    `json:"page_id" jsonschema:"Page ID to associate with the project."`
}

func registerCreateProject(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_create_project",
			Description: "Create a post project on Hooppy. Uses default settings. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createProjectInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.CreateProject(ctx, hooppy.NewProjectPayload(in.Name, in.PageID))
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- get_user (undocumented) ---

type getUserInput struct{}

func registerGetUser(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_get_user",
			Description: "Get the current authenticated user's profile. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ getUserInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.GetUser(ctx)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_watermarks (undocumented) ---

type listWatermarksInput struct{}

func registerListWatermarks(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_watermarks",
			Description: "List watermarks on Hooppy. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ listWatermarksInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListWatermarks(ctx, 0)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_proxies (undocumented) ---

type listProxiesInput struct{}

func registerListProxies(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_proxies",
			Description: "List proxy servers on Hooppy. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ listProxiesInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListProxies(ctx)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_notifications (undocumented) ---

type listNotificationsInput struct{}

func registerListNotifications(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_notifications",
			Description: "List publication status notifications on Hooppy. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ listNotificationsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListNotifications(ctx, 0)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- disconnect_page (undocumented) ---

type disconnectPageInput struct {
	ID int `json:"id" jsonschema:"Page ID to disconnect."`
}

func registerDisconnectPage(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_disconnect_page",
			Description: "Disconnect a social media page (group) by ID. Idempotent. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in disconnectPageInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.DisconnectPage(ctx, in.ID)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- update_project (undocumented) ---

type updateProjectInput struct {
	ID   int    `json:"id" jsonschema:"Project ID to update."`
	Name string `json:"name" jsonschema:"New project name."`
}

func registerUpdateProject(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_update_project",
			Description: "Update a project name on Hooppy by ID. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateProjectInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.UpdateProject(ctx, in.ID, in.Name)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- update_schedule (undocumented) ---

type updateScheduleInput struct {
	ID    int    `json:"id" jsonschema:"Schedule ID to update."`
	Name  string `json:"name" jsonschema:"Schedule name."`
	State int    `json:"state,omitempty" jsonschema:"State: 1=active (default), 0=paused."`
}

func registerUpdateSchedule(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_update_schedule",
			Description: "Update a publication schedule on Hooppy by ID. Uses default settings for unset fields. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateScheduleInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			payload := hooppy.NewSchedulePayload(in.Name)
			if in.State != 0 {
				payload.State = in.State
			}
			resp, err := c.UpdateSchedule(ctx, in.ID, payload)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- update_post (undocumented) ---

type updatePostInput struct {
	ID      int    `json:"id" jsonschema:"Post ID to update."`
	Text    string `json:"text" jsonschema:"Updated post text."`
	PageIDs []int  `json:"page_ids" jsonschema:"Page IDs to publish to."`
}

func registerUpdatePost(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_update_post",
			Description: "Update an existing post on Hooppy by ID. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updatePostInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.UpdatePost(ctx, in.ID, hooppy.PostPublishNowPayload{
				PublicationWhenType: 1,
				PublicationHowType:  1,
				SelectedPagesIDs:    in.PageIDs,
				Texts:               []hooppy.PostText{{Text: in.Text, SourceID: 0}},
				Attachments:         []hooppy.Attachment{},
			})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- create_watermark (undocumented) ---

type createWatermarkInput struct {
	Name     string `json:"name" jsonschema:"Watermark name."`
	File     string `json:"file,omitempty" jsonschema:"File path or identifier."`
	Space    int    `json:"space,omitempty" jsonschema:"Spacing."`
	Position int    `json:"position,omitempty" jsonschema:"Position."`
	Opacity  int    `json:"opacity,omitempty" jsonschema:"Opacity (0-100)."`
	Size     int    `json:"size,omitempty" jsonschema:"Size."`
}

func registerCreateWatermark(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_create_watermark",
			Description: "Create a watermark on Hooppy. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createWatermarkInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.CreateWatermark(ctx, hooppy.WatermarkPayload{
				Name: in.Name, File: in.File, Space: in.Space, Position: in.Position, Opacity: in.Opacity, Size: in.Size,
			})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- update_watermark (undocumented) ---

type updateWatermarkInput struct {
	ID       int    `json:"id" jsonschema:"Watermark ID to update."`
	Name     string `json:"name,omitempty" jsonschema:"Watermark name."`
	File     string `json:"file,omitempty" jsonschema:"File path or identifier."`
	Space    int    `json:"space,omitempty" jsonschema:"Spacing."`
	Position int    `json:"position,omitempty" jsonschema:"Position."`
	Opacity  int    `json:"opacity,omitempty" jsonschema:"Opacity (0-100)."`
	Size     int    `json:"size,omitempty" jsonschema:"Size."`
}

func registerUpdateWatermark(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_update_watermark",
			Description: "Update a watermark on Hooppy by ID. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateWatermarkInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.UpdateWatermark(ctx, in.ID, hooppy.WatermarkPayload{
				Name: in.Name, File: in.File, Space: in.Space, Position: in.Position, Opacity: in.Opacity, Size: in.Size,
			})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- delete_watermark (undocumented) ---

type deleteWatermarkInput struct {
	ID int `json:"id" jsonschema:"Watermark ID to delete."`
}

func registerDeleteWatermark(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_delete_watermark",
			Description: "Delete a watermark on Hooppy by ID. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteWatermarkInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.DeleteWatermark(ctx, in.ID)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- create_proxy (undocumented) ---

type createProxyInput struct {
	Name     string `json:"name,omitempty" jsonschema:"Proxy name."`
	IP       string `json:"ip" jsonschema:"Proxy IP address."`
	Port     string `json:"port" jsonschema:"Proxy port."`
	Login    string `json:"login,omitempty" jsonschema:"Proxy login."`
	Password string `json:"password,omitempty" jsonschema:"Proxy password."`
}

func registerCreateProxy(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_create_proxy",
			Description: "Create a proxy server on Hooppy. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createProxyInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.CreateProxy(ctx, hooppy.ProxyPayload{
				Name: in.Name, IP: in.IP, Port: in.Port, Login: in.Login, Password: in.Password,
			})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- update_proxy (undocumented) ---

type updateProxyInput struct {
	ID       int    `json:"id" jsonschema:"Proxy ID to update."`
	Name     string `json:"name,omitempty" jsonschema:"Proxy name."`
	IP       string `json:"ip,omitempty" jsonschema:"Proxy IP address."`
	Port     string `json:"port,omitempty" jsonschema:"Proxy port."`
	Login    string `json:"login,omitempty" jsonschema:"Proxy login."`
	Password string `json:"password,omitempty" jsonschema:"Proxy password."`
}

func registerUpdateProxy(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_update_proxy",
			Description: "Update a proxy server on Hooppy by ID. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateProxyInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.UpdateProxy(ctx, in.ID, hooppy.ProxyPayload{
				Name: in.Name, IP: in.IP, Port: in.Port, Login: in.Login, Password: in.Password,
			})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- delete_proxy (undocumented) ---

type deleteProxyInput struct {
	ID int `json:"id" jsonschema:"Proxy ID to delete."`
}

func registerDeleteProxy(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_delete_proxy",
			Description: "Delete a proxy server on Hooppy by ID. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in deleteProxyInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.DeleteProxy(ctx, in.ID)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- posts search (scraping external pages) — UNDOCUMENTED ---

// --- list_search_posts ---

type listSearchPostsInput struct {
	Text             string `json:"text,omitempty" jsonschema:"Search by text content."`
	DateFrom         string `json:"date_from,omitempty" jsonschema:"Filter by date from (dd.mm.yyyy)."`
	DateTo           string `json:"date_to,omitempty" jsonschema:"Filter by date to (dd.mm.yyyy)."`
	SourceType       int    `json:"source_type,omitempty" jsonschema:"Source type: 1=social, 2=RSS. 0=no filter."`
	SourceID         int    `json:"source_id,omitempty" jsonschema:"Social network ID (1=VK, 7=Instagram, etc.). 0=no filter."`
	SourceResourceID int    `json:"source_resource_id,omitempty" jsonschema:"Source resource ID (from list_source_resources). 0=no filter."`
	OwnerID          int    `json:"owner_id,omitempty" jsonschema:"Page ID within source. 0=no filter."`
	Page             int    `json:"page,omitempty" jsonschema:"Pagination page number."`
}

func registerListSearchPosts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_search_posts",
			Description: "List posts scraped from external social media pages. Posts must be scraped first via start_parsing. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listSearchPostsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListSearchPosts(ctx, hooppy.SearchPostsFilter{
				Text:             in.Text,
				DateFrom:         in.DateFrom,
				DateTo:           in.DateTo,
				SourceType:       in.SourceType,
				SourceID:         in.SourceID,
				SourceResourceID: in.SourceResourceID,
				OwnerID:          in.OwnerID,
				Page:             in.Page,
			})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_source_resources ---

func registerListSourceResources(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_source_resources",
			Description: "List configured source resources — groups of external social media pages to scrape posts from. Each resource has an ID (needed for start_parsing), a name, and the URLs to scrape. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListSourceResources(ctx)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- get_parsing_form ---

func registerGetParsingForm(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_parsing_status",
			Description: "Check parsing status and get available source resources + social accounts that can act as parsers. Returns is_parsing_in_progress flag. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.GetParsingForm(ctx)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- start_parsing ---

type startParsingInput struct {
	SourceType       int `json:"source_type" jsonschema:"Source type: 1=social, 2=RSS."`
	SearchType       int `json:"search_type" jsonschema:"Search method: 1=pages, 2=hashtag."`
	SourceID         int `json:"source_id" jsonschema:"Social network ID (1=VK, 7=Instagram, etc.)."`
	SourceResourceID int `json:"source_resource_id" jsonschema:"Source resource ID (from list_source_resources). REQUIRED."`
	AccountID        int `json:"social_account_for_parsing_id,omitempty" jsonschema:"Social account ID to use as parser (from parsing_status). 0=no account."`
	DateFrom         int `json:"date_from,omitempty" jsonschema:"Unix timestamp, 0=any date."`
	DateTo           int `json:"date_to,omitempty" jsonschema:"Unix timestamp, 0=any date."`
}

func registerStartParsing(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_start_parsing",
			Description: "Start scraping posts from an external source resource (a group of social media pages). Runs asynchronously — poll parsing_status to check completion, then list_search_posts to get results. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in startParsingInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.StartParsing(ctx, hooppy.ParsingStartPayload{
				SourceType:                in.SourceType,
				SearchType:                in.SearchType,
				SourceID:                  in.SourceID,
				SourceResourceID:          in.SourceResourceID,
				SocialAccountForParsingID: in.AccountID,
				DateFrom:                  in.DateFrom,
				DateTo:                    in.DateTo,
			})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- stop_parsing ---

func registerStopParsing(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_stop_parsing",
			Description: "Stop any in-progress scraping job. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if err := c.StopParsing(ctx); err != nil {
				return errResult(err.Error())
			}
			return jsonResult(map[string]bool{"success": true})
		},
	)
}

// --- copy_search_post ---

type copySearchPostInput struct {
	SearchPostID        int    `json:"search_post_id" jsonschema:"ID of the scraped post (from list_search_posts). REQUIRED."`
	PublicationWhenType int    `json:"publication_when_type" jsonschema:"1=publish now, 2=at specific time, 3=by schedule."`
	PublicationHowType  int    `json:"publication_how_type,omitempty" jsonschema:"Publication how type (1=default)."`
	SelectedPagesIDs    string `json:"selected_pages_ids,omitempty" jsonschema:"Comma-separated page IDs to publish to (for when_type 1 or 2). Use list_pages to get IDs."`
	SchedulesIDs        string `json:"schedules_ids,omitempty" jsonschema:"Comma-separated schedule IDs (for when_type 3). Use list_schedules to get IDs."`
	PublishDate         string `json:"publish_date,omitempty" jsonschema:"Publication date dd.mm.yyyy (for when_type 2)."`
	PublishHours        string `json:"publish_hours,omitempty" jsonschema:"Publication hours HH (for when_type 2)."`
	PublishMinutes      string `json:"publish_minutes,omitempty" jsonschema:"Publication minutes MM (for when_type 2)."`
}

func registerCopySearchPost(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_copy_search_post",
			Description: "Copy a scraped post (from list_search_posts) to your own pages. The server auto-fills text and photos from the scraped post — just provide the scraped post ID and where to publish. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in copySearchPostInput) (*mcp.CallToolResult, error) {
			if in.SearchPostID == 0 {
				return errResult("search_post_id is required (use list_search_posts to find IDs)")
			}
			if in.PublicationWhenType == 2 && (in.PublishDate == "" || in.PublishHours == "" || in.PublishMinutes == "") {
				return errResult("publish_date, publish_hours, publish_minutes are required for publication_when_type=2")
			}
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			payload := hooppy.CopySearchPostPayload{
				SearchPostID:        in.SearchPostID,
				PublicationWhenType: in.PublicationWhenType,
				PublicationHowType:  in.PublicationHowType,
			}
			switch in.PublicationWhenType {
			case 3:
				payload.SchedulesIDs = parseIntListStr(in.SchedulesIDs)
			case 2:
				payload.SelectedPagesIDs = parseIntListStr(in.SelectedPagesIDs)
				payload.PublicationDate = &hooppy.PublicationDate{
					Date:    in.PublishDate,
					Hours:   in.PublishHours,
					Minutes: in.PublishMinutes,
				}
			default:
				payload.SelectedPagesIDs = parseIntListStr(in.SelectedPagesIDs)
			}
			resp, err := c.CopySearchPost(ctx, payload)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- rewrite_search_post ---

type rewriteSearchPostInput struct {
	SearchPostID        int    `json:"search_post_id" jsonschema:"ID of the scraped post (from list_search_posts). REQUIRED."`
	Text                string `json:"text" jsonschema:"New text for the post. REQUIRED."`
	PublicationWhenType int    `json:"publication_when_type" jsonschema:"1=publish now, 2=at specific time, 3=by schedule."`
	PublicationHowType  int    `json:"publication_how_type,omitempty" jsonschema:"Publication how type (1=default)."`
	SelectedPagesIDs    string `json:"selected_pages_ids,omitempty" jsonschema:"Comma-separated page IDs to publish to (for when_type 1 or 2). Use list_pages to get IDs."`
	SchedulesIDs        string `json:"schedules_ids,omitempty" jsonschema:"Comma-separated schedule IDs (for when_type 3). Use list_schedules to get IDs."`
	PublishDate         string `json:"publish_date,omitempty" jsonschema:"Publication date dd.mm.yyyy (for when_type 2)."`
	PublishHours        string `json:"publish_hours,omitempty" jsonschema:"Publication hours HH (for when_type 2)."`
	PublishMinutes      string `json:"publish_minutes,omitempty" jsonschema:"Publication minutes MM (for when_type 2)."`
}

func registerRewriteSearchPost(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_rewrite_search_post",
			Description: "Rewrite a scraped post (from list_search_posts) with custom text and publish to your pages. To keep original photos, use copy_search_post with scraped photo IDs instead, or upload photos via upload_media first. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in rewriteSearchPostInput) (*mcp.CallToolResult, error) {
			if in.SearchPostID == 0 {
				return errResult("search_post_id is required (use list_search_posts to find IDs)")
			}
			if in.Text == "" {
				return errResult("text is required")
			}
			if in.PublicationWhenType == 2 && (in.PublishDate == "" || in.PublishHours == "" || in.PublishMinutes == "") {
				return errResult("publish_date, publish_hours, publish_minutes are required for publication_when_type=2")
			}
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			payload := hooppy.CopySearchPostPayload{
				SearchPostID:        in.SearchPostID,
				PublicationWhenType: in.PublicationWhenType,
				PublicationHowType:  in.PublicationHowType,
				Texts:               []hooppy.PostText{{Text: in.Text, SourceID: 0}},
			}
			switch in.PublicationWhenType {
			case 3:
				payload.SchedulesIDs = parseIntListStr(in.SchedulesIDs)
			case 2:
				payload.SelectedPagesIDs = parseIntListStr(in.SelectedPagesIDs)
				payload.PublicationDate = &hooppy.PublicationDate{
					Date:    in.PublishDate,
					Hours:   in.PublishHours,
					Minutes: in.PublishMinutes,
				}
			default:
				payload.SelectedPagesIDs = parseIntListStr(in.SelectedPagesIDs)
			}
			resp, err := c.RewriteSearchPost(ctx, payload)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}
