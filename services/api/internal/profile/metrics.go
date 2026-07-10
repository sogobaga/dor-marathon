package profile

import "net/http"

// AdminDataSourceMetrics GET /admin/data-source-metrics — 資料來源分布，供評估「是否值得直連手錶 / Terra 成本」。
// need_direct_watch＝有 garmin/coros 活動、但完全沒有 strava 活動的用戶數（＝真正需要直連手錶、Strava 覆蓋不到的人）。
func (h *Handler) AdminDataSourceMetrics(w http.ResponseWriter, r *http.Request) {
	var m struct {
		NeedDirectWatch int `json:"need_direct_watch"`
		WatchUsers      int `json:"watch_users"` // 有 garmin/coros 任一來源的用戶
		GarminUsers     int `json:"garmin_users"`
		CorosUsers      int `json:"coros_users"`
		StravaUsers     int `json:"strava_users"`
		GpsUsers        int `json:"gps_users"`
	}
	_ = h.db.QueryRow(r.Context(), `
		WITH per_user AS (
			SELECT user_id,
				COUNT(*) FILTER (WHERE source='garmin') AS garmin,
				COUNT(*) FILTER (WHERE source='coros')  AS coros,
				COUNT(*) FILTER (WHERE source='strava') AS strava,
				COUNT(*) FILTER (WHERE source IS NULL)  AS gps
			FROM activities
			GROUP BY user_id
		)
		SELECT
			COUNT(*) FILTER (WHERE (garmin>0 OR coros>0) AND strava=0),
			COUNT(*) FILTER (WHERE garmin>0 OR coros>0),
			COUNT(*) FILTER (WHERE garmin>0),
			COUNT(*) FILTER (WHERE coros>0),
			COUNT(*) FILTER (WHERE strava>0),
			COUNT(*) FILTER (WHERE gps>0)
		FROM per_user`).Scan(&m.NeedDirectWatch, &m.WatchUsers, &m.GarminUsers, &m.CorosUsers, &m.StravaUsers, &m.GpsUsers)
	respondJSON(w, http.StatusOK, m)
}
