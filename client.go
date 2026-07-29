package hooppy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/anatolykoptev/go-kit/retry"
	"github.com/golang-jwt/jwt/v5"
)

// MaxUploadBytes is the default upper limit on file upload size (50 MB).
// Override via Config.MaxUploadBytes.
const MaxUploadBytes int64 = 50 << 20

// MaxResponseBytes is the default upper limit on HTTP response body size (10 MB).
// Override via Config.MaxResponseBytes.
const MaxResponseBytes int64 = 10 << 20

// errorsAs is a thin wrapper around errors.As to keep the import local.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}

// Client is the Hooppy API client. It is safe for concurrent use.
type Client struct {
	baseURL          string
	token            string
	http             *http.Client
	maxUploadBytes   int64
	maxResponseBytes int64
	retryOpts        *retry.Options
}

// Config holds parameters for constructing a Client.
type Config struct {
	BaseURL          string         // overrides DefaultBaseURL if non-empty
	Token            string         // JWT bearer token (required)
	Timeout          time.Duration  // per-request header timeout; default 30s (controls ResponseHeaderTimeout, NOT total request time — context is the sole deadline authority)
	MaxUploadBytes   int64          // max file size for uploads; default 50 MB
	MaxResponseBytes int64          // max response body size; default 10 MB
	HTTPClient       *http.Client   // if non-nil, overrides the default *http.Client (caller owns transport config — pool sizing, TLS, proxies). Follows the go-kit WithHTTPClient pattern.
	RetryOptions     *retry.Options // if non-nil, enables retry for safe (idempotent) requests on transient failures (429/5xx): GET, DELETE, and full-state PUT updates to a known id (Update*). Create-shaped calls NEVER retry regardless of method — POST creates, streaming uploads, and the create-shaped PUTs (PUT /posts/import, /posts/copy, /posts/{mode}) are non-idempotent and a retry after a committed write would duplicate created posts. Retry eligibility is declared per call site (the retryable arg on doGET/doPUT/doDELETE) and enforced by TestRetryPolicySweep. Context is the sole deadline authority. Use OnRetry for observability.
}

// NewClient creates a new Hooppy API client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Token == "" {
		return nil, errors.New("hooppy: token is required")
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("hooppy: invalid base URL: %w", err)
	}
	// Pre-validate JWT expiry if the token looks like a JWT (3 dot-separated parts).
	// We parse without signature verification (we don't have the signing key);
	// the goal is to fail fast on expired tokens instead of waiting for a 401.
	if err := checkJWTExpiry(cfg.Token); err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	maxUpload := cfg.MaxUploadBytes
	if maxUpload == 0 {
		maxUpload = MaxUploadBytes
	}
	maxResp := cfg.MaxResponseBytes
	if maxResp == 0 {
		maxResp = MaxResponseBytes
	}
	// Granular Transport timeouts instead of http.Client.Timeout.
	// The context passed to each request is the sole deadline authority.
	// ResponseHeaderTimeout guards against a server that accepts the connection
	// but never responds (set from cfg.Timeout).
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   10,
	}
	httpClient := &http.Client{Transport: transport}
	if cfg.HTTPClient != nil {
		httpClient = cfg.HTTPClient
	}
	return &Client{
		baseURL:          strings.TrimRight(baseURL, "/"),
		token:            cfg.Token,
		http:             httpClient,
		maxUploadBytes:   maxUpload,
		maxResponseBytes: maxResp,
		retryOpts:        cfg.RetryOptions,
	}, nil
}

// NewClientFromEnv creates a client using HOOPPY_TOKEN (and optionally
// HOOPPY_BASE_URL) from the environment. If HOOPPY_TOKEN is unset, it
// falls back to ~/.config/hooppy/token.
func NewClientFromEnv() (*Client, error) {
	token := os.Getenv("HOOPPY_TOKEN")
	if token == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			data, err := os.ReadFile(home + "/.config/hooppy/token")
			if err == nil {
				token = strings.TrimSpace(string(data))
			}
		}
	}
	if token == "" {
		return nil, errors.New("hooppy: token not found — set HOOPPY_TOKEN env var or create ~/.config/hooppy/token")
	}
	return NewClient(Config{
		BaseURL: os.Getenv("HOOPPY_BASE_URL"),
		Token:   token,
	})
}

