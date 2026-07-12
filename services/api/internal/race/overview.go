package race

import (
	"context"
	"net/http"
	"time"

	"github.com/dor/api/internal/auth"
)

// --- 後台數據總覽 + 即時「在跑」名單（心跳）---

// UpsertLiveTracking 跑步中心跳：記錄/更新使用者最後在跑時間。
func (r *Repository) UpsertLiveTracking(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO live_tracking (user_id, last_seen) VALUES ($1, NOW())
		ON CONFLICT (user_id) DO UPDATE SET last_seen = NOW()`, userID)
	return err
}

// Ping POST /api/v1/track/ping — 跑步中每 ~30 秒回報一次（需登入）
func (h *Handler) Ping(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	if err := h.svc.repo.UpsertLiveTracking(r.Context(), userID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// OverviewRace 後台總覽單一賽事
type OverviewRace struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	DisplayStatus string    `json:"display_status"` // upcoming_reg|registering|reg_closed|starting_soon|racing|paused|suspended
	StartDate     time.Time `json:"start_date"`
	EndDate       time.Time `json:"end_date"`
	Registrations int       `json:"registrations"`  // 報名人數（未取消）
	TrackingCount int       `json:"tracking_count"` // 目前在此賽事 GPS 跑步追蹤中的人數
	TrackingNames []string  `json:"tracking_names"` // 名單
}

// AdminOverviewData 後台總覽回應
type AdminOverviewData struct {
	Races         []OverviewRace `json:"races"`
	TrackingTotal int            `json:"tracking_total"` // 全站目前在跑人數（含未報名任何賽事者）
	GeneratedAt   time.Time      `json:"generated_at"`
}

// overviewRaces 近半年內尚未結束的已核准賽事（依開始時間排序）。
func (r *Repository) overviewRaces(ctx context.Context) ([]Race, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id::text, title, COALESCE(control_status,'active'), COALESCE(starting_soon_days,5),
		       registration_start, registration_end, start_date, end_date
		FROM races
		WHERE review_status='approved' AND end_date >= NOW() AND start_date <= NOW() + INTERVAL '6 months'
		ORDER BY start_date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Race{}
	for rows.Next() {
		var rc Race
		if err := rows.Scan(&rc.ID, &rc.Title, &rc.ControlStatus, &rc.StartingSoonDays,
			&rc.RegStart, &rc.RegEnd, &rc.StartDate, &rc.EndDate); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

func (r *Repository) regCountsByRace(ctx context.Context, raceIDs []string) (map[string]int, error) {
	m := map[string]int{}
	if len(raceIDs) == 0 {
		return m, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT race_id::text, count(*) FROM registrations
		WHERE status <> 'cancelled' AND race_id::text = ANY($1) GROUP BY race_id`, raceIDs)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err == nil {
			m[id] = n
		}
	}
	return m, rows.Err()
}

// trackingByRace 近 2 分鐘有心跳、且報名該賽事者 → race_id → 名單。
func (r *Repository) trackingByRace(ctx context.Context, raceIDs []string) (map[string][]string, error) {
	m := map[string][]string{}
	if len(raceIDs) == 0 {
		return m, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT reg.race_id::text, COALESCE(NULLIF(u.name,''), u.handle, '跑者')
		FROM live_tracking lt
		JOIN registrations reg ON reg.user_id = lt.user_id AND reg.status <> 'cancelled' AND reg.race_id::text = ANY($1)
		JOIN users u ON u.id = lt.user_id
		LEFT JOIN user_profiles p ON p.user_id = lt.user_id
		WHERE lt.last_seen > NOW() - INTERVAL '2 minutes'
		ORDER BY reg.race_id`, raceIDs)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err == nil {
			m[id] = append(m[id], name)
		}
	}
	return m, rows.Err()
}

func (r *Repository) trackingTotal(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM live_tracking WHERE last_seen > NOW() - INTERVAL '2 minutes'`).Scan(&n)
	return n, err
}

// GetAdminOverview 後台數據總覽：近半年賽事（狀態/報名數/在跑名單）+ 全站在跑人數。
func (s *Service) GetAdminOverview(ctx context.Context, now time.Time) (*AdminOverviewData, error) {
	races, err := s.repo.overviewRaces(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(races))
	for i := range races {
		ids = append(ids, races[i].ID)
	}
	regCounts, _ := s.repo.regCountsByRace(ctx, ids)
	track, _ := s.repo.trackingByRace(ctx, ids)
	total, _ := s.repo.trackingTotal(ctx)

	out := &AdminOverviewData{Races: []OverviewRace{}, TrackingTotal: total, GeneratedAt: now}
	for i := range races {
		disp, _ := races[i].ComputeDisplay(now)
		names := track[races[i].ID]
		if names == nil {
			names = []string{}
		}
		out.Races = append(out.Races, OverviewRace{
			ID: races[i].ID, Title: races[i].Title, DisplayStatus: disp,
			StartDate: races[i].StartDate, EndDate: races[i].EndDate,
			Registrations: regCounts[races[i].ID],
			TrackingCount: len(names), TrackingNames: names,
		})
	}
	return out, nil
}

// AdminOverview GET /api/v1/admin/overview（需 admin）
func (h *Handler) AdminOverview(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.GetAdminOverview(r.Context(), time.Now())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get overview")
		return
	}
	respondJSON(w, http.StatusOK, data)
}
