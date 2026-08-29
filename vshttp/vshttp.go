// Package vshttp provides shared HTTP plumbing for the vintagestory-go
// clients: structured API errors, bounded response reads, status
// classification and a bounded retry policy.
//
// All errors produced by this package are *APIError values. They support
// errors.Is (generic sentinels, per-package legacy sentinels and wrapped
// causes such as context.DeadlineExceeded) and errors.As (*APIError).
package vshttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Generic sentinels matched by APIError.Is based on the error kind.
var (
	ErrInvalidResponse  = errors.New("invalid response")
	ErrResponseTooLarge = errors.New("response too large")
	ErrRateLimited      = errors.New("rate limited")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrForbidden        = errors.New("forbidden")
	ErrNotFound         = errors.New("not found")
	ErrServer           = errors.New("server error")
)

// ErrorKind classifies an APIError.
type ErrorKind string

const (
	KindUnknown          ErrorKind = "unknown"
	KindNetwork          ErrorKind = "network"
	KindTimeout          ErrorKind = "timeout"
	KindCanceled         ErrorKind = "cancelled"
	KindDNS              ErrorKind = "dns"
	KindTLS              ErrorKind = "tls"
	KindHTTPStatus       ErrorKind = "http_status"
	KindRateLimit        ErrorKind = "rate_limit"
	KindUnauthorized     ErrorKind = "unauthorized"
	KindForbidden        ErrorKind = "forbidden"
	KindNotFound         ErrorKind = "not_found"
	KindServerError      ErrorKind = "server_error"
	KindResponseTooLarge ErrorKind = "response_too_large"
	KindBodyRead         ErrorKind = "body_read"
	KindInvalidJSON      ErrorKind = "invalid_json"
	KindInvalidResponse  ErrorKind = "invalid_response"
	KindValidation       ErrorKind = "validation"
)

// APIError describes a failed HTTP/API operation. The original error is
// always preserved in Cause and reachable via errors.Unwrap/errors.Is.
type APIError struct {
	Operation   string        // logical operation, e.g. "moddb: list catalog"
	Method      string        // HTTP method
	Endpoint    string        // sanitized URL (no query, fragment or userinfo)
	StatusCode  int           // HTTP status, 0 when no response was received
	ContentType string        // response Content-Type, when available
	Kind        ErrorKind     // machine-readable category
	Retryable   bool          // whether retrying may succeed
	RetryAfter  time.Duration // server-provided Retry-After, when present
	BodySize    int64         // bytes read, for body-related failures
	Limit       int64         // configured body limit, for KindResponseTooLarge
	Legacy      error         // optional per-package sentinel kept for errors.Is compatibility
	Cause       error         // original root cause
}

func (e *APIError) Error() string {
	what := e.Operation
	if what == "" {
		what = "request"
	}
	where := e.Method + " " + e.Endpoint
	if e.Method == "" {
		where = e.Endpoint
	}
	detail := ""
	switch {
	case e.Cause != nil:
		detail = e.Cause.Error()
	case e.StatusCode != 0:
		detail = fmt.Sprintf("HTTP %d", e.StatusCode)
	default:
		detail = string(e.Kind)
	}
	if e.Kind == KindResponseTooLarge {
		detail = fmt.Sprintf("response exceeds limit of %d bytes (read %d)", e.Limit, e.BodySize)
	}
	if where != "" && where != " " {
		return fmt.Sprintf("%s: %s: %s", what, where, detail)
	}
	return fmt.Sprintf("%s: %s", what, detail)
}

// Unwrap returns the original root cause.
func (e *APIError) Unwrap() error { return e.Cause }

// Is matches the legacy per-package sentinel and the generic vshttp
// sentinels for the error kind.
func (e *APIError) Is(target error) bool {
	if target == nil {
		return false
	}
	if e.Legacy != nil && target == e.Legacy {
		return true
	}
	switch target {
	case ErrRateLimited:
		return e.Kind == KindRateLimit
	case ErrResponseTooLarge:
		return e.Kind == KindResponseTooLarge
	case ErrInvalidResponse:
		return e.Kind == KindInvalidResponse || e.Kind == KindInvalidJSON
	case ErrUnauthorized:
		return e.Kind == KindUnauthorized
	case ErrForbidden:
		return e.Kind == KindForbidden
	case ErrNotFound:
		return e.Kind == KindNotFound
	case ErrServer:
		return e.Kind == KindServerError
	}
	return false
}

// WithLegacy sets the per-package compatibility sentinel and returns e.
func (e *APIError) WithLegacy(sentinel error) *APIError {
	e.Legacy = sentinel
	return e
}

// RetryPolicy controls bounded retries of idempotent requests.
type RetryPolicy struct {
	MaxAttempts int           // total attempts, 1 disables retries
	BaseDelay   time.Duration // first backoff delay
	MaxDelay    time.Duration // backoff cap
	MaxWait     time.Duration // cap applied to server Retry-After hints
}

// DefaultRetryPolicy allows two extra attempts with a short backoff, so a
// struggling upstream cannot stall a call for more than a few seconds.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: 300 * time.Millisecond, MaxDelay: 2 * time.Second, MaxWait: 10 * time.Second}
}

func (p RetryPolicy) normalize() RetryPolicy {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = 300 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 2 * time.Second
	}
	if p.MaxWait <= 0 {
		p.MaxWait = 10 * time.Second
	}
	return p
}

// DefaultTransport returns an http.Transport with bounded dial, TLS and
// header timeouts suitable for the Vintage Story services.
func DefaultTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          20,
	}
}

// DefaultClient returns an http.Client with a bounded transport and an
// overall timeout ceiling for full body reads.
func DefaultClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: DefaultTransport()}
}

// sanitizeURL strips query, fragment and credentials from u so error
// messages never leak sensitive parameters.
func sanitizeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.RawQuery = ""
	c.RawFragment = ""
	c.Fragment = ""
	c.User = nil
	return c.String()
}
