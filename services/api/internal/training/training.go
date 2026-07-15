// Package training 自主訓練（P1 課表庫 + P2 月曆排程 + P3 訓練計畫產生器），VIP 限定功能。
//
// 前端拿到 TemplateSegment（以「效度 effort」表達強度：easy/marathon/threshold/interval/rep）＋
// 玩家自選的 PaceLevel，在前端解析成既有 WorkoutSegment（帶實際配速秒/公里），沿用 /track 既有
// 分段課表引擎（見 apps/web/src/lib/workout.ts）。P1 只提供清單；P2 新增「每日排程」的月曆
// （user_training_schedule，migration 083）；P3（migration 084）把 schedule 改成「一天可多份」，
// 並新增 training_plans（一鍵產生的訓練計畫，每帳號最多 3 個）與一鍵產生器 /training/auto-plan——
// 排程本身不觸發任何完成/獎勵，跑步照常走 GPS 上傳自動發里程 EXP，月曆只是把「排定」與「實際」對照顯示。
package training

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

// requireVIP 登入 + VIP 檢查共用 helper（P1 Templates 與 P2/P3 各排程/計畫端點共用）。
// uid=="" 代表已寫好錯誤回應，呼叫端應立即 return。
func (h *Handler) requireVIP(w http.ResponseWriter, r *http.Request) string {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return ""
	}
	var isVip bool
	_ = h.db.QueryRow(r.Context(), `SELECT COALESCE(vip_expires_at > NOW(), FALSE) FROM users WHERE id=$1`, uid).Scan(&isVip)
	if !isVip {
		respondErr(w, http.StatusForbidden, "vip_only")
		return ""
	}
	return uid
}

// --- JSON 型別（契約見 apps/web/src/lib/api.ts）---

// WorkoutTemplate 課表庫的一份課表；segments 直接回傳 workout_templates.segments 原始 jsonb。
type WorkoutTemplate struct {
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Segments    json.RawMessage `json:"segments"`
	SortOrder   int             `json:"sort_order"`
}

// PaceLevel 配速等級；paces 直接回傳 pace_levels.paces 原始 jsonb
// （形狀 {easy:{fast,slow}, marathon:{...}, threshold:{...}, interval:{...}, rep:{...}}，秒/公里）。
type PaceLevel struct {
	ID    int             `json:"id"`
	Label string          `json:"label"`
	Paces json.RawMessage `json:"paces"`
}

// Router 前台（需登入）
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/templates", h.Templates)             // GET /training/templates — 課表庫 + 配速等級表（VIP 限定）
	r.Get("/calendar", h.Calendar)               // GET /training/calendar?month=YYYY-MM — 月曆排程 vs 實際（VIP 限定）
	r.Post("/schedule", h.CreateSchedule)        // POST /training/schedule — 新增一筆手動排程（VIP 限定，一天可多份）
	r.Delete("/schedule/{id}", h.DeleteSchedule) // DELETE /training/schedule/{id} — 取消單筆排程（VIP 限定）
	r.Get("/plans", h.ListPlans)                 // GET /training/plans — 訓練計畫清單（VIP 限定，上限 3）
	r.Post("/auto-plan", h.AutoPlan)             // POST /training/auto-plan — 一鍵產生訓練計畫（VIP 限定）
	r.Delete("/plans/{id}", h.DeletePlan)        // DELETE /training/plans/{id} — 刪除計畫＋其排程（CASCADE）
	return r
}

