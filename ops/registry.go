package ops

import (
	"fmt"
	"reflect"
	"strings"
)

// Entry describes one operation shared by the CLI and the MCP server. Both
// surfaces register FROM the entry: the CLI command name comes from
// CLILeaf, the flag set from Input's `flag` tags, the MCP tool name from
// MCPTool and the tool input schema from Input's `json`/`jsonschema` tags.
type Entry struct {
	Name    string // operation name, e.g. "UpdateSchedule" — the stable lookup key
	CLIPath string // space-separated command path from the root, e.g. "schedules update"
	MCPTool string // MCP tool name, e.g. "hooppy_update_schedule"
	// MCPSingle names a single-id convenience tool ({id} instead of {ids})
	// that adapts the same op, "" when there is none. It must be registered
	// on the server; it is not counted as unmigrated.
	MCPSingle string
	Input     reflect.Type // the ops input struct type carrying the field tags
}

// Registry lists every operation migrated onto the ops layer. Operations
// not yet migrated remain registered directly on each surface; the parity
// tests count them as the migration backlog and fail only if a registry
// entry does not resolve on BOTH surfaces.
var Registry = []Entry{
	{
		Name:    "UpdateSchedule",
		CLIPath: "schedules update",
		MCPTool: "hooppy_update_schedule",
		Input:   reflect.TypeOf(UpdateScheduleInput{}),
	},
	{
		Name:    "PostWhere",
		CLIPath: "posts where",
		MCPTool: "hooppy_post_where",
		Input:   reflect.TypeOf(PostWhereInput{}),
	},
	{
		Name:    "CopySearchPost",
		CLIPath: "search copy",
		MCPTool: "hooppy_copy_search_post",
		Input:   reflect.TypeOf(CopySearchPostInput{}),
	},
	{
		Name:    "RewriteSearchPost",
		CLIPath: "search rewrite",
		MCPTool: "hooppy_rewrite_search_post",
		Input:   reflect.TypeOf(RewriteSearchPostInput{}),
	},
	{
		Name:    "ImportSearchPost",
		CLIPath: "search import",
		MCPTool: "hooppy_import_search_post",
		Input:   reflect.TypeOf(ImportSearchPostInput{}),
	},
	{
		Name:      "DeletePosts",
		CLIPath:   "posts delete",
		MCPTool:   "hooppy_batch_delete_posts",
		MCPSingle: "hooppy_delete_post",
		Input:     reflect.TypeOf(DeletePostsInput{}),
	},
	{
		Name:      "MovePosts",
		CLIPath:   "posts move",
		MCPTool:   "hooppy_batch_move_posts",
		MCPSingle: "hooppy_move_post",
		Input:     reflect.TypeOf(MovePostsInput{}),
	},
	{
		Name:    "GetPostEdit",
		CLIPath: "posts edit",
		MCPTool: "hooppy_get_post_edit",
		Input:   reflect.TypeOf(GetPostEditInput{}),
	},
}

// CLILeaf returns the last word of the entry's CLI path — the subcommand
// name registered under its parent group.
func (e Entry) CLILeaf() string {
	parts := strings.Fields(e.CLIPath)
	return parts[len(parts)-1]
}

// MustEntry returns the registry entry for the named operation. It panics
// when absent: a surface registering an op that is not in the registry is
// a build-time bug and must fail loudly at startup, not silently no-op
// (the parity tests are the slower, friendlier detector of the same drift).
func MustEntry(name string) Entry {
	for _, e := range Registry {
		if e.Name == name {
			return e
		}
	}
	panic(fmt.Sprintf("ops: no registry entry named %q", name))
}
