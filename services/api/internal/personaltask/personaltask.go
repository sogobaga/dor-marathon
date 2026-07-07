// Package personaltask 個人任務系統（跑者生命週期 10 計畫 × 每 100 天鏈式任務）。
// 有別於①活動內任務②跑步隨機事件任務，此為第三套：個人化、每日一個、完成前一個才開下一個。
//
// 挑戰制（v0.1.248+）：每個任務要先按「挑戰」才開始計算；里程從挑戰起累積、達標後「完成」才可按；
// 「放棄」判失敗可重挑。可重複挑戰爬星 1→3★，每爬一星目標變硬（tierMult）；休息日＝挑戰後窗口內
// 不能有任何里程，安靜度過才算成功。第一次挑戰免費，之後每次重挑扣該任務 retry_dp_cost（預設 10）。
package personaltask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

// 挑戰制參數（未來可移到後台設定）
const restWindowMin = 10 // 休息日「不能有里程」的基準窗口（分鐘）；測試短窗

// tierMult 星級目標倍率：1★×1.00、2★×1.15、3★×1.30（index = tier 1..3）。
var tierMult = [4]float64{0, 1.00, 1.15, 1.30}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func mult(tier int) float64 {
	if tier >= 1 && tier <= 3 {
		return tierMult[tier]
	}
	return 1.0
}

// kindOf 任務型別：有分段課表(workout_kind)=workout（帶到 GPS 追蹤跑）；否則有目標里程=mileage；
// 工作表類型含「休息」=rest；其餘=manual。
func kindOf(targetKm float64, workoutKind, workoutType string) string {
	if workoutKind != "" {
		return "workout"
	}
	if targetKm > 0 {
		return "mileage"
	}
	if strings.Contains(workoutType, "休息") {
		return "rest"
	}
	return "manual"
}

// workoutStars 結構化課表星數：需完成整份課表(finished)；work 段全在配速區間=3★、部分=2★、只完成=1★。
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
	WorkoutKind      string          `json:"workout_kind"` // 非空＝結構化課表（帶到 GPS 追蹤跑）
	Segments         json.RawMessage `json:"segments"`      // 分段課表
	// 我的進度 + 挑戰制狀態（前台按鈕流用）
	Done              bool    `json:"done"`                // 已完成至少 1★
	Stars             int     `json:"stars"`               // 最高星數 0..3
	Attempts          int     `json:"attempts"`            // 已開始挑戰次數（>0 → 下次挑戰要付 DP）
	Active            bool    `json:"active"`              // 有進行中的挑戰
	ChallengeTier     int     `json:"challenge_tier"`      // 進行中挑戰的星級
	ChallengeTargetKm float64 `json:"challenge_target_km"` // 進行中挑戰的縮放目標
	RetryDpCost       int     `json:"retry_dp_cost"`       // 重挑 DP 花費
}

// ChallengeState 進行中挑戰的即時狀態（前台顯示進度/可否完成/是否失敗）。
type ChallengeState struct {
	TaskID      string          `json:"task_id"`
	PlanCode    string          `json:"plan_code"`
	Day         int             `json:"day"`
	Title       string          `json:"title"`
	Kind        string          `json:"kind"` // mileage | rest | manual | workout
	Tier        int             `json:"tier"`
	TargetKm    float64         `json:"target_km"`     // 縮放後目標（mileage）
	AccKm       float64         `json:"acc_km"`        // 累積里程（mileage）／窗口內偵測到的里程（rest）
	DataSource  string          `json:"data_source"`   // gps | strava
	RestWindowS int             `json:"rest_window_s"` // 休息窗口秒數（rest）
	ElapsedS    int             `json:"elapsed_s"`     // 已過秒數（rest）
	Met         bool            `json:"met"`           // 條件達成 → 完成可按
	Failed      bool            `json:"failed"`        // 休息窗口內偵測到里程 → 需重挑
	WorkoutKind string          `json:"workout_kind"`  // workout：課表型別
	Segments    json.RawMessage `json:"segments"`      // workout：分段課表（給 /track 驅動）
}

// --- 路由 ---

