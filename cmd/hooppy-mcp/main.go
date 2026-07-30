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
	registerGetScheduleEdit(server)
	registerListSchedulePosts(server)
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
	registerUpdatePostText(server)
	registerMovePost(server)
	registerBatchMovePosts(server)
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

// parseOrderedIDListStr parses a comma-separated id list STRICTLY: any
// unparseable or empty element returns an error naming the offending token.
// Use this for ORDER-SIGNIFICANT id lists (search_post_ids on rewrite/import)
// where the lenient parseIntListStr would silently drop a bad entry and shift
// every later post's schedule slot by one — one post not copied plus a silent
// slot reassignment is worse than the fully-invalid case (which errors via
// the both-empty guard). Do NOT reuse the lenient helper on an ordered id
// list.
func parseOrderedIDListStr(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	ids := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("search_post_ids: empty element in %q — expected a comma-separated list of positive IDs", s)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("search_post_ids: invalid ID %q: %v", p, err)
		}
		// The error text below promises "positive IDs" — enforce it: a 0 or
		// negative id in an order-significant list is never a real scraped
		// post id, and accepting it would let a bad entry through that the
		// CLI batch path (copySearchPostIDs) already rejects (id <= 0).
		if n <= 0 {
			return nil, fmt.Errorf("search_post_ids: %q is not a positive ID — expected a comma-separated list of positive IDs", p)
		}
		ids = append(ids, n)
	}
	return ids, nil
}

// --- list_accounts ---

