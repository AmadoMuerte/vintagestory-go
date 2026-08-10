package servers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	OfficialCatalogURL = "https://servers.vintagestory.at/"
	cacheTTL           = 5 * time.Minute
	maxResponseBytes   = 4 << 20
)

// Client retrieves public server listings from the official Vintage Story catalog.
// A Client may be used concurrently by multiple goroutines.
type Client struct {
	httpClient *http.Client
	endpoint   string

	mu        sync.Mutex
	servers   []Server
	fetchedAt time.Time
}

// NewClient creates a public server catalog client. A nil client uses a
// 15-second timeout.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{httpClient: httpClient, endpoint: OfficialCatalogURL}
}

// NewClientWithURL creates a client with an explicit catalog endpoint. It is
// useful for tests and controlled integrations.
func NewClientWithURL(httpClient *http.Client, endpoint string) *Client {
	client := NewClient(httpClient)
	client.endpoint = endpoint
	return client
}

// List returns public server listings sorted by player count, highest first.
// Successful results are cached for five minutes.
func (c *Client) List(ctx context.Context) ([]Server, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.servers != nil && time.Since(c.fetchedAt) < cacheTTL {
		return append([]Server(nil), c.servers...), nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create public server catalog request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch public server catalog: %w: %w", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: response.StatusCode}
	}

	root, err := html.Parse(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("parse public server catalog: %w: %w", ErrInvalidCatalog, err)
	}
	servers := parseServers(root)
	if len(servers) == 0 {
		return nil, fmt.Errorf("public server catalog contained no server listings: %w", ErrInvalidCatalog)
	}
	c.servers = append([]Server(nil), servers...)
	c.fetchedAt = time.Now()
	return append([]Server(nil), servers...), nil
}
