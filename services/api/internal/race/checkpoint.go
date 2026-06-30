package race

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
)

// 打卡防弊參數
const (
	checkinMaxAccuracyM = 40.0 // 定位精度差於此 → 不接受打卡
	checkinMinTrackM    = 25.0 // 近期軌跡移動需達此長度才算「有移動佐證」，否則待審
)

func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// --- 型別 ---

type llPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
	T   int64   `json:"t"`
	Acc float64 `json:"acc"`
}

type checkinReq struct {
	Lat    float64   `json:"lat"`
	Lng    float64   `json:"lng"`
	Acc    float64   `json:"acc"`
	Points []llPoint `json:"points,omitempty"` // 近期前景追蹤軌跡（佐證實際移動到打卡點）
}

// CheckinResult 打卡結果
type CheckinResult struct {
	OK        bool    `json:"ok"`
	Status    string  `json:"status"` // verified | pending | already | out_of_range | low_accuracy | not_open
	DistanceM float64 `json:"distance_m"`
	Message   string  `json:"message"`
	Collected int     `json:"collected"`
	Required  int     `json:"required"`
	TaskDone  bool    `json:"task_done"`
}

// ActiveCheckpoint 會員當前可打卡的點（進行中賽事 + 已報名）
type ActiveCheckpoint struct {
	ID        string  `json:"id"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	RadiusM   int     `json:"radius_m"`
	Title     string  `json:"title,omitempty"`
	TaskID    string  `json:"task_id"`
	TaskTitle string  `json:"task_title,omitempty"`
	RaceID    string  `json:"race_id"`
	RaceTitle string  `json:"race_title,omitempty"`
	Checked   bool    `json:"checked"`
	Pending   bool    `json:"pending"`
}

// --- Repository ---

type checkpointInfo struct {
	Lat, Lng  float64
	RadiusM   int
	TaskID    string
	RaceID    string
	StartDate time.Time
	EndDate   time.Time
}

func (r *Repository) checkpointForCheckin(ctx context.Context, cpID string) (*checkpointInfo, error) {
	var c checkpointInfo
	err := r.db.QueryRow(ctx, `
		SELECT tc.lat, tc.lng, tc.radius_m, tc.task_id::text, rt.race_id::text, r.start_date, r.end_date
		FROM task_checkpoints tc
		JOIN race_tasks rt ON rt.id = tc.task_id
		JOIN races r ON r.id = rt.race_id
		WHERE tc.id = $1`, cpID).
		Scan(&c.Lat, &c.Lng, &c.RadiusM, &c.TaskID, &c.RaceID, &c.StartDate, &c.EndDate)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) userRegisteredInRace(ctx context.Context, userID, raceID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM registrations WHERE user_id=$1 AND race_id=$2 AND status<>'cancelled')`,
		userID, raceID).Scan(&ok)
	return ok, err
}

// insertCheckin 寫入打卡（每人每點唯一）。回傳是否為新打卡（false=已打過卡）。
func (r *Repository) insertCheckin(ctx context.Context, userID, cpID, raceID string,
	lat, lng, acc, distM float64, flagged bool, flagReason string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO checkpoint_checkins (user_id, checkpoint_id, race_id, lat, lng, accuracy, distance_m, flagged, flag_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))
		ON CONFLICT (user_id, checkpoint_id) DO NOTHING`,
		userID, cpID, raceID, lat, lng, acc, distM, flagged, flagReason)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// taskCheckinCounts 該會員在某 checkpoint 任務的已集點數（未標記）與總點數
func (r *Repository) taskCheckinCounts(ctx context.Context, userID, taskID string) (collected, required int, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM task_checkpoints WHERE task_id=$2),
		  (SELECT COUNT(*) FROM checkpoint_checkins ci
		     JOIN task_checkpoints tc ON tc.id = ci.checkpoint_id
		     WHERE tc.task_id=$2 AND ci.user_id=$1 AND NOT ci.flagged)`,
		userID, taskID).Scan(&required, &collected)
	return
}

