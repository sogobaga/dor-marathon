package integration

// Garmin 事件處理框架（落地後的就地處理、重試、掃描、優雅關閉）。
//
// 設計重點：
//   - integration_events 是唯一事實來源。回 200 之後 Garmin 不再重送，所以「已 ack 但沒處理完」的事件必須能由
//     這張表重放：啟動後 60 秒掃一次（StartupSweep）、下一筆推送順手掃（5 分鐘節流）、每日報告窗口再掃一次
//     （MaintainDaily）。**沒有任何 ticker／週期性 DB 迴圈**（Neon 必須能睡）；失敗重試只用一次性的記憶體計時器
//     （time.AfterFunc），計時器遺失（重啟）時由上述掃描兜底。
//   - 同一個 Garmin 使用者的事件序列化：行程內互斥＋Postgres advisory xact lock（跨副本；與 PgBouncer
//     transaction pooling 相容，不依賴 session 層 advisory lock，也不依賴 Redis）。
//   - 處理不使用請求 ctx（脫離請求但保留 dbwake 喚醒歸因）；單筆 20 秒、整批預算 25 秒，超過的事件釋放租約交給下次掃描。
//   - 暫時性失敗：attempts+1、退避 1m／5m／30m／2h；第 5 次失敗轉 dead 並告警（告警不含使用者資料）。

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/dbwake"
)

// workBegin 登記一件背景工作；已進入關閉流程回 false（呼叫端不得再開工）。
func (h *GarminHandler) workBegin() bool {
	h.workMu.Lock()
	defer h.workMu.Unlock()
	if h.workClosed {
		return false
	}
	h.workN++
	return true
}

func (h *GarminHandler) workEnd() {
	h.workMu.Lock()
	h.workN--
	if h.workN == 0 && h.workClosed && h.workIdle != nil {
		close(h.workIdle)
		h.workIdle = nil
	}
	h.workMu.Unlock()
}

// workCloseAndWait 停止接受新工作並等待進行中的工作結束（或 ctx 到期）。
func (h *GarminHandler) workCloseAndWait(ctx context.Context) {
	h.workMu.Lock()
	h.workClosed = true
	if h.workN == 0 {
		h.workMu.Unlock()
		return
	}
	idle := make(chan struct{})
	h.workIdle = idle
	h.workMu.Unlock()
	select {
	case <-idle:
	case <-ctx.Done():
	}
}

func (h *GarminHandler) track(ids []string) {
	if len(ids) == 0 {
		return
	}
	h.inflightM.Lock()
	for _, id := range ids {
		h.inflight[id] = struct{}{}
	}
	h.inflightM.Unlock()
}

func (h *GarminHandler) untrack(ids []string) {
	if len(ids) == 0 {
		return
	}
	h.inflightM.Lock()
	for _, id := range ids {
		delete(h.inflight, id)
	}
	h.inflightM.Unlock()
}

func (h *GarminHandler) inflightIDs() []string {
	h.inflightM.Lock()
	defer h.inflightM.Unlock()
	out := make([]string, 0, len(h.inflight))
	for id := range h.inflight {
		out = append(out, id)
	}
	return out
}

// --- 公開：啟動掃描、關閉、每日維護 ---

// StartupSweep 啟動後延遲 60 秒掃描一次到期（含啟動前被部署殺掉而遺留）的事件，只跑一次、不是迴圈。
// main.go：go garminHandler.StartupSweep(dbwake.WithJob(bgCtx, "garmin_startup_sweep"))
func (h *GarminHandler) StartupSweep(ctx context.Context) {
	t := time.NewTimer(h.startupDelay)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		return
	}
	if n := h.SweepPending(ctx); n > 0 {
		log.Info().Int("events", n).Msg("garmin startup sweep processed events")
	}
}

// Drain 優雅關閉：不再接受新的背景處理、停掉重試計時器、等進行中的事件處理結束（ctx 無期限時最多 20 秒），
// 最後把仍未完成的事件租約釋放（next_attempt_at=now），讓新行程的啟動掃描能立刻接手。
func (h *GarminHandler) Drain(ctx context.Context) {
	h.draining.Store(true)
	h.stopTimers()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
	}
	h.workCloseAndWait(ctx)
	if ids := h.inflightIDs(); len(ids) > 0 {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := h.store.ReleaseGarminLeases(rctx, ids); err != nil {
			log.Warn().Err(err).Int("events", len(ids)).Msg("garmin drain: release leases failed")
		}
	}
}

// SweepPending 領取並處理「已到期」的事件（最多 10 輪×200 筆）。回傳處理的事件數。
func (h *GarminHandler) SweepPending(ctx context.Context) int {
	total := 0
	for round := 0; round < 10; round++ {
		if ctx.Err() != nil || h.draining.Load() || !h.workBegin() {
			break
		}
		evs, err := h.store.ClaimDueGarminEvents(ctx, garminSweepBatch)
		if err != nil {
			h.workEnd()
			log.Warn().Err(err).Msg("garmin sweep: claim failed")
			break
		}
		if len(evs) == 0 {
			h.workEnd()
			break
		}
		ids := garminEventIDs(evs)
		h.track(ids)
		h.processEvents(ctx, evs)
		h.untrack(ids)
		h.workEnd()
		total += len(evs)
		if len(evs) < garminSweepBatch {
			break
		}
	}
	return total
}

