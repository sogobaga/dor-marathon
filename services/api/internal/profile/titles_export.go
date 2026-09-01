// 虛擬選手稱號同步（internal/virtualrunner）對外最小輸出面：重用既有稱號引擎
// （titleCategoryStats/checkAndAwardTitles，見 titles.go），不複製 SQL。這兩支只依賴 h.db，
// 故內部 new 一個最小 Handler（rt 欄位在稱號流程用不到，留零值）即可呼叫既有私有方法——
// 改動最小、不動既有玩家路徑的方法簽名。
package profile

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UnlockedTitle 某使用者已解鎖的一筆稱號，供 virtualrunner.SyncTitles 挑選「最符合人設」的展示
// 稱號使用（tier/sort_order 是挑選規則的排序依據）。
type UnlockedTitle struct {
	Code      string
	Category  string
	Tier      int
	SortOrder int
}

// AwardTitles 依既有稱號引擎規則對 uid 解鎖新稱號，回傳本次新解鎖的 code 清單（無新解鎖回空
// slice）。虛擬選手是機器人帳號、永不登入看不到解鎖彈窗，因此這裡在既有 checkAndAwardTitles
// 寫入 seen=FALSE 之後，額外補一條 UPDATE 把該 user 的 user_titles.seen 全設 TRUE——避免堆積
// 永遠不會被任何前端消化的「未讀」列（真人玩家路徑不受影響，這條 UPDATE 只在本函式呼叫時執行）。
func AwardTitles(ctx context.Context, db *pgxpool.Pool, uid string) (newCodes []string, err error) {
	h := &Handler{db: db}
	levels, err := h.levelConfigList(ctx)
	if err != nil {
		return nil, fmt.Errorf("load level config: %w", err)
	}
	awarded := h.checkAndAwardTitles(ctx, uid, levels) // best-effort：內部失敗只 log，回 nil
	if len(awarded) == 0 {
		return nil, nil
	}
	codes := make([]string, len(awarded))
	for i, a := range awarded {
		codes[i] = a.Code
	}
	if _, err := db.Exec(ctx, `UPDATE user_titles SET seen=TRUE WHERE user_id=$1 AND NOT seen`, uid); err != nil {
		return nil, fmt.Errorf("mark titles seen: %w", err)
	}
	return codes, nil
}

// UnlockedTitles 該使用者目前已解鎖的全部稱號（不篩 category）。
func UnlockedTitles(ctx context.Context, db *pgxpool.Pool, uid string) ([]UnlockedTitle, error) {
	rows, err := db.Query(ctx, `
		SELECT td.code, td.category, td.tier, td.sort_order
		FROM user_titles ut JOIN title_defs td ON td.code = ut.title_code
		WHERE ut.user_id=$1`, uid)
	if err != nil {
		return nil, fmt.Errorf("list unlocked titles: %w", err)
	}
	defer rows.Close()
	out := []UnlockedTitle{}
	for rows.Next() {
		var t UnlockedTitle
		if err := rows.Scan(&t.Code, &t.Category, &t.Tier, &t.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
