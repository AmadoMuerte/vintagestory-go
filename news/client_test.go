package news

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

func TestClientUsesUserAgentAndPrimaryFeed(t *testing.T) {
	fixture, err := os.ReadFile("testdata/blog.xml")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.UserAgent() != "Waxlight/1.2.3" {
			t.Errorf("User-Agent = %q", request.UserAgent())
		}
		_, _ = response.Write(fixture)
	}))
	defer server.Close()
	client := NewClient(server.Client(), "Waxlight/1.2.3")
	client.primaryURL = server.URL
	client.fallbackURL = server.URL + "/fallback"
	items, err := client.List(context.Background())
	if err != nil || len(items) != 3 {
		t.Fatalf("List() = %d items, %v", len(items), err)
	}
}

func TestClientFallsBackToForumFeed(t *testing.T) {
	fixture, err := os.ReadFile("testdata/forum.xml")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/primary" {
			http.Error(response, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = response.Write(fixture)
	}))
	defer server.Close()
	client := NewClient(server.Client(), "")
	client.primaryURL = server.URL + "/primary"
	client.fallbackURL = server.URL + "/fallback"
	items, err := client.List(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("List() = %d items, %v", len(items), err)
	}
}

func TestClientReportsBothFeedsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := NewClient(server.Client(), "")
	client.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	client.primaryURL = server.URL + "/primary"
	client.fallbackURL = server.URL + "/fallback"
	_, err := client.List(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	// Both per-feed failures keep their structured details.
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != vshttp.KindServerError || apiErr.StatusCode != 503 {
		t.Fatalf("%#v", err)
	}
}

func TestFeedTooLargeIsExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(make([]byte, maxResponseBytes+1))
	}))
	defer server.Close()
	client := NewClient(server.Client(), "")
	client.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 1})
	client.primaryURL = server.URL
	client.fallbackURL = server.URL
	_, err := client.List(context.Background())
	if !errors.Is(err, ErrInvalidFeed) {
		t.Fatalf("legacy sentinel: %v", err)
	}
	if !errors.Is(err, vshttp.ErrResponseTooLarge) {
		t.Fatalf("expected explicit too-large: %v", err)
	}
}
