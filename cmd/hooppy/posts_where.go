package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
)

// runPostsWhere is the thin CLI adapter for `hooppy posts where <id>`. All
// validation and the page/URL join live in ops.PostWhere (shared with the
// MCP tool); this only shapes I/O. Returns the process exit code and never
// calls os.Exit itself.
func runPostsWhere(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, id int, in ops.PostWhereInput) int {
	res, err := ops.PostWhere(ctx, c, id, in)
	if err != nil {
		var ve *ops.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintf(errOut, "hooppy: posts where: %s\n", err)
		} else {
			fmt.Fprintf(errOut, "error: %v\n", err)
		}
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}
