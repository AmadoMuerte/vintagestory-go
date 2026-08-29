package vshttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func get(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func fetch(t *testing.T, url string, policy RetryPolicy, maxBody int64, target any) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return FetchJSON(context.Background(), http.DefaultClient, policy, "test: op", req, maxBody, target)
}

func fastRetry() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, MaxWait: 50 * time.Millisecond}
}

func TestOKJSON(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"ada"}`)
	}))
	defer s.Close()
	var out struct {
		Name string `json:"name"`
	}
	if err := fetch(t, s.URL, fastRetry(), 1<<20, &out); err != nil || out.Name != "ada" {
		t.Fatalf("%v %#v", err, out)
	}
}

func TestMalformedJSON(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{`)
	}))
	defer s.Close()
	var out map[string]any
	err := fetch(t, s.URL, fastRetry(), 1<<20, &out)
	var apiErr *APIError
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindInvalidJSON {
		t.Fatalf("kind: %v", err)
	}
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("generic sentinel: %v", err)
	}
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("root cause lost: %v", err)
	}
	if apiErr.Retryable {
		t.Fatal("decode failure must not be retryable")
	}
}

func TestTruncatedJSON(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":["a","b`)
	}))
	defer s.Close()
	var out map[string]any
	err := fetch(t, s.URL, fastRetry(), 1<<20, &out)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindInvalidJSON {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(err.Error(), "unexpected end") {
		t.Fatalf("cause not visible: %v", err)
	}
}

func TestEmptyBody(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer s.Close()
	var out map[string]any
	err := fetch(t, s.URL, fastRetry(), 1<<20, &out)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindInvalidResponse {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(err.Error(), "empty response body") {
		t.Fatalf("%v", err)
	}
}

func TestWrongJSONTypes(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"count":"not-a-number"}`)
	}))
	defer s.Close()
	var out struct {
		Count int `json:"count"`
	}
	err := fetch(t, s.URL, fastRetry(), 1<<20, &out)
	var apiErr *APIError
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindInvalidResponse {
		t.Fatalf("kind: %v", err)
	}
	if !errors.As(err, &typeErr) {
		t.Fatalf("type error lost: %v", err)
	}
}

func TestResponseTooLarge(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", 2048))
	}))
	defer s.Close()
	var out map[string]any
	err := fetch(t, s.URL, fastRetry(), 1024, &out)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindResponseTooLarge {
		t.Fatalf("kind: %v", err)
	}
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("sentinel: %v", err)
	}
	if apiErr.Limit != 1024 || apiErr.BodySize <= apiErr.Limit {
		t.Fatalf("sizes: %#v", apiErr)
	}
	if strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("limit must not surface as truncation: %v", err)
	}
}

func TestHTTPStatuses(t *testing.T) {
	cases := []struct {
		code      int
		kind      ErrorKind
		sentinel  error
		retryable bool
	}{
		{400, KindHTTPStatus, nil, false},
		{401, KindUnauthorized, ErrUnauthorized, false},
		{403, KindForbidden, ErrForbidden, false},
		{404, KindNotFound, ErrNotFound, false},
		{429, KindRateLimit, ErrRateLimited, true},
		{500, KindServerError, ErrServer, false},
		{502, KindServerError, ErrServer, true},
		{503, KindServerError, ErrServer, true},
		{504, KindServerError, ErrServer, true},
	}
	for _, tc := range cases {
		var attempts atomic.Int32
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			if tc.code == 429 {
				w.Header().Set("Retry-After", "1")
			}
			http.Error(w, "no", tc.code)
		}))
		var out map[string]any
		err := fetch(t, s.URL, fastRetry(), 1<<20, &out)
		s.Close()
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%d: not an APIError: %v", tc.code, err)
		}
		if apiErr.Kind != tc.kind || apiErr.StatusCode != tc.code || apiErr.Retryable != tc.retryable {
			t.Fatalf("%d: %#v", tc.code, apiErr)
		}
		if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
			t.Fatalf("%d: sentinel %v missing in %v", tc.code, tc.sentinel, err)
		}
		if tc.code == 429 && apiErr.RetryAfter != time.Second {
			t.Fatalf("429: RetryAfter = %v", apiErr.RetryAfter)
		}
		wantAttempts := int32(1)
		if tc.retryable {
			wantAttempts = 3
		}
		if attempts.Load() != wantAttempts {
			t.Fatalf("%d: attempts = %d, want %d", tc.code, attempts.Load(), wantAttempts)
		}
	}
}

