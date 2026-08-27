// 每日排程骨架完全比照 internal/ops/selfcheck.go（hourly tick + pg_try_advisory_lock + in-memory
// 當日冪等 lastRunDate），差異只在執行窗口改台灣時間 03:00-03:59（離開 08:00 的自檢/營運報告，
// 三個每日排程互不搶時段）＋額外的「啟動補跑」邏輯（見 maybeCatchUp）。
package analytics

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// scheduleTickInterval 每小時檢查一次「現在是否落在今天的執行窗口內」，同 selfcheck.go。
	scheduleTickInterval = time.Hour

	// scheduleWindowHour 執行窗口：台灣時間 03:00-03:59。
	scheduleWindowHour = 3

	// scheduleAdvisoryLockName pg_try_advisory_lock 用的鎖名，獨立於 "ops_daily_selfcheck"/
	// "ops_daily_report"，避免跟既有排程互搶（見 migration 148 檔頭註解）。
	scheduleAdvisoryLockName = "member_analytics_daily"

	// catchUpStaleAfter 服務啟動時的補跑判準：最新一筆報告的 computed_at 距今超過這個門檻（或完全
	// 沒有任何報告），視為「該立即補算一次」，不等到今天的 03:00 視窗——避免服務重啟時間點不巧落在
	// 03:00 之後，導致要多等快一整天才有新報告。25h 比一天（24h）多一點緩衝，容忍排程本身在正常
	// 一天一次的節奏下的些微時間漂移，不會因為「今天 03:05 算完、隔天 02:58 檢查」這種邊界情況被
	// 誤判成過期。
	catchUpStaleAfter = 25 * time.Hour

	// computeTimeout 排程觸發的重算逾時。比 handler.go 的 recomputeTimeout（20s，API 端使用者
	// 在等）寬鬆，因為這是背景排程、沒有人在前端等 HTTP 回應，多給一點餘裕降低偶發逾時機率；
	// 20s vs 60s 純粹是「使用者等待中」與「背景排程」兩種情境的取捨，不代表六大區塊查詢本身需要
	// 這麼久（見 handler.go Recompute 註解對查詢效能的預期）。
	computeTimeout = 60 * time.Second
)

// RunLoop 背景每日排程：啟動先檢查一次是否需要補跑（見 maybeCatchUp），之後每小時檢查一次是否
// 落在今天的 03:00-03:59 執行窗口；ctx 取消即結束。比照 internal/ops/selfcheck.go
// RunSelfCheckLoop 的迴圈骨架。
func (h *Handler) RunLoop(ctx context.Context) {
	h.maybeCatchUp(ctx)
	t := time.NewTicker(scheduleTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.maybeRunDaily(ctx)
		}
	}
}

// maybeCatchUp 服務啟動時的補跑判斷：最新報告已超過 catchUpStaleAfter（或完全沒有報告）就立即
// 算一次（略過視窗檢查，但仍走完整的當日冪等 + advisory lock 機制，見 tryRun）。
func (h *Handler) maybeCatchUp(ctx context.Context) {
	computedAt, err := LatestComputedAt(ctx, h.db)
	stale := true
	switch {
	case err == nil:
		stale = time.Since(computedAt) > catchUpStaleAfter
	case errors.Is(err, ErrNoReport):
		stale = true
	default:
		log.Error().Err(err).Msg("member analytics: catch-up staleness check failed, skip catch-up this boot")
		return
	}
	if !stale {
		return
	}
	log.Info().Msg("member analytics: latest report missing/stale on boot, running catch-up compute now")
	h.tryRun(ctx, true)
}

// maybeRunDaily 每小時 tick 呼叫：只有落在今天的執行窗口內才真的跑（bypassWindow=false）。
func (h *Handler) maybeRunDaily(ctx context.Context) {
	h.tryRun(ctx, false)
}

// tryRun 雙層防重複（同 selfcheck.go maybeRunDaily 的取捨，理由完全相同：本套件全程唯讀彙整+
// 單筆 UPSERT，重複執行不會造成資料錯誤，鎖純粹是效能考量）：
//  1. in-memory lastRunDate：同一實例同一天只認領一次。
//  2. pg_try_advisory_lock：多實例（Railway 水平擴展）情境下，同一時刻只有一個實例真的執行。
//
// bypassWindow=true 時（maybeCatchUp 呼叫）略過「是否在 03:00-03:59」的視窗檢查，但當日冪等與
// advisory lock 兩層防重複機制依然套用——若補跑當下剛好也落在視窗內，之後同一小時的 tick 不會
// 重複再跑一次。
func (h *Handler) tryRun(ctx context.Context, bypassWindow bool) {
	now := taiwanNow()
	if !bypassWindow && now.Hour() != scheduleWindowHour {
		return
	}
	today := now.Format("2006-01-02")

	h.mu.Lock()
	alreadyRan := h.lastRunDate == today
	h.mu.Unlock()
	if alreadyRan {
		return
	}

	conn, err := h.db.Acquire(ctx)
	if err != nil {
		log.Error().Err(err).Msg("member analytics: acquire dedicated connection for advisory lock failed")
		return
	}
	defer conn.Release()

	var gotLock bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, scheduleAdvisoryLockName).Scan(&gotLock); err != nil {
		log.Error().Err(err).Msg("member analytics: try advisory lock failed")
		return
	}
	if !gotLock {
		log.Debug().Msg("member analytics: another instance is already running/ran today, skip")
		return
	}
	defer func() {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, scheduleAdvisoryLockName).Scan(&unlocked); err != nil {
			log.Warn().Err(err).Msg("member analytics: advisory unlock failed (will auto-release once this connection closes)")
		}
	}()

	// 搶到鎖即視為「今天由本實例認領」，不論後面實際執行結果如何都不重試——比照 selfcheck.go
	// 「先佔位再執行」的順序（下一次落在視窗內的 tick 已是明天）。
	h.mu.Lock()
	h.lastRunDate = today
	h.mu.Unlock()

	computeCtx, cancel := context.WithTimeout(ctx, computeTimeout)
	defer cancel()

	rpt := BuildReport(computeCtx, h.db)
	if err := SaveReport(computeCtx, h.db, rpt); err != nil {
		log.Error().Err(err).Msg("member analytics: scheduled compute save failed")
		return
	}
	log.Info().Str("day", rpt.Day).Msg("member analytics: daily report computed and saved")
}
