// Profile 模組：個人資料、完賽紀錄、成就統計
package profile

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/dor/api/internal/auth"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// Router 個人資料路由（掛載在 /api/v1/profile）
func (h *Handler) Router() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/records":
			h.Records(w, r)
		case "/stats":
			h.Stats(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// RaceRecord 個人完賽紀錄
type RaceRecord struct {
	RaceID    string    `json:"race_id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Distance  int       `json:"distance"`   // 報名組別
	TotalKm   float64   `json:"total_km"`   // 實際完成里程
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
	Faction   string    `json:"faction"`
	Status    string    `json:"status"` // completed | dnf（未完賽）
	Rank      int       `json:"rank"`   // 最終排名（從 DB activities 算）
}

type Stats struct {
	TotalKm    float64 `json:"total_km"`
	TotalRaces int     `json:"total_races"`
	Rescues    int     `json:"rescues"`
	BestPaceS  int     `json:"best_pace_s"` // 最佳配速（秒/公里）
}

// GET /api/v1/profile/records
func (h *Handler) Records(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	records, err := h.fetchRecords(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to fetch records")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"records": records,
		"count":   len(records),
	})
}

// GET /api/v1/profile/stats
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	stats, err := h.fetchStats(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to fetch stats")
		return
	}
	respondJSON(w, http.StatusOK, stats)
}

func (h *Handler) fetchRecords(ctx context.Context, userID string) ([]*RaceRecord, error) {
	rows, err := h.db.Query(ctx, `
		SELECT
		    r.id, r.slug, r.title, r.start_date, r.end_date,
		    reg.distance, COALESCE(reg.faction,'') as faction,
		    COALESCE(SUM(a.distance_km), 0) as total_km,
		    CASE WHEN COALESCE(SUM(a.distance_km),0) >= reg.distance THEN 'completed' ELSE 'dnf' END as status
		FROM registrations reg
		JOIN races r ON r.id = reg.race_id
		LEFT JOIN activities a ON a.user_id = reg.user_id AND a.race_id = reg.race_id
		WHERE reg.user_id = $1 AND reg.status = 'paid'
		GROUP BY r.id, r.slug, r.title, r.start_date, r.end_date, reg.distance, reg.faction
		ORDER BY r.end_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*RaceRecord
	for rows.Next() {
		rec := &RaceRecord{}
		if err := rows.Scan(
			&rec.RaceID, &rec.Slug, &rec.Title,
			&rec.StartDate, &rec.EndDate,
			&rec.Distance, &rec.Faction,
			&rec.TotalKm, &rec.Status,
		); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (h *Handler) fetchStats(ctx context.Context, userID string) (*Stats, error) {
	s := &Stats{}
	err := h.db.QueryRow(ctx, `
		SELECT
		    u.total_km,
		    COUNT(DISTINCT reg.race_id)                         as total_races,
		    COALESCE(SUM(mc.rescue_count), 0)                   as rescues,
		    COALESCE(MIN(a.avg_pace_s), 0)                      as best_pace_s
		FROM users u
		LEFT JOIN registrations reg ON reg.user_id = u.id AND reg.status = 'paid'
		LEFT JOIN mission_completions mc ON mc.user_id = u.id
		LEFT JOIN activities a ON a.user_id = u.id
		WHERE u.id = $1
		GROUP BY u.total_km
	`, userID).Scan(&s.TotalKm, &s.TotalRaces, &s.Rescues, &s.BestPaceS)
	if err != nil {
		return &Stats{}, nil // 新用戶沒有資料時回傳空統計
	}
	return s, nil
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
