package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/attribution"
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

func (r *Repository) Create(ctx context.Context, email, handle, name, hash, role, refCode string, acq Acq) (*User, error) {
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
	recordSignupAttribution(ctx, r.db, u.ID, acq)
	return u, nil
}

// recordSignupAttribution 是 Create/CreateGoogleUser 共用的「新用戶建立後」歸因記錄步驟：讀
// referrals 表（BindReferrer 剛寫入的資料）拿推薦人 user id、丟給 internal/attribution.Classify
// 分類、寫入 user_signup_attribution。契約明定「失敗只 log 絕不影響註冊流程」——本函式因此不回傳
// error，呼叫端無需（也不該）處理失敗分支。db 一律傳 pool（非 tx，見 CreateGoogleUser 呼叫點的
// 說明：交易內若歸因 INSERT 失敗，COMMIT 在 aborted transaction 下等同 ROLLBACK，會把已成功建立
// 的帳號一併回滾，違背「不影響註冊流程」的契約）。
func recordSignupAttribution(ctx context.Context, db attribution.Execer, userID string, acq Acq) {
	refUserID, err := attribution.ResolveReferrer(ctx, db, userID)
	if err != nil {
		log.Printf("auth.recordSignupAttribution: resolve referrer failed user=%s err=%v", userID, err)
		return
	}
	if err := attribution.Record(ctx, db, userID, refUserID, acq.LandingURL, acq.ReferrerURL); err != nil {
		log.Printf("auth.recordSignupAttribution: record failed user=%s err=%v", userID, err)
	}
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
func (r *Repository) CreateGoogleUser(ctx context.Context, email, handle, name, avatarURL, sub, refCode string, acq Acq) (*User, error) {
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
	// 來源歸因刻意在交易 Commit「之後」才記錄（見 recordSignupAttribution 的說明）：本函式的
	// users/user_identities/referrals 建立必須保持原子（上面 tx 已保證），但歸因記錄失敗不可
	// 波及已成功建立的帳號——若沿用同一筆 tx，一旦 INSERT user_signup_attribution 失敗，
	// Postgres 會把整個交易視為 aborted，COMMIT 形同 ROLLBACK，反而把剛建好的帳號一起吃掉。
	recordSignupAttribution(ctx, r.db, u.ID, acq)
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

// LoginLog 一筆登入紀錄（後台查詢用，JOIN users 帶 email）。
type LoginLog struct {
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UserID    string    `db:"user_id"    json:"user_id"`
	Email     string    `db:"email"      json:"email"`
	Method    string    `db:"method"     json:"method"` // password | google | register
	IP        string    `db:"ip"         json:"ip"`
}

// InsertLoginLog 寫入一筆登入紀錄並同步更新 users.last_login_at（單一 CTE 陳述式、單次往返）。
// 呼叫端（handler）以獨立 context + goroutine fire-and-forget 呼叫（見 Handler.recordLogin），
// 失敗不影響登入本身，故這裡不需要交易；method 不含 "refresh"（高頻自動行為，刻意不記）。
func (r *Repository) InsertLoginLog(ctx context.Context, userID, method, ip string) error {
	_, err := r.db.Exec(ctx, `
		WITH ins AS (
			INSERT INTO user_login_logs (user_id, method, ip) VALUES ($1, $2, NULLIF($3, ''))
		)
		UPDATE users SET last_login_at = NOW() WHERE id = $1
	`, userID, method, ip)
	if err != nil {
		return fmt.Errorf("insert login log: %w", err)
	}
	return nil
}

// ListLoginLogs 後台查詢登入紀錄流水（GET /admin/login-logs），依 created_at DESC 分頁；
// q 為選填 email 模糊篩選（比照 AdminListMembers 的 ILIKE 寫法）。
func (r *Repository) ListLoginLogs(ctx context.Context, q string, limit, offset int) ([]LoginLog, int, error) {
	like := "%" + q + "%"

	var total int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM user_login_logs l JOIN users u ON u.id = l.user_id
		WHERE ($1 = '' OR u.email ILIKE $2)
	`, q, like).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count login logs: %w", err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT l.created_at, l.user_id, u.email, l.method, COALESCE(l.ip, '')
		FROM user_login_logs l JOIN users u ON u.id = l.user_id
		WHERE ($1 = '' OR u.email ILIKE $2)
		ORDER BY l.created_at DESC
		LIMIT $3 OFFSET $4
	`, q, like, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list login logs: %w", err)
	}
	defer rows.Close()

	logs := []LoginLog{}
	for rows.Next() {
		var l LoginLog
		if err := rows.Scan(&l.CreatedAt, &l.UserID, &l.Email, &l.Method, &l.IP); err != nil {
			return nil, 0, fmt.Errorf("scan login log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, total, nil
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
