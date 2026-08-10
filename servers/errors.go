package servers

import (
	"errors"
	"fmt"
)

var (
	// ErrUnavailable indicates the public server catalog could not be fetched.
	ErrUnavailable = errors.New("public server catalog unavailable")
	// ErrInvalidCatalog indicates the catalog did not contain usable listings.
	ErrInvalidCatalog = errors.New("invalid public server catalog")
)

// HTTPError identifies a non-success response from the public server catalog.
type HTTPError struct {
	StatusCode int
}

func (err *HTTPError) Error() string {
	return fmt.Sprintf("public server catalog returned HTTP %d", err.StatusCode)
}

func (err *HTTPError) Unwrap() error { return ErrUnavailable }
