package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-kit/cli"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	root := cli.NewRoot(cli.RootConfig{
		Use:     "hooppy",
		Short:   "Hooppy API CLI — manage social media posts via hooppy.ru",
		Version: version,
	})

	registerAccounts(root)
	registerPages(root)
	registerPosts(root)
	registerProjects(root)
	registerSchedules(root)
	registerFiles(root)
	registerUser(root)
	registerWatermarks(root)
	registerProxies(root)
	registerNotifications(root)
	registerSearch(root)
	registerMCPConfig(root)
	registerDoctor(root)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func mustClient() *hooppy.Client {
	c, err := hooppy.NewClientFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Fprintf(os.Stderr, "set HOOPPY_TOKEN env or ~/.config/hooppy/token\n")
		os.Exit(1)
	}
	return c
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding output: %v\n", err)
		os.Exit(1)
	}
}

func die(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// --- accounts ---

func registerAccounts(root *cobra.Command) {
	cmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "accounts",
		Short: "List connected social network accounts",
	})
	var sourceID int
	cmd.Flags().IntVar(&sourceID, "source", 0, "filter by social network source ID")
	cmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListAccounts(context.Background(), hooppy.ListAccountsFilter{SourceID: sourceID})
		die(err)
		printJSON(resp)
	}
}

// --- pages ---

func registerPages(root *cobra.Command) {
	pagesCmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "pages",
		Short: "Manage connected groups/pages",
	})

	// pages list
	listCmd := cli.RegisterSubcommand(pagesCmd, cli.SubcommandConfig{
		Name:  "list",
		Short: "List connected groups/pages",
	})
	var sourceID, accountID int
	listCmd.Flags().IntVar(&sourceID, "source", 0, "filter by social network source ID")
	listCmd.Flags().IntVar(&accountID, "account", 0, "filter by account ID")
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListPages(context.Background(), hooppy.ListPagesFilter{SourceID: sourceID, AccountID: accountID})
		die(err)
		printJSON(resp)
	}

	// pages disconnect (undocumented)
	disconnectCmd := cli.RegisterSubcommand(pagesCmd, cli.SubcommandConfig{
		Name:  "disconnect",
		Short: "Disconnect a page by ID (undocumented endpoint)",
	})
	disconnectCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy pages disconnect <id>")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.DisconnectPage(context.Background(), id)
		die(err)
		printJSON(resp)
	}
}

// --- posts ---

