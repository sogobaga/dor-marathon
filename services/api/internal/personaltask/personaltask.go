// Package personaltask 個人任務系統（跑者生命週期 10 計畫 × 每 100 天鏈式任務）。
// 有別於①活動內任務②跑步隨機事件任務，此為第三套：個人化、每日一個、完成前一個才開下一個。
// Phase 0：資料模型 + 後台匯入（bulk upsert）+ 前台讀取/手動完成。自動里程結算列 Phase 2。
package personaltask

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

// --- JSON 型別 ---

type Plan struct {
	ID         string  `json:"id"`
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Lifecycle  string  `json:"lifecycle"`
	StageOrder int     `json:"stage_order"`
	TargetKm   float64 `json:"target_km"`
	TargetTime string  `json:"target_time"`
	EntryNote  string  `json:"entry_note"`
	DataSource string  `json:"data_source"`
	BannerURL  string  `json:"banner_url"`
	Enabled    bool    `json:"enabled"`
	Total      int     `json:"total"`     // 任務總數（前台摘要用）
	Completed  int     `json:"completed"` // 我完成數（前台摘要用）
}

type Task struct {
	ID               string          `json:"id"`
	PlanID           string          `json:"plan_id"`
	PlanCode         string          `json:"plan_code"`
	Day              int             `json:"day"`
	Week             int             `json:"week"`
	Title            string          `json:"title"`
	Story            string          `json:"story"`
	Workout          string          `json:"workout"`
	WorkoutType      string          `json:"workout_type"`
	TargetKm         float64         `json:"target_km"`
	TargetMin        int             `json:"target_min"`
	Intensity        string          `json:"intensity"`
	CompleteCond     string          `json:"complete_cond"`
	CompletionType   string          `json:"completion_type"`
	CompletionParams json.RawMessage `json:"completion_params"`
	RewardExp        int             `json:"reward_exp"`
	RewardDp         int             `json:"reward_dp"`
	IconURL          string          `json:"icon_url"`
	DataSource       string          `json:"data_source"`
	SafetyNote       string          `json:"safety_note"`
	Enabled          bool            `json:"enabled"`
	// 我的進度（前台計畫詳情用）
	Done  bool `json:"done"`
	Stars int  `json:"stars"`
}

// --- 路由 ---

// Router 前台（需登入）
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.ListPlans)                    // GET /personal-tasks — 10 計畫摘要 + 我的完成數
	r.Get("/plans/{code}", h.PlanDetail)       // GET /personal-tasks/plans/P01 — 計畫 + 100 任務 + 我的進度
	r.Post("/tasks/{id}/complete", h.Complete) // POST 手動完成（可帶 actual_km/pain/rpe）
	return r
}

// AdminRouter 後台（perm event_tasks）
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminList)     // 全部計畫 + 全部任務（供編輯 + 匯出）
	r.Post("/import", h.Import) // 由前台 SheetJS 解析 xlsx 後 bulk upsert
	return r
}

// --- 前台 handlers ---

func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	rows, err := h.db.Query(r.Context(), `
		SELECT p.id, p.code, p.name, p.lifecycle, p.stage_order, p.target_km, p.target_time, p.entry_note, p.data_source, p.banner_url, p.enabled,
		       (SELECT count(*) FROM personal_tasks t WHERE t.plan_id=p.id AND t.enabled) AS total,
		       (SELECT count(*) FROM personal_task_progress pr JOIN personal_tasks t ON t.id=pr.task_id
		         WHERE t.plan_id=p.id AND pr.user_id=$1) AS completed
		FROM personal_plans p WHERE p.enabled ORDER BY p.stage_order, p.code`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	plans := []Plan{}
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.Lifecycle, &p.StageOrder, &p.TargetKm, &p.TargetTime, &p.EntryNote, &p.DataSource, &p.BannerURL, &p.Enabled, &p.Total, &p.Completed); err != nil {
			continue
		}
		plans = append(plans, p)
	}
	respondJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

func (h *Handler) PlanDetail(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	code := chi.URLParam(r, "code")
	var p Plan
	if err := h.db.QueryRow(r.Context(),
		`SELECT id, code, name, lifecycle, stage_order, target_km, target_time, entry_note, data_source, banner_url, enabled
		 FROM personal_plans WHERE code=$1`, code).
		Scan(&p.ID, &p.Code, &p.Name, &p.Lifecycle, &p.StageOrder, &p.TargetKm, &p.TargetTime, &p.EntryNote, &p.DataSource, &p.BannerURL, &p.Enabled); err != nil {
		respondErr(w, http.StatusNotFound, "計畫不存在")
		return
	}
	tasks, err := h.queryTasks(r.Context(), `WHERE t.plan_id=$2 AND t.enabled ORDER BY t.seq, t.day`, uid, p.ID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"plan": p, "tasks": tasks})
}

