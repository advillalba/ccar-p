package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/auth"
)

type stubRepo struct {
	users       map[string]auth.User
	usersByID   map[string]auth.User
	sessions    map[string]auth.Session
	sessionUser map[string]auth.User
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		users:       make(map[string]auth.User),
		usersByID:   make(map[string]auth.User),
		sessions:    make(map[string]auth.Session),
		sessionUser: make(map[string]auth.User),
	}
}

func (s *stubRepo) CreateUser(_ context.Context, email, displayName, hash string) (auth.User, error) {
	if _, ok := s.users[email]; ok {
		return auth.User{}, auth.ErrDuplicateEmail
	}
	user := auth.User{ID: "user-1", Email: email, DisplayName: displayName, PasswordHash: hash, Role: auth.RoleStudent, IsActive: true, CreatedAt: time.Now()}
	s.users[email] = user
	s.usersByID[user.ID] = user
	return user, nil
}

func (s *stubRepo) UserByEmail(_ context.Context, email string) (auth.User, error) {
	if user, ok := s.users[email]; ok {
		return user, nil
	}
	return auth.User{}, auth.ErrInvalidLogin
}

func (s *stubRepo) UserByID(_ context.Context, id string) (auth.User, error) {
	if user, ok := s.usersByID[id]; ok {
		return user, nil
	}
	return auth.User{}, auth.ErrInvalidLogin
}

func (s *stubRepo) CreateSession(_ context.Context, userID string, tokenHash, csrfHash []byte, expires time.Time) (auth.Session, error) {
	session := auth.Session{ID: "sess-1", UserID: userID, TokenHash: tokenHash, CSRFHash: csrfHash, ExpiresAt: expires, CreatedAt: time.Now(), LastSeenAt: time.Now()}
	s.sessions[string(tokenHash)] = session
	s.sessionUser[string(tokenHash)] = s.usersByID[userID]
	return session, nil
}

func (s *stubRepo) SessionByTokenHash(_ context.Context, tokenHash []byte) (auth.Session, auth.User, error) {
	session, ok := s.sessions[string(tokenHash)]
	if !ok {
		return auth.Session{}, auth.User{}, auth.ErrInvalidSession
	}
	return session, s.sessionUser[string(tokenHash)], nil
}

func (s *stubRepo) RevokeSession(_ context.Context, tokenHash []byte, now time.Time) error {
	if session, ok := s.sessions[string(tokenHash)]; ok {
		session.RevokedAt = &now
		s.sessions[string(tokenHash)] = session
	}
	return nil
}

func (s *stubRepo) RevokeUserSessions(_ context.Context, userID string, now time.Time) error {
	for key, session := range s.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
			s.sessions[key] = session
		}
	}
	return nil
}

func (s *stubRepo) UpdateLastLogin(_ context.Context, id string, now time.Time) error {
	if user, ok := s.usersByID[id]; ok {
		user.LastLoginAt = &now
		s.usersByID[id] = user
		s.users[user.Email] = user
	}
	return nil
}

func (s *stubRepo) TouchSession(_ context.Context, tokenHash []byte, now time.Time) error {
	if session, ok := s.sessions[string(tokenHash)]; ok {
		session.LastSeenAt = now
		s.sessions[string(tokenHash)] = session
	}
	return nil
}

func (s *stubRepo) CleanupExpiredSessions(_ context.Context, _ time.Time, _ int) (int64, error) {
	return 0, nil
}

func (s *stubRepo) DeleteUser(_ context.Context, userID string) error {
	for email, user := range s.users {
		if user.ID == userID {
			delete(s.users, email)
			break
		}
	}
	delete(s.usersByID, userID)
	return nil
}

func newAuthHandler() (*AuthHandler, *stubRepo) {
	repo := newStubRepo()
	service := auth.NewService(repo, time.Hour)
	return NewAuthHandler(service, "secret-secret-secret-secret", false), repo
}

func obtainCSRF(t *testing.T, h *AuthHandler) string {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	w := httptest.NewRecorder()
	h.csrfEndpoint(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("csrf failed: %d", w.Code)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			return cookie.Value
		}
	}
	t.Fatalf("csrf cookie missing")
	return ""
}