func registerPosts(root *cobra.Command) {
	postsCmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "posts",
		Short: "Manage posts",
	})

	// posts list
	listCmd := cli.RegisterSubcommand(postsCmd, cli.SubcommandConfig{
		Name:  "list",
		Short: "List posts",
	})
	var published, unpublished bool
	var pubDate string
	var pageID, sourceID, projectID, scheduleID, accountID, pageNum int
	listCmd.Flags().BoolVar(&published, "published", false, "show only published posts")
	listCmd.Flags().BoolVar(&unpublished, "unpublished", false, "show only unpublished posts")
	listCmd.Flags().StringVar(&pubDate, "date", "", "filter by publication date (dd.mm.yyyy)")
	listCmd.Flags().IntVar(&pageID, "page-id", 0, "filter by page ID")
	listCmd.Flags().IntVar(&sourceID, "source-id", 0, "filter by source ID (social network)")
	listCmd.Flags().IntVar(&projectID, "project-id", 0, "filter by project ID")
	listCmd.Flags().IntVar(&scheduleID, "schedule-id", 0, "filter by schedule ID")
	listCmd.Flags().IntVar(&accountID, "account-id", 0, "filter by account ID")
	listCmd.Flags().IntVar(&pageNum, "page", 0, "page number, 1-indexed (0 or omit = first page)")
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		if published && unpublished {
			fmt.Fprintln(os.Stderr, "error: --published and --unpublished are mutually exclusive")
			os.Exit(1)
		}
		c := mustClient()
		var isPub *bool
		if published {
			t := true
			isPub = &t
		} else if unpublished {
			f := false
			isPub = &f
		}
		resp, err := c.ListPosts(context.Background(), hooppy.ListPostsFilter{
			IsPublished:     isPub,
			PublicationDate: pubDate,
			PageID:          pageID,
			SourceID:        sourceID,
			ProjectID:       projectID,
			ScheduleID:      scheduleID,
			AccountID:       accountID,
			Page:            pageNum,
		})
		die(err)
		printJSON(resp)
	}

	// posts create
	createCmd := cli.RegisterSubcommand(postsCmd, cli.SubcommandConfig{
		Name:  "create",
		Short: "Create and publish a post immediately",
	})
	var text string
	var pageIDs string
	createCmd.Flags().StringVar(&text, "text", "", "post text (required)")
	createCmd.Flags().StringVar(&pageIDs, "to", "", "comma-separated page IDs (required)")
	_ = createCmd.MarkFlagRequired("text")
	_ = createCmd.MarkFlagRequired("to")
	createCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		ids := parseIntList(pageIDs)
		resp, err := c.CreatePost(context.Background(), hooppy.PostPublishNowPayload{
			PublicationWhenType: 1,
			PublicationHowType:  1,
			SelectedPagesIDs:    ids,
			Texts:               []hooppy.PostText{{Text: text, SourceID: 0}},
		})
		die(err)
		printJSON(resp)
	}

	// posts delete
	deleteCmd := cli.RegisterSubcommand(postsCmd, cli.SubcommandConfig{
		Name:  "delete",
		Short: "Delete a post by ID",
	})
	deleteCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy posts delete <id>")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.DeletePost(context.Background(), id)
		die(err)
		printJSON(resp)
	}

	// posts edit — view a post's full editable state
	editPostCmd := cli.RegisterSubcommand(postsCmd, cli.SubcommandConfig{
		Name:  "edit",
		Short: "View a post's full editable state (texts, attachments, schedule)",
	})
	editPostCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy posts edit <id>")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		edit, err := c.GetPostEdit(context.Background(), id)
		die(err)
		printJSON(edit)
	}

	// posts update (undocumented) — two modes: text-only (preserve schedule+attachments) or full
	updateCmd := cli.RegisterSubcommand(postsCmd, cli.SubcommandConfig{
		Name:  "update",
		Short: "Update an existing post by ID (undocumented endpoint)",
	})
	var updText, updPageIDs string
	var updTextOnly bool
	updateCmd.Flags().StringVar(&updText, "text", "", "new post text (required)")
	updateCmd.Flags().StringVar(&updPageIDs, "to", "", "comma-separated page IDs (for publish-now mode)")
	updateCmd.Flags().BoolVar(&updTextOnly, "text-only", false, "change only the text, preserve schedule + attachments")
	_ = updateCmd.MarkFlagRequired("text")
	updateCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy posts update <id> --text=... [--text-only | --to=...]")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		if updTextOnly {
			// Safe mode: fetch current state, swap text, preserve everything else
			resp, err := c.UpdatePostText(context.Background(), id, updText)
			die(err)
			printJSON(resp)
			return
		}
		if updPageIDs == "" {
			fmt.Fprintln(os.Stderr, "error: --to is required unless --text-only is set")
			os.Exit(1)
		}
		// Legacy mode: publish now with new text (drops schedule)
		ids := parseIntList(updPageIDs)
		resp, err := c.UpdatePost(context.Background(), id, hooppy.PostPublishNowPayload{
			PublicationWhenType: 1,
			PublicationHowType:  1,
			SelectedPagesIDs:    ids,
			Texts:               []hooppy.PostText{{Text: updText, SourceID: 0}},
			Attachments:         []hooppy.Attachment{},
		})
		die(err)
		printJSON(resp)
	}

	// posts crosspost (undocumented) — alternative post creation modes
	crossPostCmd := cli.RegisterSubcommand(postsCmd, cli.SubcommandConfig{
		Name:  "crosspost",
		Short: "Create a post via an alternative mode (undocumented endpoints)",
	})
	var cpMode, cpText, cpPageIDs string
	crossPostCmd.Flags().StringVar(&cpMode, "mode", "", "cross-post mode: search, copy, sources, import, crosspost, rewrite, translate, queue, drafts, templates, rss, feeds, tags, watermarks, batch (required)")
	crossPostCmd.Flags().StringVar(&cpText, "text", "", "post text (required)")
	crossPostCmd.Flags().StringVar(&cpPageIDs, "to", "", "comma-separated page IDs (required)")
	_ = crossPostCmd.MarkFlagRequired("mode")
	_ = crossPostCmd.MarkFlagRequired("text")
	_ = crossPostCmd.MarkFlagRequired("to")
	crossPostCmd.Run = func(_ *cobra.Command, _ []string) {
		mode := hooppy.CrossPostMode(cpMode)
		// Validate mode
		validModes := map[hooppy.CrossPostMode]bool{
			hooppy.CrossPostModeSearch: true, hooppy.CrossPostModeCopy: true, hooppy.CrossPostModeSources: true,
			hooppy.CrossPostModeImport: true, hooppy.CrossPostModeCrossPost: true, hooppy.CrossPostModeRewrite: true,
			hooppy.CrossPostModeTranslate: true, hooppy.CrossPostModeQueue: true, hooppy.CrossPostModeDrafts: true,
			hooppy.CrossPostModeTemplates: true, hooppy.CrossPostModeRSS: true, hooppy.CrossPostModeFeeds: true,
			hooppy.CrossPostModeTags: true, hooppy.CrossPostModeWatermarks: true, hooppy.CrossPostModeBatch: true,
		}
		if !validModes[mode] {
			fmt.Fprintf(os.Stderr, "invalid mode %q; valid: search, copy, sources, import, crosspost, rewrite, translate, queue, drafts, templates, rss, feeds, tags, watermarks, batch\n", cpMode)
			os.Exit(1)
		}
		c := mustClient()
		ids := parseIntList(cpPageIDs)
		payload := hooppy.PostPublishNowPayload{
			PublicationWhenType: 1,
			PublicationHowType:  1,
			SelectedPagesIDs:    ids,
			Texts:               []hooppy.PostText{{Text: cpText, SourceID: 0}},
			Attachments:         []hooppy.Attachment{},
		}
		resp, err := c.CrossPostWithMode(context.Background(), mode, payload)
		die(err)
		printJSON(resp)
	}
}

// --- projects ---

