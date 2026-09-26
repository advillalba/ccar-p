package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRepo struct {
	users          map[string]User
	usersByID      map[string]User
	sessions       map[string]Session
	usersByToken   map[string]sessionPair
	lastLoginCalls []string
	cleanupCalls   int
	created        []User
}

type sessionPair struct {
	session Session
	user    User
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		users:        make(map[string]User),
		usersByID:    make(map[string]User),
		sessions:     make(map[string]Session),
		usersByToken: make(map[string]sessionPair),
	}
}

func (f *fakeRepo) CreateUser(_ context.Context, email, displayName, passwordHash string) (User, error) {
	for _, existing := range f.users {
		if existing.Email == email {
			return User{}, ErrDuplicateEmail
		}
	}
	user := User{ID: "user-1", Email: email, DisplayName: displayName, PasswordHash: passwordHash, Role: RoleStudent, IsActive: true, CreatedAt: time.Now()}
	f.users[email] = user
	f.usersByID[user.ID] = user
	f.created = append(f.created, user)
	return user, nil
}

func (f *fakeRepo) UserByEmail(_ context.Context, email string) (User, error) {
	if user, ok := f.users[email]; ok {
		return user, nil
	}
	return User{}, ErrInvalidLogin
}

func (f *fakeRepo) UserByID(_ context.Context, id string) (User, error) {
	if user, ok := f.usersByID[id]; ok {
		return user, nil
	}
	return User{}, ErrInvalidLogin
}

func (f *fakeRepo) CreateSession(_ context.Context, userID string, tokenHash, csrfHash []byte, expiresAt time.Time) (Session, error) {
	session := Session{ID: "session-1", UserID: userID, TokenHash: tokenHash, CSRFHash: csrfHash, ExpiresAt: expiresAt, CreatedAt: time.Now(), LastSeenAt: time.Now()}
	f.sessions[string(tokenHash)] = session
	f.usersByToken[string(tokenHash)] = sessionPair{session: session, user: f.usersByID[userID]}
	return session, nil
}

func (f *fakeRepo) SessionByTokenHash(_ context.Context, tokenHash []byte) (Session, User, error) {
	pair, ok := f.usersByToken[string(tokenHash)]
	if !ok {
		return Session{}, User{}, ErrInvalidSession
	}
	return pair.session, pair.user, nil
}

func (f *fakeRepo) RevokeSession(_ context.Context, tokenHash []byte, now time.Time) error {
	if pair, ok := f.usersByToken[string(tokenHash)]; ok {
		t := now
		pair.session.RevokedAt = &t
		f.usersByToken[string(tokenHash)] = pair
	}
	return nil
}

func (f *fakeRepo) RevokeUserSessions(_ context.Context, userID string, now time.Time) error {
	for key, pair := range f.usersByToken {
		if pair.session.UserID == userID && pair.session.RevokedAt == nil {
			t := now
			pair.session.RevokedAt = &t
			f.usersByToken[key] = pair
		}
	}
	return nil
}

func (f *fakeRepo) UpdateLastLogin(_ context.Context, id string, now time.Time) error {
	f.lastLoginCalls = append(f.lastLoginCalls, id)
	if user, ok := f.usersByID[id]; ok {
		user.LastLoginAt = &now
		f.usersByID[id] = user
		f.users[user.Email] = user
	}
	return nil
}

func (f *fakeRepo) TouchSession(_ context.Context, tokenHash []byte, now time.Time) error {
	if pair, ok := f.usersByToken[string(tokenHash)]; ok {
		pair.session.LastSeenAt = now
		f.usersByToken[string(tokenHash)] = pair
	}
	return nil
}

func (f *fakeRepo) CleanupExpiredSessions(_ context.Context, _ time.Time, _ int) (int64, error) {
	f.cleanupCalls++
	return 0, nil
}

func (f *fakeRepo) DeleteUser(_ context.Context, userID string) error {
	for email, user := range f.users {
		if user.ID == userID {
			delete(f.users, email)
			break
		}
	}
	delete(f.usersByID, userID)
	return nil
}