func TestStatusCheckedBeforeBody(t *testing.T) {
	// An HTML 500 page must be a server error, not a JSON decode error.
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `<html><body>oops</body></html>`)
	}))
	defer s.Close()
	var out map[string]any
	err := fetch(t, s.URL, fastRetry(), 1<<20, &out)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindServerError || apiErr.StatusCode != 500 {
		t.Fatalf("%v", err)
	}
}

func TestSuccessfulRetryAfterTransientFailure(t *testing.T) {
	var attempts atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "no", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer s.Close()
	var out struct {
		OK bool `json:"ok"`
	}
	if err := fetch(t, s.URL, fastRetry(), 1<<20, &out); err != nil || !out.OK {
		t.Fatalf("%v %#v", err, out)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
}

func TestRetryRespectsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", 429)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer s.Close()
	start := time.Now()
	var out struct {
		OK bool `json:"ok"`
	}
	policy := fastRetry()
	policy.MaxWait = 2 * time.Second
	if err := fetch(t, s.URL, policy, 1<<20, &out); err != nil || !out.OK {
		t.Fatalf("%v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("Retry-After not respected: %v", elapsed)
	}
}

func TestTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer s.Close()
	client := &http.Client{Timeout: 50 * time.Millisecond}
	req := get(t, s.URL)
	policy := RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	_, _, err := Do(context.Background(), client, policy, "test: op", req, 1<<20)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindTimeout || !apiErr.Retryable {
		t.Fatalf("%#v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("root cause lost: %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	_, _, err := Do(ctx, s.Client(), fastRetry(), "test: op", req, 1<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Retryable {
		t.Fatal("cancellation must not be retryable")
	}
}

func TestRetryStopsOnCancel(t *testing.T) {
	var attempts atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "no", 503)
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, _, err := Do(ctx, s.Client(), RetryPolicy{MaxAttempts: 10, BaseDelay: 200 * time.Millisecond, MaxDelay: time.Second}, "test: op", req, 1<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if attempts.Load() > 2 {
		t.Fatalf("retries continued after cancel: %d", attempts.Load())
	}
}

func TestConnectionRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	var out map[string]any
	err = fetch(t, "http://"+addr, fastRetry(), 1<<20, &out)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindNetwork {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(err.Error(), "test: op") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("context or cause missing: %v", err)
	}
}

// failingBody errors midway through the read to emulate a reset connection.
type failingBody struct{ sent bool }

func (b *failingBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, errors.New("simulated connection reset")
	}
	b.sent = true
	return copy(p, `{"partial":`), nil
}
func (b *failingBody) Close() error { return nil }

type failingTransport struct{}

func (failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       &failingBody{},
		Request:    req,
	}, nil
}

func TestBodyReadFailure(t *testing.T) {
	var attempts atomic.Int32
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return failingTransport{}.RoundTrip(req)
	})
	client := &http.Client{Transport: transport}
	req := get(t, "http://example.test/api")
	_, _, err := Do(context.Background(), client, fastRetry(), "test: op", req, 1<<20)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != KindBodyRead {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(err.Error(), "simulated connection reset") {
		t.Fatalf("cause lost: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("body read failure should be retried for GET: %d", attempts.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNoRetryForPost(t *testing.T) {
	var attempts atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "no", 503)
	}))
	defer s.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, s.URL, strings.NewReader("x"))
	resp, _, err := Do(context.Background(), s.Client(), fastRetry(), "test: op", req, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckStatus("test: op", req, resp); err == nil {
		t.Fatal("expected error")
	}
	if attempts.Load() != 1 {
		t.Fatalf("POST must not be retried: %d", attempts.Load())
	}
}

func TestEndpointSanitized(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", 400)
	}))
	defer s.Close()
	var out map[string]any
	err := fetch(t, s.URL+"/?sessionkey=SECRET&password=SECRET", fastRetry(), 1<<20, &out)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("%v", err)
	}
	if strings.Contains(apiErr.Endpoint, "SECRET") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("credentials leaked: %q / %v", apiErr.Endpoint, err)
	}
}

func TestAPIErrorUnwrapAndAs(t *testing.T) {
	root := errors.New("root cause")
	err := &APIError{Operation: "op", Kind: KindNetwork, Cause: root}
	if !errors.Is(err, root) {
		t.Fatal("Unwrap chain broken")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Cause != root {
		t.Fatal("As broken")
	}
	if got := err.Error(); !strings.Contains(got, "op") || !strings.Contains(got, "root cause") {
		t.Fatalf("%q", got)
	}
}
