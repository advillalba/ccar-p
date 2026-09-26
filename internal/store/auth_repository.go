package store

import (
	"context"
	"errors"
	"time"

	"github.com/ccar-p/study-platform/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type AuthRepository struct{ db DBTX }

func NewAuthRepository(db DBTX) *AuthRepository { return &AuthRepository{db: db} }

func (r *AuthRepository) CreateUser(ctx context.Context, email, displayName, passwordHash string) (auth.User, error) {
	const query = `INSERT INTO users (email, display_name, password_hash, role) VALUES ($1, $2, $3, 'student') RETURNING id, email, display_name, role, is_active, last_login_at, created_at`
	var user auth.User
	err := r.db.QueryRow(ctx, query, email, displayName, passwordHash).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role, &user.IsActive, &user.LastLoginAt, &user.CreatedAt)
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return auth.User{}, auth.ErrDuplicateEmail
	}
	return user, err
}

func (r *AuthRepository) UserByEmail(ctx context.Context, email string) (auth.User, error) {
	const query = `SELECT id, email, display_name, password_hash, role, is_active, last_login_at, created_at FROM users WHERE email = $1`
	return scanUser(r.db.QueryRow(ctx, query, email))
}

func (r *AuthRepository) UserByID(ctx context.Context, id string) (auth.User, error) {
	const query = `SELECT id, email, display_name, password_hash, role, is_active, last_login_at, created_at FROM users WHERE id = $1`
	return scanUser(r.db.QueryRow(ctx, query, id))
}

func (r *AuthRepository) CreateSession(ctx context.Context, userID string, tokenHash, csrfHash []byte, expiresAt time.Time) (auth.Session, error) {
	const query = `INSERT INTO sessions (user_id, token_hash, csrf_hash, expires_at) VALUES ($1, $2, $3, $4) RETURNING id, user_id, token_hash, csrf_hash, expires_at, revoked_at, created_at, last_seen_at`
	return scanSession(r.db.QueryRow(ctx, query, userID, tokenHash, csrfHash, expiresAt))
}

func (r *AuthRepository) SessionByTokenHash(ctx context.Context, tokenHash []byte) (auth.Session, auth.User, error) {
	const query = `SELECT s.id, s.user_id, s.token_hash, s.csrf_hash, s.expires_at, s.revoked_at, s.created_at, s.last_seen_at, u.id, u.email, u.display_name, u.password_hash, u.role, u.is_active, u.last_login_at, u.created_at FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = $1`
	var session auth.Session
	var user auth.User
	err := r.db.QueryRow(ctx, query, tokenHash).Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CSRFHash, &session.ExpiresAt, &session.RevokedAt, &session.CreatedAt, &session.LastSeenAt, &user.ID, &user.Email, &user.DisplayName, &user.PasswordHash, &user.Role, &user.IsActive, &user.LastLoginAt, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Session{}, auth.User{}, auth.ErrInvalidSession
	}
	return session, user, err
}

func (r *AuthRepository) RevokeSession(ctx context.Context, tokenHash []byte, now time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE sessions SET revoked_at = COALESCE(revoked_at, $2) WHERE token_hash = $1`, tokenHash, now)
	return err
}

func (r *AuthRepository) RevokeUserSessions(ctx context.Context, userID string, now time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, now)
	return err
}

func (r *AuthRepository) UpdateLastLogin(ctx context.Context, userID string, now time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET last_login_at = $2 WHERE id = $1`, userID, now)
	return err
}

func (r *AuthRepository) TouchSession(ctx context.Context, tokenHash []byte, now time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE token_hash = $1`, tokenHash, now)
	return err
}

func (r *AuthRepository) CleanupExpiredSessions(ctx context.Context, now time.Time, limit int) (int64, error) {
	const query = `DELETE FROM sessions WHERE id IN (SELECT id FROM sessions WHERE expires_at <= $1 OR revoked_at IS NOT NULL ORDER BY expires_at NULLS FIRST LIMIT $2)`
	tag, err := r.db.Exec(ctx, query, now, limit)
	return tag.RowsAffected(), err
}

func (r *AuthRepository) DeleteUser(ctx context.Context, userID string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM practice_answers WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `DELETE FROM attempt_answers WHERE attempt_id IN (SELECT id FROM attempts WHERE user_id = $1::uuid)`, userID); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `DELETE FROM attempts WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	return err
}

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (auth.User, error) {
	var user auth.User
	err := row.Scan(&user.ID, &user.Email, &user.DisplayName, &user.PasswordHash, &user.Role, &user.IsActive, &user.LastLoginAt, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrInvalidLogin
	}
	return user, err
}

func scanSession(row scanner) (auth.Session, error) {
	var session auth.Session
	err := row.Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CSRFHash, &session.ExpiresAt, &session.RevokedAt, &session.CreatedAt, &session.LastSeenAt)
	return session, err
}
