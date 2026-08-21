package activityreward

// repository.go 全域即時獎勵模板（reward_templates）CRUD + 活動優惠券券種（event_coupon_defs，
// migration 138）CRUD 資料存取。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrCouponDefInUse 券種已被發放過（user_rewards.coupon_def_id 有引用），刪除會違反外鍵——
	// 比照 rewardserial.ErrMerchantInUse 的處理方式，回友善錯誤要求改停用而非刪除。
	ErrCouponDefInUse = errors.New("此券種已發放過，無法刪除，請改為停用")
)

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// itemsToBytes 將 []RewardItem 序列化為 JSON bytes 供寫入 JSONB；nil 一律存成 '[]'（非 NULL），
// 讀取端因此不必處理 NULL 分支（比照 reward_templates.items 欄位 NOT NULL DEFAULT '[]' 的設計）。
func itemsToBytes(items []RewardItem) ([]byte, error) {
	if items == nil {
		items = []RewardItem{}
	}
	return json.Marshal(items)
}

func bytesToItems(b []byte) ([]RewardItem, error) {
	items := []RewardItem{}
	if len(b) == 0 {
		return items, nil
	}
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := r.db.Query(ctx, `SELECT id, name, items, created_at FROM reward_templates ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		var t Template
		var itemsBytes []byte
		if err := rows.Scan(&t.ID, &t.Name, &itemsBytes, &t.CreatedAt); err != nil {
			return nil, err
		}
		if t.Items, err = bytesToItems(itemsBytes); err != nil {
			return nil, fmt.Errorf("parse template items: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repository) CreateTemplate(ctx context.Context, name string, items []RewardItem) (*Template, error) {
	itemsBytes, err := itemsToBytes(items)
	if err != nil {
		return nil, fmt.Errorf("marshal items: %w", err)
	}
	t := &Template{Name: name, Items: items}
	err = r.db.QueryRow(ctx,
		`INSERT INTO reward_templates (name, items) VALUES ($1,$2) RETURNING id, created_at`,
		name, itemsBytes).Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}
	return t, nil
}

func (r *Repository) UpdateTemplate(ctx context.Context, id, name string, items []RewardItem) (*Template, error) {
	itemsBytes, err := itemsToBytes(items)
	if err != nil {
		return nil, fmt.Errorf("marshal items: %w", err)
	}
	t := &Template{ID: id, Name: name, Items: items}
	err = r.db.QueryRow(ctx,
		`UPDATE reward_templates SET name=$1, items=$2 WHERE id=$3 RETURNING created_at`,
		name, itemsBytes, id).Scan(&t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *Repository) DeleteTemplate(ctx context.Context, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM reward_templates WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- 活動優惠券券種（event_coupon_defs，migration 138）---

const couponDefCols = `id, name, amount_cents, expiry_mode, expires_at, valid_days, enabled, created_at, updated_at`

func scanCouponDef(row pgx.Row) (*CouponDef, error) {
	d := &CouponDef{}
	if err := row.Scan(&d.ID, &d.Name, &d.AmountCents, &d.ExpiryMode, &d.ExpiresAt, &d.ValidDays, &d.Enabled, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	return d, nil
}

// ListCouponDefs 列出所有券種＋列表統計（已發放張數/已使用張數，COUNT user_rewards）。
func (r *Repository) ListCouponDefs(ctx context.Context) ([]CouponDef, error) {
	rows, err := r.db.Query(ctx, `SELECT `+couponDefCols+` FROM event_coupon_defs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	defs := []CouponDef{}
	for rows.Next() {
		d, err := scanCouponDef(rows)
		if err != nil {
			return nil, err
		}
		defs = append(defs, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.hydrateCouponDefStats(ctx, defs); err != nil {
		return nil, err
	}
	return defs, nil
}

// hydrateCouponDefStats 補上已發放/已使用張數統計（單一 GROUP BY 查詢，避免每筆券種各查一次）。
func (r *Repository) hydrateCouponDefStats(ctx context.Context, defs []CouponDef) error {
	if len(defs) == 0 {
		return nil
	}
	ids := make([]string, len(defs))
	for i := range defs {
		ids[i] = defs[i].ID
	}
	rows, err := r.db.Query(ctx, `
		SELECT coupon_def_id::text, COUNT(*), COUNT(*) FILTER (WHERE used)
		FROM user_rewards WHERE coupon_def_id = ANY($1::uuid[]) GROUP BY coupon_def_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	stats := map[string][2]int{}
	for rows.Next() {
		var id string
		var issued, used int
		if err := rows.Scan(&id, &issued, &used); err != nil {
			return err
		}
		stats[id] = [2]int{issued, used}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range defs {
		s := stats[defs[i].ID]
		defs[i].IssuedCount, defs[i].UsedCount = s[0], s[1]
	}
	return nil
}

func (r *Repository) GetCouponDef(ctx context.Context, id string) (*CouponDef, error) {
	d, err := scanCouponDef(r.db.QueryRow(ctx, `SELECT `+couponDefCols+` FROM event_coupon_defs WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defs := []CouponDef{*d}
	if err := r.hydrateCouponDefStats(ctx, defs); err != nil {
		return nil, err
	}
	return &defs[0], nil
}

func (r *Repository) CreateCouponDef(ctx context.Context, in CouponDefInput, expiresAt *time.Time) (*CouponDef, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO event_coupon_defs (name, amount_cents, expiry_mode, expires_at, valid_days, enabled)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		in.Name, in.AmountCents, in.ExpiryMode, expiresAt, in.ValidDays, in.Enabled,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create coupon def: %w", err)
	}
	return r.GetCouponDef(ctx, id)
}

func (r *Repository) UpdateCouponDef(ctx context.Context, id string, in CouponDefInput, expiresAt *time.Time) (*CouponDef, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE event_coupon_defs SET
			name=$1, amount_cents=$2, expiry_mode=$3, expires_at=$4, valid_days=$5, enabled=$6, updated_at=NOW()
		WHERE id=$7`,
		in.Name, in.AmountCents, in.ExpiryMode, expiresAt, in.ValidDays, in.Enabled, id)
	if err != nil {
		return nil, fmt.Errorf("update coupon def: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return r.GetCouponDef(ctx, id)
}

// DeleteCouponDef 刪除券種；已發放過（user_rewards.coupon_def_id 有引用，FK RESTRICT）則拒絕，
// 要求管理者改用「停用」——已發出去的券不該因券種被刪而變成斷鏈資料（玩家錢包/報名折抵都還在讀它）。
func (r *Repository) DeleteCouponDef(ctx context.Context, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM event_coupon_defs WHERE id=$1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrCouponDefInUse
		}
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
