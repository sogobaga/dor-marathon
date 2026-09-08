package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/notify"
)

var errGPSNotPending = errors.New("此筆已審核或不存在")

// errGPSApproveEnqueueFailed 核准當下推入活動佇列（XAdd）失敗——見 AdminApproveGPS 對這個錯誤的
// 處理（finding 1，2026-09-08 第二次稽核）：已用 revertGPSReview 把這筆復原成 pending，管理者可以
// 重新按核准鍵重試，不會被 errGPSNotPending 擋下。AdminApproveGPSHandler 對此回 503（可重試），
// 比照 gps.go UploadGPS 對 ErrGPSEnqueueFailed 的既有慣例。
var errGPSApproveEnqueueFailed = errors.New("入隊失敗，請稍後再試")

// GPSRunSummary 後台審核 / 個人歷史 共用。DistanceKm/AvgPaceS 對兩種用途都是 gps_runs 的「原始
// 值」（未套 GPS 距離校正，見 internal/gpscalib、migrations/154 對 gps_runs.distance_km 的欄位
// 註解：「原始有效距離，永不套校正」）——後台審核本就該看原始軌跡重算值。CalibDistanceKm/
// CalibFactor 只有本人歷史（ListUserGPS/GetUserGPSRun）才會填入：對抗式審查修正（medium-3
// finding）：本人歷史頁過去只顯示這個原始值，跟「已同步活動」列表/總里程用的校正後距離不一致
// （同一趟兩處數字對不上）；後台審核端維持原樣不填這兩欄（omitempty 不出現在 JSON）。
type GPSRunSummary struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id,omitempty"`
	UserName        string    `json:"user_name,omitempty"`
	UserEmail       string    `json:"user_email,omitempty"` // 僅 ListRecentGPS（回收流程，2026-09-03）填入
	DistanceKm      float64   `json:"distance_km"`
	DurationS       int       `json:"duration_s"`
	AvgPaceS        int       `json:"avg_pace_s"`
	PointCount      int       `json:"point_count"`
	Flagged         bool      `json:"flagged"`
	FlagReason      string    `json:"flag_reason,omitempty"`
	ReviewAction    string    `json:"review_action,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	EndedAt         time.Time `json:"ended_at"`
	Polyline        string    `json:"polyline,omitempty"`          // 僅詳情回傳（壓縮軌跡）
	KmPaces         []int     `json:"km_paces,omitempty"`          // 僅詳情回傳：每公里分段配速(秒/km)
	CalibDistanceKm *float64  `json:"calib_distance_km,omitempty"` // 校正後距離；僅本人歷史填入
	CalibFactor     *float64  `json:"calib_factor,omitempty"`      // 上傳當下生效的係數；僅本人歷史填入
	// CalibAvgPaceS：校正後平均配速（duration_s / CalibDistanceKm）；僅本人歷史填入。對抗式審查
	// 修正：AvgPaceS 欄位對兩種用途都固定是 gps_runs.avg_pace_s 原始值（見上方註解），但本人歷史頁
	// 距離已改顯示校正後（CalibDistanceKm），若配速仍顯示原始值，同一畫面會出現「距離×配速≠時間」
	// 且跟「已同步活動」（activities.avg_pace_s 是校正後）的同一趟配速對不上，兩處數字互相矛盾。
	CalibAvgPaceS *int `json:"calib_avg_pace_s,omitempty"`
	// ExcludedKm/ExcludedSegments：被排除區段（超速∪訊號斷點，見 internal/activity/gps.go
	// computeRun 的 gapInvalid/speedInvalid、migrations/166）的原始直線距離加總／段數；不套校正
	// 係數 k。本人歷史（ListUserGPS/GetUserGPSRun）填入實際值供前端顯示「⚠️ 已排除 Xkm」；後台
	// 審核（ListPendingGPS/GetGPSRun）目前 SELECT 未帶這兩欄，維持零值。
	ExcludedKm       float64 `json:"excluded_km"`
	ExcludedSegments int     `json:"excluded_segments"`
	// ActivityID/ExpAwarded：僅 ListRecentGPS（回收流程，2026-09-03 owner 決策新增，見 gps_recall.go）
	// 填入——依「同 user、source IS NULL、recorded_at=gps_runs.ended_at」慣例解析出對應 activities
	// 列；查無對應活動時皆為 nil（該趟從未產生活動，例如上傳當下已被標記）。刻意不用 omitempty：
	// 前端需要能區分「這欄位存在但是 null」與「後端沒回這欄位」。
	ActivityID *string `json:"activity_id"`
	ExpAwarded *bool   `json:"exp_awarded"`
}

// withCalibAvgPace 由 CalibDistanceKm/DurationS 算出校正後平均配速，只有本人歷史（有帶
// CalibDistanceKm）才會有值；沒有校正資料（CalibDistanceKm 為 nil，如舊資料或後台審核視角）維持
// nil，前端據此 fallback 回原始 AvgPaceS 顯示。
func (s *GPSRunSummary) withCalibAvgPace() *GPSRunSummary {
	if s.CalibDistanceKm != nil && *s.CalibDistanceKm > 0 && s.DurationS > 0 {
		v := int(float64(s.DurationS) / *s.CalibDistanceKm)
		s.CalibAvgPaceS = &v
	}
	return s
}

func (r *Repository) ListPendingGPS(ctx context.Context) ([]GPSRunSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id::text, g.user_id::text, COALESCE(u.name,''), g.distance_km, g.duration_s, g.avg_pace_s,
		       g.point_count, COALESCE(g.flag_reason,''), g.started_at, g.ended_at
		FROM gps_runs g JOIN users u ON u.id=g.user_id
		WHERE g.flagged AND g.reviewed_at IS NULL
		ORDER BY g.created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GPSRunSummary{}
	for rows.Next() {
		var s GPSRunSummary
		if err := rows.Scan(&s.ID, &s.UserID, &s.UserName, &s.DistanceKm, &s.DurationS, &s.AvgPaceS,
			&s.PointCount, &s.FlagReason, &s.StartedAt, &s.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) GetGPSRun(ctx context.Context, id string) (*GPSRunSummary, error) {
	var s GPSRunSummary
	err := r.db.QueryRow(ctx, `
		SELECT g.id::text, g.user_id::text, COALESCE(u.name,''), g.distance_km, g.duration_s, g.avg_pace_s,
		       g.point_count, g.flagged, COALESCE(g.flag_reason,''), COALESCE(g.review_action,''),
		       g.started_at, g.ended_at, COALESCE(g.polyline,'')
		FROM gps_runs g JOIN users u ON u.id=g.user_id WHERE g.id=$1`, id).
		Scan(&s.ID, &s.UserID, &s.UserName, &s.DistanceKm, &s.DurationS, &s.AvgPaceS,
			&s.PointCount, &s.Flagged, &s.FlagReason, &s.ReviewAction, &s.StartedAt, &s.EndedAt, &s.Polyline)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// gpsReviewResult reviewGPS 的回傳（核准需要的完整欄位，含 GPS 距離校正——見 internal/gpscalib）。
type gpsReviewResult struct {
	UserID, RaceID             string
	RawDistanceKm, CalibFactor float64 // gps_runs 的原始距離／上傳當下生效的校正係數
	CalibDistanceKm            float64 // = round2(RawDistanceKm × CalibFactor)；calib_distance_km 為 NULL 時退化成 RawDistanceKm（等同係數 1.0）
	DurationS, RawAvgPaceS     int
	EndedAt                    time.Time
}

// claimPendingGPS 取出待審且鎖定（回傳發活動所需欄位，含校正後距離——見 gpsReviewResult 註解）；
// 非 pending 回 errGPSNotPending
func (r *Repository) reviewGPS(ctx context.Context, id, action string) (gpsReviewResult, error) {
	var res gpsReviewResult
	var calibDistN *float64
	err := r.db.QueryRow(ctx, `
		UPDATE gps_runs SET reviewed_at=NOW(), review_action=$2
		WHERE id=$1 AND flagged AND reviewed_at IS NULL
		RETURNING user_id::text, COALESCE(race_id::text,''), distance_km, duration_s, avg_pace_s, ended_at,
		          calib_distance_km, calib_factor`,
		id, action).Scan(&res.UserID, &res.RaceID, &res.RawDistanceKm, &res.DurationS, &res.RawAvgPaceS, &res.EndedAt,
		&calibDistN, &res.CalibFactor)
	res.CalibDistanceKm = res.RawDistanceKm
	if calibDistN != nil {
		res.CalibDistanceKm = *calibDistN
	}
	return res, err
}

// revertGPSReview 把一筆「剛核准但推入活動佇列失敗」的 gps_runs 復原成待審（finding 1，2026-09-08
// 第二次稽核）：reviewGPS 的 UPDATE 是 WHERE reviewed_at IS NULL，一旦標成 approved，後續就算入隊
// 失敗也無法重新走 reviewGPS 再試一次（永遠命中 errGPSNotPending）。這裡把 reviewed_at/review_action
// 清空讓它重新變回「待審」，管理者可以在後台重按核准鍵重試。
//
// 安全條件：只在「目前確實是 approved」且「查無對應活動」時才復原——用查無對應活動當防線，
// 避免萬一 XAdd 其實已經送達（例如用戶端逾時但伺服器端實際成功）、worker 也已經消費建立了活動，
// 這裡卻把它打回 pending，之後又被重新核准一次造成重複入帳。配對慣例同 gps_recall.go 檔頭註解：
// 同 user、source IS NULL、recorded_at=gps_runs.ended_at。
func (r *Repository) revertGPSReview(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE gps_runs g SET reviewed_at = NULL, review_action = NULL
		WHERE g.id = $1 AND g.review_action = 'approved'
		  AND NOT EXISTS (
		    SELECT 1 FROM activities a
		    WHERE a.user_id = g.user_id AND a.source IS NULL AND date_trunc('second', a.recorded_at) = date_trunc('second', g.ended_at))`, id)
	return err
}

