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
	rows, err := h.db.Query(r.Context(),
		`SELECT exp_amount, dp_amount, km_added FROM mileage_exp_events
		 WHERE user_id=$1 AND seen_at IS NULL ORDER BY created_at`, userID)
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
