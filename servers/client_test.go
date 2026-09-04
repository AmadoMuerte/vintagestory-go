package servers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

const catalogFixture = `<div class="server"><b>2 players</b><a href="vintagestoryjoin://one.test:42420">One</a></div><div class="server"><b>8 players</b><a href="vintagestoryjoin://two.test:42420">Two</a></div>`

func TestListCachesAndCopiesResults(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = io.WriteString(w, catalogFixture)
	}))
	defer server.Close()

	client := NewClientWithURL(server.Client(), server.URL)
	first, err := client.List(context.Background())
	if err != nil || len(first) != 2 || first[0].Name != "Two" {
		t.Fatalf("got %#v, %v", first, err)
	}
	first[0].Name = "mutated"
	second, err := client.List(context.Background())
	if err != nil || requests != 1 || second[0].Name != "Two" {
		t.Fatalf("got %#v, requests=%d, err=%v", second, requests, err)
	}

	client.fetchedAt = time.Now().Add(-cacheTTL)
	if _, err := client.List(context.Background()); err != nil || requests != 2 {
		t.Fatalf("requests=%d, err=%v", requests, err)
	}
}

func TestListConcurrentCallsUseOneFetch(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, catalogFixture)
	}))
	defer server.Close()

	client := NewClientWithURL(server.Client(), server.URL)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := client.List(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if requests != 1 {
		t.Fatalf("got %d requests", requests)
	}
}

func TestListFailures(t *testing.T) {
	tests := map[string]struct {
		handler http.HandlerFunc
		wantErr error
	}{
		"status": {
			handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusServiceUnavailable) },
			wantErr: ErrUnavailable,
		},
		"empty catalog": {
			handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `<main>none</main>`) },
			wantErr: ErrInvalidCatalog,
		},
		"oversized catalog": {
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, strings.Repeat(" ", maxResponseBytes+1)+catalogFixture)
			},
			wantErr: ErrInvalidCatalog,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			_, err := NewClientWithURL(server.Client(), server.URL).List(context.Background())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("got %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestListHonorsCanceledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewClientWithURL(server.Client(), server.URL).List(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestOversizedCatalogIsExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", maxResponseBytes+1))
	}))
	defer server.Close()
	_, err := NewClientWithURL(server.Client(), server.URL).List(context.Background())
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("legacy sentinel: %v", err)
	}
	if !errors.Is(err, vshttp.ErrResponseTooLarge) {
		t.Fatalf("expected explicit too-large error: %v", err)
	}
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != vshttp.KindResponseTooLarge || apiErr.Limit != maxResponseBytes {
		t.Fatalf("%#v", err)
	}
}

func TestGetServerDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/s/42" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `<main class="server" data-sid="42"><h1>Detailed</h1><div class="text-section"><p>Full description</p></div></main>`)
	}))
	defer server.Close()

	got, err := NewClientWithURL(server.Client(), server.URL).Get(context.Background(), "42")
	if err != nil || got.ID != "42" || got.Name != "Detailed" || got.FullDescription != "Full description" || got.URL != server.URL+"/s/42" {
		t.Fatalf("got %#v, err=%v", got, err)
	}
}
