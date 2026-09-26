package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ccar-p/study-platform/internal/publishing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrMissingBearerToken     = errors.New("mcp: missing bearer token")
	ErrInvalidBearerFormat    = errors.New("mcp: bearer token must be non-empty")
	ErrTokenHashNotConfigured = errors.New("mcp: server token hash is not configured")
	ErrTokenMismatch          = errors.New("mcp: bearer token did not match")
	ErrTokenExpired           = errors.New("mcp: service token expired")
	ErrTokenRevoked           = errors.New("mcp: service token revoked")
	ErrContentAuthorInactive  = errors.New("mcp: content author is missing or inactive")
)

const bootstrapTokenID = "00000000-0000-0000-0000-000000000001"

type TokenRecord struct {
	ID              string
	Name            string
	Hash            []byte
	ContentAuthorID string
	Scopes          Scopes
	ExpiresAt       *time.Time
	RevokedAt       *time.Time
}

type TokenRepository interface {
	FindServiceTokenByHash(context.Context, []byte) (TokenRecord, error)
	ValidateContentAuthor(context.Context, string) error
	TouchServiceToken(context.Context, string, time.Time) error
}

type TokenAuthenticator struct {
	expectedHash           []byte
	repository             TokenRepository
	bootstrap              Scopes
	bootstrapContentAuthor string
	now                    func() time.Time
}

func NewTokenAuthenticator(expectedHexHash string) (*TokenAuthenticator, error) {
	return NewTokenAuthenticatorWithRepository(expectedHexHash, nil, AllScopes(), "")
}

func NewTokenAuthenticatorWithRepository(expectedHexHash string, repository TokenRepository, bootstrapScopes Scopes, bootstrapContentAuthor string) (*TokenAuthenticator, error) {
	hash, err := decodeHexHash(expectedHexHash)
	if err != nil {
		return nil, err
	}
	if len(bootstrapScopes) == 0 {
		return nil, errors.New("mcp: bootstrap scopes are required")
	}
	return &TokenAuthenticator{expectedHash: hash, repository: repository, bootstrap: bootstrapScopes.Clone(), bootstrapContentAuthor: strings.TrimSpace(bootstrapContentAuthor), now: time.Now}, nil
}

func (a *TokenAuthenticator) Authenticate(r *http.Request) (publishing.Actor, error) {
	principal, err := a.authenticate(r.Context(), r)
	return principal.Actor, err
}

func (a *TokenAuthenticator) authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	if a == nil || len(a.expectedHash) == 0 {
		return Principal{}, ErrTokenHashNotConfigured
	}
	bearer, err := extractBearer(r)
	if err != nil {
		return Principal{}, err
	}
	sum := sha256.Sum256([]byte(bearer))
	if subtle.ConstantTimeCompare(sum[:], a.expectedHash) == 1 {
		if err := a.validateContentAuthor(ctx, a.bootstrapContentAuthor); err != nil {
			return Principal{}, err
		}
		return Principal{Actor: publishing.Actor{Kind: publishing.ActorServiceToken, ID: bootstrapTokenID, Name: "bootstrap"}, ContentAuthorID: a.bootstrapContentAuthor, Scopes: a.bootstrap.Clone(), Bootstrap: true}, nil
	}
	if a.repository == nil {
		return Principal{}, ErrTokenMismatch
	}
	record, err := a.repository.FindServiceTokenByHash(ctx, sum[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Principal{}, ErrTokenMismatch
		}
		return Principal{}, err
	}
	now := a.now().UTC()
	if record.RevokedAt != nil {
		return Principal{}, ErrTokenRevoked
	}
	if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
		return Principal{}, ErrTokenExpired
	}
	if len(record.Scopes) == 0 {
		return Principal{}, ErrTokenMismatch
	}
	if err := a.validateContentAuthor(ctx, record.ContentAuthorID); err != nil {
		return Principal{}, err
	}
	_ = a.repository.TouchServiceToken(ctx, record.ID, now)
	return Principal{Actor: publishing.Actor{Kind: publishing.ActorServiceToken, ID: record.ID, Name: record.Name}, ContentAuthorID: record.ContentAuthorID, Scopes: record.Scopes.Clone()}, nil
}

