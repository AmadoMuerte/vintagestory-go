// Package news retrieves official Vintage Story news posts.
package news

import "time"

type Category string

const (
	CategoryNews        Category = "news"
	CategoryRelease     Category = "release"
	CategoryDevelopment Category = "development"
)

// Item is an official Vintage Story news post. Summary is plain text; URL and
// ImageURL are validated official URLs.
type Item struct {
	ID          string
	Title       string
	URL         string
	Summary     string
	ImageURL    string
	PublishedAt time.Time
	Category    Category
}
