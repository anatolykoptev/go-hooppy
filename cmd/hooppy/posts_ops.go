package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/spf13/cobra"
)

// deleteInputFromCmd reads a parsed `posts delete` command into the op input.
func deleteInputFromCmd(cmd *cobra.Command, args []string) (in ops.DeletePostsInput, err error) {
	if err = FillOpInput(cmd, &in); err != nil {
		return in, err
	}
	in.IDs, _, err = postIDsFromArgs(cmd, args, in.IDs)
	return in, err
}

// runDeletePosts is the thin CLI adapter for `hooppy posts delete`. All
// validation and the per-id outcomes live in ops.DeletePosts (shared with the
// MCP tools); this only shapes I/O. Exit codes follow the repo convention:
// 0 = every id deleted, 1 = error (a refusal before any request, nothing
// deleted, or an unknown outcome; the per-id record of failures still goes to
// stdout), 2 = partial (some deleted, some failed — the full per-id record goes
// to stdout so a re-run can skip what is gone).
// Never calls os.Exit.
func runDeletePosts(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, in ops.DeletePostsInput) int {
	res, err := ops.DeletePosts(ctx, c, in)
	if err != nil {
		var pde *ops.PartialDeleteError
		if errors.As(err, &pde) {
			// The stdout record IS the operator's record of what was deleted;
			// if it cannot be written, exit 1 rather than a record-less 2.
			if encErr := encodeJSON(out, res); encErr != nil {
				fmt.Fprintf(errOut, "error encoding output: %v\n", encErr)
				return 1
			}
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 2
		}
		// Nothing was deleted, or one id failed: the per-id record still goes to
		// stdout (the MCP tool carries it too) so the failing ids and their
		// server errors are not lost. A refusal before any request has none.
		if res != nil && (len(res.Failed) > 0 || len(res.AlreadyGone) > 0) {
			if encErr := encodeJSON(out, res); encErr != nil {
				fmt.Fprintf(errOut, "error encoding output: %v\n", encErr)
			}
		}
		var ve *ops.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintf(errOut, "hooppy: posts delete: %s\n", err)
		} else {
			fmt.Fprintf(errOut, "error: %v\n", err)
		}
		return 1
	}
	if err := encodeJSON(out, res); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}

// runGetPostEdit is the thin CLI adapter for `hooppy posts edit <post-id>`:
// ops.GetPostEdit prints the post's full editable state as JSON.
func runGetPostEdit(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, id int) int {
	edit, err := ops.GetPostEdit(ctx, c, id, ops.GetPostEditInput{})
	if err != nil {
		var ve *ops.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintf(errOut, "hooppy: posts edit: %s\n", err)
		} else {
			fmt.Fprintf(errOut, "error: %v\n", err)
		}
		return 1
	}
	if err := encodeJSON(out, edit); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}

func encodeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