// checkJWTExpiry parses the token as a JWT (without signature verification)
// and returns an error if the exp claim is in the past. Non-JWT tokens (no
// exp claim or not 3-part dot-separated) are silently accepted for backward
// compatibility with opaque tokens.
func checkJWTExpiry(token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil // not a JWT, skip check
	}
	var claims jwt.RegisteredClaims
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	if _, _, err := parser.ParseUnverified(token, &claims); err != nil {
		return nil // can't parse, let the API decide
	}
	if claims.ExpiresAt == nil {
		return nil // no exp claim, skip
	}
	if claims.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("hooppy: token expired at %s", claims.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

// doGET performs a GET request and decodes the JSON response into out.
// retryable declares the call site's retry policy: when true AND
// Config.RetryOptions is non-nil, transient failures (429/5xx) are retried
// with exponential backoff; when false the call never retries even with
// RetryOptions set. Retry eligibility is a property of the ENDPOINT, not the
// HTTP method — every call site MUST pass an explicit value so a new endpoint
// is forced to declare its policy rather than silently inheriting the method
// default. Every current GET is a safe idempotent read, so all doGET call
// sites pass true. The declaration is enforced by TestRetryPolicySweep, which
// fails when a request-issuing method exists with no declared policy.
func (c *Client) doGET(ctx context.Context, path string, params url.Values, out interface{}, retryable bool) error {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	buildReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("hooppy: build request: %w", err)
		}
		c.setAuth(req)
		return req, nil
	}
	if retryable && c.retryOpts != nil {
		return c.doWithRetry(ctx, buildReq, out)
	}
	req, err := buildReq()
	if err != nil {
		return err
	}
	return c.do(req, out)
}

