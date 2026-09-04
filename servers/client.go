package servers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sync"
	"time"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
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
	retry      vshttp.RetryPolicy

	mu        sync.Mutex
	servers   []Server
	fetchedAt time.Time
}

// NewClient creates a public server catalog client. A nil client uses
// bounded default timeouts.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = vshttp.DefaultClient(15 * time.Second)
	}
	return &Client{httpClient: httpClient, endpoint: OfficialCatalogURL, retry: vshttp.DefaultRetryPolicy()}
}

// NewClientWithURL creates a client with an explicit catalog endpoint. It is
// useful for tests and controlled integrations.
func NewClientWithURL(httpClient *http.Client, endpoint string) *Client {
	client := NewClient(httpClient)
	client.endpoint = endpoint
	return client
}

// SetRetryPolicy overrides the retry policy used for the catalog request.
func (c *Client) SetRetryPolicy(p vshttp.RetryPolicy) { c.retry = p }

// List returns public server listings sorted by player count, highest first.
// Successful results are cached for five minutes and concurrent callers
// share a single fetch. A failed refresh never replaces a valid cache.
func (c *Client) List(ctx context.Context) ([]Server, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.servers != nil && time.Since(c.fetchedAt) < cacheTTL {
		return append([]Server(nil), c.servers...), nil
	}

	const op = "servers: list catalog"
	root, err := c.fetchHTML(ctx, c.endpoint, op)
	if err != nil {
		return nil, err
	}
	servers := parseServers(root)
	if len(servers) == 0 {
		return nil, legacy(&vshttp.APIError{Operation: op, Method: http.MethodGet, Endpoint: c.endpoint, Kind: vshttp.KindInvalidResponse, Cause: fmt.Errorf("catalog contained no server listings")})
	}
	for i := range servers {
		resolveURLs(&servers[i], c.endpoint)
	}
	c.servers = append([]Server(nil), servers...)
	c.fetchedAt = time.Now()
	return append([]Server(nil), servers...), nil
}

// Get returns the full public details for a server.
func (c *Client) Get(ctx context.Context, id string) (Server, error) {
	if id == "" {
		return Server{}, legacy(&vshttp.APIError{Operation: "servers: get detail", Method: http.MethodGet, Endpoint: c.endpoint, Kind: vshttp.KindValidation, Cause: fmt.Errorf("server ID is empty")})
	}
	endpoint, err := detailURL(c.endpoint, id)
	if err != nil {
		return Server{}, legacy(&vshttp.APIError{Operation: "servers: get detail", Method: http.MethodGet, Endpoint: c.endpoint, Kind: vshttp.KindValidation, Cause: err})
	}
	root, err := c.fetchHTML(ctx, endpoint, "servers: get detail")
	if err != nil {
		return Server{}, err
	}
	server, ok := parseServerDetail(root)
	if !ok {
		return Server{}, legacy(&vshttp.APIError{Operation: "servers: get detail", Method: http.MethodGet, Endpoint: endpoint, Kind: vshttp.KindInvalidResponse, Cause: fmt.Errorf("server detail page contained no server")})
	}
	server.URL = endpoint
	resolveURLs(&server, endpoint)
	return server, nil
}

func resolveURLs(server *Server, base string) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return
	}
	resolve := func(value *string) {
		if *value == "" {
			return
		}
		if parsed, err := url.Parse(*value); err == nil {
			*value = baseURL.ResolveReference(parsed).String()
		}
	}
	resolve(&server.URL)
	resolve(&server.ImageURL)
	resolve(&server.BannerURL)
	resolve(&server.OperatorURL)
	for i := range server.Mods {
		resolve(&server.Mods[i].URL)
	}
}

func (c *Client) fetchHTML(ctx context.Context, endpoint, op string) (*html.Node, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, legacy(&vshttp.APIError{Operation: op, Method: http.MethodGet, Endpoint: endpoint, Kind: vshttp.KindValidation, Cause: err})
	}
	response, body, err := vshttp.Do(ctx, c.httpClient, c.retry, op, request, maxResponseBytes)
	if err != nil {
		return nil, legacy(err)
	}
	if err := vshttp.CheckStatus(op, request, response); err != nil {
		return nil, legacy(err)
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, legacy(&vshttp.APIError{Operation: op, Method: http.MethodGet, Endpoint: endpoint, StatusCode: response.StatusCode, ContentType: response.Header.Get("Content-Type"), Kind: vshttp.KindInvalidResponse, Cause: fmt.Errorf("parse HTML: %w", err)})
	}
	return root, nil
}

func detailURL(endpoint, id string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	parsed.Path = path.Join(parsed.Path, "s", url.PathEscape(id))
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