func TestRegisterCreatesStudentAccount(t *testing.T) {
	repo := newFakeRepo()
	service := NewService(repo, time.Hour)
	dto, fields, err := service.Register(context.Background(), "Student@Example.com", "  Student  ", "supersecretpwd1")
	if err != nil || len(fields) != 0 {
		t.Fatalf("unexpected register outcome: err=%v fields=%v", err, fields)
	}
	if dto.Role != RoleStudent || dto.Email != "student@example.com" {
		t.Fatalf("unexpected dto: %#v", dto)
	}
	if repo.created[0].PasswordHash == "supersecretpwd1" || len(repo.created[0].PasswordHash) < 30 {
		t.Fatalf("password hash must not be plaintext")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	repo := newFakeRepo()
	service := NewService(repo, time.Hour)
	if _, _, err := service.Register(context.Background(), "dup@example.com", "Name", "supersecretpwd1"); err != nil {
		t.Fatalf("seed register failed: %v", err)
	}
	_, _, err := service.Register(context.Background(), "DUP@example.com", "Other", "supersecretpwd1")
	if !errors.Is(err, ErrDuplicateEmail) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestRegisterValidationErrors(t *testing.T) {
	repo := newFakeRepo()
	service := NewService(repo, time.Hour)
	_, fields, err := service.Register(context.Background(), "ab", "", "abc")
	if err != nil {
		t.Fatalf("expected validation fields, got error: %v", err)
	}
	for _, key := range []string{"email", "display_name", "password"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("expected %q in field errors: %#v", key, fields)
		}
	}
}

func TestLoginRotationAndLastLogin(t *testing.T) {
	repo := newFakeRepo()
	service := NewService(repo, time.Hour)
	if _, _, err := service.Register(context.Background(), "user@example.com", "User", "supersecretpwd1"); err != nil {
		t.Fatalf("seed register failed: %v", err)
	}
	result, err := service.Login(context.Background(), "user@example.com", "supersecretpwd1")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if result.SessionToken == "" || result.CSRFToken == "" || len(repo.lastLoginCalls) != 1 {
		t.Fatalf("expected session/csrf and last login: %#v", result)
	}
	if _, err := service.Login(context.Background(), "user@example.com", "wrong-password"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("expected invalid login, got %v", err)
	}
}

func TestAuthenticateExpiryAndCSRF(t *testing.T) {
	repo := newFakeRepo()
	service := NewService(repo, time.Hour)
	if _, _, err := service.Register(context.Background(), "user@example.com", "User", "supersecretpwd1"); err != nil {
		t.Fatalf("seed register failed: %v", err)
	}
	result, err := service.Login(context.Background(), "user@example.com", "supersecretpwd1")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	user, session, err := service.Authenticate(context.Background(), result.SessionToken)
	if err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
	if !service.ValidateSessionCSRF(session, result.CSRFToken) {
		t.Fatalf("valid CSRF must pass")
	}
	if service.ValidateSessionCSRF(session, "tampered-token") {
		t.Fatalf("invalid CSRF must fail")
	}
	if _, _, err := service.Authenticate(context.Background(), "wrong"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expected invalid session for wrong token")
	}
	if _, _, err := service.Authenticate(context.Background(), ""); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expected invalid session for empty token")
	}
	if user.Role != RoleStudent {
		t.Fatalf("expected student role")
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	repo := newFakeRepo()
	service := NewService(repo, time.Hour)
	if _, _, err := service.Register(context.Background(), "user@example.com", "User", "supersecretpwd1"); err != nil {
		t.Fatalf("seed register failed: %v", err)
	}
	result, _ := service.Login(context.Background(), "user@example.com", "supersecretpwd1")
	if err := service.Logout(context.Background(), result.SessionToken); err != nil {
		t.Fatalf("logout failed: %v", err)
	}
	if _, _, err := service.Authenticate(context.Background(), result.SessionToken); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expected invalid session after logout")
	}
}

func TestPasswordHashingRoundTrip(t *testing.T) {
	hash, err := HashPassword("supersecretpwd1")
	if err != nil || hash == "supersecretpwd1" {
		t.Fatalf("hash should not equal plaintext: %v %q", err, hash)
	}
	if err := VerifyPassword(hash, "supersecretpwd1"); err != nil {
		t.Fatalf("verify should succeed: %v", err)
	}
	if err := VerifyPassword(hash, "supersecretpwd2"); err == nil {
		t.Fatalf("verify must fail for wrong password")
	}
}