// Templates GET /training/templates — VIP 專屬：課表庫 + 配速等級表。
// library_visible=FALSE 的距離變體（lsd_6..lsd_32/easy_4/8/10，migration 084）只給產生器排課，不進課表庫清單。
func (h *Handler) Templates(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}

	tRows, err := h.db.Query(r.Context(), `
		SELECT code, name, category, description, segments, sort_order
		FROM workout_templates WHERE enabled AND library_visible ORDER BY sort_order`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tRows.Close()
	templates := []WorkoutTemplate{}
	for tRows.Next() {
		var t WorkoutTemplate
		if err := tRows.Scan(&t.Code, &t.Name, &t.Category, &t.Description, &t.Segments, &t.SortOrder); err != nil {
			continue
		}
		templates = append(templates, t)
	}
	tRows.Close()

	pRows, err := h.db.Query(r.Context(), `SELECT id, label, paces FROM pace_levels WHERE enabled ORDER BY id`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer pRows.Close()
	paceLevels := []PaceLevel{}
	for pRows.Next() {
		var p PaceLevel
		if err := pRows.Scan(&p.ID, &p.Label, &p.Paces); err != nil {
			continue
		}
		paceLevels = append(paceLevels, p)
	}
	pRows.Close()

	respondJSON(w, http.StatusOK, map[string]any{"templates": templates, "pace_levels": paceLevels})
}

// --- P2/P3 月曆排程 JSON 型別（契約見 apps/web/src/lib/api.ts）---

// ScheduledWorkout 單筆排定課表；name/category 為存檔當下的快照，template_code/pace_level 供前端
// 重新解析分段（沿用 workout.ts resolveTemplate）。plan_id 為 NULL 代表手動排定；非 NULL 則為某訓練
// 計畫底下自動產生的一筆，plan_name 為該計畫名稱（JOIN training_plans 取得）。
type ScheduledWorkout struct {
	ID           string  `json:"id"`
	PlanID       *string `json:"plan_id"`
	PlanName     *string `json:"plan_name,omitempty"`
	TemplateCode string  `json:"template_code"`
	Name         string  `json:"name"`
	Category     string  `json:"category"`
	PaceLevel    int     `json:"pace_level"`
	PlannedKm    float64 `json:"planned_km"`
	PlannedMin   int     `json:"planned_min"`
}

// ScheduleRow POST /training/schedule 回存好的一筆（含日期）。
type ScheduleRow struct {
	Date string `json:"date"`
	ScheduledWorkout
}

// TrainingDay 月曆單日：排定（陣列，一天可多份）vs 實際。
type TrainingDay struct {
	Date        string             `json:"date"`
	Scheduled   []ScheduledWorkout `json:"scheduled"`
	ActualKm    float64            `json:"actual_km"`
	HasActivity bool               `json:"has_activity"`
}

// TrainingTotals 整月統計（排定或實際各一份）。planned.Days＝有排程的 distinct 日數（非 rows 數）。
type TrainingTotals struct {
	Days int     `json:"days"`
	Km   float64 `json:"km"`
	Min  int     `json:"min"`
}

// TrainingCalendar GET /training/calendar 回傳整月。
type TrainingCalendar struct {
	Month   string         `json:"month"`
	Planned TrainingTotals `json:"planned"`
	Actual  TrainingTotals `json:"actual"`
	Days    []TrainingDay  `json:"days"`
}

// resolveMonth 解析 ?month=YYYY-MM；缺省回傳當月首日。日曆日採 Asia/Taipei（固定 UTC+8 位移，SQL 端
// 也一律 AT TIME ZONE 'Asia/Taipei'，比照 profile/titles.go computeCurrentStreak——distroless production
// 映像沒有 tzdata，Go 端故意不用 time.LoadLocation）。
func resolveMonth(raw string) (time.Time, bool) {
	if raw == "" {
		nowTaipei := time.Now().UTC().Add(8 * time.Hour)
		return time.Date(nowTaipei.Year(), nowTaipei.Month(), 1, 0, 0, 0, 0, time.UTC), true
	}
	t, err := time.Parse("2006-01", raw)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), true
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Calendar GET /training/calendar?month=YYYY-MM — VIP 專屬：排定 vs 實際整月對照。
// 每日排定改回陣列（migration 084 起一天可多份），plan_id 非空時 LEFT JOIN training_plans 取 plan_name。
func (h *Handler) Calendar(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	ctx := r.Context()

	monthStart, ok := resolveMonth(r.URL.Query().Get("month"))
	if !ok {
		respondErr(w, http.StatusBadRequest, "invalid month")
		return
	}
	monthStr := monthStart.Format("2006-01")
	monthStartStr := monthStart.Format("2006-01-02")

	// 排定：該月 user_training_schedule 所有 rows（一天可多份）。
	schedRows, err := h.db.Query(ctx, `
		SELECT s.id, s.plan_id, p.name, s.scheduled_date, s.template_code, s.pace_level, s.name, s.category, s.planned_km, s.planned_min
		FROM user_training_schedule s
		LEFT JOIN training_plans p ON p.id = s.plan_id
		WHERE s.user_id=$1 AND s.scheduled_date >= $2::date AND s.scheduled_date < ($2::date + INTERVAL '1 month')
		ORDER BY s.scheduled_date, s.created_at`,
		uid, monthStartStr)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	scheduled := map[string][]ScheduledWorkout{}
	var plannedKm float64
	var plannedMin int
	for schedRows.Next() {
		var d time.Time
		var planID *string
		var planName *string
		var sw ScheduledWorkout
		if err := schedRows.Scan(&sw.ID, &planID, &planName, &d, &sw.TemplateCode, &sw.PaceLevel, &sw.Name, &sw.Category, &sw.PlannedKm, &sw.PlannedMin); err != nil {
			schedRows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		sw.PlanID = planID
		if planID != nil {
			sw.PlanName = planName
		}
		dateStr := d.Format("2006-01-02")
		scheduled[dateStr] = append(scheduled[dateStr], sw)
		plannedKm += sw.PlannedKm
		plannedMin += sw.PlannedMin
	}
	schedRows.Close()
	if err := schedRows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// 實際：該月 activities，依 (recorded_at AT TIME ZONE 'Asia/Taipei')::date 分桶（NOT flagged）。
	actRows, err := h.db.Query(ctx, `
		SELECT (recorded_at AT TIME ZONE 'Asia/Taipei')::date AS day, SUM(distance_km), SUM(duration_s)
		FROM activities
		WHERE user_id=$1 AND NOT flagged
		  AND (recorded_at AT TIME ZONE 'Asia/Taipei')::date >= $2::date
		  AND (recorded_at AT TIME ZONE 'Asia/Taipei')::date < ($2::date + INTERVAL '1 month')
		GROUP BY day`, uid, monthStartStr)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	actualKmByDay := map[string]float64{}
	var actualKm float64
	var actualSec int
	for actRows.Next() {
		var d time.Time
		var km float64
		var sec int
		if err := actRows.Scan(&d, &km, &sec); err != nil {
			actRows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		actualKmByDay[d.Format("2006-01-02")] = km
		actualKm += km
		actualSec += sec
	}
	actRows.Close()
	if err := actRows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// days：涵蓋該月每一天（1..月底）。
	daysInMonth := monthStart.AddDate(0, 1, -1).Day()
	days := make([]TrainingDay, 0, daysInMonth)
	for day := 1; day <= daysInMonth; day++ {
		dateStr := time.Date(monthStart.Year(), monthStart.Month(), day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		td := TrainingDay{Date: dateStr, Scheduled: []ScheduledWorkout{}}
		if sws, found := scheduled[dateStr]; found {
			td.Scheduled = sws
		}
		if km, found := actualKmByDay[dateStr]; found {
			td.ActualKm = round2(km)
			td.HasActivity = true
		}
		days = append(days, td)
	}

	respondJSON(w, http.StatusOK, TrainingCalendar{
		Month: monthStr,
		Planned: TrainingTotals{
			Days: len(scheduled),
			Km:   round2(plannedKm),
			Min:  plannedMin,
		},
		Actual: TrainingTotals{
			Days: len(actualKmByDay),
			Km:   round2(actualKm),
			Min:  int(math.Round(float64(actualSec) / 60)),
		},
		Days: days,
	})
}

// scheduleRequest POST /training/schedule 請求體。
type scheduleRequest struct {
	Date         string  `json:"date"`
	TemplateCode string  `json:"template_code"`
	PaceLevel    int     `json:"pace_level"`
	PlannedKm    float64 `json:"planned_km"`
	PlannedMin   int     `json:"planned_min"`
}

// CreateSchedule POST /training/schedule — VIP 專屬：新增一筆手動排程（plan_id=NULL）。
// migration 084 起 user_training_schedule 改「一天可多份」（PK 從 user+date 改成 id），故改成單純
// INSERT，不再 upsert-by-date。name/category 一律由後端依 template_code 查 workout_templates 填入
// （權威快照），不信任前端傳入值。
func (h *Handler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	ctx := r.Context()

	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if _, err := time.Parse("2006-01-02", req.Date); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid date")
		return
	}

	var name, category string
	err := h.db.QueryRow(ctx, `SELECT name, category FROM workout_templates WHERE code=$1 AND enabled`, req.TemplateCode).Scan(&name, &category)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusBadRequest, "invalid template_code")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	var paceOK bool
	if err := h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pace_levels WHERE id=$1 AND enabled)`, req.PaceLevel).Scan(&paceOK); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if !paceOK {
		respondErr(w, http.StatusBadRequest, "invalid pace_level")
		return
	}

	var d time.Time
	row := ScheduleRow{}
	if err := h.db.QueryRow(ctx, `
		INSERT INTO user_training_schedule (user_id, scheduled_date, template_code, pace_level, name, category, planned_km, planned_min)
		VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8)
		RETURNING id, scheduled_date, template_code, pace_level, name, category, planned_km, planned_min`,
		uid, req.Date, req.TemplateCode, req.PaceLevel, name, category, req.PlannedKm, req.PlannedMin).
		Scan(&row.ID, &d, &row.TemplateCode, &row.PaceLevel, &row.Name, &row.Category, &row.PlannedKm, &row.PlannedMin); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	row.Date = d.Format("2006-01-02")
	row.PlanID = nil

	respondJSON(w, http.StatusOK, row)
}

// DeleteSchedule DELETE /training/schedule/{id} — VIP 專屬：取消單筆排程（改用 id，不再用 date；
// 一天可多份後 date 已不足以定位單筆）。
func (h *Handler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `DELETE FROM user_training_schedule WHERE id=$1 AND user_id=$2`, id, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- P3 訓練計畫（training_plans）---

// TrainingPlan 一個「一鍵產生」的訓練計畫；workout_count 為該計畫底下的排程筆數（非 distinct 日數）。
type TrainingPlan struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	RaceDate     *string `json:"race_date"`
	RaceDistance string  `json:"race_distance"`
	Weeks        int     `json:"weeks"`
	DaysPerWeek  int     `json:"days_per_week"`
	PaceLevel    int     `json:"pace_level"`
	StartDate    string  `json:"start_date"`
	EndDate      string  `json:"end_date"`
	WorkoutCount int     `json:"workout_count"`
}

// planLimit 每帳號最多同時保留的訓練計畫數（POST /training/auto-plan 超過即 409 plan_limit）。
const planLimit = 3

// ListPlans GET /training/plans — VIP 專屬：該帳號的訓練計畫清單。
func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT p.id, p.name, p.race_date, p.race_distance, p.weeks, p.days_per_week, p.pace_level, p.start_date, p.end_date,
		       (SELECT COUNT(*) FROM user_training_schedule s WHERE s.plan_id = p.id) AS workout_count
		FROM training_plans p
		WHERE p.user_id=$1
		ORDER BY p.created_at`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	plans := []TrainingPlan{}
	for rows.Next() {
		var p TrainingPlan
		var raceDate *time.Time
		var startDate, endDate time.Time
		if err := rows.Scan(&p.ID, &p.Name, &raceDate, &p.RaceDistance, &p.Weeks, &p.DaysPerWeek, &p.PaceLevel, &startDate, &endDate, &p.WorkoutCount); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if raceDate != nil {
			s := raceDate.Format("2006-01-02")
			p.RaceDate = &s
		}
		p.StartDate = startDate.Format("2006-01-02")
		p.EndDate = endDate.Format("2006-01-02")
		plans = append(plans, p)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"plans": plans, "limit": planLimit})
}

