package activity

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
)

// ListUserGPS 本人歷史清單（不含 polyline）。CalibDistanceKm/CalibFactor：見 GPSRunSummary 欄位
// 註解（medium-3 finding 修正）——本人視角要跟「已同步活動」列表/總里程同一套數字，COALESCE
// calib_distance_km 退化成 distance_km（舊資料/未套校正時兩者相同，前端不必特判 null）。
func (r *Repository) ListUserGPS(ctx context.Context, userID string) ([]GPSRunSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id::text, distance_km, duration_s, avg_pace_s, point_count,
		       flagged, COALESCE(flag_reason,''), COALESCE(review_action,''), started_at, ended_at,
		       COALESCE(calib_distance_km, distance_km), calib_factor
		FROM gps_runs WHERE user_id=$1
		ORDER BY started_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GPSRunSummary{}
	for rows.Next() {
		var s GPSRunSummary
		if err := rows.Scan(&s.ID, &s.DistanceKm, &s.DurationS, &s.AvgPaceS, &s.PointCount,
			&s.Flagged, &s.FlagReason, &s.ReviewAction, &s.StartedAt, &s.EndedAt,
			&s.CalibDistanceKm, &s.CalibFactor); err != nil {
			return nil, err
		}
		out = append(out, *s.withCalibAvgPace())
	}
	return out, rows.Err()
}

// GetUserGPSRun 本人單筆（含 polyline）。CalibDistanceKm/CalibFactor 同上。
func (r *Repository) GetUserGPSRun(ctx context.Context, userID, id string) (*GPSRunSummary, error) {
	var s GPSRunSummary
	err := r.db.QueryRow(ctx, `
		SELECT id::text, distance_km, duration_s, avg_pace_s, point_count,
		       flagged, COALESCE(flag_reason,''), COALESCE(review_action,''), started_at, ended_at, COALESCE(polyline,''), COALESCE(km_paces,'{}'),
		       COALESCE(calib_distance_km, distance_km), calib_factor
		FROM gps_runs WHERE id=$1 AND user_id=$2`, id, userID).
		Scan(&s.ID, &s.DistanceKm, &s.DurationS, &s.AvgPaceS, &s.PointCount,
			&s.Flagged, &s.FlagReason, &s.ReviewAction, &s.StartedAt, &s.EndedAt, &s.Polyline, &s.KmPaces,
			&s.CalibDistanceKm, &s.CalibFactor)
	if err != nil {
		return nil, err
	}
	return s.withCalibAvgPace(), nil
}

// GET /api/v1/activities/gps/history — 本人 GPS 跑步歷史
func (h *Handler) GPSHistory(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		http.Error(w, `{"error":"login required"}`, http.StatusUnauthorized)
		return
	}
	runs, err := h.svc.repo.ListUserGPS(r.Context(), userID)
	if err != nil {
		http.Error(w, `{"error":"failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"runs": runs})
}

// GET /api/v1/activities/gps/{id} — 本人單筆軌跡（回放）
func (h *Handler) GPSDetail(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		http.Error(w, `{"error":"login required"}`, http.StatusUnauthorized)
		return
	}
	run, err := h.svc.repo.GetUserGPSRun(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"run": run})
}
