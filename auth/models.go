package auth

import (
	"encoding/json"
	"fmt"
	"strings"
)

type flexibleBool bool

func (value *flexibleBool) UnmarshalJSON(data []byte) error {
	switch strings.ToLower(strings.TrimSpace(string(data))) {
	case "1", "true", `"1"`, `"true"`:
		*value = true
	case "0", "false", `"0"`, `"false"`:
		*value = false
	default:
		return fmt.Errorf("unsupported boolean representation: %q", data)
	}
	return nil
}

// Session is the authenticated Vintage Story game session returned by Login.
type Session struct {
	SessionKey       string
	SessionSignature string
	UID              string
	PlayerName       string
}

// TOTPChallenge contains the token required to complete a two-factor login.
type TOTPChallenge struct{ PreLoginToken string }

type loginResponse struct {
	SessionKey       *string      `json:"sessionkey"`
	SessionSignature *string      `json:"sessionsignature"`
	UID              *string      `json:"uid"`
	PlayerName       *string      `json:"playername"`
	Valid            flexibleBool `json:"valid"`
	PreLoginToken    *string      `json:"prelogintoken"`
	Reason           *string      `json:"reason"`
}

type validateResponse struct {
	Valid flexibleBool `json:"valid"`
}

var _ json.Unmarshaler = (*flexibleBool)(nil)
