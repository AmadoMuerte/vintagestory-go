package news

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const maximumSummaryRunes = 320

type rssDocument struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PublishedAt string `xml:"pubDate"`
}

func parse(data []byte) ([]Item, error) {
	var document rssDocument
	if err := xml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode official news feed: %w: %w", ErrInvalidFeed, err)
	}
	items := make([]Item, 0, len(document.Channel.Items))
	seen := make(map[string]struct{}, len(document.Channel.Items))
	for _, raw := range document.Channel.Items {
		item, ok := normalize(raw)
		if !ok {
			continue
		}
		if _, duplicate := seen[item.ID]; duplicate {
			continue
		}
		seen[item.ID] = struct{}{}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("official news feed contained no usable posts: %w", ErrInvalidFeed)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].PublishedAt.Equal(items[j].PublishedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	return items, nil
}

func normalize(raw rssItem) (Item, bool) {
	title := strings.TrimSpace(raw.Title)
	publishedAt, err := time.Parse(time.RFC1123Z, strings.TrimSpace(raw.PublishedAt))
	if err != nil {
		publishedAt, err = time.Parse(time.RFC1123, strings.TrimSpace(raw.PublishedAt))
	}
	articleURL := canonicalArticleURL(raw.Link)
	summary, imageURL, linkedArticleURL := parseDescription(raw.Description)
	if articleURL == "" {
		articleURL = linkedArticleURL
	}
	if title == "" || articleURL == "" || err != nil {
		return Item{}, false
	}
	return Item{
		ID:          articleURL,
		Title:       title,
		URL:         articleURL,
		Summary:     summary,
		ImageURL:    imageURL,
		PublishedAt: publishedAt.UTC(),
		Category:    category(title),
	}, true
}

func parseDescription(value string) (summary, imageURL, articleURL string) {
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	var text strings.Builder
	skipDepth := 0
	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case html.ErrorToken:
			if tokenizer.Err() != io.EOF {
				return "", imageURL, articleURL
			}
			return truncate(strings.Join(strings.Fields(text.String()), " "), maximumSummaryRunes), imageURL, articleURL
		case html.TextToken:
			if skipDepth == 0 {
				text.Write(tokenizer.Text())
				text.WriteByte(' ')
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if token.Data == "script" || token.Data == "style" {
				if tokenType == html.StartTagToken {
					skipDepth++
				}
				continue
			}
			for _, attribute := range token.Attr {
				if token.Data == "img" && attribute.Key == "src" && imageURL == "" {
					imageURL = canonicalImageURL(attribute.Val)
				}
				if token.Data == "a" && attribute.Key == "href" {
					if candidate := canonicalArticleURL(attribute.Val); candidate != "" {
						articleURL = candidate
					}
				}
			}
		case html.EndTagToken:
			token := tokenizer.Token()
			if (token.Data == "script" || token.Data == "style") && skipDepth > 0 {
				skipDepth--
			}
		}
	}
}

// IsOfficialArticleURL reports whether value is a canonical official blog URL.
func IsOfficialArticleURL(value string) bool { return canonicalArticleURL(value) != "" }

func canonicalArticleURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "www.vintagestory.at") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.HasPrefix(parsed.EscapedPath(), "/blog.html/news/") {
		return ""
	}
	parsed.Host = "www.vintagestory.at"
	return parsed.String()
}

func canonicalImageURL(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "media.vintagestory.at") || parsed.User != nil {
		return ""
	}
	parsed.Host = "media.vintagestory.at"
	return parsed.String()
}

func category(title string) Category {
	lower := strings.ToLower(strings.TrimSpace(title))
	if strings.Contains(lower, "development update") {
		return CategoryDevelopment
	}
	lower = strings.TrimPrefix(lower, "v")
	first, _, _ := strings.Cut(lower, " ")
	if len(first) > 0 && first[0] >= '0' && first[0] <= '9' && strings.Contains(first, ".") {
		return CategoryRelease
	}
	return CategoryNews
}

func truncate(value string, maximum int) string {
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maximum])) + "…"
}
