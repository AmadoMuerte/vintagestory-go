package moddb

import (
	"errors"
	"fmt"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

var (
	ErrNotFound        = errors.New("mod not found")
	ErrUnavailable     = errors.New("mod catalog unavailable")
	ErrInvalidResponse = errors.New("invalid mod catalog response")
	// ErrStale marks errors returned together with stale cached data: the
	// refresh failed, but previously fetched data is still served.
	ErrStale = errors.New("mod catalog refresh failed, serving stale cached data")
)

// HTTPError is kept for source compatibility. New code should use
// errors.As with *vshttp.APIError.
type HTTPError = vshttp.APIError

// legacy annotates a vshttp error with the matching package sentinel so
// errors.Is keeps working for ErrNotFound, ErrUnavailable and
// ErrInvalidResponse.
func legacy(err error) error {
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Legacy != nil {
		return err
	}
	switch apiErr.Kind {
	case vshttp.KindNotFound:
		apiErr.Legacy = ErrNotFound
	case vshttp.KindInvalidJSON, vshttp.KindInvalidResponse, vshttp.KindResponseTooLarge:
		apiErr.Legacy = ErrInvalidResponse
	default:
		apiErr.Legacy = ErrUnavailable
	}
	return apiErr
}

// apiStatusError reports a 2xx HTTP response whose ModDB payload carries a
// non-200 application status code.
func apiStatusError(op, endpoint, code string) error {
	kind := vshttp.KindServerError
	sentinel := ErrUnavailable
	if code == "404" {
		kind = vshttp.KindNotFound
		sentinel = ErrNotFound
	}
	return &vshttp.APIError{
		Operation: op,
		Method:    "GET",
		Endpoint:  endpoint,
		Kind:      kind,
		Legacy:    sentinel,
		Cause:     fmt.Errorf("moddb API returned statuscode %q", code),
	}
}
