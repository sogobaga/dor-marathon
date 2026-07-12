// 稱號系統：8 類別統計門檻自動解鎖 + 展示中稱號。
package profile

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/dor/api/internal/auth"
)

// unlockedNamePlaceholder 未解鎖稱號一律以此遮蔽（8 個全形問號），前端不得看到真名。
const unlockedNamePlaceholder = "？？？？？？？？"

// AwardedTitle 新解鎖（尚未看過）的稱號，供 Dashboard 彈窗使用。
type AwardedTitle struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Tier     int    `json:"tier"`
	Category string `json:"category"`
}

// titleCategoryStats 計算 8 類別（category -> 目前統計值）。
func (h *Handler) titleCategoryStats(ctx context.Context, uid string) (map[string]float64, error) {
	stats := map[string]float64{}

	var singleMaxKm, cumKm float64
	var cumSecs int
	if err := h.db.QueryRow(ctx, `
		SELECT COALESCE(MAX(distance_km),0), COALESCE(SUM(distance_km),0), COALESCE(SUM(duration_s),0)
		FROM activities WHERE user_id=$1 AND NOT flagged`, uid).
		Scan(&singleMaxKm, &cumKm, &cumSecs); err != nil {
		return nil, err
	}
	stats["single_dist"] = singleMaxKm
	stats["cum_dist"] = cumKm
	stats["cum_time"] = float64(cumSecs) / 3600.0

	var checkinCount int
	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM explore_progress WHERE user_id=$1 AND discovered=true`, uid).Scan(&checkinCount); err != nil {
		return nil, err
	}
	stats["checkin"] = float64(checkinCount)

	var bossCount int
	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM explore_progress WHERE user_id=$1 AND completed_at IS NOT NULL AND stars>0`, uid).Scan(&bossCount); err != nil {
		return nil, err
	}
	stats["boss"] = float64(bossCount)

	var personalCount int
	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM personal_task_progress WHERE user_id=$1`, uid).Scan(&personalCount); err != nil {
		return nil, err
	}
	stats["personal"] = float64(personalCount)

	var cardCount int
	if err := h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM explore_progress WHERE user_id=$1 AND card_obtained=true`, uid).Scan(&cardCount); err != nil {
		return nil, err
	}
	stats["card"] = float64(cardCount)

	var exp int
	if err := h.db.QueryRow(ctx, `SELECT exp FROM users WHERE id=$1`, uid).Scan(&exp); err != nil {
		return nil, err
	}
	levels, err := h.levelConfigList(ctx)
	if err != nil {
		return nil, err
	}
	level, _, _, _ := computeLevel(exp, levels)
	stats["level"] = float64(level)

	return stats, nil
}

