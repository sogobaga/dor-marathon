// Package promo 優惠序號：後台 CRUD + 使用紀錄，以及供 race 報名交易使用的 tx 輔助函式。
package promo

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- 錯誤 ---

var (
	ErrNotFound      = errors.New("promo code not found")
	ErrInactive      = errors.New("優惠序號已停用")
	ErrNotStarted    = errors.New("優惠序號尚未開始")
	ErrExpired       = errors.New("優惠序號已過期")
	ErrWrongRace     = errors.New("優惠序號不適用此賽事")
	ErrWrongUser     = errors.New("優惠序號不適用此帳號")
	ErrUsedUp        = errors.New("優惠序號已達使用上限")
	ErrUserUsed      = errors.New("您已使用過此優惠序號")
	ErrInvalidConfig = errors.New("invalid promo config")
)

// --- 模型 ---

type PromoCode struct {
	ID            string     `json:"id"`
	Code          string     `json:"code"`
	DiscountType  string     `json:"discount_type"` // amount | percent
	DiscountValue int        `json:"discount_value"`
	MaxUses       *int       `json:"max_uses,omitempty"`
	UsedCount     int        `json:"used_count"`
	PerUserOnce   bool       `json:"per_user_once"`
	RaceID        *string    `json:"race_id,omitempty"`
	TargetUserID  *string    `json:"target_user_id,omitempty"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
	BatchID       *string    `json:"batch_id,omitempty"`
	Note          string     `json:"note,omitempty"`
	Active        bool       `json:"active"`
	CreatedAt     time.Time  `json:"created_at"`
}

type Usage struct {
	ID            string    `json:"id"`
	UserName      string    `json:"user_name"`
	UserEmail     string    `json:"user_email"`
	RaceTitle     string    `json:"race_title"`
	DiscountCents int       `json:"discount_cents"`
	UsedAt        time.Time `json:"used_at"`
}

// DiscountCents 計算某序號對某報名費的折抵金額（分）。只折報名費。
func DiscountCents(p *PromoCode, entryFeeCents int) int {
	if entryFeeCents <= 0 {
		return 0
	}
	var d int
	switch p.DiscountType {
	case "amount":
		d = p.DiscountValue
	case "percent":
		d = (entryFeeCents*p.DiscountValue + 50) / 100 // 四捨五入
	}
	if d > entryFeeCents {
		d = entryFeeCents
	}
	if d < 0 {
		d = 0
	}
	return d
}

// validate 共用驗證（不含 used_count/per_user 的即時鎖定檢查，那些在 tx 內做）。
func validate(p *PromoCode, raceID, userID string, now time.Time, userAlreadyUsed bool) error {
	if !p.Active {
		return ErrInactive
	}
	if p.ValidFrom != nil && now.Before(*p.ValidFrom) {
		return ErrNotStarted
	}
	if p.ValidUntil != nil && now.After(*p.ValidUntil) {
		return ErrExpired
	}
	if p.RaceID != nil && *p.RaceID != raceID {
		return ErrWrongRace
	}
	if p.TargetUserID != nil && *p.TargetUserID != userID {
		return ErrWrongUser
	}
	if p.MaxUses != nil && p.UsedCount >= *p.MaxUses {
		return ErrUsedUp
	}
	if p.PerUserOnce && userAlreadyUsed {
		return ErrUserUsed
	}
	return nil
}

// --- Repository ---

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

const promoCols = `id, code, discount_type, discount_value, max_uses, used_count, per_user_once,
	COALESCE(race_id::text,''), COALESCE(target_user_id::text,''), valid_from, valid_until,
	COALESCE(batch_id::text,''), COALESCE(note,''), active, created_at`

func scanPromo(row pgx.Row) (*PromoCode, error) {
	p := &PromoCode{}
	var raceID, targetUser, batchID string
	err := row.Scan(&p.ID, &p.Code, &p.DiscountType, &p.DiscountValue, &p.MaxUses, &p.UsedCount,
		&p.PerUserOnce, &raceID, &targetUser, &p.ValidFrom, &p.ValidUntil, &batchID, &p.Note,
		&p.Active, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	if raceID != "" {
		p.RaceID = &raceID
	}
	if targetUser != "" {
		p.TargetUserID = &targetUser
	}
	if batchID != "" {
		p.BatchID = &batchID
	}
	return p, nil
}

func (r *Repository) GetByCode(ctx context.Context, code string) (*PromoCode, error) {
	p, err := scanPromo(r.db.QueryRow(ctx, `SELECT `+promoCols+` FROM promo_codes WHERE code=$1`, strings.ToUpper(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (r *Repository) HasUserUsed(ctx context.Context, codeID, userID string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM promo_code_usages WHERE promo_code_id=$1 AND user_id=$2)`,
		codeID, userID).Scan(&exists)
	return exists, err
}