// markGPSEnqueued 標記一筆 gps_runs 已成功推入活動佇列（migrations/172 gps_runs.enqueued_at）。
// SaveGPSRun／AdminApproveGPS 在 XAdd 成功「之後」呼叫；RequeueUnenqueued（見 gps.go）依
// enqueued_at IS NULL 找出「已寫入 gps_runs 但因程序中斷而漏推入佇列」的孤兒列並補送。
func (r *Repository) markGPSEnqueued(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `UPDATE gps_runs SET enqueued_at = NOW() WHERE id = $1`, id)
	return err
}

// AdminApproveGPS 核准：標記 approved 並推入活動管線（記錄 + 里程 EXP）。
//
// 對抗式審查修正（low-2 finding）：改用「校正後」的距離/配速組出 ActivityEvent（並帶
// RawDistanceKm/CalibFactor），跟 SaveGPSRun 正常上傳路徑（未被標記那條）同一口徑——修正前這裡
// 永遠塞 gps_runs 的原始值、且不帶那兩個校正欄位，worker 會 fallback 成 raw=distance、factor=1.0，
// 導致同一位使用者「核准後才入帳」的這一趟跟其他趟的 calib_factor 對不上（例如係數 0.9781 的
// 使用者，這一趟核准後卻顯示 1.0000、里程也多算了）。
func (s *Service) AdminApproveGPS(ctx context.Context, id string) error {
	res, err := s.repo.reviewGPS(ctx, id, "approved")
	if err != nil {
		return errGPSNotPending
	}
	avgPaceS := res.RawAvgPaceS
	if res.CalibDistanceKm > 0 {
		avgPaceS = int(float64(res.DurationS) / res.CalibDistanceKm)
	}
	evt := ActivityEvent{
		UserID: res.UserID, RaceID: res.RaceID, DistanceKm: round2(res.CalibDistanceKm),
		DurationS: res.DurationS, AvgPaceS: avgPaceS, RecordedAt: res.EndedAt.Format(time.RFC3339),
		RawDistanceKm: round2(res.RawDistanceKm), CalibFactor: res.CalibFactor,
	}
	// finding 1（2026-09-08 第二次稽核）：XAdd 失敗過去被吃掉（連錯誤都不檢查）——這筆已經被標成
	// approved，卻沒有真的入隊，且 reviewGPS 的 WHERE reviewed_at IS NULL 讓它從此無法被重新核准
	// （永遠命中 errGPSNotPending，管理者重按核准鍵也沒用）。現在改成：檢查 XAdd 錯誤 → 失敗時
	// 告警 + revertGPSReview 復原成待審（讓管理者能重按核准重試）+ 回可重試錯誤（見
	// errGPSApproveEnqueueFailed／AdminApproveGPSHandler 對應的 503）。
	if err := enqueueActivityEvent(ctx, s.rdb, evt); err != nil {
		log.Error().Err(err).Str("gps_run_id", id).Str("user_id", res.UserID).
			Msg("AdminApproveGPS: XAdd enqueue failed, reverting review to pending")
		notify.Alert("gps_admin_approve_enqueue_failed", "GPS 後台核准推入活動佇列失敗（已嘗試復原為待審）",
			fmt.Sprintf("gps_run_id=%s user_id=%s err=%v", id, res.UserID, err))
		if revertErr := s.repo.revertGPSReview(ctx, id); revertErr != nil {
			log.Error().Err(revertErr).Str("gps_run_id", id).
				Msg("AdminApproveGPS: revertGPSReview also failed, stuck in approved with no activity")
			notify.Alert("gps_admin_approve_revert_failed", "GPS 核准復原也失敗，卡在 approved 且無對應活動，需人工處理",
				fmt.Sprintf("gps_run_id=%s user_id=%s revert_err=%v", id, res.UserID, revertErr))
		}
		return errGPSApproveEnqueueFailed
	}
	if err := s.repo.markGPSEnqueued(ctx, id); err != nil {
		// 非致命：入隊已成功，只是這個標記沒寫上；至多讓 RequeueUnenqueued 的孤兒列掃描白跑一次
		// （該掃描本身有「查無對應活動才補送」的防重複保護，見 ListUnenqueuedGPS 註解），不會重複發獎。
		log.Error().Err(err).Str("gps_run_id", id).
			Msg("AdminApproveGPS: markGPSEnqueued failed (non-fatal, enqueue itself succeeded)")
	}
	return nil
}