// DeletePlan DELETE /training/plans/{id} — VIP 專屬：刪除計畫；其排程由 FK ON DELETE CASCADE 連帶刪除。
func (h *Handler) DeletePlan(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `DELETE FROM training_plans WHERE id=$1 AND user_id=$2`, id, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- P3 一鍵產生器（POST /training/auto-plan）---

// autoPlanRequest 一鍵產生器請求體（欄位需與前端 apps/web/src/lib/api.ts AutoPlanRequest 一一對齊）。
type autoPlanRequest struct {
	RunningAge   string  `json:"running_age"` // 跑齡等級：new|novice|experienced|veteran（無 days_per_week 時用來推預設）
	Best1kmS     int     `json:"best_1km_s"`
	LongestKm    float64 `json:"longest_km"`
	LongestMin   int     `json:"longest_min"`
	HasRace      bool    `json:"has_race"`
	RaceDate     string  `json:"race_date"`
	RaceDistance string  `json:"race_distance"`
	Weeks        int     `json:"weeks"`
	DaysPerWeek  int     `json:"days_per_week"`
}

// raceDistanceLabel race_distance → 計畫命名用中文/簡稱；也做為合法值白名單。
var raceDistanceLabel = map[string]string{"5k": "5K", "10k": "10K", "half": "半馬", "full": "全馬"}

// allowedFreeWeeks 無 race_date 時，body.weeks 的合法值。
var allowedFreeWeeks = map[int]bool{1: true, 4: true, 8: true, 12: true, 16: true}

// paceRange 配速區間（秒/公里）。
type paceRange struct {
	Fast float64 `json:"fast"`
	Slow float64 `json:"slow"`
}

// paceMap 一個配速等級的完整效度→配速對照（easy/marathon/threshold/interval/rep）。
type paceMap map[string]paceRange

func (pm paceMap) mid(effort string) float64 {
	if effort == "" {
		effort = "easy"
	}
	if p, ok := pm[effort]; ok && p.Fast > 0 && p.Slow > 0 {
		return (p.Fast + p.Slow) / 2
	}
	return 420 // 比照前端 workout.ts estMinutes 的預設 fallback（7:00/km）
}

// templateSeg workout_templates.segments 的一段（比照前端 apps/web/src/lib/api.ts TemplateSegment）。
type templateSeg struct {
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	Effort     string  `json:"effort"`
	TargetType string  `json:"target_type"`
	Target     float64 `json:"target"`
	Reps       int     `json:"reps"`
	RestS      int     `json:"rest_s"`
}

// templateInfo 產生器用的課表快照（name/category/segments），由 code 索引。
type templateInfo struct {
	Name     string
	Category string
	Segments []templateSeg
}

// segTotalKm 距離段加總（公里，與配速等級無關）；比照前端 workout.ts totalKm。
func segTotalKm(segs []templateSeg) float64 {
	var m float64
	for _, s := range segs {
		reps := s.Reps
		if reps < 1 {
			reps = 1
		}
		if s.TargetType == "distance" {
			m += s.Target * float64(reps)
		}
	}
	return math.Round(m/100) / 10
}

// segEstMinutes 預估完成時間（分）：距離段用配速中位數估、時間段直接計，加組間休；比照 workout.ts estMinutes。
func segEstMinutes(segs []templateSeg, pm paceMap) int {
	var total float64
	for _, s := range segs {
		reps := s.Reps
		if reps < 1 {
			reps = 1
		}
		if s.TargetType == "distance" {
			total += (s.Target / 1000) * pm.mid(s.Effort) * float64(reps)
		} else {
			total += s.Target * float64(reps)
		}
		if reps > 1 && s.RestS > 0 {
			total += float64(s.RestS) * float64(reps-1)
		}
	}
	return int(math.Round(total / 60))
}

// lsdCandidate 產生器可選的長跑距離變體（含 16K 用既有 'lsd'，其餘為 migration 084 新增變體）。
type lsdCandidate struct {
	Km   float64
	Code string
}

var lsdCandidates = []lsdCandidate{
	{6, "lsd_6"}, {8, "lsd_8"}, {10, "lsd_10"}, {12, "lsd_12"}, {14, "lsd_14"}, {16, "lsd"},
	{18, "lsd_18"}, {20, "lsd_20"}, {22, "lsd_22"}, {24, "lsd_24"}, {28, "lsd_28"}, {32, "lsd_32"},
}

// nearestLsdCode 挑距離最接近 km 的長跑變體 code。
func nearestLsdCode(km float64) string {
	best := lsdCandidates[0]
	bestDiff := math.Abs(best.Km - km)
	for _, c := range lsdCandidates[1:] {
		if d := math.Abs(c.Km - km); d < bestDiff {
			best, bestDiff = c, d
		}
	}
	return best.Code
}

// longRunCap 長跑距離上限（公里）：有 race_distance 用固定值，否則依 longest_km 推估。
func longRunCap(raceDistance string, longestKm float64) float64 {
	switch raceDistance {
	case "5k":
		return 10
	case "10k":
		return 14
	case "half":
		return 20
	case "full":
		return 32
	default:
		return min(24, max(12, longestKm*1.6))
	}
}

// weekdayRoles 每週固定星期→角色樣式（長跑固定週日；quality 依 daysPerWeek 給 1 或 2 天，彼此不相鄰、
// 不緊鄰長跑；其餘訓練日 easy）。daysPerWeek clamp 在 2..7，故只需處理這 6 種。
func weekdayRoles(daysPerWeek int) map[time.Weekday]string {
	switch daysPerWeek {
	case 2:
		return map[time.Weekday]string{time.Sunday: "long", time.Thursday: "quality"}
	case 3:
		return map[time.Weekday]string{time.Sunday: "long", time.Tuesday: "easy", time.Thursday: "quality"}
	case 4:
		return map[time.Weekday]string{time.Sunday: "long", time.Tuesday: "easy", time.Thursday: "quality", time.Saturday: "easy"}
	case 5:
		return map[time.Weekday]string{time.Sunday: "long", time.Monday: "easy", time.Tuesday: "quality", time.Thursday: "quality", time.Saturday: "easy"}
	case 6:
		return map[time.Weekday]string{time.Sunday: "long", time.Monday: "easy", time.Tuesday: "quality", time.Wednesday: "easy", time.Thursday: "quality", time.Saturday: "easy"}
	default: // 7
		return map[time.Weekday]string{time.Sunday: "long", time.Monday: "easy", time.Tuesday: "quality", time.Wednesday: "easy", time.Thursday: "quality", time.Friday: "easy", time.Saturday: "easy"}
	}
}

// phaseFor 該 weekIndex（從 start_date 算的滾動 7 天區塊，非日曆週）所屬分期。
// base=前 ceil(weeks*0.35) 週；taper=最後 taperWeeks 週；peak=taper 前最多 2 週的過渡；build=其餘中段。
func phaseFor(weekIndex, weeks, baseWeeks, taperWeeks int) string {
	if taperWeeks > 0 && weekIndex >= weeks-taperWeeks {
		return "taper"
	}
	if weekIndex < baseWeeks {
		return "base"
	}
	middle := weeks - baseWeeks - taperWeeks
	peakWeeks := 0
	if middle > 0 {
		peakWeeks = min(2, middle)
	}
	if weekIndex >= weeks-taperWeeks-peakWeeks {
		return "peak"
	}
	return "build"
}

// qualityTemplates 依 phase×race_distance 輪替的 quality 課表 code 清單。
func qualityTemplates(phase, raceDistance string) []string {
	switch phase {
	case "base":
		return []string{"tempo", "threshold", "fartlek"}
	case "build":
		switch raceDistance {
		case "5k", "10k":
			return []string{"int_400", "int_800", "int_1000", "int_1200"}
		case "half":
			return []string{"int_1000", "int_1200", "threshold", "tempo"}
		case "full":
			return []string{"tempo", "yasso", "threshold", "int_1000"}
		default:
			return []string{"int_800", "int_1000", "tempo", "threshold", "fartlek"}
		}
	case "peak":
		switch raceDistance {
		case "5k", "10k":
			return []string{"int_400", "int_800", "int_1000"}
		case "half":
			return []string{"threshold", "int_1000", "tempo"}
		case "full":
			return []string{"yasso", "tempo", "threshold"}
		default:
			// 無 race 沒有專屬 peak 清單，沿用無 race build 的清單。
			return []string{"int_800", "int_1000", "tempo", "threshold", "fartlek"}
		}
	case "taper":
		return []string{"int_400", "tempo"}
	default:
		return []string{"tempo"}
	}
}

// plannedWorkout 一天要排的一筆課表（產生器內部用，尚未查 template 詳情）。
type plannedWorkout struct {
	Date         string
	TemplateCode string
}

// AutoPlan POST /training/auto-plan — VIP 專屬：依跑者能力/賽事目標一鍵產生一份訓練計畫（每帳號最多 3 個）。
func (h *Handler) AutoPlan(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	ctx := r.Context()

	var req autoPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	var planCount int
	if err := h.db.QueryRow(ctx, `SELECT COUNT(*) FROM training_plans WHERE user_id=$1`, uid).Scan(&planCount); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if planCount >= planLimit {
		respondErr(w, http.StatusConflict, "plan_limit")
		return
	}

	if req.Best1kmS <= 0 {
		respondErr(w, http.StatusBadRequest, "invalid best_1km_s")
		return
	}
	if req.RaceDistance != "" {
		if _, ok := raceDistanceLabel[req.RaceDistance]; !ok {
			respondErr(w, http.StatusBadRequest, "invalid race_distance")
			return
		}
	}

	nowTaipei := time.Now().UTC().Add(8 * time.Hour)
	today := time.Date(nowTaipei.Year(), nowTaipei.Month(), nowTaipei.Day(), 0, 0, 0, 0, time.UTC)

	weeks := 0
	start := today
	var end time.Time
	var raceDateTime time.Time
	if req.RaceDate != "" {
		raceDate, err := time.Parse("2006-01-02", req.RaceDate)
		if err != nil {
			respondErr(w, http.StatusBadRequest, "invalid race_date")
			return
		}
		if !raceDate.After(today) {
			respondErr(w, http.StatusBadRequest, "invalid race_date")
			return
		}
		raceDateTime = raceDate
		days := int(math.Round(raceDate.Sub(today).Hours() / 24))
		rawWeeks := max(1, int(math.Ceil(float64(days)/7)))
		if rawWeeks > 24 {
			// 賽事 >24 週遠：只排最後 24 週，讓 weeks/end/迴圈/phaseFor 全一致（weekIndex 保證落在 [0,weeks)）。
			weeks = 24
			start = raceDate.AddDate(0, 0, -(24*7 - 1))
		} else {
			weeks = rawWeeks
		}
		end = raceDate
	} else {
		if !allowedFreeWeeks[req.Weeks] {
			respondErr(w, http.StatusBadRequest, "invalid weeks")
			return
		}
		weeks = req.Weeks
		end = start.AddDate(0, 0, weeks*7-1)
	}

	daysPerWeek := 0
	if req.DaysPerWeek != 0 {
		daysPerWeek = min(7, max(2, req.DaysPerWeek))
	} else {
		switch req.RunningAge {
		case "new":
			daysPerWeek = 3
		case "novice":
			daysPerWeek = 4
		case "experienced", "veteran":
			daysPerWeek = 5
		default:
			daysPerWeek = 4
		}
	}

	taperWeeks := 0
	if req.RaceDate != "" {
		if weeks >= 8 {
			taperWeeks = 2
		} else {
			taperWeeks = 1
		}
	}
	baseWeeks := int(math.Ceil(float64(weeks) * 0.35))
	if baseWeeks < 1 {
		baseWeeks = 1
	}

	// 配速等級：選 rep 中位最接近 best_1km_s 者。
	pRows, err := h.db.Query(ctx, `SELECT id, paces FROM pace_levels WHERE enabled ORDER BY id`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	levelPaces := map[int]paceMap{}
	selectedLevel := 0
	bestDiff := math.MaxFloat64
	maxLevelID := 0
	for pRows.Next() {
		var id int
		var raw json.RawMessage
		if err := pRows.Scan(&id, &raw); err != nil {
			pRows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		var pm paceMap
		if err := json.Unmarshal(raw, &pm); err != nil {
			continue
		}
		levelPaces[id] = pm
		if id > maxLevelID {
			maxLevelID = id
		}
		rep := pm["rep"]
		mid := (rep.Fast + rep.Slow) / 2
		if diff := math.Abs(mid - float64(req.Best1kmS)); diff < bestDiff {
			bestDiff = diff
			selectedLevel = id
		}
	}
	pRows.Close()
	if err := pRows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if selectedLevel < 1 {
		respondErr(w, http.StatusInternalServerError, "no pace levels configured")
		return
	}
	selectedLevel = min(maxLevelID, max(1, selectedLevel))
	easyPaceMid := levelPaces[selectedLevel].mid("easy")

	// 課表詳情（含 library_visible=FALSE 的距離變體，產生器要用）。
	tRows, err := h.db.Query(ctx, `SELECT code, name, category, segments FROM workout_templates WHERE enabled`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	templates := map[string]templateInfo{}
	for tRows.Next() {
		var code, name, category string
		var raw json.RawMessage
		if err := tRows.Scan(&code, &name, &category, &raw); err != nil {
			tRows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		var segs []templateSeg
		if err := json.Unmarshal(raw, &segs); err != nil {
			continue
		}
		templates[code] = templateInfo{Name: name, Category: category, Segments: segs}
	}
	tRows.Close()
	if err := tRows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// 長跑距離：capKm + 起始值 + 逐週漸增/recovery/taper 調整。
	capKm := longRunCap(req.RaceDistance, req.LongestKm)
	startLong := req.LongestKm
	if startLong <= 0 {
		startLong = 5
	}
	startLong = min(capKm, max(4, startLong))
	rampWeeks := weeks - taperWeeks

	roles := weekdayRoles(daysPerWeek)
	qualityCounter := 0
	easyCounter := 0
	var plan []plannedWorkout

	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if req.RaceDate != "" && !d.Before(raceDateTime) {
			continue // 賽事當日不排課表（那天是比賽，且迴圈不會超過 raceDateTime=end）
		}
		role, ok := roles[d.Weekday()]
		if !ok {
			continue
		}
		weekIndex := int(d.Sub(start).Hours()/24) / 7
		phase := phaseFor(weekIndex, weeks, baseWeeks, taperWeeks)
		isRecovery := weekIndex%4 == 3 && phase != "taper"

		var code string
		switch role {
		case "long":
			var km float64
			if phase == "taper" {
				taperIdx := weekIndex - (weeks - taperWeeks)
				denom := max(1, taperWeeks-1)
				mult := 0.6 - 0.2*(float64(taperIdx)/float64(denom))
				km = capKm * mult
			} else {
				t := 0.0
				if rampWeeks > 1 {
					t = min(1, max(0, float64(weekIndex)/float64(rampWeeks-1)))
				}
				nominal := startLong + (capKm-startLong)*t
				if isRecovery {
					km = nominal * 0.65
				} else {
					km = nominal
				}
			}
			if req.LongestMin > 0 && easyPaceMid > 0 {
				km = min(km, float64(req.LongestMin)*60/easyPaceMid)
			}
			km = max(3, km)
			code = nearestLsdCode(km)
		case "quality":
			list := qualityTemplates(phase, req.RaceDistance)
			code = list[qualityCounter%len(list)]
			qualityCounter++
		case "easy":
			if isRecovery {
				if easyCounter%2 == 0 {
					code = "recovery"
				} else {
					code = "easy_4"
				}
			} else {
				if easyCounter%2 == 0 {
					code = "easy"
				} else {
					code = "easy_8"
				}
			}
			easyCounter++
		}
		if _, ok := templates[code]; !ok {
			continue // 防禦：理論上不會發生（所有 code 皆來自固定清單，且都在 seed migration 建立）
		}
		plan = append(plan, plannedWorkout{Date: d.Format("2006-01-02"), TemplateCode: code})
	}

	name := fmt.Sprintf("%d週訓練", weeks)
	if req.RaceDistance != "" {
		name = fmt.Sprintf("%d週·%s", weeks, raceDistanceLabel[req.RaceDistance])
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx)

	var raceDateParam any
	if req.RaceDate != "" {
		raceDateParam = req.RaceDate
	}
	var planID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO training_plans (user_id, name, race_date, race_distance, weeks, days_per_week, pace_level, start_date, end_date)
		VALUES ($1,$2,$3::date,$4,$5,$6,$7,$8::date,$9::date)
		RETURNING id`,
		uid, name, raceDateParam, req.RaceDistance, weeks, daysPerWeek, selectedLevel,
		start.Format("2006-01-02"), end.Format("2006-01-02")).Scan(&planID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	pm := levelPaces[selectedLevel]
	for _, pw := range plan {
		t := templates[pw.TemplateCode]
		plannedKm := segTotalKm(t.Segments)
		plannedMin := segEstMinutes(t.Segments, pm)
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_training_schedule (user_id, plan_id, scheduled_date, template_code, pace_level, name, category, planned_km, planned_min)
			VALUES ($1,$2,$3::date,$4,$5,$6,$7,$8,$9)`,
			uid, planID, pw.Date, pw.TemplateCode, selectedLevel, t.Name, t.Category, plannedKm, plannedMin); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	result := TrainingPlan{
		ID: planID, Name: name, RaceDistance: req.RaceDistance, Weeks: weeks, DaysPerWeek: daysPerWeek,
		PaceLevel: selectedLevel, StartDate: start.Format("2006-01-02"), EndDate: end.Format("2006-01-02"),
		WorkoutCount: len(plan),
	}
	if req.RaceDate != "" {
		rd := req.RaceDate
		result.RaceDate = &rd
	}
	respondJSON(w, http.StatusOK, map[string]any{"plan": result})
}

// --- 共用 ---

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
