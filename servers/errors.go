package servers

import (
	"errors"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

var (
	// ErrUnavailable indicates the public server catalog could not be fetched.
	ErrUnavailable = errors.New("public server catalog unavailable")
	// ErrInvalidCatalog indicates the catalog did not contain usable listings.
	ErrInvalidCatalog = errors.New("invalid public server catalog")
)

// HTTPError is kept for source compatibility. New code should use
// errors.As with *vshttp.APIError.
type HTTPError = vshttp.APIError

// legacy annotates a vshttp error with the matching package sentinel so
// errors.Is keeps working for ErrUnavailable and ErrInvalidCatalog.
func legacy(err error) error {
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Legacy != nil {
		return err
	}
	switch apiErr.Kind {
	case vshttp.KindInvalidJSON, vshttp.KindInvalidResponse, vshttp.KindResponseTooLarge:
		apiErr.Legacy = ErrInvalidCatalog
	default:
		apiErr.Legacy = ErrUnavailable
	}
	return apiErr
}
