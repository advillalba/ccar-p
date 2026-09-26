package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/auth"
)

const (
	csrfCookieName = "ccarp_csrf"
	csrfLifetime   = 30 * time.Minute
)

type anonymousCSRF struct {
	secret []byte
	secure bool
}

func newAnonymousCSRF(secret []byte, secureCookies bool) *anonymousCSRF {
	if len(secret) == 0 {
		secret = []byte("ccar-p-development-only-do-not-use")
	}
	return &anonymousCSRF{secret: secret, secure: secureCookies}
}

func (c *anonymousCSRF) issue(w http.ResponseWriter) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("generate csrf token: " + err.Error())
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw[:])
	expires := time.Now().Add(csrfLifetime).UTC().Format(time.RFC3339)
	payload := nonce + "." + expires
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(payload))
	token := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: token, Path: "/", MaxAge: int(csrfLifetime.Seconds()), Secure: c.secure, SameSite: http.SameSiteLaxMode})
	return token, nil
}

func (c *anonymousCSRF) valid(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	if cookie.Value != requestCSRFToken(r) {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return false
	}
	expires, err := time.Parse(time.RFC3339, parts[1])
	if err != nil || !expires.After(time.Now()) {
		return false
	}
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	return hmac.Equal(mac.Sum(nil), signature)
}

type sessionCSRFValidator interface {
	ValidateSessionCSRF(session auth.Session, token string) bool
}

func validSessionCSRF(v sessionCSRFValidator, session auth.Session, r *http.Request) bool {
	if v == nil {
		return false
	}
	return v.ValidateSessionCSRF(session, requestCSRFToken(r))
}

func requestCSRFToken(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get("X-CSRF-Token")); token != "" {
		return token
	}
	return strings.TrimSpace(r.FormValue("csrf_token"))
}
