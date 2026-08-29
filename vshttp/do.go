package vshttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Do sends req with bounded retries, reads up to maxBody bytes of the
// response body and returns the response together with the body. The
// returned response body is already closed.
//
// Retries apply only to idempotent requests (GET/HEAD without a body) that
// fail with a transient transport error, a body read failure, or a 429,
// 502, 503 or 504 status. The caller's context always wins.
func Do(ctx context.Context, client *http.Client, policy RetryPolicy, op string, req *http.Request, maxBody int64) (*http.Response, []byte, error) {
	policy = policy.normalize()
	idempotent := req.Body == nil && (req.Method == http.MethodGet || req.Method == http.MethodHead)
	attempts := 1
	if idempotent {
		attempts = policy.MaxAttempts
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, backoff(policy, attempt-1, retryAfterOf(lastErr))); err != nil {
				return nil, nil, &APIError{Operation: op, Method: req.Method, Endpoint: sanitizeURL(req.URL), Kind: KindCanceled, Cause: ctx.Err()}
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = transportError(op, req, err)
			if ctx.Err() != nil || !lastErr.(*APIError).Retryable {
				return nil, nil, lastErr
			}
			continue
		}
		body, readErr := readBody(op, req, resp, maxBody)
		if readErr != nil {
			lastErr = readErr
			if ctx.Err() != nil || !readErr.Retryable {
				return nil, nil, readErr
			}
			continue
		}
		if retryableStatus(resp.StatusCode) && attempt+1 < attempts {
			lastErr = statusError(op, req, resp)
			continue
		}
		return resp, body, nil
	}
	return nil, nil, lastErr
}

// FetchJSON performs a JSON GET-style request: Do, CheckStatus, DecodeJSON.
func FetchJSON(ctx context.Context, client *http.Client, policy RetryPolicy, op string, req *http.Request, maxBody int64, target any) error {
	resp, body, err := Do(ctx, client, policy, op, req, maxBody)
	if err != nil {
		return err
	}
	if err := CheckStatus(op, req, resp); err != nil {
		return err
	}
	return DecodeJSON(op, req, resp, body, target)
}

// CheckStatus returns nil for 2xx responses and a classified *APIError for
// any other status. It must be evaluated before interpreting the body.
func CheckStatus(op string, req *http.Request, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return statusError(op, req, resp)
}

// DecodeJSON unmarshals a fully read 2xx body, preserving the exact JSON
// failure as the cause.
func DecodeJSON(op string, req *http.Request, resp *http.Response, body []byte, target any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return &APIError{Operation: op, Method: req.Method, Endpoint: sanitizeURL(req.URL), StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Kind: KindInvalidResponse, Cause: errors.New("empty response body")}
	}
	if err := json.Unmarshal(body, target); err != nil {
		kind := KindInvalidResponse
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) || errors.Is(err, io.ErrUnexpectedEOF) {
			kind = KindInvalidJSON
		}
		return &APIError{Operation: op, Method: req.Method, Endpoint: sanitizeURL(req.URL), StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Kind: kind, BodySize: int64(len(body)), Cause: err}
	}
	return nil
}

// readBody reads at most maxBody+1 bytes so an oversized response is
// reported explicitly instead of surfacing as a truncated-body error.
func readBody(op string, req *http.Request, resp *http.Response, maxBody int64) ([]byte, *APIError) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, &APIError{Operation: op, Method: req.Method, Endpoint: sanitizeURL(req.URL), StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Kind: KindBodyRead, Retryable: true, BodySize: int64(len(body)), Limit: maxBody, Cause: err}
	}
	if int64(len(body)) > maxBody {
		return nil, &APIError{Operation: op, Method: req.Method, Endpoint: sanitizeURL(req.URL), StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Kind: KindResponseTooLarge, BodySize: int64(len(body)), Limit: maxBody, Cause: fmt.Errorf("response exceeds limit of %d bytes", maxBody)}
	}
	return body, nil
}

func transportError(op string, req *http.Request, err error) *APIError {
	kind, retryable := classify(err)
	return &APIError{Operation: op, Method: req.Method, Endpoint: sanitizeURL(req.URL), Kind: kind, Retryable: retryable, Cause: err}
}

// classify maps a transport error to a kind and retryability.
func classify(err error) (ErrorKind, bool) {
	if errors.Is(err, context.Canceled) {
		return KindCanceled, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindTimeout, true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return KindDNS, true
	}
	var recordErr *tls.RecordHeaderError
	var verifyErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	if errors.As(err, &recordErr) || errors.As(err, &verifyErr) || errors.As(err, &unknownAuthority) || errors.As(err, &hostnameErr) {
		return KindTLS, false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return KindTimeout, true
	}
	return KindNetwork, true
}

func statusError(op string, req *http.Request, resp *http.Response) *APIError {
	e := &APIError{Operation: op, Method: req.Method, Endpoint: sanitizeURL(req.URL), StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Kind: KindHTTPStatus, Cause: fmt.Errorf("unexpected HTTP status %s", resp.Status)}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		e.Kind = KindUnauthorized
	case http.StatusForbidden:
		e.Kind = KindForbidden
	case http.StatusNotFound:
		e.Kind = KindNotFound
	case http.StatusRequestTimeout:
		e.Kind = KindTimeout
		e.Retryable = true
	case http.StatusTooManyRequests:
		e.Kind = KindRateLimit
		e.Retryable = true
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	default:
		if resp.StatusCode >= 500 {
			e.Kind = KindServerError
			e.Retryable = retryableStatus(resp.StatusCode)
		}
	}
	return e
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// parseRetryAfter understands both delta-seconds and HTTP-date forms.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(time.Until(when), 0)
	}
	return 0
}

func backoff(policy RetryPolicy, attempt int, hint time.Duration) time.Duration {
	delay := policy.BaseDelay << attempt
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	// ±25% jitter to avoid thundering herd.
	jitter := time.Duration(rand.Int64N(int64(delay) / 2))
	delay = delay - delay/4 + jitter
	if hint > 0 {
		delay = min(hint, policy.MaxWait)
	}
	return delay
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return &APIError{Kind: KindCanceled, Cause: ctx.Err()}
	case <-timer.C:
		return nil
	}
}

func retryAfterOf(err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter
	}
	return 0
}
