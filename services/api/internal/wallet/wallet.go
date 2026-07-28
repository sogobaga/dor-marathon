// Package wallet 提供 GP（Game Point，環台大富翁貨幣）加/扣值的共用 helper，供新功能呼叫。
// 刻意獨立成 leaf 套件（不依賴 profile/race），避免 import 循環（比照 internal/vip 的模式）。
//
// 本階段只新增本套件供新程式使用；既有 DP 內聯 CAS SQL（如 internal/explore、internal/personaltask）
// 的行為維持不變，不在本階段遷移。
package wallet

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execer 是 *pgxpool.Pool 與 pgx.Tx 的共同子集，讓呼叫端可傳入交易以確保原子性（比照 internal/vip.Execer）。
type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// txBeginner 是 *pgxpool.Pool 與 pgx.Tx 皆滿足的共同子集（兩者都有 Begin）。AwardGP/SpendGP
// 各自要執行兩條 SQL（改餘額 + 寫 gp_events），必須同進退，靠它在內部開一個交易：
//   - 呼叫端傳入 *pgxpool.Pool → 這裡是真正的 BEGIN/COMMIT/ROLLBACK。
//   - 呼叫端傳入既有 pgx.Tx（外層已在交易中）→ pgx 對 Tx.Begin 會退化成 SAVEPOINT，
//     一樣保證這兩條 SQL 同進退，且不會誤 Commit 或中斷呼叫端外層的交易。
type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// withTx 在 db 支援 Begin 時（Execer 目前唯二的實作 *pgxpool.Pool、pgx.Tx 皆支援）於內部交易
// 中執行 fn：fn 成功才 Commit，任何一步失敗立即 Rollback，確保 fn 內的多條 SQL 同進退，不會
// 半途而廢（部分成功、部分失敗）。
func withTx(ctx context.Context, db Execer, fn func(Execer) error) error {
	tb, ok := db.(txBeginner)
	if !ok {
		// 理論上不會發生；保底維持修正前「直接在 db 上執行」的行為。
		return fn(db)
	}
	tx, err := tb.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AwardGP 原子加值 GP，並寫一筆 gp_events 流水（純對帳用，不參與業務判斷）。delta 應 > 0，<=0 為 no-op。
// refID 可為 nil（無關聯物件）。「改餘額」與「寫 gp_events」保證同進退（見 withTx）：其中一步失敗，
// 另一步不會留下痕跡，不會發生「錢動了但流水沒留下」或「流水寫了但錢沒動」的部分失敗。
func AwardGP(ctx context.Context, db Execer, userID string, delta int, reason, refType string, refID *string) error {
	if delta <= 0 {
		return nil
	}
	return withTx(ctx, db, func(e Execer) error {
		if _, err := e.Exec(ctx, `UPDATE users SET gp = gp + $1 WHERE id = $2`, delta, userID); err != nil {
			return err
		}
		_, err := e.Exec(ctx, `
			INSERT INTO gp_events (user_id, delta, reason, ref_type, ref_id)
			VALUES ($1, $2, $3, $4, $5)`, userID, delta, reason, refType, refID)
		return err
	})
}

// AwardDP 原子加值 DP，簽名與 AwardGP 對稱（供新程式呼叫時兩種貨幣寫法一致）。delta 應 > 0，<=0 為
// no-op。refID 目前保留給未來若新增 DP 流水表時使用，本函式暫不寫入任何流水：DP 至今沒有獨立的
// ledger 表（gp 有 gp_events，dp 沒有對應的 dp_events），既有呼叫端（internal/explore、
// internal/personaltask、internal/event、internal/race 等）一律用內聯 `UPDATE users SET dp = dp + $n`
// 直接改 users.dp，此處刻意維持相同行為、不為此新增流水表，避免「同一種貨幣有兩套加值路徑各自記帳」
// 的不一致。reason/refType/refID 暫時不落地，僅保留參數以維持與 AwardGP 對稱的呼叫介面，方便未來若要
// 補流水表時只需改本函式內部、不必動呼叫端。
func AwardDP(ctx context.Context, db Execer, userID string, delta int, reason, refType string, refID *string) error {
	if delta <= 0 {
		return nil
	}
	_, err := db.Exec(ctx, `UPDATE users SET dp = dp + $1 WHERE id = $2`, delta, userID)
	return err
}

// SpendGP CAS 原子扣值：UPDATE ... WHERE gp >= amount，RowsAffected=0 代表餘額不足 → ok=false，
// 餘額完全不變（絕不可讓 gp 變負）。成功才寫入 gp_events（delta 為負）。amount 應 > 0，<=0 視為
// 無需扣款、直接回 ok=true。refID 可為 nil。「改餘額」與「寫 gp_events」保證同進退（見 withTx）：
// gp_events 寫入失敗時，已執行的扣款 UPDATE 會隨內部交易一起 Rollback，err!=nil 時餘額必定
// 維持未扣款前的狀態，呼叫端可放心把 err!=nil 與 ok==false（餘額不足）都當作「這筆消費沒發生」
// 處理、允許重試，不會有雙花風險。
func SpendGP(ctx context.Context, db Execer, userID string, amount int, reason, refType string, refID *string) (bool, error) {
	if amount <= 0 {
		return true, nil
	}
	ok := false
	err := withTx(ctx, db, func(e Execer) error {
		tag, err := e.Exec(ctx, `UPDATE users SET gp = gp - $1 WHERE id = $2 AND gp >= $1`, amount, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil // 餘額不足：無異動可回滾，交易正常 Commit，ok 維持 false
		}
		if _, err := e.Exec(ctx, `
			INSERT INTO gp_events (user_id, delta, reason, ref_type, ref_id)
			VALUES ($1, $2, $3, $4, $5)`, userID, -amount, reason, refType, refID); err != nil {
			return err
		}
		ok = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return ok, nil
}
