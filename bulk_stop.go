package hooppy

import (
	"errors"
	"fmt"
	"net/http"
)

// MaxConsecutiveServerErrors is how many 5xx answers in a row end a loop that
// sends one request per id: a server that fails three requests in a row is
// down or overloaded, and the remaining ids would only repeat it.
const MaxConsecutiveServerErrors = 3

// IsServerAnswer reports whether err is the server's own answer to a request
// (a 2xx success:false, or an HTTP error other than 401/403/429) rather than a
// failure that repeats on every further request: no answer at all (transport
// error, cancelled or expired context), an auth refusal or a rate limit.
//
// It is the ONE rule every per-id loop in the library and in ops shares to
// decide whether going on with the next id can still tell it something.
func IsServerAnswer(err error) bool {
	var sf *SuccessFalseError
	if errors.As(err, &sf) {
		return true
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return false
	}
	return true
}

// BulkStop decides when a loop that sends one request per id must stop. Feed
// it the outcome of every request, in order, with Observe. The zero value is
// ready to use; it is not safe for concurrent use.
//
// The loop stops at the first outcome that is not the server's own answer
// (see IsServerAnswer: no answer, a cancelled context, 401, 403, 429) and
// after MaxConsecutiveServerErrors 5xx answers in a row. Any other answer,
// including a 404, ends a 5xx streak.
type BulkStop struct{ serverErrors int }

// Observe records one request outcome (nil for success) and reports whether
// the loop must stop now, with a short reason for the ids left unattempted.
func (b *BulkStop) Observe(err error) (stop bool, why string) {
	if err == nil {
		b.serverErrors = 0
		return false, ""
	}
	if !IsServerAnswer(err) {
		return true, "stopped after " + err.Error()
	}
	var ae *APIError
	if errors.As(err, &ae) && ae.StatusCode >= http.StatusInternalServerError {
		b.serverErrors++
		if b.serverErrors >= MaxConsecutiveServerErrors {
			return true, fmt.Sprintf("stopped after %d consecutive server errors, the last: %v", b.serverErrors, err)
		}
		return false, ""
	}
	b.serverErrors = 0
	return false, ""
}
