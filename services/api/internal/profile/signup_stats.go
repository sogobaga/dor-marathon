// 推廣連結頁「成效統計」：各通路（見 migration 147_signup_attribution）近 12 週註冊數趨勢
// ＋各來源彙總（近7天/近30天/累計），供 admin/promo-links 頁的堆疊長條圖與彙總表使用。
package profile

import (
	"net/http"
	"time"
)

// signupStatsWeeks 週別趨勢回溯週數（含當週）。
const signupStatsWeeks = 12

// signupStatsWeek 單週各來源註冊數。counts 只含當週實際有註冊的來源（無資料的來源不出現在 map
// 中，前端視同 0）；全站當週完全無新註冊時 counts 為空 map。
type signupStatsWeek struct {
	WeekStart string         `json:"week_start"`
	Counts    map[string]int `json:"counts"`
}

// signupStatsTotal 各來源彙總一列。source='other' 時依 utm->>'source' 細分成多列（UTMSource 填
// 原值，無值的一列 UTMSource=""）；非 other 來源固定一列，UTMSource 恆為空字串。
type signupStatsTotal struct {
	Source    string `json:"source"`
	UTMSource string `json:"utm_source"`
	C7        int    `json:"c7"`
	C30       int    `json:"c30"`
	Total     int    `json:"total"`
}

type signupStatsResponse struct {
	Weekly []signupStatsWeek  `json:"weekly"`
	Totals []signupStatsTotal `json:"totals"`
}

// AdminSignupStats GET /admin/signup-stats：推廣連結頁「成效統計」——近 12 週各來源註冊數（供堆疊
// 長條圖，依台灣時區週一起算）＋各來源彙總（近7天/近30天/累計，供表格）。
//
// 時間邊界一律台灣日口徑，換算慣例比照 internal/ops/dailyreport.go：借 Postgres 雙重
// `AT TIME ZONE 'Asia/Taipei'`（先轉成台灣本地的 naive timestamp 做 date_trunc，再轉回去）在 SQL
// 端一次算出正確的 UTC 絕對時刻邊界；Go 側只對這個絕對時刻做加減週/日與 +8h 格式化，不使用
// time.LoadLocation（本站 distroless 執行環境沒有 tzdata）。
//
// SQL 全唯讀、容忍 0 列：user_signup_attribution 剛上線或該區間無資料時，weekly 仍回滿 12 週的
// 零值列（counts 為空 map），totals 回空陣列，皆不是錯誤。
func (h *Handler) AdminSignupStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1) 邊界：本週（Taipei 週一 00:00）與今日（Taipei 00:00），换算成 UTC 絕對時刻後可直接跟
	// created_at（timestamptz）比較，不必在下面每個查詢裡再轉一次時區。
	var thisWeekStart, todayStart time.Time
	if err := h.db.QueryRow(ctx, `
		SELECT
			date_trunc('week', (now() AT TIME ZONE 'Asia/Taipei')) AT TIME ZONE 'Asia/Taipei',
			date_trunc('day', (now() AT TIME ZONE 'Asia/Taipei')) AT TIME ZONE 'Asia/Taipei'
	`).Scan(&thisWeekStart, &todayStart); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to compute time boundaries")
		return
	}
	seriesStart := thisWeekStart.AddDate(0, 0, -7*(signupStatsWeeks-1)) // 12 週前（含當週）的週一
	c7Start := todayStart.AddDate(0, 0, -6)                             // 近 7 天＝含今日在內的 7 個台灣日
	c30Start := todayStart.AddDate(0, 0, -29)                           // 近 30 天＝含今日在內的 30 個台灣日

	// 2) 週別分佈：依 source 分組（不細分 other 的 utm_source——堆疊圖只認 13 種標準來源，細分留給
	// 下面的彙總表）。week_start 用同一招雙重 AT TIME ZONE 換算回 timestamptz，與 Go 端算出的週界
	// （seriesStart 逐週 +7 天）在同一個時刻系統下，才能對得起來。
	weekRows, err := h.db.Query(ctx, `
		SELECT date_trunc('week', created_at AT TIME ZONE 'Asia/Taipei') AT TIME ZONE 'Asia/Taipei' AS week_start,
		       source, COUNT(*)
		FROM user_signup_attribution
		WHERE created_at >= $1
		GROUP BY week_start, source
	`, seriesStart)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load weekly stats")
		return
	}
	byWeek := map[string]map[string]int{}
	for weekRows.Next() {
		var wk time.Time
		var source string
		var n int
		if err := weekRows.Scan(&wk, &source, &n); err != nil {
			weekRows.Close()
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		label := wk.UTC().Add(8 * time.Hour).Format("2006-01-02")
		if byWeek[label] == nil {
			byWeek[label] = map[string]int{}
		}
		byWeek[label][source] = n
	}
	if err := weekRows.Err(); err != nil {
		weekRows.Close()
		respondErr(w, http.StatusInternalServerError, "weekly rows error")
		return
	}
	weekRows.Close()

	weekly := make([]signupStatsWeek, 0, signupStatsWeeks)
	for i := 0; i < signupStatsWeeks; i++ {
		boundary := seriesStart.AddDate(0, 0, 7*i)
		label := boundary.UTC().Add(8 * time.Hour).Format("2006-01-02")
		counts := byWeek[label]
		if counts == nil {
			counts = map[string]int{}
		}
		weekly = append(weekly, signupStatsWeek{WeekStart: label, Counts: counts})
	}

	// 3) 彙總：各來源近7天/近30天/累計；source='other' 依 utm->>'source' 細分成多列。
	totalRows, err := h.db.Query(ctx, `
		SELECT source, utm_source,
		       COUNT(*) FILTER (WHERE created_at >= $1) AS c7,
		       COUNT(*) FILTER (WHERE created_at >= $2) AS c30,
		       COUNT(*) AS total
		FROM (
			SELECT created_at, source,
			       CASE WHEN source = 'other' THEN COALESCE(utm->>'source', '') ELSE '' END AS utm_source
			FROM user_signup_attribution
		) s
		GROUP BY source, utm_source
		ORDER BY total DESC, source, utm_source
	`, c7Start, c30Start)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load total stats")
		return
	}
	totals := []signupStatsTotal{}
	for totalRows.Next() {
		var t signupStatsTotal
		if err := totalRows.Scan(&t.Source, &t.UTMSource, &t.C7, &t.C30, &t.Total); err != nil {
			totalRows.Close()
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		totals = append(totals, t)
	}
	if err := totalRows.Err(); err != nil {
		totalRows.Close()
		respondErr(w, http.StatusInternalServerError, "total rows error")
		return
	}
	totalRows.Close()

	respondJSON(w, http.StatusOK, signupStatsResponse{Weekly: weekly, Totals: totals})
}
