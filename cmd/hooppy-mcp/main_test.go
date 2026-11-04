package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
	"github.com/anatolykoptev/go-hooppy/ops"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The search-post publish tools are thin adapters over the ops layer: the
// tool input IS the ops input struct, so every guard the old per-surface
// builders carried (strict ordered-id parse, schedules/when-type
// contradiction, when-type range, batch+text refusal) now lives in the op
// and runs identically for the CLI. The tables below re-drive the same
// refusal cases against the ops directly — a nil client is safe because
// validation fires before any request is built. End-to-end coverage of the
// tool-call path (JSON args → adapter → op → wire) lives in
// import_rewrite_test.go and the F1/F2/F3 adapter tests there.
//
// Ordered-id strictness: MCP agents now pass search_post_ids as a JSON
// array, not a comma string, so the unparseable-token class is rejected by
// JSON decode at the boundary; the zero/negative classes the ops still see
// are asserted here and in ops/publish_search_test.go.

func pint(v int) *int           { return &v }
func pstr(v string) *string     { return &v }
func pids(v ...int) *ops.IDList { l := ops.IDList(v); return &l }

// requireOpValidation asserts err is a *ops.ValidationError naming every
// substring — the refusal a tool adapter maps to IsError=true before any
// request is built.
func requireOpValidation(t *testing.T, err error, subs ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a *ops.ValidationError refusal, got nil")
	}
	var ve *ops.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v (%T), want *ops.ValidationError — a local refusal must be typed so the adapter maps it to a tool error before any request", err, err)
	}
	for _, sub := range subs {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q does not contain %q", err.Error(), sub)
		}
	}
}

// TestRewriteSearchPost_StrictIDs guards the ordered-id strictness at the op:
// a zero or negative id in an order-significant batch must error naming the
// offending value — a dropped entry shifts every later post's schedule slot
// by one. (Unparseable tokens cannot reach here anymore: the MCP arg is a
// JSON array and the CLI flag is a parsed []int.)
func TestRewriteSearchPost_StrictIDs(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		ids    []int
		errSub string
	}{
		{"zero id errors naming it", []int{2001, 0, 2003}, "0"},
		{"negative id errors naming it", []int{2001, -5, 2003}, "-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ops.RewriteSearchPost(ctx, nil, ops.RewriteSearchPostInput{
				SearchPostIDs: pids(tc.ids...),
				PublishSearchTarget: ops.PublishSearchTarget{
					PublicationWhenType: pint(1),
				},
			})
			requireOpValidation(t, err, "positive", tc.errSub)
		})
	}
}

// TestRewriteSearchPost_BatchTextRefusal pins finding 4 at the op: batch
// rewrite cannot express per-post text — text with search_post_ids errors;
// single-post without text errors.
func TestRewriteSearchPost_BatchTextRefusal(t *testing.T) {
	ctx := context.Background()
	t.Run("batch + text refused", func(t *testing.T) {
		_, err := ops.RewriteSearchPost(ctx, nil, ops.RewriteSearchPostInput{
			SearchPostIDs: pids(2001, 2002),
			Text:          pstr("hello"),
			PublishSearchTarget: ops.PublishSearchTarget{
				PublicationWhenType: pint(1),
				SelectedPagesIDs:    pids(123),
			},
		})
		requireOpValidation(t, err, "--text", "--post-ids")
	})
	t.Run("single + no text refused", func(t *testing.T) {
		_, err := ops.RewriteSearchPost(ctx, nil, ops.RewriteSearchPostInput{
			SearchPostID: pint(2001),
			PublishSearchTarget: ops.PublishSearchTarget{
				PublicationWhenType: pint(1),
				SelectedPagesIDs:    pids(123),
			},
		})
		requireOpValidation(t, err, "--text", "required")
	})
}

