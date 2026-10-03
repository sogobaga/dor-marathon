package integration

// Garmin 直連後台管理 API（僅超級管理員）：摘要、事件元資料、事件重放、封鎖／解除封鎖。
//
// 資料紀律：事件只回「元資料」——絕不回 payload 本文（那是 Garmin 的使用者資料）、不回 Garmin userId 全碼
// （只回末 4 碼遮罩）、不回 token。DOR user id 可回（只有超管看得到）。錯誤回應只有固定代碼，不含內部細節。
// 沒有任何背景迴圈或 ticker：重放只在請求當下就地處理一次（與重試計時器同一條 claim → processEvents 路徑）。
//
// 掛載（cmd/api/main.go 的 admin 群組內，與其他後台模組同一組 RequireAuth／RequireAdmin／Audit 之下）：
//
//	r.With(adminAcctHandler.RequireSuper).Mount("/admin/garmin", garminHandler.AdminRouter())
//
// 路由（相對於掛載點）與回應：
//
//	GET  /summary                 DirectStats 的人數／筆數＋待處理／錯誤／dead／封鎖人數＋最後收到推送時間（只有數字）
//	GET  /events?status=&limit=   最近事件元資料（status：pending|done|error|dead，缺省＝全部；limit 預設 50、上限 200）
//	POST /events/{id}/replay      error／dead → pending（attempts 歸零、立即到期）並就地處理一次；冪等
//	POST /users/{userID}/block    body {"reason":"…"}（必填、≤120 字）→ BlockUser；冪等
//	POST /users/{userID}/unblock  → UnblockUser；冪等
//
// 防線：外層 RequireAuth／RequireAdmin／RequireSuper／Audit 由 main.go 的 admin 群組提供（Audit 會記錄這裡的 POST）；
// 路由內再自行驗證「登入者是超管」（fail-closed：未登入、查詢失敗、store 不支援一律拒絕），外層漏掛也不會裸露。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/dbwake"
)

const (
	garminAdminDefaultLimit = 50
	garminAdminMaxLimit     = 200
	garminAdminErrMaxRunes  = 120 // last_error 輸出上限（DB 欄位本身已是短代碼，這裡再保險）
	garminAdminResultMax    = 40
	garminAdminReasonMax    = 120 // garmin_blocklist.reason VARCHAR(120)
	garminAdminBodyMax      = 4 << 10

	garminAdminOpTimeout    = 15 * time.Second // 單純 DB 操作（重排事件等）：脫離請求取消，避免做一半
	garminAdminBlockBudget  = 60 * time.Second // 封鎖：含向 Garmin 撤銷註冊（≤15 秒）與清除（鎖等待 ≤8 秒）
	garminAdminKickBudget   = garminProcessBudget
	garminAdminMaskedPrefix = "****"
)

// garminAdminReplayWait：重放後最多等多久讓就地處理完成，好把處理後的狀態一併回給呼叫端（逾時就回目前狀態，處理仍在背景進行）。
// 測試可覆寫。
var garminAdminReplayWait = 4 * time.Second

// --- 資料存取（*Repository 實作；單元測試以記憶體假物件取代）---

// garminAdminEvent：integration_events 的一列「元資料」（刻意沒有 payload 欄位）。
type garminAdminEvent struct {
	ID             string
	EventType      string
	Status         string
	Attempts       int
	Result         string
	LastError      string
	ReceivedAt     time.Time
	ProcessedAt    *time.Time
	NextAttemptAt  time.Time
	ProviderUserID string // Garmin userId 原值：只在伺服器內存在，輸出前一律遮罩
	UserID         string // 對得到直連連線時的 DOR user id（連線已清除則為空字串）
}

// garminRequeue：RequeueGarminEvent 的結果。
type garminRequeue struct {
	Found    bool
	Requeued bool
	Status   string // Requeued＝重排前的狀態（error／dead）；未重排＝目前狀態（pending／done）
}

// garminAdminCounts：摘要用的事件表與封鎖清單計數（只有數字）。
type garminAdminCounts struct {
	Pending        int
	Error          int
	Done           int
	Dead           int
	Blocked        int
	LastReceivedAt *time.Time // 保存期限內最近一筆事件的落地時間（任何類型）
}

