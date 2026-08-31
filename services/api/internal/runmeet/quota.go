// ⚠️ 產品規則（使用者明訂）：「開啟後關閉，一樣消耗一次」。
// 本檔只有 consumeQuota()，沒有 refund()。close / cancel / delete 一律不得呼叫任何回補。
// 唯一的返還管道是後台人工 POST /admin/run-meets/quota/{userId}/adjust（走 Audit 留痕）。
// 若日後有人「好心」想在關閉流程加退還邏輯——那會讓「建了又刪」變成無限發起，請先回來讀這段。
package runmeet

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// taipeiLoc 台北時區。配額以自然月計、每月 1 日 00:00（台北）重置。
//
// ⚠️ 與既有 activity_coupon_month（race/repository.go 用 to_char(NOW(),'YYYY-MM') 走 DB session
// TZ=UTC，實際重置點是台北 08:00）**刻意不一致**。那是既有缺口，改它會變更持券者行為，另開單；
// 新功能不複製 bug，與 explore 的台北日界一致。
var taipeiLoc = time.FixedZone("Asia/Taipei", 8*60*60)

// QuotaMonth 給定時刻所屬的「台北月」字串（YYYY-MM）。純函式，可單元測試。
// SQL 端用 to_char(NOW() AT TIME ZONE 'Asia/Taipei','YYYY-MM') 產同一個值——兩邊必須一致，
// 否則會出現「DB 認為換月了、Go 認為還沒」的錯亂。這裡的 Go 版本只給顯示用（quota 端點、
// 錯誤訊息的「9 月 1 日重置」），扣點的權威判定一律在 SQL 那句 CAS 裡。
func QuotaMonth(t time.Time) string {
	return t.In(taipeiLoc).Format("2006-01")
}

// QuotaResetAt 下一次重置時刻（台北下月 1 日 00:00）。純函式。
func QuotaResetAt(t time.Time) time.Time {
	tp := t.In(taipeiLoc)
	y, m := tp.Year(), tp.Month()
	if m == time.December {
		return time.Date(y+1, time.January, 1, 0, 0, 0, 0, taipeiLoc)
	}
	return time.Date(y, m+1, 1, 0, 0, 0, 0, taipeiLoc)
}

// QuotaCap 這次建立當下的上限。
//
// ⚠️ 語意是「累加 used，上限每次即時算」，不是既有 activity_coupon_balance 的「補滿到 N」。
// 若照抄補滿制，月中升級 VIP 的人會因為「本月已補過」而拿不到 10 次，剛付錢就客訴。
// 累加制下上限是讀取時決定 → 升級當下立刻從 1 變 10（已用 1 次仍剩 9）。
//
// ⚠️ 與 image_limit（建立當下快照寫進 run_meets.image_limit）刻意相反：那個要快照，因為
// VIP 用 4 張建團後到期、只想改人數上限時，即時判定會 400 擋死正常操作。兩者理由不同，別統一。
func QuotaCap(isVIP bool, normalCap, vipCap int) int {
	if isVIP {
		return vipCap
	}
	return normalCap
}

// ImageLimit 建立當下的每團圖片張數（寫進 run_meets.image_limit 快照）。
func ImageLimit(isVIP bool, normalLimit, vipLimit int) int {
	if isVIP {
		return vipLimit
	}
	return normalLimit
}

// ErrQuotaExhausted 本月發起次數已用完（→ 409）。
var ErrQuotaExhausted = errors.New("quota exhausted")

// consumeQuota 單句 CAS：跨月重置 ＋ 額度檢查 ＋ 序列化三合一。
//
//	回 0 列（pgx.ErrNoRows）＝額度用盡 → 呼叫端轉 409。
//	單筆 UPDATE 走 row-level lock，兩個並行請求會排隊，第二個讀到已更新值 → 不需 advisory lock
//	（對比 explore 的 pg_advisory_xact_lock：那邊是 COUNT(*) 型判定才需要額外鎖）。
//
// 必須在 tx 內、且在 INSERT run_meets 之前；任一步失敗整包 rollback，不會白扣。
// 連點／網路重試另用 client_token 部分唯一索引擋（見 repository.CreateMeet）。
func consumeQuota(ctx context.Context, tx pgx.Tx, userID, month string, cap int) (used int, err error) {
	err = tx.QueryRow(ctx, `
		UPDATE users
		   SET run_meet_month = $2,
		       run_meet_used  = CASE WHEN COALESCE(run_meet_month,'') = $2
		                             THEN run_meet_used + 1 ELSE 1 END
		 WHERE id = $1
		   AND (COALESCE(run_meet_month,'') <> $2 OR run_meet_used < $3)
		RETURNING run_meet_used`, userID, month, cap).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrQuotaExhausted
	}
	return used, err
}