// doPOST performs a POST request with a JSON body and decodes the response.
func (c *Client) doPOST(ctx context.Context, path string, body interface{}, out interface{}) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return fmt.Errorf("hooppy: encode body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, &buf)
	if err != nil {
		return fmt.Errorf("hooppy: build request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

// doPUT performs a PUT request with a JSON body and decodes the response.
// retryable declares the call site's retry policy (see doGET). PUT is the
// only verb whose retry eligibility is NOT uniform: a full-state Update* to a
// known id converges on re-send (retryable=true, safe), but a create-shaped
// PUT (PUT /posts/import, /posts/copy, /posts/{mode}) creates objects and
// MUST pass false — a 5xx or timeout arriving after the write committed,
// retried, would duplicate the created posts in a live publishing queue
// (issue #87). The per-call-site declaration is enforced by
// TestRetryPolicySweep; the create-never-retries invariant is pinned
// behaviourally by TestRetryPolicy_CreateNotRetried.
func (c *Client) doPUT(ctx context.Context, path string, body interface{}, out interface{}, retryable bool) error {
	buildReq := func() (*http.Request, error) {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, fmt.Errorf("hooppy: encode body: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, &buf)
		if err != nil {
			return nil, fmt.Errorf("hooppy: build request: %w", err)
		}
		c.setAuth(req)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}
	if retryable && c.retryOpts != nil {
		return c.doWithRetry(ctx, buildReq, out)
	}
	req, err := buildReq()
	if err != nil {
		return err
	}
	return c.do(req, out)
}

// doGETRaw performs a GET request and returns the raw response body bytes,
// without JSON-decoding into a Go struct. Used by UpdateScheduleFromEdit to
// fetch the full /edit response (72 keys) as raw bytes so unmodelled fields
// are preserved through the read-modify-write cycle — decoding into a Go
// struct would silently drop them.
func (c *Client) doGETRaw(ctx context.Context, path string) ([]byte, error) {
	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("hooppy: build request: %w", err)
	}
	c.setAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hooppy: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newAPIError(resp)
	}
	limited := io.LimitReader(resp.Body, c.maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("hooppy: read response: %w", err)
	}
	if int64(len(data)) > c.maxResponseBytes {
		return nil, fmt.Errorf("hooppy: response exceeds max size %d bytes", c.maxResponseBytes)
	}
	return data, nil
}

// doPUTRaw performs a PUT request with a pre-encoded JSON body (raw bytes)
// and decodes the JSON response into out. Used by UpdateScheduleFromEdit to
// send the complete 72-key state object without re-marshalling through a Go
// struct (which would drop unmodelled fields).
func (c *Client) doPUTRaw(ctx context.Context, path string, body []byte, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("hooppy: build request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

// doDELETE performs a DELETE request and decodes the response.
// retryable declares the call site's retry policy (see doGET). When true AND
// Config.RetryOptions is non-nil, transient failures (429/5xx) are retried
// with exponential backoff. DELETE is idempotent per RFC 9110 §9.3.2, so all
// doDELETE call sites pass true. The declaration is enforced by
// TestRetryPolicySweep.
func (c *Client) doDELETE(ctx context.Context, path string, out interface{}, retryable bool) error {
	buildReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
		if err != nil {
			return nil, fmt.Errorf("hooppy: build request: %w", err)
		}
		c.setAuth(req)
		return req, nil
	}
	if retryable && c.retryOpts != nil {
		return c.doWithRetry(ctx, buildReq, out)
	}
	req, err := buildReq()
	if err != nil {
		return err
	}
	return c.do(req, out)
}

// doMultipart performs a multipart/form-data POST with a file field.
// fileData is the raw file content; for streaming uploads from a file handle,
// use doMultipartStream instead.
func (c *Client) doMultipart(ctx context.Context, path, fileField, filename string, fileData []byte, extraFields map[string]string, out interface{}) error {
	return c.doMultipartStream(ctx, path, fileField, filename, int64(len(fileData)), bytes.NewReader(fileData), extraFields, out)
}

// doMultipartStream performs a streaming multipart/form-data POST using io.Pipe.
// The file content is read from body (an io.Reader) and streamed to the server
// without buffering the entire multipart body in memory. fileSize is used for
// the Content-Length header when known (pass -1 if unknown).
func (c *Client) doMultipartStream(ctx context.Context, path, fileField, filename string, fileSize int64, body io.Reader, extraFields map[string]string, out interface{}) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		for k, v := range extraFields {
			if err := mw.WriteField(k, v); err != nil {
				pw.CloseWithError(fmt.Errorf("hooppy: write field %s: %w", k, err))
				return
			}
		}
		fw, err := mw.CreateFormFile(fileField, filename)
		if err != nil {
			pw.CloseWithError(fmt.Errorf("hooppy: create form file: %w", err))
			return
		}
		if _, err := io.Copy(fw, body); err != nil {
			pw.CloseWithError(fmt.Errorf("hooppy: write file: %w", err))
			return
		}
		if err := mw.Close(); err != nil {
			pw.CloseWithError(fmt.Errorf("hooppy: close multipart: %w", err))
			return
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, pr)
	if err != nil {
		// Close the pipe reader so the writer goroutine exits instead of leaking.
		pr.Close()
		return fmt.Errorf("hooppy: build request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req, out)
}

func (c *Client) setAuth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
}

func (c *Client) do(req *http.Request, out interface{}) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hooppy: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newAPIError(resp)
	}
	if out == nil {
		return nil
	}
	// Cap response body size to prevent OOM on malicious/buggy server responses.
	limited := io.LimitReader(resp.Body, c.maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("hooppy: read response: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("hooppy: empty response body for HTTP %d", resp.StatusCode)
	}
	if int64(len(data)) > c.maxResponseBytes {
		return fmt.Errorf("hooppy: response exceeds max size %d bytes", c.maxResponseBytes)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("hooppy: decode response: %w", err)
	}
	return nil
}

// doWithRetry wraps a request-builder closure with retry.Do for transient
// failures (429/5xx). Non-retryable APIErrors (4xx other than 429) and
// body-read/decode errors are wrapped with retry.Permanent to stop
// immediately. Retryable APIErrors with a Retry-After header use
// retry.RetryAfter to override the exponential backoff. MaxElapsedTime
// defaults to 30s if unset. Context is the sole deadline authority —
// retry.Do respects ctx.Done().
func (c *Client) doWithRetry(ctx context.Context, buildReq func() (*http.Request, error), out interface{}) error {
	opts := *c.retryOpts // copy so applyDefaults doesn't mutate the caller's Options
	if opts.MaxElapsedTime == 0 {
		opts.MaxElapsedTime = 30 * time.Second
	}
	_, err := retry.Do[struct{}](ctx, opts, func() (struct{}, error) {
		req, err := buildReq()
		if err != nil {
			return struct{}{}, retry.Permanent(err)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return struct{}{}, fmt.Errorf("hooppy: request: %w", err) // transient network error, retryable
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			ae := newAPIError(resp)
			if isRetryableStatus(resp.StatusCode) {
				if ae.RetryAfter > 0 {
					return struct{}{}, retry.RetryAfter(ae.RetryAfter, ae)
				}
				return struct{}{}, ae // retryable, bare error
			}
			return struct{}{}, retry.Permanent(ae) // non-retryable 4xx
		}
		if out == nil {
			return struct{}{}, nil
		}
		limited := io.LimitReader(resp.Body, c.maxResponseBytes+1)
		data, err := io.ReadAll(limited)
		if err != nil {
			return struct{}{}, retry.Permanent(fmt.Errorf("hooppy: read response: %w", err))
		}
		if len(data) == 0 {
			return struct{}{}, retry.Permanent(fmt.Errorf("hooppy: empty response body for HTTP %d", resp.StatusCode))
		}
		if int64(len(data)) > c.maxResponseBytes {
			return struct{}{}, retry.Permanent(fmt.Errorf("hooppy: response exceeds max size %d bytes", c.maxResponseBytes))
		}
		if err := json.Unmarshal(data, out); err != nil {
			return struct{}{}, retry.Permanent(fmt.Errorf("hooppy: decode response: %w", err))
		}
		return struct{}{}, nil
	})
	return err
}