func (a *TokenAuthenticator) validateContentAuthor(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if a.repository == nil {
		return nil
	}
	return a.repository.ValidateContentAuthor(ctx, id)
}

func extractBearer(r *http.Request) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", ErrMissingBearerToken
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", ErrInvalidBearerFormat
	}
	bearer := strings.TrimSpace(parts[1])
	if bearer == "" {
		return "", ErrInvalidBearerFormat
	}
	return bearer, nil
}

func decodeHexHash(value string) ([]byte, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "sha256:"))
	if value == "" {
		return nil, ErrTokenHashNotConfigured
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, fmt.Errorf("mcp: token hash must be a SHA-256 hex digest")
	}
	return decoded, nil
}

type TokenDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type PostgresTokenRepository struct {
	db TokenDB
}

func NewPostgresTokenRepository(db TokenDB) *PostgresTokenRepository {
	return &PostgresTokenRepository{db: db}
}

func (r *PostgresTokenRepository) FindServiceTokenByHash(ctx context.Context, hash []byte) (TokenRecord, error) {
	var record TokenRecord
	var scopes []string
	err := r.db.QueryRow(ctx, `SELECT id, name, token_hash, content_author_id, scopes, expires_at, revoked_at FROM service_tokens WHERE token_hash = $1`, hash).Scan(&record.ID, &record.Name, &record.Hash, &record.ContentAuthorID, &scopes, &record.ExpiresAt, &record.RevokedAt)
	if err != nil {
		return TokenRecord{}, err
	}
	record.Scopes, err = ParseScopes(scopes)
	if err != nil {
		return TokenRecord{}, err
	}
	return record, nil
}

func (r *PostgresTokenRepository) ValidateContentAuthor(ctx context.Context, id string) error {
	var valid bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND is_active)`, id).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrContentAuthorInactive
	}
	return nil
}

func (r *PostgresTokenRepository) TouchServiceToken(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE service_tokens SET last_used_at = $2 WHERE id = $1`, id, at)
	return err
}

type ServiceTokenManager struct {
	db  TokenDB
	now func() time.Time
}

func NewServiceTokenManager(db TokenDB) *ServiceTokenManager {
	return &ServiceTokenManager{db: db, now: time.Now}
}

type CreatedServiceToken struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Token           string     `json:"token"`
	ContentAuthorID string     `json:"content_author_id"`
	Scopes          []Scope    `json:"scopes"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
}

func (m *ServiceTokenManager) Create(ctx context.Context, name, contentAuthorID string, scopes Scopes, expiresAt *time.Time) (CreatedServiceToken, error) {
	name = strings.TrimSpace(name)
	contentAuthorID = strings.TrimSpace(contentAuthorID)
	if name == "" || contentAuthorID == "" || len(scopes) == 0 {
		return CreatedServiceToken{}, errors.New("mcp: token name, content author, and scopes are required")
	}
	if expiresAt != nil && !expiresAt.After(m.now()) {
		return CreatedServiceToken{}, errors.New("mcp: token expiry must be in the future")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return CreatedServiceToken{}, err
	}
	plaintext := "mcp_" + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plaintext))
	var id string
	if err := m.db.QueryRow(ctx, `INSERT INTO service_tokens (name, token_hash, content_author_id, scopes, expires_at) VALUES ($1, $2, $3, $4, $5) RETURNING id`, name, hash[:], contentAuthorID, scopeStrings(scopes), expiresAt).Scan(&id); err != nil {
		return CreatedServiceToken{}, err
	}
	return CreatedServiceToken{ID: id, Name: name, Token: plaintext, ContentAuthorID: contentAuthorID, Scopes: scopes.List(), ExpiresAt: expiresAt}, nil
}

func (m *ServiceTokenManager) Revoke(ctx context.Context, id string) error {
	tag, err := m.db.Exec(ctx, `UPDATE service_tokens SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1`, id, m.now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("mcp: service token not found")
	}
	return nil
}

func scopeStrings(scopes Scopes) []string {
	list := scopes.List()
	out := make([]string, len(list))
	for i, scope := range list {
		out[i] = string(scope)
	}
	return out
}
