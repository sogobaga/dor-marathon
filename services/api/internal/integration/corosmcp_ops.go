package integration

// COROS MCP 的每日報告「直連手錶」段來源（GA 契約 §2.9、計畫 S5）：實作 ops.DirectWearableProvider。
// 純 DB、單一查詢、不逐人呼叫 COROS；只回人數／筆數（絕不回顯示名稱或任何可識別個人的欄位）。
// ops 套件不得 import integration（integration 已 import ops，見 terra.go 的 WearableReporter 同款依賴方向）。

import (
	"context"
	"fmt"

	"github.com/dor/api/internal/ops"
)

// Name 供應商代碼（日報顯示 COROS）。
func (h *CorosMcpHandler) Name() string { return "coros" }

// DirectStats 統計 COROS MCP 連線的健康度：
//   - Connected：連線人數；
//   - Active24h：24 小時內有成功同步（last_synced_at）的人數；
//   - NeedsReauth：需要重新授權（已標記 reauth_required_at，或 access token 已過期且沒有 refresh token）；
//   - Stale3d：最後成功同步（從未同步則以連接時間計）超過 3 天；
//   - Imported24h：24 小時內匯入的活動筆數；
//   - Notes：尚未綁定 COROS 帳號識別（沒有 id_token 或舊連線）的人數——這些連線無法防止「同一個 COROS 帳號連到兩個 DOR 帳號」。
func (h *CorosMcpHandler) DirectStats(ctx context.Context) (ops.DirectWearableStats, error) {
	st := ops.DirectWearableStats{Provider: "coros"}
	var unbound int
	err := h.repo.db.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE last_synced_at >= NOW() - INTERVAL '24 hours'),
		       COUNT(*) FILTER (WHERE reauth_required_at IS NOT NULL OR (refresh_token = '' AND expires_at <= NOW())),
		       COUNT(*) FILTER (WHERE COALESCE(last_synced_at, created_at) < NOW() - INTERVAL '3 days'),
		       COUNT(*) FILTER (WHERE COALESCE(provider_user_id,'') = ''),
		       (SELECT COUNT(*) FROM activities WHERE source = 'coros' AND external_id LIKE 'mcp:%' AND created_at >= NOW() - INTERVAL '24 hours')
		FROM user_integrations WHERE provider = 'coros_mcp'`).
		Scan(&st.Connected, &st.Active24h, &st.NeedsReauth, &st.Stale3d, &unbound, &st.Imported24h)
	if err != nil {
		return st, err
	}
	if unbound > 0 {
		st.Notes = append(st.Notes, fmt.Sprintf("尚未綁定 COROS 帳號識別的連線 %d 人（無法防止同一個 COROS 帳號連到多個 DOR 帳號）", unbound))
	}
	return st, nil
}

// MaintainDaily 每日維護：COROS MCP 目前沒有需要在日報順手做的清理（讀取測試紀錄的保存期限清除由既有的
// ops.ProbeLogPurger／PurgeExpired 負責，避免重複清理與重複計數）。
func (h *CorosMcpHandler) MaintainDaily(ctx context.Context) (string, error) { return "", nil }

// 編譯期檢查：CorosMcpHandler 必須滿足 ops.DirectWearableProvider。
var _ ops.DirectWearableProvider = (*CorosMcpHandler)(nil)
