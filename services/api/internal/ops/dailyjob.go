package ops

// 每日視窗排程的共用骨架（每日營運報告 dailyreport.go、每日自檢 selfcheck.go；COROS GA 契約 §4）。
//
// 為什麼重寫（2026-10-03 擁有者項目 4：每日報告可靠度）：舊骨架在「認領」當下就先寫持久標記，之後才產生並
// 送出報告——Telegram 送失敗（網路、5xx）時，標記已寫、窗口內不會重試，當天就靜默漏報；每小時才 tick 一次，
// 08:00 之後最早 09:00 才有機會，但窗口只有 08:00–08:59，等於一天只有一次機會；成功也沒有任何 log。
// 新骨架：
//
//  1. 成功才標記：先佔跨實例鎖（advisory lock）與 in-memory「進行中」標記，產生＋送出都成功（Telegram 回 2xx）
//     之後才寫持久標記與 in-memory「已完成」標記；任何階段失敗 → 不寫持久標記、釋放 in-memory 標記，視窗內
//     過 10 分鐘再試（最多 5 次）。
//  2. 純時間 tick：每分鐘檢查「現在是不是台灣 08:00–08:59、今天還沒成功、重試時間到了沒」——這一步完全不碰
//     DB（Neon 才睡得著）；只有條件全部成立才去搶鎖、讀持久標記（一天最多幾次）。
//  3. 成功留 log（`ops dailyreport: sent`：報告日、字元數、耗時）；失敗訊息帶階段（lock／build／send／check）。
//
// 跨實例協調（鎖、持久標記）抽成 jobStore 介面：成功才標記、重試、鎖互斥這些邏輯因此能不連資料庫單元測試
// （見 dailyjob_test.go）；pgJobStore 是 Postgres 實作（advisory lock＋app_settings），整合測試見
// dailyjob_integration_test.go。

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

const (
	// dailyJobTickInterval 純時間 tick 的間隔（不碰 DB，見檔頭）。
	dailyJobTickInterval = time.Minute
	// dailyJobRetryEvery 失敗後多久再試（窗口 08:00–08:59 內）。
	dailyJobRetryEvery = 10 * time.Minute
	// dailyJobMaxAttempts 一天最多嘗試次數（含第一次）：08:00、:10、:20、:30、:40。
	dailyJobMaxAttempts = 5
	// dailyJobAttemptTimeout 單次嘗試（取鎖＋產生＋送出）的整體逾時。
	dailyJobAttemptTimeout = 5 * time.Minute
)

// jobOutcome 一次嘗試的結果。
type jobOutcome int

const (
	outcomeDone jobOutcome = iota // 成功（或持久標記顯示今天已由別的實例完成）→ 今天不再執行
	outcomeFail                   // 失敗 → 計一次嘗試、10 分鐘後重試
	outcomeSkip                   // 沒搶到鎖（別的實例在做）→ 不計嘗試，下一分鐘再看
)

// dailyJobState 單一排程在「本進程」的 in-memory 狀態（自帶互斥鎖）。
type dailyJobState struct {
	mu         sync.Mutex
	doneDate   string    // 已成功完成的台灣日（只有成功才設）
	inflight   string    // 正在執行的台灣日（避免同進程 tick 重入）
	attemptDay string    // attempts／nextTry 所屬的台灣日
	attempts   int       // 今天已失敗的次數
	nextTry    time.Time // 下一次可嘗試的時間（失敗後 +10 分鐘）
}

// begin 純記憶體閘門（不碰 DB）：今天已完成／進行中／嘗試已達上限／還沒到重試時間 → false。
// 回 true 時已把 inflight 標成今天，呼叫端結束時必須呼叫 finish。
func (s *dailyJobState) begin(today string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doneDate == today || s.inflight == today {
		return false
	}
	if s.attemptDay != today {
		s.attemptDay, s.attempts, s.nextTry = today, 0, time.Time{}
	}
	if s.attempts >= dailyJobMaxAttempts || now.Before(s.nextTry) {
		return false
	}
	s.inflight = today
	return true
}

// finish 結束一次嘗試並更新狀態；回傳今天累計的失敗次數。
func (s *dailyJobState) finish(today string, now time.Time, o jobOutcome) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight = ""
	switch o {
	case outcomeDone:
		s.doneDate = today
	case outcomeFail:
		s.attempts++
		s.nextTry = now.Add(dailyJobRetryEvery)
	}
	return s.attempts
}