func TestRegisterEndpointReturnsValidationErrors(t *testing.T) {
	h, _ := newAuthHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	csrf := obtainCSRF(t, h)
	body := `{"email":"ab","display_name":"","password":"x"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d %s", w.Code, w.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Error.Fields["email"] == "" || env.Error.Fields["password"] == "" {
		t.Fatalf("expected field errors, got %s", w.Body.String())
	}
}

func TestRegisterEndpointDuplicateEmail(t *testing.T) {
	h, _ := newAuthHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	csrf := obtainCSRF(t, h)
	body := `{"email":"User@Example.com","display_name":"User","password":"supersecretpwd1"}`
	first := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	first.Header.Set("Content-Type", "application/json")
	first.Header.Set("X-CSRF-Token", csrf)
	first.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, first)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", w.Code, w.Body.String())
	}
	csrf2 := obtainCSRF(t, h)
	second := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	second.Header.Set("Content-Type", "application/json")
	second.Header.Set("X-CSRF-Token", csrf2)
	second.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf2})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, second)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", w.Code, w.Body.String())
	}
}

func TestRegisterCreatesOnlyStudent(t *testing.T) {
	h, repo := newAuthHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	csrf := obtainCSRF(t, h)
	body := `{"email":"user@example.com","display_name":"User","password":"supersecretpwd1"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	if repo.users["user@example.com"].Role != auth.RoleStudent {
		t.Fatalf("expected student role")
	}
}

func TestLoginSetsCookieAndMe(t *testing.T) {
	h, _ := newAuthHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	csrf := obtainCSRF(t, h)
	body := `{"email":"User@Example.com","display_name":"User","password":"supersecretpwd1"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed register failed: %d", w.Code)
	}
	loginCSRF := obtainCSRF(t, h)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"User@Example.com","password":"supersecretpwd1"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-CSRF-Token", loginCSRF)
	loginReq.AddCookie(&http.Cookie{Name: csrfCookieName, Value: loginCSRF})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, loginReq)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly {
		t.Fatalf("session cookie must be HttpOnly")
	}
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, meReq)
	if w.Code != http.StatusOK {
		t.Fatalf("me failed: %d %s", w.Code, w.Body.String())
	}
}

func TestLoginRotationAndNonEnumeratingFailure(t *testing.T) {
	h, _ := newAuthHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	csrf := obtainCSRF(t, h)
	body := `{"email":"User@Example.com","display_name":"User","password":"supersecretpwd1"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	mux.ServeHTTP(httptest.NewRecorder(), r)
	loginCSRF := obtainCSRF(t, h)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"User@Example.com","password":"supersecretpwd1"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("X-CSRF-Token", loginCSRF)
	login.AddCookie(&http.Cookie{Name: csrfCookieName, Value: loginCSRF})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, login)
	if w.Code != http.StatusOK {
		t.Fatalf("first login failed: %d", w.Code)
	}
	firstSession, _ := loginCookie(w)
	loginCSRF2 := obtainCSRF(t, h)
	login2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"User@Example.com","password":"supersecretpwd1"}`))
	login2.Header.Set("Content-Type", "application/json")
	login2.Header.Set("X-CSRF-Token", loginCSRF2)
	login2.AddCookie(&http.Cookie{Name: csrfCookieName, Value: loginCSRF2})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, login2)
	secondSession, _ := loginCookie(w)
	if firstSession.Value == secondSession.Value {
		t.Fatalf("expected session rotation")
	}
	firstMe := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	firstMe.AddCookie(firstSession)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, firstMe)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected first session revoked, got %d", w.Code)
	}
	missingCSRF := obtainCSRF(t, h)
	missing := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"Unknown@Example.com","password":"any"}`))
	missing.Header.Set("Content-Type", "application/json")
	missing.Header.Set("X-CSRF-Token", missingCSRF)
	missing.AddCookie(&http.Cookie{Name: csrfCookieName, Value: missingCSRF})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, missing)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid_credentials") {
		t.Fatalf("expected non-enumerating failure, got %d %s", w.Code, w.Body.String())
	}
}

func TestLogoutRequiresSessionCSRF(t *testing.T) {
	h, _ := newAuthHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	csrf := obtainCSRF(t, h)
	registerBody := `{"email":"User@Example.com","display_name":"User","password":"supersecretpwd1"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(registerBody))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed register failed: %d", w.Code)
	}
	loginCSRF := obtainCSRF(t, h)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"User@Example.com","password":"supersecretpwd1"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("X-CSRF-Token", loginCSRF)
	login.AddCookie(&http.Cookie{Name: csrfCookieName, Value: loginCSRF})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, login)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d", w.Code)
	}
	sessionCookie, _ := loginCookie(w)
	var envelope successEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	csrfToken := envelope.Data.(map[string]any)["csrf_token"].(string)
	logoutMissing := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutMissing.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, logoutMissing)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing CSRF, got %d", w.Code)
	}
	logoutTampered := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutTampered.AddCookie(sessionCookie)
	logoutTampered.Header.Set("X-CSRF-Token", "wrong-token")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, logoutTampered)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for tampered CSRF, got %d", w.Code)
	}
	logoutValid := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutValid.AddCookie(sessionCookie)
	logoutValid.Header.Set("X-CSRF-Token", csrfToken)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, logoutValid)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestRateLimiterBouncesAfterFailures(t *testing.T) {
	h, _ := newAuthHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	for i := 0; i < 5; i++ {
		csrf := obtainCSRF(t, h)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"Unknown@Example.com","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
		req.RemoteAddr = "10.1.1.1:1234"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, w.Code)
		}
	}
	csrf := obtainCSRF(t, h)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"Unknown@Example.com","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	req.RemoteAddr = "10.1.1.1:1234"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatalf("retry-after header expected")
	}
}

func TestRequireAdminRejectsStudent(t *testing.T) {
	h, _ := newAuthHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	csrf := obtainCSRF(t, h)
	body := `{"email":"user@example.com","display_name":"User","password":"supersecretpwd1"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("register failed: %d", w.Code)
	}
	loginCSRF := obtainCSRF(t, h)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"user@example.com","password":"supersecretpwd1"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("X-CSRF-Token", loginCSRF)
	login.AddCookie(&http.Cookie{Name: csrfCookieName, Value: loginCSRF})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, login)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d", w.Code)
	}
	sessionCookie, _ := loginCookie(w)
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, meReq)
	if w.Code != http.StatusOK {
		t.Fatalf("me failed: %d", w.Code)
	}
	user, session, _ := h.service.Authenticate(context.Background(), sessionCookie.Value)
	ctx := context.WithValue(meReq.Context(), authContextKey{}, authenticated{user: user, session: session})
	admin := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	adminReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin", nil).WithContext(ctx)
	w = httptest.NewRecorder()
	admin.ServeHTTP(w, adminReq)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", w.Code, w.Body.String())
	}
}

func loginCookie(w *httptest.ResponseRecorder) (*http.Cookie, *http.Cookie) {
	var session, csrf *http.Cookie
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case sessionCookieName:
			session = c
		case csrfCookieName:
			csrf = c
		}
	}
	return session, csrf
}
