// Package training 自主訓練（P1 課表庫 + P2 月曆排程），VIP 限定功能。
//
// 前端拿到 TemplateSegment（以「效度 effort」表達強度：easy/marathon/threshold/interval/rep）＋
// 玩家自選的 PaceLevel，在前端解析成既有 WorkoutSegment（帶實際配速秒/公里），沿用 /track 既有
// 分段課表引擎（見 apps/web/src/lib/workout.ts）。P1 只提供清單；P2 新增「每人每日一份課表」的
// 月曆排程（user_training_schedule，migration 083）——排程本身不觸發任何完成/獎勵，跑步照常走
// GPS 上傳自動發里程 EXP，月曆只是把「排定」與「實際」對照顯示。
package training

import (
	"encoding/json"
	"errors"
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

// requireVIP 登入 + VIP 檢查共用 helper（P1 Templates 與 P2 三個排程端點共用）。
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
	r.Get("/templates", h.Templates)        // GET /training/templates — 課表庫 + 配速等級表（VIP 限定）
	r.Get("/calendar", h.Calendar)          // GET /training/calendar?month=YYYY-MM — 月曆排程 vs 實際（VIP 限定）
	r.Post("/schedule", h.UpsertSchedule)   // POST /training/schedule — 排定/更新單日課表（VIP 限定）
	r.Delete("/schedule", h.DeleteSchedule) // DELETE /training/schedule?date=YYYY-MM-DD — 取消單日排程（VIP 限定）
	return r
}

// Templates GET /training/templates — VIP 專屬：課表庫 + 配速等級表。
func (h *Handler) Templates(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}

	tRows, err := h.db.Query(r.Context(), `
		SELECT code, name, category, description, segments, sort_order
		FROM workout_templates WHERE enabled ORDER BY sort_order`)
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

// --- P2 月曆排程 JSON 型別（契約見 apps/web/src/lib/api.ts）---

// ScheduledWorkout 單日排定課表；name/category 為存檔當下的快照，template_code/pace_level 供前端
// 重新解析分段（沿用 workout.ts resolveTemplate）。
type ScheduledWorkout struct {
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

// TrainingDay 月曆單日：排定 vs 實際。
type TrainingDay struct {
	Date        string            `json:"date"`
	Scheduled   *ScheduledWorkout `json:"scheduled"`
	ActualKm    float64           `json:"actual_km"`
	HasActivity bool              `json:"has_activity"`
}

// TrainingTotals 整月統計（排定或實際各一份）。
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

	// 排定：該月 user_training_schedule rows。
	schedRows, err := h.db.Query(ctx, `
		SELECT scheduled_date, template_code, pace_level, name, category, planned_km, planned_min
		FROM user_training_schedule
		WHERE user_id=$1 AND scheduled_date >= $2::date AND scheduled_date < ($2::date + INTERVAL '1 month')`,
		uid, monthStartStr)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	scheduled := map[string]ScheduledWorkout{}
	var plannedKm float64
	var plannedMin int
	for schedRows.Next() {
		var d time.Time
		var sw ScheduledWorkout
		if err := schedRows.Scan(&d, &sw.TemplateCode, &sw.PaceLevel, &sw.Name, &sw.Category, &sw.PlannedKm, &sw.PlannedMin); err != nil {
			schedRows.Close()
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		scheduled[d.Format("2006-01-02")] = sw
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
		td := TrainingDay{Date: dateStr}
		if sw, found := scheduled[dateStr]; found {
			swCopy := sw
			td.Scheduled = &swCopy
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

// UpsertSchedule POST /training/schedule — VIP 專屬：排定/更新單日課表（PK user_id+scheduled_date upsert）。
// name/category 一律由後端依 template_code 查 workout_templates 填入（權威快照），不信任前端傳入值。
func (h *Handler) UpsertSchedule(w http.ResponseWriter, r *http.Request) {
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
		ON CONFLICT (user_id, scheduled_date) DO UPDATE SET
			template_code=EXCLUDED.template_code, pace_level=EXCLUDED.pace_level,
			name=EXCLUDED.name, category=EXCLUDED.category,
			planned_km=EXCLUDED.planned_km, planned_min=EXCLUDED.planned_min
		RETURNING scheduled_date, template_code, pace_level, name, category, planned_km, planned_min`,
		uid, req.Date, req.TemplateCode, req.PaceLevel, name, category, req.PlannedKm, req.PlannedMin).
		Scan(&d, &row.TemplateCode, &row.PaceLevel, &row.Name, &row.Category, &row.PlannedKm, &row.PlannedMin); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	row.Date = d.Format("2006-01-02")

	respondJSON(w, http.StatusOK, row)
}

// DeleteSchedule DELETE /training/schedule?date=YYYY-MM-DD — VIP 專屬：取消單日排程。
func (h *Handler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	date := r.URL.Query().Get("date")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid date")
		return
	}
	if _, err := h.db.Exec(r.Context(), `DELETE FROM user_training_schedule WHERE user_id=$1 AND scheduled_date=$2::date`, uid, date); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
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
