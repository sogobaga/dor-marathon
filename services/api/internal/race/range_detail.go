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

// 區間類任務（平均配速/心率區間）的「達標明細」：讓玩家點進去看自己是哪幾公里/哪幾筆達標。
// 配速：優先看每公里分段（哪幾公里落在區間），無分段退回整段均配速。

type RangeActivity struct {
	RecordedAt time.Time `json:"recorded_at"`
	DistanceKm float64   `json:"distance_km"`
	AvgPaceS   int       `json:"avg_pace_s"`
	AvgHr      int       `json:"avg_hr"`
	KmPaces    []int     `json:"km_paces"`
	QualifyKms []int     `json:"qualify_kms"` // 1-based：落在配速區間的公里
	Qualified  bool      `json:"qualified"`
}

type TaskRangeDetail struct {
	TaskID     string          `json:"task_id"`
	TaskTitle  string          `json:"task_title"`
	Metric     string          `json:"metric"` // avg_pace_range | avg_hr_range
	RangeLo    float64         `json:"range_lo"`
	RangeHi    float64         `json:"range_hi"`
	Activities []RangeActivity `json:"activities"`
}

// loadUserRangeActivities 目前登入者在此賽事期間的活動（含每公里分段），新到舊。
func (r *Repository) loadUserRangeActivities(ctx context.Context, raceID, userID string) ([]progAct, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.distance_km, COALESCE(a.avg_hr,0), a.avg_pace_s, a.recorded_at, COALESCE(a.km_paces,'{}')
		FROM races rc
		JOIN activities a ON a.user_id=$2 AND NOT a.flagged AND a.recorded_at BETWEEN rc.start_date AND rc.end_date
		WHERE rc.id=$1
		ORDER BY a.recorded_at DESC`, raceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []progAct{}
	for rows.Next() {
		var a progAct
		if err := rows.Scan(&a.Dist, &a.HR, &a.PaceS, &a.At, &a.KmPaces); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetTaskRangeDetail 計算某區間任務下、目前登入者各活動的達標明細。
func (s *Service) GetTaskRangeDetail(ctx context.Context, raceID, taskID, userID string) (*TaskRangeDetail, error) {
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
	tasks, err := s.repo.GetRaceTasks(ctx, raceID)
	if err != nil {
		return nil, err
	}
	var task *RaceTask
	for i := range tasks {
		if tasks[i].ID == taskID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		return nil, ErrRaceNotFound
	}
	spec, ok := MetricCatalog[task.MetricType]
	if !ok || spec.Kind != MetricRange {
		return nil, ErrRaceNotFound // 僅支援區間類任務
	}
	lo, hi := 0.0, 0.0
	if task.RangeLo != nil {
		lo = *task.RangeLo
	}
	if task.RangeHi != nil {
		hi = *task.RangeHi
	}
	inRange := func(v float64) bool { return v > 0 && v >= lo && v <= hi }

	acts, err := s.repo.loadUserRangeActivities(ctx, raceID, userID)
	if err != nil {
		return nil, err
	}
	out := &TaskRangeDetail{TaskID: task.ID, TaskTitle: task.Title, Metric: task.MetricType, RangeLo: lo, RangeHi: hi, Activities: []RangeActivity{}}
	for _, a := range acts {
		ra := RangeActivity{RecordedAt: a.At, DistanceKm: math.Round(a.Dist*100) / 100, AvgPaceS: a.PaceS, AvgHr: a.HR, KmPaces: a.KmPaces, QualifyKms: []int{}}
		if task.MetricType == "avg_pace_range" {
			for i, p := range a.KmPaces {
				if inRange(float64(p)) {
					ra.QualifyKms = append(ra.QualifyKms, i+1)
				}
			}
			ra.Qualified = len(ra.QualifyKms) > 0 || (len(a.KmPaces) == 0 && inRange(float64(a.PaceS)))
		} else { // avg_hr_range
			ra.Qualified = inRange(float64(a.HR))
		}
		out.Activities = append(out.Activities, ra)
	}
	return out, nil
}

// GET /api/v1/races/:raceID/tasks/:taskID/range-detail — 區間任務的個人達標明細（需登入）
func (h *Handler) TaskRangeDetail(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	taskID := chi.URLParam(r, "taskID")
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	d, err := h.svc.GetTaskRangeDetail(r.Context(), raceID, taskID, userID)
	if errors.Is(err, ErrRaceNotFound) {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get range detail")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"detail": d})
}
