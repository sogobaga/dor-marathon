// Package explore 城市探索：打卡點關主挑戰 + 卡片收集。
// 綁在玩家個人身上、與賽事無關、全免費、可持續擴充。每個打卡點＝一位關主（結構化課表挑戰，比照個人任務）。
// Phase 1：資料模型 + 後台 CRUD（含 Scene/Card 圖）+ 前台列表(圖鑑/探索用)。挑戰流程(接受/完成)於 Phase 3。
package explore

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

type Boss struct {
	ID              string          `json:"id"`
	Code            string          `json:"code"`
	Name            string          `json:"name"`
	Title           string          `json:"title"`
	Region          string          `json:"region"`
	Place           string          `json:"place"`
	Gender          string          `json:"gender"`
	Age             int             `json:"age"`
	WorkoutLabel    string          `json:"workout_label"`
	DifficultyStars int             `json:"difficulty_stars"`
	Quote           string          `json:"quote"`
	SkillName       string          `json:"skill_name"`
	SkillDesc       string          `json:"skill_desc"`
	DialogueIntro   string          `json:"dialogue_intro"`
	DialogueStart   string          `json:"dialogue_start"`
	SceneImageURL   string          `json:"scene_image_url"`
	CardImageURL    string          `json:"card_image_url"`
	Lat             float64         `json:"lat"`
	Lng             float64         `json:"lng"`
	RadiusM         int             `json:"radius_m"`
	RewardExp       int             `json:"reward_exp"`
	RewardDp        int             `json:"reward_dp"`
	RetryDpCost     int             `json:"retry_dp_cost"`
	WorkoutKind     string          `json:"workout_kind"`
	Segments        json.RawMessage `json:"segments"`
	DataSource      string          `json:"data_source"`
	DisplayOrder    int             `json:"display_order"`
	Enabled         bool            `json:"enabled"`
	// 玩家進度（前台）
	Stars        int  `json:"stars"`
	CardObtained bool `json:"card_obtained"`
	Active       bool `json:"active"`
	Attempts     int  `json:"attempts"`
	Discovered   bool `json:"discovered"` // 已打卡揭露關主（未揭露則前台只顯示地點、其餘欄位遮蔽）
}

const bossCols = `id, code, name, title, region, place, gender, age, workout_label, difficulty_stars,
	quote, skill_name, skill_desc, dialogue_intro, dialogue_start, scene_image_url, card_image_url,
	lat, lng, radius_m, reward_exp, reward_dp, retry_dp_cost, workout_kind, segments, data_source, display_order, enabled`

func scanBoss(row interface{ Scan(...any) error }) (Boss, error) {
	var b Boss
	err := row.Scan(&b.ID, &b.Code, &b.Name, &b.Title, &b.Region, &b.Place, &b.Gender, &b.Age, &b.WorkoutLabel, &b.DifficultyStars,
		&b.Quote, &b.SkillName, &b.SkillDesc, &b.DialogueIntro, &b.DialogueStart, &b.SceneImageURL, &b.CardImageURL,
		&b.Lat, &b.Lng, &b.RadiusM, &b.RewardExp, &b.RewardDp, &b.RetryDpCost, &b.WorkoutKind, &b.Segments, &b.DataSource, &b.DisplayOrder, &b.Enabled)
	return b, err
}

// --- 路由 ---

// Router 前台（需登入）：列出啟用中的關主 + 我的進度（供城市探索頁 / 卡片圖鑑）。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	return r
}

// AdminRouter 後台（perm event_tasks，沿用）：關主 CRUD。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminList)
	r.Post("/", h.Save)
	r.Post("/{id}/delete", h.Delete)
	return r
}

// maskBoss 未揭露(未打卡)→ 只保留地點(place/region/lat/lng/radius)與進度，遮蔽關主身分/圖/難度/課表/對話。
func maskBoss(b *Boss) {
	b.Code, b.Name, b.Title, b.Gender, b.WorkoutLabel = "", "", "", "", ""
	b.Age, b.DifficultyStars, b.RewardExp, b.RewardDp, b.RetryDpCost = 0, 0, 0, 0, 0
	b.Quote, b.SkillName, b.SkillDesc, b.DialogueIntro, b.DialogueStart = "", "", "", "", ""
	b.SceneImageURL, b.CardImageURL, b.WorkoutKind = "", "", ""
	b.Segments = json.RawMessage("[]")
}

