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

// GPSRunSummary 後台審核清單/詳情
type GPSRunSummary struct {
	ID         string          `json:"id"`
	UserID     string          `json:"user_id"`
	UserName   string          `json:"user_name"`
	DistanceKm float64         `json:"distance_km"`
	DurationS  int             `json:"duration_s"`
	AvgPaceS   int             `json:"avg_pace_s"`
	PointCount int             `json:"point_count"`
	FlagReason string          `json:"flag_reason"`
	StartedAt  time.Time       `json:"started_at"`
	EndedAt    time.Time       `json:"ended_at"`
	Points     json.RawMessage `json:"points,omitempty"` // 僅詳情回傳
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
	var pts []byte
	err := r.db.QueryRow(ctx, `
		SELECT g.id::text, g.user_id::text, COALESCE(u.name,''), g.distance_km, g.duration_s, g.avg_pace_s,
		       g.point_count, COALESCE(g.flag_reason,''), g.started_at, g.ended_at, COALESCE(g.points,'[]'::jsonb)
		FROM gps_runs g JOIN users u ON u.id=g.user_id WHERE g.id=$1`, id).
		Scan(&s.ID, &s.UserID, &s.UserName, &s.DistanceKm, &s.DurationS, &s.AvgPaceS,
			&s.PointCount, &s.FlagReason, &s.StartedAt, &s.EndedAt, &pts)
	if err != nil {
		return nil, err
	}
	s.Points = pts
	return &s, nil
}

// claimPendingGPS 取出待審且鎖定（回傳發活動所需欄位）；非 pending 回 errGPSNotPending
func (r *Repository) reviewGPS(ctx context.Context, id, action string) (userID, raceID string, dist float64, dur, pace int, ended time.Time, err error) {
	err = r.db.QueryRow(ctx, `
		UPDATE gps_runs SET reviewed_at=NOW(), review_action=$2
		WHERE id=$1 AND flagged AND reviewed_at IS NULL
		RETURNING user_id::text, COALESCE(race_id::text,''), distance_km, duration_s, avg_pace_s, ended_at`,
		id, action).Scan(&userID, &raceID, &dist, &dur, &pace, &ended)
	return
}

// AdminApproveGPS 核准：標記 approved 並推入活動管線（記錄 + 里程 EXP）
func (s *Service) AdminApproveGPS(ctx context.Context, id string) error {
	userID, raceID, dist, dur, pace, ended, err := s.repo.reviewGPS(ctx, id, "approved")
	if err != nil {
		return errGPSNotPending
	}
	evt := ActivityEvent{UserID: userID, RaceID: raceID, DistanceKm: dist, DurationS: dur, AvgPaceS: pace, RecordedAt: ended.Format(time.RFC3339)}
	b, _ := json.Marshal(evt)
	s.rdb.XAdd(ctx, &redis.XAddArgs{Stream: streamKey, Values: map[string]any{"data": string(b)}})
	return nil
}

// AdminRejectGPS 駁回：標記 rejected，不發 EXP
func (s *Service) AdminRejectGPS(ctx context.Context, id string) error {
	if _, _, _, _, _, _, err := s.repo.reviewGPS(ctx, id, "rejected"); err != nil {
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
