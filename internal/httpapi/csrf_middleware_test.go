package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccar-p/study-platform/internal/auth"
)

func TestAnonymousCSRFIssueAndValidate(t *testing.T) {
	c := newAnonymousCSRF([]byte("test-secret-test-secret-test"), false)
	w := httptest.NewRecorder()
	token, err := c.issue(w)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	if token == "" || !strings.Contains(token, ".") {
		t.Fatalf("unexpected token: %s", token)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	req.Header.Set("X-CSRF-Token", token)
	if !c.valid(req) {
		t.Fatalf("expected valid token")
	}
}

func TestAnonymousCSRFRejectsTamperedSignature(t *testing.T) {
	c := newAnonymousCSRF([]byte("test-secret-test-secret-test"), false)
	w := httptest.NewRecorder()
	token, err := c.issue(w)
	if err != nil {
		t.Fatalf("issue failed: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected token shape: %s", token)
	}
	tampered := parts[0] + "." + parts[1] + "." + strings.Repeat("A", len(parts[2]))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: tampered})
	req.Header.Set("X-CSRF-Token", tampered)
	if c.valid(req) {
		t.Fatalf("expected tampered signature rejected")
	}
}

func TestAnonymousCSRFRejectsExpiredToken(t *testing.T) {
	c := newAnonymousCSRF([]byte("test-secret-test-secret-test"), false)
	w := httptest.NewRecorder()
	token, _ := c.issue(w)
	parts := strings.Split(token, ".")
	expired := parts[0] + "." + time.Now().Add(-time.Minute).UTC().Format(time.RFC3339) + "." + parts[2]
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: expired})
	req.Header.Set("X-CSRF-Token", expired)
	if c.valid(req) {
		t.Fatalf("expected expired token rejected")
	}
}

func TestAnonymousCSRFRejectsMissingHeaderMatch(t *testing.T) {
	c := newAnonymousCSRF([]byte("test-secret-test-secret-test"), false)
	w := httptest.NewRecorder()
	token, _ := c.issue(w)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: token})
	if c.valid(req) {
		t.Fatalf("expected header mismatch rejected")
	}
}

