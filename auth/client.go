package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	VintageStoryLoginURL    = "https://auth3.vintagestory.at/v2/gamelogin"
	VintageStoryValidateURL = "https://auth3.vintagestory.at/clientvalidate"
	maxResponseBytes        = 1 << 20
)

// Client is an HTTP client for Vintage Story authentication.
type Client struct {
	httpClient            *http.Client
	loginURL, validateURL string
}

// NewClient creates an authentication client. A nil client uses bounded default timeouts.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, ExpectContinueTimeout: time.Second}}
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
		return Session{}, nil, fmt.Errorf("create login request: %w", ErrNetwork)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response loginResponse
	if err := c.doJSON(req, &response); err != nil {
		return Session{}, nil, err
	}
	if bool(response.Valid) {
		if response.SessionKey == nil || *response.SessionKey == "" || response.SessionSignature == nil || *response.SessionSignature == "" || response.UID == nil || *response.UID == "" || response.PlayerName == nil || *response.PlayerName == "" {
			return Session{}, nil, ErrInvalidAuthReply
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
			return Session{}, nil, ErrInvalidAuthReply
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
		return false, fmt.Errorf("create validation request: %w", ErrNetwork)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response validateResponse
	if err := c.doJSON(req, &response); err != nil {
		return false, err
	}
	return bool(response.Valid), nil
}

func (c *Client) doJSON(request *http.Request, target any) error {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", ErrNetwork)
	}
	defer response.Body.Close()
	contentType := response.Header.Get("Content-Type")
	contents, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	invalid := func(size int) error {
		return &InvalidResponseError{StatusCode: response.StatusCode, ContentType: contentType, BodySize: size, Cause: ErrInvalidAuthReply}
	}
	if readErr != nil {
		return invalid(0)
	}
	if len(contents) > maxResponseBytes {
		return invalid(len(contents))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("authentication server returned HTTP %d: %w", response.StatusCode, ErrServer)
	}
	trimmed := bytes.TrimSpace(contents)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return invalid(len(trimmed))
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode authentication response: %w", invalid(len(trimmed)))
	}
	var trailing any
	err = decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing authentication response: %w", invalid(len(trimmed)))
	}
	return invalid(len(trimmed))
}
