package moddb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
}
