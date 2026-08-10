package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginAndTOTP(t *testing.T) {
	for _, tc := range []struct {
		body string
		want error
		totp bool
	}{{`{"valid":"true","sessionkey":"key","sessionsignature":"sig","uid":"uid","playername":"Ada"}`, nil, false}, {`{"valid":0,"reason":"requiretotpcode","prelogintoken":"pre"}`, ErrTOTPRequired, true}, {`{"valid":false,"reason":"invalidemailorpassword"}`, ErrInvalidCredentials, false}, {`{`, ErrInvalidAuthReply, false}} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Error("not POST")
			}
			_ = r.ParseForm()
			if r.Form.Get("email") != "a@b.test" {
				t.Error("missing email")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, tc.body)
		}))
		session, challenge, err := NewClientWithURLs(s.Client(), s.URL, s.URL).Login(context.Background(), "a@b.test", "secret", "", "")
		s.Close()
		if !errors.Is(err, tc.want) || tc.totp != (challenge != nil) {
			t.Fatalf("got %v %#v", err, challenge)
		}
		if tc.want == nil && session.UID != "uid" {
			t.Fatal("session not parsed")
		}
	}
}
func TestValidateAndServerFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Method != http.MethodPost || r.Form.Get("uid") != "u" || r.Form.Get("sessionkey") != "k" {
			t.Error("wrong request")
		}
		_, _ = io.WriteString(w, `{"valid":"0"}`)
	}))
	ok, e := NewClientWithURLs(s.Client(), s.URL, s.URL).Validate(context.Background(), "u", "k")
	s.Close()
	if e != nil || ok {
		t.Fatalf("got %v %v", ok, e)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "bad", 500) }))
	_, _, e = NewClientWithURLs(bad.Client(), bad.URL, bad.URL).Login(context.Background(), "a", "b", "", "")
	bad.Close()
	if !errors.Is(e, ErrServer) {
		t.Fatalf("got %v", e)
	}
}