func registerProjects(root *cobra.Command) {
	projectsCmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "projects",
		Short: "Manage post projects",
	})

	// projects list
	listCmd := cli.RegisterSubcommand(projectsCmd, cli.SubcommandConfig{
		Name:  "list",
		Short: "List post projects",
	})
	var projPage int
	var projAll bool
	listCmd.Flags().IntVar(&projPage, "page", 0, "page number, 1-indexed (0 or omit = first page)")
	listCmd.Flags().BoolVar(&projAll, "all", false, "fetch all pages (walks until is_has_more is false)")
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		if projAll {
			all, total, err := c.ListAllProjectsWithTotal(context.Background())
			die(err)
			env, err := hooppy.NewAllListEnvelope(all, total, func(p hooppy.Project) int { return p.ID })
			die(err)
			printJSON(env)
			return
		}
		resp, err := c.ListProjects(context.Background(), projPage)
		die(err)
		printJSON(resp)
	}

	// projects create (undocumented)
	createCmd := cli.RegisterSubcommand(projectsCmd, cli.SubcommandConfig{
		Name:  "create",
		Short: "Create a project (undocumented endpoint)",
	})
	var projName string
	var projPageID int
	createCmd.Flags().StringVar(&projName, "name", "", "project name (required)")
	createCmd.Flags().IntVar(&projPageID, "page", 0, "page ID to associate (required)")
	_ = createCmd.MarkFlagRequired("name")
	_ = createCmd.MarkFlagRequired("page")
	createCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.CreateProject(context.Background(), hooppy.NewProjectPayload(projName, projPageID))
		die(err)
		printJSON(resp)
	}

	// projects delete (undocumented)
	deleteCmd := cli.RegisterSubcommand(projectsCmd, cli.SubcommandConfig{
		Name:  "delete",
		Short: "Delete a project by ID (undocumented endpoint)",
	})
	deleteCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy projects delete <id>")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.DeleteProject(context.Background(), id)
		die(err)
		printJSON(resp)
	}

	// projects update (undocumented)
	projUpdateCmd := cli.RegisterSubcommand(projectsCmd, cli.SubcommandConfig{
		Name:  "update",
		Short: "Update a project name by ID (undocumented endpoint)",
	})
	var projUpdName string
	projUpdateCmd.Flags().StringVar(&projUpdName, "name", "", "new project name (required)")
	_ = projUpdateCmd.MarkFlagRequired("name")
	projUpdateCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy projects update <id> --name=...")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.UpdateProject(context.Background(), id, projUpdName)
		die(err)
		printJSON(resp)
	}
}

// --- schedules ---

func registerSchedules(root *cobra.Command) {
	schedulesCmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "schedules",
		Short: "Manage publication schedules",
	})

	// schedules list
	listCmd := cli.RegisterSubcommand(schedulesCmd, cli.SubcommandConfig{
		Name:  "list",
		Short: "List publication schedules",
	})
	var schedPage int
	var schedAll bool
	listCmd.Flags().IntVar(&schedPage, "page", 0, "page number, 1-indexed (0 or omit = first page)")
	listCmd.Flags().BoolVar(&schedAll, "all", false, "fetch all pages (walks until is_has_more is false)")
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		if schedAll {
			all, total, err := c.ListAllSchedulesWithTotal(context.Background())
			die(err)
			env, err := hooppy.NewAllListEnvelope(all, total, func(s hooppy.Schedule) int { return s.ID })
			die(err)
			printJSON(env)
			return
		}
		resp, err := c.ListSchedules(context.Background(), schedPage)
		die(err)
		printJSON(resp)
	}

	// schedules create (undocumented)
	createCmd := cli.RegisterSubcommand(schedulesCmd, cli.SubcommandConfig{
		Name:  "create",
		Short: "Create a schedule (undocumented endpoint)",
	})
	var schedName string
	createCmd.Flags().StringVar(&schedName, "name", "", "schedule name (required)")
	_ = createCmd.MarkFlagRequired("name")
	createCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.CreateSchedule(context.Background(), hooppy.NewSchedulePayload(schedName))
		die(err)
		printJSON(resp)
	}

	// schedules delete (undocumented)
	deleteCmd := cli.RegisterSubcommand(schedulesCmd, cli.SubcommandConfig{
		Name:  "delete",
		Short: "Delete a schedule by ID (undocumented endpoint)",
	})
	deleteCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy schedules delete <id>")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.DeleteSchedule(context.Background(), id)
		die(err)
		printJSON(resp)
	}

	// schedules update (undocumented)
	schedUpdateCmd := cli.RegisterSubcommand(schedulesCmd, cli.SubcommandConfig{
		Name:  "update",
		Short: "Update a schedule by ID (undocumented endpoint)",
	})
	var schedUpdName string
	var schedUpdState int
	schedUpdateCmd.Flags().StringVar(&schedUpdName, "name", "", "schedule name (required)")
	schedUpdateCmd.Flags().IntVar(&schedUpdState, "state", 1, "state: 1=active, 0=paused")
	_ = schedUpdateCmd.MarkFlagRequired("name")
	schedUpdateCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy schedules update <id> --name=... [--state=1]")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		payload := hooppy.NewSchedulePayload(schedUpdName)
		payload.State = schedUpdState
		resp, err := c.UpdateSchedule(context.Background(), id, payload)
		die(err)
		printJSON(resp)
	}
}

// --- files ---

func registerFiles(root *cobra.Command) {
	filesCmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "files",
		Short: "Upload files for posts",
	})

	uploadMedia := cli.RegisterSubcommand(filesCmd, cli.SubcommandConfig{
		Name:  "upload-media",
		Short: "Upload a photo or video file",
	})
	uploadMedia.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy files upload-media <path>")
			os.Exit(1)
		}
		c := mustClient()
		resp, err := c.UploadMedia(context.Background(), args[0], "")
		die(err)
		printJSON(resp)
	}

	uploadDoc := cli.RegisterSubcommand(filesCmd, cli.SubcommandConfig{
		Name:  "upload-document",
		Short: "Upload a document file",
	})
	uploadDoc.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy files upload-document <path>")
			os.Exit(1)
		}
		c := mustClient()
		resp, err := c.UploadDocument(context.Background(), args[0], "")
		die(err)
		printJSON(resp)
	}
}

