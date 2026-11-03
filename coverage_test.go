package hooppy_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-hooppy/internal/apicov"
)

var updateReadme = flag.Bool("update-readme", false, "rewrite the README API coverage table from openapi.yaml and the Go sources")

// TestCoverageTable_ReadmeMatchesDerivation fails when README.md's API
// coverage table differs from what apicov derives from openapi.yaml plus the
// real client/CLI/MCP registrations. Regenerate with:
//
//	go test -run TestCoverageTable -update-readme .
//
// Mutations that go RED: deleting a path from openapi.yaml (a row vanishes),
// renaming a client method or dropping a tool registration (a cell changes),
// or hand-editing the table.
func TestCoverageTable_ReadmeMatchesDerivation(t *testing.T) {
	spec, err := apicov.LoadSpec("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(spec) == 0 {
		t.Fatal("openapi.yaml documents no operations")
	}
	surf, err := apicov.Derive(".")
	if err != nil {
		t.Fatal(err)
	}
	want := apicov.Render(apicov.Rows(spec, surf))

	readmePath := filepath.Join(".", "README.md")
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := apicov.ReplaceBlock(string(raw), want)
	if err != nil {
		t.Fatal(err)
	}
	if *updateReadme {
		if err := os.WriteFile(readmePath, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if updated != string(raw) {
		t.Fatalf("README.md API coverage table is stale; run: go test -run TestCoverageTable -update-readme .\n--- derived table (head) ---\n%s", head(want, 12))
	}
}

// TestCoverage_DerivationResolvesEveryCallShape guards the AST walk
// itself: the derivation must resolve a path for the client methods this
// test names (one per call shape: const, fmt.Sprintf(const), multipart,
// raw), so a refactor of how paths are passed cannot silently empty the
// table.
func TestCoverage_DerivationResolvesEveryCallShape(t *testing.T) {
	surf, err := apicov.Derive(".")
	if err != nil {
		t.Fatal(err)
	}
	for method, want := range map[string]apicov.Op{
		"ListPages":       {Method: "GET", Path: "/accounts/pages"},          // const path
		"DeletePost":      {Method: "DELETE", Path: "/posts/{}"},             // fmt.Sprintf(const, id)
		"UploadMedia":     {Method: "POST", Path: "/files/media/upload"},     // multipart stream
		"GetScheduleEdit": {Method: "GET", Path: "/posts/schedules/{}/edit"}, // plain GET
	} {
		found := false
		for _, o := range surf.ClientDirect[method] {
			if o == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: derived ops %v do not include %s", method, surf.ClientDirect[method], want)
		}
	}
}

func head(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
