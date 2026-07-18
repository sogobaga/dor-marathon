// 成就統計：里程/時長/連續天數/打卡/關主/個人任務/等級/卡片等總覽 + 月曆里程。
package profile

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/dor/api/internal/auth"
)

// Achievements 成就總覽（GET /profile/achievements）。
type Achievements struct {
	SingleMaxKm   float64 `json:"single_max_km"`
	CumKm         float64 `json:"cum_km"`
	SingleMaxSec  int     `json:"single_max_sec"`
	CumSec        int     `json:"cum_sec"`
	ActivityCount int     `json:"activity_count"`
	StreakDays    int     `json:"streak_days"`
	CheckinCount  int     `json:"checkin_count"`
	BossCount     int     `json:"boss_count"`
	BossS1        int     `json:"boss_s1"`
	BossS2        int     `json:"boss_s2"`
	BossS3        int     `json:"boss_s3"`
	PersonalCount int     `json:"personal_count"`
	Level         int     `json:"level"`
	LevelTitle    string  `json:"level_title"`
	CardCount     int     `json:"card_count"`
	Following     int     `json:"following"`
	Followers     int     `json:"followers"`
	Dp            int     `json:"dp"`
	RaceCount     int     `json:"race_count"`
}

// computeStreak 最長連續打卡（有活動記錄）天數，依 activities.recorded_at 台北日曆日
// （AT TIME ZONE 'Asia/Taipei'，比照 titles.go computeCurrentStreak；只算歷史最長連續段，無「今天」錨點）。
func (h *Handler) computeStreak(ctx context.Context, uid string) (int, error) {
	rows, err := h.db.Query(ctx, `
		SELECT DISTINCT (recorded_at AT TIME ZONE 'Asia/Taipei')::date FROM activities
		WHERE user_id=$1 AND NOT flagged
		ORDER BY (recorded_at AT TIME ZONE 'Asia/Taipei')::date`, uid)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var days []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return 0, err
		}
		days = append(days, d)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	longest, cur := 0, 0
	for i := range days {
		if i > 0 && days[i].Sub(days[i-1]) == 24*time.Hour {
			cur++
		} else {
			cur = 1
		}
		if cur > longest {
			longest = cur
		}
	}
	return longest, nil
}

// GET /api/v1/profile/achievements
func (h *Handler) GetAchievements(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()
	var a Achievements

	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(MAX(distance_km),0), COALESCE(SUM(distance_km),0),
		       COALESCE(MAX(duration_s),0), COALESCE(SUM(duration_s),0), COUNT(*)
		FROM activities WHERE user_id=$1 AND NOT flagged`, uid).
		Scan(&a.SingleMaxKm, &a.CumKm, &a.SingleMaxSec, &a.CumSec, &a.ActivityCount); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	streak, err := h.computeStreak(ctx, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	a.StreakDays = streak

	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM explore_progress WHERE user_id=$1 AND discovered=true`, uid).Scan(&a.CheckinCount); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	if err := h.db.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE stars=1),
		       COUNT(*) FILTER (WHERE stars=2),
		       COUNT(*) FILTER (WHERE stars=3)
		FROM explore_progress WHERE user_id=$1 AND completed_at IS NOT NULL AND stars>0`, uid).
		Scan(&a.BossCount, &a.BossS1, &a.BossS2, &a.BossS3); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM personal_task_progress WHERE user_id=$1`, uid).Scan(&a.PersonalCount); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM explore_progress WHERE user_id=$1 AND card_obtained=true`, uid).Scan(&a.CardCount); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	var exp int
	if err := h.db.QueryRow(ctx, `SELECT COALESCE(dp,0), exp FROM users WHERE id=$1`, uid).Scan(&a.Dp, &exp); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	levels, err := h.levelConfigList(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	a.Level, a.LevelTitle, _, _ = computeLevel(exp, levels)

	if err := h.db.QueryRow(ctx, `SELECT COUNT(*) FROM follows WHERE follower_id=$1`, uid).Scan(&a.Following); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if err := h.db.QueryRow(ctx, `SELECT COUNT(*) FROM follows WHERE followee_id=$1`, uid).Scan(&a.Followers); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM registrations WHERE user_id=$1 AND status<>'cancelled'`, uid).Scan(&a.RaceCount); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	respondJSON(w, http.StatusOK, a)
}

// CalendarDay 月曆單日里程。
type CalendarDay struct {
	Date string  `json:"date"`
	Km   float64 `json:"km"`
}

// GET /api/v1/profile/achievements/calendar?month=YYYY-MM
func (h *Handler) AchievementsCalendar(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	month := r.URL.Query().Get("month")
	monthStart, err := time.Parse("2006-01", month)
	if err != nil {
		respondErr(w, http.StatusBadRequest, "invalid month")
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT (recorded_at AT TIME ZONE 'Asia/Taipei')::date, SUM(distance_km)
		FROM activities
		WHERE user_id=$1 AND NOT flagged
		  AND (recorded_at AT TIME ZONE 'Asia/Taipei')::date >= $2::date
		  AND (recorded_at AT TIME ZONE 'Asia/Taipei')::date < ($2::date + INTERVAL '1 month')
		GROUP BY (recorded_at AT TIME ZONE 'Asia/Taipei')::date
		ORDER BY (recorded_at AT TIME ZONE 'Asia/Taipei')::date`, uid, monthStart.Format("2006-01-02"))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	days := []CalendarDay{}
	var totalKm float64
	for rows.Next() {
		var d time.Time
		var km float64
		if err := rows.Scan(&d, &km); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		km = math.Round(km*100) / 100
		days = append(days, CalendarDay{Date: d.Format("2006-01-02"), Km: km})
		totalKm += km
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "scan failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"month":    month,
		"total_km": math.Round(totalKm*100) / 100,
		"days":     days,
	})
}
