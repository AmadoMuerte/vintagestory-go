// Package auth provides a client for the Vintage Story authentication API.
package auth

import (
	"errors"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTOTPRequired       = errors.New("totp required")
	ErrIPChanged          = errors.New("ip changed")
	ErrTemporarilyBlocked = errors.New("temporarily blocked")
	ErrSessionExpired     = errors.New("session expired")
	ErrInvalidAuthReply   = errors.New("invalid auth response")
	ErrNetwork            = errors.New("auth network error")
	ErrServer             = errors.New("auth server error")
	ErrUnknown            = errors.New("unknown auth error")
)

// InvalidResponseError is kept for source compatibility. New code should
// use errors.As with *vshttp.APIError, which preserves the original cause
// (JSON syntax error, truncated body, size limit, ...).
type InvalidResponseError = vshttp.APIError

// legacy annotates a vshttp error with the matching package sentinel so
// errors.Is keeps working for ErrNetwork, ErrServer and ErrInvalidAuthReply.
func legacy(err error) error {
	var apiErr *vshttp.APIError
	if !errors.As(err, &apiErr) || apiErr.Legacy != nil {
		return err
	}
	switch apiErr.Kind {
	case vshttp.KindInvalidJSON, vshttp.KindInvalidResponse, vshttp.KindResponseTooLarge, vshttp.KindBodyRead:
		apiErr.Legacy = ErrInvalidAuthReply
	case vshttp.KindNetwork, vshttp.KindTimeout, vshttp.KindDNS, vshttp.KindTLS, vshttp.KindCanceled, vshttp.KindValidation:
		apiErr.Legacy = ErrNetwork
	default:
		apiErr.Legacy = ErrServer
	}
	return apiErr
}
