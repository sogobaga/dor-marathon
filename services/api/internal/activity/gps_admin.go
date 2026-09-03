package activity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

var errGPSNotPending = errors.New("此筆已審核或不存在")

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
	b, _ := json.Marshal(evt)
	s.rdb.XAdd(ctx, &redis.XAddArgs{Stream: streamKey, Values: map[string]any{"data": string(b)}})
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
	if err := h.svc.AdminApproveGPS(r.Context(), chi.URLParam(r, "id")); err != nil {
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
	r.Get("/", h.AdminListGPS)
	r.Get("/{id}", h.AdminGetGPS)
	r.Post("/{id}/approve", h.AdminApproveGPSHandler)
	r.Post("/{id}/reject", h.AdminRejectGPSHandler)
	return r
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	b, _ := json.Marshal(v)
	w.Write(b)
}