// The schedules/when-type contradiction guard, driven at the op for all
// three tools — both directions per tool so neither arm is satisfied by a
// guard that refuses everything:
//   - schedules with when-type 1 → error (the contradiction guard)
//   - when-type 3 without schedules → error (the converse guard)
//
// The _OK pair (schedules DO reach the wire under when-type 3) is pinned at
// the wire level in ops/publish_search_test.go
// (TestCopySearchPost_WhenType3SendsSchedules).
func TestOps_SchedulesWhenTypeGuard(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		whenTy int
		sched  []int
		pages  []int // pages only on the when-type-1 row (pages under 3 is the #139 guard, a different refusal)
		errSub string
	}{
		{"when_type 1 + schedules refused (contradiction guard)", 1, []int{10, 11}, []int{123}, "when-type 1"},
		{"when_type 3 + no schedules refused (converse guard)", 3, nil, nil, "required"},
	} {
		t.Run("copy/"+tc.name, func(t *testing.T) {
			_, err := ops.CopySearchPost(ctx, nil, ops.CopySearchPostInput{
				SearchPostID: pint(2001),
				PublishSearchTarget: ops.PublishSearchTarget{
					PublicationWhenType: pint(tc.whenTy),
					SchedulesIDs:        pids(tc.sched...),
					SelectedPagesIDs:    pids(tc.pages...),
				},
			})
			requireOpValidation(t, err, "schedules", tc.errSub)
		})
		t.Run("rewrite/"+tc.name, func(t *testing.T) {
			_, err := ops.RewriteSearchPost(ctx, nil, ops.RewriteSearchPostInput{
				SearchPostID: pint(2001),
				Text:         pstr("x"),
				PublishSearchTarget: ops.PublishSearchTarget{
					PublicationWhenType: pint(tc.whenTy),
					SchedulesIDs:        pids(tc.sched...),
					SelectedPagesIDs:    pids(tc.pages...),
				},
			})
			requireOpValidation(t, err, "schedules", tc.errSub)
		})
		t.Run("import/"+tc.name, func(t *testing.T) {
			_, err := ops.ImportSearchPost(ctx, nil, ops.ImportSearchPostInput{
				SearchPostID: pint(2001),
				PublishSearchTarget: ops.PublishSearchTarget{
					PublicationWhenType: pint(tc.whenTy),
					SchedulesIDs:        pids(tc.sched...),
					SelectedPagesIDs:    pids(tc.pages...),
				},
			})
			requireOpValidation(t, err, "schedules", tc.errSub)
		})
	}
}

// F11 — the MCP stop path's three arms. Round 2 of the PR #138 review found
// this was the one change in the branch that nothing gated: reverting the
// in-progress arm to errResult left all three packages green, and a grep for
// StopParsing across the MCP tests returned nothing at all.
//
// The in-progress arm is the load-bearing one. An LLM agent reads IsError as
// "retry", and a retried stop cancels a job started in the interim
// (posts_search.go:485-489), so reporting a still-settling stop as a failure
// is how the tool destroys work it was not asked to touch.
func TestStopParsingResult_ThreeArms_F11(t *testing.T) {
	for _, tc := range []struct {
		name        string
		res         hooppy.StopParsingResult
		wantIsError bool
		wantSuccess bool
		wantInProg  bool
	}{
		{
			name:        "idle after stop — success",
			res:         hooppy.StopParsingResult{IsParsingInProgress: false},
			wantIsError: false,
			wantSuccess: true,
			wantInProg:  false,
		},
		{
			name:        "still in progress — a STATE, never an error",
			res:         hooppy.StopParsingResult{IsParsingInProgress: true},
			wantIsError: false,
			wantSuccess: false,
			wantInProg:  true,
		},
		{
			name:        "confirmation failed — a real error",
			res:         hooppy.StopParsingResult{ConfirmErr: "status read failed"},
			wantIsError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stopParsingResult(&tc.res)
			if err != nil {
				t.Fatalf("stopParsingResult returned a transport error: %v", err)
			}
			if got.IsError != tc.wantIsError {
				t.Fatalf("IsError = %v, want %v — an agent reads IsError as 'retry the stop', and a retried stop cancels a job started in the interim (posts_search.go:485-489)", got.IsError, tc.wantIsError)
			}
			if tc.wantIsError {
				return
			}
			var payload map[string]interface{}
			if len(got.Content) == 0 {
				t.Fatalf("no content in result")
			}
			tc2, ok := got.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("content[0] is %T, want *mcp.TextContent", got.Content[0])
			}
			if err := json.Unmarshal([]byte(tc2.Text), &payload); err != nil {
				t.Fatalf("result body is not JSON: %v\n%s", err, tc2.Text)
			}
			if payload["success"] != tc.wantSuccess {
				t.Errorf("success = %v, want %v", payload["success"], tc.wantSuccess)
			}
			if payload["is_parsing_in_progress"] != tc.wantInProg {
				t.Errorf("is_parsing_in_progress = %v, want %v — the agent needs the STATE to decide between polling parsing_status and re-stopping", payload["is_parsing_in_progress"], tc.wantInProg)
			}
		})
	}
}

