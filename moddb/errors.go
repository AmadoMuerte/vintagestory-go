package moddb

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound        = errors.New("mod not found")
	ErrUnavailable     = errors.New("mod catalog unavailable")
	ErrInvalidResponse = errors.New("invalid mod catalog response")
)

// HTTPError identifies a non-successful ModDB HTTP response.
type HTTPError struct {
	StatusCode int
	Cause      error
}

func (e *HTTPError) Error() string { return fmt.Sprintf("mod catalog returned HTTP %d", e.StatusCode) }
func (e *HTTPError) Unwrap() error { return e.Cause }
