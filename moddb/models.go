// Package moddb provides a client for the Vintage Story ModDB API.
package moddb

import "time"

type Side string

const (
	SideClient  Side = "client"
	SideServer  Side = "server"
	SideBoth    Side = "both"
	SideUnknown Side = "unknown"
)

// Mod is a ModDB catalog entry.
type Mod struct {
	ID, Slug, Name, AuthorName, Summary, ImageURL, LatestVersion string
	Side                                                         Side
	GameVersions, ModIDStrings, Tags                             []string
	Downloads                                                    int64
	CreatedAt, UpdatedAt                                         *time.Time
}
type Screenshot struct{ URL, Caption string }

// Dependency is a release dependency when supplied by the ModDB API.
type Dependency struct{ ModID, Name, Version, Requirement string }

// Release is a ModDB mod release.
type Release struct {
	ID, Version           string
	GameVersions          []string
	ReleaseType, FileName string
	FileSize              int64
	DownloadURL, Checksum string
	PublishedAt           *time.Time
	Changelog             string
	Dependencies          []Dependency
}

// ModDetails contains a mod and its full metadata.
type ModDetails struct {
	Mod
	Description                    string
	Screenshots                    []Screenshot
	Releases                       []Release
	Dependencies                   []Dependency
	WebsiteURL, SourceURL, License string
}

// SearchOptions controls locally applied ModDB catalog filtering and paging.
type SearchOptions struct {
	Text, GameVersion, Sort string
	Side                    Side
	UpdatedAfter            *time.Time
	Tags                    []string
	Page, PageSize          int
}
type Tag struct {
	Name  string
	Count int
}
type SearchResult struct {
	Items                                  []Mod
	Page, PageSize, TotalItems, TotalPages int
	HasNext                                bool
}
