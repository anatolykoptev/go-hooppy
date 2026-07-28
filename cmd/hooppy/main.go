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
	cmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "pages",
		Short: "List connected groups/pages",
	})
	var sourceID, accountID int
	cmd.Flags().IntVar(&sourceID, "source", 0, "filter by social network source ID")
	cmd.Flags().IntVar(&accountID, "account", 0, "filter by account ID")
	cmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListPages(context.Background(), hooppy.ListPagesFilter{SourceID: sourceID, AccountID: accountID})
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
}

// --- projects ---

func registerProjects(root *cobra.Command) {
	cmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "projects",
		Short: "List post projects",
	})
	cmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListProjects(context.Background(), 0)
		die(err)
		printJSON(resp)
	}
}

// --- schedules ---

func registerSchedules(root *cobra.Command) {
	cmd := cli.RegisterSubcommand(root, cli.SubcommandConfig{
		Name:  "schedules",
		Short: "List publication schedules",
	})
	cmd.Run = func(_ *cobra.Command, _ []string) {
		c := mustClient()
		resp, err := c.ListSchedules(context.Background(), 0)
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
