package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anatolykoptev/go-kit/retry"
)

// newTestClient creates a Client pointing at a httptest.Server.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(Config{
		Token:   "test-token",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestNewClient_EmptyToken(t *testing.T) {
	_, err := NewClient(Config{Token: ""})
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestNewClient_InvalidBaseURL(t *testing.T) {
	_, err := NewClient(Config{Token: "x", BaseURL: "ht tp://bad"})
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
}

func TestNewClient_ValidToken(t *testing.T) {
	c, err := NewClient(Config{Token: "valid-token"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.token != "valid-token" {
		t.Errorf("token = %q, want %q", c.token, "valid-token")
	}
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
}

func TestNewClient_CustomLimits(t *testing.T) {
	c, err := NewClient(Config{Token: "x", MaxUploadBytes: 100, MaxResponseBytes: 200})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.maxUploadBytes != 100 {
		t.Errorf("maxUploadBytes = %d, want 100", c.maxUploadBytes)
	}
	if c.maxResponseBytes != 200 {
		t.Errorf("maxResponseBytes = %d, want 200", c.maxResponseBytes)
	}
}

func TestNewClient_DefaultLimits(t *testing.T) {
	c, err := NewClient(Config{Token: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.maxUploadBytes != MaxUploadBytes {
		t.Errorf("maxUploadBytes = %d, want %d", c.maxUploadBytes, MaxUploadBytes)
	}
	if c.maxResponseBytes != MaxResponseBytes {
		t.Errorf("maxResponseBytes = %d, want %d", c.maxResponseBytes, MaxResponseBytes)
	}
}

func TestNewClient_ExpiredJWT(t *testing.T) {
	// JWT with exp=1600000000 (Sep 2020) — expired.
	token := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJleHAiOjE2MDAwMDAwMDB9.invalid"
	_, err := NewClient(Config{Token: token})
	if err == nil {
		t.Fatal("expected error for expired JWT")
	}
}

func TestNewClient_NonJWTToken(t *testing.T) {
	// Non-JWT opaque token — should be accepted (no exp check).
	c, err := NewClient(Config{Token: "opaque-api-key-no-dots"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.token != "opaque-api-key-no-dots" {
		t.Errorf("token = %q", c.token)
	}
}

func TestNewClient_JWTWithoutExp(t *testing.T) {
	// JWT with no exp claim — should be accepted.
	// header: {"typ":"JWT","alg":"HS256"}, payload: {"sub":"test"}
	token := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.invalid"
	c, err := NewClient(Config{Token: token})
	if err != nil {
		t.Fatalf("unexpected error for JWT without exp: %v", err)
	}
	if c.token != token {
		t.Errorf("token mismatch")
	}
}

func TestNewClientFromEnv_NoToken(t *testing.T) {
	t.Setenv("HOOPPY_TOKEN", "")
	t.Setenv("HOME", "/tmp/nonexistent-home-12345")
	_, err := NewClientFromEnv()
	if err == nil {
		t.Fatal("expected error when no token available")
	}
}

func TestNewClientFromEnv_EnvToken(t *testing.T) {
	t.Setenv("HOOPPY_TOKEN", "env-token-123")
	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.token != "env-token-123" {
		t.Errorf("token = %q, want %q", c.token, "env-token-123")
	}
}

func TestClient_DoGET_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"total_rows":2,"is_has_more":false,"list":[{"id":1,"source_id":6}]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	resp, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if resp.TotalRows != 2 {
		t.Errorf("TotalRows = %d, want 2", resp.TotalRows)
	}
}

func TestClient_DoGET_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error for empty 200 response")
	}
}

func TestClient_DoGET_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !IsUnauthorized(err) {
		t.Errorf("expected IsUnauthorized, got: %v", err)
	}
}

func TestClient_DoGET_403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if !IsForbidden(err) {
		t.Errorf("expected IsForbidden, got: %v", err)
	}
}

func TestClient_DoGET_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if !IsNotFound(err) {
		t.Errorf("expected IsNotFound, got: %v", err)
	}
}

func TestClient_DoGET_429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if !IsRateLimited(err) {
		t.Errorf("expected IsRateLimited, got: %v", err)
	}
}

