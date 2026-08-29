package versions

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

func TestCatalogMappingAndInvalidRelease(t *testing.T) {
	body := `{"1.22.0-rc.2":{"linux":{"filename":"rc.tar.gz","filesize":"590.5 MB","md5":"0123456789abcdef0123456789abcdef","urls":{"cdn":"https://cdn.vintagestory.at/gamefiles/unstable/rc.tar.gz"}}},"1.22.0":{"linux":{"filename":"game.tar.gz","filesize":"591 MB","md5":"abcdef0123456789abcdef0123456789","urls":{"cdn":"https://cdn.vintagestory.at/gamefiles/stable/game.tar.gz"},"latest":1}},"bad":{"linux":{"filename":"bad.tar.gz","md5":"bad","urls":{"cdn":"https://example.test/bad.tar.gz"}}}}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer s.Close()
	items, e := NewCatalogForPlatform(s.Client(), s.URL, "linux", "amd64").List(context.Background())
	if e != nil || len(items) != 2 {
		t.Fatalf("%v %#v", e, items)
	}
	if items[0].ID != "1.22.0" || items[0].Channel != "stable" || items[1].DownloadSize != 590500000 {
		t.Fatalf("%#v", items)
	}
}
func TestUnsupportedPlatform(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer s.Close()
	c := NewCatalogForPlatform(s.Client(), s.URL, "darwin", "arm64")
	items, e := c.List(context.Background())
	if e != nil || len(items) != 0 {
		t.Fatalf("%v %#v", e, items)
	}
}

func TestFetchFailuresKeepCause(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no", 500)
		}))
		defer s.Close()
		c := NewCatalogForPlatform(s.Client(), s.URL, "linux", "amd64")
		_, err := c.List(context.Background())
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, vshttp.ErrServer) {
			t.Fatalf("%v", err)
		}
		var apiErr *vshttp.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || apiErr.Kind != vshttp.KindServerError {
			t.Fatalf("%#v", err)
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"1.22.0":`)
		}))
		defer s.Close()
		c := NewCatalogForPlatform(s.Client(), s.URL, "linux", "amd64")
		_, err := c.List(context.Background())
		if !errors.Is(err, ErrInvalidResponse) || !strings.Contains(err.Error(), "unexpected end") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("retry then success", func(t *testing.T) {
		var attempts atomic.Int32
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if attempts.Add(1) == 1 {
				http.Error(w, "no", 503)
				return
			}
			_, _ = io.WriteString(w, `{}`)
		}))
		defer s.Close()
		c := NewCatalogForPlatform(s.Client(), s.URL, "linux", "amd64")
		c.SetRetryPolicy(vshttp.RetryPolicy{MaxAttempts: 3, BaseDelay: 1e6, MaxDelay: 5e6})
		if _, err := c.List(context.Background()); err != nil {
			t.Fatal(err)
		}
		if attempts.Load() != 2 {
			t.Fatalf("attempts = %d", attempts.Load())
		}
	})
}
