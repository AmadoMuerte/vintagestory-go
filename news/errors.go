package news

import (
	"errors"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

var (
	ErrUnavailable = errors.New("official news unavailable")
	ErrInvalidFeed = errors.New("invalid official news feed")
)

// HTTPError is kept for source compatibility. New code should use
// errors.As with *vshttp.APIError.
type HTTPError = vshttp.APIError

// legacy annotates a vshttp error with the matching package sentinel so
// errors.Is keeps working for ErrUnavailable and ErrInvalidFeed.
func legacy(err error) error {
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Legacy != nil {
		return err
	}
	switch apiErr.Kind {
	case vshttp.KindInvalidJSON, vshttp.KindInvalidResponse, vshttp.KindResponseTooLarge:
		apiErr.Legacy = ErrInvalidFeed
	default:
		apiErr.Legacy = ErrUnavailable
	}
	return apiErr
}
