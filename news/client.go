package news

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	OfficialFeedURL         = "https://www.vintagestory.at/blog.html/?rss=1"
	OfficialFallbackFeedURL = "https://www.vintagestory.at/forums/forum/7-news.xml/"
	maxResponseBytes        = 4 << 20
)

// Client retrieves the official Vintage Story news feed. A Client may be used
// concurrently by multiple goroutines.
type Client struct {
	httpClient  *http.Client
	primaryURL  string
	fallbackURL string
	userAgent   string
}

// NewClient creates an official news client. A nil client uses a 15-second
// timeout. An empty user agent uses "vintagestory-go".
func NewClient(httpClient *http.Client, userAgent string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 5 || request.URL.Scheme != "https" ||
					!strings.EqualFold(request.URL.Hostname(), "www.vintagestory.at") {
					return errors.New("official news redirected to an untrusted URL")
				}
				return nil
			},
		}
	}
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "vintagestory-go"
	}
	return &Client{
		httpClient:  httpClient,
		primaryURL:  OfficialFeedURL,
		fallbackURL: OfficialFallbackFeedURL,
		userAgent:   userAgent,
	}
}

// List returns official posts sorted newest-first. The forum feed is tried
// only when the primary blog feed cannot provide usable entries.
func (client *Client) List(ctx context.Context) ([]Item, error) {
	items, primaryErr := client.fetch(ctx, client.primaryURL)
	if primaryErr == nil {
		return items, nil
	}
	items, fallbackErr := client.fetch(ctx, client.fallbackURL)
	if fallbackErr == nil {
		return items, nil
	}
	return nil, fmt.Errorf("%w: %w", ErrUnavailable, errors.Join(primaryErr, fallbackErr))
}

func (client *Client) fetch(ctx context.Context, endpoint string) ([]Item, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create official news request: %w", err)
	}
	request.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9")
	request.Header.Set("User-Agent", client.userAgent)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch official news: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read official news: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("official news feed exceeds %d bytes: %w", maxResponseBytes, ErrInvalidFeed)
	}
	return parse(body)
}
