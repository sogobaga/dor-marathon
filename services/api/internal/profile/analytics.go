package profile

import (
	"net/http"
	"time"
)

// vipByPlanCounts vip_by_plan 分佈（僅目前仍是 VIP 的人）
type vipByPlanCounts struct {
	Trial   int `json:"trial"`
	Monthly int `json:"monthly"`
	Annual  int `json:"annual"`
}

// vipNonRenewer 上個月到期、尚未續訂的會員
type vipNonRenewer struct {
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Plan      string    `json:"plan"`
	ExpiredAt time.Time `json:"expired_at"`
}

// vipMonthCount 近 12 個月連續序列的單月數值（缺月補 0）
type vipMonthCount struct {
	Month string `json:"month"`
	Count int    `json:"count"`
}

// vipAnalytics GET /admin/vip-analytics 回傳契約
type vipAnalytics struct {
	Total                int             `json:"total"`
	VIP                  int             `json:"vip"`
	General              int             `json:"general"`
	VipByPlan            vipByPlanCounts `json:"vip_by_plan"`
	LastMonthNonRenewers []vipNonRenewer `json:"last_month_non_renewers"`
	Growth               []vipMonthCount `json:"growth"`
	Churn                []vipMonthCount `json:"churn"`
}

// monthSeriesCTE 近 12 個月（含當月）連續序列，缺月以 0 補。
const monthSeriesCTE = `
	WITH months AS (
		SELECT gs, to_char(gs,'YYYY-MM') AS m
		FROM generate_series(date_trunc('month',NOW())-interval '11 months', date_trunc('month',NOW()), interval '1 month') gs)
`

// AdminVipAnalytics GET /admin/vip-analytics — VIP 訂閱後台分析總覽。
func (h *Handler) AdminVipAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := vipAnalytics{
		LastMonthNonRenewers: []vipNonRenewer{},
		Growth:               []vipMonthCount{},
		Churn:                []vipMonthCount{},
	}

	if err := h.db.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE vip_expires_at > NOW())
		FROM users`).Scan(&out.Total, &out.VIP); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load totals")
		return
	}
	out.General = out.Total - out.VIP

	if err := h.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE COALESCE(vip_plan,'')='trial'),
			COUNT(*) FILTER (WHERE COALESCE(vip_plan,'')='monthly'),
			COUNT(*) FILTER (WHERE COALESCE(vip_plan,'')='annual')
		FROM users WHERE vip_expires_at > NOW()`).
		Scan(&out.VipByPlan.Trial, &out.VipByPlan.Monthly, &out.VipByPlan.Annual); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load vip_by_plan")
		return
	}

	nrRows, err := h.db.Query(ctx, `
		SELECT u.id, COALESCE(NULLIF(p.nickname,''), u.name, ''), u.email, COALESCE(u.vip_plan,''), u.vip_expires_at
		FROM users u LEFT JOIN user_profiles p ON p.user_id = u.id
		WHERE u.vip_expires_at >= date_trunc('month', NOW()) - interval '1 month'
		  AND u.vip_expires_at < date_trunc('month', NOW())
		ORDER BY u.vip_expires_at DESC
		LIMIT 200`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load non-renewers")
		return
	}
	for nrRows.Next() {
		var n vipNonRenewer
		if err := nrRows.Scan(&n.UserID, &n.Name, &n.Email, &n.Plan, &n.ExpiredAt); err != nil {
			nrRows.Close()
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out.LastMonthNonRenewers = append(out.LastMonthNonRenewers, n)
	}
	nrRows.Close()

	growthRows, err := h.db.Query(ctx, monthSeriesCTE+`
		SELECT months.m, COUNT(u.id)
		FROM months LEFT JOIN users u ON date_trunc('month',u.created_at)=months.gs
		GROUP BY months.m, months.gs ORDER BY months.gs`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load growth")
		return
	}
	for growthRows.Next() {
		var mc vipMonthCount
		if err := growthRows.Scan(&mc.Month, &mc.Count); err != nil {
			growthRows.Close()
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out.Growth = append(out.Growth, mc)
	}
	growthRows.Close()

	churnRows, err := h.db.Query(ctx, monthSeriesCTE+`
		SELECT months.m, COUNT(u.id)
		FROM months LEFT JOIN users u
			ON date_trunc('month',u.vip_expires_at)=months.gs AND u.vip_expires_at < NOW()
		GROUP BY months.m, months.gs ORDER BY months.gs`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load churn")
		return
	}
	for churnRows.Next() {
		var mc vipMonthCount
		if err := churnRows.Scan(&mc.Month, &mc.Count); err != nil {
			churnRows.Close()
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out.Churn = append(out.Churn, mc)
	}
	churnRows.Close()

	respondJSON(w, http.StatusOK, out)
}