// --- user (undocumented) ---

func registerUser(root *cobra.Command) {
	cmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "user",
		Short: "Get current user profile (undocumented endpoint)",
	})
	cmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.GetUser(context.Background())
		die(err)
		printJSON(resp)
	}
}

// --- watermarks (undocumented) ---

func registerWatermarks(root *cobra.Command) {
	wmCmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "watermarks",
		Short: "Manage watermarks (undocumented endpoints)",
	})

	// watermarks list
	listCmd := cli.RegisterSubcommand(wmCmd, cli.SubcommandConfig{
		Name:  "list",
		Short: "List watermarks",
	})
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListWatermarks(context.Background(), 0)
		die(err)
		printJSON(resp)
	}

	// watermarks create
	createCmd := cli.RegisterSubcommand(wmCmd, cli.SubcommandConfig{
		Name:  "create",
		Short: "Create a watermark",
	})
	var wmName, wmFile string
	var wmSpace, wmPosition, wmOpacity, wmSize int
	createCmd.Flags().StringVar(&wmName, "name", "", "watermark name (required)")
	createCmd.Flags().StringVar(&wmFile, "file", "", "file path")
	createCmd.Flags().IntVar(&wmSpace, "space", 0, "space")
	createCmd.Flags().IntVar(&wmPosition, "position", 0, "position")
	createCmd.Flags().IntVar(&wmOpacity, "opacity", 0, "opacity (0-100)")
	createCmd.Flags().IntVar(&wmSize, "size", 0, "size")
	_ = createCmd.MarkFlagRequired("name")
	createCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.CreateWatermark(context.Background(), hooppy.WatermarkPayload{
			Name: wmName, File: wmFile, Space: wmSpace, Position: wmPosition, Opacity: wmOpacity, Size: wmSize,
		})
		die(err)
		printJSON(resp)
	}

	// watermarks delete
	deleteCmd := cli.RegisterSubcommand(wmCmd, cli.SubcommandConfig{
		Name:  "delete",
		Short: "Delete a watermark by ID",
	})
	deleteCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy watermarks delete <id>")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.DeleteWatermark(context.Background(), id)
		die(err)
		printJSON(resp)
	}

	// watermarks update
	wmUpdateCmd := cli.RegisterSubcommand(wmCmd, cli.SubcommandConfig{
		Name:  "update",
		Short: "Update a watermark by ID",
	})
	var wmUpdName, wmUpdFile string
	var wmUpdSpace, wmUpdPosition, wmUpdOpacity, wmUpdSize int
	wmUpdateCmd.Flags().StringVar(&wmUpdName, "name", "", "watermark name")
	wmUpdateCmd.Flags().StringVar(&wmUpdFile, "file", "", "file path")
	wmUpdateCmd.Flags().IntVar(&wmUpdSpace, "space", 0, "space")
	wmUpdateCmd.Flags().IntVar(&wmUpdPosition, "position", 0, "position")
	wmUpdateCmd.Flags().IntVar(&wmUpdOpacity, "opacity", 0, "opacity (0-100)")
	wmUpdateCmd.Flags().IntVar(&wmUpdSize, "size", 0, "size")
	wmUpdateCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy watermarks update <id> [--name=...] [--file=...] ...")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.UpdateWatermark(context.Background(), id, hooppy.WatermarkPayload{
			Name: wmUpdName, File: wmUpdFile, Space: wmUpdSpace, Position: wmUpdPosition, Opacity: wmUpdOpacity, Size: wmUpdSize,
		})
		die(err)
		printJSON(resp)
	}
}

// --- proxies (undocumented) ---

func registerProxies(root *cobra.Command) {
	proxyCmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "proxies",
		Short: "Manage proxy servers (undocumented endpoints)",
	})

	// proxies list
	listCmd := cli.RegisterSubcommand(proxyCmd, cli.SubcommandConfig{
		Name:  "list",
		Short: "List proxies",
	})
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListProxies(context.Background())
		die(err)
		printJSON(resp)
	}

	// proxies create
	createCmd := cli.RegisterSubcommand(proxyCmd, cli.SubcommandConfig{
		Name:  "create",
		Short: "Create a proxy",
	})
	var pName, pIP, pPort, pLogin, pPassword string
	createCmd.Flags().StringVar(&pName, "name", "", "proxy name")
	createCmd.Flags().StringVar(&pIP, "ip", "", "IP address (required)")
	createCmd.Flags().StringVar(&pPort, "port", "", "port (required)")
	createCmd.Flags().StringVar(&pLogin, "login", "", "login")
	createCmd.Flags().StringVar(&pPassword, "password", "", "password")
	_ = createCmd.MarkFlagRequired("ip")
	_ = createCmd.MarkFlagRequired("port")
	createCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.CreateProxy(context.Background(), hooppy.ProxyPayload{
			Name: pName, IP: pIP, Port: pPort, Login: pLogin, Password: pPassword,
		})
		die(err)
		printJSON(resp)
	}

	// proxies delete
	deleteCmd := cli.RegisterSubcommand(proxyCmd, cli.SubcommandConfig{
		Name:  "delete",
		Short: "Delete a proxy by ID",
	})
	deleteCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy proxies delete <id>")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.DeleteProxy(context.Background(), id)
		die(err)
		printJSON(resp)
	}

	// proxies update
	proxyUpdateCmd := cli.RegisterSubcommand(proxyCmd, cli.SubcommandConfig{
		Name:  "update",
		Short: "Update a proxy by ID",
	})
	var pUpdName, pUpdIP, pUpdPort, pUpdLogin, pUpdPassword string
	proxyUpdateCmd.Flags().StringVar(&pUpdName, "name", "", "proxy name")
	proxyUpdateCmd.Flags().StringVar(&pUpdIP, "ip", "", "IP address")
	proxyUpdateCmd.Flags().StringVar(&pUpdPort, "port", "", "port")
	proxyUpdateCmd.Flags().StringVar(&pUpdLogin, "login", "", "login")
	proxyUpdateCmd.Flags().StringVar(&pUpdPassword, "password", "", "password")
	proxyUpdateCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy proxies update <id> [--name=...] [--ip=...] ...")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
		resp, err := c.UpdateProxy(context.Background(), id, hooppy.ProxyPayload{
			Name: pUpdName, IP: pUpdIP, Port: pUpdPort, Login: pUpdLogin, Password: pUpdPassword,
		})
		die(err)
		printJSON(resp)
	}
}

