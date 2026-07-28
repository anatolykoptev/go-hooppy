package hooppy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

func TestNewAPIError_JSONMessageField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if ae.Message != "bad request" {
		t.Errorf("Message = %q, want %q", ae.Message, "bad request")
	}
}

func TestNewAPIError_JSONErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if ae.Message != "unauthorized" {
		t.Errorf("Message = %q, want %q", ae.Message, "unauthorized")
	}
}

func TestNewAPIError_InvalidJSONFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("<<<not json>>>"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if ae.Message != "<<<not json>>>" {
		t.Errorf("Message = %q, want raw body", ae.Message)
	}
}

func TestNewAPIError_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if ae.Message != "" {
		t.Errorf("Message = %q, want empty", ae.Message)
	}
}

func TestNewAPIError_TruncatedBody(t *testing.T) {
	// Generate a body larger than 1 MB to test truncation marker.
	large := make([]byte, 2<<20) // 2 MB
	for i := range large {
		large[i] = 'x'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(large)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if !contains(ae.Message, "(truncated)") {
		t.Errorf("Message should contain '(truncated)', got length %d", len(ae.Message))
	}
}

func TestAPIError_ErrorString(t *testing.T) {
	ae := &APIError{StatusCode: 404, Message: "not found"}
	want := "hooppy: HTTP 404: not found"
	if ae.Error() != want {
		t.Errorf("Error() = %q, want %q", ae.Error(), want)
	}
}

func TestAPIError_ErrorStringNoMessage(t *testing.T) {
	ae := &APIError{StatusCode: 500}
	want := "hooppy: HTTP 500"
	if ae.Error() != want {
		t.Errorf("Error() = %q, want %q", ae.Error(), want)
	}
}

func TestIsUnauthorized_NonAPIError(t *testing.T) {
	if IsUnauthorized(nil) {
		t.Error("IsUnauthorized(nil) should be false")
	}
}

func TestIsForbidden_NonAPIError(t *testing.T) {
	if IsForbidden(nil) {
		t.Error("IsForbidden(nil) should be false")
	}
}

func TestIsNotFound_NonAPIError(t *testing.T) {
	if IsNotFound(nil) {
		t.Error("IsNotFound(nil) should be false")
	}
}

func TestIsRateLimited_NonAPIError(t *testing.T) {
	if IsRateLimited(nil) {
		t.Error("IsRateLimited(nil) should be false")
	}
}

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestUUIDv4_Format(t *testing.T) {
	id, err := uuidv4()
	if err != nil {
		t.Fatalf("uuidv4: %v", err)
	}
	if !uuidRegex.MatchString(id) {
		t.Errorf("uuid %q does not match UUID v4 format", id)
	}
}

func TestUUIDv4_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id, err := uuidv4()
		if err != nil {
			t.Fatalf("uuidv4 iteration %d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate UUID at iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

func TestParseRetryAfter_Seconds(t *testing.T) {
	if d := parseRetryAfter("60"); d != 60*time.Second {
		t.Errorf("parseRetryAfter(\"60\") = %v, want 60s", d)
	}
	if d := parseRetryAfter("0"); d != 0 {
		t.Errorf("parseRetryAfter(\"0\") = %v, want 0", d)
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(future)
	if d <= 0 || d > 2*time.Hour+5*time.Second {
		t.Errorf("parseRetryAfter(future HTTP-date) = %v, want ~2h", d)
	}
}

func TestParseRetryAfter_PastHTTPDate(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(past); d != 0 {
		t.Errorf("parseRetryAfter(past HTTP-date) = %v, want 0", d)
	}
}

func TestParseRetryAfter_Empty(t *testing.T) {
	if d := parseRetryAfter(""); d != 0 {
		t.Errorf("parseRetryAfter(\"\") = %v, want 0", d)
	}
}

func TestParseRetryAfter_Invalid(t *testing.T) {
	if d := parseRetryAfter("not-a-date-or-number"); d != 0 {
		t.Errorf("parseRetryAfter(invalid) = %v, want 0", d)
	}
}

func TestParseRetryAfter_NegativeSeconds(t *testing.T) {
	if d := parseRetryAfter("-5"); d != 0 {
		t.Errorf("parseRetryAfter(\"-5\") = %v, want 0", d)
	}
}

func TestNewAPIError_RetryAfterHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if ae.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", ae.RetryAfter)
	}
}

func TestNewAPIError_NoRetryAfterHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal"}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	_, err := c.ListAccounts(context.Background(), ListAccountsFilter{})
	var ae *APIError
	if !errorsAs(err, &ae) {
		t.Fatal("expected *APIError")
	}
	if ae.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0", ae.RetryAfter)
	}
}

func TestIsRetryableStatus(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusGatewayTimeout, true},
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusNotFound, false},
	}
	for _, tc := range cases {
		if got := isRetryableStatus(tc.code); got != tc.want {
			t.Errorf("isRetryableStatus(%d) = %v, want %v", tc.code, got, tc.want)
		}
	}
}
