// Package servers provides access to the public Vintage Story server catalog.
package servers

// Server is a listing published by the Vintage Story public server catalog.
// It contains neither credentials nor launch configuration.
type Server struct {
	Name              string
	Address           string
	Description       string
	Players           int
	ModCount          int
	RequiresWhitelist bool
	PasswordProtected bool
	Joinable          bool
}
