package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/anatolykoptev/go-hooppy"
)

// emitList is the shared tail of every list command's testable core. It
// encodes result to out as indented JSON and, when the result is a TRUNCATED
// single page (is_has_more=true) and the caller did NOT pass --all, prints a
// stderr warning naming the numbers and the exact remedy. Stdout stays valid
// JSON — the warning goes to errOut, never out — and the exit code stays 0:
// a truncated page is a complete answer to "give me page N", not an error.
//
// noun is the lowercase plural the warning reads back to the user ("pages",
// "accounts", "source resources", ...). returned is len(list) on this page;
// total is the envelope's total_rows. For an --all walk the caller passes
// all=true and isHasMore=false (the AllListEnvelope pins is_has_more false),
// so no warning fires.
//
// The warning text is fixed by issue #103: a silent short list is the
// failure mode, and the warning is the fix even before --all lands
// everywhere. The format mirrors the summary work in #101:
//
//	40 pages total — showing 20. Use --all to fetch every page.
func emitList(out, errOut io.Writer, noun string, all bool, returned, total int, isHasMore bool, result interface{}) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(errOut, "error encoding output: %v\n", err)
		return 1
	}
	if !all && isHasMore {
		fmt.Fprintf(errOut, "%d %s total — showing %d. Use --all to fetch every page.\n", total, noun, returned)
	}
	return 0
}

// runListPages is the testable core of `hooppy pages list`. all walks every
// page via ListAllPagesWithTotal and emits an AllListEnvelope; otherwise a
// single page is fetched and a truncation warning is emitted when
// is_has_more is true. Returns the process exit code (0 on success, 1 on
// fetch/encode error).
func runListPages(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, f hooppy.ListPagesFilter, all bool) int {
	if all {
		list, total, err := c.ListAllPagesWithTotal(ctx, f)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelope(list, total, func(p hooppy.Page) int { return p.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "pages", true, len(list), total, false, env)
	}
	resp, err := c.ListPages(ctx, f)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "pages", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListAccounts is the testable core of `hooppy accounts`.
func runListAccounts(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, f hooppy.ListAccountsFilter, all bool) int {
	if all {
		list, total, err := c.ListAllAccountsWithTotal(ctx, f)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelope(list, total, func(a hooppy.Account) int { return a.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "accounts", true, len(list), total, false, env)
	}
	resp, err := c.ListAccounts(ctx, f)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "accounts", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListNotifications is the testable core of `hooppy notifications`.
func runListNotifications(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, page int, all bool) int {
	if all {
		list, firstTotal, lastTotal, err := c.ListAllNotificationsWithFirstAndLastTotal(ctx)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelopeHighChurn(list, firstTotal, lastTotal, func(n hooppy.Notification) int { return n.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "notifications", true, len(list), lastTotal, false, env)
	}
	resp, err := c.ListNotifications(ctx, page)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "notifications", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListProxies is the testable core of `hooppy proxies list`.
func runListProxies(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, page int, all bool) int {
	if all {
		list, total, err := c.ListAllProxiesWithTotal(ctx)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelope(list, total, func(p hooppy.Proxy) int { return p.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "proxies", true, len(list), total, false, env)
	}
	resp, err := c.ListProxies(ctx, page)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "proxies", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListWatermarks is the testable core of `hooppy watermarks list`.
func runListWatermarks(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, page int, all bool) int {
	if all {
		list, total, err := c.ListAllWatermarksWithTotal(ctx)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelope(list, total, func(w hooppy.Watermark) int { return w.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "watermarks", true, len(list), total, false, env)
	}
	resp, err := c.ListWatermarks(ctx, page)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "watermarks", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListPosts is the testable core of `hooppy posts list`.
func runListPosts(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, f hooppy.ListPostsFilter, all bool) int {
	if all {
		list, firstTotal, lastTotal, err := c.ListAllPostsWithFirstAndLastTotal(ctx, f)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelopeHighChurn(list, firstTotal, lastTotal, func(p hooppy.Post) int { return p.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "posts", true, len(list), lastTotal, false, env)
	}
	resp, err := c.ListPosts(ctx, f)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "posts", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListSearchPosts is the testable core of `hooppy search posts`.
//
// Exit codes follow the repo's partial-read convention established by
// runScheduleQueue: 0 = complete, 1 = error (no usable data), 2 = partial.
// A CAPPED --all walk (the server's Elasticsearch max_result_window refused
// the next page) is a PARTIAL read: the operator asked for --all and got
// every row the server could serve via offset paging — a bounded,
// complete-as-possible result, not an error and not complete. It exits 2 so a
// script can branch on "complete vs capped vs error", mirroring
// runScheduleQueue's is_has_more=true -> exit 2. The rows are emitted to
// stdout (NOT discarded — discarding 10000 collected rows to an error on the
// next page was the defect this fixes); a stderr warning names the row count,
// the server wall, and the --date-from/--date-to remedy. The stdout envelope
// is built with NewCappedAllListEnvelope (is_has_more=true), NOT
// NewAllListEnvelopeHighChurn: a capped total_rows is a ceiling, not a count,
// and the first-total validation would validate against a constant.
func runListSearchPosts(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, f hooppy.SearchPostsFilter, all bool) int {
	if all {
		res, err := c.ListAllSearchPostsWithFirstAndLastTotal(ctx, f)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		if res.Capped {
			env := hooppy.NewCappedAllListEnvelope(res.List, res.LastTotalRows)
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(env); err != nil {
				fmt.Fprintf(errOut, "error encoding output: %v\n", err)
				return 1
			}
			fmt.Fprintf(errOut, "warn: CAPPED result — %d search posts returned; the server refuses offset paging past its reachable window (Elasticsearch max_result_window, total_rows=%d) so older rows are unreachable by page number alone. Narrow with --date-from/--date-to (date_from/date_to) to reach posts beyond the window.\n", len(res.List), res.LastTotalRows)
			return 2
		}
		env, err := hooppy.NewAllListEnvelopeHighChurn(res.List, res.FirstTotalRows, res.LastTotalRows, func(p hooppy.SearchPost) int { return p.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "search posts", true, len(res.List), res.LastTotalRows, false, env)
	}
	resp, err := c.ListSearchPosts(ctx, f)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "search posts", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListSourceResources is the testable core of `hooppy search sources`.
func runListSourceResources(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, page int, all bool) int {
	if all {
		list, total, err := c.ListAllSourceResourcesWithTotal(ctx)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelope(list, total, func(s hooppy.SourceResource) int { return s.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "source resources", true, len(list), total, false, env)
	}
	resp, err := c.ListSourceResources(ctx, page)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "source resources", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListProjects is the testable core of `hooppy projects list`.
func runListProjects(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, page int, all bool) int {
	if all {
		list, total, err := c.ListAllProjectsWithTotal(ctx)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelope(list, total, func(p hooppy.Project) int { return p.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "projects", true, len(list), total, false, env)
	}
	resp, err := c.ListProjects(ctx, page)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "projects", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}

// runListSchedules is the testable core of `hooppy schedules list`.
func runListSchedules(ctx context.Context, c *hooppy.Client, out, errOut io.Writer, page int, all bool) int {
	if all {
		list, total, err := c.ListAllSchedulesWithTotal(ctx)
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		env, err := hooppy.NewAllListEnvelope(list, total, func(s hooppy.Schedule) int { return s.ID })
		if err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return emitList(out, errOut, "schedules", true, len(list), total, false, env)
	}
	resp, err := c.ListSchedules(ctx, page)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return 1
	}
	return emitList(out, errOut, "schedules", false, len(resp.List), resp.TotalRows, resp.IsHasMore, resp)
}
