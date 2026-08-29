package moddb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

func TestSearchDetailsAndTags(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"downloads":20,"name":"Server Tools","summary":"Admin helpers","author":"Ada","side":"server","type":"mod","tags":["Utility"],"modidstrs":["servertools"],"lastreleased":"2026-08-01 12:00:00"},{"modid":2,"downloads":200,"name":"Warm Light","summary":"Soft lamps","author":"Bea","side":"client","type":"mod","tags":["Graphics"],"lastreleased":"2026-08-02 12:00:00"}]}`)
		case "/mod/1":
			_, _ = io.WriteString(w, `{"statuscode":"200","mod":{"modid":1,"name":"Server Tools","text":"details","author":"Ada","side":"server","releases":[{"releaseid":7,"mainfile":"https://cdn.test/mod.zip","filename":"mod.zip","tags":["1.22.0"],"modversion":"2.0.0"}],"screenshots":["https://cdn.test/a.png"]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	result, e := c.Search(context.Background(), SearchOptions{Text: "ada", Side: SideServer, Page: 1, PageSize: 24})
	if e != nil || result.TotalItems != 1 || result.Items[0].ID != "1" {
		t.Fatalf("%v %#v", e, result)
	}
	details, e := c.Get(context.Background(), "1")
	if e != nil || len(details.Releases) != 1 || len(details.Screenshots) != 1 {
		t.Fatalf("%v %#v", e, details)
	}
	tags, e := c.ListTags(context.Background())
	if e != nil || len(tags) != 2 {
		t.Fatalf("%v %#v", e, tags)
	}
}
func TestHTTPFailures(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 503) }))
	defer s.Close()
	_, e := NewClientWithURL(s.Client(), s.URL).List(context.Background())
	var httpErr *HTTPError
	if !errors.As(e, &httpErr) || !errors.Is(e, ErrUnavailable) {
		t.Fatalf("%v", e)
	}
	if httpErr.StatusCode != 503 || httpErr.Kind != vshttp.KindServerError || !httpErr.Retryable {
		t.Fatalf("%#v", httpErr)
	}
}

func TestMalformedResponse(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{`)
	}))
	defer s.Close()
	_, err := NewClientWithURL(s.Client(), s.URL).List(context.Background())
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("got %v", err)
	}
	// The root JSON cause must survive.
	if !strings.Contains(err.Error(), "unexpected end") {
		t.Fatalf("root cause lost: %v", err)
	}
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != vshttp.KindInvalidJSON {
		t.Fatalf("%#v", err)
	}
}

func TestConcurrentCatalogFetchIsCoalesced(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := c.List(context.Background())
			if err != nil || len(items) != 1 {
				t.Errorf("%v %#v", err, items)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestCacheHitAndRefresh(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	if _, err := c.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("cache miss: requests = %d", requests.Load())
	}
	c.mu.Lock()
	c.catalogAt = time.Now().Add(-catalogCacheTTL)
	c.mu.Unlock()
	if _, err := c.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("refresh: requests = %d", requests.Load())
	}
}

func TestFailedRefreshServesStaleCache(t *testing.T) {
	var fail atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "no", 503)
			return
		}
		_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	first, err := c.List(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.catalogAt = time.Now().Add(-catalogCacheTTL)
	c.mu.Unlock()
	fail.Store(true)
	stale, err := c.List(context.Background())
	if !errors.Is(err, ErrStale) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
	if len(stale) != 1 || stale[0].Name != "A" {
		t.Fatalf("stale data lost: %#v", stale)
	}
	// The failed refresh must not corrupt the cache: recover and verify.
	fail.Store(false)
	c.mu.Lock()
	c.catalogAt = time.Now().Add(-catalogCacheTTL)
	c.mu.Unlock()
	items, err := c.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %#v", err, items)
	}
}

func TestSearchWarningOnPartialEnrichment(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"},{"modid":2,"name":"B","type":"mod"}]}`)
		case "/mod/1":
			_, _ = io.WriteString(w, `{"statuscode":"200","mod":{"modid":1,"name":"A","releases":[{"releaseid":1,"tags":["1.22.0"],"modversion":"1.0.0"}]}}`)
		default:
			http.Error(w, "boom", 500)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	result, err := c.Search(context.Background(), SearchOptions{GameVersion: "1.22.0"})
	if err != nil {
		t.Fatalf("partial failure must not fail the search: %v", err)
	}
	if result.Warning == nil {
		t.Fatal("partial failure must be reported via Warning")
	}
	if len(result.Items) != 1 || result.Items[0].ID != "1" {
		t.Fatalf("%#v", result.Items)
	}
}

func TestGetNotFoundKeepsRootCause(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"statuscode":"404","mod":null}`)
	}))
	defer s.Close()
	_, err := NewClientWithURL(s.Client(), s.URL).Get(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(err.Error(), `statuscode "404"`) {
		t.Fatalf("cause lost: %v", err)
	}
}

func TestConcurrentGetIsCoalesced(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mod/1" {
			requests.Add(1)
			time.Sleep(20 * time.Millisecond)
			_, _ = io.WriteString(w, `{"statuscode":"200","mod":{"modid":1,"name":"A"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), "1"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}