// --- 前台 handlers ---

// List GET /explore — 啟用中的關主（含我的進度：星數/是否已取得卡片/進行中）。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		uid = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT `+bossCols+`,
		       COALESCE(pr.stars,0), COALESCE(pr.card_obtained,FALSE), COALESCE(pr.active,FALSE), COALESCE(pr.attempts,0), COALESCE(pr.discovered,FALSE)
		FROM explore_bosses b
		LEFT JOIN explore_progress pr ON pr.boss_id=b.id AND pr.user_id=$1
		WHERE b.enabled
		ORDER BY b.display_order, b.code`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []Boss{}
	for rows.Next() {
		var b Boss
		var disc bool
		if err := rows.Scan(&b.ID, &b.Code, &b.Name, &b.Title, &b.Region, &b.Place, &b.Gender, &b.Age, &b.WorkoutLabel, &b.DifficultyStars,
			&b.Quote, &b.SkillName, &b.SkillDesc, &b.DialogueIntro, &b.DialogueStart, &b.SceneImageURL, &b.CardImageURL,
			&b.Lat, &b.Lng, &b.RadiusM, &b.RewardExp, &b.RewardDp, &b.RetryDpCost, &b.WorkoutKind, &b.Segments, &b.DataSource, &b.DisplayOrder, &b.Enabled,
			&b.Stars, &b.CardObtained, &b.Active, &b.Attempts, &disc); err != nil {
			continue
		}
		// 已揭露＝已打卡(discovered) 或 已挑戰(stars>0) 或 已取得卡片。未揭露→只留地點、遮蔽關主資料(伺服器端，devtools 也看不到)。
		b.Discovered = disc || b.CardObtained || b.Stars > 0
		if !b.Discovered {
			maskBoss(&b)
		}
		out = append(out, b)
	}
	respondJSON(w, http.StatusOK, map[string]any{"bosses": out})
}

// --- 後台 handlers ---

func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT `+bossCols+` FROM explore_bosses ORDER BY display_order, code`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []Boss{}
	for rows.Next() {
		if b, err := scanBoss(rows); err == nil {
			out = append(out, b)
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"bosses": out})
}

// Save POST /admin/explore — 依 code 新增/更新關主。
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	var b Boss
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if b.Code == "" {
		respondErr(w, http.StatusBadRequest, "缺少關主編號 code")
		return
	}
	if len(b.Segments) == 0 {
		b.Segments = json.RawMessage("[]")
	}
	if b.DataSource == "" {
		b.DataSource = "gps"
	}
	var id string
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO explore_bosses (code, name, title, region, place, gender, age, workout_label, difficulty_stars,
			quote, skill_name, skill_desc, dialogue_intro, dialogue_start, scene_image_url, card_image_url,
			lat, lng, radius_m, reward_exp, reward_dp, retry_dp_cost, workout_kind, segments, data_source, display_order, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
		ON CONFLICT (code) DO UPDATE SET name=$2, title=$3, region=$4, place=$5, gender=$6, age=$7, workout_label=$8, difficulty_stars=$9,
			quote=$10, skill_name=$11, skill_desc=$12, dialogue_intro=$13, dialogue_start=$14, scene_image_url=$15, card_image_url=$16,
			lat=$17, lng=$18, radius_m=$19, reward_exp=$20, reward_dp=$21, retry_dp_cost=$22, workout_kind=$23, segments=$24, data_source=$25, display_order=$26, enabled=$27
		RETURNING id`,
		b.Code, b.Name, b.Title, b.Region, b.Place, b.Gender, b.Age, b.WorkoutLabel, b.DifficultyStars,
		b.Quote, b.SkillName, b.SkillDesc, b.DialogueIntro, b.DialogueStart, b.SceneImageURL, b.CardImageURL,
		b.Lat, b.Lng, b.RadiusM, b.RewardExp, b.RewardDp, b.RetryDpCost, b.WorkoutKind, b.Segments, b.DataSource, b.DisplayOrder, b.Enabled).Scan(&id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "儲存失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"id": id})
}

// Delete POST /admin/explore/{id}/delete — 刪除關主（連帶清進度）。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `DELETE FROM explore_bosses WHERE id=$1`, id); err != nil {
		respondErr(w, http.StatusInternalServerError, "刪除失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