type garminAdminStore interface {
	IsGarminSuperAdmin(ctx context.Context, userID string) (bool, error)
	GarminUserExists(ctx context.Context, userID string) (bool, error)
	ListGarminEventsAdmin(ctx context.Context, status string, limit int) ([]garminAdminEvent, error)
	GetGarminEventAdmin(ctx context.Context, id string) (*garminAdminEvent, error)
	RequeueGarminEvent(ctx context.Context, id string) (garminRequeue, error)
	GarminAdminCounts(ctx context.Context) (garminAdminCounts, error)
}

var _ garminAdminStore = (*Repository)(nil)

// garminAdminEventSelect：後台事件查詢（元資料＋對應的 DOR user id）。⚠️ 不得選取 payload（單元測試守住）。
const garminAdminEventSelect = `
	SELECT e.id::text, e.event_type, e.status, e.attempts, COALESCE(e.result,''), COALESCE(e.last_error,''),
	       e.received_at, e.processed_at, e.next_attempt_at, e.provider_user_id, COALESCE(ui.user_id::text,'')
	  FROM integration_events e
	  LEFT JOIN LATERAL (SELECT user_id FROM user_integrations
	                      WHERE provider='garmin' AND via='direct' AND provider_user_id <> '' AND provider_user_id = e.provider_user_id
	                      LIMIT 1) ui ON true`

func scanGarminAdminEvent(row pgx.Row) (*garminAdminEvent, error) {
	var e garminAdminEvent
	if err := row.Scan(&e.ID, &e.EventType, &e.Status, &e.Attempts, &e.Result, &e.LastError,
		&e.ReceivedAt, &e.ProcessedAt, &e.NextAttemptAt, &e.ProviderUserID, &e.UserID); err != nil {
		return nil, err
	}
	return &e, nil
}

// IsGarminSuperAdmin：這個 DOR 帳號是不是超級管理員（users.role='admin' 且 is_super_admin）。
func (r *Repository) IsGarminSuperAdmin(ctx context.Context, userID string) (bool, error) {
	var super bool
	err := r.db.QueryRow(ctx, `SELECT COALESCE(is_super_admin, FALSE) FROM users WHERE id=$1::uuid AND role='admin'`, userID).Scan(&super)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return super, err
}

