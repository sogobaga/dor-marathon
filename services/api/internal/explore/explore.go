// Package explore 城市探索：打卡點關主挑戰 + 卡片收集。
// 綁在玩家個人身上、與賽事無關、全免費、可持續擴充。每個打卡點＝一位關主（結構化課表挑戰，比照個人任務）。
// Phase 1：資料模型 + 後台 CRUD（含 Scene/Card 圖）+ 前台列表(圖鑑/探索用)。挑戰流程(接受/完成)於 Phase 3。
package explore

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

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
	r.Post("/{id}/checkin", h.Checkin)   // 到打卡點打卡 → 揭露關主
	r.Post("/{id}/accept", h.Accept)     // 接受挑戰（扣 DP=難度×10）
	r.Post("/{id}/complete", h.Complete) // 完成挑戰（得星、3★ 取卡）
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

type checkinReq struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
	Acc float64 `json:"acc"`
}

// Checkin POST /explore/{id}/checkin — 到打卡點打卡：驗地理圍欄 → 設 discovered(揭露關主) → 回關主完整資料。
func (h *Handler) Checkin(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	bossID := chi.URLParam(r, "id")
	var req checkinReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	b, err := scanBoss(h.db.QueryRow(r.Context(), `SELECT `+bossCols+` FROM explore_bosses WHERE id=$1 AND enabled`, bossID))
	if err != nil {
		respondErr(w, http.StatusNotFound, "打卡點不存在")
		return
	}
	if req.Acc > 65 {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "status": "low_accuracy", "message": "GPS 精度不足，請到較空曠處再試"})
		return
	}
	dist := haversineM(req.Lat, req.Lng, b.Lat, b.Lng)
	radius := b.RadiusM
	if radius <= 0 {
		radius = 40
	}
	if dist > float64(radius)+req.Acc {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "status": "out_of_range", "distance_m": dist, "message": fmt.Sprintf("還沒到打卡點（距離約 %d 公尺）", int(dist))})
		return
	}
	// 打卡成功 → 揭露關主（discovered=TRUE）
	if _, err := h.db.Exec(r.Context(), `
		INSERT INTO explore_progress (user_id, boss_id, discovered) VALUES ($1,$2,TRUE)
		ON CONFLICT (user_id, boss_id) DO UPDATE SET discovered=TRUE`, uid, bossID); err != nil {
		respondErr(w, http.StatusInternalServerError, "打卡失敗")
		return
	}
	b.Discovered = true
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "verified", "distance_m": dist, "boss": b})
}

// workoutStars 課表星數：需完成整份(finished)；work 段全在配速區間=3★、部分=2★、只完成=1★。
func workoutStars(finished bool, workInBand, workTotal int) int {
	if !finished {
		return 0
	}
	if workTotal > 0 {
		if workInBand >= workTotal {
			return 3
		}
		if workInBand > 0 {
			return 2
		}
	}
	return 1
}