// activeCheckpointsForUser 進行中賽事 + 已報名的所有打卡點（含當前會員打卡狀態）
func (r *Repository) activeCheckpointsForUser(ctx context.Context, userID string) ([]ActiveCheckpoint, error) {
	rows, err := r.db.Query(ctx, `
		SELECT tc.id::text, tc.lat, tc.lng, tc.radius_m, COALESCE(tc.title,''),
		       rt.id::text, COALESCE(rt.title,''), r.id::text, r.title,
		       (ci.id IS NOT NULL), COALESCE(ci.flagged, FALSE)
		FROM task_checkpoints tc
		JOIN race_tasks rt ON rt.id = tc.task_id AND rt.metric_type = 'checkpoint'
		JOIN races r ON r.id = rt.race_id AND r.review_status='approved'
		            AND NOW() BETWEEN r.start_date AND r.end_date
		JOIN registrations reg ON reg.race_id = r.id AND reg.user_id = $1 AND reg.status <> 'cancelled'
		LEFT JOIN checkpoint_checkins ci ON ci.checkpoint_id = tc.id AND ci.user_id = $1
		ORDER BY r.start_date, rt.display_order, tc.display_order`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActiveCheckpoint{}
	for rows.Next() {
		var c ActiveCheckpoint
		var checked, flagged bool
		if err := rows.Scan(&c.ID, &c.Lat, &c.Lng, &c.RadiusM, &c.Title,
			&c.TaskID, &c.TaskTitle, &c.RaceID, &c.RaceTitle, &checked, &flagged); err != nil {
			return nil, err
		}
		c.Checked = checked && !flagged
		c.Pending = checked && flagged
		out = append(out, c)
	}
	return out, rows.Err()
}

// userRaceCheckins 該會員在某賽事各打卡點狀態：map[checkpointID]flagged
func (r *Repository) userRaceCheckins(ctx context.Context, userID, raceID string) (map[string]bool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ci.checkpoint_id::text, ci.flagged
		FROM checkpoint_checkins ci
		JOIN task_checkpoints tc ON tc.id = ci.checkpoint_id
		JOIN race_tasks rt ON rt.id = tc.task_id
		WHERE rt.race_id=$1 AND ci.user_id=$2`, raceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var cp string
		var flagged bool
		if err := rows.Scan(&cp, &flagged); err != nil {
			return nil, err
		}
		m[cp] = flagged
	}
	return m, rows.Err()
}

// checkpointCompletion 賽事內每個 checkpoint 任務、各會員是否「集滿全部點（未標記）」
// 回傳 map[taskID]map[userID]bool
func (r *Repository) checkpointCompletion(ctx context.Context, raceID string) (map[string]map[string]bool, error) {
	required := map[string]int{}
	rr, err := r.db.Query(ctx, `
		SELECT tc.task_id::text, COUNT(*)
		FROM task_checkpoints tc JOIN race_tasks rt ON rt.id=tc.task_id
		WHERE rt.race_id=$1 GROUP BY tc.task_id`, raceID)
	if err != nil {
		return nil, err
	}
	for rr.Next() {
		var tid string
		var n int
		if err := rr.Scan(&tid, &n); err != nil {
			rr.Close()
			return nil, err
		}
		required[tid] = n
	}
	rr.Close()
	if err := rr.Err(); err != nil {
		return nil, err
	}

	done := map[string]map[string]bool{}
	cr, err := r.db.Query(ctx, `
		SELECT tc.task_id::text, ci.user_id::text, COUNT(*)
		FROM checkpoint_checkins ci
		JOIN task_checkpoints tc ON tc.id = ci.checkpoint_id
		JOIN race_tasks rt ON rt.id = tc.task_id
		WHERE rt.race_id=$1 AND NOT ci.flagged
		GROUP BY tc.task_id, ci.user_id`, raceID)
	if err != nil {
		return nil, err
	}
	defer cr.Close()
	for cr.Next() {
		var tid, uid string
		var n int
		if err := cr.Scan(&tid, &uid, &n); err != nil {
			return nil, err
		}
		if req := required[tid]; req > 0 && n >= req {
			if done[tid] == nil {
				done[tid] = map[string]bool{}
			}
			done[tid][uid] = true
		}
	}
	return done, cr.Err()
}

// --- Service ---

// CheckIn 會員對某打卡點打卡：伺服器重算距離 + 精度 + 時間窗 + 軌跡佐證
func (s *Service) CheckIn(ctx context.Context, userID, cpID string, req checkinReq) (*CheckinResult, error) {
	cp, err := s.repo.checkpointForCheckin(ctx, cpID)
	if err != nil {
		return nil, ErrRaceNotFound // 找不到打卡點
	}
	reg, err := s.repo.userRegisteredInRace(ctx, userID, cp.RaceID)
	if err != nil {
		return nil, err
	}
	if !reg {
		return &CheckinResult{Status: "not_open", Message: "尚未報名此賽事，無法打卡"}, nil
	}
	now := time.Now()
	if now.Before(cp.StartDate) || now.After(cp.EndDate) {
		return &CheckinResult{Status: "not_open", Message: "賽事未在進行中，無法打卡"}, nil
	}
	if req.Acc > 0 && req.Acc > checkinMaxAccuracyM {
		return &CheckinResult{Status: "low_accuracy", Message: fmt.Sprintf("定位精度不足（±%.0fm），請到空曠處再試", req.Acc)}, nil
	}

	distM := haversineMeters(req.Lat, req.Lng, cp.Lat, cp.Lng)
	radius := float64(cp.RadiusM)
	if distM > radius {
		return &CheckinResult{
			Status: "out_of_range", DistanceM: math.Round(distM),
			Message: fmt.Sprintf("不在打卡範圍內（距離約 %.0fm，需在 %.0fm 內）", distM, radius),
		}, nil
	}

	// 軌跡佐證：近期前景軌跡需呈現實際移動（避免單點偽造）
	trackLen := trackLength(req.Points)
	flagged := trackLen < checkinMinTrackM
	flagReason := ""
	if flagged {
		flagReason = "缺 GPS 軌跡佐證，待審核"
	}

	inserted, err := s.repo.insertCheckin(ctx, userID, cpID, cp.RaceID, req.Lat, req.Lng, req.Acc, distM, flagged, flagReason)
	if err != nil {
		return nil, err
	}
	collected, required, err := s.repo.taskCheckinCounts(ctx, userID, cp.TaskID)
	if err != nil {
		return nil, err
	}
	res := &CheckinResult{
		OK: true, DistanceM: math.Round(distM),
		Collected: collected, Required: required, TaskDone: required > 0 && collected >= required,
	}
	switch {
	case !inserted:
		res.Status = "already"
		res.Message = "你已在此打卡點打過卡"
	case flagged:
		res.Status = "pending"
		res.Message = "打卡成功，但缺移動軌跡佐證，將由主辦審核後計入"
	default:
		res.Status = "verified"
		res.Message = "打卡成功！"
		if res.TaskDone {
			res.Message = "打卡成功！本任務全部打卡點已集滿 🎉"
		}
	}
	return res, nil
}

// trackLength 近期軌跡的總移動長度（公尺）
func trackLength(pts []llPoint) float64 {
	total := 0.0
	for i := 1; i < len(pts); i++ {
		total += haversineMeters(pts[i-1].Lat, pts[i-1].Lng, pts[i].Lat, pts[i].Lng)
	}
	return total
}

// ActiveCheckpoints 會員當前可打卡的點清單
func (s *Service) ActiveCheckpoints(ctx context.Context, userID string) ([]ActiveCheckpoint, error) {
	return s.repo.activeCheckpointsForUser(ctx, userID)
}

// --- Handler ---

// CheckpointRouter 打卡相關路由（掛載在 /api/v1/checkpoints，需登入）
func (h *Handler) CheckpointRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.MyActiveCheckpoints)
	r.Post("/{checkpointID}/checkin", h.Checkin)
	return r
}

// GET /api/v1/checkpoints — 當前可打卡的點
func (h *Handler) MyActiveCheckpoints(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	list, err := h.svc.ActiveCheckpoints(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load checkpoints")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"checkpoints": list})
}

// POST /api/v1/checkpoints/{checkpointID}/checkin — 打卡
func (h *Handler) Checkin(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	cpID := chi.URLParam(r, "checkpointID")
	var req checkinReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	res, err := h.svc.CheckIn(r.Context(), userID, cpID, req)
	if err == ErrRaceNotFound {
		respondErr(w, http.StatusNotFound, "checkpoint not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to check in")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"result": res})
}