// F12 — publication_when_type outside {1,2,3} is refused at the op, and an
// explicitly-given 0 in particular. (The pointer input means an OMITTED
// when-type is nil → the op's safe default of 3 applies, which then demands
// schedules — it no longer falls through to publish-now. An explicit 0/4/-1
// is still refused.)
func TestOps_RejectInvalidWhenType_F12(t *testing.T) {
	ctx := context.Background()
	for _, wt := range []int{0, 4, -1} {
		t.Run(fmt.Sprintf("copy_when_type_%d", wt), func(t *testing.T) {
			_, err := ops.CopySearchPost(ctx, nil, ops.CopySearchPostInput{
				SearchPostID: pint(2001),
				PublishSearchTarget: ops.PublishSearchTarget{
					PublicationWhenType: pint(wt),
					SelectedPagesIDs:    pids(123),
				},
			})
			requireOpValidation(t, err, "when-type", fmt.Sprintf("%d", wt))
		})
		t.Run(fmt.Sprintf("rewrite_when_type_%d", wt), func(t *testing.T) {
			_, err := ops.RewriteSearchPost(ctx, nil, ops.RewriteSearchPostInput{
				SearchPostID: pint(2001),
				Text:         pstr("x"),
				PublishSearchTarget: ops.PublishSearchTarget{
					PublicationWhenType: pint(wt),
					SelectedPagesIDs:    pids(123),
				},
			})
			requireOpValidation(t, err, "when-type", fmt.Sprintf("%d", wt))
		})
		t.Run(fmt.Sprintf("import_when_type_%d", wt), func(t *testing.T) {
			_, err := ops.ImportSearchPost(ctx, nil, ops.ImportSearchPostInput{
				SearchPostID: pint(2001),
				PublishSearchTarget: ops.PublishSearchTarget{
					PublicationWhenType: pint(wt),
					SelectedPagesIDs:    pids(123),
				},
			})
			requireOpValidation(t, err, "when-type", fmt.Sprintf("%d", wt))
		})
	}
}

// TestOps_CrossSurfaceGuards_F21 is the cross-surface invariant: EVERY
// publish op refuses schedules with a when-type other than 3, and refuses a
// when-type outside {1,2,3}. The ops are enumerated explicitly so a fourth
// publish op added without the shared target resolver fails the suite rather
// than shipping — a count of 2 guards across 3 tools is what hid the round-3
// BLOCKER (the import builder had neither guard, and no test seam reached
// it).
func TestOps_CrossSurfaceGuards_F21(t *testing.T) {
	ctx := context.Background()
	target := func(whenTy int, sched []int) ops.PublishSearchTarget {
		return ops.PublishSearchTarget{
			PublicationWhenType: pint(whenTy),
			SchedulesIDs:        pids(sched...),
			SelectedPagesIDs:    pids(123),
		}
	}
	cases := []struct {
		name      string
		schedules func() error // schedules_ids with when-type 1 — must error
		whenType0 func() error // when-type 0 (explicit zero) — must error
		whenType4 func() error // when-type 4 (out of range) — must error
	}{
		{"ops.CopySearchPost",
			func() error {
				_, err := ops.CopySearchPost(ctx, nil, ops.CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: target(1, []int{10, 11})})
				return err
			},
			func() error {
				_, err := ops.CopySearchPost(ctx, nil, ops.CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: target(0, nil)})
				return err
			},
			func() error {
				_, err := ops.CopySearchPost(ctx, nil, ops.CopySearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: target(4, nil)})
				return err
			},
		},
		{"ops.RewriteSearchPost",
			func() error {
				_, err := ops.RewriteSearchPost(ctx, nil, ops.RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: target(1, []int{10, 11})})
				return err
			},
			func() error {
				_, err := ops.RewriteSearchPost(ctx, nil, ops.RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: target(0, nil)})
				return err
			},
			func() error {
				_, err := ops.RewriteSearchPost(ctx, nil, ops.RewriteSearchPostInput{SearchPostID: pint(2001), Text: pstr("x"), PublishSearchTarget: target(4, nil)})
				return err
			},
		},
		{"ops.ImportSearchPost",
			func() error {
				_, err := ops.ImportSearchPost(ctx, nil, ops.ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: target(1, []int{10, 11})})
				return err
			},
			func() error {
				_, err := ops.ImportSearchPost(ctx, nil, ops.ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: target(0, nil)})
				return err
			},
			func() error {
				_, err := ops.ImportSearchPost(ctx, nil, ops.ImportSearchPostInput{SearchPostID: pint(2001), PublishSearchTarget: target(4, nil)})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/schedules_with_when_type_1", func(t *testing.T) {
			if err := tc.schedules(); err == nil {
				t.Fatalf("%s: schedules_ids with publication_when_type=1 was accepted — every publish op must refuse this (the schedules are silently dropped and the post publishes NOW, the round-3 BLOCKER)", tc.name)
			}
		})
		t.Run(tc.name+"/when_type_0", func(t *testing.T) {
			if err := tc.whenType0(); err == nil {
				t.Fatalf("%s: publication_when_type=0 was accepted — every publish op must refuse an out-of-range when-type", tc.name)
			}
		})
		t.Run(tc.name+"/when_type_4", func(t *testing.T) {
			if err := tc.whenType4(); err == nil {
				t.Fatalf("%s: publication_when_type=4 was accepted — every publish op must refuse a when-type outside {1,2,3}", tc.name)
			}
		})
	}
}
