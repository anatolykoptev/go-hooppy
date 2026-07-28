# Changelog

## Unreleased


### Added

* **Retry for transient failures (429/5xx)**: opt-in retry for GET and DELETE requests via `Config.RetryOptions` (uses `go-kit/retry`). POST and streaming uploads NEVER retry (non-idempotent). Context is the sole deadline authority. `MaxElapsedTime` defaults to 30s. `APIError.RetryAfter` parsed from the `Retry-After` header (RFC 7231: seconds + HTTP-date).
* **HTTP client configurability**: `Config.HTTPClient` lets callers inject a pre-configured `*http.Client` (custom transport, pool sizing, TLS, proxies). Follows the go-kit `WithHTTPClient` pattern.

### Changed

* `APIError` struct gains a `RetryAfter time.Duration` field (additive, zero value = no behavior change).
## 1.0.0 (2026-07-28)


### Added

* Go client library, CLI, and MCP server for Hooppy API ([03a6dc8](https://github.com/anatolykoptev/go-hooppy/commit/03a6dc89218c55a37c976eced33e0017bfa3f488))