// Router 前台（需登入）
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.ListPlans)                      // GET /personal-tasks — 計畫摘要 + 我的完成數
	r.Get("/plans/{code}", h.PlanDetail)         // GET /personal-tasks/plans/P01 — 計畫 + 任務 + 我的進度/挑戰狀態
	r.Post("/status", h.Status)                  // POST 進行中挑戰的即時狀態（開頁/輪詢/跑步後呼叫）
	r.Post("/tasks/{id}/challenge", h.Challenge) // POST 開始挑戰（第一次免費、之後扣 DP）
	r.Post("/tasks/{id}/abandon", h.Abandon)     // POST 放棄 → 判失敗、可重挑
	r.Post("/tasks/{id}/complete", h.Complete)   // POST 完成（僅達標可完成；發星 + 獎勵）
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
		         WHERE t.plan_id=p.id AND pr.user_id=$1 AND pr.stars>0) AS completed
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

// isFrontierLocked 全域順序鏈：若目標任務尚未完成(stars=0)且前面（stage_order, code, day 較小）還有未完成任務 → 鎖住。
// 已完成過的任務（stars>0，重挑爬星）不受鏈限制。
func (h *Handler) isFrontierLocked(ctx context.Context, uid, taskID string) (bool, error) {
	var priorLeft int
	err := h.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM personal_tasks t JOIN personal_plans pl ON pl.id=t.plan_id
		JOIN personal_tasks tt ON tt.id=$2
		JOIN personal_plans pp ON pp.id=tt.plan_id
		WHERE t.enabled AND pl.enabled
		  AND (pl.stage_order, pl.code, t.day) < (pp.stage_order, pp.code, tt.day)
		  AND NOT EXISTS (SELECT 1 FROM personal_task_progress pr WHERE pr.user_id=$1 AND pr.task_id=t.id AND pr.stars>0)`,
		uid, taskID).Scan(&priorLeft)
	return priorLeft > 0, err
}

// Challenge POST /personal-tasks/tasks/{id}/challenge — 開始挑戰。
func (h *Handler) Challenge(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	taskID := chi.URLParam(r, "id")

	var baseTarget float64
	var retryDp int
	var dataSource, workoutType, workoutKind string
	if err := h.db.QueryRow(r.Context(),
		`SELECT target_km, retry_dp_cost, data_source, workout_type, workout_kind FROM personal_tasks WHERE id=$1 AND enabled`, taskID).
		Scan(&baseTarget, &retryDp, &dataSource, &workoutType, &workoutKind); err != nil {
		respondErr(w, http.StatusNotFound, "任務不存在")
		return
	}
	// 我對此任務的進度
	var bestStars, attempts int
	var active bool
	_ = h.db.QueryRow(r.Context(),
		`SELECT COALESCE(stars,0), COALESCE(attempts,0), COALESCE(active,FALSE) FROM personal_task_progress WHERE user_id=$1 AND task_id=$2`,
		uid, taskID).Scan(&bestStars, &attempts, &active)
	if active { // 已在挑戰中 → 冪等，直接回目前狀態
		st, _ := ChallengeStatusUser(r.Context(), h.db, uid)
		respondJSON(w, http.StatusOK, map[string]any{"already": true, "challenge": st})
		return
	}
	if bestStars >= 3 {
		respondErr(w, http.StatusConflict, "已達 3★ 上限，無需再挑戰")
		return
	}
	// 只允許同時一個進行中的挑戰
	var other int
	_ = h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM personal_task_progress WHERE user_id=$1 AND active`, uid).Scan(&other)
	if other > 0 {
		respondErr(w, http.StatusConflict, "你有其他進行中的挑戰，請先完成或放棄")
		return
	}
	// 全域順序鏈：未完成任務要照順序
	if bestStars == 0 {
		locked, err := h.isFrontierLocked(r.Context(), uid, taskID)
		if err == nil && locked {
			respondErr(w, http.StatusConflict, "請先完成前面的任務")
			return
		}
	}
	kind := kindOf(baseTarget, workoutKind, workoutType)
	tier := bestStars + 1
	scaled := 0.0
	if kind == "mileage" {
		scaled = round1(baseTarget * mult(tier)) // 只有里程任務才縮放目標；workout/rest 不用
	}
	cost := 0
	if attempts > 0 { // 非第一次挑戰 → 付費重挑
		cost = retryDp
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())
	if cost > 0 {
		tag, err := tx.Exec(r.Context(), `UPDATE users SET dp = dp - $2 WHERE id=$1 AND dp >= $2`, uid, cost)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if tag.RowsAffected() != 1 {
			respondErr(w, http.StatusConflict, fmt.Sprintf("DP 不足，重新挑戰需 %d DP", cost))
			return
		}
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO personal_task_progress (user_id, task_id, status, stars, attempts, active, challenge_tier, challenge_target_km, challenge_started_at, awarded_stars)
		VALUES ($1,$2,'challenging',0,1,TRUE,$3,$4,NOW(),0)
		ON CONFLICT (user_id, task_id) DO UPDATE SET
		  status='challenging', active=TRUE, challenge_tier=$3, challenge_target_km=$4, challenge_started_at=NOW(),
		  attempts = personal_task_progress.attempts + 1`,
		uid, taskID, tier, scaled); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"challenging": true, "tier": tier, "kind": kind, "target_km": scaled,
		"charged_dp": cost, "rest_window_s": int(math.Round(float64(restWindowMin*60) * mult(tier))),
	})
}

// Abandon POST /personal-tasks/tasks/{id}/abandon — 放棄（判失敗，可重挑；已付的 DP 不退）。
func (h *Handler) Abandon(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	taskID := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `
		UPDATE personal_task_progress
		SET active=FALSE, challenge_tier=0, challenge_target_km=0, challenge_started_at=NULL,
		    status = CASE WHEN stars>0 THEN 'completed' ELSE 'available' END
		WHERE user_id=$1 AND task_id=$2`, uid, taskID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type completeReq struct {
	Pain int `json:"pain"`
	Rpe  int `json:"rpe"`
	// workout（結構化課表）完成回報：由 /track 分段引擎送
	Finished   bool            `json:"finished"`     // 是否完成整份課表（距離/時間）
	WorkInBand int             `json:"work_in_band"` // work 段落在配速區間的數量
	WorkTotal  int             `json:"work_total"`   // work 段總數
	Evidence   json.RawMessage `json:"evidence"`     // 逐段明細（存證）
}

// Complete POST /personal-tasks/tasks/{id}/complete — 完成（僅在挑戰達標時可完成）。
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	taskID := chi.URLParam(r, "id")
	var req completeReq
	_ = json.NewDecoder(r.Body).Decode(&req) // 允許空 body

	// 進度（必須有進行中的挑戰）
	var bestStars, awardedStars, tier int
	var active bool
	var chalTarget float64
	var startedAt time.Time
	if err := h.db.QueryRow(r.Context(),
		`SELECT COALESCE(stars,0), COALESCE(awarded_stars,0), COALESCE(active,FALSE), COALESCE(challenge_tier,0), COALESCE(challenge_target_km,0), challenge_started_at
		 FROM personal_task_progress WHERE user_id=$1 AND task_id=$2`, uid, taskID).
		Scan(&bestStars, &awardedStars, &active, &tier, &chalTarget, &startedAt); err != nil {
		respondErr(w, http.StatusConflict, "尚未開始挑戰")
		return
	}
	if !active {
		respondErr(w, http.StatusConflict, "尚未開始挑戰")
		return
	}
	// 任務資料
	var baseTarget float64
	var rExp, rDp int
	var dataSource, workoutType, workoutKind string
	if err := h.db.QueryRow(r.Context(),
		`SELECT target_km, reward_exp, reward_dp, data_source, workout_type, workout_kind FROM personal_tasks WHERE id=$1 AND enabled`, taskID).
		Scan(&baseTarget, &rExp, &rDp, &dataSource, &workoutType, &workoutKind); err != nil {
		respondErr(w, http.StatusNotFound, "任務不存在")
		return
	}
	kind := kindOf(baseTarget, workoutKind, workoutType)
	acc := 0.0
	now := time.Now()
	cStars := tier // 本次挑戰達成的星數（依 kind 決定）
	switch kind {
	case "mileage":
		a, err := accDistance(r.Context(), h.db, uid, startedAt, dataSource)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		acc = a
		if acc < chalTarget {
			respondErr(w, http.StatusConflict, fmt.Sprintf("尚未達標（%.1f/%.1f K）", acc, chalTarget))
			return
		}
		cStars = tier
	case "rest":
		windowS := int(math.Round(float64(restWindowMin*60) * mult(tier)))
		end := startedAt.Add(time.Duration(windowS) * time.Second)
		to := now
		if to.After(end) {
			to = end
		}
		a, err := accAnyDistance(r.Context(), h.db, uid, startedAt, to)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if a > 0 { // 窗口內出現里程 → 失敗，標記需重挑
			_, _ = h.db.Exec(r.Context(), `UPDATE personal_task_progress SET active=FALSE, status=CASE WHEN stars>0 THEN 'completed' ELSE 'available' END, challenge_started_at=NULL WHERE user_id=$1 AND task_id=$2`, uid, taskID)
			respondErr(w, http.StatusConflict, "挑戰失敗：休息窗口內偵測到里程，請重新挑戰")
			return
		}
		if now.Before(end) {
			left := int(math.Ceil(end.Sub(now).Minutes()))
			respondErr(w, http.StatusConflict, fmt.Sprintf("休息窗口尚未結束，還要 %d 分", left))
			return
		}
		cStars = 3 // 休息日完成＝直接 3★（無 1/2/3 成長）
	case "workout":
		cStars = workoutStars(req.Finished, req.WorkInBand, req.WorkTotal)
		if cStars == 0 {
			respondErr(w, http.StatusConflict, "尚未完成整份課表")
			return
		}
	}
	// 完成 → 取「本次達成」與「歷史最高」的大者
	newStars := bestStars
	if cStars > newStars {
		newStars = cStars
	}
	grant := newStars > awardedStars // 爬到「新的最高星」才發一次基準獎勵（冪等）
	evidence, _ := json.Marshal(map[string]any{"tier": tier, "kind": kind, "stars": cStars, "acc_km": acc, "pain": req.Pain, "rpe": req.Rpe, "work_in_band": req.WorkInBand, "work_total": req.WorkTotal})

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())
	newAwarded := awardedStars
	if grant && newStars > newAwarded {
		newAwarded = newStars
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE personal_task_progress
		SET status='completed', active=FALSE, stars=$3, awarded_stars=$4, evidence=$5, awarded=TRUE, completed_at=NOW(),
		    challenge_tier=0, challenge_target_km=0, challenge_started_at=NULL
		WHERE user_id=$1 AND task_id=$2`, uid, taskID, newStars, newAwarded, evidence); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if grant && (rExp > 0 || rDp > 0) {
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
		"completed": true, "stars": newStars, "tier": tier,
		"reward_exp": ternInt(grant, rExp, 0), "reward_dp": ternInt(grant, rDp, 0),
	})
}