func TestClient_DoGET_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestClient_DoGET_500_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("plain text error"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error for 500 plain text")
	}
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if ae.Message != "plain text error" {
		t.Errorf("Message = %q, want %q", ae.Message, "plain text error")
	}
}

func TestClient_DoGET_500_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error for 500 empty body")
	}
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if ae.Message != "" {
		t.Errorf("Message = %q, want empty", ae.Message)
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := c.ListAccounts(ctx, ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestClient_ResponseExceedsMaxSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Send a response larger than MaxResponseBytes.
		large := make([]byte, 11<<20) // 11 MB > default 10 MB
		w.Write(large)
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, MaxResponseBytes: 100})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
}

// fastRetryOpts returns retry options suitable for tests: 1ms delay, no jitter.
func fastRetryOpts() *retry.Options {
	return &retry.Options{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond,
		MaxDelay:     time.Millisecond,
	}
}

func TestRetry_GET_429ThenSuccess(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":1,"name":"acct"}],"total":1}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
	if len(resp.List) != 0 {
		t.Errorf("resp.List len = %d, want 0 (mock returns empty data shape)", len(resp.List))
	}
}

func TestRetry_GET_503ThenSuccess(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"total":0}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestRetry_DELETE_429ThenSuccess(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.DeletePost(context.Background(), 42)
	if err != nil {
		t.Fatalf("DeletePost: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestRetry_POST_NotRetried(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.CreatePost(context.Background(), map[string]string{"text": "hello"})
	if err == nil {
		t.Fatal("expected error from 429")
	}
	if calls.Load() != 1 {
		t.Errorf("POST calls = %d, want 1 (POST must not retry)", calls.Load())
	}
}

func TestRetry_401NotRetried(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error from 401")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (401 must not retry)", calls.Load())
	}
}

func TestRetry_404NotRetried(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.DeletePost(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error from 404")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (404 must not retry)", calls.Load())
	}
}

func TestRetry_ContextCancelStopsRetry(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: &retry.Options{
		MaxAttempts:  10,
		InitialDelay: 5 * time.Second,
		MaxDelay:     5 * time.Second,
	}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = c.ListAccounts(ctx, ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	// First call happens immediately, then retry waits 5s but context cancels at 100ms.
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (context should cancel before 2nd attempt)", calls.Load())
	}
}

func TestRetry_MaxAttemptsExhausted(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3 (MaxAttempts)", calls.Load())
	}
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if ae.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("ae.StatusCode = %d, want 503", ae.StatusCode)
	}
}

func TestRetry_RetryAfterHeaderHonored(t *testing.T) {
	var calls atomic.Int64
	var firstCallTime time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			firstCallTime = time.Now()
			w.Header().Set("Retry-After", "1") // 1 second
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		elapsed := time.Since(firstCallTime)
		if elapsed < 900*time.Millisecond {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"retry too fast"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"total":0}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: &retry.Options{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Second, // high default; Retry-After should override
		MaxDelay:     10 * time.Second,
	}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestRetry_ZeroConfigNoRetry(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv) // no RetryOptions
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error from 503")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry without RetryOptions)", calls.Load())
	}
}

func TestRetry_PreservesAPIErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"specific rate limit message"}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if ae.Message != "specific rate limit message" {
		t.Errorf("ae.Message = %q, want %q", ae.Message, "specific rate limit message")
	}
	if ae.StatusCode != http.StatusTooManyRequests {
		t.Errorf("ae.StatusCode = %d, want 429", ae.StatusCode)
	}
}