// Accept POST /explore/{id}/accept — 接受挑戰：需已打卡揭露、扣 DP(難度×10 或 retry_dp_cost)、設 active。
func (h *Handler) Accept(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	bossID := chi.URLParam(r, "id")
	var difficulty, retryDp int
	var enabled bool
	if err := h.db.QueryRow(r.Context(), `SELECT difficulty_stars, retry_dp_cost, enabled FROM explore_bosses WHERE id=$1`, bossID).Scan(&difficulty, &retryDp, &enabled); err != nil || !enabled {
		respondErr(w, http.StatusNotFound, "關主不存在")
		return
	}
	var discovered, cardObtained, alreadyActive bool
	var best int
	_ = h.db.QueryRow(r.Context(), `SELECT COALESCE(discovered,FALSE), COALESCE(card_obtained,FALSE), COALESCE(active,FALSE), COALESCE(stars,0) FROM explore_progress WHERE user_id=$1 AND boss_id=$2`, uid, bossID).Scan(&discovered, &cardObtained, &alreadyActive, &best)
	if !discovered {
		respondErr(w, http.StatusConflict, "請先到打卡點打卡")
		return
	}
	if cardObtained {
		respondErr(w, http.StatusConflict, "已收服此關主，卡片已收藏")
		return
	}
	// 冪等：已接受且進行中（尚未完成）→ 不重複扣 DP，直接放行去挑戰
	if alreadyActive {
		respondJSON(w, http.StatusOK, map[string]any{"ok": true, "tier": best + 1, "charged_dp": 0, "resumed": true})
		return
	}
	cost := difficulty * 10
	if retryDp > 0 {
		cost = retryDp
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())
	if cost > 0 {
		tag, err := tx.Exec(r.Context(), `UPDATE users SET dp=dp-$2 WHERE id=$1 AND dp>=$2`, uid, cost)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if tag.RowsAffected() == 0 {
			respondErr(w, http.StatusConflict, fmt.Sprintf("DP 不足（需 %d）", cost))
			return
		}
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO explore_progress (user_id, boss_id, active, challenge_started_at, attempts, discovered)
		VALUES ($1,$2,TRUE,NOW(),1,TRUE)
		ON CONFLICT (user_id, boss_id) DO UPDATE SET active=TRUE, challenge_started_at=NOW(), attempts=explore_progress.attempts+1, discovered=TRUE`, uid, bossID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "tier": best + 1, "charged_dp": cost})
}

type completeReq struct {
	Finished   bool `json:"finished"`
	WorkInBand int  `json:"work_in_band"`
	WorkTotal  int  `json:"work_total"`
}

// Complete POST /explore/{id}/complete — 完成挑戰：得星、3★ 取卡、發獎(冪等)。疑似載具不計。
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	bossID := chi.URLParam(r, "id")
	var req completeReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	var active bool
	var best, awarded, rExp, rDp int
	if err := h.db.QueryRow(r.Context(), `
		SELECT COALESCE(pr.active,FALSE), COALESCE(pr.stars,0), COALESCE(pr.awarded_stars,0), b.reward_exp, b.reward_dp
		FROM explore_bosses b LEFT JOIN explore_progress pr ON pr.boss_id=b.id AND pr.user_id=$1
		WHERE b.id=$2`, uid, bossID).Scan(&active, &best, &awarded, &rExp, &rDp); err != nil {
		respondErr(w, http.StatusNotFound, "關主不存在")
		return
	}
	if !active {
		respondErr(w, http.StatusConflict, "尚未開始挑戰")
		return
	}
	// 反作弊：本趟若被判定疑似載具 → 不計成績（與里程/個人任務一致）
	var vehFlag bool
	_ = h.db.QueryRow(r.Context(), `SELECT COALESCE(flagged,FALSE) FROM gps_runs WHERE user_id=$1 AND ended_at > NOW()-INTERVAL '20 minutes' ORDER BY ended_at DESC LIMIT 1`, uid).Scan(&vehFlag)
	if vehFlag {
		respondErr(w, http.StatusConflict, "本趟疑似使用交通工具，挑戰成績不計")
		return
	}
	cStars := workoutStars(req.Finished, req.WorkInBand, req.WorkTotal)
	if cStars == 0 {
		respondErr(w, http.StatusConflict, "尚未完成整份課表")
		return
	}
	newStars := best
	if cStars > newStars {
		newStars = cStars
	}
	cardObtained := newStars >= 3
	grant := newStars > awarded
	newAwarded := awarded
	if grant && newStars > newAwarded {
		newAwarded = newStars
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		UPDATE explore_progress SET active=FALSE, stars=$3, awarded_stars=$4, card_obtained=$5, completed_at=NOW(),
			card_obtained_at=CASE WHEN $5 AND card_obtained_at IS NULL THEN NOW() ELSE card_obtained_at END
		WHERE user_id=$1 AND boss_id=$2`, uid, bossID, newStars, newAwarded, cardObtained); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if grant && (rExp > 0 || rDp > 0) {
		if _, err := tx.Exec(r.Context(), `UPDATE users SET exp=exp+$2, dp=dp+$3 WHERE id=$1`, uid, rExp, rDp); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"completed": true, "stars": newStars, "card_obtained": cardObtained,
		"reward_exp": ternInt(grant, rExp, 0), "reward_dp": ternInt(grant, rDp, 0),
	})
}

func ternInt(c bool, a, b int) int {
	if c {
		return a
	}
	return b
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
