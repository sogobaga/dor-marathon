package runmeet

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/runmeet/live"
)

// live_hooks.go：團練同步跑（internal/runmeet/live）的「撤銷掛鉤」與「緊急關閉」接線。
// 契約：docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §4。
//
// ## 撤銷掛鉤清單（寫入點對照，P1 驗收：grep run_meet_members／run_meets 所有寫入點）
//
// 這份清單由 live_hooks_test.go 的 AST 稽核測試強制：套件內任何寫入 run_meet_members／run_meets 的
// 函式都必須列在那份表裡（hooked 或附理由的 not-hooked），新增寫入點卻沒分類 → 測試紅燈。
//
// 原則（審查 M1）：
//  1. 一律在 DB commit **之後**呼叫（commit 之前呼叫會被後到的 /live/start——還讀得到舊的 joined
//     ——覆蓋；start 另以 checkedAtMs 對照撤銷墓碑關掉這個 TOCTOU）。
//  2. 掛鉤**永遠不得讓主流程失敗**：失敗只記 log、繼續；Redis 為 nil 時整個略過；panic 一律攔下。
//     最壞情況由 grant 有界過期（15 分鐘）兜底，且 DB 才是真相（下一次 start 會被 DB 擋下）。
//  3. 掛鉤放在 repository 層（緊貼 commit），不放 handler——這樣任何呼叫端（含未來的內部路徑）都會觸發。

// liveHooks live.Store 的撤銷介面（定義在使用端，讓 repository 不依賴具體型別，測試可注入 fake）。
type liveHooks interface {
	Revoke(ctx context.Context, meetID, uid string) error
	MarkDead(ctx context.Context, meetID string) error
	ClearDead(ctx context.Context, meetID string) error
}

// liveHookTimeout 單次掛鉤的逾時（Redis 單一指令，正常 < 1 ms；逾時只會讓掛鉤失敗，不影響主流程）。
const liveHookTimeout = 2 * time.Second

// SetLiveHooks 注入撤銷掛鉤（NewHandler 接線）。h 為 nil＝不接線（Redis 未設定）。
// ⚠️ 呼叫端必須傳「確定非 nil 的實作」或乾脆傳 nil 介面——不要傳 typed-nil（*live.Store(nil)）。
func (r *Repository) SetLiveHooks(h liveHooks) { r.live = h }

// liveRevoke 撤銷某人在某團練的同步（被踢／拒絕／退出）。DB commit 之後呼叫。
func (r *Repository) liveRevoke(ctx context.Context, meetID, userID string) {
	r.liveCall(ctx, "revoke", meetID, userID, func(c context.Context) error {
		return r.live.Revoke(c, meetID, userID)
	})
}

// liveMarkDead 團練取消／刪除／後台下架：所有人的同步終止。DB commit 之後呼叫。
func (r *Repository) liveMarkDead(ctx context.Context, meetID string) {
	r.liveCall(ctx, "mark_dead", meetID, "", func(c context.Context) error {
		return r.live.MarkDead(c, meetID)
	})
}

// liveClearDead 團練恢復（cancelled→open、後台取消下架）：清掉 dead 旗標。
// ⚠️ 契約只列了 MarkDead；沒有對應清除會讓恢復後的團練在 24 小時內一直回 meet_over。
func (r *Repository) liveClearDead(ctx context.Context, meetID string) {
	r.liveCall(ctx, "clear_dead", meetID, "", func(c context.Context) error {
		return r.live.ClearDead(c, meetID)
	})
}

// liveCall 掛鉤共用外殼：nil 安全、與請求取消脫鉤（commit 已完成，client 此時斷線不該讓撤銷落空）、
// 帶逾時、攔 panic、只記 log。⚠️ log 內容只有操作名稱與 id——沒有座標、沒有 body。
func (r *Repository) liveCall(ctx context.Context, op, meetID, userID string, fn func(context.Context) error) {
	if r == nil || r.live == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			log.Error().Interface("panic", rec).Str("op", op).Str("meet_id", meetID).
				Msg("runmeet live hook panicked (ignored; DB state is authoritative, grant expires within 15 min)")
		}
	}()
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveHookTimeout)
	defer cancel()
	if err := fn(cctx); err != nil {
		ev := log.Warn().Err(err).Str("op", op).Str("meet_id", meetID)
		if userID != "" {
			ev = ev.Str("user_id", userID)
		}
		ev.Msg("runmeet live hook failed (ignored; DB state is authoritative, grant expires within 15 min)")
	}
}

// --- 緊急關閉（契約 §4 M2）---

// registerLiveKillSwitch 註冊「後台寫入 runmeet_live_entry_state」的回呼：值為 hidden → SET rml:kill 1；
// 其他任何值（含空字串）→ DEL。kill 旗標讓 /pos、/start 在下一個請求就回 410 killed——/pos 熱路徑
// 不讀設定，沒有這個旗標就無法在事故中緊急止血。回傳取消註冊函式（測試用）。
//
// ⚠️ hidden 對所有人生效，**包含超管**（見 liveEntryFrom）：kill 旗標在 Lua 裡對任何使用者一視同仁，
// 入口判定若讓超管繞過 hidden，超管會看到可按的按鈕、按下去卻得到 410。
func registerLiveKillSwitch(store *live.Store) (unregister func()) {
	return appsettings.OnChange(live.EntryStateKey, func(ctx context.Context, value string) {
		on := strings.TrimSpace(value) == "hidden"
		if err := store.SetKill(ctx, on); err != nil {
			// 管理員以為已緊急關閉、實際沒有：這是安全相關的失敗，記 Error。
			log.Error().Err(err).Bool("kill", on).
				Msg("runmeet live: failed to sync kill flag with runmeet_live_entry_state (re-save the setting to retry)")
		}
	})
}
