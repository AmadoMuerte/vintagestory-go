package news

import (
	"errors"
	"os"
	"testing"
)

func TestParseNormalizesAndSortsOfficialFeed(t *testing.T) {
	data, err := os.ReadFile("testdata/blog.xml")
	if err != nil {
		t.Fatal(err)
	}
	items, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0].Title != "v1.22.7 - Auction house upgrades" || items[0].Category != CategoryRelease {
		t.Fatalf("unexpected first item: %+v", items[0])
	}
	if items[0].ID != items[0].URL || items[0].ImageURL == "" {
		t.Fatalf("item was not normalized: %+v", items[0])
	}
	if items[1].Category != CategoryDevelopment || items[2].Summary != "" || items[2].ImageURL != "" {
		t.Fatalf("optional fields or category are incorrect: %+v", items)
	}
}

func TestParseFallbackUsesCanonicalBlogURL(t *testing.T) {
	data, err := os.ReadFile("testdata/forum.xml")
	if err != nil {
		t.Fatal(err)
	}
	items, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://www.vintagestory.at/blog.html/news/v1227-auction-house-upgrades-r449/"
	if len(items) != 1 || items[0].ID != want || items[0].URL != want {
		t.Fatalf("unexpected fallback item: %+v", items)
	}
}

func TestParseRejectsInvalidXML(t *testing.T) {
	_, err := parse([]byte("<rss>"))
	if !errors.Is(err, ErrInvalidFeed) {
		t.Fatalf("got %v", err)
	}
}

func TestOfficialArticleURL(t *testing.T) {
	if !IsOfficialArticleURL("https://www.vintagestory.at/blog.html/news/post-r1/") {
		t.Fatal("official URL rejected")
	}
	for _, value := range []string{
		"http://www.vintagestory.at/blog.html/news/post-r1/",
		"https://example.com/blog.html/news/post-r1/",
		"https://www.vintagestory.at/forums/topic/1/",
		"https://user@www.vintagestory.at/blog.html/news/post-r1/",
	} {
		if IsOfficialArticleURL(value) {
			t.Fatalf("untrusted URL accepted: %s", value)
		}
	}
}
