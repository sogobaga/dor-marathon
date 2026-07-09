package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID           string  `db:"id"`
	Email        string  `db:"email"`
	Handle       string  `db:"handle"`
	Name         string  `db:"name"`
	PasswordHash string  `db:"password_hash"`
	AvatarURL    string  `db:"avatar_url"`
	TotalKm      float64 `db:"total_km"`
	Role         string  `db:"role"` // user | organizer | admin
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, email, handle, name, hash, role string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO users (email, handle, name, password_hash, role, vip_expires_at, vip_plan, vip_since)
		VALUES ($1, $2, $3, $4, $5,
		        NOW() + (COALESCE(NULLIF((SELECT value FROM app_settings WHERE key='vip_trial_days'),''),'14') || ' days')::interval,
		        'trial', NOW())
		RETURNING id, email, handle, name, password_hash,
		          COALESCE(avatar_url, '') as avatar_url, total_km, role
	`, email, handle, name, hash, role).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

func (r *Repository) FindByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, handle, name, COALESCE(password_hash,'') as password_hash,
		       COALESCE(avatar_url, '') as avatar_url, total_km, role
		FROM users WHERE email = $1
	`, email).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}
	return u, nil
}

func (r *Repository) FindByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, handle, name, COALESCE(password_hash,'') as password_hash,
		       COALESCE(avatar_url, '') as avatar_url, total_km, role
		FROM users WHERE id = $1
	`, id).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return u, nil
}

// FindByGoogleSub 透過 Google sub（user_identities）找使用者
func (r *Repository) FindByGoogleSub(ctx context.Context, sub string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.handle, u.name, COALESCE(u.password_hash,'') as password_hash,
		       COALESCE(u.avatar_url, '') as avatar_url, u.total_km, u.role
		FROM users u
		JOIN user_identities i ON i.user_id = u.id
		WHERE i.provider = 'google' AND i.provider_uid = $1
	`, sub).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by google sub: %w", err)
	}
	return u, nil
}

// CreateGoogleUser 建立 Google 登入會員（無密碼）並寫入 user_identities，於單一交易內完成
func (r *Repository) CreateGoogleUser(ctx context.Context, email, handle, name, avatarURL, sub string) (*User, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	u := &User{}
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, handle, name, password_hash, avatar_url, role, vip_expires_at, vip_plan, vip_since)
		VALUES ($1, $2, $3, NULL, NULLIF($4,''), 'user',
		        NOW() + (COALESCE(NULLIF((SELECT value FROM app_settings WHERE key='vip_trial_days'),''),'14') || ' days')::interval,
		        'trial', NOW())
		RETURNING id, email, handle, name, COALESCE(password_hash,'') as password_hash,
		          COALESCE(avatar_url, '') as avatar_url, total_km, role
	`, email, handle, name, avatarURL).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role,
	)
	if err != nil {
		return nil, fmt.Errorf("insert google user: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO user_identities (user_id, provider, provider_uid, email)
		VALUES ($1, 'google', $2, $3)
	`, u.ID, sub, email); err != nil {
		return nil, fmt.Errorf("insert identity: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

// LinkIdentity 為既有 email 帳號補一筆 Google 身分（帳號連結）
func (r *Repository) LinkIdentity(ctx context.Context, userID, sub, email string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_identities (user_id, provider, provider_uid, email)
		VALUES ($1, 'google', $2, $3)
		ON CONFLICT (provider, provider_uid) DO NOTHING
	`, userID, sub, email)
	if err != nil {
		return fmt.Errorf("link identity: %w", err)
	}
	return nil
}

func (r *Repository) EmailExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)`, email).Scan(&exists)
	return exists, err
}

func (r *Repository) HandleExists(ctx context.Context, handle string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE handle=$1)`, handle).Scan(&exists)
	return exists, err
}
