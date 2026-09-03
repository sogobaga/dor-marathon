package profile

import (
	"fmt"
	"net/http"

	"github.com/dor/api/internal/auth"
)

// MileageBreakdown 與 race.ExpBreakdown 同 JSON 形狀，前台共用結算彈窗
type MileageBreakdown struct {
	Gained    int               `json:"gained"`
	ExpBefore int               `json:"exp_before"`
	ExpAfter  int               `json:"exp_after"`
	DpGained  int               `json:"dp_gained"`
	DpAfter   int               `json:"dp_after"`
	Items     []mileageItem     `json:"items"`
	Levels    []mileageLevelRow `json:"levels"`
}
type mileageItem struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
	Dp     int    `json:"dp"`
	Kind   string `json:"kind"`
}
type mileageLevelRow struct {
	Level       int    `json:"level"`
	Title       string `json:"title"`
	ExpRequired int    `json:"exp_required"`
}

// GET /api/v1/profile/mileage-exp — 取未顯示的日常里程 EXP 結算（給彈窗）
func (h *Handler) GetMileageExp(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	bd := MileageBreakdown{Items: []mileageItem{}, Levels: []mileageLevelRow{}}

	// 顯示「實際發獎的整公里數」km_added（跨越幾個整公里就發幾份），
	// 不用單趟 distance_km（避免出現「里程 0.2 km 卻發獎」的誤導——獎勵其實是跨過整公里才給）。
	//
	// AND (exp_amount > 0 OR dp_amount > 0)：防禦性修正（2026-09-03 owner 回收決策新增，見
	// internal/activity/gps_recall.go）——異常活動回收會寫入 exp_amount/dp_amount/km_added 皆為
	// 負值的「回收標記列」，正常情況下該列的 seen_at 在寫入當下就已直接設為 NOW()，理論上不會被
	// 這裡的 seen_at IS NULL 撈到；這裡加這層過濾是雙保險，即使 seen_at 因故仍是 NULL，也絕不能讓
	// 使用者看到「里程 -11 km，獲得 EXP -50」這種倒扣彈窗。
	rows, err := h.db.Query(r.Context(),
		`SELECT exp_amount, dp_amount, km_added FROM mileage_exp_events
		 WHERE user_id=$1 AND seen_at IS NULL AND (exp_amount > 0 OR dp_amount > 0) ORDER BY created_at`, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var amt, dp, kmAdded int
		if err := rows.Scan(&amt, &dp, &kmAdded); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		bd.Items = append(bd.Items, mileageItem{Label: fmt.Sprintf("里程達成 %d km", kmAdded), Amount: amt, Dp: dp, Kind: "mileage"})
		bd.Gained += amt
		bd.DpGained += dp
	}

	_ = h.db.QueryRow(r.Context(), `SELECT COALESCE(exp,0), COALESCE(dp,0) FROM users WHERE id=$1`, userID).Scan(&bd.ExpAfter, &bd.DpAfter)
	bd.ExpBefore = bd.ExpAfter - bd.Gained
	if bd.ExpBefore < 0 {
		bd.ExpBefore = 0
	}

	lrows, err := h.db.Query(r.Context(), `SELECT level, COALESCE(title,''), exp_required FROM level_config ORDER BY exp_required`)
	if err == nil {
		defer lrows.Close()
		for lrows.Next() {
			var lr mileageLevelRow
			if err := lrows.Scan(&lr.Level, &lr.Title, &lr.ExpRequired); err == nil {
				bd.Levels = append(bd.Levels, lr)
			}
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{"breakdown": bd})
}

// GET /api/v1/profile/mileage-config — 前台 /track 讀里程獎勵設定（畫即時進度條、預覽獎勵用）
func (h *Handler) MileageConfig(w http.ResponseWriter, r *http.Request) {
	var perKm, dpPerKm, capKm int
	_ = h.db.QueryRow(r.Context(),
		`SELECT COALESCE(per_km,0), COALESCE(dp_per_km,0), COALESCE(mileage_cap_km,21) FROM exp_rules WHERE id=TRUE`).
		Scan(&perKm, &dpPerKm, &capKm)
	respondJSON(w, http.StatusOK, map[string]any{"per_km": perKm, "dp_per_km": dpPerKm, "cap_km": capKm})
}

// POST /api/v1/profile/mileage-exp/seen — 標記已顯示
func (h *Handler) MarkMileageSeen(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`UPDATE mileage_exp_events SET seen_at=NOW() WHERE user_id=$1 AND seen_at IS NULL`, userID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