// --- notifications (undocumented) ---

func registerNotifications(root *cobra.Command) {
	cmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "notifications",
		Short: "List publication status notifications (undocumented endpoint)",
	})
	cmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListNotifications(context.Background(), 0)
		die(err)
		printJSON(resp)
	}
}

// --- mcp-config ---

func registerMCPConfig(root *cobra.Command) {
	cmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "mcp-config",
		Short: "Print the claude mcp add command for hooppy-mcp",
	})
	cmd.Run = func(_ *cobra.Command, _ []string) {
		fmt.Println("# Add the Hooppy MCP server to Claude Code:")
		fmt.Println("claude mcp add hooppy --transport stdio -- hooppy-mcp")
		fmt.Println()
		fmt.Println("# Or via HTTP (if running hooppy-mcp without --stdio):")
		cli.PrintMCPConfig("hooppy", "http://localhost:8080/mcp", "http")
	}
}

func registerSearch(root *cobra.Command) {
	searchCmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "search",
		Short: "Search and scrape posts from external social media pages",
	})

	// search sources
	sourcesCmd := cli.RegisterSubcommand(searchCmd, cli.SubcommandConfig{
		Name:  "sources",
		Short: "List configured source resources (external pages to scrape from)",
	})
	sourcesCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListSourceResources(context.Background())
		die(err)
		printJSON(resp)
	}

	// search posts
	postsCmd := cli.RegisterSubcommand(searchCmd, cli.SubcommandConfig{
		Name:  "posts",
		Short: "List scraped posts from external pages",
	})
	var sText, sDateFrom, sDateTo, sSortBy, sSortDir, sContentTypes, sContentTypesExclude string
	var sSourceType, sSourceID, sSourceResourceID, sOwnerID, sPage, sMinLikes, sMinViews, sMinComments, sMinReposts, sPhotosAmount int
	var sMinInvolvement float64
	postsCmd.Flags().StringVar(&sText, "text", "", "search by text")
	postsCmd.Flags().StringVar(&sDateFrom, "date-from", "", "filter by date from (dd.mm.yyyy)")
	postsCmd.Flags().StringVar(&sDateTo, "date-to", "", "filter by date to (dd.mm.yyyy)")
	postsCmd.Flags().IntVar(&sSourceType, "source-type", 0, "source type: 1=social, 2=RSS")
	postsCmd.Flags().IntVar(&sSourceID, "source-id", 0, "social network ID (1=VK, 7=Instagram, etc.)")
	postsCmd.Flags().IntVar(&sSourceResourceID, "source-resource-id", 0, "source resource ID (see 'search sources')")
	postsCmd.Flags().IntVar(&sOwnerID, "owner-id", 0, "page ID within source")
	postsCmd.Flags().IntVar(&sPage, "page", 0, "pagination page number")
	postsCmd.Flags().StringVar(&sSortBy, "sort-by", "", "sort field: publication_date, likes, reposts, comments, views, involvement")
	postsCmd.Flags().StringVar(&sSortDir, "sort-dir", "desc", "sort direction: desc (default) or asc")
	postsCmd.Flags().IntVar(&sMinLikes, "min-likes", 0, "minimum likes")
	postsCmd.Flags().IntVar(&sMinViews, "min-views", 0, "minimum views")
	postsCmd.Flags().IntVar(&sMinComments, "min-comments", 0, "minimum comments")
	postsCmd.Flags().IntVar(&sMinReposts, "min-reposts", 0, "minimum reposts")
	postsCmd.Flags().Float64Var(&sMinInvolvement, "min-involvement", 0, "minimum involvement (e.g. 10.5)")
	postsCmd.Flags().IntVar(&sPhotosAmount, "photos-amount", 0, "exact photo count")
	postsCmd.Flags().StringVar(&sContentTypes, "content-types", "", "comma-separated content types to include: photos, videos, audios, documents, links")
	postsCmd.Flags().StringVar(&sContentTypesExclude, "content-types-exclude", "", "comma-separated content types to exclude")
	postsCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListSearchPosts(context.Background(), hooppy.SearchPostsFilter{
			Text:                sText,
			DateFrom:            sDateFrom,
			DateTo:              sDateTo,
			SourceType:          sSourceType,
			SourceID:            sSourceID,
			SourceResourceID:    sSourceResourceID,
			OwnerID:             sOwnerID,
			Page:                sPage,
			SortBy:              sSortBy,
			SortDirection:       sSortDir,
			MinLikes:            sMinLikes,
			MinViews:            sMinViews,
			MinComments:         sMinComments,
			MinReposts:          sMinReposts,
			MinInvolvement:      sMinInvolvement,
			PhotosAmount:        sPhotosAmount,
			ContentTypes:        sContentTypes,
			ContentTypesExclude: sContentTypesExclude,
		})
		die(err)
		printJSON(resp)
	}

	// search status
	statusCmd := cli.RegisterSubcommand(searchCmd, cli.SubcommandConfig{
		Name:  "status",
		Short: "Show parsing status (in-progress or not)",
	})
	statusCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.GetParsingForm(context.Background())
		die(err)
		printJSON(resp)
	}

	// search parse
	parseCmd := cli.RegisterSubcommand(searchCmd, cli.SubcommandConfig{
		Name:  "parse",
		Short: "Start scraping posts from an external source resource",
	})
	var pSourceType, pSearchType, pSourceID, pSourceResourceID, pAccountID, pDateFrom, pDateTo int
	parseCmd.Flags().IntVar(&pSourceType, "source-type", 1, "source type: 1=social, 2=RSS")
	parseCmd.Flags().IntVar(&pSearchType, "search-type", 1, "search method: 1=pages, 2=hashtag")
	parseCmd.Flags().IntVar(&pSourceID, "source-id", 1, "social network ID (1=VK, 7=Instagram, etc.)")
	parseCmd.Flags().IntVar(&pSourceResourceID, "source-resource-id", 0, "source resource ID (REQUIRED, see 'search sources')")
	parseCmd.Flags().IntVar(&pAccountID, "account-id", 0, "social account ID to use as parser (see 'search status')")
	parseCmd.Flags().IntVar(&pDateFrom, "date-from", 0, "unix timestamp, 0=any")
	parseCmd.Flags().IntVar(&pDateTo, "date-to", 0, "unix timestamp, 0=any")
	parseCmd.Run = func(_ *cobra.Command, _ []string) {
		if pSourceResourceID == 0 {
			fmt.Fprintln(os.Stderr, "error: --source-resource-id is required (see 'hooppy search sources')")
			os.Exit(1)
		}
		c := mustClient()
		resp, err := c.StartParsing(context.Background(), hooppy.ParsingStartPayload{
			SourceType:                pSourceType,
			SearchType:                pSearchType,
			SourceID:                  pSourceID,
			SourceResourceID:          pSourceResourceID,
			SocialAccountForParsingID: pAccountID,
			DateFrom:                  pDateFrom,
			DateTo:                    pDateTo,
		})
		die(err)
		printJSON(resp)
	}

	// search stop
	stopCmd := cli.RegisterSubcommand(searchCmd, cli.SubcommandConfig{
		Name:  "stop",
		Short: "Stop any in-progress scraping job",
	})
	stopCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		err := c.StopParsing(context.Background())
		die(err)
		fmt.Println(`{"success":true}`)
	}

	// search copy
	copyCmd := cli.RegisterSubcommand(searchCmd, cli.SubcommandConfig{
		Name:  "copy",
		Short: "Copy a scraped post to your own pages (auto-fills text + photos from the scraped post)",
	})
	var copyPostID int
	var copyPages string
	var copyWhenType, copyHowType int
	var copySchedules string
	var copyDate, copyHours, copyMinutes string
	copyCmd.Flags().IntVar(&copyPostID, "post-id", 0, "scraped post ID from 'search posts' (REQUIRED)")
	copyCmd.Flags().StringVar(&copyPages, "to", "", "comma-separated page IDs to publish to (for when-type 1 or 2)")
	copyCmd.Flags().IntVar(&copyWhenType, "when-type", 1, "1=publish now, 2=at specific time, 3=by schedule")
	copyCmd.Flags().IntVar(&copyHowType, "how-type", 1, "publication how type (1=default)")
	copyCmd.Flags().StringVar(&copySchedules, "schedules", "", "comma-separated schedule IDs (for when-type 3)")
	copyCmd.Flags().StringVar(&copyDate, "date", "", "publication date dd.mm.yyyy (for when-type 2)")
	copyCmd.Flags().StringVar(&copyHours, "hours", "", "publication hours HH (for when-type 2)")
	copyCmd.Flags().StringVar(&copyMinutes, "minutes", "", "publication minutes MM (for when-type 2)")
	copyCmd.Run = func(_ *cobra.Command, _ []string) {
		if copyPostID == 0 {
			fmt.Fprintln(os.Stderr, "error: --post-id is required (see 'hooppy search posts')")
			os.Exit(1)
		}
		if copyWhenType == 2 && (copyDate == "" || copyHours == "" || copyMinutes == "") {
			fmt.Fprintln(os.Stderr, "error: --date, --hours, --minutes are required for --when-type 2")
			os.Exit(1)
		}
		if copyWhenType == 3 && len(parseIntList(copySchedules)) == 0 {
			fmt.Fprintln(os.Stderr, "error: --schedules is required for --when-type 3 (by schedule) — a schedule-driven copy targeted at no schedule publishes to nothing")
			os.Exit(1)
		}
		c := mustClient()
		payload := hooppy.CopySearchPostPayload{
			SearchPostID:        copyPostID,
			PublicationWhenType: copyWhenType,
			PublicationHowType:  copyHowType,
		}
		switch copyWhenType {
		case 3:
			payload.SchedulesIDs = parseIntList(copySchedules)
		case 2:
			payload.SelectedPagesIDs = parseIntList(copyPages)
			payload.PublicationDate = &hooppy.PublicationDate{
				Date:    copyDate,
				Hours:   copyHours,
				Minutes: copyMinutes,
			}
		default:
			payload.SelectedPagesIDs = parseIntList(copyPages)
		}
		resp, err := c.CopySearchPost(context.Background(), payload)
		die(err)
		printJSON(resp)
	}

	// search rewrite
	rewriteCmd := cli.RegisterSubcommand(searchCmd, cli.SubcommandConfig{
		Name:  "rewrite",
		Short: "Rewrite a scraped post with custom text and publish to your pages",
	})
	var rwPostID int
	var rwText, rwPages, rwSchedules string
	var rwWhenType, rwHowType int
	var rwDate, rwHours, rwMinutes string
	var rwNoAttachments bool
	rewriteCmd.Flags().IntVar(&rwPostID, "post-id", 0, "scraped post ID from 'search posts' (REQUIRED)")
	rewriteCmd.Flags().StringVar(&rwText, "text", "", "new text for the post (REQUIRED)")
	rewriteCmd.Flags().StringVar(&rwPages, "to", "", "comma-separated page IDs to publish to (for when-type 1 or 2)")
	rewriteCmd.Flags().IntVar(&rwWhenType, "when-type", 1, "1=publish now, 2=at specific time, 3=by schedule")
	rewriteCmd.Flags().IntVar(&rwHowType, "how-type", 1, "publication how type (1=default)")
	rewriteCmd.Flags().StringVar(&rwSchedules, "schedules", "", "comma-separated schedule IDs (for when-type 3)")
	rewriteCmd.Flags().StringVar(&rwDate, "date", "", "publication date dd.mm.yyyy (for when-type 2)")
	rewriteCmd.Flags().StringVar(&rwHours, "hours", "", "publication hours HH (for when-type 2)")
	rewriteCmd.Flags().StringVar(&rwMinutes, "minutes", "", "publication minutes MM (for when-type 2)")
	rewriteCmd.Flags().BoolVar(&rwNoAttachments, "no-attachments", false, "strip all attachments (photos, links, etc.) from the scraped post")
	rewriteCmd.Run = func(_ *cobra.Command, _ []string) {
		if rwPostID == 0 {
			fmt.Fprintln(os.Stderr, "error: --post-id is required (see 'hooppy search posts')")
			os.Exit(1)
		}
		if rwText == "" {
			fmt.Fprintln(os.Stderr, "error: --text is required")
			os.Exit(1)
		}
		if rwWhenType == 2 && (rwDate == "" || rwHours == "" || rwMinutes == "") {
			fmt.Fprintln(os.Stderr, "error: --date, --hours, --minutes are required for --when-type 2")
			os.Exit(1)
		}
		if rwWhenType == 3 && len(parseIntList(rwSchedules)) == 0 {
			fmt.Fprintln(os.Stderr, "error: --schedules is required for --when-type 3 (by schedule) — a schedule-driven rewrite targeted at no schedule publishes to nothing")
			os.Exit(1)
		}
		c := mustClient()
		payload := hooppy.CopySearchPostPayload{
			SearchPostID:        rwPostID,
			PublicationWhenType: rwWhenType,
			PublicationHowType:  rwHowType,
			Texts:               []hooppy.PostText{{Text: rwText, SourceID: 0}},
		}
		switch rwWhenType {
		case 3:
			payload.SchedulesIDs = parseIntList(rwSchedules)
		case 2:
			payload.SelectedPagesIDs = parseIntList(rwPages)
			payload.PublicationDate = &hooppy.PublicationDate{
				Date:    rwDate,
				Hours:   rwHours,
				Minutes: rwMinutes,
			}
		default:
			payload.SelectedPagesIDs = parseIntList(rwPages)
		}
		if !rwNoAttachments {
			// By default, preserve ALL attachments from the scraped post:
			// - Photos: download from edit endpoint URLs → re-upload via UploadMedia
			//   (server doesn't download automatically; MediaItem must have id/name/folder/file_path)
			// - Other attachments (copyright, link, poll, etc.): pass through as-is
			edit, err := c.GetSearchPostEdit(context.Background(), rwPostID)
			die(err)
			var mediaItems []interface{}
			var attachments []hooppy.Attachment
			for i, att := range edit.Attachments {
				if att.Type != "photo" && att.Type != "video" {
					// Non-photo attachment — pass through as-is
					attachments = append(attachments, att)
					continue
				}
				// Extract URL from the attachment data
				data, ok := att.Data.(map[string]interface{})
				if !ok {
					continue
				}
				photoURL, _ := data["url"].(string)
				if photoURL == "" {
					continue
				}
				// Download photo
				tmpPath := fmt.Sprintf("/tmp/hooppy_photo_%d_%d.jpg", rwPostID, i)
				if err := downloadPhoto(photoURL, tmpPath); err != nil {
					fmt.Fprintf(os.Stderr, "error: download photo %d: %v\n", i, err)
					os.Exit(1)
				}
				// Upload with generated file_id (server uses it as id + name)
				media, err := c.UploadMedia(context.Background(), tmpPath, "")
				die(err)
				mediaItems = append(mediaItems, media.Photo)
				os.Remove(tmpPath)
			}
			if len(mediaItems) > 0 {
				attachments = append([]hooppy.Attachment{{Type: "photos", Data: mediaItems}}, attachments...)
			}
			if len(attachments) > 0 {
				payload.Attachments = attachments
			}
		}
		resp, err := c.RewriteSearchPost(context.Background(), payload)
		die(err)
		printJSON(resp)
	}

	// search import — copy one scraped post via PUT /posts/import with full text + attachments
	importCmd := cli.RegisterSubcommand(searchCmd, cli.SubcommandConfig{
		Name:  "import",
		Short: "Copy a scraped post with full text + photos/videos via PUT /posts/import (server downloads photos async)",
	})
	var impPostID int
	var impSchedules string
	var impWhenType, impHowType int
	var impNoAttachments bool
	importCmd.Flags().IntVar(&impPostID, "post-id", 0, "scraped post ID from 'search posts' (REQUIRED)")
	importCmd.Flags().IntVar(&impWhenType, "when-type", 3, "1=publish now, 2=at specific time, 3=by schedule")
	importCmd.Flags().IntVar(&impHowType, "how-type", 2, "publication how type (2=by schedule pages)")
	importCmd.Flags().StringVar(&impSchedules, "schedules", "", "comma-separated schedule IDs (for when-type 3)")
	importCmd.Flags().BoolVar(&impNoAttachments, "no-attachments", false, "strip all attachments (photos, videos, links, etc.)")
	importCmd.Run = func(_ *cobra.Command, _ []string) {
		if impPostID == 0 {
			fmt.Fprintln(os.Stderr, "error: --post-id is required (see 'hooppy search posts')")
			os.Exit(1)
		}
		if impWhenType == 3 && len(parseIntList(impSchedules)) == 0 {
			fmt.Fprintln(os.Stderr, "error: --schedules is required for --when-type 3 (by schedule) — a schedule-driven import targeted at no schedule publishes to nothing")
			os.Exit(1)
		}
		c := mustClient()
		// Get edit data for text + attachments
		edit, err := c.GetSearchPostEdit(context.Background(), impPostID)
		die(err)
		// Extract original text
		text := ""
		if len(edit.Texts) > 0 {
			text = edit.Texts[0].Text
		}
		// Build attachments — photos AND videos grouped into {type: "photos"} (UI behavior)
		var attachments []hooppy.Attachment
		if !impNoAttachments {
			attachments = hooppy.SearchPostEditAttachments(edit.Attachments)
		}
		payload := hooppy.CopySearchPostPayload{
			SearchPostID:        impPostID,
			PublicationWhenType: impWhenType,
			PublicationHowType:  impHowType,
			SchedulesIDs:        parseIntList(impSchedules),
			Texts:               []hooppy.PostText{{Text: text, SourceID: 0}},
			Attachments:         attachments,
		}
		resp, err := c.ImportSearchPost(context.Background(), payload)
		die(err)
		printJSON(resp)
	}
}

