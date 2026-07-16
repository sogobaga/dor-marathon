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
	"strings"
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
// AdjustType（migration 085）：distance(調總距離)/reps(調趟數)/pyramid(調峰值±400m)/none，前端課表卡
// 據此決定是否顯示微調 UI 及其步階單位。
type WorkoutTemplate struct {
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Segments    json.RawMessage `json:"segments"`
	SortOrder   int             `json:"sort_order"`
	AdjustType  string          `json:"adjust_type"`
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
	r.Get("/templates", h.Templates)              // GET /training/templates — 課表庫 + 配速等級表（VIP 限定）
	r.Get("/calendar", h.Calendar)                // GET /training/calendar?month=YYYY-MM — 月曆排程 vs 實際（VIP 限定）
	r.Post("/schedule", h.CreateSchedule)         // POST /training/schedule — 新增一筆手動排程（VIP 限定，一天可多份）
	r.Delete("/schedule/{id}", h.DeleteSchedule)  // DELETE /training/schedule/{id} — 取消單筆排程（VIP 限定）
	r.Post("/schedule/{id}/move", h.MoveSchedule) // POST /training/schedule/{id}/move — 拖曳改期＋同來源連鎖推擠（VIP 限定）
	r.Get("/plans", h.ListPlans)                  // GET /training/plans — 訓練計畫清單（VIP 限定，上限 3）
	r.Post("/auto-plan", h.AutoPlan)              // POST /training/auto-plan — 一鍵產生訓練計畫（VIP 限定）
	r.Delete("/plans/{id}", h.DeletePlan)         // DELETE /training/plans/{id} — 刪除計畫＋其排程（CASCADE）
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
		SELECT code, name, category, description, segments, sort_order, adjust_type
		FROM workout_templates WHERE enabled AND library_visible ORDER BY sort_order`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tRows.Close()
	templates := []WorkoutTemplate{}
	for tRows.Next() {
		var t WorkoutTemplate
		if err := tRows.Scan(&t.Code, &t.Name, &t.Category, &t.Description, &t.Segments, &t.SortOrder, &t.AdjustType); err != nil {
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
	Adjust       int     `json:"adjust"`
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
		SELECT s.id, s.plan_id, p.name, s.scheduled_date, s.template_code, s.pace_level, s.name, s.category, s.planned_km, s.planned_min, s.adjust
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
		if err := schedRows.Scan(&sw.ID, &planID, &planName, &d, &sw.TemplateCode, &sw.PaceLevel, &sw.Name, &sw.Category, &sw.PlannedKm, &sw.PlannedMin, &sw.Adjust); err != nil {
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

// scheduleRequest POST /training/schedule 請求體。Adjust（migration 085）＝微調量 delta：距離型±公里、
// 間歇型±趟、金字塔±(400m 峰值階)；純存放，實際套用在前端 resolveTemplate，後端不校驗其範圍。
type scheduleRequest struct {
	Date         string  `json:"date"`
	TemplateCode string  `json:"template_code"`
	PaceLevel    int     `json:"pace_level"`
	PlannedKm    float64 `json:"planned_km"`
	PlannedMin   int     `json:"planned_min"`
	Adjust       int     `json:"adjust"`
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
		INSERT INTO user_training_schedule (user_id, scheduled_date, template_code, pace_level, name, category, planned_km, planned_min, adjust)
		VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, scheduled_date, template_code, pace_level, name, category, planned_km, planned_min, adjust`,
		uid, req.Date, req.TemplateCode, req.PaceLevel, name, category, req.PlannedKm, req.PlannedMin, req.Adjust).
		Scan(&row.ID, &d, &row.TemplateCode, &row.PaceLevel, &row.Name, &row.Category, &row.PlannedKm, &row.PlannedMin, &row.Adjust); err != nil {
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

// moveRequest POST /training/schedule/{id}/move 請求體。
type moveRequest struct {
	Date string `json:"date"`
}

// scheduleMove 套用階段要執行的單一 UPDATE（某筆排程 id → 新日期）。
type scheduleMove struct {
	ID   string
	Date string // "2006-01-02"
}

// maxChainScanDays 連鎖推擠往後尋找空日的掃描上限（天）；超過視為找不到空位（409 no_free_day）。
const maxChainScanDays = 400

// MoveSchedule POST /training/schedule/{id}/move — VIP 專屬：拖曳把某筆排程 X 改到新日期 T，僅在
// 「同來源」（plan_id 相同，或兩者皆 NULL＝手動排）範圍內連鎖推擠，其他來源/其他使用者完全不受影響。
// 「一天可多份」（手動排 plan_id 皆 NULL，同日常見 ≥2 筆）：佔用以「該日同來源的整組 id」為單位判斷/位移，
// 空日定義為該日同來源 id 數為 0，而非「原本只有 X 一筆、X 離開後就算空」：
//   - T 空 → X 直接搬過去。
//   - T 已有課表（一或多筆，視為「整組」）→ 優先把整組塞去 T-1 或 T+1 的空位；兩者皆滿則整段
//     （[T+1, F-1]，F 為往後第一個空日）以「日」為單位、每日整組一起往後順延一天（由後往前套用
//     避免互相覆蓋），騰出 T+1 給 T 的整組，最後 X→T。
//
// 全部在單一交易內完成；user_training_schedule 無 (user,date) 唯一約束，故套用順序不影響 DB
// 正確性，只影響「同一輪次內」位移計算是否正確（見上）。取得 X 現況的初始 SELECT 與同來源列讀取
// 皆鎖 FOR UPDATE：前者確保 origDate 是鎖定後的最新值（避免併發移動同一筆時讀到 stale 值、清錯原位），
// 後者避免同來源兩個併發搬移互相踩到彼此算好的空位表。
func (h *Handler) MoveSchedule(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx)

	var origDate time.Time
	var planID *string
	err = tx.QueryRow(ctx, `SELECT scheduled_date, plan_id FROM user_training_schedule WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, uid).
		Scan(&origDate, &planID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	target, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		respondErr(w, http.StatusBadRequest, "invalid date")
		return
	}
	targetStr := target.Format("2006-01-02")
	origStr := origDate.Format("2006-01-02")

	if targetStr == origStr {
		// 目標日＝現在日期，no-op（仍在交易內，直接 commit 即可，反正沒有異動）。
		if err := tx.Commit(ctx); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"ok": true, "moved": 0})
		return
	}

	// 同來源（plan_id 相同，或 $2 與 plan_id 皆 NULL）所有列，鎖 FOR UPDATE。
	rows, err := tx.Query(ctx, `
		SELECT id, scheduled_date FROM user_training_schedule
		WHERE user_id=$1 AND (plan_id = $2 OR ($2 IS NULL AND plan_id IS NULL))
		FOR UPDATE`,
		uid, planID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	byDate := map[string][]string{} // 日期 → 該日同來源「所有」排程 id（一天可多份；X 此時仍在其中，佔著 origStr 那份）
	for rows.Next() {
		var rowID string
		var d time.Time
		if err := rows.Scan(&rowID, &d); err != nil {
			rows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		ds := d.Format("2006-01-02")
		byDate[ds] = append(byDate[ds], rowID) // 不可覆蓋：同日同來源可能已有其他筆
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	isFree := func(d string) bool { return len(byDate[d]) == 0 } // 空日＝該日同來源 id 數為 0（非「key 不存在」）

	// X 要離開原位：只從原位清單移除「自己」這一筆 id，同日若還有其他同來源課表則留在原地、
	// 原位是否算空取決於移除後清單是否為空（絕不可整日 delete）。
	orig := byDate[origStr]
	for i, rid := range orig {
		if rid == id {
			orig = append(orig[:i], orig[i+1:]...)
			break
		}
	}
	byDate[origStr] = orig

	var moves []scheduleMove
	if !isFree(targetStr) {
		group := append([]string(nil), byDate[targetStr]...) // T 當日「整組」同來源 id（一或多筆），要整組一起位移
		before := target.AddDate(0, 0, -1).Format("2006-01-02")
		after := target.AddDate(0, 0, 1).Format("2006-01-02")
		switch {
		case isFree(before):
			for _, gid := range group {
				moves = append(moves, scheduleMove{ID: gid, Date: before})
			}
		case isFree(after):
			for _, gid := range group {
				moves = append(moves, scheduleMove{ID: gid, Date: after})
			}
		default:
			// 前後皆滿 → 找最小的 F > T 且空；[T+1, F-1] 全部視定義必是滿的，每日整組各 +1 天，
			// 由後往前（F-1 遞減到 T+1）處理，確保每一步騰出的位子是下一步要搬進去的位子。
			var free time.Time
			found := false
			for i := 1; i <= maxChainScanDays; i++ {
				cand := target.AddDate(0, 0, i)
				if isFree(cand.Format("2006-01-02")) {
					free = cand
					found = true
					break
				}
			}
			if !found {
				respondErr(w, http.StatusConflict, "no_free_day")
				return
			}
			lowerBound := target.AddDate(0, 0, 1)
			for d := free.AddDate(0, 0, -1); !d.Before(lowerBound); d = d.AddDate(0, 0, -1) {
				occGroup := byDate[d.Format("2006-01-02")]
				if len(occGroup) == 0 {
					continue // 理論上不會發生：[T+1, F-1] 依 F 的最小性定義必全滿
				}
				newDate := d.AddDate(0, 0, 1).Format("2006-01-02")
				for _, occID := range occGroup {
					moves = append(moves, scheduleMove{ID: occID, Date: newDate})
				}
			}
			for _, gid := range group {
				moves = append(moves, scheduleMove{ID: gid, Date: after})
			}
		}
	}
	moves = append(moves, scheduleMove{ID: id, Date: targetStr}) // X → T，最後套用

	for _, mv := range moves {
		if _, err := tx.Exec(ctx, `UPDATE user_training_schedule SET scheduled_date=$1::date WHERE id=$2 AND user_id=$3`,
			mv.Date, mv.ID, uid); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "moved": len(moves)})
}

// --- P3 訓練計畫（training_plans）---

// TrainingPlan 一個「一鍵產生」的訓練計畫；workout_count 為該計畫底下的排程筆數（非 distinct 日數）。
// RaceName（migration 086）為使用者自填的目標賽事名稱，顯示優先序 race_name > name（自動命名，如
// 「23週·全馬」）；後端不做這個 fallback，由前端決定顯示哪個。Stats 見 PlanStats（僅 ListPlans 填入，
// AutoPlan 回傳當下維持零值，避免多打額外查詢）。
type TrainingPlan struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RaceName     string    `json:"race_name"`
	RaceDate     *string   `json:"race_date"`
	RaceDistance string    `json:"race_distance"`
	Weeks        int       `json:"weeks"`
	DaysPerWeek  int       `json:"days_per_week"`
	PaceLevel    int       `json:"pace_level"`
	StartDate    string    `json:"start_date"`
	EndDate      string    `json:"end_date"`
	WorkoutCount int       `json:"workout_count"`
	MonthlyKm    int       `json:"monthly_km"`  // 產生當下填寫的月跑量(km)，0=未填；純記錄，不隨時間更新。
	GoalTimeS    int       `json:"goal_time_s"` // 目標完賽秒數，0=未設定。
	GoalPaceS    int       `json:"goal_pace_s"` // 衍生值＝goal_time_s÷賽事距離，非 DB 欄位；ListPlans 用 goalPaceSecPerKm 算出填入。
	PlanMode     string    `json:"plan_mode"`   // 保守/積極（migration 089）：conservative|aggressive。
	Stats        PlanStats `json:"stats"`
}

// PlanStats 單一訓練計畫的統計，取代舊版「以月為單位」（Calendar handler 的整月 planned/actual）在
// 前端拼湊計畫進度的作法，改成直接以「計畫期間」（start_date~end_date）為單位。Planned 統計該計畫底下
// 排定了什麼；Actual 統計期間內該使用者實際跑了什麼（與是否屬於這個計畫的排程無關，只看日期落點）。
// 若計畫尚無任何排程/活動，Planned/Actual 各欄位為零值（TrainingTotals 零值），不會缺欄位。
type PlanStats struct {
	Planned       TrainingTotals `json:"planned"`
	Actual        TrainingTotals `json:"actual"`
	TotalDays     int            `json:"total_days"`     // end_date - start_date + 1
	ElapsedDays   int            `json:"elapsed_days"`   // clamp(today - start_date + 1, 0, TotalDays)，today 用 Asia/Taipei
	RemainingDays int            `json:"remaining_days"` // TotalDays - ElapsedDays
}

// planLimit 每帳號最多同時保留的訓練計畫數（POST /training/auto-plan 超過即 409 plan_limit）。
const planLimit = 3

// ListPlans GET /training/plans — VIP 專屬：該帳號的訓練計畫清單，含 race_name 與每個計畫的統計
// （PlanStats，以「計畫期間」為單位）。計畫最多 3 個，但無論幾個都固定發 3 次查詢（計畫本身 + planned
// 統計 + actual 統計），皆以 user_id 為條件一次撈完全部計畫、GROUP BY 分組，避免依計畫數逐一查詢
// （N+1）；PlanStats 在 Go 端用 map 對回各計畫，無資料的計畫維持 TrainingTotals 零值。
func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	ctx := r.Context()

	rows, err := h.db.Query(ctx, `
		SELECT p.id, p.name, p.race_name, p.race_date, p.race_distance, p.weeks, p.days_per_week, p.pace_level, p.start_date, p.end_date,
		       p.monthly_km, p.goal_time_s, p.plan_mode,
		       (SELECT COUNT(*) FROM user_training_schedule s WHERE s.plan_id = p.id) AS workout_count
		FROM training_plans p
		WHERE p.user_id=$1
		ORDER BY p.created_at`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	type planRow struct {
		plan      TrainingPlan
		startDate time.Time
		endDate   time.Time
	}
	var planRows []planRow
	for rows.Next() {
		var p TrainingPlan
		var raceDate *time.Time
		var startDate, endDate time.Time
		if err := rows.Scan(&p.ID, &p.Name, &p.RaceName, &raceDate, &p.RaceDistance, &p.Weeks, &p.DaysPerWeek, &p.PaceLevel, &startDate, &endDate,
			&p.MonthlyKm, &p.GoalTimeS, &p.PlanMode, &p.WorkoutCount); err != nil {
			rows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		if raceDate != nil {
			s := raceDate.Format("2006-01-02")
			p.RaceDate = &s
		}
		p.StartDate = startDate.Format("2006-01-02")
		p.EndDate = endDate.Format("2006-01-02")
		p.GoalPaceS = goalPaceSecPerKm(p.GoalTimeS, p.RaceDistance)
		planRows = append(planRows, planRow{plan: p, startDate: startDate, endDate: endDate})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// planned：該帳號所有計畫的排程統計一次撈完（GROUP BY plan_id）。Days＝COUNT(DISTINCT
	// scheduled_date)，因一天可多份課表，不能用 COUNT(*)。
	plannedMap := map[string]TrainingTotals{}
	prows, err := h.db.Query(ctx, `
		SELECT s.plan_id, COUNT(DISTINCT s.scheduled_date), COALESCE(SUM(s.planned_km),0), COALESCE(SUM(s.planned_min),0)
		FROM user_training_schedule s
		WHERE s.plan_id IN (SELECT id FROM training_plans WHERE user_id=$1)
		GROUP BY s.plan_id`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	for prows.Next() {
		var pid string
		var days, minutes int
		var km float64
		if err := prows.Scan(&pid, &days, &km, &minutes); err != nil {
			prows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		plannedMap[pid] = TrainingTotals{Days: days, Km: round2(km), Min: minutes}
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// actual：每個計畫「期間內」（start_date~end_date）該使用者實際跑了什麼，一次撈完（JOIN
	// training_plans 依台北日期落點分組），比照 Calendar handler 的 NOT flagged 與
	// AT TIME ZONE 'Asia/Taipei' 分桶邏輯。
	actualMap := map[string]TrainingTotals{}
	arows, err := h.db.Query(ctx, `
		SELECT p.id, COUNT(DISTINCT (a.recorded_at AT TIME ZONE 'Asia/Taipei')::date), COALESCE(SUM(a.distance_km),0), COALESCE(SUM(a.duration_s),0)
		FROM training_plans p
		JOIN activities a ON a.user_id = p.user_id AND NOT a.flagged
		  AND (a.recorded_at AT TIME ZONE 'Asia/Taipei')::date BETWEEN p.start_date AND p.end_date
		WHERE p.user_id=$1
		GROUP BY p.id`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	for arows.Next() {
		var pid string
		var days, sec int
		var km float64
		if err := arows.Scan(&pid, &days, &km, &sec); err != nil {
			arows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		actualMap[pid] = TrainingTotals{Days: days, Km: round2(km), Min: int(math.Round(float64(sec) / 60))}
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// today 固定用 Asia/Taipei（UTC+8 位移），比照檔案內既有慣例（distroless production 無 tzdata，
	// 不可用 time.LoadLocation）。
	nowTaipei := time.Now().UTC().Add(8 * time.Hour)
	today := time.Date(nowTaipei.Year(), nowTaipei.Month(), nowTaipei.Day(), 0, 0, 0, 0, time.UTC)

	plans := make([]TrainingPlan, 0, len(planRows))
	for _, pr := range planRows {
		p := pr.plan
		totalDays := int(pr.endDate.Sub(pr.startDate).Hours()/24) + 1
		elapsed := int(today.Sub(pr.startDate).Hours()/24) + 1
		if elapsed < 0 {
			elapsed = 0
		}
		if elapsed > totalDays {
			elapsed = totalDays
		}
		p.Stats = PlanStats{
			Planned:       plannedMap[p.ID],
			Actual:        actualMap[p.ID],
			TotalDays:     totalDays,
			ElapsedDays:   elapsed,
			RemainingDays: totalDays - elapsed,
		}
		plans = append(plans, p)
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
	RunningAge   string  `json:"running_age"` // 跑齡等級：new|novice|experienced|veteran（quality 日數上限：new→最多 1 天）
	Best1kmS     int     `json:"best_1km_s"`
	LongestKm    float64 `json:"longest_km"`
	LongestMin   int     `json:"longest_min"`
	HasRace      bool    `json:"has_race"`
	RaceDate     string  `json:"race_date"`
	RaceName     string  `json:"race_name"` // 使用者自填的目標賽事名稱（migration 086）；只有有 race_date 才有意義，但沒 race_date 也照存
	RaceDistance string  `json:"race_distance"`
	Weeks        int     `json:"weeks"`
	RestDays     []int   `json:"rest_days"`   // 預定休息的星期索引，0=週一..6=週日（前端 checkbox 一..日）；其餘星期皆為訓練日
	MonthlyKm    int     `json:"monthly_km"`  // 目前月跑量(km)，選填。0=未填→行為與加此欄位前完全一致（只依賽事距離定 LSD 上限）。
	GoalTimeS    int     `json:"goal_time_s"` // 目標完賽秒數(如全馬4:30:00=16200)，選填。0=未設定；只影響「目標配速/可行性提示」，絕不拿來拉快訓練配速。
	PlanMode     string  `json:"plan_mode"`   // 保守/積極（migration 089）：conservative|aggressive；正規化見 normalizePlanMode。
}

// normalizePlanMode 正規化 plan_mode：只接受 "conservative"/"aggressive"，其餘（含空字串）一律視為
// "conservative"。預設保守的理由：會主動使用課表微調（adjust）把強度/量調回自己想要的樣子的，多半是
// 已經有經驗、清楚自己身體狀況的資深跑者——他們本就該自己選 aggressive；不會微調、也還不清楚怎麼
// 拿捏強度的多半是新手，預設就該把保護機制做滿，而不是預設塞給他們一份沒特別要求就比較激進的課表。
func normalizePlanMode(raw string) string {
	if raw == "aggressive" {
		return "aggressive"
	}
	return "conservative"
}

// raceDistanceLabel race_distance → 計畫命名用中文/簡稱；也做為合法值白名單。
var raceDistanceLabel = map[string]string{"5k": "5K", "10k": "10K", "half": "半馬", "full": "全馬"}

// recommendedMonthlyKm race_distance → volume_note 提示用的建議起訓月跑量下限(km)。僅用於文字提示，
// 不影響排程。取值涵蓋 AutoPlan 內 growth 全區間(1.15~1.8 倍，見下方跑量驅動 LSD 約束段落)下「長跑上限
// 可跨過 7K 量化門檻」所需月跑量的安全上緣，是常見教練經驗法則(週跑量約落在賽事距離的 4~5 倍)取整，
// 非精算值。
var recommendedMonthlyKm = map[string]int{"5k": 20, "10k": 30, "half": 50, "full": 80}

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

// adjustTypeForCategory 比照 migration 085 的 category→adjust_type 映射（見該檔 UPDATE 語句）。
// AutoPlan 產生器已經查到 category，直接用它推算，不必為此再多查一次 adjust_type 欄位。
func adjustTypeForCategory(category string) string {
	switch category {
	case "recovery", "easy", "lsd", "tempo", "threshold", "progression":
		return "distance"
	case "interval", "fartlek", "norwegian", "yasso", "rep":
		return "reps"
	case "pyramid":
		return "pyramid"
	default:
		return "none"
	}
}

// applyAdjustSegs 依 category 對應的 adjust_type 套用微調（比照前端 workout.ts applyAdjust）；
// 只實作 AutoPlan 賽前降量會用到的 distance/reps 兩種，pyramid/none 原樣不動——賽前週會被降量的
// quality 課表清單（qualityTemplates）不會選到 pyramid 課表。delta=0 或找不到可調整的段落時原樣回傳。
// distance：delta(km) 平均分攤到所有 work 距離段，各段夾下限 1000m。
// reps：主間歇 work 段（reps>1 者優先，否則第一個 work 段）reps += delta，夾 [1,20]。
func applyAdjustSegs(segs []templateSeg, category string, delta int) []templateSeg {
	if delta == 0 {
		return segs
	}
	out := make([]templateSeg, len(segs))
	copy(out, segs)
	switch adjustTypeForCategory(category) {
	case "distance":
		var idx []int
		for i, s := range out {
			if s.Kind == "work" && s.TargetType == "distance" {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			return segs
		}
		share := float64(delta*1000) / float64(len(idx))
		for _, i := range idx {
			out[i].Target = max(1000, out[i].Target+share)
		}
		return out
	case "reps":
		mainIdx := -1
		for i, s := range out {
			if s.Kind == "work" && s.Reps > 1 {
				mainIdx = i
				break
			}
		}
		if mainIdx < 0 {
			for i, s := range out {
				if s.Kind == "work" {
					mainIdx = i
					break
				}
			}
		}
		if mainIdx < 0 {
			return segs
		}
		r := out[mainIdx].Reps
		if r < 1 {
			r = 1
		}
		out[mainIdx].Reps = min(20, max(1, r+delta))
		return out
	default:
		return segs
	}
}

// qualityTaperDelta 概算「打對折」的降量 adjust 值，供 aggressive 模式賽前 2~7 天的 quality 課表
// 使用——與 role=="long" 的 longKm/2 用同一套「打對折」邏輯，讓賽前週真的「維持強度但降距離」
// （migration 089 / TrainingScreen.tsx 的承諾），而不是原封不動排入全份課表。
// reps 型：主課趟數的一半（無條件捨去，至少降 1 趟）。distance 型：work 距離段加總的一半（公里，
// 四捨五入，至少降 1K）。找不到可調整的段落時回傳 0（不調整，等同原行為）。
func qualityTaperDelta(category string, segs []templateSeg) int {
	switch adjustTypeForCategory(category) {
	case "reps":
		mainReps := 0
		for _, s := range segs {
			if s.Kind == "work" && s.Reps > 1 {
				mainReps = s.Reps
				break
			}
		}
		if mainReps == 0 {
			return 0
		}
		cut := mainReps / 2
		if cut < 1 {
			cut = 1
		}
		return -cut
	case "distance":
		workKm := 0.0
		for _, s := range segs {
			if s.Kind == "work" && s.TargetType == "distance" {
				workKm += s.Target / 1000
			}
		}
		if workKm <= 0 {
			return 0
		}
		cut := math.Round(workKm / 2)
		if cut < 1 {
			cut = 1
		}
		return -int(cut)
	default:
		return 0
	}
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

// raceDistanceKm 賽事距離代碼→公里；未知/未設定（含 race_distance==""）回 0。
func raceDistanceKm(d string) float64 {
	switch d {
	case "5k":
		return 5
	case "10k":
		return 10
	case "half":
		return 21.0975
	case "full":
		return 42.195
	default:
		return 0
	}
}

// riegelPredictS Riegel 公式：T2 = T1*(D2/D1)^k，從 1K 最佳成績推估 raceKm 距離的完賽秒數。
// 標準 k=1.06 是拿「同量級耐力賽事」互推（如 10K 推半馬）才準；本專案唯一有的成績是 1K PB——
// 距離極短、幾乎全無氧，直接套標準 k 值去推全馬會嚴重樂觀（低估長距離所需的有氧耐力衰減）。
// 故刻意採更保守的 k=1.10：寧可讓可行性提示低估、多提醒幾次，也不要用一個對新手過度樂觀的模型
// 去慫恿他們照著一個實際上到不了的配速硬練受傷。
func riegelPredictS(fastest1kmS, raceKm float64) float64 {
	if fastest1kmS <= 0 || raceKm <= 0 {
		return 0
	}
	const k = 1.10
	return fastest1kmS * math.Pow(raceKm, k)
}

// formatHMS 秒數→"H:MM:SS"（給目標時間/預估完賽時間顯示用，如 16200 → "4:30:00"）。
func formatHMS(totalS int) string {
	if totalS < 0 {
		totalS = 0
	}
	h := totalS / 3600
	m := (totalS % 3600) / 60
	s := totalS % 60
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

// goalPaceSecPerKm 目標配速（秒/公里）＝ goal_time_s ÷ 賽事距離(km)，四捨五入到秒；缺賽事距離
// 或未設目標時回 0。AutoPlan（產生當下）與 ListPlans（事後查詢）共用，避免同一算式寫兩份。
func goalPaceSecPerKm(goalTimeS int, raceDistance string) int {
	km := raceDistanceKm(raceDistance)
	if goalTimeS <= 0 || km <= 0 {
		return 0
	}
	return int(math.Round(float64(goalTimeS) / km))
}

// abs 絕對值（int）。
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// pickQualityDays 從候選日（呼叫端已排除長跑日與緊鄰長跑日者）中，依 count(0/1/2) 確定性地選出 quality 日：
// count=1 取候選清單中段者；count=2 取最早者＋另一個與最早者不相鄰的候選（優先從中段往後找，找不到再往回找），
// 真找不到第二個合格日則退化成只排 1 天。
func pickQualityDays(candidates []int, count int) []int {
	if count <= 0 || len(candidates) == 0 {
		return nil
	}
	if count == 1 {
		return []int{candidates[len(candidates)/2]}
	}
	first := candidates[0]
	mid := len(candidates) / 2
	second := -1
	for i := mid; i < len(candidates); i++ {
		if candidates[i] != first && abs(candidates[i]-first) > 1 {
			second = candidates[i]
			break
		}
	}
	if second == -1 {
		for i := mid - 1; i >= 0; i-- {
			if candidates[i] != first && abs(candidates[i]-first) > 1 {
				second = candidates[i]
				break
			}
		}
	}
	if second == -1 {
		return []int{first}
	}
	return []int{first, second}
}

// weekdayRoleAssignment 依 restDays（0=週一..6=週日，超出範圍的值忽略）指派星期→角色（long/quality/easy）。
// trainingWeekdays＝0..6 扣掉 restDays；ok=false 代表沒有任何訓練日（呼叫端應回 400 need_training_day）。
// 長跑日：非休息日中優先週日(6)，否則週六(5)，否則最晚的非休息日。
// quality 日：從非休息訓練日（排除長跑日、且排除緊鄰長跑日者）中選 1-2 天，數量＝trainingDays>=5→2、
// trainingDays>=3→1、否則 0，再受 running_age 上限（new→最多 1），最後受 planMode 上限：
// conservative 在原本數量 >0 時再少 1 天，但下限 1（不可變成 0——保守只是降強度/降頻率，仍要保留
// 「有機會完成目標」的最低限度；原本就是 0 天的情況不受影響，不會憑空生出一天 quality）；
// aggressive 完全不受此步驟影響（維持現況）。
// 回傳：星期索引→角色、訓練天數(daysPerWeek 顯示用)、是否成功。
func weekdayRoleAssignment(restDays []int, runningAge string, planMode string) (map[int]string, int, bool) {
	restSet := map[int]bool{}
	for _, d := range restDays {
		if d >= 0 && d <= 6 {
			restSet[d] = true
		}
	}
	var trainingWeekdays []int
	for d := 0; d < 7; d++ {
		if !restSet[d] {
			trainingWeekdays = append(trainingWeekdays, d)
		}
	}
	trainingDays := len(trainingWeekdays)
	if trainingDays == 0 {
		return nil, 0, false
	}

	longDay := trainingWeekdays[trainingDays-1] // fallback：最晚的非休息日
	switch {
	case !restSet[6]:
		longDay = 6
	case !restSet[5]:
		longDay = 5
	}

	var candidates []int
	for _, d := range trainingWeekdays {
		diff := abs(d - longDay)
		if d == longDay || diff == 1 || diff == 6 { // 環狀相鄰：週日(6)↔週一(0) 也算緊鄰長跑日，不排 quality
			continue
		}
		candidates = append(candidates, d)
	}

	qualityCount := 0
	switch {
	case trainingDays >= 5:
		qualityCount = 2
	case trainingDays >= 3:
		qualityCount = 1
	}
	if runningAge == "new" && qualityCount > 1 {
		qualityCount = 1
	}
	if planMode == "conservative" && qualityCount > 0 {
		qualityCount--
		if qualityCount < 1 {
			qualityCount = 1
		}
	}
	if qualityCount > len(candidates) {
		qualityCount = len(candidates)
	}

	roles := map[int]string{longDay: "long"}
	for _, d := range pickQualityDays(candidates, qualityCount) {
		roles[d] = "quality"
	}
	for _, d := range trainingWeekdays {
		if _, ok := roles[d]; !ok {
			roles[d] = "easy"
		}
	}

	return roles, trainingDays, true
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
// Adjust：aggressive 賽前週降量用（見 qualityTaperDelta），預設 0＝不調整，與 CreateSchedule 手動
// 排程沿用同一顆 user_training_schedule.adjust 欄位（migration 085）。
type plannedWorkout struct {
	Date         string
	TemplateCode string
	Adjust       int
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
	planMode := normalizePlanMode(req.PlanMode)
	// monthly_km/goal_time_s 皆選填，超出合理範圍一律視為「未填」（歸零）而非拒絕請求——兩者只是
	// 錦上添花的輸入，不該讓一個離譜數字擋掉整個產生器；歸零後續行為就等同沒填這兩個欄位（回歸底線）。
	if req.MonthlyKm < 0 || req.MonthlyKm > 2000 {
		req.MonthlyKm = 0
	}
	if req.GoalTimeS != 0 && (req.GoalTimeS < 600 || req.GoalTimeS > 172800) {
		req.GoalTimeS = 0
	}
	// race_name：TrimSpace + 用 []rune 截斷（絕不可用 byte 切，會切壞 UTF-8 中文）。
	raceName := strings.TrimSpace(req.RaceName)
	if rn := []rune(raceName); len(rn) > 40 {
		raceName = string(rn[:40])
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

	// 目標配速 + Riegel 可行性提示：只用來「顯示」與「提醒」，絕不回頭影響下面的訓練配速
	// （訓練配速一律仍依 pace_level／目前體能），也絕不擋 AutoPlan 繼續產生計畫。
	raceKm := raceDistanceKm(req.RaceDistance)
	goalPaceS := goalPaceSecPerKm(req.GoalTimeS, req.RaceDistance)
	goalNote := ""
	if req.GoalTimeS > 0 && raceKm > 0 {
		if pred := riegelPredictS(float64(req.Best1kmS), raceKm); pred > 0 && float64(req.GoalTimeS) < pred*0.95 {
			goalNote = fmt.Sprintf("以目前 1K 最佳成績推估，%s約 %s；%s 目標偏積極。計畫仍依目前體能安排，請以完賽為優先。",
				raceDistanceLabel[req.RaceDistance], formatHMS(int(math.Round(pred))), formatHMS(req.GoalTimeS))
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

	roles, daysPerWeek, ok := weekdayRoleAssignment(req.RestDays, req.RunningAge, planMode)
	if !ok {
		respondErr(w, http.StatusBadRequest, "need_training_day")
		return
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

	// volumeNote：跑量驅動 LSD 約束的附帶說明，只顯示、不影響排程（沿用 goalNote 的模式）。宣告在
	// `if req.MonthlyKm > 0` 區塊外，否則區塊結束後變數就離開作用域，下面 respondJSON 讀不到。
	volumeNote := ""

	// 跑量驅動的 LSD 約束（monthly_km>0 時才套用；=0 時完全不動上面算出的 capKm/startLong，
	// 是回歸底線）。動機：單次跑過 21K 不代表「每週都能吃 21K」——後者靠的是月跑量長期堆出來的
	// 耐受度，前者只證明身體某一天撐得住一次。longRunCap 只看賽事距離/最長跑過距離，完全不看
	// 月跑量，會把月跑量僅 150K 的人一路推到 32K 長跑，等同主動叫他受傷。故改用「當週跑量的固定
	// 比例」重新框住長跑距離，取代（縮小）純看距離推出的 capKm/startLong。
	if req.MonthlyKm > 0 {
		weeklyBase := float64(req.MonthlyKm) / 4 // 月跑量→週跑量基準，例：150K/月 → 37.5K/週
		// pct：每週跑量增幅上限。conservative（保守，降強度、多一層保護）8%；aggressive（積極，維持
		// 現況）10%——這是任務3唯一要調整的三個旋鈕之一，其餘常數（40%長跑佔比、4週恢復週、×0.65、
		// taper 週數等）不動。
		pct := 0.10
		if planMode == "conservative" {
			pct = 0.08
		}
		// growthCap：growth 倍率的上限。conservative 1.5、aggressive 維持 1.8（任務3的第二個旋鈕）；
		// 下限 1.15 兩模式共用，避免 rampWeeks 極短時倍數失控。
		growthCap := 1.8
		if planMode == "conservative" {
			growthCap = 1.5
		}
		// growth：隨 rampWeeks（真正可以逐週漸增的週數）拉長，逐步放大「週跑量峰值」相對基準的倍數；
		// 週數越多代表有越長時間可以安全地把量堆上去。1.15/growthCap 是防呆下限/上限，避免 rampWeeks
		// 極端值（很短或很長）時倍數失控。
		growth := min(growthCap, max(1.15, 1+0.035*float64(rampWeeks)))
		weeklyPeak := weeklyBase * growth
		if rampWeeks > 1 {
			// 峰值週量粗上限：不超過用 pct 從 weeklyBase 疊 rampWeeks-1 次所能到達的量。這只是「量體」
			// 的防呆上限，不等於下面長跑曲線本身合規——長跑漸增走的是 startLong→capKm 這條線，基數跟
			// weeklyBase→weeklyPeak 不同；真正保證長跑曲線每週增幅 <=pct 的判斷在下面對 capKm 的處理。
			weeklyPeak = min(weeklyPeak, weeklyBase*(1+pct*float64(rampWeeks-1)))
		} else {
			weeklyPeak = weeklyBase
		}
		capKm = min(capKm, weeklyPeak*0.40)                 // 長跑上限＝峰值週量的 40%（常見經驗值，避免單次長跑吃掉過高比例的週跑量）
		startLong = max(4, min(startLong, weeklyBase*0.40)) // 起始長跑＝目前週量的 40%，且不超過「已經跑過的最長距離」（不可無中生有推高起點）
		if rampWeeks > 1 {
			// pct 規則：每週跑量增幅不可超過前一週的 pct（conservative 8%／aggressive 10%），避免練
			// 太快造成傷害。真正逐週漸增的是下面 nominal := startLong + (capKm-startLong)*t 這條線性
			// 曲線，不是 weeklyBase→weeklyPeak。線性 ramp 下相鄰兩週的增量固定 =
			// (capKm-startLong)/(rampWeeks-1)，其占「起點」startLong 的百分比在起點（基數最小）最大、
			// 越往後基數變大增幅自動遞減；因此只要 capKm <= startLong*(1+pct*(rampWeeks-1))，增幅最大
			// 的第一週就不超過 pct，中段每一週的實際增幅必然更小，全程自動合規，不需逐週檢查。
			// 恢復週（每 4 週回彈到 nominal*0.65，見下方 isRecovery）刻意不受此限：pct 規則管的是
			// 「訓練量趨勢線」的長期爬升速度，恢復週是刻意的短期回落、事後會再回升到趨勢線上，並非
			// 趨勢線本身；若也套 pct 上限，會反過來逼恢復週不夠低、讓恢復週失去恢復的意義。
			//
			// ⚠️ 此約束式必須套在 startLong 定案之後（startLong 已經過 min(capKm, max(4, ...)) 確定），
			// 不可誤套在 weeklyPeak/weeklyBase 上——那是不同的基數，套錯會導致 longest_km 較小時第一週
			// 增幅可達 +70%（曾發生過的迴歸）。
			capKm = min(capKm, startLong*(1+pct*float64(rampWeeks-1)))
		}
		capKm = max(startLong, capKm) // 防呆：避免 cap 被壓得比 start 還低，造成長跑曲線倒退

		// 月跑量很低時 capKm 會被壓到很小（例：月跑量 40K + 全馬 + 23 週 → capKm≈6.94）。這裡刻意
		// 不加 capKm 下限去「救」這個數字——月跑量低就是該跑少，是刻意的保護（新手要優先考慮休息/
		// 恢復/避免受傷，不是把長跑硬撐上去）。但 capKm<7 時會撞上 nearestLsdCode 的量化死角：目前
		// 最小長跑課表是 lsd_6，次小 lsd_8，兩者判定中點剛好在 7.0，所以只要整條 startLong→capKm 的
		// ramp 都落在 7 以下，每一週都會被 nearestLsdCode 量化成同一個 lsd_6，使用者會看到全程（例中
		// 23 週）長跑距離完全沒有漸增、卻沒有任何解釋。問題出在「靜默」，不在數字小，所以在這裡把原因
		// 講清楚——沿用 goalNote「只顯示、不影響排程」的作法，用 volume_note 說明現況並給出後續建議。
		if capKm < 7 && req.RaceDistance != "" {
			volumeNote = fmt.Sprintf("目前月跑量 %dK 偏低，長跑距離已依跑量下修至 %dK 上下；要安全完成%s，建議先把月跑量堆到 %dK 以上再重排計畫。",
				req.MonthlyKm, int(math.Round(capKm)), raceDistanceLabel[req.RaceDistance], recommendedMonthlyKm[req.RaceDistance])
		}
	}

	qualityCounter := 0
	easyCounter := 0
	var plan []plannedWorkout

	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if req.RaceDate != "" && !d.Before(raceDateTime) {
			continue // 賽事當日不排課表（那天是比賽，且迴圈不會超過 raceDateTime=end）
		}
		role, hasRole := roles[(int(d.Weekday())+6)%7]
		if !hasRole {
			continue
		}
		weekIndex := int(d.Sub(start).Hours()/24) / 7
		phase := phaseFor(weekIndex, weeks, baseWeeks, taperWeeks)
		isRecovery := weekIndex%4 == 3 && phase != "taper"

		var code string
		var longKm float64 // role=="long" 時記錄最終選 code 用的公里數；aggressive 賽前一週降距離要用（見下方覆蓋區塊）
		var adjust int     // aggressive 賽前一週 quality 降量用（見 qualityTaperDelta）；0＝不調整
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
			longKm = km
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
		// 賽前一週規則（依 plan_mode 分流；migration 089）。位置維持在原本 shakeout 覆蓋的地方——
		// role→code 選完之後、templates 存在性檢查之前，覆蓋 role 判斷選出的 code。d/raceDateTime
		// 皆為 UTC 午夜 DATE 值（Go time.Date 於 UTC 無 DST），Sub().Hours()/24 恆為整數，賽事當日已於
		// 迴圈開頭 continue 跳過，故 d 必早於 raceDateTime、daysToRace 恆 >=1。無 race_date（weeks 模式）
		// 時 req.RaceDate=="" ，整段規則完全不套用，不受 plan_mode 影響。
		if req.RaceDate != "" {
			daysToRace := int(raceDateTime.Sub(d).Hours() / 24)
			switch planMode {
			case "conservative":
				// 保守：賽前一天完全不排課（休息日）——用 continue 跳過，不留一份空課表；
				// 賽前 2~7 天一律不得有強度課或長跑，強制覆蓋成維持跑感的輕鬆跑。
				if daysToRace == 1 {
					continue
				}
				if daysToRace >= 2 && daysToRace <= 7 {
					code = "easy_4" // 覆蓋 long/quality/easy 的原選擇；easy_4 = 熱身1K+輕鬆4K+緩和1K ≈ 6K，維持跑感
				}
			case "aggressive":
				// 積極：賽前一天維持跑感的輕鬆跑；賽前 2~7 天保留原本 role 選出的課表（含強度），但降量。
				if daysToRace == 1 {
					code = "easy_4"
				} else if daysToRace >= 2 && daysToRace <= 7 {
					// long：距離減半，沿用既有 nearestLsdCode 換算成最接近的較短長跑變體。
					// quality：課表不變（強度/配速維持原樣），改用 adjust（既有 migration 085 欄位，
					// CreateSchedule 手動排程也是存這欄）把主課趟數/距離打對折——qualityTaperDelta 與
					// long 的 longKm/2 是同一套「打對折」邏輯，plannedKm/plannedMin 會在下方 INSERT
					// 迴圈依 adjust 重新計算，真正做到「維持強度但降距離」，不是空口承諾。
					// easy：本已是低量課表，同樣維持原 code、不調整。
					if role == "long" {
						code = nearestLsdCode(longKm / 2)
					} else if role == "quality" {
						if t, ok := templates[code]; ok {
							adjust = qualityTaperDelta(t.Category, t.Segments)
						}
					}
				}
			}
		}

		if _, ok := templates[code]; !ok {
			continue // 防禦：理論上不會發生（所有 code 皆來自固定清單，且都在 seed migration 建立）
		}
		plan = append(plan, plannedWorkout{Date: d.Format("2006-01-02"), TemplateCode: code, Adjust: adjust})
	}

	name := fmt.Sprintf("%d週訓練", weeks)
	if req.RaceDistance != "" {
		name = fmt.Sprintf("%d週·%s", weeks, raceDistanceLabel[req.RaceDistance])
	}

	// 一份課表都排不出來就不要建計畫：conservative 模式下「賽事就在明天」會讓唯一可排的那天
	// 落進「賽前一天休息」而被跳過 → 產生零課表的空計畫，還白白佔掉 3 個計畫額度中的一個。
	if len(plan) == 0 {
		respondErr(w, http.StatusBadRequest, "no_workout_scheduled")
		return
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
		INSERT INTO training_plans (user_id, name, race_name, race_date, race_distance, weeks, days_per_week, pace_level, start_date, end_date, monthly_km, goal_time_s, plan_mode)
		VALUES ($1,$2,$3,$4::date,$5,$6,$7,$8,$9::date,$10::date,$11,$12,$13)
		RETURNING id`,
		uid, name, raceName, raceDateParam, req.RaceDistance, weeks, daysPerWeek, selectedLevel,
		start.Format("2006-01-02"), end.Format("2006-01-02"), req.MonthlyKm, req.GoalTimeS, planMode).Scan(&planID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	pm := levelPaces[selectedLevel]
	for _, pw := range plan {
		t := templates[pw.TemplateCode]
		segs := t.Segments
		if pw.Adjust != 0 {
			segs = applyAdjustSegs(t.Segments, t.Category, pw.Adjust) // aggressive 賽前週 quality 降量（見 qualityTaperDelta）
		}
		plannedKm := segTotalKm(segs)
		plannedMin := segEstMinutes(segs, pm)
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_training_schedule (user_id, plan_id, scheduled_date, template_code, pace_level, name, category, planned_km, planned_min, adjust)
			VALUES ($1,$2,$3::date,$4,$5,$6,$7,$8,$9,$10)`,
			uid, planID, pw.Date, pw.TemplateCode, selectedLevel, t.Name, t.Category, plannedKm, plannedMin, pw.Adjust); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	result := TrainingPlan{
		ID: planID, Name: name, RaceName: raceName, RaceDistance: req.RaceDistance, Weeks: weeks, DaysPerWeek: daysPerWeek,
		PaceLevel: selectedLevel, StartDate: start.Format("2006-01-02"), EndDate: end.Format("2006-01-02"),
		WorkoutCount: len(plan), MonthlyKm: req.MonthlyKm, GoalTimeS: req.GoalTimeS, GoalPaceS: goalPaceS, PlanMode: planMode,
	}
	if req.RaceDate != "" {
		rd := req.RaceDate
		result.RaceDate = &rd
	}
	respondJSON(w, http.StatusOK, map[string]any{"plan": result, "goal_note": goalNote, "volume_note": volumeNote})
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
