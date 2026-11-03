package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy"
)

// stubScheduleEdit serves GET /posts/schedules/{id}/edit with the given body.
// Used by the `schedules show` and `schedules list --repeated` tests.
func stubScheduleEdit(t *testing.T, edits map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts/schedules/") && strings.HasSuffix(r.URL.Path, "/edit") {
			if body, ok := edits[r.URL.Path]; ok {
				w.Write([]byte(body))
				return
			}
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
}

// TestRunScheduleShow_PrintsAutoDeleteFields drives `schedules show <id>` end
// to end and asserts the JSON output carries id, name, state,
// is_posts_repeated, delete_posts_day and delete_posts_hour read from the
// /edit response — the fields the auto-delete workflow needs.
//
// RED-on-revert: if runScheduleShow stops routing through GetScheduleEdit or
// drops a field from the output struct, the corresponding assertion fails.
func TestRunScheduleShow_PrintsAutoDeleteFields(t *testing.T) {
	srv := stubScheduleEdit(t, map[string]string{
		"/posts/schedules/57793/edit": `{
			"id": 57793,
			"name": "Repeating",
			"state": 1,
			"publication_how_type": 1,
			"is_posts_repeated": 1,
			"delete_posts_day": 30,
			"delete_posts_hour": 5,
			"times": [[{"hours":12,"minutes":25}],[],[],[],[],[],[]]
		}`,
	})
	defer srv.Close()
	c, err := hooppy.NewClient(hooppy.Config{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var out, errOut bytes.Buffer
	code := runScheduleShow(context.Background(), c, &out, &errOut, 57793)
	if code != 0 {
		t.Fatalf("runScheduleShow exit %d; stderr=%s", code, errOut.String())
	}
	var got struct {
		ID              int    `json:"id"`
		Name            string `json:"name"`
		State           int    `json:"state"`
		IsPostsRepeated int    `json:"is_posts_repeated"`
		DeletePostsDay  int    `json:"delete_posts_day"`
		DeletePostsHour int    `json:"delete_posts_hour"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not the schedule summary JSON: %v (stdout=%q)", err, out.String())
	}
	if got.ID != 57793 || got.Name != "Repeating" || got.State != 1 ||
		got.IsPostsRepeated != 1 || got.DeletePostsDay != 30 || got.DeletePostsHour != 5 {
		t.Errorf("stdout summary = %+v, want id=57793 name=Repeating state=1 is_posts_repeated=1 delete_posts_day=30 delete_posts_hour=5", got)
	}
}
