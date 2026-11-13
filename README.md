# vintagestory-go

Reusable Go clients and data utilities for Vintage Story services. This module contains no launcher UI, storage, credential-store, or database integration.

Packages:

- `auth`: login, session validation, and TOTP challenges for the Vintage Story authentication API.
- `versions`: official game-release catalog retrieval and validation.
- `moddb`: Vintage Story ModDB catalog, search, details, releases, and tags.
- `modpack`: catalog-agnostic installed-mod update analysis.
- `servers`: public Vintage Story server catalog discovery.
- `news`: official news feed retrieval and normalization.
- `vshttp`: shared HTTP plumbing: structured `APIError`, bounded response reads, status classification, bounded retries.

```go
authClient := auth.NewClient(nil)
session, challenge, err := authClient.Login(ctx, email, password, "", "")

catalog := moddb.NewClient(nil)
mods, err := catalog.Search(ctx, moddb.SearchOptions{Text: "storage", Page: 1})
```

```go
releases, err := versions.NewCatalog(nil).List(ctx)
```

```go
client := servers.NewClient(nil)
list, err := client.List(ctx)
for _, server := range list {
	fmt.Println(server.Name, server.Address)
}
	details, err := client.Get(ctx, list[0].ID)
	fmt.Println(details.FullDescription, details.BannerURL, len(details.Mods))

posts, err := news.NewClient(nil, "MyApp/1.0").List(ctx)
```

## Error model

Every HTTP/API failure is a `*vshttp.APIError`. It records the operation,
method, sanitized endpoint (query and credentials are stripped), HTTP
status, `Content-Type`, a machine-readable `Kind`, retryability, the
server's `Retry-After` hint when present, and — always — the original root
cause in `Cause`. Per-package sentinels (`moddb.ErrNotFound`,
`auth.ErrNetwork`, ...) keep working via `errors.Is`; the generic
`vshttp.Err...` sentinels match by kind.

```go
result, err := client.Search(ctx, moddb.SearchOptions{Text: "storage"})
if err != nil {
	var apiErr *vshttp.APIError
	switch {
	case errors.Is(err, context.Canceled):
		// caller went away
	case errors.Is(err, context.DeadlineExceeded):
		// timeout; apiErr.Retryable tells whether a retry may succeed
	case errors.Is(err, moddb.ErrNotFound):
		// also matches vshttp.ErrNotFound
	case errors.Is(err, vshttp.ErrRateLimited):
		// 429; see apiErr.RetryAfter
	case errors.Is(err, moddb.ErrInvalidResponse):
		// malformed payload; the JSON root cause is in the chain
	case errors.As(err, &apiErr):
		log.Printf("%s failed: kind=%s status=%d retryable=%v: %v",
			apiErr.Operation, apiErr.Kind, apiErr.StatusCode, apiErr.Retryable, apiErr.Cause)
	}
}
```

Kinds: `network`, `timeout`, `cancelled`, `dns`, `tls`, `http_status`,
`rate_limit`, `unauthorized`, `forbidden`, `not_found`, `server_error`,
`response_too_large`, `body_read`, `invalid_json`, `invalid_response`,
`validation`, `unknown`.

Retries: only idempotent GET requests are retried (connection/reset and
timeout transport errors, body read failures, and 429/502/503/504), at most
2 extra attempts with exponential backoff, jitter and `Retry-After`
honoured (capped). Configure via `SetRetryPolicy(vshttp.RetryPolicy{...})`
on the moddb, versions, servers and news clients; pass a custom
`*http.Client` to the constructors for full control.

`moddb` specifics: concurrent catalog/detail fetches are coalesced
(singleflight), so a cache miss under load causes exactly one upstream
request. When a catalog refresh fails but a previous catalog is cached,
`List` returns the stale data together with an error matching
`moddb.ErrStale`, and `Search` reports it via `SearchResult.Warning`.
Partial game-version enrichment failures are also surfaced through
`SearchResult.Warning` instead of vanishing.

Licensed under GPL-3.0-only, compatible with the extracted source.
