package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ccar-p/study-platform/internal/auth"
)

const sessionCookieName = "ccarp_session"

type authContextKey struct{}

type authenticated struct {
	user    auth.User
	session auth.Session
}

func currentAuth(ctx context.Context) (auth.User, auth.Session, bool) {
	value, ok := ctx.Value(authContextKey{}).(authenticated)
	return value.user, value.session, ok
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func accountKey(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	sum := sha256.Sum256([]byte(normalized))
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

type Authenticator interface {
	Authenticate(ctx context.Context, sessionToken string) (auth.User, auth.Session, error)
}

func RequireAuth(svc Authenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, session, err := svc.Authenticate(r.Context(), sessionToken(r))
		if err != nil {
			WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, authenticated{user: user, session: session})))
	}
}

// OptionalAuth attaches the authenticated identity to the request context
// when a valid session is present, but never rejects the request. Handlers
// that support both authenticated users and anonymous guests use it to
// resolve the acting owner before falling back to a guest identity.
func OptionalAuth(svc Authenticator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if user, session, err := svc.Authenticate(r.Context(), sessionToken(r)); err == nil {
			r = r.WithContext(context.WithValue(r.Context(), authContextKey{}, authenticated{user: user, session: session}))
		}
		next(w, r)
	}
}

func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := currentAuth(r.Context())
		if !ok {
			WriteError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required.", nil)
			return
		}
		if !user.IsActive || user.Role != auth.RoleAdmin {
			WriteError(w, r, http.StatusForbidden, "administrator_required", "Administrator access is required.", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type loginLimit struct {
	attempts int
	reset    time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]loginLimit
	limit   int
	window  time.Duration
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{entries: make(map[string]loginLimit), limit: limit, window: window}
}

func (l *loginLimiter) retryAfter(ip, account string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.retryLocked(ip, account, now)
}

func (l *loginLimiter) retryLocked(ip, account string, now time.Time) time.Duration {
	for _, key := range []string{"ip:" + ip, "account:" + account} {
		entry := l.entries[key]
		if entry.reset.After(now) && entry.attempts >= l.limit {
			return time.Until(entry.reset)
		}
	}
	return 0
}

func (l *loginLimiter) failure(ip, account string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range []string{"ip:" + ip, "account:" + account} {
		entry := l.entries[key]
		if !entry.reset.After(now) {
			entry = loginLimit{reset: now.Add(l.window)}
		}
		entry.attempts++
		l.entries[key] = entry
	}
	if len(l.entries) > 4096 {
		for key, entry := range l.entries {
			if !entry.reset.After(now) {
				delete(l.entries, key)
			}
		}
	}
}

func (l *loginLimiter) success(ip, account string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, "ip:"+ip)
	delete(l.entries, "account:"+account)
}

type rateLimitDecision struct {
	retryAfter time.Duration
	blocked    bool
}

func evaluateRateLimit(limiter *loginLimiter, ip, email string, now time.Time) rateLimitDecision {
	account := accountKey(email)
	if retry := limiter.retryAfter(ip, account, now); retry > 0 {
		return rateLimitDecision{retryAfter: retry, blocked: true}
	}
	return rateLimitDecision{}
}

func recordRateLimitFailure(limiter *loginLimiter, ip, email string, now time.Time) {
	limiter.failure(ip, accountKey(email), now)
}

func recordRateLimitSuccess(limiter *loginLimiter, ip, email string) {
	limiter.success(ip, accountKey(email))
}