// AdminRejectGPS 駁回：標記 rejected，不發 EXP
func (s *Service) AdminRejectGPS(ctx context.Context, id string) error {
	if _, err := s.repo.reviewGPS(ctx, id, "rejected"); err != nil {
		return errGPSNotPending
	}
	return nil
}

// --- handlers ---

func (h *Handler) AdminListGPS(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.repo.ListPendingGPS(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"runs": rows})
}

func (h *Handler) AdminGetGPS(w http.ResponseWriter, r *http.Request) {
	run, err := h.svc.repo.GetGPSRun(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"run": run})
}

func (h *Handler) AdminApproveGPSHandler(w http.ResponseWriter, r *http.Request) {
	err := h.svc.AdminApproveGPS(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, errGPSApproveEnqueueFailed) {
		// finding 1：入隊失敗已於 AdminApproveGPS 內復原為待審，這裡回 503 讓後台前端可以提示
		// 「稍後再試」並允許重按核准鍵——不是 400（審核狀態本身沒有錯，只是入隊當下失敗）。
		http.Error(w, `{"error":"入隊失敗，請稍後再試"}`, http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) AdminRejectGPSHandler(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.AdminRejectGPS(r.Context(), chi.URLParam(r, "id")); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminRouter GPS 審核路由（掛 /admin/gps-runs）
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	// /recent 是固定路徑，必須（習慣上）在 /{id} 之前註冊，避免與動態參數路由混淆；chi 的樹狀路由
	// 本身會優先比對靜態節點，但仍照這個順序寫，一目了然、也不依賴實作細節。
	r.Get("/recent", h.AdminRecentGPSHandler) // 回收流程用清單（見 gps_recall.go，2026-09-03 owner 決策）
	r.Get("/", h.AdminListGPS)
	r.Get("/{id}", h.AdminGetGPS)
	r.Post("/{id}/approve", h.AdminApproveGPSHandler)
	r.Post("/{id}/reject", h.AdminRejectGPSHandler)
	r.Post("/{id}/recall", h.AdminRecallGPSHandler) // 已入帳活動的異常回收（見 gps_recall.go）
	return r
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	b, _ := json.Marshal(v)
	w.Write(b)
}
