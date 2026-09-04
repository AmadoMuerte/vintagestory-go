// Package servers provides access to the public Vintage Story server catalog.
package servers

// Server is a listing published by the Vintage Story public server catalog.
// It contains neither credentials nor launch configuration.
type Server struct {
	ID                string
	URL               string
	Name              string
	Address           string
	Description       string
	FullDescription   string
	DescriptionHTML   string
	ImageURL          string
	BannerURL         string
	GameVersion       string
	Players           int
	MaxPlayers        int
	ModCount          int
	Location          string
	Languages         []string
	Operator          string
	OperatorURL       string
	Modified          bool
	RequiresWhitelist bool
	PasswordProtected bool
	Joinable          bool
	Mods              []Mod
}

// Mod is a mod reported by a server's detail page.
type Mod struct {
	Name    string
	Version string
	URL     string
}
