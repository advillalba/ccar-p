package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/auth"
)

type registrationRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Remember any    `json:"remember"`
}

type AuthHandler struct {
	service       *auth.Service
	csrf          *anonymousCSRF
	secureCookies bool
	limiter       *loginLimiter
}

func NewAuthHandler(service *auth.Service, sessionSecret string, secureCookies bool) *AuthHandler {
	return &AuthHandler{service: service, csrf: newAnonymousCSRF([]byte(sessionSecret), secureCookies), secureCookies: secureCookies, limiter: newLoginLimiter(5, 10*time.Minute)}
}

func (h *AuthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/csrf", h.csrfEndpoint)
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", RequireAuth(h.service, h.logout))
	mux.HandleFunc("GET /api/v1/me", RequireAuth(h.service, h.me))
	mux.HandleFunc("DELETE /api/v1/me", RequireAuth(h.service, h.deleteAccount))
}

func (h *AuthHandler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return RequireAuth(h.service, next)
}

func (h *AuthHandler) csrfEndpoint(w http.ResponseWriter, r *http.Request) {
	token, err := h.csrf.issue(w)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	WriteData(w, r, http.StatusOK, map[string]string{"csrf_token": token})
}

func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	if !h.csrf.valid(r) {
		WriteError(w, r, http.StatusForbidden, "csrf_invalid", "CSRF validation failed", nil)
		return
	}
	var input registrationRequest
	if !decodeRequest(w, r, &input) {
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid", nil)
		return
	}
	user, fields, err := h.service.Register(r.Context(), input.Email, input.DisplayName, input.Password)
	if len(fields) > 0 {
		WriteError(w, r, http.StatusUnprocessableEntity, "validation_failed", "Some fields need attention.", fields)
		return
	}
	if errors.Is(err, auth.ErrDuplicateEmail) {
		WriteError(w, r, http.StatusConflict, "email_unavailable", "This username cannot be used.", map[string]string{"email": "This username cannot be used."})
		return
	}
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	WriteData(w, r, http.StatusCreated, user)
}

func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	if !h.csrf.valid(r) {
		WriteError(w, r, http.StatusForbidden, "csrf_invalid", "CSRF validation failed", nil)
		return
	}
	var input loginRequest
	if !decodeRequest(w, r, &input) {
		WriteError(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid", nil)
		return
	}
	ip := clientIP(r)
	now := time.Now()
	decision := evaluateRateLimit(h.limiter, ip, input.Email, now)
	if decision.blocked {
		w.Header().Set("Retry-After", decision.retryAfter.Round(time.Second).String())
		WriteError(w, r, http.StatusTooManyRequests, "login_throttled", "Try again later.", nil)
		return
	}
	result, err := h.service.Login(r.Context(), input.Email, input.Password)
	if err != nil {
		recordRateLimitFailure(h.limiter, ip, input.Email, now)
		WriteError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password.", nil)
		return
	}
	recordRateLimitSuccess(h.limiter, ip, input.Email)
	h.setSessionCookie(w, result.SessionToken, result.ExpiresAt)
	WriteData(w, r, http.StatusOK, map[string]any{"user": result.User, "csrf_token": result.CSRFToken})
}

func (h *AuthHandler) logout(w http.ResponseWriter, r *http.Request) {
	_, session, ok := currentAuth(r.Context())
	if !ok || !validSessionCSRF(h.service, session, r) {
		WriteError(w, r, http.StatusForbidden, "csrf_invalid", "CSRF validation failed", nil)
		return
	}
	if err := h.service.Logout(r.Context(), sessionToken(r)); err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	h.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) me(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentAuth(r.Context())
	WriteData(w, r, http.StatusOK, user.DTO())
}

func (h *AuthHandler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	user, _, ok := currentAuth(r.Context())
	if !ok {
		WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
		return
	}
	if err := h.service.DeleteUser(r.Context(), user.ID); err != nil {
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
		return
	}
	h.clearSessionCookie(w)
	// Clear practice cookie as well
	http.SetCookie(w, &http.Cookie{Name: practiceCookieName, Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", Expires: expires, MaxAge: maxAge, HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode})
}

func (h *AuthHandler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode})
}

func decodeRequest(_ http.ResponseWriter, r *http.Request, destination any) bool {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		return decoder.Decode(destination) == nil
	}
	if err := r.ParseForm(); err != nil {
		return false
	}
	switch v := destination.(type) {
	case *registrationRequest:
		v.Email, v.DisplayName, v.Password = r.FormValue("email"), r.FormValue("display_name"), r.FormValue("password")
	case *loginRequest:
		v.Email, v.Password = r.FormValue("email"), r.FormValue("password")
	default:
		return false
	}
	return true
}

func decodeJSONBody(r *http.Request, destination any) error {
	if r.Body == nil {
		return io.EOF
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}