// checkAndAwardTitles best-effort：算統計值→依門檻發放未解鎖稱號→回傳「未看過」清單。
// 錯誤僅記 log、不中斷呼叫端（Dashboard 等流程）。
func (h *Handler) checkAndAwardTitles(ctx context.Context, uid string) []AwardedTitle {
	stats, err := h.titleCategoryStats(ctx, uid)
	if err != nil {
		log.Printf("checkAndAwardTitles: stats failed: %v", err)
		return nil
	}

	toAward, err := func() ([]string, error) {
		rows, err := h.db.Query(ctx, `SELECT code, category, threshold FROM title_defs WHERE enabled`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var code, category string
			var threshold float64
			if err := rows.Scan(&code, &category, &threshold); err != nil {
				return nil, err
			}
			if v, ok := stats[category]; ok && v >= threshold {
				out = append(out, code)
			}
		}
		return out, rows.Err()
	}()
	if err != nil {
		log.Printf("checkAndAwardTitles: load defs failed: %v", err)
		return nil
	}

	if len(toAward) > 0 {
		if _, err := h.db.Exec(ctx,
			`INSERT INTO user_titles (user_id, title_code) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`,
			uid, toAward); err != nil {
			log.Printf("checkAndAwardTitles: insert failed: %v", err)
			return nil
		}
	}

	rows, err := h.db.Query(ctx, `
		SELECT td.code, td.name, td.tier, td.category
		FROM user_titles ut JOIN title_defs td ON td.code = ut.title_code
		WHERE ut.user_id=$1 AND NOT ut.seen
		ORDER BY td.tier DESC, ut.earned_at`, uid)
	if err != nil {
		log.Printf("checkAndAwardTitles: unseen query failed: %v", err)
		return nil
	}
	defer rows.Close()
	var out []AwardedTitle
	for rows.Next() {
		var a AwardedTitle
		if err := rows.Scan(&a.Code, &a.Name, &a.Tier, &a.Category); err != nil {
			log.Printf("checkAndAwardTitles: scan unseen failed: %v", err)
			return nil
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		log.Printf("checkAndAwardTitles: unseen rows err: %v", err)
		return nil
	}
	return out
}

// titleCategoryLabels 固定順序（供前台分頁/分類展示）。
var titleCategoryLabels = []struct{ Key, Label string }{
	{"single_dist", "單次距離"},
	{"cum_dist", "累積距離"},
	{"cum_time", "累積時間"},
	{"checkin", "打卡地點"},
	{"boss", "關主挑戰"},
	{"personal", "個人任務"},
	{"level", "玩家等級"},
	{"card", "卡片收藏"},
}

// TitleRow 稱號圖鑑單筆（未解鎖時 Name 遮蔽為問號）。
type TitleRow struct {
	Code      string     `json:"code"`
	Category  string     `json:"category"`
	Name      string     `json:"name"`
	Tier      int        `json:"tier"`
	Threshold float64    `json:"threshold"`
	Unit      string     `json:"unit"`
	Earned    bool       `json:"earned"`
	EarnedAt  *time.Time `json:"earned_at,omitempty"`
}

// GET /api/v1/profile/titles — 稱號圖鑑（依 category, sort_order 排序；未解鎖遮蔽真名）。
func (h *Handler) Titles(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT td.code, td.category, td.name, td.tier, td.threshold, td.unit,
		       (ut.title_code IS NOT NULL), ut.earned_at
		FROM title_defs td
		LEFT JOIN user_titles ut ON ut.title_code = td.code AND ut.user_id = $1
		WHERE td.enabled
		ORDER BY td.sort_order`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []TitleRow{}
	for rows.Next() {
		var t TitleRow
		if err := rows.Scan(&t.Code, &t.Category, &t.Name, &t.Tier, &t.Threshold, &t.Unit, &t.Earned, &t.EarnedAt); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		if !t.Earned {
			t.Name = unlockedNamePlaceholder
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "scan failed")
		return
	}

	var displayed string
	if err := h.db.QueryRow(r.Context(), `SELECT COALESCE(displayed_title,'') FROM users WHERE id=$1`, uid).Scan(&displayed); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	cats := make([]map[string]string, 0, len(titleCategoryLabels))
	for _, c := range titleCategoryLabels {
		cats = append(cats, map[string]string{"key": c.Key, "label": c.Label})
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"categories": cats,
		"titles":     out,
		"displayed":  displayed,
	})
}

// POST /api/v1/profile/titles/display  body: {"code":"..."}（空字串=不展示）
func (h *Handler) SetDisplayedTitle(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Code != "" {
		var exists bool
		if err := h.db.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM user_titles WHERE user_id=$1 AND title_code=$2)`, uid, body.Code).Scan(&exists); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if !exists {
			respondErr(w, http.StatusBadRequest, "稱號尚未解鎖")
			return
		}
	}
	if _, err := h.db.Exec(r.Context(), `UPDATE users SET displayed_title=$1 WHERE id=$2`, body.Code, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /api/v1/profile/titles/seen  body: {"codes":["..."]}
func (h *Handler) MarkTitlesSeen(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var body struct {
		Codes []string `json:"codes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(body.Codes) > 0 {
		if _, err := h.db.Exec(r.Context(),
			`UPDATE user_titles SET seen=true WHERE user_id=$1 AND title_code=ANY($2)`, uid, body.Codes); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
