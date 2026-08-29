package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AmadoMuerte/vintagestory-go/vshttp"
)

const (
	VintageStoryLoginURL    = "https://auth3.vintagestory.at/v2/gamelogin"
	VintageStoryValidateURL = "https://auth3.vintagestory.at/clientvalidate"
	maxResponseBytes        = 1 << 20
)

// noRetry: login and validation are non-idempotent POST requests and are
// never retried automatically.
var noRetry = vshttp.RetryPolicy{MaxAttempts: 1}

// Client is an HTTP client for Vintage Story authentication.
type Client struct {
	httpClient            *http.Client
	loginURL, validateURL string
}

// NewClient creates an authentication client. A nil client uses bounded
// default timeouts for connect, TLS, headers and the overall request.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = vshttp.DefaultClient(20 * time.Second)
	}
	clone := *httpClient
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{httpClient: &clone, loginURL: VintageStoryLoginURL, validateURL: VintageStoryValidateURL}
}

// NewClientWithURLs creates a client with explicit endpoints, primarily for tests and controlled integrations.
func NewClientWithURLs(httpClient *http.Client, loginURL, validateURL string) *Client {
	client := NewClient(httpClient)
	client.loginURL, client.validateURL = loginURL, validateURL
	return client
}

// Login authenticates with an email and password. A TOTP challenge returns ErrTOTPRequired and a non-nil challenge.
func (c *Client) Login(ctx context.Context, email, password, totpCode, preLoginToken string) (Session, *TOTPChallenge, error) {
	form := url.Values{"email": {email}, "password": {password}}
	if totpCode != "" || preLoginToken != "" {
		form.Set("totpcode", totpCode)
		form.Set("prelogintoken", preLoginToken)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Session{}, nil, legacy(&vshttp.APIError{Operation: "auth: login", Method: http.MethodPost, Endpoint: c.loginURL, Kind: vshttp.KindValidation, Cause: err})
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response loginResponse
	if err := c.doJSON("auth: login", req, &response); err != nil {
		return Session{}, nil, err
	}
	if bool(response.Valid) {
		if response.SessionKey == nil || *response.SessionKey == "" || response.SessionSignature == nil || *response.SessionSignature == "" || response.UID == nil || *response.UID == "" || response.PlayerName == nil || *response.PlayerName == "" {
			return Session{}, nil, &vshttp.APIError{Operation: "auth: login", Method: http.MethodPost, Endpoint: c.loginURL, StatusCode: http.StatusOK, Kind: vshttp.KindValidation, Legacy: ErrInvalidAuthReply, Cause: fmt.Errorf("valid session reply is missing required fields")}
		}
		return Session{SessionKey: *response.SessionKey, SessionSignature: *response.SessionSignature, UID: *response.UID, PlayerName: *response.PlayerName}, nil, nil
	}
	reason := ""
	if response.Reason != nil {
		reason = strings.ToLower(strings.TrimSpace(*response.Reason))
	}
	switch reason {
	case "requiretotpcode":
		if response.PreLoginToken == nil || *response.PreLoginToken == "" {
			return Session{}, nil, &vshttp.APIError{Operation: "auth: login", Method: http.MethodPost, Endpoint: c.loginURL, StatusCode: http.StatusOK, Kind: vshttp.KindValidation, Legacy: ErrInvalidAuthReply, Cause: fmt.Errorf("totp challenge is missing the pre-login token")}
		}
		return Session{}, &TOTPChallenge{PreLoginToken: *response.PreLoginToken}, ErrTOTPRequired
	case "invalidemailorpassword", "invalidtotpcode":
		return Session{}, nil, ErrInvalidCredentials
	case "ipchanged":
		return Session{}, nil, ErrIPChanged
	case "temporarilyblocked":
		return Session{}, nil, ErrTemporarilyBlocked
	default:
		return Session{}, nil, ErrUnknown
	}
}

// Validate reports whether a session key remains valid for a player UID.
func (c *Client) Validate(ctx context.Context, uid, sessionKey string) (bool, error) {
	form := url.Values{"uid": {uid}, "sessionkey": {sessionKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.validateURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false, legacy(&vshttp.APIError{Operation: "auth: validate session", Method: http.MethodPost, Endpoint: c.validateURL, Kind: vshttp.KindValidation, Cause: err})
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response validateResponse
	if err := c.doJSON("auth: validate session", req, &response); err != nil {
		return false, err
	}
	return bool(response.Valid), nil
}

// doJSON performs a POST request without retries, checks the HTTP status
// before interpreting the body and decodes the JSON reply. The original
// failure is always preserved in the returned error chain.
func (c *Client) doJSON(op string, request *http.Request, target any) error {
	resp, body, err := vshttp.Do(request.Context(), c.httpClient, noRetry, op, request, maxResponseBytes)
	if err != nil {
		return legacy(err)
	}
	if err := vshttp.CheckStatus(op, request, resp); err != nil {
		return legacy(err)
	}
	return legacy(vshttp.DecodeJSON(op, request, resp, body, target))
}
