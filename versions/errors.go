package versions

import (
	"errors"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

var (
	// ErrUnavailable indicates the version catalog could not be fetched.
	ErrUnavailable = errors.New("version catalog unavailable")
	// ErrInvalidResponse indicates the catalog payload could not be decoded.
	ErrInvalidResponse = errors.New("invalid version catalog response")
)

// legacy annotates a vshttp error with the matching package sentinel.
func legacy(err error) error {
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Legacy != nil {
		return err
	}
	switch apiErr.Kind {
	case vshttp.KindInvalidJSON, vshttp.KindInvalidResponse, vshttp.KindResponseTooLarge:
		apiErr.Legacy = ErrInvalidResponse
	default:
		apiErr.Legacy = ErrUnavailable
	}
	return apiErr
}
