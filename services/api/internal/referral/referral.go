// Package referral 推薦/推廣連結系統：total_km >= 10 的帳號可產生專屬推薦碼；透過 ?ref=<code>
// 連結註冊的新帳號，一旦自己也跨過 10km 門檻 → 推薦人與新朋友互相 +VIP 天數（雙向、一次性、無上限）。
// 刻意獨立成 leaf 套件（比照 internal/vip，不依賴 profile/auth），避免 import 循環；內部呼叫
// vip.Extend 加天，供 auth（綁定推薦關係）與 mileage_exp（達標發獎）呼叫。
package referral

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dor/api/internal/vip"
)

// Execer 是 *pgxpool.Pool 與 pgx.Tx 的共同子集，讓呼叫端可傳入交易以確保原子性
// （比照 vip.Execer，另加 QueryRow 供本套件內部查詢/CAS 用）。
type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

const codeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ" // 去易混淆字（無 0/1/I/O），比照 profile.genAccountCode

func genCode(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	out := make([]byte, n)
	for i := range b {
		out[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(out)
}

// GetOrCreateCode lazy 產生使用者的專屬推薦碼；已存在就回既有值。**不做** 10km 門檻檢查——
// 呼叫端（profile handler）需自行先確認 total_km >= 10 才呼叫本函式，避免未達標的人也佔用一組碼。
func GetOrCreateCode(ctx context.Context, db Execer, userID string) (string, error) {
	var code *string
	if err := db.QueryRow(ctx, `SELECT referral_code FROM users WHERE id=$1`, userID).Scan(&code); err != nil {
		return "", err
	}
	if code != nil && *code != "" {
		return *code, nil
	}
	for i := 0; i < 8; i++ {
		c := genCode(8)
		ct, err := db.Exec(ctx, `UPDATE users SET referral_code=$1 WHERE id=$2 AND referral_code IS NULL`, c, userID)
		if err == nil && ct.RowsAffected() == 1 {
			return c, nil
		}
		if err == nil {
			// 衝突（UNIQUE 撞碼）或併發：重讀一次，可能是別的 request 剛好搶先生成了。
			if err2 := db.QueryRow(ctx, `SELECT referral_code FROM users WHERE id=$1`, userID).Scan(&code); err2 == nil && code != nil && *code != "" {
				return *code, nil
			}
		}
	}
	return "", errors.New("referral: failed to generate unique code")
}

// BindReferrer 於新帳號註冊成功後呼叫，把 ?ref=<code> 帶入的推薦關係寫進 referrals（只綁定，不發獎；
// 發獎見 Reward，於被推薦人日後跨過 10km 門檻時才觸發）。refCode 空／查無此碼／自我推薦一律靜默
// 忽略（return nil），不擋註冊流程。ON CONFLICT (referred_user_id) DO NOTHING 確保冪等
// （同一使用者只會被綁定一次，重試/併發安全）。
func BindReferrer(ctx context.Context, tx Execer, newUserID, refCode string) error {
	refCode = strings.TrimSpace(refCode)
	if refCode == "" {
		return nil
	}
	var referrerID string
	err := tx.QueryRow(ctx, `SELECT id FROM users WHERE referral_code=$1`, refCode).Scan(&referrerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // 無效碼，忽略
	}
	if err != nil {
		return err
	}
	if referrerID == newUserID {
		return nil // 擋自我推薦
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO referrals (referred_user_id, referrer_user_id, code_used)
		VALUES ($1, $2, $3)
		ON CONFLICT (referred_user_id) DO NOTHING`, newUserID, referrerID, refCode)
	return err
}

// Reward 於「使用者累積里程更新」的同一交易內呼叫（同步 Strava/Terra 路徑見
// internal/integration/mileage_exp.go；非同步 GPS 路徑見 services/worker/main.go 的內聯複製版——
// worker 是獨立 Go module 不能 import 本套件，改動判斷邏輯需同步修改兩邊）。
//
// 只在 newTotalKm >= 10 時才可能發獎：CAS 鎖定 referrals 該筆（rewarded_at IS NULL → 設值並
// RETURNING referrer_user_id），拿得到 row 才代表「這是本筆推薦關係第一次達標」，據此對推薦人／
// 被推薦人各自呼叫 vip.Extend 疊加天數。非推薦來的人（referrals 無此筆）或已發過獎（rewarded_at
// 已非 NULL）→ CAS 拿不到 row，直接結束（no-op），確保一次性、冪等、可重複呼叫。
func Reward(ctx context.Context, tx Execer, referredUserID string, newTotalKm float64) error {
	if newTotalKm < 10 {
		return nil
	}
	var referrerID string
	err := tx.QueryRow(ctx, `
		UPDATE referrals SET rewarded_at = now()
		WHERE referred_user_id = $1 AND rewarded_at IS NULL
		RETURNING referrer_user_id`, referredUserID).Scan(&referrerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // 非推薦來的人，或這筆推薦關係已經發過獎
	}
	if err != nil {
		return err
	}
	referrerDays := getSettingInt(ctx, tx, "referral_reward_referrer_days", 1)
	referredDays := getSettingInt(ctx, tx, "referral_reward_referred_days", 3)
	if err := vip.Extend(ctx, tx, referrerID, referrerDays); err != nil {
		return err
	}
	return vip.Extend(ctx, tx, referredUserID, referredDays)
}

// getSettingInt 讀 app_settings 整數設定，邏輯比照 internal/appsettings.GetInt，但吃通用 Execer
// 而非 *pgxpool.Pool——本套件的呼叫端（Reward）多半帶著交易 tx 進來，appsettings.GetInt 綁定 pool
// 型別無法接受 tx，故這裡獨立一份最小實作，避免型別不符（不是 import 循環——只是型別限制）。
func getSettingInt(ctx context.Context, db Execer, key string, def int) int {
	var v string
	if err := db.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, key).Scan(&v); err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}
