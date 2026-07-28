package hooppy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// errorsAs is a thin wrapper around errors.As to keep the import local.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}

// Client is the Hooppy API client. It is safe for concurrent use.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// Config holds parameters for constructing a Client.
type Config struct {
	BaseURL string        // overrides DefaultBaseURL if non-empty
	Token   string        // JWT bearer token (required)
	Timeout time.Duration // per-request timeout; default 30s
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
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   cfg.Token,
		http:    &http.Client{Timeout: timeout},
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
	return NewClient(Config{
		BaseURL: os.Getenv("HOOPPY_BASE_URL"),
		Token:   token,
	})
}

// doGET performs a GET request and decodes the JSON response into out.
func (c *Client) doGET(ctx context.Context, path string, params url.Values, out interface{}) error {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("hooppy: build request: %w", err)
	}
	c.setAuth(req)
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

// doDELETE performs a DELETE request and decodes the response.
func (c *Client) doDELETE(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("hooppy: build request: %w", err)
	}
	c.setAuth(req)
	return c.do(req, out)
}

// doMultipart performs a multipart/form-data POST with a file field.
func (c *Client) doMultipart(ctx context.Context, path, fileField, filename string, fileData []byte, extraFields map[string]string, out interface{}) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range extraFields {
		if err := w.WriteField(k, v); err != nil {
			return fmt.Errorf("hooppy: write field %s: %w", k, err)
		}
	}
	fw, err := w.CreateFormFile(fileField, filename)
	if err != nil {
		return fmt.Errorf("hooppy: create form file: %w", err)
	}
	if _, err := fw.Write(fileData); err != nil {
		return fmt.Errorf("hooppy: write file: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("hooppy: close multipart: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, &buf)
	if err != nil {
		return fmt.Errorf("hooppy: build request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", w.FormDataContentType())
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
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("hooppy: decode response: %w", err)
	}
	return nil
}