// --- 挑戰即時狀態 ---

// ChallengeStatusUser 回傳使用者「進行中挑戰」的即時狀態（無則 nil）。package 函式便於他處（如首頁）呼叫。
func ChallengeStatusUser(ctx context.Context, db *pgxpool.Pool, uid string) (*ChallengeState, error) {
	var taskID, planCode, title, dataSource, workoutType, workoutKind string
	var day, tier int
	var baseTarget, chalTarget float64
	var startedAt time.Time
	var segments json.RawMessage
	err := db.QueryRow(ctx, `
		SELECT t.id, pl.code, t.day, t.title, t.target_km, t.data_source, t.workout_type, t.workout_kind, t.segments,
		       COALESCE(pr.challenge_tier,0), COALESCE(pr.challenge_target_km,0), pr.challenge_started_at
		FROM personal_task_progress pr
		JOIN personal_tasks t ON t.id = pr.task_id
		JOIN personal_plans pl ON pl.id = t.plan_id
		WHERE pr.user_id=$1 AND pr.active
		LIMIT 1`, uid).Scan(&taskID, &planCode, &day, &title, &baseTarget, &dataSource, &workoutType, &workoutKind, &segments, &tier, &chalTarget, &startedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	kind := kindOf(baseTarget, workoutKind, workoutType)
	st := &ChallengeState{TaskID: taskID, PlanCode: planCode, Day: day, Title: title, Kind: kind, Tier: tier, DataSource: dataSource}
	now := time.Now()
	switch kind {
	case "workout":
		st.WorkoutKind = workoutKind
		st.Segments = segments
		// 完成與否由 /track 分段引擎判定並回報 complete；status 只回課表與「進行中」旗標
	case "mileage":
		acc, err := accDistance(ctx, db, uid, startedAt, dataSource)
		if err != nil {
			return nil, err
		}
		st.TargetKm = chalTarget
		st.AccKm = acc
		st.Met = acc >= chalTarget
	case "rest":
		windowS := int(math.Round(float64(restWindowMin*60) * mult(tier)))
		end := startedAt.Add(time.Duration(windowS) * time.Second)
		to := now
		if to.After(end) {
			to = end
		}
		acc, err := accAnyDistance(ctx, db, uid, startedAt, to)
		if err != nil {
			return nil, err
		}
		st.RestWindowS = windowS
		if el := int(now.Sub(startedAt).Seconds()); el > 0 {
			st.ElapsedS = el
		}
		st.AccKm = acc
		if acc > 0 {
			st.Failed = true
		} else {
			st.Met = !now.Before(end)
		}
	default: // manual
		st.Met = true
	}
	return st, nil
}

// accDistance 從 since 起、符合 data_source 的未 flagged 活動累積里程（strava 任務計 source='strava'；gps 計 source IS NULL）。
func accDistance(ctx context.Context, db *pgxpool.Pool, uid string, since time.Time, dataSource string) (float64, error) {
	var acc float64
	err := db.QueryRow(ctx, `
		SELECT COALESCE(SUM(distance_km),0) FROM activities
		WHERE user_id=$1 AND NOT flagged AND recorded_at >= $2
		  AND ( ($3='strava' AND source='strava') OR ($3<>'strava' AND source IS NULL) )`,
		uid, since, dataSource).Scan(&acc)
	return acc, err
}

// accAnyDistance 區間 [from, to) 內任何來源的未 flagged 里程總和（休息日判定：一有里程就失敗）。
func accAnyDistance(ctx context.Context, db *pgxpool.Pool, uid string, from, to time.Time) (float64, error) {
	var acc float64
	err := db.QueryRow(ctx, `
		SELECT COALESCE(SUM(distance_km),0) FROM activities
		WHERE user_id=$1 AND NOT flagged AND recorded_at >= $2 AND recorded_at < $3`,
		uid, from, to).Scan(&acc)
	return acc, err
}

// Status POST /personal-tasks/status — 回傳進行中挑戰的即時狀態。
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	st, err := ChallengeStatusUser(r.Context(), h.db, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"challenge": st})
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

