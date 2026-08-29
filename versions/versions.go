// Package versions retrieves Vintage Story game releases from the official catalog.
package versions

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

const OfficialCatalogURL = "https://api.vintagestory.at/stable-unstable.json"

// Release is a downloadable Vintage Story game release.
type Release struct {
	ID, Name, Channel, Platform, Architecture, Filename, DownloadURL, Checksum, ChecksumAlgorithm string
	DownloadSize                                                                                  int64
	Latest                                                                                        bool
}
type catalogFile struct {
	Filename string `json:"filename"`
	FileSize string `json:"filesize"`
	MD5      string `json:"md5"`
	URLs     struct {
		CDN string `json:"cdn"`
	} `json:"urls"`
	Latest int `json:"latest"`
}

// Catalog retrieves releases for one OS and CPU architecture.
type Catalog struct {
	client                           *http.Client
	endpoint, platform, architecture string
	retry                            vshttp.RetryPolicy
	cacheMu                          sync.Mutex
	cachedAt                         time.Time
	cached                           []Release
}

// NewCatalog creates a catalog for the current platform. A nil client uses
// bounded default timeouts.
func NewCatalog(client *http.Client) *Catalog {
	if client == nil {
		client = vshttp.DefaultClient(20 * time.Second)
	}
	return &Catalog{client: client, endpoint: OfficialCatalogURL, retry: vshttp.DefaultRetryPolicy(), platform: runtime.GOOS, architecture: runtime.GOARCH}
}

// NewCatalogForPlatform creates a catalog with an explicit endpoint and target platform.
func NewCatalogForPlatform(client *http.Client, endpoint, platform, architecture string) *Catalog {
	c := NewCatalog(client)
	c.endpoint, c.platform, c.architecture = endpoint, platform, architecture
	return c
}

// SetRetryPolicy overrides the retry policy used for the catalog request.
func (c *Catalog) SetRetryPolicy(p vshttp.RetryPolicy) { c.retry = p }

// List returns supported releases sorted newest-first. Results are cached
// for five minutes; concurrent callers share a single fetch. A failed
// refresh never replaces a previously cached catalog.
func (c *Catalog) List(ctx context.Context) ([]Release, error) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if len(c.cached) > 0 && time.Since(c.cachedAt) < 5*time.Minute {
		return append([]Release(nil), c.cached...), nil
	}
	result, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	c.cached = append([]Release(nil), result...)
	c.cachedAt = time.Now()
	return result, nil
}

func (c *Catalog) fetch(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, legacy(&vshttp.APIError{Operation: "versions: list catalog", Method: http.MethodGet, Endpoint: c.endpoint, Kind: vshttp.KindValidation, Cause: err})
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vintagestory-go")
	resp, body, err := vshttp.Do(ctx, c.client, c.retry, "versions: list catalog", req, 8<<20)
	if err != nil {
		return nil, legacy(err)
	}
	if err := vshttp.CheckStatus("versions: list catalog", req, resp); err != nil {
		return nil, legacy(err)
	}
	var payload map[string]map[string]catalogFile
	if err := vshttp.DecodeJSON("versions: list catalog", req, resp, body, &payload); err != nil {
		return nil, legacy(err)
	}
	key, arch, ok := distributionFor(c.platform, c.architecture)
	if !ok {
		return []Release{}, nil
	}
	result := make([]Release, 0, len(payload))
	for id, files := range payload {
		if file, ok := files[key]; ok {
			if release, err := parseRelease(id, file, c.platform, arch); err == nil {
				result = append(result, release)
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return compareVersions(result[i].ID, result[j].ID) > 0 })
	return result, nil
}
func distributionFor(platform, architecture string) (string, string, bool) {
	switch {
	case platform == "linux" && architecture == "amd64":
		return "linux", "amd64", true
	case platform == "windows" && architecture == "amd64":
		return "windows", "amd64", true
	}
	return "", "", false
}
func parseRelease(id string, file catalogFile, platform, architecture string) (Release, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.TrimSpace(file.Filename) == "" {
		return Release{}, fmt.Errorf("missing release identity")
	}
	if path.Base(file.Filename) != file.Filename || strings.Contains(file.Filename, "\\") {
		return Release{}, fmt.Errorf("unsafe release filename")
	}
	u, err := url.Parse(file.URLs.CDN)
	if err != nil || u.Scheme != "https" || u.Hostname() != "cdn.vintagestory.at" {
		return Release{}, fmt.Errorf("untrusted release URL")
	}
	if path.Base(u.Path) != file.Filename {
		return Release{}, fmt.Errorf("release filename does not match URL")
	}
	checksum := strings.ToLower(strings.TrimSpace(file.MD5))
	if len(checksum) != 32 {
		return Release{}, fmt.Errorf("invalid MD5 checksum")
	}
	for _, r := range checksum {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return Release{}, fmt.Errorf("invalid MD5 checksum")
		}
	}
	channel := "unknown"
	if strings.Contains(u.Path, "/stable/") {
		channel = "stable"
	} else if strings.Contains(u.Path, "/unstable/") {
		channel = "unstable"
	}
	return Release{ID: id, Name: id, Channel: channel, Platform: platform, Architecture: architecture, Filename: file.Filename, DownloadURL: u.String(), DownloadSize: parseFileSize(file.FileSize), Checksum: checksum, ChecksumAlgorithm: "md5", Latest: file.Latest == 1}, nil
}
func parseFileSize(value string) int64 {
	p := strings.Fields(strings.TrimSpace(value))
	if len(p) != 2 {
		return 0
	}
	n, e := strconv.ParseFloat(p[0], 64)
	if e != nil || n < 0 {
		return 0
	}
	m := map[string]float64{"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9}[strings.ToUpper(p[1])]
	return int64(n * m)
}

type versionPart struct {
	number *int
	text   string
}

func compareVersions(a, b string) int {
	ap, bp := splitVersion(a), splitVersion(b)
	n := len(ap)
	if len(bp) > n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		if i >= len(ap) {
			return -trailingVersionOrder(bp[i:])
		}
		if i >= len(bp) {
			return trailingVersionOrder(ap[i:])
		}
		x, y := ap[i], bp[i]
		if x.number != nil && y.number != nil {
			if *x.number > *y.number {
				return 1
			}
			if *x.number < *y.number {
				return -1
			}
			continue
		}
		if x.number != nil {
			return 1
		}
		if y.number != nil {
			return -1
		}
		if x.text > y.text {
			return 1
		}
		if x.text < y.text {
			return -1
		}
	}
	return 0
}
func trailingVersionOrder(p []versionPart) int {
	for _, x := range p {
		if x.number != nil && *x.number != 0 {
			return 1
		}
		if x.text != "" {
			return -1
		}
	}
	return 0
}
func splitVersion(v string) []versionPart {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	out := []versionPart{}
	for i := 0; i < len(v); {
		if unicode.IsDigit(rune(v[i])) {
			j := i + 1
			for j < len(v) && unicode.IsDigit(rune(v[j])) {
				j++
			}
			n, _ := strconv.Atoi(v[i:j])
			out = append(out, versionPart{number: &n})
			i = j
			continue
		}
		if unicode.IsLetter(rune(v[i])) {
			j := i + 1
			for j < len(v) && unicode.IsLetter(rune(v[j])) {
				j++
			}
			out = append(out, versionPart{text: v[i:j]})
			i = j
			continue
		}
		i++
	}
	return out
}
