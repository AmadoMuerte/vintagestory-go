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
	// The server-side filter already guarantees compatibility, so failed
	// detail lookups degrade the enrichment, not the result set.
	if len(result.Items) != 2 {
		t.Fatalf("%#v", result.Items)
	}
	if len(result.Items[0].GameVersions) != 1 || result.Items[0].GameVersions[0] != "1.22.0" {
		t.Fatalf("enrichment lost: %#v", result.Items[0])
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

func TestListSlowBodySucceedsWithinBudget(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		_, _ = io.WriteString(w, `{"statuscode":"200","mods":[`)
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, `{"modid":1,"name":"A","type":"mod"}]}`)
	}))
	defer s.Close()
	c := NewClientWithURL(&http.Client{Timeout: 2 * time.Second}, s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	items, err := c.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %#v", err, items)
	}
}

func TestListSlowBodyTimesOutAndRetries(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		_, _ = io.WriteString(w, `{"statuscode":"200","mods":[`)
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	c := NewClientWithURL(&http.Client{Timeout: 80 * time.Millisecond}, s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	_, err := c.List(context.Background())
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != vshttp.KindBodyRead || !apiErr.Retryable || apiErr.StatusCode != 200 {
		t.Fatalf("%#v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("root cause lost: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestListRetriesBrokenBodyRead(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			hijackBrokenBody(w)
			return
		}
		_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	items, err := c.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %#v", err, items)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestListRetryExhaustion(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		hijackBrokenBody(w)
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	_, err := c.List(context.Background())
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != vshttp.KindBodyRead {
		t.Fatalf("%#v", err)
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want 3", requests.Load())
	}
}

func TestListNoRetryOnNotFound(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.NotFound(w, nil)
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	_, err := c.List(context.Background())
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != vshttp.KindNotFound {
		t.Fatalf("%#v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestListCancellationDuringBodyRead(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		_, _ = io.WriteString(w, `{"statuscode":"200","mods":[`)
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	c := NewClientWithURL(&http.Client{Timeout: 300 * time.Millisecond}, s.URL)
	start := time.Now()
	_, err := c.List(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancel not prompt: %v", elapsed)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

// hijackBrokenBody answers 200 with a declared Content-Length and then drops
// the connection mid-body, so the client sees an unexpected EOF during the
// body read.
func hijackBrokenBody(w http.ResponseWriter) {
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 4096\r\n\r\n{\"statuscode\":\"200\",\"mods\":[")
	_ = buf.Flush()
	_ = conn.Close()
}

func TestSearchGameVersionUsesServerFilter(t *testing.T) {
	var modsRequests atomic.Int32
	var sawGameVersion atomic.Bool
	var detailRequests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			modsRequests.Add(1)
			if r.URL.Query().Get("gameversion") == "1.20" {
				sawGameVersion.Store(true)
			}
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"Alpha","type":"mod"},{"modid":2,"name":"Beta","type":"mod"}]}`)
		case "/mod/1", "/mod/2":
			detailRequests.Add(1)
			_, _ = io.WriteString(w, `{"statuscode":"200","mod":{"modid":`+strings.TrimPrefix(r.URL.Path, "/mod/")+`,"name":"A","releases":[{"releaseid":1,"tags":["1.20.7"],"modversion":"2.0.0"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	result, err := c.Search(context.Background(), SearchOptions{GameVersion: "1.20.x", Page: 1, PageSize: 24})
	if err != nil {
		t.Fatal(err)
	}
	if !sawGameVersion.Load() {
		t.Fatal("server filter not used")
	}
	if modsRequests.Load() != 1 {
		t.Fatalf("mods requests = %d, want 1", modsRequests.Load())
	}
	if result.TotalItems != 2 || len(result.Items) != 2 {
		t.Fatalf("%#v", result)
	}
	if len(result.Items[0].GameVersions) != 1 || result.Items[0].GameVersions[0] != "1.20.7" {
		t.Fatalf("page enrichment missing: %#v", result.Items[0])
	}
	if detailRequests.Load() != 2 {
		t.Fatalf("detail requests = %d, want 2", detailRequests.Load())
	}
}

func TestSearchGameVersionExactUsesGv(t *testing.T) {
	var sawGv atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			if r.URL.Query().Get("gv") == "1.20.7" {
				sawGv.Store(true)
			}
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	if _, err := c.Search(context.Background(), SearchOptions{GameVersion: "1.20.7"}); err != nil {
		t.Fatal(err)
	}
	if !sawGv.Load() {
		t.Fatal("gv parameter not used")
	}
}

func TestSearchGameVersionInvalidFallsBackToCatalog(t *testing.T) {
	var plainRequests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			if len(r.URL.RawQuery) == 0 {
				plainRequests.Add(1)
			}
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	result, err := c.Search(context.Background(), SearchOptions{GameVersion: "garbage!!"})
	if err != nil {
		t.Fatal(err)
	}
	if plainRequests.Load() == 0 {
		t.Fatal("expected fallback to the plain catalog")
	}
	if result.TotalItems != 1 {
		t.Fatalf("%#v", result)
	}
}

func TestSearchGameVersionCacheHit(t *testing.T) {
	var modsRequests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			modsRequests.Add(1)
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	for range 2 {
		if _, err := c.Search(context.Background(), SearchOptions{GameVersion: "1.20.x"}); err != nil {
			t.Fatal(err)
		}
	}
	if modsRequests.Load() != 1 {
		t.Fatalf("mods requests = %d, want 1", modsRequests.Load())
	}
}

func TestSearchGameVersionSingleflight(t *testing.T) {
	var modsRequests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			modsRequests.Add(1)
			time.Sleep(20 * time.Millisecond)
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Search(context.Background(), SearchOptions{GameVersion: "1.20.x"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if modsRequests.Load() != 1 {
		t.Fatalf("mods requests = %d, want 1", modsRequests.Load())
	}
}

func TestSearchGameVersionStaleOnError(t *testing.T) {
	var fail atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			if fail.Load() {
				http.Error(w, "no", 503)
				return
			}
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	if _, err := c.Search(context.Background(), SearchOptions{GameVersion: "1.20.x"}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	entry := c.catalogByGV["gameversion=1.20"]
	entry.at = time.Now().Add(-catalogCacheTTL)
	c.catalogByGV["gameversion=1.20"] = entry
	c.mu.Unlock()
	fail.Store(true)
	result, err := c.Search(context.Background(), SearchOptions{GameVersion: "1.20.x"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Warning == nil || !errors.Is(result.Warning, ErrStale) {
		t.Fatalf("stale data must be served with ErrStale warning: %v", result.Warning)
	}
	if result.TotalItems != 1 {
		t.Fatalf("%#v", result)
	}
}

func TestSearchGameVersionCancellationDuringFetch(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		_, _ = io.WriteString(w, `{"statuscode":"200","mods":[`)
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	c := NewClientWithURL(&http.Client{Timeout: 300 * time.Millisecond}, s.URL)
	start := time.Now()
	_, err := c.Search(ctx, SearchOptions{GameVersion: "1.20.x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancel not prompt: %v", elapsed)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestSearchGameVersionEnrichesOnlyPage(t *testing.T) {
	var modsRequests atomic.Int32
	var detailRequests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mods":
			modsRequests.Add(1)
			_, _ = io.WriteString(w, `{"statuscode":"200","mods":[{"modid":1,"name":"A","type":"mod"},{"modid":2,"name":"B","type":"mod"},{"modid":3,"name":"C","type":"mod"}]}`)
		default:
			detailRequests.Add(1)
			_, _ = io.WriteString(w, `{"statuscode":"200","mod":{"modid":`+strings.TrimPrefix(r.URL.Path, "/mod/")+`,"name":"A","releases":[{"releaseid":1,"tags":["1.20.7"],"modversion":"1.0.0"}]}}`)
		}
	}))
	defer s.Close()
	c := NewClientWithURL(s.Client(), s.URL)
	c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	result, err := c.Search(context.Background(), SearchOptions{GameVersion: "1.20.x", Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if modsRequests.Load() != 1 {
		t.Fatalf("mods requests = %d, want 1", modsRequests.Load())
	}
	if detailRequests.Load() != 2 {
		t.Fatalf("detail requests = %d, want 2 (page size)", detailRequests.Load())
	}
	if len(result.Items) != 2 {
		t.Fatalf("%#v", result.Items)
	}
}