// jobStore 每日排程的跨實例協調。
type jobStore interface {
	// TryLock 非阻塞取得跨實例鎖；ok=false＝別的實例持有。ok=true 時 release 必須被呼叫。
	TryLock(ctx context.Context, name string) (release func(), ok bool, err error)
	// ReadMarker 讀持久標記（不存在回 "", nil）。
	ReadMarker(ctx context.Context, key string) (string, error)
	// WriteMarker 寫持久標記。
	WriteMarker(ctx context.Context, key, value string) error
}

// pgJobStore Postgres 實作：專屬連線上的 pg_try_advisory_lock（session 鎖，連線關閉自動釋放）＋ app_settings。
type pgJobStore struct{ db *pgxpool.Pool }

func (s pgJobStore) TryLock(ctx context.Context, name string) (func(), bool, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, name).Scan(&got); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		// 用獨立 context：呼叫端 ctx 可能已逾時／取消，鎖仍然要釋放。
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(uctx, `SELECT pg_advisory_unlock(hashtext($1))`, name).Scan(&unlocked); err != nil {
			log.Warn().Err(err).Str("lock", name).Msg("ops: advisory unlock failed (released when this connection closes)")
		}
		conn.Release()
	}, true, nil
}

func (s pgJobStore) ReadMarker(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s pgJobStore) WriteMarker(ctx context.Context, key, value string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, key, value)
	return err
}

// dailyJob 一個每日視窗排程的定義。
type dailyJob struct {
	name       string // log 前綴：dailyreport／selfcheck
	lockName   string // advisory lock 名稱
	persistKey string // app_settings 持久標記 key（值＝已成功完成的台灣日）
	state      *dailyJobState
	// run 產生＋送出一次；err==nil 才算成功。stage 標明失敗階段（build／send／check…），寫進 log。
	run func(ctx context.Context) (stage string, err error)
	// onFail（可選）每次失敗後呼叫（attempt＝今天第幾次失敗；giveUp＝已達次數上限、今天不再重試）。
	onFail func(stage string, err error, attempt int, giveUp bool)
}

// runDailyJob 執行一輪「純時間 tick」要做的事（見檔頭）。now 是台灣時間（taiwanNow），由呼叫端注入。
func runDailyJob(ctx context.Context, store jobStore, job *dailyJob, now time.Time) {
	if !inSelfCheckWindow(now) {
		return // 純時間：窗口外完全不碰 DB
	}
	today := now.Format("2006-01-02")
	if !job.state.begin(today, now) {
		return
	}
	outcome := outcomeSkip
	stage, runErr := "", error(nil)
	defer func() {
		attempts := job.state.finish(today, now, outcome)
		if outcome == outcomeFail && job.onFail != nil {
			job.onFail(stage, runErr, attempts, attempts >= dailyJobMaxAttempts)
		}
	}()

	actx, cancel := context.WithTimeout(ctx, dailyJobAttemptTimeout)
	defer cancel()

	release, ok, err := store.TryLock(actx, job.lockName)
	if err != nil {
		stage, runErr, outcome = "lock", err, outcomeFail
		log.Error().Err(err).Str("stage", stage).Msg("ops " + job.name + ": acquire lock failed")
		return
	}
	if !ok {
		log.Debug().Msg("ops " + job.name + ": another instance holds the lock, skip")
		return
	}
	defer release()

	marker, err := store.ReadMarker(actx, job.persistKey)
	if err != nil {
		// 讀不到持久標記：視同今天還沒成功（寧可重送也不要漏整天），只警告。
		log.Warn().Err(err).Msg("ops " + job.name + ": read persistent marker failed, treating as not-done")
	}
	if marker == today {
		outcome = outcomeDone // 本實例重啟前（或別的實例）已成功送過：同步 in-memory 後跳過
		log.Debug().Msg("ops " + job.name + ": persistent marker says already done today, skip")
		return
	}

	stage, runErr = job.run(actx)
	if runErr != nil {
		outcome = outcomeFail
		return // 失敗：不寫持久標記；log 由 onFail 帶階段與次數
	}
	// 成功才標記（契約 §4.1）。寫標記失敗只警告——in-memory 已完成標記擋住本進程重送，
	// 最壞情況是進程在視窗內重啟、被別的路徑重送一次（比漏報好）。
	if err := store.WriteMarker(actx, job.persistKey, today); err != nil {
		log.Warn().Err(err).Msg("ops " + job.name + ": persist marker failed after success")
	}
	outcome = outcomeDone
}