// maybeSweep 推送觸發的順手掃描：Redis SET NX（5 分鐘）搶到才掃，沒有 ticker。
func (h *GarminHandler) maybeSweep(ctx context.Context) {
	ok, err := h.kv.SetNX(ctx, "garmin:sweep", "1", garminSweepThrottle)
	if err != nil || !ok {
		return
	}
	h.SweepPending(ctx)
}

// --- 處理 ---

func garminEventStart(ev garminEvent) int64 {
	if ev.EventType != garminEvActivity {
		return ev.ReceivedAt.Unix()
	}
	var p struct {
		Start int64 `json:"startTimeInSeconds"`
	}
	if json.Unmarshal(ev.Payload, &p) != nil || p.Start <= 0 {
		return ev.ReceivedAt.Unix()
	}
	return p.Start
}

func garminEventRank(t string) int {
	switch t {
	case garminEvActivity:
		return 0
	case garminEvPermission:
		return 1
	default:
		return 2 // deregistration 最後處理（它會清掉該使用者的資料）
	}
}

// sortGarminEvents：先活動（依開始時間由舊到新，讓重疊偵測與 EXP 差額補償順序固定）、再權限、最後撤銷。
func sortGarminEvents(evs []garminEvent) {
	sort.SliceStable(evs, func(i, j int) bool {
		ri, rj := garminEventRank(evs[i].EventType), garminEventRank(evs[j].EventType)
		if ri != rj {
			return ri < rj
		}
		return garminEventStart(evs[i]) < garminEventStart(evs[j])
	})
}

// processEvents 依 Garmin 使用者分組逐一處理；預算（ctx）用完時，剩下的事件釋放租約交給下次掃描。
func (h *GarminHandler) processEvents(ctx context.Context, evs []garminEvent) {
	groups := map[string][]garminEvent{}
	var order []string
	for _, e := range evs {
		if _, ok := groups[e.ProviderUserID]; !ok {
			order = append(order, e.ProviderUserID)
		}
		groups[e.ProviderUserID] = append(groups[e.ProviderUserID], e)
	}
	for i, uid := range order {
		if ctx.Err() != nil {
			var rest []garminEvent
			for _, u := range order[i:] {
				rest = append(rest, groups[u]...)
			}
			h.releaseLeases(rest)
			return
		}
		h.processUserGroup(ctx, uid, groups[uid])
	}
}

func (h *GarminHandler) releaseLeases(evs []garminEvent) {
	if len(evs) == 0 {
		return
	}
	rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.ReleaseGarminLeases(rctx, garminEventIDs(evs)); err != nil {
		log.Warn().Err(err).Int("events", len(evs)).Msg("garmin: release leases failed")
	}
}

func (h *GarminHandler) processUserGroup(ctx context.Context, garminUID string, evs []garminEvent) {
	sortGarminEvents(evs)
	// 先取使用者鎖、再取全域處理名額：等待同一使用者的鎖時不占用全域名額（避免一位使用者的長處理讓所有名額閒置等待）。
	unlock := h.userMu.Lock(garminUID)
	defer unlock()
	select {
	case h.procSem <- struct{}{}:
		defer func() { <-h.procSem }()
	case <-ctx.Done():
		h.releaseLeases(evs)
		return
	}
	err := h.store.WithGarminUserLock(ctx, garminUID, func(lctx context.Context) error {
		for _, ev := range evs {
			if lctx.Err() != nil {
				break
			}
			h.processOne(lctx, ev)
		}
		return nil
	})
	if err != nil {
		// 拿不到鎖（別的副本／清理正在處理同一使用者）或鎖交易本身失敗：事件本身沒有錯，不計失敗次數，稍後再試。
		delay := 30 * time.Second
		note := "busy"
		if !errors.Is(err, errGarminBusy) {
			delay, note = time.Minute, "lock_error"
			log.Warn().Err(err).Msg("garmin: user lock failed, deferring events")
		}
		h.deferEvents(ctx, evs, note, delay)
	}
}

func (h *GarminHandler) deferEvents(ctx context.Context, evs []garminEvent, note string, delay time.Duration) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	next := h.now().Add(delay)
	for _, ev := range evs {
		if err := h.store.DeferGarminEvent(dctx, ev.ID, note, next); err != nil {
			log.Warn().Err(err).Msg("garmin: defer event failed")
			continue
		}
		h.scheduleRetry(ev.ID, delay)
	}
}

// processOne 處理單筆事件並記錄結果（處理函式由 h.handlers 提供，可在測試替換）。
func (h *GarminHandler) processOne(ctx context.Context, ev garminEvent) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Error().Interface("panic", rec).Str("type", ev.EventType).Msg("garmin event handler panic")
			h.failEvent(ctx, ev, errors.New("panic"))
		}
	}()
	var fn func(context.Context, garminEvent) (string, error)
	switch ev.EventType {
	case garminEvActivity:
		fn = h.handlers.activity
	case garminEvDeregister:
		fn = h.handlers.deregister
	case garminEvPermission:
		fn = h.handlers.permission
	}
	if fn == nil {
		h.finishEvent(ctx, ev, "skipped_unknown_type", nil)
		return
	}
	ectx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := fn(ectx, ev)
	h.finishEvent(ctx, ev, result, err)
}