func TestConfig_HTTPClientOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"total":0}`))
	}))
	defer srv.Close()
	custom := &http.Client{}
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, HTTPClient: custom})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.http != custom {
		t.Error("c.http != custom HTTPClient")
	}
	_, err = c.ListAccounts(context.Background(), ListAccountsFilter{})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
}

// TestRetry_ImportSearchPost_NotRetried pins the idempotency fix: POST
// /posts CREATES posts, so the PublishPost step inside ImportSearchPost must
// issue exactly one POST /posts even with RetryOptions set — a 5xx after the
// write committed, retried, would duplicate the created posts in a live
// publishing queue. The 500 surfaces as an error. Asserting the count (not
// just an error) catches both an unbounded-retry and a single-retry
// regression.
//
// The resolve step (GET /posts-search/{id}/edit) is a safe idempotent read
// and IS retried — the stub lets it succeed so the flow reaches POST /posts.
func TestRetry_ImportSearchPost_NotRetried(t *testing.T) {
	var postCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/posts-search/") && strings.HasSuffix(r.URL.Path, "/edit"):
			// Resolve step succeeds — flow proceeds to POST /posts.
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"123","publication_when_type":1,"publication_how_type":1,"publication_where_type":1,"created_by":7,"texts":[{"text":"x","source_id":0}],"attachments":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/posts":
			postCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"boom"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// when_type=1 (not schedule-driven) so no before-snapshot fires — only
	// the resolve + POST /posts requests reach the stub.
	_, err = c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        123,
		PublicationWhenType: 1,
	})
	if err == nil {
		t.Fatal("expected error from 500, got nil — POST /posts must not retry past a 5xx")
	}
	if got := postCalls.Load(); got != 1 {
		t.Errorf("POST /posts calls = %d, want 1 (POST /posts creates posts and must not retry)", got)
	}
}

// TestRetry_UpdatePostRetried confirms the retrying PUT path still applies to
// the full-state Update* verbs: a 500 then 200 yields exactly two requests
// and a success. UpdatePost targets a known id and converges on re-send, so
// retry is safe here (unlike the create-shaped import).
func TestRetry_UpdatePostRetried(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c, err := NewClient(Config{Token: "x", BaseURL: srv.URL, RetryOptions: fastRetryOpts()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := c.UpdatePost(context.Background(), 42, map[string]string{"text": "hi"})
	if err != nil {
		t.Fatalf("UpdatePost: %v", err)
	}
	if !resp.Success {
		t.Errorf("resp.Success = %v, want true", resp.Success)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("update calls = %d, want 2 (Update* PUT retries on 5xx)", got)
	}
}

// TestNewClientFromEnv_RetryEnabled is the WIRING test for defect #2: both
// front-ends (cmd/hooppy, cmd/hooppy-mcp) call NewClientFromEnv, which set
// BaseURL and Token but left RetryOptions nil — so every shipped binary
// died on the first 429 with no backoff. Asserting the retry POLICY table
// (TestRetryPolicySweep) does NOT catch this: it checks which calls WOULD
// retry if retry were on, never that retry is actually switched on. This
// test asserts the wiring itself: a client from NewClientFromEnv, pointed
// at a persistent-429 server, must issue MORE than one request (retry
// happened). Before the fix RetryOptions was nil and exactly one request
// reached the server.
//
// RED-on-revert: remove the RetryOptions from NewClientFromEnv's Config and
// calls drops to 1 (no retry path taken) — this test fails.
func TestNewClientFromEnv_RetryEnabled(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()
	t.Setenv("HOOPPY_TOKEN", "test-token")
	t.Setenv("HOOPPY_BASE_URL", srv.URL)
	c, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("NewClientFromEnv: %v", err)
	}
	// Context bounds the test: with a 2s deadline and 500ms initial backoff,
	// at least one retry fires (attempt 1 at t=0, attempt 2 at t≈500ms).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = c.ListAccounts(ctx, ListAccountsFilter{})
	if err == nil {
		t.Fatal("expected error from persistent 429, got nil")
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("calls = %d, want >= 2 — NewClientFromEnv must wire retry so a 429 is retried (before the fix RetryOptions was nil and only 1 request reached the server; the policy table was green the whole time because it checks which calls WOULD retry, never that retry is switched on)", got)
	}
}

// TestImportSearchPost_NoRetryOptions pins the default path: with
// RetryOptions == nil, ImportSearchPost issues exactly one request and the
// 500 surfaces as an error — byte-identical to today's behaviour.
func TestImportSearchPost_NoRetryOptions(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv) // no RetryOptions
	_, err := c.ImportSearchPost(context.Background(), CopySearchPostPayload{
		SearchPostID:        123,
		PublicationWhenType: 1,
	})
	if err == nil {
		t.Fatal("expected error from 500, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("import calls = %d, want 1 (no retry without RetryOptions)", got)
	}
}