type completeReq struct {
	ActualKm float64 `json:"actual_km"`
	Pain     int     `json:"pain"`
	Rpe      int     `json:"rpe"`
}

func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	taskID := chi.URLParam(r, "id")
	var req completeReq
	_ = json.NewDecoder(r.Body).Decode(&req) // 允許空 body（純手動完成）

	// 取任務目標 + 獎勵（同時擋前置未完成）
	var targetKm float64
	var rExp, rDp int
	var prereq *string
	if err := h.db.QueryRow(r.Context(),
		`SELECT target_km, reward_exp, reward_dp, prereq_task_id FROM personal_tasks WHERE id=$1 AND enabled`, taskID).
		Scan(&targetKm, &rExp, &rDp, &prereq); err != nil {
		respondErr(w, http.StatusNotFound, "任務不存在")
		return
	}
	// 前置任務未完成 → 擋（完成前一個才能開下一個）
	if prereq != nil {
		var done bool
		_ = h.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM personal_task_progress WHERE user_id=$1 AND task_id=$2)`, uid, *prereq).Scan(&done)
		if !done {
			respondErr(w, http.StatusConflict, "請先完成前一個任務")
			return
		}
	}
	// 星星（簡版）：完成=1★；實際里程≥目標=2★；≥目標×1.15=3★（無目標里程者=1★）
	stars := 1
	if targetKm > 0 && req.ActualKm >= targetKm {
		stars = 2
	}
	if targetKm > 0 && req.ActualKm >= targetKm*1.15 {
		stars = 3
	}
	evidence, _ := json.Marshal(map[string]any{"actual_km": req.ActualKm, "pain": req.Pain, "rpe": req.Rpe})

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(),
		`INSERT INTO personal_task_progress (user_id, task_id, status, stars, evidence, awarded, completed_at)
		 VALUES ($1,$2,'completed',$3,$4,TRUE,NOW()) ON CONFLICT (user_id, task_id) DO NOTHING`,
		uid, taskID, stars, evidence)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	firstTime := tag.RowsAffected() == 1
	if firstTime && (rExp > 0 || rDp > 0) {
		// 首次完成才發獎（冪等）；比照日常里程/事件直接加 users.exp/dp
		if _, err := tx.Exec(r.Context(), `UPDATE users SET exp = exp + $2, dp = dp + $3 WHERE id=$1`, uid, rExp, rDp); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"completed": true, "stars": stars,
		"reward_exp": ternInt(firstTime, rExp, 0), "reward_dp": ternInt(firstTime, rDp, 0),
		"already": !firstTime,
	})
}

// --- 後台 handlers ---

func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	prows, err := h.db.Query(r.Context(),
		`SELECT id, code, name, lifecycle, stage_order, target_km, target_time, entry_note, data_source, banner_url, enabled
		 FROM personal_plans ORDER BY stage_order, code`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer prows.Close()
	plans := []Plan{}
	for prows.Next() {
		var p Plan
		if err := prows.Scan(&p.ID, &p.Code, &p.Name, &p.Lifecycle, &p.StageOrder, &p.TargetKm, &p.TargetTime, &p.EntryNote, &p.DataSource, &p.BannerURL, &p.Enabled); err == nil {
			plans = append(plans, p)
		}
	}
	prows.Close()
	tasks, err := h.queryTasks(r.Context(), `WHERE 1=1 ORDER BY pl.stage_order, t.plan_id, t.seq, t.day`, "")
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"plans": plans, "tasks": tasks})
}

type importPlan struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	Lifecycle  string  `json:"lifecycle"`
	StageOrder int     `json:"stage_order"`
	TargetKm   float64 `json:"target_km"`
	TargetTime string  `json:"target_time"`
	EntryNote  string  `json:"entry_note"`
	DataSource string  `json:"data_source"`
}
type importTask struct {
	PlanCode     string  `json:"plan_code"`
	Day          int     `json:"day"`
	Week         int     `json:"week"`
	Title        string  `json:"title"`
	Workout      string  `json:"workout"`
	WorkoutType  string  `json:"workout_type"`
	TargetKm     float64 `json:"target_km"`
	TargetMin    int     `json:"target_min"`
	Intensity    string  `json:"intensity"`
	CompleteCond string  `json:"complete_cond"`
	RewardExp    int     `json:"reward_exp"`
	RewardDp     int     `json:"reward_dp"`
	DataSource   string  `json:"data_source"`
	SafetyNote   string  `json:"safety_note"`
}
type importReq struct {
	Plans []importPlan `json:"plans"`
	Tasks []importTask `json:"tasks"`
}

// Import 由前台 SheetJS 解析 xlsx 後送整包 → upsert（依 code / (plan,day) 更新，保留既有 id 與使用者進度）。
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	var req importReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.Plans) == 0 || len(req.Tasks) == 0 {
		respondErr(w, http.StatusBadRequest, "plans/tasks 不可為空")
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())

	code2id := map[string]string{}
	for _, p := range req.Plans {
		ds := p.DataSource
		if ds == "" {
			ds = "gps"
		}
		var id string
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO personal_plans (code, name, lifecycle, stage_order, target_km, target_time, entry_note, data_source)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			 ON CONFLICT (code) DO UPDATE SET name=$2, lifecycle=$3, stage_order=$4, target_km=$5, target_time=$6, entry_note=$7, data_source=$8
			 RETURNING id`,
			p.Code, p.Name, p.Lifecycle, p.StageOrder, p.TargetKm, p.TargetTime, p.EntryNote, ds).Scan(&id); err != nil {
			respondErr(w, http.StatusInternalServerError, "plan 匯入失敗："+p.Code)
			return
		}
		code2id[p.Code] = id
	}
	nTasks := 0
	for _, t := range req.Tasks {
		planID, ok := code2id[t.PlanCode]
		if !ok || t.Day <= 0 {
			continue // 略過對不到計畫或無 Day 的列
		}
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO personal_tasks (plan_id, day, week, seq, title, workout, workout_type, target_km, target_min, intensity, complete_cond, reward_exp, reward_dp, data_source, safety_note)
			 VALUES ($1,$2,$3,$2,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			 ON CONFLICT (plan_id, day) DO UPDATE SET
			   week=$3, seq=$2, title=$4, workout=$5, workout_type=$6, target_km=$7, target_min=$8, intensity=$9,
			   complete_cond=$10, reward_exp=$11, reward_dp=$12, data_source=$13, safety_note=$14`,
			planID, t.Day, t.Week, t.Title, t.Workout, t.WorkoutType, t.TargetKm, t.TargetMin, t.Intensity, t.CompleteCond, t.RewardExp, t.RewardDp, t.DataSource, t.SafetyNote); err != nil {
			respondErr(w, http.StatusInternalServerError, "task 匯入失敗")
			return
		}
		nTasks++
	}
	// 前置任務鏈：每個 task 的 prereq = 同計畫前一天；第一天為 NULL（保留 story/icon 等後台另填欄位不動）
	if _, err := tx.Exec(r.Context(),
		`UPDATE personal_tasks t SET prereq_task_id = p.id FROM personal_tasks p
		 WHERE t.plan_id = p.plan_id AND p.day = t.day - 1`); err != nil {
		respondErr(w, http.StatusInternalServerError, "prereq 連結失敗")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE personal_tasks SET prereq_task_id = NULL WHERE day = 1`); err != nil {
		respondErr(w, http.StatusInternalServerError, "prereq 連結失敗")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "commit failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"plans": len(req.Plans), "tasks": nTasks})
}

// --- 共用 ---

// queryTasks 依 where/args 撈任務 + 我的進度。uid 固定為 $1；where 內的參數請從 $2 起算。
// uid 空字串＝後台不看進度（用零值 UUID，永不命中任何使用者進度）。
func (h *Handler) queryTasks(ctx context.Context, whereOrder string, uid string, args ...any) ([]Task, error) {
	if uid == "" {
		uid = "00000000-0000-0000-0000-000000000000"
	}
	q := `SELECT t.id, t.plan_id, pl.code, t.day, t.week, t.title, t.story, t.workout, t.workout_type, t.target_km, t.target_min,
	             t.intensity, t.complete_cond, t.completion_type, t.completion_params, t.reward_exp, t.reward_dp, t.icon_url, t.data_source, t.safety_note, t.enabled,
	             (pr.user_id IS NOT NULL) AS done, COALESCE(pr.stars,0) AS stars
	      FROM personal_tasks t JOIN personal_plans pl ON pl.id = t.plan_id
	      LEFT JOIN personal_task_progress pr ON pr.task_id = t.id AND pr.user_id = $1 ` + whereOrder
	fullArgs := append([]any{uid}, args...)
	rows, err := h.db.Query(ctx, q, fullArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.PlanID, &t.PlanCode, &t.Day, &t.Week, &t.Title, &t.Story, &t.Workout, &t.WorkoutType, &t.TargetKm, &t.TargetMin,
			&t.Intensity, &t.CompleteCond, &t.CompletionType, &t.CompletionParams, &t.RewardExp, &t.RewardDp, &t.IconURL, &t.DataSource, &t.SafetyNote, &t.Enabled,
			&t.Done, &t.Stars); err != nil {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}

func ternInt(cond bool, a, b int) int {
	if cond {
		return a
	}
	return b
}