// finishEvent 依處理結果寫回事件狀態。寫回使用不隨 ctx 取消的短 context（避免處理剛好逾時就丟掉結果）。
func (h *GarminHandler) finishEvent(ctx context.Context, ev garminEvent, result string, err error) {
	switch {
	case err == nil:
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if werr := h.store.MarkGarminEventDone(wctx, ev.ID, result); werr != nil {
			// 結果寫不回去：事件保持原狀，租約到期後會被重新處理（處理本身冪等）。
			log.Warn().Err(werr).Msg("garmin: mark event done failed")
		}
	case errors.Is(err, errGarminNotReady):
		h.deferEvents(ctx, []garminEvent{ev}, "processor_not_ready", time.Hour)
	case errors.Is(err, errGarminBusy):
		h.deferEvents(ctx, []garminEvent{ev}, "busy", 30*time.Second)
	default:
		h.failEvent(ctx, ev, err)
	}
}

func (h *GarminHandler) failEvent(ctx context.Context, ev garminEvent, cause error) {
	idx := ev.Attempts
	if idx >= len(garminBackoff) {
		idx = len(garminBackoff) - 1
	}
	delay := garminBackoff[idx]
	var re *garminRecheckError
	if errors.As(cause, &re) {
		delay = re.Delay // 撤銷實測「token 仍有效」：依 +10 分／+1 小時／+6 小時重驗（不走一般退避表）
	}
	code := garminErrCode(cause)
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	attempts, dead, err := h.store.MarkGarminEventError(wctx, ev.ID, code, h.now().Add(delay), garminMaxAttempts)
	if err != nil {
		log.Warn().Err(err).Msg("garmin: mark event error failed")
		return
	}
	if attempts == 0 {
		return // 事件已被別的處理者完成（done／dead），不覆蓋也不排重試
	}
	log.Warn().Str("type", ev.EventType).Int("attempts", attempts).Bool("dead", dead).Str("code", code).Msg("garmin event failed")
	if dead {
		// 告警只帶事件 id、類型、錯誤代碼——不含使用者資料。
		h.alert("garmin_dead_event", "Garmin 事件處理失敗（dead）",
			"event_id="+ev.ID+" type="+ev.EventType+" attempts="+strconv.Itoa(attempts)+" code="+code)
		return
	}
	h.scheduleRetry(ev.ID, delay)
}

// --- 重試計時器（一次性，非週期；遺失時由掃描兜底）---

func (h *GarminHandler) scheduleRetry(id string, d time.Duration) {
	if h.draining.Load() {
		return
	}
	h.timersM.Lock()
	defer h.timersM.Unlock()
	if old := h.timers[id]; old != nil {
		old.Stop()
		delete(h.timers, id)
	}
	if len(h.timers) >= garminMaxTimers {
		return // 計時器太多：不排，讓掃描兜底
	}
	h.timers[id] = time.AfterFunc(d, func() {
		h.timersM.Lock()
		delete(h.timers, id)
		h.timersM.Unlock()
		h.retryOne(id)
	})
}

func (h *GarminHandler) stopTimers() {
	h.timersM.Lock()
	for id, t := range h.timers {
		t.Stop()
		delete(h.timers, id)
	}
	h.timersM.Unlock()
}

func (h *GarminHandler) retryOne(id string) {
	if h.draining.Load() || !h.workBegin() {
		return
	}
	defer h.workEnd()
	ctx, cancel := context.WithTimeout(dbwake.WithJob(context.Background(), "garmin_retry"), garminProcessBudget)
	defer cancel()
	ev, err := h.store.ClaimGarminEventByID(ctx, id)
	if err != nil || ev == nil {
		return
	}
	h.track([]string{ev.ID})
	defer h.untrack([]string{ev.ID})
	h.processEvents(ctx, []garminEvent{*ev})
}

// garminErrCode 把錯誤分類成短代碼（last_error／log／告警用；不含使用者資料，也不放原始錯誤文字）。
func garminErrCode(err error) string {
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errGarminBusy):
		return "busy"
	case errors.As(err, new(*garminRecheckError)):
		return "recheck"
	case errors.Is(err, errGarminTransient):
		return "transient"
	case errors.Is(err, errGarminConfig):
		return "config"
	case isTimeoutErr(err):
		return "timeout"
	case errors.As(err, &pgErr):
		return "db:" + pgErr.Code
	}
	if st := garminAPIStatusOf(err); st > 0 {
		return "api:" + strconv.Itoa(st)
	}
	if err.Error() == "panic" {
		return "panic"
	}
	return "error"
}

func isTimeoutErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// garminAPIStatusOf：呼叫 Garmin API 失敗時的 HTTP 狀態（G3 的 garminAPIError 提供；此處先給預設 0）。
var garminAPIStatusOf = func(err error) int { return 0 }
