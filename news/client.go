package news

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
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
	retry       vshttp.RetryPolicy
}

// NewClient creates an official news client. A nil client uses bounded
// default timeouts. An empty user agent uses "vintagestory-go".
func NewClient(httpClient *http.Client, userAgent string) *Client {
	if httpClient == nil {
		httpClient = vshttp.DefaultClient(15 * time.Second)
		httpClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 || request.URL.Scheme != "https" ||
				!strings.EqualFold(request.URL.Hostname(), "www.vintagestory.at") {
				return errors.New("official news redirected to an untrusted URL")
			}
			return nil
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
		retry:       vshttp.DefaultRetryPolicy(),
	}
}

// SetRetryPolicy overrides the retry policy used for feed requests.
func (client *Client) SetRetryPolicy(p vshttp.RetryPolicy) { client.retry = p }

// List returns official posts sorted newest-first. The forum feed is tried
// only when the primary blog feed cannot provide usable entries.
func (client *Client) List(ctx context.Context) ([]Item, error) {
	items, primaryErr := client.fetch(ctx, client.primaryURL)
	if primaryErr == nil {
		return items, nil
	}
	if ctx.Err() != nil {
		return nil, primaryErr
	}
	items, fallbackErr := client.fetch(ctx, client.fallbackURL)
	if fallbackErr == nil {
		return items, nil
	}
	return nil, fmt.Errorf("%w: %w", ErrUnavailable, errors.Join(primaryErr, fallbackErr))
}

func (client *Client) fetch(ctx context.Context, endpoint string) ([]Item, error) {
	op := "news: fetch feed"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, legacy(&vshttp.APIError{Operation: op, Method: http.MethodGet, Endpoint: endpoint, Kind: vshttp.KindValidation, Cause: err})
	}
	request.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9")
	request.Header.Set("User-Agent", client.userAgent)
	response, body, err := vshttp.Do(ctx, client.httpClient, client.retry, op, request, maxResponseBytes)
	if err != nil {
		return nil, legacy(err)
	}
	if err := vshttp.CheckStatus(op, request, response); err != nil {
		return nil, legacy(err)
	}
	items, err := parse(body)
	if err != nil {
		return nil, legacy(&vshttp.APIError{Operation: op, Method: http.MethodGet, Endpoint: endpoint, StatusCode: response.StatusCode, ContentType: response.Header.Get("Content-Type"), Kind: vshttp.KindInvalidResponse, BodySize: int64(len(body)), Cause: err})
	}
	return items, nil
}
