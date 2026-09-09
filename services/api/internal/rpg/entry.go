// entry.go：入口可見性判定 + 身分/基本等級查詢。獨立實作於本套件內（比照 internal/gpscalib
// 前例），不 import profile 的未匯出 resolveEntry/computeLevel——避免 profile↔rpg 循環依賴
// （profile.Dashboard 需要顯示這裡的 rpg_entry 欄位，見 membership.go）。
package rpg

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
)

// EntryStateKey/EntryWhitelistKey app_settings key（migration 175 種下 is_vvip 欄位，設定值後台
// 「遊戲化」頁「入口與 VVIP」分頁可改）。
const (
	EntryStateKey     = "rpg_entry_state"
	EntryWhitelistKey = "rpg_entry_whitelist"
)

// ResolveEntry 依 rpg_entry_state/whitelist + 使用者 is_vvip 解析入口可見性（D3，owner 明確要求
// 開發期「連超管都預設看不到，避免揭露給現有會員」）：
//
//	shown/open         → 全體皆可見（appsettings specs 沿用通用 isEntryState 驗證器，"open" 是其
//	                      餘 *_entry_state 慣例的「全開」值，這裡當同義詞收，避免後台填錯字）
//	whitelist          → is_vvip=TRUE 或 email/帳號編碼命中白名單 或 super_admin
//	hidden(預設/其他值) → 一律不可見，即使是超級管理員——這是刻意的「先只給站長自己開」開關，
//	                      與其餘 *_entry_state 慣例（hidden 仍對超管旁路）不同，比照任務規格 D3 明文。
//	                      state 想暫時整批關閉時，改回 hidden 即可，不需要動白名單。
func ResolveEntry(state, whitelist string, isVVIP bool, email, code string, isSuperAdmin bool) string {
	switch state {
	case "shown", "open":
		return "shown"
	case "whitelist":
		if isVVIP || isSuperAdmin || whitelisted(whitelist, email, code) {
			return "shown"
		}
		return "hidden"
	default: // hidden 或未設定
		return "hidden"
	}
}

// DashboardEntry 從系統設定 + userID 查 is_vvip 解析入口狀態，供 profile.membership.go 的
// Dashboard 呼叫（比照 gpscalib.DashboardSummary 前例，套件內自己查 is_vvip，email/code/
// isSuperAdmin 由呼叫端傳入——Dashboard 熱路徑通常已經查過一次，避免重複查詢）。
func DashboardEntry(ctx context.Context, db *pgxpool.Pool, userID, email, code string, isSuperAdmin bool) string {
	var isVVIP bool
	if err := db.QueryRow(ctx, `SELECT COALESCE(is_vvip,FALSE) FROM users WHERE id=$1`, userID).Scan(&isVVIP); err != nil {
		isVVIP = false // 查詢失敗保守視為非 VVIP，不因此讓 Dashboard 500
	}
	state := appsettings.GetString(ctx, db, EntryStateKey, "hidden")
	wl := appsettings.GetString(ctx, db, EntryWhitelistKey, "")
	return ResolveEntry(state, wl, isVVIP, email, code, isSuperAdmin)
}

// whitelisted 比照 profile/membership.go personalWhitelisted：換行/逗號/分號/空白分隔，
// 可填帳號編碼（#可省）或 email，大小寫不敏感。
func whitelisted(list, email, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	code = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(code), "#"))
	for _, tok := range strings.FieldsFunc(list, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		t := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tok), "#"))
		if t == "" {
			continue
		}
		if (email != "" && t == email) || (code != "" && t == code) {
			return true
		}
	}
	return false
}

// identity 使用者身分快照（requireEntry 中介層 + handler 共用）。
type identity struct {
	Email        string
	Code         string
	IsVVIP       bool
	IsSuperAdmin bool
}

// resolveIdentity 查該使用者的 email/account_code/is_vvip/is_super_admin。
func resolveIdentity(ctx context.Context, db *pgxpool.Pool, userID string) (identity, error) {
	var id identity
	err := db.QueryRow(ctx, `SELECT COALESCE(email,''), COALESCE(account_code,''), COALESCE(is_vvip,FALSE), is_super_admin FROM users WHERE id=$1`, userID).
		Scan(&id.Email, &id.Code, &id.IsVVIP, &id.IsSuperAdmin)
	return id, err
}

// baseLevelFromExp 依 exp 與 level_config 門檻表推導「基本等級」（沿用既有 DOR 等級系統，
// 見 profile.membership.go computeLevel 的同一概念；獨立重查一次是為了不 import profile 造成
// profile↔rpg 循環依賴，見檔頭註解）。查無等級設定時回 1（比照 computeLevel 的預設）。
func baseLevelFromExp(ctx context.Context, db *pgxpool.Pool, exp int) (int, error) {
	rows, err := db.Query(ctx, `SELECT level, exp_required FROM level_config ORDER BY exp_required`)
	if err != nil {
		return 1, err
	}
	defer rows.Close()
	level := 1
	for rows.Next() {
		var lv, required int
		if err := rows.Scan(&lv, &required); err != nil {
			return 1, err
		}
		if exp >= required {
			level = lv
		}
	}
	return level, rows.Err()
}
