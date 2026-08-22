package news

import (
	"errors"
	"fmt"
)

var (
	ErrUnavailable = errors.New("official news unavailable")
	ErrInvalidFeed = errors.New("invalid official news feed")
)

type HTTPError struct {
	StatusCode int
}

func (err *HTTPError) Error() string {
	return fmt.Sprintf("official news feed returned HTTP %d", err.StatusCode)
}

func (err *HTTPError) Unwrap() error { return ErrUnavailable }
