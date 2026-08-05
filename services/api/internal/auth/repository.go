package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/referral"
)

type User struct {
	ID           string  `db:"id"`
	Email        string  `db:"email"`
	Handle       string  `db:"handle"`
	Name         string  `db:"name"`
	PasswordHash string  `db:"password_hash"`
	AvatarURL    string  `db:"avatar_url"`
	TotalKm      float64 `db:"total_km"`
	Role         string  `db:"role"`          // user | organizer | admin
	SessionEpoch int     `db:"session_epoch"` // 單一登入：目前有效的 session epoch（見 BumpSessionEpoch）
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, email, handle, name, hash, role, refCode string) (*User, error) {
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
	// 綁定推薦關係（見 internal/referral.BindReferrer）：本函式本身無交易，直接用 pool 呼叫亦安全——
	// BindReferrer 自帶 ON CONFLICT(referred_user_id) DO NOTHING 冪等；refCode 空/查無/自我推薦皆靜默忽略，不擋註冊。
	if err := referral.BindReferrer(ctx, r.db, u.ID, refCode); err != nil {
		return nil, fmt.Errorf("bind referrer: %w", err)
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
		       COALESCE(avatar_url, '') as avatar_url, total_km, role, session_epoch
		FROM users WHERE id = $1
	`, id).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role, &u.SessionEpoch,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return u, nil
}

// BumpSessionEpoch 遞增使用者的 session_epoch（單一登入強制用）：每次「登入」
// （Register/Login/LoginWithGoogle）成功都呼叫，回傳遞增後的新 epoch，寫進新簽發的
// token（sev claim）；帳號目前 epoch 與舊 token 的 sev 不符 → 舊 token（含 refresh）全數失效。
func (r *Repository) BumpSessionEpoch(ctx context.Context, userID string) (int, error) {
	var epoch int
	err := r.db.QueryRow(ctx, `
		UPDATE users SET session_epoch = session_epoch + 1 WHERE id = $1
		RETURNING session_epoch
	`, userID).Scan(&epoch)
	if err != nil {
		return 0, fmt.Errorf("bump session epoch: %w", err)
	}
	return epoch, nil
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
func (r *Repository) CreateGoogleUser(ctx context.Context, email, handle, name, avatarURL, sub, refCode string) (*User, error) {
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

	// 綁定推薦關係（見 internal/referral.BindReferrer），與建帳號同一交易保持原子；
	// refCode 空/查無/自我推薦皆靜默忽略，不擋註冊。
	if err := referral.BindReferrer(ctx, tx, u.ID, refCode); err != nil {
		return nil, fmt.Errorf("bind referrer: %w", err)
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
