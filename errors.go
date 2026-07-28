package hooppy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// APIError represents a non-2xx response from the Hooppy API.
type APIError struct {
	StatusCode int
	Body       []byte
	Message    string
}

func newAPIError(resp *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	ae := &APIError{
		StatusCode: resp.StatusCode,
		Body:       body,
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
	return ae
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
