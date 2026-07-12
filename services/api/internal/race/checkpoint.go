package race

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
)

// 打卡防弊參數
const (
	checkinMaxAccuracyM = 40.0 // 定位精度差於此 → 不接受打卡
	// 速度合理性：兩打卡點中心的直線距離÷時間 隱含的移動速度上限 ≈7.7m/s（100m 至少需 13 秒）。
	// 快於此（例：相隔 100m 卻只差 1 秒）視為異常 → 待審核，不無腦通過。
	checkinMinSecPerMeter = 13.0 / 100.0
	checkinMinMoveM       = 50.0 // 兩打卡點中心距離小於此 → 太近、不做速度判定（避免叢集打卡點誤判）
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

// PendingCheckin 後台待審核的打卡（flagged=true）：含會員、打卡點、位置與佐證資訊
type PendingCheckin struct {
	ID             string    `json:"id"`
	UserName       string    `json:"user_name"`
	UserEmail      string    `json:"user_email"`
	CheckpointID   string    `json:"checkpoint_id"`
	CheckpointName string    `json:"checkpoint_name"`
	TaskTitle      string    `json:"task_title"`
	Lat            float64   `json:"lat"`     // 會員打卡當下位置
	Lng            float64   `json:"lng"`
	CpLat          float64   `json:"cp_lat"`  // 打卡點座標
	CpLng          float64   `json:"cp_lng"`
	RadiusM        int       `json:"radius_m"`
	Accuracy       float64   `json:"accuracy"`
	DistanceM      float64   `json:"distance_m"`
	FlagReason     string    `json:"flag_reason"`
	CheckedAt      time.Time `json:"checked_at"`
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

// prevCheckin 取該會員在「同一賽事」最近一次打卡的『打卡點中心座標』與『距今秒數』（皆用 DB 時鐘，供速度稽核）。
// 用打卡點中心而非使用者回報座標→避免 GPS 抖動誤判；限同賽事→跨賽事的前次打卡不干擾。
// found=false：同賽事尚無前次打卡（正常放行）。err!=nil：查詢異常，呼叫端應保守處理（勿逕自放行）。
func (r *Repository) prevCheckin(ctx context.Context, userID, raceID string) (lat, lng, gapS float64, found bool, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT tc.lat, tc.lng, EXTRACT(EPOCH FROM (NOW() - ci.checked_at))
		FROM checkpoint_checkins ci
		JOIN task_checkpoints tc ON tc.id = ci.checkpoint_id
		WHERE ci.user_id=$1 AND ci.race_id=$2
		ORDER BY ci.checked_at DESC LIMIT 1`, userID, raceID).
		Scan(&lat, &lng, &gapS)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, 0, false, err
	}
	return lat, lng, gapS, true, nil
}

// checkinFlagged 查某會員某打卡點現有打卡是否仍為待審(flagged)；exists=false 表示尚無該筆。
func (r *Repository) checkinFlagged(ctx context.Context, userID, cpID string) (flagged, exists bool) {
	if err := r.db.QueryRow(ctx,
		`SELECT flagged FROM checkpoint_checkins WHERE user_id=$1 AND checkpoint_id=$2`, userID, cpID).Scan(&flagged); err != nil {
		return false, false
	}
	return flagged, true
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

// listPendingCheckins 某賽事所有待審核（flagged）打卡
func (r *Repository) listPendingCheckins(ctx context.Context, raceID string) ([]PendingCheckin, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ci.id::text, u.name, u.email, ci.checkpoint_id::text,
		       COALESCE(tc.title,''), COALESCE(rt.title,''),
		       ci.lat, ci.lng, tc.lat, tc.lng, tc.radius_m,
		       COALESCE(ci.accuracy,0), COALESCE(ci.distance_m,0), COALESCE(ci.flag_reason,''), ci.checked_at
		FROM checkpoint_checkins ci
		JOIN users u ON u.id = ci.user_id
		JOIN task_checkpoints tc ON tc.id = ci.checkpoint_id
		JOIN race_tasks rt ON rt.id = tc.task_id
		WHERE ci.race_id = $1 AND ci.flagged
		ORDER BY ci.checked_at ASC`, raceID)
	if err != nil {
		return nil, fmt.Errorf("list pending checkins: %w", err)
	}
	defer rows.Close()
	out := []PendingCheckin{}
	for rows.Next() {
		var p PendingCheckin
		if err := rows.Scan(&p.ID, &p.UserName, &p.UserEmail, &p.CheckpointID,
			&p.CheckpointName, &p.TaskTitle, &p.Lat, &p.Lng, &p.CpLat, &p.CpLng, &p.RadiusM,
			&p.Accuracy, &p.DistanceM, &p.FlagReason, &p.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// approveCheckin 核准：flagged=false → 計入集章。回傳是否有更新到（找不到/已非待審=false）
func (r *Repository) approveCheckin(ctx context.Context, checkinID string) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE checkpoint_checkins SET flagged=false, flag_reason=NULL WHERE id=$1 AND flagged`, checkinID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// rejectCheckin 退回：刪除該筆待審打卡（會員可重新打卡）。
func (r *Repository) rejectCheckin(ctx context.Context, checkinID string) (bool, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM checkpoint_checkins WHERE id=$1 AND flagged`, checkinID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// --- Service ---

// CheckIn 會員對某打卡點打卡：伺服器重算距離 + 精度 + 時間窗 + 速度合理性稽核（免審核，異常才待審）
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

	// 速度合理性稽核：與同帳號「同賽事上一次打卡」的『打卡點中心』比對——直線距離÷時間隱含的
	// 移動速度過快（例：相隔 100m 卻只差 1 秒）→ 判為異常、待主辦審核；正常則在範圍內即免審核成功。
	flagged, flagReason := false, ""
	if pLat, pLng, gapS, hasPrev, perr := s.repo.prevCheckin(ctx, userID, cp.RaceID); perr != nil {
		flagged, flagReason = true, "打卡稽核查詢異常，暫轉人工審核" // 查詢異常 → 保守起見不放行
	} else if hasPrev {
		moveM := haversineMeters(pLat, pLng, cp.Lat, cp.Lng)
		if moveM >= checkinMinMoveM && gapS >= 0 && gapS < moveM*checkinMinSecPerMeter {
			flagged = true
			flagReason = fmt.Sprintf("距同賽事上次打卡 %.0f 秒、直線 %.0f 公尺，移動速度異常", gapS, moveM)
		}
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
		// 已有此打卡點的紀錄：仍待審則如實回報 pending（勿誤稱「已打卡」讓玩家以為已計入）
		if wasFlagged, _ := s.repo.checkinFlagged(ctx, userID, cpID); wasFlagged {
			res.Status = "pending"
			res.Message = "此打卡點仍在審核中，通過後才計入"
		} else {
			res.Status = "already"
			res.Message = "你已在此打卡點打過卡"
		}
	case flagged:
		res.Status = "pending"
		res.Message = "打卡成功，但移動速度異常（疑似定位偽造），將由主辦審核後計入"
	default:
		res.Status = "verified"
		res.Message = "打卡成功！"
		if res.TaskDone {
			res.Message = "打卡成功！本任務全部打卡點已集滿 🎉"
		}
	}
	return res, nil
}

// ActiveCheckpoints 會員當前可打卡的點清單
func (s *Service) ActiveCheckpoints(ctx context.Context, userID string) ([]ActiveCheckpoint, error) {
	return s.repo.activeCheckpointsForUser(ctx, userID)
}

// ListPendingCheckins 後台：某賽事待審核打卡
func (s *Service) ListPendingCheckins(ctx context.Context, raceID string) ([]PendingCheckin, error) {
	return s.repo.listPendingCheckins(ctx, raceID)
}

// ReviewCheckin 後台核准/退回一筆待審打卡。approve=true 核准；false 退回。
func (s *Service) ReviewCheckin(ctx context.Context, checkinID string, approve bool) error {
	var ok bool
	var err error
	if approve {
		ok, err = s.repo.approveCheckin(ctx, checkinID)
	} else {
		ok, err = s.repo.rejectCheckin(ctx, checkinID)
	}
	if err != nil {
		return err
	}
	if !ok {
		return ErrCheckinNotFound
	}
	return nil
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

// --- 後台：打卡審核 ---

// CheckinReviewRouter 打卡審核路由（掛載在 /api/v1/admin/checkin-review）
func (h *Handler) CheckinReviewRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListPendingCheckins)
	r.Patch("/{checkinID}/approve", h.AdminApproveCheckin)
	r.Patch("/{checkinID}/reject", h.AdminRejectCheckin)
	return r
}

// GET /api/v1/admin/checkin-review?race_id=
func (h *Handler) AdminListPendingCheckins(w http.ResponseWriter, r *http.Request) {
	raceID := r.URL.Query().Get("race_id")
	if raceID == "" {
		respondErr(w, http.StatusBadRequest, "race_id is required")
		return
	}
	list, err := h.svc.ListPendingCheckins(r.Context(), raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list pending checkins")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"checkins": list, "count": len(list)})
}

// PATCH /api/v1/admin/checkin-review/{checkinID}/approve
func (h *Handler) AdminApproveCheckin(w http.ResponseWriter, r *http.Request) {
	h.reviewCheckin(w, r, true)
}

// PATCH /api/v1/admin/checkin-review/{checkinID}/reject
func (h *Handler) AdminRejectCheckin(w http.ResponseWriter, r *http.Request) {
	h.reviewCheckin(w, r, false)
}

func (h *Handler) reviewCheckin(w http.ResponseWriter, r *http.Request, approve bool) {
	err := h.svc.ReviewCheckin(r.Context(), chi.URLParam(r, "checkinID"), approve)
	switch {
	case err == ErrCheckinNotFound:
		respondErr(w, http.StatusNotFound, "打卡不存在或已審核")
	case err != nil:
		respondErr(w, http.StatusInternalServerError, "failed to review checkin")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
