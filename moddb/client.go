package moddb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
	"golang.org/x/sync/singleflight"
)

// Server-side query parameter formats accepted by the ModDB v1 list endpoint
// (mirrored from the vsmoddb server, lib/version.php).
var (
	majorMinorRE = regexp.MustCompile(`^\d+\.\d+$`)
	semverRE     = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-(?:dev|pre|rc)\.\d+)?$`)
)

const (
	DefaultBaseURL  = "https://mods.vintagestory.at/api"
	maxReplyBytes   = 16 << 20
	catalogCacheTTL = 10 * time.Minute
	detailsCacheTTL = 30 * time.Minute
	// requestTimeout bounds a single HTTP attempt, including the full body
	// read. The catalog is several megabytes of uncompressed JSON that the
	// server may stream slowly, so the budget must cover a slow body read on
	// a degraded connection. It applies per attempt: the retry loop issues a
	// fresh http.Client request (and thus a fresh timeout timer) each round,
	// while dial, TLS and header stalls still fail fast via the transport
	// timeouts.
	requestTimeout = 120 * time.Second
)

// Client retrieves and locally searches the Vintage Story ModDB catalog.
// A Client may be used concurrently by multiple goroutines.
type Client struct {
	httpClient *http.Client
	baseURL    string
	retry      vshttp.RetryPolicy

	mu          sync.RWMutex
	catalog     []Mod
	catalogAt   time.Time
	catalogByGV map[string]cachedCatalog
	details     map[string]cachedDetails
	inflight    singleflight.Group
}
type cachedDetails struct {
	value     ModDetails
	fetchedAt time.Time
}
type cachedCatalog struct {
	items []Mod
	at    time.Time
}

// NewClient creates a ModDB client. A nil client uses bounded default
// timeouts suitable for unstable networks.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = vshttp.DefaultClient(requestTimeout)
	}
	return &Client{httpClient: httpClient, baseURL: DefaultBaseURL, retry: vshttp.DefaultRetryPolicy(), details: map[string]cachedDetails{}, catalogByGV: map[string]cachedCatalog{}}
}

// NewClientWithURL creates a client against an explicit API base URL.
func NewClientWithURL(httpClient *http.Client, baseURL string) *Client {
	c := NewClient(httpClient)
	c.baseURL = strings.TrimRight(baseURL, "/")
	return c
}

// SetRetryPolicy overrides the retry policy used for GET requests.
func (c *Client) SetRetryPolicy(p vshttp.RetryPolicy) { c.retry = p }

type modsResponse struct {
	StatusCode string       `json:"statuscode"`
	Mods       []apiSummary `json:"mods"`
}
type apiSummary struct {
	ModID                                           int64 `json:"modid"`
	Downloads                                       int64 `json:"downloads"`
	Name, Summary, Author, Side, Type, LastReleased string
	ModIDStrings                                    []string `json:"modidstrs"`
	URLAlias                                        *string  `json:"urlalias"`
	Logo                                            *string  `json:"logo"`
	Tags                                            []string `json:"tags"`
}
type modResponse struct {
	StatusCode string    `json:"statuscode"`
	Mod        apiDetail `json:"mod"`
}
type apiDetail struct {
	ModID                        int64 `json:"modid"`
	Name, Text, Author, URLAlias string
	Logo                         string `json:"logofile"`
	HomepageURL                  string `json:"homepageurl"`
	SourceCodeURL                string `json:"sourcecodeurl"`
	Downloads                    int64  `json:"downloads"`
	Side, Created, LastReleased  string
	Tags                         []string          `json:"tags"`
	Releases                     []apiRelease      `json:"releases"`
	Screenshots                  []json.RawMessage `json:"screenshots"`
}
type apiRelease struct {
	ReleaseID                      int64 `json:"releaseid"`
	MainFile, Filename             string
	Tags                           []string `json:"tags"`
	ModVersion, Created, Changelog string
}

// List returns all catalog mods, excluding non-mod assets. Results are
// cached for ten minutes and concurrent calls share a single upstream
// request.
//
// When a refresh fails but a previous catalog is still cached, List returns
// the stale data together with an error matching ErrStale: callers that
// prefer availability over freshness may use the data, callers that do not
// can keep treating the error as fatal.
func (c *Client) List(ctx context.Context) ([]Mod, error) {
	items, e := c.list(ctx)
	return append([]Mod(nil), items...), e
}

// Search filters, sorts, and pages the catalog locally. When a game-version
// filter is set, the catalog is fetched server-side with the gameversion or
// gv query parameter instead of the full list; only the returned page is
// then enriched with version details. Partial failures of those detail
// lookups are reported via SearchResult.Warning instead of silently
// dropping results.
func (c *Client) Search(ctx context.Context, q SearchOptions) (SearchResult, error) {
	param := ""
	if q.GameVersion != "" {
		if name, value, ok := gameVersionParam(q.GameVersion); ok {
			param = name + "=" + url.QueryEscape(value)
		}
	}
	var items []Mod
	var err error
	if param != "" {
		items, err = c.listForGameVersion(ctx, param)
	} else {
		items, err = c.list(ctx)
	}
	if err != nil && !errors.Is(err, ErrStale) {
		return SearchResult{}, err
	}
	warning := err
	filtered := make([]Mod, 0, len(items))
	text := strings.ToLower(strings.TrimSpace(q.Text))
	for _, m := range items {
		if text != "" && !matchesText(m, text) {
			continue
		}
		if q.Side != "" && q.Side != SideUnknown && m.Side != q.Side {
			continue
		}
		if q.UpdatedAfter != nil && (m.UpdatedAt == nil || m.UpdatedAt.Before(*q.UpdatedAfter)) {
			continue
		}
		if len(q.Tags) > 0 && !containsAllFold(m.Tags, q.Tags) {
			continue
		}
		filtered = append(filtered, m)
	}
	sortMods(filtered, q.Sort, text != "")
	page := q.Page
	if page < 1 {
		page = 1
	}
	size := q.PageSize
	if size < 1 || size > 60 {
		size = 24
	}
	total := len(filtered)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	pageItems := append([]Mod(nil), filtered[start:end]...)
	if param != "" {
		enriched, enrichErr := c.enrichPage(ctx, pageItems)
		if enrichErr != nil && errors.Is(enrichErr, context.Canceled) {
			return SearchResult{}, enrichErr
		}
		if enrichErr != nil && warning == nil {
			warning = enrichErr
		}
		pageItems = enriched
	}
	return SearchResult{Items: pageItems, Page: page, PageSize: size, TotalItems: total, TotalPages: (total + size - 1) / size, HasNext: end < total, Warning: warning}, nil
}

// ListTags returns case-insensitive catalog tags with usage counts. The
// upstream /api/tags endpoint carries no usage counts, so tags are counted
// from the cached catalog instead of issuing a separate heavy request.
func (c *Client) ListTags(ctx context.Context) ([]Tag, error) {
	items, err := c.list(ctx)
	if err != nil && !errors.Is(err, ErrStale) {
		return nil, err
	}
	by := map[string]*Tag{}
	for _, m := range items {
		for _, name := range m.Tags {
			k := strings.ToLower(name)
			if by[k] == nil {
				by[k] = &Tag{Name: name}
			}
			by[k].Count++
		}
	}
	out := make([]Tag, 0, len(by))
	for _, t := range by {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, err
}

// list returns the catalog, fetching it through a single shared flight on
// cache miss. On refresh failure a stale cache is returned together with an
// error wrapping ErrStale; the stale cache is never replaced by a failed
// or partial response.
func (c *Client) list(ctx context.Context) ([]Mod, error) {
	c.mu.RLock()
	if len(c.catalog) > 0 && time.Since(c.catalogAt) < catalogCacheTTL {
		out := append([]Mod(nil), c.catalog...)
		c.mu.RUnlock()
		return out, nil
	}
	stale := append([]Mod(nil), c.catalog...)
	c.mu.RUnlock()

	result := c.inflight.DoChan("catalog", func() (any, error) {
		// Detach from the caller's cancellation: the shared fetch must
		// survive the first caller leaving so all waiters get a result
		// and the cache still gets populated. Bounded by client timeouts.
		items, err := c.fetchCatalog(context.WithoutCancel(ctx))
		if err != nil && len(stale) > 0 {
			return stale, fmt.Errorf("%w: %w", ErrStale, err)
		}
		return items, err
	})
	select {
	case r := <-result:
		if r.Err != nil {
			items, _ := r.Val.([]Mod)
			return items, r.Err
		}
		items, _ := r.Val.([]Mod)
		return append([]Mod(nil), items...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// listForGameVersion returns the catalog filtered server-side by a game
// version query parameter, sharing the same caching, singleflight and
// stale-if-error semantics as list. On refresh failure a previously cached
// filtered catalog is returned together with an error wrapping ErrStale.
func (c *Client) listForGameVersion(ctx context.Context, param string) ([]Mod, error) {
	c.mu.RLock()
	var stale []Mod
	if entry, ok := c.catalogByGV[param]; ok {
		if time.Since(entry.at) < catalogCacheTTL {
			c.mu.RUnlock()
			return append([]Mod(nil), entry.items...), nil
		}
		stale = append([]Mod(nil), entry.items...)
	}
	c.mu.RUnlock()

	op := "moddb: list catalog (" + param + ")"
	endpoint := c.baseURL + "/mods?" + param
	result := c.inflight.DoChan("catalog:gv:"+param, func() (any, error) {
		// Detach from the caller's cancellation like list does: the shared
		// fetch must survive the first caller leaving so all waiters get a
		// result and the cache still gets populated. Bounded by client
		// timeouts.
		items, err := c.fetchMods(context.WithoutCancel(ctx), op, endpoint)
		if err != nil && len(stale) > 0 {
			return stale, fmt.Errorf("%w: %w", ErrStale, err)
		}
		if err == nil {
			c.mu.Lock()
			c.catalogByGV[param] = cachedCatalog{items: append([]Mod(nil), items...), at: time.Now()}
			c.mu.Unlock()
		}
		return items, err
	})
	select {
	case r := <-result:
		if r.Err != nil {
			items, _ := r.Val.([]Mod)
			return items, r.Err
		}
		items, _ := r.Val.([]Mod)
		return append([]Mod(nil), items...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) fetchCatalog(ctx context.Context) ([]Mod, error) {
	items, err := c.fetchMods(ctx, "moddb: list catalog", c.baseURL+"/mods")
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.catalog = append([]Mod(nil), items...)
	c.catalogAt = time.Now()
	c.mu.Unlock()
	return items, nil
}

// fetchMods fetches and parses a mod list response. It never touches the
// caches; callers own cache writes.
func (c *Client) fetchMods(ctx context.Context, op, endpoint string) ([]Mod, error) {
	var r modsResponse
	if e := c.getJSON(ctx, op, endpoint, &r); e != nil {
		return nil, e
	}
	if r.StatusCode != "200" {
		return nil, legacy(apiStatusError(op, endpoint, r.StatusCode))
	}
	out := make([]Mod, 0, len(r.Mods))
	for _, x := range r.Mods {
		if x.Type != "mod" || x.ModID == 0 {
			continue
		}
		slug := ""
		if x.URLAlias != nil {
			slug = *x.URLAlias
		}
		if slug == "" && len(x.ModIDStrings) > 0 {
			slug = x.ModIDStrings[0]
		}
		image := ""
		if x.Logo != nil {
			image = *x.Logo
		}
		out = append(out, Mod{ID: strconv.FormatInt(x.ModID, 10), Slug: slug, Name: x.Name, AuthorName: x.Author, Summary: x.Summary, ImageURL: image, Side: normalizeSide(x.Side), Downloads: x.Downloads, UpdatedAt: parseDate(x.LastReleased), Tags: nonEmpty(x.Tags), ModIDStrings: nonEmpty(x.ModIDStrings), GameVersions: []string{}})
	}
	return out, nil
}

// Get returns detailed metadata for a numeric ID or ModDB slug. Results are
// cached for thirty minutes and concurrent calls for the same mod share a
// single upstream request.
func (c *Client) Get(ctx context.Context, id string) (ModDetails, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ModDetails{}, ErrNotFound
	}
	c.mu.RLock()
	if x, ok := c.details[id]; ok && time.Since(x.fetchedAt) < detailsCacheTTL {
		c.mu.RUnlock()
		return x.value, nil
	}
	c.mu.RUnlock()

	result := c.inflight.DoChan("mod:"+id, func() (any, error) {
		return c.fetchDetails(context.WithoutCancel(ctx), id)
	})
	select {
	case r := <-result:
		if r.Err != nil {
			return ModDetails{}, r.Err
		}
		return r.Val.(ModDetails), nil
	case <-ctx.Done():
		return ModDetails{}, ctx.Err()
	}
}

func (c *Client) fetchDetails(ctx context.Context, id string) (ModDetails, error) {
	endpoint := c.baseURL + "/mod/" + url.PathEscape(id)
	var r modResponse
	if e := c.getJSON(ctx, "moddb: get mod details", endpoint, &r); e != nil {
		return ModDetails{}, e
	}
	if r.StatusCode != "200" || r.Mod.ModID == 0 {
		return ModDetails{}, &vshttp.APIError{
			Operation: "moddb: get mod details",
			Method:    "GET",
			Endpoint:  endpoint,
			Kind:      vshttp.KindNotFound,
			Legacy:    ErrNotFound,
			Cause:     fmt.Errorf("moddb API returned statuscode %q", r.StatusCode),
		}
	}
	d := mapDetails(r.Mod)
	c.mu.Lock()
	x := cachedDetails{d, time.Now()}
	c.details[id] = x
	c.details[d.ID] = x
	if d.Slug != "" {
		c.details[d.Slug] = x
	}
	c.mu.Unlock()
	return d, nil
}
func mapDetails(x apiDetail) ModDetails {
	releases := make([]Release, 0, len(x.Releases))
	set := map[string]struct{}{}
	for _, r := range x.Releases {
		for _, v := range r.Tags {
			set[v] = struct{}{}
		}
		releases = append(releases, Release{ID: strconv.FormatInt(r.ReleaseID, 10), Version: r.ModVersion, GameVersions: nonEmpty(r.Tags), ReleaseType: releaseType(r.ModVersion), FileName: r.Filename, DownloadURL: r.MainFile, PublishedAt: parseDate(r.Created), Changelog: r.Changelog})
	}
	versions := make([]string, 0, len(set))
	for v := range set {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	latest := ""
	if len(releases) > 0 {
		latest = releases[0].Version
	}
	d := ModDetails{Mod: Mod{ID: strconv.FormatInt(x.ModID, 10), Slug: x.URLAlias, Name: x.Name, AuthorName: x.Author, ImageURL: x.Logo, Side: normalizeSide(x.Side), LatestVersion: latest, GameVersions: versions, Downloads: x.Downloads, CreatedAt: parseDate(x.Created), UpdatedAt: parseDate(x.LastReleased), Tags: nonEmpty(x.Tags)}, Description: x.Text, Releases: releases, WebsiteURL: x.HomepageURL, SourceURL: x.SourceCodeURL}
	for _, s := range x.Screenshots {
		if v, ok := parseScreenshot(s); ok {
			d.Screenshots = append(d.Screenshots, v)
		}
	}
	return d
}

// gameVersionParam maps a game-version filter to the ModDB v1 list query
// parameter. Series or major versions ("1.20.x", "1.20") map to the
// gameversion parameter, exact versions ("1.20.7", "1.20.7-rc.1") map to gv.
// The accepted formats mirror the server-side parsers in vsmoddb
// (lib/version.php). Unrecognized input is rejected so callers fall back to
// the plain full catalog.
func gameVersionParam(raw string) (name, value string, ok bool) {
	v := strings.TrimSpace(raw)
	series := strings.TrimSuffix(v, ".x")
	if majorMinorRE.MatchString(series) {
		return "gameversion", series, true
	}
	if semverRE.MatchString(v) {
		return "gv", v, true
	}
	return "", "", false
}

// enrichPage fills GameVersions and LatestVersion for the mods of one result
// page from their (cached) details. The list payload carries no per-mod game
// version data, so a server-filtered search annotates just the visible page.
// Lookup failures degrade to an error that the caller surfaces as a Warning
// instead of failing the search.
func (c *Client) enrichPage(ctx context.Context, items []Mod) ([]Mod, error) {
	if len(items) == 0 {
		return items, nil
	}
	type result struct {
		i   int
		d   ModDetails
		err error
	}
	jobs := make(chan int)
	results := make(chan result, len(items))
	workers := 8
	if workers > len(items) {
		workers = len(items)
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				d, e := c.Get(ctx, items[i].ID)
				results <- result{i, d, e}
			}
		}()
	}
	go func() {
		for i := range items {
			select {
			case jobs <- i:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				close(results)
				return
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	out := append([]Mod(nil), items...)
	var firstErr error
	failures := 0
	for x := range results {
		if x.err != nil {
			failures++
			if firstErr == nil {
				firstErr = x.err
			}
			continue
		}
		out[x.i].GameVersions = append([]string(nil), x.d.GameVersions...)
		out[x.i].LatestVersion = x.d.LatestVersion
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if firstErr != nil {
		return out, fmt.Errorf("moddb: game version enrichment: %d of %d lookups failed, first failure: %w", failures, len(items), firstErr)
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, op, endpoint string, target any) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		return &vshttp.APIError{Operation: op, Method: http.MethodGet, Endpoint: endpoint, Kind: vshttp.KindValidation, Legacy: ErrUnavailable, Cause: e}
	}
	req.Header.Set("Accept", "application/json")
	return legacy(vshttp.FetchJSON(ctx, c.httpClient, c.retry, op, req, maxReplyBytes, target))
}
func matchesText(m Mod, q string) bool {
	return strings.Contains(strings.ToLower(strings.Join([]string{m.ID, m.Slug, m.Name, m.AuthorName, m.Summary, strings.Join(m.Tags, " ")}, " ")), q)
}
func containsAllFold(v, r []string) bool {
	s := map[string]struct{}{}
	for _, x := range v {
		s[strings.ToLower(x)] = struct{}{}
	}
	for _, x := range r {
		if _, ok := s[strings.ToLower(x)]; !ok {
			return false
		}
	}
	return true
}
func sortMods(v []Mod, by string, query bool) {
	sort.SliceStable(v, func(i, j int) bool {
		a, b := v[i], v[j]
		switch by {
		case "downloads":
			return a.Downloads > b.Downloads
		case "name_asc":
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case "name_desc":
			return strings.ToLower(a.Name) > strings.ToLower(b.Name)
		case "newest", "updated":
			return dateAfter(a.UpdatedAt, b.UpdatedAt)
		}
		return !query && dateAfter(a.UpdatedAt, b.UpdatedAt)
	})
}
func dateAfter(a, b *time.Time) bool { return a != nil && (b == nil || a.After(*b)) }
func parseDate(v string) *time.Time {
	for _, l := range []string{"2006-01-02 15:04:05", time.RFC3339, time.RFC3339Nano} {
		if x, e := time.ParseInLocation(l, strings.TrimSpace(v), time.UTC); e == nil {
			return &x
		}
	}
	return nil
}
func normalizeSide(v string) Side {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "client":
		return SideClient
	case "server":
		return SideServer
	case "both":
		return SideBoth
	}
	return SideUnknown
}
func nonEmpty(v []string) []string {
	out := []string{}
	for _, x := range v {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out
}
func releaseType(v string) string {
	v = strings.ToLower(v)
	if strings.Contains(v, "alpha") {
		return "alpha"
	}
	if strings.Contains(v, "beta") || strings.Contains(v, "rc") || strings.Contains(v, "pre") {
		return "beta"
	}
	return "stable"
}
func parseScreenshot(raw json.RawMessage) (Screenshot, bool) {
	var direct string
	if json.Unmarshal(raw, &direct) == nil && strings.HasPrefix(direct, "https://") {
		return Screenshot{URL: direct}, true
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return Screenshot{}, false
	}
	for _, k := range []string{"mainfile", "url", "file", "filename", "image"} {
		if v, ok := object[k].(string); ok && strings.HasPrefix(v, "https://") {
			caption, _ := object["caption"].(string)
			return Screenshot{v, caption}, true
		}
	}
	return Screenshot{}, false
}
