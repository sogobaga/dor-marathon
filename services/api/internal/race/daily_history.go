package race

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
)

// 進度頁「每日歷程記錄」：玩家在此賽事期間，每一天跑了幾筆、各筆的距離/時長/配速。
// 里程窗與 LoadRaceActivities／loadUserRangeActivities／「我的里程」完全一致（personal 從
// challenge_started_at 起算、external_data gate、排除 flagged、賽事期間窗），確保每日加總對得起「我的里程」，
// 不會重演「貢獻榜 6km vs 我的里程 1.27km」那種對不上的情形（見 memory personal-challenge-mode）。

type DailyActivity struct {
	RecordedAt time.Time `json:"recorded_at"`
	DistanceKm float64   `json:"distance_km"`
	DurationS  int       `json:"duration_s"`
	AvgPaceS   int       `json:"avg_pace_s"`
	Source     string    `json:"source"` // "" = App GPS；其餘 strava/garmin/coros
}

type DailyStat struct {
	Date       string          `json:"date"` // 台北時區日期 YYYY-MM-DD
	TotalKm    float64         `json:"total_km"`
	Count      int             `json:"count"`
	Activities []DailyActivity `json:"activities"`
}

// loadUserDailyActivities 目前登入者在此賽事里程窗內的逐筆活動（新到舊）。
// scoping 與 loadUserRangeActivities 完全一致（僅 SELECT 欄位不同：多取 duration_s / source）。
func (r *Repository) loadUserDailyActivities(ctx context.Context, raceID, userID string) ([]DailyActivity, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.distance_km, a.duration_s, a.avg_pace_s, a.recorded_at, COALESCE(a.source,'')
		FROM races rc
		LEFT JOIN registrations reg ON reg.race_id = rc.id AND reg.user_id = $2
		     AND rc.event_mode = 'personal' AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL
		JOIN activities a ON a.user_id=$2 AND NOT a.flagged
		                  AND a.recorded_at <= rc.end_date
		                  AND a.recorded_at >= CASE WHEN rc.event_mode = 'personal'
		                                            THEN reg.challenge_started_at ELSE rc.start_date END
		                  AND (rc.external_data OR a.source IS NULL)
		WHERE rc.id=$1
		ORDER BY a.recorded_at DESC`, raceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyActivity{}
	for rows.Next() {
		var a DailyActivity
		if err := rows.Scan(&a.DistanceKm, &a.DurationS, &a.AvgPaceS, &a.RecordedAt, &a.Source); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetMyDailyActivities 依台北日期分組回傳（日期新到舊；每日內依時間由早到晚排，符合「當天先跑再跑」的時間軸）。
func (s *Service) GetMyDailyActivities(ctx context.Context, raceID, userID string) ([]DailyStat, error) {
	if userID == "" {
		return nil, ErrRaceNotFound
	}
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	acts, err := s.repo.loadUserDailyActivities(ctx, raceID, userID)
	if err != nil {
		return nil, err
	}
	order := []string{}
	byDay := map[string]*DailyStat{}
	for _, a := range acts {
		// 台北日期：不用 time.LoadLocation（distroless 無 tzdata），一律 UTC+8（見 memory 時區慣例）
		day := a.RecordedAt.UTC().Add(8 * time.Hour).Format("2006-01-02")
		d := byDay[day]
		if d == nil {
			d = &DailyStat{Date: day, Activities: []DailyActivity{}}
			byDay[day] = d
			order = append(order, day) // acts 為新到舊 → 首見的日期即最新，順序天然新到舊
		}
		d.TotalKm += a.DistanceKm
		d.Count++
		d.Activities = append(d.Activities, a)
	}
	out := make([]DailyStat, 0, len(order))
	for _, day := range order {
		d := byDay[day]
		d.TotalKm = math.Round(d.TotalKm*100) / 100
		// 查詢是新到舊 → 每日內反轉成由早到晚
		for i, j := 0, len(d.Activities)-1; i < j; i, j = i+1, j-1 {
			d.Activities[i], d.Activities[j] = d.Activities[j], d.Activities[i]
		}
		out = append(out, *d)
	}
	return out, nil
}

// GET /api/v1/races/:raceID/my-daily-activities — 進度頁每日歷程記錄（需登入；未登入回 404）
func (h *Handler) MyDailyActivities(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	days, err := h.svc.GetMyDailyActivities(r.Context(), raceID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get daily activities")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"days": days})
}
