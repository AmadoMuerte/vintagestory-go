package versions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
