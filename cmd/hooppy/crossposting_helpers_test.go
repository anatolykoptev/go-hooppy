package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// stubCrossPostingEditServer serves GET /cross-posting/{id}/edit with body.
func stubCrossPostingEditServer(t *testing.T, id int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/cross-posting/"+itoa(id)+"/edit" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// stubCrossPostingStatsServer serves GET /cross-posting/{id}/statistics with body.
func stubCrossPostingStatsServer(t *testing.T, id int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/cross-posting/"+itoa(id)+"/statistics" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// hooppyCrossPostingEditFixture is the 95-key scrubbed /edit fixture, loaded
// from the repo's testdata/live. The CLI tests run in cmd/hooppy, so the
// path is relative to the repo root.
var hooppyCrossPostingEditFixture = loadCrossPostingEditFixture()

func loadCrossPostingEditFixture() string {
	for _, p := range []string{
		filepath.Join("..", "..", "testdata", "live", "cross_posting_edit.json"),
		filepath.Join("testdata", "live", "cross_posting_edit.json"),
	} {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	return "{}"
}

// itoa is a local strconv.Itoa to avoid an extra import in the helpers file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