func (r *Repository) List(ctx context.Context, raceID, q string) ([]PromoCode, error) {
	like := "%" + strings.ToUpper(q) + "%"
	rows, err := r.db.Query(ctx, `SELECT `+promoCols+` FROM promo_codes
		WHERE ($1='' OR race_id=$1::uuid) AND ($2='' OR code ILIKE $3)
		ORDER BY created_at DESC LIMIT 500`, raceID, q, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PromoCode{}
	for rows.Next() {
		p, err := scanPromo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *Repository) ListUsages(ctx context.Context, codeID string) ([]Usage, error) {
	rows, err := r.db.Query(ctx, `
		SELECT pu.id, u.name, u.email, COALESCE(rc.title,''), pu.discount_cents, pu.used_at
		FROM promo_code_usages pu
		JOIN users u ON u.id = pu.user_id
		LEFT JOIN races rc ON rc.id = pu.race_id
		WHERE pu.promo_code_id=$1 ORDER BY pu.used_at DESC`, codeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Usage{}
	for rows.Next() {
		var u Usage
		if err := rows.Scan(&u.ID, &u.UserName, &u.UserEmail, &u.RaceTitle, &u.DiscountCents, &u.UsedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// insertOne 插入單筆（code 為空則自動產生，衝突重試）
func (r *Repository) insertOne(ctx context.Context, p *PromoCode, batchID interface{}) (*PromoCode, error) {
	for attempt := 0; attempt < 8; attempt++ {
		code := p.Code
		if code == "" {
			code = genCode(8)
		}
		var id string
		err := r.db.QueryRow(ctx, `
			INSERT INTO promo_codes (code, discount_type, discount_value, max_uses, per_user_once,
				race_id, target_user_id, valid_from, valid_until, batch_id, note)
			VALUES (UPPER($1),$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''))
			RETURNING id`,
			code, p.DiscountType, p.DiscountValue, p.MaxUses, p.PerUserOnce,
			p.RaceID, p.TargetUserID, p.ValidFrom, p.ValidUntil, batchID, p.Note,
		).Scan(&id)
		if err == nil {
			p.ID = id
			p.Code = strings.ToUpper(code)
			return p, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && p.Code == "" {
			continue // 自動碼撞號，重試
		}
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, fmt.Errorf("序號已存在")
		}
		return nil, fmt.Errorf("insert promo: %w", err)
	}
	return nil, fmt.Errorf("產生序號失敗，請重試")
}

func (r *Repository) Create(ctx context.Context, p *PromoCode) (*PromoCode, error) {
	return r.insertOne(ctx, p, nil)
}

func (r *Repository) CreateBatch(ctx context.Context, n int, tmpl *PromoCode) ([]PromoCode, error) {
	var batchID string
	if err := r.db.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&batchID); err != nil {
		return nil, err
	}
	out := make([]PromoCode, 0, n)
	for i := 0; i < n; i++ {
		cp := *tmpl
		cp.Code = "" // 強制自動產生
		created, err := r.insertOne(ctx, &cp, batchID)
		if err != nil {
			return nil, err
		}
		out = append(out, *created)
	}
	return out, nil
}

func (r *Repository) SetActive(ctx context.Context, id string, active bool) error {
	ct, err := r.db.Exec(ctx, `UPDATE promo_codes SET active=$1 WHERE id=$2`, active, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FindUserIDByEmail 解析指定帳號（email → user_id）
func (r *Repository) FindUserIDByEmail(ctx context.Context, email string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// --- tx 輔助（供 race 報名交易使用）---

// LockAndValidateTx 在交易內鎖定序號並驗證（含即時 used_count / per_user_once）。
func LockAndValidateTx(ctx context.Context, tx pgx.Tx, code, raceID, userID string, now time.Time) (*PromoCode, error) {
	p, err := scanPromo(tx.QueryRow(ctx, `SELECT `+promoCols+` FROM promo_codes WHERE code=UPPER($1) FOR UPDATE`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	userUsed := false
	if p.PerUserOnce {
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM promo_code_usages WHERE promo_code_id=$1 AND user_id=$2)`,
			p.ID, userID).Scan(&userUsed); err != nil {
			return nil, err
		}
	}
	if err := validate(p, raceID, userID, now, userUsed); err != nil {
		return nil, err
	}
	return p, nil
}

// RecordUsageTx 交易內記錄使用：used_count+1 + 一筆使用紀錄。
func RecordUsageTx(ctx context.Context, tx pgx.Tx, promoID, userID, raceID, regID, orderID string, discountCents int) error {
	if _, err := tx.Exec(ctx, `UPDATE promo_codes SET used_count=used_count+1 WHERE id=$1`, promoID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO promo_code_usages (promo_code_id, user_id, race_id, registration_id, order_id, discount_cents)
		VALUES ($1,$2,$3,$4,$5,$6)`, promoID, userID, raceID, regID, orderID, discountCents)
	return err
}

// --- Service ---

type Service struct{ repo *Repository }

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

func (s *Service) List(ctx context.Context, raceID, q string) ([]PromoCode, error) {
	return s.repo.List(ctx, raceID, q)
}
func (s *Service) ListUsages(ctx context.Context, codeID string) ([]Usage, error) {
	return s.repo.ListUsages(ctx, codeID)
}
func (s *Service) SetActive(ctx context.Context, id string, active bool) error {
	return s.repo.SetActive(ctx, id, active)
}
func (s *Service) GetByCode(ctx context.Context, code string) (*PromoCode, error) {
	return s.repo.GetByCode(ctx, code)
}
func (s *Service) HasUserUsed(ctx context.Context, codeID, userID string) (bool, error) {
	return s.repo.HasUserUsed(ctx, codeID, userID)
}

// QuotePromoForRace 給 race 套件做報名前折抵預覽用（讀取 + 驗證 + 折抵）。
func (s *Service) ValidateForRace(ctx context.Context, code, raceID, userID string) (*PromoCode, error) {
	p, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}
	userUsed := false
	if p.PerUserOnce {
		if userUsed, err = s.repo.HasUserUsed(ctx, p.ID, userID); err != nil {
			return nil, err
		}
	}
	if err := validate(p, raceID, userID, time.Now(), userUsed); err != nil {
		return nil, err
	}
	return p, nil
}

type CreateInput struct {
	Code          string  `json:"code"`
	DiscountType  string  `json:"discount_type"`
	DiscountValue int     `json:"discount_value"`
	MaxUses       *int    `json:"max_uses"`
	PerUserOnce   bool    `json:"per_user_once"`
	RaceID        *string `json:"race_id"`
	TargetEmail   string  `json:"target_email"`
	ValidFrom     *string `json:"valid_from"`
	ValidUntil    *string `json:"valid_until"`
	Note          string  `json:"note"`
	Quantity      int     `json:"quantity"`
}

func (s *Service) Create(ctx context.Context, in CreateInput) ([]PromoCode, error) {
	if in.DiscountType != "amount" && in.DiscountType != "percent" {
		return nil, fmt.Errorf("%w: discount_type", ErrInvalidConfig)
	}
	if in.DiscountValue <= 0 {
		return nil, fmt.Errorf("%w: discount_value", ErrInvalidConfig)
	}
	if in.DiscountType == "percent" && in.DiscountValue > 100 {
		return nil, fmt.Errorf("%w: percent > 100", ErrInvalidConfig)
	}
	qty := in.Quantity
	if qty <= 0 {
		qty = 1
	}
	if qty > 500 {
		return nil, fmt.Errorf("%w: 一次最多 500 筆", ErrInvalidConfig)
	}

	tmpl := &PromoCode{
		Code:          strings.ToUpper(strings.TrimSpace(in.Code)),
		DiscountType:  in.DiscountType,
		DiscountValue: in.DiscountValue,
		MaxUses:       in.MaxUses,
		PerUserOnce:   in.PerUserOnce,
		RaceID:        in.RaceID,
		Note:          in.Note,
	}
	if in.TargetEmail != "" {
		uid, err := s.repo.FindUserIDByEmail(ctx, in.TargetEmail)
		if err != nil {
			return nil, err
		}
		if uid == "" {
			return nil, fmt.Errorf("%w: 指定帳號 email 不存在", ErrInvalidConfig)
		}
		tmpl.TargetUserID = &uid
	}
	if t := parseTime(in.ValidFrom); t != nil {
		tmpl.ValidFrom = t
	}
	if t := parseTime(in.ValidUntil); t != nil {
		tmpl.ValidUntil = t
	}

	if qty == 1 {
		c, err := s.repo.Create(ctx, tmpl)
		if err != nil {
			return nil, err
		}
		return []PromoCode{*c}, nil
	}
	return s.repo.CreateBatch(ctx, qty, tmpl)
}

// --- Handler ---

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Patch("/{id}", h.Patch)
	r.Get("/{id}/usages", h.Usages)
	return r
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	codes, err := h.svc.List(r.Context(), r.URL.Query().Get("race_id"), r.URL.Query().Get("q"))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list promo codes")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"codes": codes, "count": len(codes)})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	codes, err := h.svc.Create(r.Context(), in)
	if errors.Is(err, ErrInvalidConfig) {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"codes": codes, "count": len(codes)})
}

func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active *bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Active == nil {
		respondErr(w, http.StatusBadRequest, "active is required")
		return
	}
	if err := h.svc.SetActive(r.Context(), chi.URLParam(r, "id"), *req.Active); err != nil {
		if errors.Is(err, ErrNotFound) {
			respondErr(w, http.StatusNotFound, "not found")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to update")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Usages(w http.ResponseWriter, r *http.Request) {
	usages, err := h.svc.ListUsages(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list usages")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"usages": usages, "count": len(usages)})
}

// --- helpers ---

const codeChars = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // 去易混字 0O1IL

func genCode(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	out := make([]byte, n)
	for i := range b {
		out[i] = codeChars[int(b[i])%len(codeChars)]
	}
	return string(out)
}

func parseTime(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, *s); err == nil {
		return &t
	}
	return nil
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
