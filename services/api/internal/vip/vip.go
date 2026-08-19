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

// ExtendPeriod 依訂閱方案（monthly=+1月／annual=+1年）延長使用者 VIP 到期日一期，供綠界訂閱
// 綁卡扣款結算用（VIP 訂閱 Phase C2）。與 Extend 的差異：Extend 是「固定天數」疊加（活動獎勵等
// 場景），ExtendPeriod 是「日曆期別」疊加——一律用 Postgres interval 加法，自帶月底 clamp
// （例如 1/31 訂閱月繳，下一期會落在 2/28，不會產生 3/3 這種進位錯誤，make_interval(days=>30) 做
// 不到這件事，這是兩個函式不合併成一個的原因）。
//
// 同 Extend，從 max(現有到期, now) 起算（不縮短既有 VIP，即使是回頭補結算晚到的 webhook 也一樣安全）；
// vip_plan 直接覆寫成本次方案（付費訂閱以此為準，不像 Extend 的活動獎勵天數要保留原方案不覆蓋）；
// vip_since 只在原本是 NULL 時才補上（沿用既有值，不因續期而改動「首次成為 VIP」的時間點）。
func ExtendPeriod(ctx context.Context, db Execer, userID, plan string) error {
	_, err := db.Exec(ctx, `
		UPDATE users SET
		  vip_expires_at = GREATEST(COALESCE(vip_expires_at, now()), now())
		                   + CASE WHEN $2 = 'annual' THEN interval '1 year' ELSE interval '1 month' END,
		  vip_since      = COALESCE(vip_since, now()),
		  vip_plan       = $2
		WHERE id = $1`, userID, plan)
	return err
}
