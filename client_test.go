package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
