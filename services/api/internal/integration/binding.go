package integration

// 供應商帳號綁定（COROS GA 契約 §2.6、Garmin 直連沿用）。
//
// 問題：同一個手錶廠商帳號可被多個 DOR 帳號輪流連接，同一趟活動就能在兩個 DOR 帳號各領一次獎勵。
// 解法：provider_user_id（廠商帳號的穩定識別）非空時，(provider, provider_user_id) 必須唯一——
// migration 198 的部分唯一索引 user_integrations_provider_account_uniq。這個檔案只負責把「撞到唯一索引」
// 的資料庫錯誤翻成業務錯誤；寫入端（SaveCorosMcp／Garmin SaveGarmin）遇到時回 ErrProviderAccountLinked，
// callback 導回 /?coros_mcp=already_linked，前台顯示「此 COROS 帳號已連結到另一個 DOR 帳號」。

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrProviderAccountLinked 這個廠商帳號已被另一個 DOR 帳號連接。
var ErrProviderAccountLinked = errors.New("integration: provider account already linked to another user")

// providerAccountUniqIndex migration 198 建立的部分唯一索引名稱。
const providerAccountUniqIndex = "user_integrations_provider_account_uniq"

// IsProviderAccountConflict 判斷 err 是否為「撞到 (provider, provider_user_id) 唯一索引」：
// pgconn 23505（unique_violation）、表為 user_integrations。優先認約束名稱；某些驅動／代理層不帶約束名稱時，
// 退而認「表名 user_integrations 且約束不是 (user_id, provider) 那條主要唯一鍵」——
// (user_id, provider) 的衝突在 INSERT … ON CONFLICT (user_id, provider) 下不會以錯誤形式出現，
// 因此 user_integrations 上其他任何 23505 都只可能來自帳號綁定索引。
func IsProviderAccountConflict(err error) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	if pe.Code != "23505" {
		return false
	}
	if pe.ConstraintName == providerAccountUniqIndex {
		return true
	}
	return pe.TableName == "user_integrations" && pe.ConstraintName != "user_integrations_user_id_provider_key"
}
