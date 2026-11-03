package ops

import (
	"context"
	"strings"

	"github.com/anatolykoptev/go-hooppy"
)

// UpdateScheduleInput is the single source of the `schedules update` /
// hooppy_update_schedule parameter set. Every field is optional and a
// pointer: nil means "not given" (the key is not sent), a pointed-at zero
// is a real value (delete_posts_day=0 disables auto-delete,
// delete_posts_hour=0 is midnight). The `flag`/`flagusage` tags drive the
// CLI's flag binder, `json`/`jsonschema` drive the MCP input schema.
type UpdateScheduleInput struct {
	Name            *string `json:"name,omitempty" jsonschema:"New schedule name. Omit to leave unchanged." flag:"name" flagusage:"schedule name (applied via read-modify-write; all other fields preserved)"`
	State           *int    `json:"state,omitempty" jsonschema:"Schedule state: 1=active, 2=deferred, 3=stopped. Omit to leave unchanged; 0 is NOT a valid state." flag:"state" flagusage:"state override: 1=active, 2=deferred, 3=stopped (unset = leave unchanged)"`
	DeletePostsDay  *int    `json:"delete_posts_day,omitempty" jsonschema:"Auto-delete posts this many days after publication (0 disables auto-delete). Omit to leave unchanged." flag:"delete-after-days" flagusage:"auto-delete posts this many days after publication (0 = disable; only sent when the flag is given)"`
	DeletePostsHour *int    `json:"delete_posts_hour,omitempty" jsonschema:"Auto-delete hour within the day, 0..23 (0 = midnight). Omit to leave unchanged." flag:"delete-after-hours" flagusage:"auto-delete hour within the day, 0..23 (only sent when the flag is given)"`
}

// UpdateSchedule is the one implementation of "edit a schedule, preserving
// every field not explicitly given" for both surfaces. It validates the
// input locally (refusing before any request is built), turns the given
// fields into overrides, and routes through UpdateScheduleFromEdit's
// read-modify-write — NOT the raw UpdateSchedule partial writer, which
// drops 36 of 72 fields the server carries on /edit.
//
// All validation lives here and nowhere else: at least one field given,
// a given name non-empty after trimming ("" is a real value on the wire —
// it would overwrite the schedule's name, not leave it unchanged), state
// in {1,2,3} (0 is not a state — the 2026-11-03 audit F-1 defect was an
// MCP schema documenting "0=paused" while the handler treated 0 as
// unset), day >= 0, hour 0..23. Local refusals are *ValidationError.
func UpdateSchedule(ctx context.Context, c *hooppy.Client, id int, in UpdateScheduleInput) (*hooppy.ScheduleResponse, error) {
	if id == 0 {
		return nil, &ValidationError{msg: "id is required (got 0)"}
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, errFieldf(in, "Name", "must not be empty")
	}
	if in.State != nil && (*in.State < 1 || *in.State > 3) {
		return nil, errFieldf(in, "State", "must be one of 1, 2 or 3 (got %d)", *in.State)
	}
	if in.DeletePostsDay != nil && *in.DeletePostsDay < 0 {
		return nil, errFieldf(in, "DeletePostsDay", "must be >= 0 (got %d)", *in.DeletePostsDay)
	}
	if in.DeletePostsHour != nil && (*in.DeletePostsHour < 0 || *in.DeletePostsHour > 23) {
		return nil, errFieldf(in, "DeletePostsHour", "must be in 0..23 (got %d)", *in.DeletePostsHour)
	}
	overrides, err := buildOverrides(in)
	if err != nil {
		return nil, err
	}
	if len(overrides) == 0 {
		return nil, errNoFields(in)
	}
	return c.UpdateScheduleFromEdit(ctx, id, overrides)
}