func TestRequestCSRFTokenReadsHeaderAndForm(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("csrf_token=form-token"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-CSRF-Token", "header-token")
	if got := requestCSRFToken(r); got != "header-token" {
		t.Fatalf("header should win: %s", got)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("csrf_token=form-token"))
	r2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := requestCSRFToken(r2); got != "form-token" {
		t.Fatalf("form fallback failed: %s", got)
	}
}

func TestAccountKeyIsNormalizedAndHashed(t *testing.T) {
	a := accountKey("User@Example.com")
	b := accountKey("user@example.com ")
	c := accountKey("Other@Example.com")
	if a != b {
		t.Fatalf("expected normalized equality, got %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("expected different keys for different accounts")
	}
	if len(a) == 0 {
		t.Fatalf("expected non-empty key")
	}
}

func TestLoginLimiterUsesBothIPAndAccountKeys(t *testing.T) {
	limiter := newLoginLimiter(2, time.Hour)
	now := time.Now()
	limiter.failure("10.0.0.1", accountKey("a@example.com"), now)
	limiter.failure("10.0.0.1", accountKey("a@example.com"), now)
	if retry := limiter.retryAfter("10.0.0.1", accountKey("a@example.com"), now); retry <= 0 {
		t.Fatalf("expected IP+account throttled")
	}
	if retry := limiter.retryAfter("10.0.0.2", accountKey("a@example.com"), now); retry <= 0 {
		t.Fatalf("expected account-only throttled from different IP")
	}
	if retry := limiter.retryAfter("10.0.0.99", accountKey("never@example.com"), now); retry > 0 {
		t.Fatalf("expected fresh IP+account not throttled, got %s", retry)
	}
	limiter.success("10.0.0.1", accountKey("a@example.com"))
	if retry := limiter.retryAfter("10.0.0.1", accountKey("a@example.com"), now); retry > 0 {
		t.Fatalf("expected throttle cleared after success")
	}
}

func TestLoginLimiterSuccessClearsBothKeys(t *testing.T) {
	limiter := newLoginLimiter(1, time.Hour)
	now := time.Now()
	limiter.failure("10.0.0.1", accountKey("a@example.com"), now)
	limiter.failure("10.0.0.5", accountKey("a@example.com"), now)
	limiter.success("10.0.0.1", accountKey("a@example.com"))
	if retry := limiter.retryAfter("10.0.0.1", accountKey("a@example.com"), now); retry > 0 {
		t.Fatalf("ip key should be cleared")
	}
	if retry := limiter.retryAfter("10.0.0.5", accountKey("a@example.com"), now); retry <= 0 {
		t.Fatalf("account key should still throttle from other IP")
	}
}

type stubAuthenticator struct {
	user    auth.User
	session auth.Session
	err     error
}

func (s stubAuthenticator) Authenticate(_ context.Context, _ string) (auth.User, auth.Session, error) {
	return s.user, s.session, s.err
}

func TestRequireAuthWritesUnauthorizedOnFailure(t *testing.T) {
	svc := stubAuthenticator{err: auth.ErrInvalidSession}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	called := false
	RequireAuth(svc, func(http.ResponseWriter, *http.Request) { called = true })(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d %s", w.Code, w.Body.String())
	}
	if called {
		t.Fatalf("handler must not be invoked on failure")
	}
}

func TestRequireAuthPopulatesContextOnSuccess(t *testing.T) {
	session := auth.Session{ID: "s-1"}
	user := auth.User{ID: "u-1", Role: auth.RoleStudent, IsActive: true}
	svc := stubAuthenticator{user: user, session: session}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "token"})
	w := httptest.NewRecorder()
	RequireAuth(svc, func(_ http.ResponseWriter, r *http.Request) {
		gotUser, gotSession, ok := currentAuth(r.Context())
		if !ok {
			t.Fatalf("expected authentication in context")
		}
		if gotUser.ID != user.ID || gotSession.ID != session.ID {
			t.Fatalf("unexpected auth context: %+v %+v", gotUser, gotSession)
		}
	})(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireAdminRejectsAnonymous(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin", nil)
	w := httptest.NewRecorder()
	RequireAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatalf("next should not run")
	})).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRequireAdminRejectsInactive(t *testing.T) {
	user := auth.User{ID: "u-1", Role: auth.RoleAdmin, IsActive: false}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin", nil)
	ctx := context.WithValue(r.Context(), authContextKey{}, authenticated{user: user})
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	RequireAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatalf("next should not run")
	})).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireAdminAllowsActiveAdmin(t *testing.T) {
	user := auth.User{ID: "u-1", Role: auth.RoleAdmin, IsActive: true}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin", nil)
	ctx := context.WithValue(r.Context(), authContextKey{}, authenticated{user: user})
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	called := false
	RequireAdmin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)
	if w.Code != http.StatusOK || !called {
		t.Fatalf("expected admin to be allowed, got %d called=%v", w.Code, called)
	}
}

func TestEvaluateRateLimitNonEnumeration(t *testing.T) {
	limiter := newLoginLimiter(1, time.Hour)
	now := time.Now()
	if decision := evaluateRateLimit(limiter, "10.0.0.1", "unknown@example.com", now); decision.blocked {
		t.Fatalf("expected no throttle on first attempt")
	}
	recordRateLimitFailure(limiter, "10.0.0.1", "unknown@example.com", now)
	decision := evaluateRateLimit(limiter, "10.0.0.1", "unknown@example.com", now)
	if !decision.blocked || decision.retryAfter <= 0 {
		t.Fatalf("expected throttle after failure: %+v", decision)
	}
}

func TestClientIPFromRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.10:54321"
	if got := clientIP(r); got != "192.0.2.10" {
		t.Fatalf("unexpected ip: %s", got)
	}
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "192.0.2.10"
	if got := clientIP(r2); got != "192.0.2.10" {
		t.Fatalf("unexpected ip without port: %s", got)
	}
}
