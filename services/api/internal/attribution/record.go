package attribution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// maxURLLen 與前端 lib/acquisition.ts 約定的 landing_url/referrer_url 截斷長度一致；後端在寫入
// 前再截一次（防禦性——避免略過前端的呼叫端把整串超長字串塞進 TEXT 欄位）。
const maxURLLen = 500

// Execer 是 *pgxpool.Pool 與 pgx.Tx 的共同子集，比照 internal/referral.Execer。
type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Record 於「新用戶建立成功」之後呼叫一次：判斷本次註冊來源並寫入 user_signup_attribution
// （ON CONFLICT (user_id) DO NOTHING，冪等）。
//
// refUserID 的推薦人身分不在本函式重新解析 ?ref=<code>——直接信任呼叫端已用既有
// internal/referral.BindReferrer 的結果（referrals 表 referred_user_id=userID 那筆的
// referrer_user_id，查無則代表本次註冊沒有推薦關係，refUserID 傳空字串）。
//
// 呼叫慣例（見 internal/auth/repository.go 呼叫點）：本函式的錯誤一律只 log、絕不可回傳擋掉
// 註冊流程；也因此刻意設計成「新用戶建立的交易 Commit 之後」才呼叫（用 pool 而非 tx），即使
// 本表尚未跑過 migration（relation 不存在）等罕見錯誤，也不會拖累已交易成功的帳號建立本身。
func Record(ctx context.Context, db Execer, userID, refUserID, landingURL, referrerURL string) error {
	result := Classify(refUserID, landingURL, referrerURL)

	var utmArg any
	if len(result.UTM) > 0 {
		b, err := json.Marshal(result.UTM)
		if err != nil {
			return fmt.Errorf("marshal utm: %w", err)
		}
		utmArg = b
	}

	var refUserIDArg any
	if result.Source == SourceReferral && result.RefUserID != "" {
		refUserIDArg = result.RefUserID
	}

	_, err := db.Exec(ctx, `
		INSERT INTO user_signup_attribution (user_id, source, ref_user_id, utm, landing_url, referrer_url)
		VALUES ($1, $2, $3, $4, NULLIF($5,''), NULLIF($6,''))
		ON CONFLICT (user_id) DO NOTHING`,
		userID, result.Source, refUserIDArg, utmArg, truncateURL(landingURL), truncateURL(referrerURL))
	if err != nil {
		return fmt.Errorf("insert attribution: %w", err)
	}
	return nil
}

// ResolveReferrer 讀取 referrals 表找出 userID 這筆註冊是否透過推薦連結而來，回傳推薦人
// user_id（查無則回空字串、nil error）。沿用 internal/referral.BindReferrer 剛寫入的資料——
// 呼叫端須確保先呼叫過 BindReferrer（不論成功綁定與否）才呼叫本函式。
func ResolveReferrer(ctx context.Context, db Execer, userID string) (string, error) {
	var referrerID string
	err := db.QueryRow(ctx, `SELECT referrer_user_id FROM referrals WHERE referred_user_id=$1`, userID).Scan(&referrerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve referrer: %w", err)
	}
	return referrerID, nil
}

// truncateURL 依 rune 截斷到 maxURLLen，避免多位元組字元被從中間切斷。
func truncateURL(s string) string {
	r := []rune(s)
	if len(r) <= maxURLLen {
		return s
	}
	return string(r[:maxURLLen])
}
