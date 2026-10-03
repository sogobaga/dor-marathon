package ops

// 每日報告「直連手錶」段（COROS GA 契約 §2.9／Garmin 計畫 S5）：供應商無關，每個直連供應商（COROS MCP、
// Garmin）各實作一個 DirectWearableProvider 註冊進來，日報只印「人數」——
//
//	⚠️ 絕不印顯示名稱、email、帳號編碼、任何可識別個人的欄位（政策稿前提 P5）。
//	   Notes 是供應商自己產生的人話備註，同樣只能含統計數字，不得含使用者識別資訊。
//
// 純 DB、不逐人呼叫外部 API（Terra 日報序列呼叫曾拖慢報告）；單一供應商查詢失敗只在該行顯示「統計失敗」，
// 不讓整份報告失敗。
//
// 依賴方向：integration 套件實作這個介面（terra.go 已有同樣的 import 方向），本套件不得 import integration。

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
)

// DirectWearableStats 單一直連供應商的彙整統計（全是人數／筆數）。
type DirectWearableStats struct {
	Provider string // 小寫代碼（coros／garmin），顯示時轉大寫
	// Connected 目前有連線列的人數。
	Connected int
	// Active24h 24 小時內有成功同步／收到資料的人數。
	Active24h int
	// NeedsReauth 需要重新授權的人數（refresh 失敗、無 refresh token 且已過期…）。
	NeedsReauth int
	// Paused 暫停中的人數（例如 Garmin 的連線暫停旗標）；沒有此概念的供應商填 0。
	Paused int
	// Stale3d last_synced_at（或最後收到資料）超過 3 天的人數。
	Stale3d int
	// Events24h／Imported24h 24 小時內收到的事件數／實際匯入的活動數（只有 push 型供應商有意義）。
	Events24h   int
	Imported24h int
	// PendingStale 事件已收到但卡在待處理超過門檻的筆數；Dead 死信筆數；UnknownUser24h 24h 內收到的
	// 「查無對應連線」事件數（push 型供應商才有）。
	PendingStale   int
	Dead           int
	UnknownUser24h int
	// Notes 補充說明（人話，只能含統計數字，不得含使用者識別資訊）。
	Notes []string
}

// DirectWearableProvider 直連供應商介面（integration 套件實作）。
type DirectWearableProvider interface {
	// Name 供應商代碼（小寫），日誌與統計失敗行用。
	Name() string
	// DirectStats 純 DB 統計。
	DirectStats(ctx context.Context) (DirectWearableStats, error)
	// MaintainDaily 每日維護（清理過期紀錄、補償掃描…），回傳一行人話摘要（空字串＝沒有要報告的）。
	// 日報每次產生報告時呼叫一次，必須冪等；失敗只記 log，不影響報告。
	MaintainDaily(ctx context.Context) (string, error)
}

// AddDirectWearable 註冊一個直連供應商（main.go 啟動時呼叫；晚於 NewHandler）。
func (h *Handler) AddDirectWearable(p DirectWearableProvider) {
	if p == nil {
		return
	}
	h.mu.Lock()
	h.directWearables = append(h.directWearables, p)
	h.mu.Unlock()
}

func (h *Handler) directWearableProviders() []DirectWearableProvider {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]DirectWearableProvider(nil), h.directWearables...)
}

// directWearableSection 日報「直連手錶」段的資料。
type directWearableSection struct {
	Stats []DirectWearableStats
	// Failed 統計失敗的供應商代碼（只印代碼，不印錯誤內文）。
	Failed []string
	// Maintenance 各供應商 MaintainDaily 回傳的摘要行。
	Maintenance []string
}

// buildDirectWearableSection 對每個已註冊供應商取統計並跑每日維護。任何錯誤只記 log。
func buildDirectWearableSection(ctx context.Context, ps []DirectWearableProvider) directWearableSection {
	var out directWearableSection
	for _, p := range ps {
		name := p.Name()
		st, err := p.DirectStats(ctx)
		if err != nil {
			log.Warn().Err(err).Str("provider", name).Msg("daily report: direct wearable stats failed")
			out.Failed = append(out.Failed, name)
		} else {
			if st.Provider == "" {
				st.Provider = name
			}
			out.Stats = append(out.Stats, st)
		}
		note, err := p.MaintainDaily(ctx)
		if err != nil {
			log.Warn().Err(err).Str("provider", name).Msg("daily report: direct wearable daily maintenance failed")
			continue
		}
		if strings.TrimSpace(note) != "" {
			out.Maintenance = append(out.Maintenance, strings.TrimSpace(note))
		}
	}
	return out
}

// formatDirectWearableLine 組單一供應商的一行（純人數）。⚠️ 開頭＝有需要人工看的訊號
// （超過 3 天沒同步、待處理逾時、死信）；需要重新授權的人數本身不加 ⚠️（開放初期是常態）。
func formatDirectWearableLine(s DirectWearableStats) string {
	var b strings.Builder
	warn := s.Stale3d > 0 || s.PendingStale > 0 || s.Dead > 0
	if warn {
		b.WriteString("⚠️ ")
	}
	fmt.Fprintf(&b, "%s：連線 %d 人，24h 內有同步 %d 人，需重新授權 %d 人，超過 3 天未同步 %d 人",
		strings.ToUpper(s.Provider), s.Connected, s.Active24h, s.NeedsReauth, s.Stale3d)
	if s.Paused > 0 {
		fmt.Fprintf(&b, "，暫停 %d 人", s.Paused)
	}
	if s.Events24h > 0 { // push 型供應商（Garmin）：事件與匯入並陳
		fmt.Fprintf(&b, "；24h 事件 %d 筆、匯入 %d 筆", s.Events24h, s.Imported24h)
	} else if s.Imported24h > 0 { // pull 型供應商（COROS）：沒有「事件」概念，只看匯入筆數
		fmt.Fprintf(&b, "；24h 匯入 %d 筆", s.Imported24h)
	}
	if s.PendingStale > 0 {
		fmt.Fprintf(&b, "；待處理逾時 %d 筆", s.PendingStale)
	}
	if s.Dead > 0 {
		fmt.Fprintf(&b, "；死信 %d 筆", s.Dead)
	}
	if s.UnknownUser24h > 0 {
		fmt.Fprintf(&b, "；24h 未知使用者事件 %d 筆", s.UnknownUser24h)
	}
	return b.String()
}

// formatDirectWearableSection 組整段文字（不含標題行）；沒有任何內容時回空字串（呼叫端據此整段不顯示）。
func formatDirectWearableSection(sec directWearableSection) string {
	var lines []string
	for _, s := range sec.Stats {
		lines = append(lines, formatDirectWearableLine(s))
		for _, n := range s.Notes {
			if n = strings.TrimSpace(n); n != "" {
				lines = append(lines, "· "+n)
			}
		}
	}
	for _, name := range sec.Failed {
		lines = append(lines, fmt.Sprintf("⚠️ %s：統計失敗（詳見伺服器日誌）", strings.ToUpper(name)))
	}
	for _, m := range sec.Maintenance {
		lines = append(lines, "· "+m)
	}
	return strings.Join(lines, "\n")
}
