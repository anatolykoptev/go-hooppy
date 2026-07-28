package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	registerMCPConfig(root)

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
	var unpublished bool
	listCmd.Flags().BoolVar(&unpublished, "unpublished", false, "show only unpublished posts")
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		var isPub *bool
		if unpublished {
			f := false
			isPub = &f
		}
		resp, err := c.ListPosts(context.Background(), hooppy.ListPostsFilter{IsPublished: isPub})
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

	// posts update (undocumented)
	updateCmd := cli.RegisterSubcommand(postsCmd, cli.SubcommandConfig{
		Name:  "update",
		Short: "Update an existing post by ID (undocumented endpoint)",
	})
	var updText, updPageIDs string
	updateCmd.Flags().StringVar(&updText, "text", "", "post text (required)")
	updateCmd.Flags().StringVar(&updPageIDs, "to", "", "comma-separated page IDs (required)")
	_ = updateCmd.MarkFlagRequired("text")
	_ = updateCmd.MarkFlagRequired("to")
	updateCmd.Run = func(_ *cobra.Command, args []string) {
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: hooppy posts update <id> --text=... --to=...")
			os.Exit(1)
		}
		id, err := strconv.Atoi(args[0])
		die(err)
		c := mustClient()
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
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListProjects(context.Background(), 0)
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
	listCmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListSchedules(context.Background(), 0)
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
