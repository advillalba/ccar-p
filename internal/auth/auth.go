package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	RoleStudent = "student"
	RoleAdmin   = "admin"
)

var (
	ErrDuplicateEmail = errors.New("email is already registered")
	ErrInvalidLogin   = errors.New("invalid email or password")
	ErrInactiveUser   = errors.New("user is inactive")
	ErrInvalidSession = errors.New("invalid session")
)

type User struct {
	ID           string
	Email        string
	DisplayName  string
	Role         string
	IsActive     bool
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	PasswordHash string
}

type UserDTO struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (u User) DTO() UserDTO {
	return UserDTO{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, Role: u.Role, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt}
}

type Session struct {
	ID         string
	UserID     string
	TokenHash  []byte
	CSRFHash   []byte
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}

type Repository interface {
	CreateUser(context.Context, string, string, string) (User, error)
	UserByEmail(context.Context, string) (User, error)
	UserByID(context.Context, string) (User, error)
	CreateSession(context.Context, string, []byte, []byte, time.Time) (Session, error)
	SessionByTokenHash(context.Context, []byte) (Session, User, error)
	RevokeSession(context.Context, []byte, time.Time) error
	RevokeUserSessions(context.Context, string, time.Time) error
	UpdateLastLogin(context.Context, string, time.Time) error
	TouchSession(context.Context, []byte, time.Time) error
	CleanupExpiredSessions(context.Context, time.Time, int) (int64, error)
	DeleteUser(ctx context.Context, userID string) error
}

type Service struct {
	repo            Repository
	sessionLifetime time.Duration
	now             func() time.Time
}

func NewService(repo Repository, sessionLifetime time.Duration) *Service {
	if sessionLifetime <= 0 {
		sessionLifetime = 14 * 24 * time.Hour
	}
	return &Service{repo: repo, sessionLifetime: sessionLifetime, now: time.Now}
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func ValidateRegistration(email, displayName, password string) map[string]string {
	fields := make(map[string]string)
	email = NormalizeEmail(email)
	if len(email) < 3 || len(email) > 254 {
		fields["email"] = "Enter a username with 3-254 characters (email or username allowed)."
	}
	if len(strings.TrimSpace(displayName)) == 0 || len([]rune(strings.TrimSpace(displayName))) > 100 {
		fields["display_name"] = "Enter a display name of up to 100 characters."
	}
	if err := ValidatePassword(password); err != nil {
		fields["password"] = err.Error()
	}
	return fields
}

func (s *Service) Register(ctx context.Context, email, displayName, password string) (UserDTO, map[string]string, error) {
	fields := ValidateRegistration(email, displayName, password)
	if len(fields) > 0 {
		return UserDTO{}, fields, nil
	}
	hash, err := HashPassword(password)
	if err != nil {
		return UserDTO{}, nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.repo.CreateUser(ctx, NormalizeEmail(email), strings.TrimSpace(displayName), hash)
	if err != nil {
		return UserDTO{}, nil, err
	}
	return user.DTO(), nil, nil
}

type LoginResult struct {
	User         UserDTO
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
}

func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	user, err := s.repo.UserByEmail(ctx, NormalizeEmail(email))
	if err != nil || !user.IsActive || VerifyPassword(user.PasswordHash, password) != nil {
		return LoginResult{}, ErrInvalidLogin
	}
	now := s.now().UTC()
	if err := s.repo.RevokeUserSessions(ctx, user.ID, now); err != nil {
		return LoginResult{}, fmt.Errorf("rotate user sessions: %w", err)
	}
	sessionToken, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	expiresAt := now.Add(s.sessionLifetime)
	if _, err = s.repo.CreateSession(ctx, user.ID, tokenHash(sessionToken), tokenHash(csrfToken), expiresAt); err != nil {
		return LoginResult{}, fmt.Errorf("create session: %w", err)
	}
	if err = s.repo.UpdateLastLogin(ctx, user.ID, now); err != nil {
		return LoginResult{}, fmt.Errorf("update last login: %w", err)
	}
	user.LastLoginAt = &now
	return LoginResult{User: user.DTO(), SessionToken: sessionToken, CSRFToken: csrfToken, ExpiresAt: expiresAt}, nil
}

func (s *Service) Authenticate(ctx context.Context, sessionToken string) (User, Session, error) {
	if sessionToken == "" {
		return User{}, Session{}, ErrInvalidSession
	}
	session, user, err := s.repo.SessionByTokenHash(ctx, tokenHash(sessionToken))
	if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(s.now()) || !user.IsActive {
		return User{}, Session{}, ErrInvalidSession
	}
	if err := s.repo.TouchSession(ctx, tokenHash(sessionToken), s.now().UTC()); err != nil {
		return User{}, Session{}, fmt.Errorf("touch session: %w", err)
	}
	return user, session, nil
}

func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return s.repo.RevokeSession(ctx, tokenHash(sessionToken), s.now().UTC())
}

func (s *Service) ValidateSessionCSRF(session Session, token string) bool {
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare(session.CSRFHash, tokenHash(token)) == 1
}

func (s *Service) CleanupExpiredSessions(ctx context.Context, limit int) (int64, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.repo.CleanupExpiredSessions(ctx, s.now().UTC(), limit)
}

func (s *Service) DeleteUser(ctx context.Context, userID string) error {
	return s.repo.DeleteUser(ctx, userID)
}

func tokenHash(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
