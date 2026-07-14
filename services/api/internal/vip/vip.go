// Package vip 提供最小、無依賴環的 VIP 天數疊加 helper，供各模組（race 結算、活動任務…）呼叫。
// 刻意獨立成 leaf 套件（不依賴 profile/race），避免 import 循環。
package vip

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer 是 *pgxpool.Pool 與 pgx.Tx 的共同子集，讓呼叫端可傳入交易以確保原子性。
type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Extend 延長使用者 VIP 到期日 days 天：從 max(現有到期, now) 起算（不縮短既有 VIP）。
// days<=0 為 no-op。付費會員的 vip_plan 不覆蓋；只有從未當過 VIP（空字串）才標記為 'bonus'。
func Extend(ctx context.Context, db Execer, userID string, days int) error {
	if days <= 0 {
		return nil
	}
	_, err := db.Exec(ctx, `
		UPDATE users SET
		  vip_expires_at = GREATEST(COALESCE(vip_expires_at, now()), now()) + make_interval(days => $2),
		  vip_since      = COALESCE(vip_since, now()),
		  vip_plan       = CASE WHEN COALESCE(vip_plan,'')='' THEN 'bonus' ELSE vip_plan END
		WHERE id = $1`, userID, days)
	return err
}