type listAccountsInput struct {
	SourceID int  `json:"source_id,omitempty" jsonschema:"Filter by social network source ID (e.g. 1=VK, 6=Pinterest, 9=Telegram channels). 0=no filter."`
	Page     int  `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page, 20 rows per page)."`
	All      bool `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListAccounts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_accounts",
			Description: "List connected social network accounts on Hooppy. Returns account IDs, social network type (source_id), and profile info. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows).",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listAccountsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllAccountsWithTotal(ctx, hooppy.ListAccountsFilter{SourceID: in.SourceID})
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(a hooppy.Account) int { return a.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListAccounts(ctx, hooppy.ListAccountsFilter{SourceID: in.SourceID, Page: in.Page})
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_pages ---

type listPagesInput struct {
	SourceID  int  `json:"source_id,omitempty" jsonschema:"Filter by social network source ID. 0=no filter."`
	AccountID int  `json:"account_id,omitempty" jsonschema:"Filter by parent account ID. 0=no filter."`
	Page      int  `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page, 20 rows per page)."`
	All       bool `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListPages(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_pages",
			Description: "List connected groups/pages for social network accounts. Page IDs are needed for create_post (selected_pages_ids). Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows).",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listPagesInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllPagesWithTotal(ctx, hooppy.ListPagesFilter{SourceID: in.SourceID, AccountID: in.AccountID})
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(p hooppy.Page) int { return p.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListPages(ctx, hooppy.ListPagesFilter{SourceID: in.SourceID, AccountID: in.AccountID, Page: in.Page})
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
	AccountID       int    `json:"account_id,omitempty" jsonschema:"DEPRECATED/no-op: the API silently ignores account_id on /posts; use schedule_id, source_id, or project_id to narrow. Setting this errors."`
	PageID          int    `json:"page_id,omitempty" jsonschema:"DEPRECATED/no-op: the API silently ignores page_id on /posts; use schedule_id, source_id, or project_id to narrow. Setting this errors."`
	ScheduleID      int    `json:"schedule_id,omitempty" jsonschema:"Filter by schedule ID."`
	ProjectID       int    `json:"project_id,omitempty" jsonschema:"Filter by project ID."`
	Page            int    `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page)."`
	All             bool   `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListPosts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_posts",
			Description: "List posts on Hooppy with optional filters by status, date, social network, schedule, or project. page_id and account_id are NOT server-side on this endpoint — the API silently ignores them; setting either errors, so use schedule_id, source_id, or project_id to narrow. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows).",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listPostsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllPostsWithTotal(ctx, hooppy.ListPostsFilter{
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
				env, err := hooppy.NewAllListEnvelope(all, total, func(p hooppy.Post) int { return p.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListPosts(ctx, hooppy.ListPostsFilter{
				IsPublished:     in.IsPublished,
				PublicationDate: in.PublicationDate,
				SourceID:        in.SourceID,
				AccountID:       in.AccountID,
				PageID:          in.PageID,
				ScheduleID:      in.ScheduleID,
				ProjectID:       in.ProjectID,
				Page:            in.Page,
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

type listProjectsInput struct {
	Page int  `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page, 20 rows per page)."`
	All  bool `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListProjects(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_projects",
			Description: "List post projects on Hooppy. Projects group posts for multi-platform publishing. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows).",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listProjectsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllProjectsWithTotal(ctx)
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(p hooppy.Project) int { return p.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListProjects(ctx, in.Page)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_schedules ---

type listSchedulesInput struct {
	Page int  `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page, 20 rows per page)."`
	All  bool `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListSchedules(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_schedules",
			Description: "List publication schedules on Hooppy. Schedules define recurring publication plans. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows).",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listSchedulesInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllSchedulesWithTotal(ctx)
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(s hooppy.Schedule) int { return s.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListSchedules(ctx, in.Page)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- get_schedule_edit (undocumented endpoint) ---

type getScheduleEditInput struct {
	ID int `json:"id" jsonschema:"Schedule ID to fetch the editable state for (use list_schedules to find IDs). REQUIRED."`
}

func registerGetScheduleEdit(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_get_schedule_edit",
			Description: "Get a schedule's full editable state, including its posting times (an array of 7 weekday arrays, each holding that day's time slots). This is the endpoint that models the schedule's times — the list_schedules response does not carry them. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in getScheduleEditInput) (*mcp.CallToolResult, error) {
			if in.ID == 0 {
				return errResult("id is required (use list_schedules to find IDs)")
			}
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.GetScheduleEdit(ctx, in.ID)
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

type listWatermarksInput struct {
	Page int  `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page, 20 rows per page)."`
	All  bool `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListWatermarks(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_watermarks",
			Description: "List watermarks on Hooppy. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows). UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listWatermarksInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllWatermarksWithTotal(ctx)
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(w hooppy.Watermark) int { return w.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListWatermarks(ctx, in.Page)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_proxies (undocumented) ---

type listProxiesInput struct {
	Page int  `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page, 20 rows per page)."`
	All  bool `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListProxies(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_proxies",
			Description: "List proxy servers on Hooppy. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows). UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listProxiesInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllProxiesWithTotal(ctx)
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(p hooppy.Proxy) int { return p.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListProxies(ctx, in.Page)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_notifications (undocumented) ---

type listNotificationsInput struct {
	Page int  `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page, 20 rows per page)."`
	All  bool `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListNotifications(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_notifications",
			Description: "List publication status notifications on Hooppy. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows). UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listNotificationsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllNotificationsWithTotal(ctx)
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(n hooppy.Notification) int { return n.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListNotifications(ctx, in.Page)
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
	Name  string `json:"name,omitempty" jsonschema:"Schedule name. Optional, but at least one of name or state must be set."`
	State int    `json:"state,omitempty" jsonschema:"State: 1=active (default), 0=paused. Optional, but at least one of name or state must be set."`
}

// registerUpdateSchedule wires the schedule-safe update path. It delegates to
// hooppy.UpdateScheduleFromEdit, which fetches the full schedule state via
// GET /posts/schedules/{id}/edit (72 keys on the wire) and PUTs the complete
// object back with only the caller's overrides applied — preserving every
// field the tool does not expose as an input.
//
// This is the mitigation for issue #81 (the third instance of the
// CLI-safe/MCP-destructive divergence class): the previous handler called the
// raw UpdateSchedule partial writer, which sends only the 36 keys
// SchedulePayload models and silently resets the other 36 — posting times,
// page targets, captions, comments, buttons, start/stop dates, watermark,
// UTM tags, and the two story/reels source-id lists — on an object that
// decides where everything publishes. An LLM asked to "change the posting
// time on that schedule" would match this tool's intent and wipe the
// schedule. The sibling CLI `schedules update` was already routed through
// UpdateScheduleFromEdit; the MCP handler is the worse surface (the client is
// an LLM) and must not lag behind it.
func registerUpdateSchedule(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_update_schedule",
			Description: "Update a publication schedule on Hooppy by ID. Performs a read-modify-write: fetches the schedule's full editable state, applies only the fields you specify (name, state), and sends the complete object back — preserving every other field the API carries (posting times, page targets, captions, comments, buttons, start/stop dates, watermark, UTM tags, story/reels source-id lists, and ~50 more the tool does not expose as inputs). Do NOT expect a partial update: changing only the name through a partial writer would silently wipe the schedule's posting times and page selection. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateScheduleInput) (*mcp.CallToolResult, error) {
			if in.ID == 0 {
				return errResult("id is required (use list_schedules to find IDs)")
			}
			if in.Name == "" && in.State == 0 {
				return errResult("at least one of name or state is required")
			}
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			overrides := map[string]json.RawMessage{}
			if in.Name != "" {
				b, err := json.Marshal(in.Name)
				if err != nil {
					return errResult(fmt.Sprintf("marshal name: %v", err))
				}
				overrides["name"] = b
			}
			if in.State != 0 {
				b, err := json.Marshal(in.State)
				if err != nil {
					return errResult(fmt.Sprintf("marshal state: %v", err))
				}
				overrides["state"] = b
			}
			resp, err := c.UpdateScheduleFromEdit(ctx, in.ID, overrides)
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
			Name: "hooppy_update_post",
			Description: "Update an existing post on Hooppy by ID by republishing it immediately (publication_when_type=1) to the given page_ids. " +
				"WARNING: this drops the post out of any schedule it belongs to and clears attachments — it is a full republish, not an in-place edit. " +
				"To edit only the text of a scheduled post while preserving its schedule, attachments, page selection and per-source text variants, use hooppy_update_post_text instead. " +
				"UNDOCUMENTED endpoint — may change without notice.",
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

// --- update_post_text (undocumented, schedule-safe) ---

type updatePostTextInput struct {
	ID   int    `json:"id" jsonschema:"Post ID to edit."`
	Text string `json:"text" jsonschema:"New post text. Replaces the text of every per-network text variant the post currently has, preserving each variant's source_id."`
}

// registerUpdatePostText wires the schedule-safe text-only edit path. It
// delegates to hooppy.UpdatePostText, which fetches the current post via
// GET /posts/{id}/edit and sends the full state back via PUT /posts/{id}
// with only the text swapped — preserving schedule_id, attachments, page
// selection (selected_pages_by_source_ids) and per-source text variants.
// This is the correct tool for "fix the typo in that scheduled post"; the
// sibling hooppy_update_post republishes immediately and drops the schedule
// (issue #49).
func registerUpdatePostText(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name: "hooppy_update_post_text",
			Description: "Edit ONLY the text of an existing Hooppy post while preserving its schedule (schedule_id), attachments, page selection and per-source text variants. " +
				"The safe way to fix a typo or reword a SCHEDULED post — unlike hooppy_update_post, this does NOT republish immediately or drop the post out of its schedule. " +
				"Fetches the current post state and sends the full state back with only the text changed, so nothing else is wiped. UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updatePostTextInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.UpdatePostText(ctx, in.ID, in.Text)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- move_post (undocumented, schedule-safe) ---

type movePostInput struct {
	ID           int `json:"id" jsonschema:"Post ID to move."`
	ToScheduleID int `json:"to_schedule_id" jsonschema:"Target schedule ID to move the post to (REQUIRED). The post is re-slotted to the TAIL of the target queue; the server assigns the new publication_date, which is returned in the result."`
}

// registerMovePost wires the single-post move path. It delegates to
// hooppy.MovePost, which guards publication_when_type==3 via a pre-move
// GET /posts/{id}/edit, then moves via POST /posts/batch/move (the same
// server-side move the batch path uses, passing the single id) — the server
// re-slots the post to the tail and preserves texts/attachments/page
// selection (the body carries only posts_ids + schedule_id). The new
// publication_date is recovered from a post-move GET /posts/{id}/edit. The
// date is the load-bearing output: a move re-slots to the tail, and moving
// into a booked schedule is a silent months-long delay otherwise.
func registerMovePost(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name: "hooppy_move_post",
			Description: "Move a single existing post to another schedule, preserving its texts, attachments, page selection and per-source text variants. " +
				"A move RE-SLOTS the post to the TAIL of the target queue — the server assigns the new publication_date, which is returned in the result. " +
				"Moving into a booked schedule can delay the post by months; the returned publication_date is how the caller sees that delay. " +
				"Only a schedule-driven post (publication_when_type=3) can be moved; a non-schedule post is refused. " +
				"UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in movePostInput) (*mcp.CallToolResult, error) {
			if in.ID <= 0 {
				return errResult("id is required (a positive post id) — an impossible id is accepted by the server and fabricates a success entry")
			}
			if in.ToScheduleID <= 0 {
				return errResult("to_schedule_id is required (a positive schedule id) — a move targeted at no schedule (0) or a negative id would publish to nothing; the server accepts a negative and fabricates a success entry")
			}
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.MovePost(ctx, in.ID, in.ToScheduleID)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- batch_move_posts (undocumented) ---

type batchMovePostsInput struct {
	IDs          []int `json:"ids" jsonschema:"Post IDs to move (1 to 1000)."`
	ToScheduleID int   `json:"to_schedule_id" jsonschema:"Target schedule ID to move the posts to (REQUIRED). Each post is re-slotted to the TAIL of the target queue; the server assigns each post's new publication_date, which is returned per post in the result."`
}

// registerBatchMovePosts wires the batch move path. It delegates to
// hooppy.BatchMovePosts, which POSTs /posts/batch/move with posts_ids as a
// comma-joined STRING (NOT a JSON array — a JSON array makes the live
// server 500) and then recovers each post's new publication_date from a
// post-move GET /posts/{id}/edit. The per-post dates are the load-bearing
// output.
//
// A single-id batch is routed to MovePost (so the when_type guard fires —
// item E) and the result is WRAPPED into a BatchMovePostsResult so the
// output shape is identical for one id and many: a consumer reading
// .moved[] gets the entry either way. The result is ALWAYS a
// BatchMovePostsResult {success, moved:[{id, schedule_id, publication_date,
// …}]}.
func registerBatchMovePosts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name: "hooppy_batch_move_posts",
			Description: "Move multiple existing posts to another schedule in a single request. " +
				"Each post is RE-SLOTTED to the TAIL of the target queue — the server assigns each post's new publication_date, which is returned per post in the `moved` array of the result (one entry per id, even for a single-id call). " +
				"Moving into a booked schedule can delay posts by months; the returned per-post publication_date values are how the caller sees that delay. " +
				"UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in batchMovePostsInput) (*mcp.CallToolResult, error) {
			if len(in.IDs) == 0 {
				return errResult("ids is required (at least one post ID)")
			}
			for _, id := range in.IDs {
				if id <= 0 {
					return errResult(fmt.Sprintf("ids must all be positive post ids (got %d) — an impossible id is accepted by the server and fabricates a success entry", id))
				}
			}
			if in.ToScheduleID <= 0 {
				return errResult("to_schedule_id is required (a positive schedule id) — a move targeted at no schedule (0) or a negative id would publish to nothing; the server accepts a negative and fabricates a success entry")
			}
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			// Route a single-id batch to MovePost so the when_type guard
			// fires (item E): a single-id batch and a single-post move now
			// behave identically. WRAP the result into a
			// BatchMovePostsResult so the output shape matches the multi-id
			// case — a consumer reading .moved[] gets the entry for the
			// single id. Zero extra requests for N>1.
			if len(in.IDs) == 1 {
				resp, err := c.MovePost(ctx, in.IDs[0], in.ToScheduleID)
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(&hooppy.BatchMovePostsResult{
					Success: resp.Success,
					Moved: []hooppy.MovedPost{{
						ID:              in.IDs[0],
						ScheduleID:      resp.ScheduleID,
						PublicationDate: resp.PublicationDate,
						SlotLookupError: resp.SlotLookupError,
						Warning:         resp.Warning,
					}},
				})
			}
			resp, err := c.BatchMovePosts(ctx, in.IDs, in.ToScheduleID)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_schedule_posts (undocumented) ---

type listSchedulePostsInput struct {
	ScheduleID int    `json:"schedule_id" jsonschema:"Schedule ID to show the queue for (use list_schedules to find IDs). REQUIRED."`
	DateFrom   string `json:"date_from,omitempty" jsonschema:"Narrow the calendar start (dd.mm.yyyy). Use this to recover a TRUNCATED result (is_has_more=true)."`
	DateTo     string `json:"date_to,omitempty" jsonschema:"Narrow the calendar end (dd.mm.yyyy). Use this to recover a TRUNCATED result (is_has_more=true)."`
	Page       int    `json:"page,omitempty" jsonschema:"Page number, 1-indexed (0 or omit = first page). Advance this to recover a TRUNCATED result (is_has_more=true) without guessing dates."`
}

// schedulePostsResultEnvelope wraps the schedule-queue response with an
// optional warning field so a truncation (or page-overrun) signal travels
// as STRUCTURED data — valid JSON in BOTH branches — not as a prose prefix
// that makes the truncated result unparseable (the prior shape). The
// warning field is named in the hooppy_list_schedule_posts description; an
// agent reads it the same way it reads PostMoveResult.Warning /
// MovedPost.Warning. The embedded *SchedulePostsResponse flattens its
// fields (posts_by_days, total_rows, rows_limit, is_has_more) alongside
// warning, so a non-truncated call (warning empty, omitempty) serialises
// identically to the raw envelope.
type schedulePostsResultEnvelope struct {
	Warning string `json:"warning,omitempty"`
	*hooppy.SchedulePostsResponse
}

// registerListSchedulePosts wires the schedule-queue read. It delegates to
// hooppy.ListSchedulePosts, which issues exactly ONE GET
// /posts/schedules/{id}/posts and returns the queue depth (total_rows) and
// the per-day calendar (posts_by_days). The LAST key in posts_by_days is
// the booked-until date — but ONLY when is_has_more is false AND no
// date_from/date_to/page narrowing was applied; a narrowed query is a
// subset by construction (its last key is the WINDOW's last day, not the
// schedule's booked-until), and a truncated response's last day key is the
// last day of page one. The caller MUST check is_has_more and the warning
// field, and narrow with date_from/date_to when truncated. No paged walk —
// the endpoint returns the whole calendar in one envelope (or a truncated
// first page; narrow to recover the rest).
func registerListSchedulePosts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name: "hooppy_list_schedule_posts",
			Description: "Show a schedule's queue — its depth (total_rows) and per-day calendar (posts_by_days, keyed dd.mm.yyyy, each value an object {day_name, day_date, posts[]}) — in ONE request. " +
				"The LAST key in posts_by_days is the booked-until date ONLY when is_has_more is false AND no date_from/date_to/page narrowing was applied (a narrowed query's last key is the WINDOW's last day, not the schedule's booked-until). " +
				"When is_has_more is true the result is TRUNCATED to the first page and a `warning` field is set; the caller MUST narrow with date_from/date_to to recover the rest. " +
				"A `warning` field is ALSO set when page>0 returns zero day keys with total_rows>0 (a page past the end — total_rows is the collection total and does not change with paging). " +
				"The result is ALWAYS valid JSON: {warning?, posts_by_days, total_rows, rows_limit, is_has_more}. " +
				"Use this before moving posts INTO a schedule to see how far out the queue runs. " +
				"UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listSchedulePostsInput) (*mcp.CallToolResult, error) {
			if in.ScheduleID == 0 {
				return errResult("schedule_id is required (use list_schedules to find IDs)")
			}
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.ListSchedulePosts(ctx, hooppy.ListSchedulePostsFilter{
				ScheduleID: in.ScheduleID,
				DateFrom:   in.DateFrom,
				DateTo:     in.DateTo,
				Page:       in.Page,
			})
			if err != nil {
				return errResult(err.Error())
			}
			// Enforce truncation + page-overrun on BOTH front-ends (issue
			// #81 class): the CLI exits non-zero; the MCP tool MUST also
			// signal it. An agent reads MCP, where there is no exit code —
			// so the signal travels as a STRUCTURED `warning` field on the
			// JSON envelope (valid JSON in both branches, unlike the prior
			// prose-prefix shape that made the truncated result unparseable).
			// The data is still present (total_rows is the real depth); the
			// warning names the recovery levers (date_from/date_to, page).
			env := schedulePostsResultEnvelope{SchedulePostsResponse: resp}
			switch {
			case in.Page > 0 && len(resp.PostsByDays) == 0 && resp.TotalRows > 0:
				// Page past the end: total_rows is the collection total
				// (unchanged by paging), so only this signal detects it.
				env.Warning = fmt.Sprintf("PARTIAL result — page %d is past the end of the calendar (total_rows=%d, zero day keys returned). total_rows is the collection total and does not change with paging; this page has no data. Use a lower page.", in.Page, resp.TotalRows)
			case resp.IsHasMore:
				env.Warning = fmt.Sprintf("PARTIAL result — is_has_more=true (total_rows=%d, rows_limit=%d); the calendar is truncated to the first page. Narrow with date_from/date_to or advance page to recover the rest. One-request contract: no paged walk.", resp.TotalRows, resp.RowsLimit)
			}
			return jsonResult(env)
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
	Text                string  `json:"text,omitempty" jsonschema:"Search by text content."`
	DateFrom            string  `json:"date_from,omitempty" jsonschema:"Filter by date from (dd.mm.yyyy)."`
	DateTo              string  `json:"date_to,omitempty" jsonschema:"Filter by date to (dd.mm.yyyy)."`
	SourceType          int     `json:"source_type,omitempty" jsonschema:"Source type: 1=social, 2=RSS. 0=no filter."`
	SourceID            int     `json:"source_id,omitempty" jsonschema:"DEPRECATED/no-op: the API silently ignores source_id on /posts-search; use source_type, content_types, photos_amount, video_duration, or text to narrow. 0=no filter (setting non-zero errors)."`
	SourceResourceID    int     `json:"source_resource_id,omitempty" jsonschema:"DEPRECATED/no-op: the API silently ignores source_resource_id on /posts-search; use source_type, content_types, photos_amount, video_duration, or text to narrow. 0=no filter (setting non-zero errors)."`
	OwnerID             int     `json:"owner_id,omitempty" jsonschema:"DEPRECATED/no-op: the API silently ignores owner_id on /posts-search; use source_type, content_types, photos_amount, video_duration, or text to narrow. 0=no filter (setting non-zero errors)."`
	Page                int     `json:"page,omitempty" jsonschema:"Pagination page number."`
	SortBy              string  `json:"sort_by,omitempty" jsonschema:"Sort field: publication_date, likes, reposts, comments, views, involvement."`
	SortDirection       string  `json:"sort_direction,omitempty" jsonschema:"Sort direction: desc (default) or asc."`
	MinLikes            int     `json:"min_likes,omitempty" jsonschema:"DEPRECATED/no-op: the API has no min-likes filter; use sort_by=likes instead. Setting this errors."`
	MinViews            int     `json:"min_views,omitempty" jsonschema:"DEPRECATED/no-op: the API has no min-views filter; use sort_by=views instead. Setting this errors."`
	MinComments         int     `json:"min_comments,omitempty" jsonschema:"DEPRECATED/no-op: the API has no min-comments filter; use sort_by=comments instead. Setting this errors."`
	MinReposts          int     `json:"min_reposts,omitempty" jsonschema:"DEPRECATED/no-op: the API has no min-reposts filter; use sort_by=reposts instead. Setting this errors."`
	MinInvolvement      float64 `json:"min_involvement,omitempty" jsonschema:"DEPRECATED/no-op: the API has no min-involvement filter; use sort_by=involvement instead. Setting this errors."`
	PhotosAmount        int     `json:"photos_amount,omitempty" jsonschema:"Photo count bucket (non-negative; 0 = unset). Measured against a live account: 1 -> 9294; 5 -> 566; 6 -> 742; 10 -> 2172; 99 -> 2172 (identical to 10, so the parameter saturates — it means \"N or more\", not \"exactly N\"). The filters_plug values array is empty, so valid keys are not enumerable client-side; any non-negative value is passed through verbatim and the server answers."`
	VideoDuration       int     `json:"video_duration,omitempty" jsonschema:"Video duration bucket (non-negative; 0 = unset). Measured against a live account (video content only): 1 -> 710; 2 -> 159; 3 -> 3525; 4 -> 4036; 5 -> 4128; 6 -> 4161; 7 -> 644; 8 -> 677; 9 and 10 return a server error. Keys 5-8 are real and each returns a distinct result set — the prior 1..4 guard hard-errored on four working filters. The valid key space is not enumerable client-side (the vendor may add keys); any non-negative value is passed through verbatim and the server answers. The filters_plug values array is empty."`
	ContentTypes        string  `json:"content_types,omitempty" jsonschema:"Comma-separated content types to include: photos, videos, audios, documents, links (AND filter)."`
	ContentTypesExclude string  `json:"content_types_exclude,omitempty" jsonschema:"Comma-separated content types to exclude."`
	All                 bool    `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListSearchPosts(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_search_posts",
			Description: "List posts scraped from external social media pages. Posts must be scraped first via start_parsing. Supports sorting by metrics (sort_by: likes, views, comments, reposts, involvement) and filtering by content types, photo count, and video duration. Metric THRESHOLD filters (min_likes/min_views/min_comments/min_reposts/min_involvement) are NOT server-side — the API silently ignores them; setting any of them errors, so use sort_by to rank by a metric instead. source_id, source_resource_id, and owner_id are also NOT server-side on this endpoint — the API accepts and silently ignores them; setting any of them errors, so use source_type, content_types, photos_amount, video_duration, or text to narrow. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows). UNDOCUMENTED endpoint — may change without notice.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listSearchPostsInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			f := hooppy.SearchPostsFilter{
				Text:                in.Text,
				DateFrom:            in.DateFrom,
				DateTo:              in.DateTo,
				SourceType:          in.SourceType,
				SourceID:            in.SourceID,
				SourceResourceID:    in.SourceResourceID,
				OwnerID:             in.OwnerID,
				Page:                in.Page,
				SortBy:              in.SortBy,
				SortDirection:       in.SortDirection,
				MinLikes:            in.MinLikes,
				MinViews:            in.MinViews,
				MinComments:         in.MinComments,
				MinReposts:          in.MinReposts,
				MinInvolvement:      in.MinInvolvement,
				PhotosAmount:        in.PhotosAmount,
				VideoDuration:       in.VideoDuration,
				ContentTypes:        in.ContentTypes,
				ContentTypesExclude: in.ContentTypesExclude,
			}
			if in.All {
				all, total, err := c.ListAllSearchPostsWithTotal(ctx, f)
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(p hooppy.SearchPost) int { return p.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListSearchPosts(ctx, f)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}

// --- list_source_resources ---

type listSourceResourcesInput struct {
	Page int  `json:"page,omitempty" jsonschema:"Page number for pagination, 1-indexed (0 or omit = first page, 20 rows per page)."`
	All  bool `json:"all,omitempty" jsonschema:"If true, fetch ALL pages in one call (walks until is_has_more is false). Recommended for LLM clients that cannot paginate reliably; overrides page."`
}

func registerListSourceResources(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_list_source_resources",
			Description: "List configured source resources — groups of external social media pages to scrape posts from. Each resource has an ID (needed for start_parsing), a name, and the URLs to scrape. Returns 20 rows per page; use page to paginate (1-indexed, 0 or omit = first page), or set all=true to fetch every page in one call (recommended — the response has is_has_more/total_rows). UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listSourceResourcesInput) (*mcp.CallToolResult, error) {
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			if in.All {
				all, total, err := c.ListAllSourceResourcesWithTotal(ctx)
				if err != nil {
					return errResult(err.Error())
				}
				env, err := hooppy.NewAllListEnvelope(all, total, func(s hooppy.SourceResource) int { return s.ID })
				if err != nil {
					return errResult(err.Error())
				}
				return jsonResult(env)
			}
			resp, err := c.ListSourceResources(ctx, in.Page)
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
	SearchPostID        int    `json:"search_post_id,omitempty" jsonschema:"ID of a single scraped post (from list_search_posts). Mutually exclusive with search_post_ids; pass exactly one."`
	SearchPostIDs       string `json:"search_post_ids,omitempty" jsonschema:"Comma-separated IDs of scraped posts (from list_search_posts) for a batch rewrite. Mutually exclusive with search_post_id; pass exactly one. The server assigns schedule slots in the given order."`
	Text                string `json:"text,omitempty" jsonschema:"New text overriding the original. REQUIRED with search_post_id; NOT allowed with search_post_ids — batch rewrite cannot express per-post text (omit text to keep each post's original text, like import)."`
	PublicationWhenType int    `json:"publication_when_type" jsonschema:"1=publish now, 2=at specific time, 3=by schedule."`
	PublicationHowType  int    `json:"publication_how_type,omitempty" jsonschema:"Publication how type (1=default)."`
	SelectedPagesIDs    string `json:"selected_pages_ids,omitempty" jsonschema:"Comma-separated page IDs to publish to (for when_type 1 or 2). Use list_pages to get IDs."`
	SchedulesIDs        string `json:"schedules_ids,omitempty" jsonschema:"Comma-separated schedule IDs (for when_type 3). Use list_schedules to get IDs."`
	PublishDate         string `json:"publish_date,omitempty" jsonschema:"Publication date dd.mm.yyyy (for when_type 2)."`
	PublishHours        string `json:"publish_hours,omitempty" jsonschema:"Publication hours HH (for when_type 2)."`
	PublishMinutes      string `json:"publish_minutes,omitempty" jsonschema:"Publication minutes MM (for when_type 2)."`
}

// buildRewriteSearchPostPayload validates a rewriteSearchPostInput and builds
// the payload — the testable analogue of the CLI buildRewritePayload. It is
// extracted so the strict-parse call site (search_post_ids) is guarded by a
// test: reverting the call site to the lenient parseIntListStr left the MCP
// suite green under the old inline form, because only the parser (not its
// use) was tested. Mirrors the CLI's form-dependent text rule (finding 4):
// single-post requires text; batch rejects text and sends an empty Texts
// slice (the server keeps each post's original text, like import).
func buildRewriteSearchPostPayload(in rewriteSearchPostInput) (hooppy.CopySearchPostPayload, error) {
	if in.SearchPostID != 0 && in.SearchPostIDs != "" {
		return hooppy.CopySearchPostPayload{}, fmt.Errorf("search_post_id and search_post_ids are mutually exclusive — pass only one (the scalar for a single post, the comma-separated list for a batch)")
	}
	if in.SearchPostID == 0 && in.SearchPostIDs == "" {
		return hooppy.CopySearchPostPayload{}, fmt.Errorf("search_post_id or search_post_ids is required (use list_search_posts to find IDs)")
	}
	batch := in.SearchPostIDs != ""
	// Batch rewrite cannot express per-post text through this payload shape
	// (one texts array for N ids is a broadcast or a positional pairing that
	// blanks posts 2..N). Refuse the combination; batch alone keeps each
	// post's original text (an empty Texts slice, like import). Single-post
	// requires text (the override).
	if batch && in.Text != "" {
		return hooppy.CopySearchPostPayload{}, fmt.Errorf("text is not allowed with search_post_ids — batch rewrite cannot express per-post text through this payload shape (one texts array for N ids is a broadcast or a positional pairing that blanks posts 2..N); omit text to keep each post's original text, or rewrite one post at a time with search_post_id")
	}
	if !batch && in.Text == "" {
		return hooppy.CopySearchPostPayload{}, fmt.Errorf("text is required for search_post_id (rewrite overrides the single post's text)")
	}
	if in.PublicationWhenType == 2 && (in.PublishDate == "" || in.PublishHours == "" || in.PublishMinutes == "") {
		return hooppy.CopySearchPostPayload{}, fmt.Errorf("publish_date, publish_hours, publish_minutes are required for publication_when_type=2")
	}
	// search_post_ids is ORDER-SIGNIFICANT (the server assigns schedule
	// slots in the given order), so parse it STRICTLY: a lenient parse that
	// skips a bad entry ("2001,abc,2003" → [2001,2003]) silently drops one
	// post and shifts every later slot by one. The fully-invalid case errors
	// via the both-empty guard above; the partial drop is the worse half and
	// must error too, naming the bad token.
	var batchIDs []int
	if batch {
		ids, err := parseOrderedIDListStr(in.SearchPostIDs)
		if err != nil {
			return hooppy.CopySearchPostPayload{}, err
		}
		batchIDs = ids
	}
	payload := hooppy.CopySearchPostPayload{
		PublicationWhenType: in.PublicationWhenType,
		PublicationHowType:  in.PublicationHowType,
	}
	if batch {
		payload.SearchPostIDs = batchIDs
		// Empty (non-nil) slice: RewriteSearchPost only replaces a nil slice,
		// so this passes through as `[]` on the wire and the server keeps each
		// post's original text (same shape as batch import). NOT
		// []PostText{{Text: ""}} — that risks publishing blank across the batch.
		payload.Texts = []hooppy.PostText{}
	} else {
		payload.SearchPostID = in.SearchPostID
		payload.Texts = []hooppy.PostText{{Text: in.Text, SourceID: 0}}
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
	return payload, nil
}

func registerRewriteSearchPost(server *mcp.Server) {
	mcpserver.AddTool(server,
		&mcp.Tool{
			Name:        "hooppy_rewrite_search_post",
			Description: "Rewrite one or more scraped posts (from list_search_posts) and publish to your pages. Pass a single id via search_post_id (text overrides the original), or a batch via search_post_ids (comma-separated; the server assigns schedule slots in the given order). Batch rewrite CANNOT override text — the payload shape cannot express per-post text — so text is rejected with search_post_ids and the batch keeps each post's original text (like import_search_post). To keep original photos for a single-post rewrite, use copy_search_post or upload photos via upload_media first. UNDOCUMENTED endpoint.",
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, in rewriteSearchPostInput) (*mcp.CallToolResult, error) {
			payload, err := buildRewriteSearchPostPayload(in)
			if err != nil {
				return errResult(err.Error())
			}
			c, err := client()
			if err != nil {
				return errResult(err.Error())
			}
			resp, err := c.RewriteSearchPost(ctx, payload)
			if err != nil {
				return errResult(err.Error())
			}
			return jsonResult(resp)
		},
	)
}
