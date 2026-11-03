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

// runScheduleUpdate is the thin CLI adapter for `hooppy schedules update`.
// All validation, presence semantics and request building live in
// ops.UpdateSchedule (the shared layer with the MCP tool); this function
// only shapes I/O: a *ops.ValidationError prints as a usage-style refusal
// under the command name, an API/transport error prints as a plain error,
// and the response encodes as indented JSON. Returns the process exit code
// (0 on success, 1 on any refusal/error) and never calls os.Exit itself.
func runScheduleUpdate(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, id int, in ops.UpdateScheduleInput) int {
	if id == 0 {
		fmt.Fprintln(errOut, "hooppy: schedules update: id is required (got 0)")
		return 1
	}
	resp, err := ops.UpdateSchedule(ctx, c, id, in)
	if err != nil {
		var ve *ops.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintf(errOut, "hooppy: schedules update: %s\n", err)
		} else {
			fmt.Fprintf(errOut, "error: %v\n", err)
		}
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(resp); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	return 0
}