// GarminUserExists：DOR 帳號是否存在（封鎖前確認，避免手誤把不存在的 UUID 寫進封鎖清單）。
func (r *Repository) GarminUserExists(ctx context.Context, userID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1::uuid)`, userID).Scan(&ok)
	return ok, err
}

// ListGarminEventsAdmin：最近事件元資料（新到舊）。status 為空＝全部。
func (r *Repository) ListGarminEventsAdmin(ctx context.Context, status string, limit int) ([]garminAdminEvent, error) {
	if limit <= 0 {
		limit = garminAdminDefaultLimit
	}
	if limit > garminAdminMaxLimit {
		limit = garminAdminMaxLimit
	}
	rows, err := r.db.Query(ctx, garminAdminEventSelect+`
		WHERE e.provider='garmin' AND ($1::text = '' OR e.status = $1::text)
		ORDER BY e.received_at DESC, e.id
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []garminAdminEvent{}
	for rows.Next() {
		e, err := scanGarminAdminEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// GetGarminEventAdmin：單筆事件元資料；不存在回 (nil, nil)。
func (r *Repository) GetGarminEventAdmin(ctx context.Context, id string) (*garminAdminEvent, error) {
	e, err := scanGarminAdminEvent(r.db.QueryRow(ctx, garminAdminEventSelect+` WHERE e.id=$1::uuid AND e.provider='garmin'`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

// RequeueGarminEvent：把 error／dead 的事件重排成 pending（attempts 歸零、立即到期、清掉舊的錯誤與結果）。
// 單一交易、鎖住該列：並發重放同一筆時只有一個會真的重排，其餘看到的是 pending／done（冪等）。
func (r *Repository) RequeueGarminEvent(ctx context.Context, id string) (garminRequeue, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return garminRequeue{}, err
	}
	defer tx.Rollback(context.Background())
	var cur string
	err = tx.QueryRow(ctx, `SELECT status FROM integration_events WHERE id=$1::uuid AND provider='garmin' FOR UPDATE`, id).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return garminRequeue{}, nil
	}
	if err != nil {
		return garminRequeue{}, err
	}
	if cur != garminStError && cur != garminStDead {
		return garminRequeue{Found: true, Status: cur}, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE integration_events
		   SET status='pending', attempts=0, next_attempt_at=now(), result=NULL, last_error=NULL, processed_at=NULL
		 WHERE id=$1::uuid AND provider='garmin'`, id); err != nil {
		return garminRequeue{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return garminRequeue{}, err
	}
	return garminRequeue{Found: true, Requeued: true, Status: cur}, nil
}

// GarminAdminCounts：事件表各狀態筆數、封鎖清單筆數、最近一筆事件的落地時間（純 DB、只有數字與時間）。
func (r *Repository) GarminAdminCounts(ctx context.Context) (garminAdminCounts, error) {
	var c garminAdminCounts
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='pending'),
		       count(*) FILTER (WHERE status='error'),
		       count(*) FILTER (WHERE status='done'),
		       count(*) FILTER (WHERE status='dead'),
		       max(received_at),
		       (SELECT count(*) FROM garmin_blocklist)
		  FROM integration_events WHERE provider='garmin'`).
		Scan(&c.Pending, &c.Error, &c.Done, &c.Dead, &c.LastReceivedAt, &c.Blocked)
	return c, err
}

// --- 輸出（DTO）與遮罩 ---

// garminMaskUserID：Garmin userId 只露末 4 碼（太短就整個遮掉，避免短 id 被完整還原）。
func garminMaskUserID(id string) string {
	r := []rune(strings.TrimSpace(id))
	switch {
	case len(r) == 0:
		return ""
	case len(r) < 8:
		return garminAdminMaskedPrefix
	}
	return garminAdminMaskedPrefix + string(r[len(r)-4:])
}

// garminRedactID：保險——輸出文字若意外含有該 Garmin userId 全碼，換成遮罩值。
func garminRedactID(s, id string) string {
	id = strings.TrimSpace(id)
	if s == "" || len(id) < 8 {
		return s
	}
	return strings.ReplaceAll(s, id, garminMaskUserID(id))
}

// garminAdminEventDTO：GET /events 與重放回應中的事件。沒有 payload、沒有 dedupe_key（內含 Garmin 的活動識別碼）。
type garminAdminEventDTO struct {
	ID            string     `json:"id"`
	EventType     string     `json:"event_type"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	Result        string     `json:"result"`
	LastError     string     `json:"last_error"`
	ReceivedAt    time.Time  `json:"received_at"`
	ProcessedAt   *time.Time `json:"processed_at"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	UserID        string     `json:"user_id"`         // DOR user id；對不到連線（已清除）為空字串
	GarminUserRef string     `json:"garmin_user_ref"` // Garmin userId 遮罩（末 4 碼）
}

func garminAdminEventView(e garminAdminEvent) garminAdminEventDTO {
	var processed *time.Time
	if e.ProcessedAt != nil {
		t := e.ProcessedAt.UTC()
		processed = &t
	}
	clean := func(s string, maxRunes int) string {
		return truncateRunes(garminCleanString(garminRedactID(s, e.ProviderUserID)), maxRunes)
	}
	return garminAdminEventDTO{
		ID: e.ID, EventType: e.EventType, Status: e.Status, Attempts: e.Attempts,
		Result:        clean(e.Result, garminAdminResultMax),
		LastError:     clean(e.LastError, garminAdminErrMaxRunes),
		ReceivedAt:    e.ReceivedAt.UTC(),
		ProcessedAt:   processed,
		NextAttemptAt: e.NextAttemptAt.UTC(),
		UserID:        e.UserID,
		GarminUserRef: garminMaskUserID(e.ProviderUserID),
	}
}

type garminAdminConnCounts struct {
	Connected   int `json:"connected"`
	Active24h   int `json:"active_24h"`
	NeedsReauth int `json:"needs_reauth"`
	Paused      int `json:"paused"`
	Stale3d     int `json:"stale_3d"`
}

type garminAdminEventCounts struct {
	Received24h  int `json:"received_24h"`
	Imported24h  int `json:"imported_24h"`
	Pending      int `json:"pending"`
	PendingStale int `json:"pending_stale"` // pending／error 且已逾期超過 10 分鐘
	Error        int `json:"error"`
	Done         int `json:"done"`
	Dead         int `json:"dead"`
}

type garminAdminSummary struct {
	Provider           string                 `json:"provider"`
	Connections        garminAdminConnCounts  `json:"connections"`
	Events             garminAdminEventCounts `json:"events"`
	UnknownUser24h     int                    `json:"unknown_user_24h"`
	Blocked            int                    `json:"blocked"`
	LastPushReceivedAt *time.Time             `json:"last_push_received_at"`
	Notes              []string               `json:"notes"`
}

// --- 路由與防線 ---

// adminStore：admin 專用的資料存取；h.store 不支援（理論上只有測試的假 store）時回 false（fail-closed）。
func (h *GarminHandler) adminStore() (garminAdminStore, bool) {
	st, ok := h.store.(garminAdminStore)
	return st, ok
}

func garminNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// adminGuard：登入者必須是超管（fail-closed）。與外層 adminAcctHandler.RequireSuper 重複是刻意的（縱深防禦）。
func (h *GarminHandler) adminGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
		if uid == "" || !isValidUUID(uid) {
			respondErr(w, http.StatusUnauthorized, "login_required")
			return
		}
		st, ok := h.adminStore()
		if !ok {
			respondErr(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		super, err := st.IsGarminSuperAdmin(r.Context(), uid)
		if err != nil {
			log.Error().Err(err).Msg("garmin admin: super-admin check failed")
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if !super {
			respondErr(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminRouter 掛在 /admin/garmin（見檔頭）。限流（若 main.go 注入 RateLimit）一律掛在守衛之後，依登入者維度計算。
func (h *GarminHandler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(garminNoStore)
	r.Use(h.adminGuard)
	read := h.limit("garmin_admin_read", 120, time.Minute)
	r.With(read).Get("/summary", h.AdminSummary)
	r.With(read).Get("/events", h.AdminListEvents)
	r.With(h.limit("garmin_admin_replay", 30, time.Minute)).Post("/events/{id}/replay", h.AdminReplayEvent)
	block := h.limit("garmin_admin_block", 20, time.Minute)
	r.With(block).Post("/users/{userID}/block", h.AdminBlockUser)
	r.With(block).Post("/users/{userID}/unblock", h.AdminUnblockUser)
	return r
}

// garminAdminUUIDParam 取路徑參數並驗證為標準格式 UUID（回傳小寫）。
func garminAdminUUIDParam(r *http.Request, name string) (string, bool) {
	v := strings.TrimSpace(chi.URLParam(r, name))
	if !isValidUUID(v) {
		return "", false
	}
	return strings.ToLower(v), true
}

func garminAdminDetached(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), d)
}

func garminAdminActor(r *http.Request) string {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	return uid
}

// --- GET /summary ---

// AdminSummary：DirectStats 的人數／筆數（與日報同源）＋待處理／錯誤／dead／封鎖人數＋最後收到推送時間。只有數字。
func (h *GarminHandler) AdminSummary(w http.ResponseWriter, r *http.Request) {
	st, ok := h.adminStore()
	if !ok {
		respondErr(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	ds, err := h.DirectStats(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("garmin admin: direct stats failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	ac, err := st.GarminAdminCounts(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("garmin admin: event counts failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	var last *time.Time
	if ac.LastReceivedAt != nil {
		t := ac.LastReceivedAt.UTC()
		last = &t
	}
	notes := ds.Notes
	if notes == nil {
		notes = []string{}
	}
	respondJSON(w, http.StatusOK, garminAdminSummary{
		Provider: ds.Provider,
		Connections: garminAdminConnCounts{
			Connected: ds.Connected, Active24h: ds.Active24h, NeedsReauth: ds.NeedsReauth, Paused: ds.Paused, Stale3d: ds.Stale3d,
		},
		Events: garminAdminEventCounts{
			Received24h: ds.Events24h, Imported24h: ds.Imported24h, Pending: ac.Pending, PendingStale: ds.PendingStale,
			Error: ac.Error, Done: ac.Done, Dead: ac.Dead,
		},
		UnknownUser24h:     ds.UnknownUser24h,
		Blocked:            ac.Blocked,
		LastPushReceivedAt: last,
		Notes:              notes,
	})
}

// --- GET /events ---

func garminAdminStatusOK(s string) bool {
	switch s {
	case garminStPending, garminStDone, garminStError, garminStDead:
		return true
	}
	return false
}

// AdminListEvents：最近事件元資料。status／limit 先驗證再查（limit 超過上限就夾到上限）。
func (h *GarminHandler) AdminListEvents(w http.ResponseWriter, r *http.Request) {
	st, ok := h.adminStore()
	if !ok {
		respondErr(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	if status != "" && !garminAdminStatusOK(status) {
		respondErr(w, http.StatusBadRequest, "invalid_status")
		return
	}
	limit := garminAdminDefaultLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			respondErr(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = n
	}
	if limit > garminAdminMaxLimit {
		limit = garminAdminMaxLimit
	}
	rows, err := st.ListGarminEventsAdmin(r.Context(), status, limit)
	if err != nil {
		log.Error().Err(err).Msg("garmin admin: list events failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	out := make([]garminAdminEventDTO, 0, len(rows))
	for _, e := range rows {
		out = append(out, garminAdminEventView(e))
	}
	respondJSON(w, http.StatusOK, map[string]any{"events": out, "count": len(out), "limit": limit, "status": status})
}

// --- POST /events/{id}/replay ---

// garminKick：一次就地處理的進度。claimed 在領取嘗試結束時送出一個值（true＝領到並開始處理，false＝沒有可領的事件）；
// done 在整個處理結束後關閉。
type garminKick struct {
	claimed chan bool
	done    chan struct{}
}

// adminKickEvent 領取並就地處理單一事件（與重試計時器同一條路：ClaimGarminEventByID → processEvents）。
// 關閉中（Drain 已開始）回 nil——事件維持原狀，新行程的啟動掃描會接手。
// 領取只認「已到期」的事件（pending／error 且租約過了），所以別的處理者正在處理的事件不會被重複處理。
func (h *GarminHandler) adminKickEvent(id string) *garminKick {
	if h.draining.Load() || !h.workBegin() {
		return nil
	}
	k := &garminKick{claimed: make(chan bool, 1), done: make(chan struct{})}
	go func() {
		defer close(k.done)
		defer h.workEnd()
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().Interface("panic", rec).Msg("garmin admin replay panic")
				select { // 領取結果還沒送出就 panic：補送 false，別讓呼叫端空等到逾時
				case k.claimed <- false:
				default:
				}
			}
		}()
		ctx, cancel := context.WithTimeout(dbwake.WithJob(context.Background(), "garmin_admin_replay"), garminAdminKickBudget)
		defer cancel()
		ev, err := h.store.ClaimGarminEventByID(ctx, id)
		if err != nil || ev == nil {
			k.claimed <- false
			return
		}
		k.claimed <- true
		h.track([]string{ev.ID})
		defer h.untrack([]string{ev.ID})
		h.processEvents(ctx, []garminEvent{*ev})
	}()
	return k
}

// AdminReplayEvent：重排 error／dead 事件並就地處理一次。冪等：
//   - error／dead → requeued（attempts 歸零、立即到期）並嘗試處理；
//   - 已是 pending → already_pending：不改狀態，只順手嘗試處理「已到期」者（租約中的事件領不到，不會重複處理）；
//   - done → already_done：什麼都不做。
//
// processing_started＝這次請求真的領到事件並開始處理；event＝處理後（或等待逾時時）的最新狀態。
func (h *GarminHandler) AdminReplayEvent(w http.ResponseWriter, r *http.Request) {
	st, ok := h.adminStore()
	if !ok {
		respondErr(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	id, ok := garminAdminUUIDParam(r, "id")
	if !ok {
		respondErr(w, http.StatusBadRequest, "invalid_id")
		return
	}
	ctx, cancel := garminAdminDetached(r, garminAdminOpTimeout)
	defer cancel()
	rq, err := st.RequeueGarminEvent(ctx, id)
	if err != nil {
		log.Error().Err(err).Msg("garmin admin: requeue event failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if !rq.Found {
		respondErr(w, http.StatusNotFound, "event_not_found")
		return
	}
	outcome := "requeued"
	switch {
	case rq.Requeued:
	case rq.Status == garminStDone:
		outcome = "already_done"
	default:
		outcome = "already_pending"
	}

	started := false
	if outcome != "already_done" {
		if k := h.adminKickEvent(id); k != nil {
			t := time.NewTimer(garminAdminReplayWait)
			select {
			case started = <-k.claimed:
			case <-t.C:
			case <-r.Context().Done():
			}
			if started {
				select {
				case <-k.done:
				case <-t.C:
				case <-r.Context().Done():
				}
			}
			t.Stop()
		}
	}
	resp := map[string]any{"outcome": outcome, "processing_started": started}
	if rq.Requeued {
		resp["previous_status"] = rq.Status
	}
	if ev, gerr := st.GetGarminEventAdmin(ctx, id); gerr == nil && ev != nil {
		resp["event"] = garminAdminEventView(*ev)
	}
	log.Info().Str("admin", prefix8(garminAdminActor(r))).Str("event", prefix8(id)).Str("outcome", outcome).Bool("started", started).
		Msg("garmin admin: event replay")
	respondJSON(w, http.StatusOK, resp)
}

// --- POST /users/{userID}/block｜unblock ---

// AdminBlockUser：後台封鎖（BlockUser：先寫封鎖清單、仍呼叫 DELETE registration、再清除；冪等）。reason 必填（≤120 字）。
// 執行期間脫離請求取消：封鎖是合規動作，不能因為管理者關掉頁面就停在一半（封鎖清單本來就是第一步，重送也安全）。
func (h *GarminHandler) AdminBlockUser(w http.ResponseWriter, r *http.Request) {
	st, ok := h.adminStore()
	if !ok {
		respondErr(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	userID, ok := garminAdminUUIDParam(r, "userID")
	if !ok {
		respondErr(w, http.StatusBadRequest, "invalid_id")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	// 空 body（io.EOF）視同沒給 reason；其餘解析錯誤（壞 JSON、型別不符、超過 4KB）一律 invalid_body。
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, garminAdminBodyMax)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		respondErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	reason := garminCleanString(req.Reason)
	if reason == "" {
		respondErr(w, http.StatusBadRequest, "reason_required")
		return
	}
	if utf8.RuneCountInString(reason) > garminAdminReasonMax {
		respondErr(w, http.StatusBadRequest, "reason_too_long")
		return
	}
	ctx, cancel := garminAdminDetached(r, garminAdminBlockBudget)
	defer cancel()
	exists, err := st.GarminUserExists(ctx, userID)
	if err != nil {
		log.Error().Err(err).Msg("garmin admin: user lookup failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if !exists {
		respondErr(w, http.StatusNotFound, "user_not_found")
		return
	}
	already, _ := h.store.GarminBlocked(ctx, userID, "") // 只供回應提示；查詢失敗當作 false

	adminID := garminAdminActor(r)
	res, err := h.BlockUser(ctx, userID, reason, adminID)
	if err != nil {
		// 封鎖清單是第一步：到這裡失敗時封鎖多半已生效、清除可能只做了一半；BlockUser 冪等，重送即可補完。
		log.Error().Err(err).Str("admin", prefix8(adminID)).Str("user", prefix8(userID)).Msg("garmin admin: block failed (idempotent, safe to retry)")
		if errors.Is(err, errGarminBusy) {
			respondErr(w, http.StatusConflict, "user_busy")
			return
		}
		respondErr(w, http.StatusInternalServerError, "block_failed")
		return
	}
	log.Info().Str("admin", prefix8(adminID)).Str("user", prefix8(userID)).Bool("registration_deleted", res.RegistrationDeleted).
		Bool("had_connection", res.Purge.HadConnection).Msg("garmin admin: user blocked")
	respondJSON(w, http.StatusOK, map[string]any{
		"ok": true, "blocked": true, "already_blocked": already,
		"registration_deleted": res.RegistrationDeleted,
		"had_connection":       res.Purge.HadConnection,
		"deleted_activities":   res.Purge.DeletedActivities,
		"deleted_events":       res.Purge.DeletedEvents,
		"anonymized_events":    res.Purge.AnonymizedEvents,
	})
}

// AdminUnblockUser：解除封鎖（冪等；不要求使用者仍存在）。unblocked＝這次真的移除了封鎖列。
func (h *GarminHandler) AdminUnblockUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.adminStore(); !ok {
		respondErr(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	userID, ok := garminAdminUUIDParam(r, "userID")
	if !ok {
		respondErr(w, http.StatusBadRequest, "invalid_id")
		return
	}
	ctx, cancel := garminAdminDetached(r, garminAdminOpTimeout)
	defer cancel()
	removed, err := h.UnblockUser(ctx, userID)
	if err != nil {
		log.Error().Err(err).Str("user", prefix8(userID)).Msg("garmin admin: unblock failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	log.Info().Str("admin", prefix8(garminAdminActor(r))).Str("user", prefix8(userID)).Bool("removed", removed).Msg("garmin admin: user unblocked")
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "unblocked": removed})
}