func parseIntList(s string) []int {
	parts := strings.Split(s, ",")
	var ids []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid page ID %q: %v\n", p, err)
			os.Exit(1)
		}
		ids = append(ids, n)
	}
	return ids
}

func downloadPhoto(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// --- doctor ---

func registerDoctor(root *cobra.Command) {
	cmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "doctor",
		Short: "Diagnose broken connections from the notification log (read-only)",
	})
	var sinceDays int
	var exitCode bool
	cmd.Flags().IntVar(&sinceDays, "since", 7, "only report errors whose operation_date falls within the last N days. NOTE: the window is computed in the HOST's local timezone (time.Now), but the vendor renders operation_date in the ACCOUNT's timezone (a user setting on hooppy.ru, not exposed by the API). If the two differ, the window boundary can be off by the offset between them — a row the account considers inside the window may be excluded, or vice versa, by up to that offset.")
	cmd.Flags().BoolVar(&exitCode, "exit-code", true, "exit 1 if any error falls inside the --since window (grouped errors, unparseable-date rows, or a truncated walk), 0 otherwise (for cron / pre-flight)")
	cmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		os.Exit(runDoctor(context.Background(), c, os.Stdout, os.Stderr, sinceDays, exitCode))
	}
}

// runDoctor is the testable core of the `hooppy doctor` command. It runs
// the doctor report, prints it as JSON to out, and returns the process exit
// code: 1 if exitCode is true AND any error signal is present (grouped
// errors, unparseable-date rows, or a truncated walk), 0 otherwise. A
// RunDoctor error is printed to errOut and returns 1.
//
// The exit-code gate covers THREE error signals, not just grouped errors:
//   - len(Groups) > 0           — classified errors inside the window
//   - len(UnparseableRows) > 0  — error rows whose operation_date failed to
//     parse (a vendor date-format drift puts
//     every error row here; gating only on
//     Groups would read exit 0 over undiagnosed
//     publication failures)
//   - WalkIncomplete            — a truncated notifications or pages walk
//     (a row skipped by a mid-walk offset shift
//     is invisible; doctor is the one command
//     whose purpose is not missing a failure)
func runDoctor(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, sinceDays int, exitCode bool) int {
	report, err := c.RunDoctor(ctx, sinceDays)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	if exitCode && (len(report.Groups) > 0 || len(report.UnparseableRows) > 0 || report.WalkIncomplete) {
		return 1
	}
	return 0
}