// queryTasks 依 where/args 撈任務 + 我的進度/挑戰狀態。uid 固定為 $1；where 內的參數請從 $2 起算。
// uid 空字串＝後台不看進度（用零值 UUID，永不命中任何使用者進度）。
func (h *Handler) queryTasks(ctx context.Context, whereOrder string, uid string, args ...any) ([]Task, error) {
	if uid == "" {
		uid = "00000000-0000-0000-0000-000000000000"
	}
	q := `SELECT t.id, t.plan_id, pl.code, t.day, t.week, t.title, t.story, t.workout, t.workout_type, t.target_km, t.target_min,
	             t.intensity, t.complete_cond, t.completion_type, t.completion_params, t.reward_exp, t.reward_dp, t.icon_url, t.data_source, t.safety_note, t.enabled, t.retry_dp_cost,
	             t.workout_kind, t.segments,
	             (COALESCE(pr.stars,0) > 0) AS done, COALESCE(pr.stars,0) AS stars,
	             COALESCE(pr.attempts,0) AS attempts, COALESCE(pr.active,FALSE) AS active,
	             COALESCE(pr.challenge_tier,0) AS challenge_tier, COALESCE(pr.challenge_target_km,0) AS challenge_target_km
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
			&t.Intensity, &t.CompleteCond, &t.CompletionType, &t.CompletionParams, &t.RewardExp, &t.RewardDp, &t.IconURL, &t.DataSource, &t.SafetyNote, &t.Enabled, &t.RetryDpCost,
			&t.WorkoutKind, &t.Segments,
			&t.Done, &t.Stars, &t.Attempts, &t.Active, &t.ChallengeTier, &t.ChallengeTargetKm); err != nil {
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
