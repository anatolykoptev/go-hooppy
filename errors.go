package hooppy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIError represents a non-2xx response from the Hooppy API.
type APIError struct {
	StatusCode int
	Body       []byte
	Message    string
	RetryAfter time.Duration // parsed from Retry-After header (RFC 7231); 0 if absent
}

func newAPIError(resp *http.Response) *APIError {
	const maxErrBody = 1 << 20 // 1 MB
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody+1))
	truncated := int64(len(body)) > maxErrBody
	if truncated {
		body = body[:maxErrBody]
	}
	ae := &APIError{
		StatusCode: resp.StatusCode,
		Body:       body,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
	// Try to extract a message from common JSON shapes: {"message": "..."} or {"error": "..."}.
	var m map[string]interface{}
	if json.Unmarshal(body, &m) == nil {
		if v, ok := m["message"].(string); ok {
			ae.Message = v
		} else if v, ok := m["error"].(string); ok {
			ae.Message = v
		}
	}
	if ae.Message == "" && len(body) > 0 {
		ae.Message = string(body)
	}
	if truncated {
		ae.Message += "... (truncated)"
	}
	return ae
}

// isRetryableStatus reports whether the HTTP status code warrants a retry.
// Same set as go-kit/retry.isRetryableStatus (retry.go:157-168).
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// parseRetryAfter parses the HTTP Retry-After header per RFC 7231. The
// value can be either a non-negative integer number of seconds or an
// HTTP-date. Returns 0 on empty or unparseable input.
// Modeled on go-kit/llm/errors.go:parseRetryAfter (unexported there).
func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	// Seconds form.
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	// HTTP-date form.
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("hooppy: HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("hooppy: HTTP %d", e.StatusCode)
}

// IsUnauthorized returns true for 401 responses (invalid/expired token).
func IsUnauthorized(err error) bool {
	var ae *APIError
	if !errorsAs(err, &ae) {
		return false
	}
	return ae.StatusCode == http.StatusUnauthorized
}

// IsForbidden returns true for 403 responses (insufficient permissions/plan).
func IsForbidden(err error) bool {
	var ae *APIError
	if !errorsAs(err, &ae) {
		return false
	}
	return ae.StatusCode == http.StatusForbidden
}

// IsNotFound returns true for 404 responses.
func IsNotFound(err error) bool {
	var ae *APIError
	if !errorsAs(err, &ae) {
		return false
	}
	return ae.StatusCode == http.StatusNotFound
}

// IsRateLimited returns true for 429 responses.
func IsRateLimited(err error) bool {
	var ae *APIError
	if !errorsAs(err, &ae) {
		return false
	}
	return ae.StatusCode == http.StatusTooManyRequests
}

// isResultWindowError reports whether err is the Elasticsearch
// max_result_window rejection: HTTP 500 carrying "Result window is too large"
// (an illegal_argument_exception). This is the server's HARD ceiling on offset
// paging — from + size must be <= max_result_window (10000 on Hooppy today,
// but the value is a server config, NOT hardcoded here). It is NOT a transient
// 500 and retrying it is pointless (the same offset reproduces it), but the
// retry layer will still surface it after its attempts exhaust; the caller's
// job is to recognise it and keep the rows already collected instead of
// discarding them. The only recovery is to narrow with date filters so the
// offset stays within the window.
//
// Detection matches the phrase in EITHER the extracted Message or the raw
// Body: the real ES error nests the reason under an "error" object
// ({"error":{"type":"illegal_argument_exception","reason":"Result window is
// too large, ..."}}), so newAPIError's string-extraction falls through to the
// raw body and the phrase lives in Body, not Message. Checking both is robust
// to either shape.
func isResultWindowError(err error) bool {
	var ae *APIError
	if !errorsAs(err, &ae) {
		return false
	}
	if ae.StatusCode != http.StatusInternalServerError {
		return false
	}
	hay := ae.Message + " " + string(ae.Body)
	return strings.Contains(hay, "Result window is too large")
}
