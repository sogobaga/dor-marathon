// Package event 事件任務（Phase A：日常隨機事件）。
// 觸發/完成為模組化的 type + 參數；獎勵直接加 users.exp/dp（非賽事，不走 exp_ledger），
// 以 occurrence 列作紀錄與冪等守門。
package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/realtime"
)

const maxSpeedMS = 1000.0 / 120.0 // 8.33 m/s（2:00/km）人類極限，防瞬移

// --- 型錄（後端單一真實來源，前端鏡像）---

type ParamSpec struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Unit  string `json:"unit"`
}
type TypeSpec struct {
	Key    string      `json:"key"`
	Label  string      `json:"label"`
	Params []ParamSpec `json:"params"`
}

// TriggerCatalog 觸發條件模組（距離類 + 配速類；由前端跑步引擎即時評估，非賽事，無伺服器端防弊需求——
// 觸發只是「讓事件跳出來」，獎勵仍靠完成條件的伺服器驗證，故偽造觸發無利可圖）。
// 配速單位一律「秒/公里」（值越大＝跑越慢，例：420=7:00/km、360=6:00/km、300=5:00/km）。
var TriggerCatalog = []TypeSpec{
	{"distance_below", "近期幾乎沒移動（原地）", []ParamSpec{
		{"window_s", "觀察時間", "秒"},
		{"max_move_m", "此時間內移動小於", "公尺"},
	}},
	{"distance_above", "近期移動很多", []ParamSpec{
		{"window_s", "觀察時間", "秒"},
		{"min_move_m", "此時間內移動大於", "公尺"},
	}},
	{"pace_slow", "配速偏慢（跑太慢時）", []ParamSpec{
		{"window_s", "觀察時間", "秒"},
		{"min_move_m", "此時間內至少移動（排除原地）", "公尺"},
		{"slower_than_spk", "配速慢於", "秒/公里"},
	}},
	{"pace_fast", "配速偏快（跑很快時）", []ParamSpec{
		{"window_s", "觀察時間", "秒"},
		{"faster_than_spk", "配速快於", "秒/公里"},
	}},
	{"pace_drop", "越跑越慢（配速明顯下滑）", []ParamSpec{
		{"window_s", "觀察時間", "秒"},
		{"min_move_m", "此時間內至少移動（排除原地）", "公尺"},
		{"drop_spk", "後半配速比前半慢超過", "秒/公里"},
	}},
}

// CompletionCatalog 完成條件模組
var CompletionCatalog = []TypeSpec{
	{"move_more", "限時內再移動達標", []ParamSpec{
		{"limit_s", "完成時限", "秒"},
		{"target_m", "需移動大於", "公尺"},
	}},
	{"move_less", "限時內維持不超過（穩住）", []ParamSpec{
		{"limit_s", "維持時間", "秒"},
		{"max_m", "移動需小於", "公尺"},
	}},
	{"hold_pace", "維持配速（全程不能停）", []ParamSpec{
		{"limit_s", "維持時間", "秒"},
		{"check_s", "檢查區間", "秒"},
		{"min_m", "每區間至少移動", "公尺"},
	}},
	{"sprint", "衝刺加速（短時間爆發）", []ParamSpec{
		{"limit_s", "時限", "秒"},
		{"burst_s", "爆發區間", "秒"},
		{"burst_m", "爆發區間需移動", "公尺"},
	}},
	{"pace_shift", "變速跑（相對平均配速）", []ParamSpec{
		{"limit_s", "持續時間", "秒"},
		{"faster", "方向（1=加速 0=減速）", ""},
		{"delta_spk", "與平均配速差", "秒/公里"},
	}},
	{"tap_burst", "連續點擊（物理攻擊）", []ParamSpec{
		{"limit_s", "時限", "秒"},
		{"target_taps", "目標點擊次數", "次"},
	}},
	{"hold_press", "按住螢幕（物理防禦）", []ParamSpec{
		{"limit_s", "時限", "秒"},
		{"hold_s", "需按住時間", "秒"},
	}},
	{"swipe_charge", "連續滑動蓄力（魔法攻擊）", []ParamSpec{
		{"limit_s", "時限", "秒"},
		{"target_px", "累積滑動距離（建議 3000–8000）", "px"},
	}},
	{"dodge_swipe", "連續滑動閃避", []ParamSpec{
		{"limit_s", "時限", "秒"},
		{"target_swipes", "閃避次數（滑動段數）", "次"},
	}},
	{"draw_shape", "畫出圖形（魔法陣）", []ParamSpec{
		{"limit_s", "時限", "秒"},
		{"attempts", "可嘗試次數", "次"},
		{"w3", "三角 出現權重", ""},
		{"x3_exp", "三角 加碼 EXP", "點"},
		{"x3_dp", "三角 加碼 DP", "點"},
		{"w4", "四角 出現權重", ""},
		{"x4_exp", "四角 加碼 EXP", "點"},
		{"x4_dp", "四角 加碼 DP", "點"},
		{"w5", "五芒星 出現權重", ""},
		{"x5_exp", "五芒星 加碼 EXP", "點"},
		{"x5_dp", "五芒星 加碼 DP", "點"},
	}},
	{"negative_split", "後段加速（舊，建議改用變速跑）", []ParamSpec{
		{"limit_s", "時限", "秒"},
		{"ratio_pct", "後半需為前半的", "%"},
	}},
}

func validTrigger(k string) bool {
	for _, t := range TriggerCatalog {
		if t.Key == k {
			return true
		}
	}
	return false
}
func validCompletion(k string) bool {
	for _, t := range CompletionCatalog {
		if t.Key == k {
			return true
		}
	}
	return false
}

// --- 型別 ---

type EventDef struct {
	ID               string             `json:"id,omitempty"`
	Name             string             `json:"name"`
	Description      string             `json:"description,omitempty"`
	Enabled          bool               `json:"enabled"`
	Weight           int                `json:"weight"`
	CooldownSec      int                `json:"cooldown_sec"`
	TriggerType      string             `json:"trigger_type"`
	TriggerParams    map[string]float64 `json:"trigger_params"`
	CompletionType   string             `json:"completion_type"`
	CompletionParams map[string]float64 `json:"completion_params"`
	Message          string             `json:"message"`
	GoalText         string             `json:"goal_text"`       // 自訂任務目標說明（留空＝用 goalText() 自動產生）
	ImageURL         string             `json:"image_url"`       // 預設圖（時段未設定時回退）
	ImageDayURL      string             `json:"image_day_url"`   // 白天 06:00–17:00
	ImageDuskURL     string             `json:"image_dusk_url"`  // 黃昏 17:00–19:00
	ImageNightURL    string             `json:"image_night_url"` // 晚上 19:00–06:00
	RewardExp        int                `json:"reward_exp"`
	RewardDp         int                `json:"reward_dp"`
}

type Handler struct {
	db *pgxpool.Pool
	rt *realtime.Manager
}

func NewHandler(db *pgxpool.Pool, rt *realtime.Manager) *Handler {
	return &Handler{db: db, rt: rt}
}

// eventWaitBounds 事件任務的隨機等待區間（秒）。取自系統設定 app_settings，含合理夾限。
// 前端跑步引擎每次事件間會在 [min,max] 隨機取一個等待時間；min 同時作為伺服器端防濫用地板。
func (h *Handler) eventWaitBounds(ctx context.Context) (minSec, maxSec int) {
	minSec = appsettings.GetInt(ctx, h.db, "event_wait_min_sec", 300)
	maxSec = appsettings.GetInt(ctx, h.db, "event_wait_max_sec", 900)
	if minSec < 60 {
		minSec = 60
	}
	if maxSec < minSec {
		maxSec = minSec
	}
	if maxSec > 3600 {
		maxSec = 3600
	}
	return
}

// stagedFirstWait 新手加速：依帳號「已完成上傳跑步筆數」回傳本趟「第一個事件」的等待秒數，
// 讓新玩家更快遇到事件。runCount 0/1/2 → 第 1/2/3 趟（較短，預設 45/90/180 秒，後台可調）；
// ≥3 → 回 0（前端改用正常隨機區間）。gps_runs 只在跑步完成上傳時新增，故計數＝已完成趟數。
func (h *Handler) stagedFirstWait(ctx context.Context, uid string) int {
	if uid == "" {
		return 0
	}
	var runCount int
	if err := h.db.QueryRow(ctx, `SELECT count(*) FROM gps_runs WHERE user_id=$1`, uid).Scan(&runCount); err != nil {
		return 0
	}
	var key string
	var def int
	switch runCount {
	case 0:
		key, def = "event_first_wait_run1_sec", 45
	case 1:
		key, def = "event_first_wait_run2_sec", 90
	case 2:
		key, def = "event_first_wait_run3_sec", 180
	default:
		return 0
	}
	v := appsettings.GetInt(ctx, h.db, key, def)
	if v < 5 {
		return 0 // 明確設得過小 → 視為關閉（用正常區間）
	}
	return v
}

// taskGateOpen 全域任務閘門：一個跑者同時只有一個進行中任務、任務間至少 min 等待（防濫用地板）。
// 只要近 min 秒內有任何 Phase A 觸發或 Phase B 加入紀錄 → 閘門關閉（不再觸發/不可加入）。
// 實際節奏由前端在 [min,max] 隨機等待決定；此處僅擋「比 min 還快」的重複觸發。
func (h *Handler) taskGateOpen(ctx context.Context, uid string) (bool, error) {
	floor, _ := h.eventWaitBounds(ctx)
	var recent bool
	err := h.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM event_task_occurrences WHERE user_id=$1 AND triggered_at > NOW() - make_interval(secs => $2)
			UNION ALL
			SELECT 1 FROM event_race_participants WHERE user_id=$1 AND joined_at > NOW() - make_interval(secs => $2)
		)`, uid, floor).Scan(&recent)
	return !recent, err
}

// --- Admin CRUD（掛 /admin/events，需 event_tasks 權限）---

func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Put("/{id}", h.Update)
	r.Delete("/{id}", h.Delete)
	r.Post("/{id}/push", h.PushToUser) // 測試：手動觸發此事件給指定帳號
	// 每個管理者專屬的「測試觸發」常用名單（非全站共用）
	r.Get("/test-targets", h.ListTestTargets)
	r.Post("/test-targets", h.AddTestTarget)
	r.Delete("/test-targets", h.RemoveTestTarget)
	r.Patch("/test-targets/default", h.SetDefaultTestTarget)
	return r
}

type testTarget struct {
	Email     string `json:"email"`
	IsDefault bool   `json:"is_default"`
}

// respondTestTargets 回傳目前管理者的常用名單（預設值排最前）
func (h *Handler) respondTestTargets(w http.ResponseWriter, ctx context.Context, adminID string) {
	rows, err := h.db.Query(ctx, `SELECT email, is_default FROM admin_test_targets WHERE admin_user_id=$1 ORDER BY is_default DESC, created_at`, adminID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []testTarget{}
	for rows.Next() {
		var t testTarget
		if rows.Scan(&t.Email, &t.IsDefault) == nil {
			out = append(out, t)
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"targets": out})
}

func adminID(r *http.Request) string {
	id, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	return id
}

func (h *Handler) ListTestTargets(w http.ResponseWriter, r *http.Request) {
	h.respondTestTargets(w, r.Context(), adminID(r))
}

func (h *Handler) AddTestTarget(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email       string `json:"email"`
		MakeDefault bool   `json:"make_default"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || strings.TrimSpace(b.Email) == "" {
		respondErr(w, http.StatusBadRequest, "請提供 email")
		return
	}
	uid, email := adminID(r), strings.TrimSpace(b.Email)
	if _, err := h.db.Exec(r.Context(), `INSERT INTO admin_test_targets (admin_user_id, email) VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid, email); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if b.MakeDefault {
		h.setDefaultTarget(r.Context(), uid, email)
	}
	h.respondTestTargets(w, r.Context(), uid)
}

func (h *Handler) RemoveTestTarget(w http.ResponseWriter, r *http.Request) {
	uid := adminID(r)
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if email == "" {
		respondErr(w, http.StatusBadRequest, "請提供 email")
		return
	}
	_, _ = h.db.Exec(r.Context(), `DELETE FROM admin_test_targets WHERE admin_user_id=$1 AND email=$2`, uid, email)
	h.respondTestTargets(w, r.Context(), uid)
}

func (h *Handler) SetDefaultTestTarget(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	uid := adminID(r)
	h.setDefaultTarget(r.Context(), uid, strings.TrimSpace(b.Email)) // email 空＝清除預設
	h.respondTestTargets(w, r.Context(), uid)
}

func (h *Handler) setDefaultTarget(ctx context.Context, uid, email string) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `UPDATE admin_test_targets SET is_default=FALSE WHERE admin_user_id=$1`, uid)
	if email != "" {
		_, _ = tx.Exec(ctx, `UPDATE admin_test_targets SET is_default=TRUE WHERE admin_user_id=$1 AND email=$2`, uid, email)
	}
	_ = tx.Commit(ctx)
}

// POST /admin/events/{id}/push  {email} — 手動觸發事件給指定帳號（測試用）。
// 對方需在「開始跑步」狀態，其 /track 會於數秒內認領並觸發；未認領 3 分鐘後過期。
func (h *Handler) PushToUser(w http.ResponseWriter, r *http.Request) {
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	defID := chi.URLParam(r, "id")
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Email) == "" {
		respondErr(w, http.StatusBadRequest, "請提供帳號 email")
		return
	}
	var uid, name string
	err := h.db.QueryRow(r.Context(),
		`SELECT id::text, COALESCE(NULLIF(name,''),email) FROM users WHERE lower(email)=lower($1)`,
		strings.TrimSpace(body.Email)).Scan(&uid, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "查無此 email 的帳號")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	var exists bool
	_ = h.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM event_task_defs WHERE id=$1)`, defID).Scan(&exists)
	if !exists {
		respondErr(w, http.StatusNotFound, "事件不存在")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO event_manual_pushes (def_id, target_user_id, created_by) VALUES ($1,$2,NULLIF($3,'')::uuid)`,
		defID, uid, adminID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "target": name})
}

func scanDef(row pgx.Row) (EventDef, error) {
	var d EventDef
	var tp, cp []byte
	var desc *string
	err := row.Scan(&d.ID, &d.Name, &desc, &d.Enabled, &d.Weight, &d.CooldownSec,
		&d.TriggerType, &tp, &d.CompletionType, &cp, &d.Message, &d.ImageURL,
		&d.ImageDayURL, &d.ImageDuskURL, &d.ImageNightURL, &d.RewardExp, &d.RewardDp, &d.GoalText)
	if err != nil {
		return d, err
	}
	if desc != nil {
		d.Description = *desc
	}
	_ = json.Unmarshal(tp, &d.TriggerParams)
	_ = json.Unmarshal(cp, &d.CompletionParams)
	if d.TriggerParams == nil {
		d.TriggerParams = map[string]float64{}
	}
	if d.CompletionParams == nil {
		d.CompletionParams = map[string]float64{}
	}
	return d, nil
}

const defCols = `id, name, description, enabled, weight, cooldown_sec, trigger_type, trigger_params, completion_type, completion_params, message, image_url, image_day_url, image_dusk_url, image_night_url, reward_exp, reward_dp, goal_text`

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT `+defCols+` FROM event_task_defs ORDER BY created_at DESC`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	defs := []EventDef{}
	for rows.Next() {
		d, err := scanDef(rows)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		defs = append(defs, d)
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"defs":               defs,
		"trigger_catalog":    TriggerCatalog,
		"completion_catalog": CompletionCatalog,
	})
}

func (h *Handler) parseDef(w http.ResponseWriter, r *http.Request) (*EventDef, bool) {
	var d EventDef
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return nil, false
	}
	if d.Name == "" || !validTrigger(d.TriggerType) || !validCompletion(d.CompletionType) {
		respondErr(w, http.StatusBadRequest, "名稱必填、觸發/完成類型需有效")
		return nil, false
	}
	if d.Weight <= 0 {
		d.Weight = 100
	}
	if d.CooldownSec < 0 {
		d.CooldownSec = 0
	}
	if d.TriggerParams == nil {
		d.TriggerParams = map[string]float64{}
	}
	if d.CompletionParams == nil {
		d.CompletionParams = map[string]float64{}
	}
	return &d, true
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	d, ok := h.parseDef(w, r)
	if !ok {
		return
	}
	tp, _ := json.Marshal(d.TriggerParams)
	cp, _ := json.Marshal(d.CompletionParams)
	out, err := scanDef(h.db.QueryRow(r.Context(), `
		INSERT INTO event_task_defs (name, description, enabled, weight, cooldown_sec,
			trigger_type, trigger_params, completion_type, completion_params, message, image_url,
			image_day_url, image_dusk_url, image_night_url, reward_exp, reward_dp, goal_text)
		VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING `+defCols,
		d.Name, d.Description, d.Enabled, d.Weight, d.CooldownSec,
		d.TriggerType, tp, d.CompletionType, cp, d.Message, d.ImageURL,
		d.ImageDayURL, d.ImageDuskURL, d.ImageNightURL, d.RewardExp, d.RewardDp, d.GoalText))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "建立失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"def": out})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, ok := h.parseDef(w, r)
	if !ok {
		return
	}
	tp, _ := json.Marshal(d.TriggerParams)
	cp, _ := json.Marshal(d.CompletionParams)
	out, err := scanDef(h.db.QueryRow(r.Context(), `
		UPDATE event_task_defs SET name=$2, description=NULLIF($3,''), enabled=$4, weight=$5, cooldown_sec=$6,
			trigger_type=$7, trigger_params=$8, completion_type=$9, completion_params=$10,
			message=$11, image_url=$12, image_day_url=$13, image_dusk_url=$14, image_night_url=$15,
			reward_exp=$16, reward_dp=$17, goal_text=$18, updated_at=NOW()
		WHERE id=$1 RETURNING `+defCols,
		id, d.Name, d.Description, d.Enabled, d.Weight, d.CooldownSec,
		d.TriggerType, tp, d.CompletionType, cp, d.Message, d.ImageURL,
		d.ImageDayURL, d.ImageDuskURL, d.ImageNightURL, d.RewardExp, d.RewardDp, d.GoalText))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "更新失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"def": out})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `DELETE FROM event_task_defs WHERE id=$1`, id); err != nil {
		respondErr(w, http.StatusInternalServerError, "刪除失敗")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Runner（掛 /events，需登入）---

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/active", h.Active)
	r.Post("/occurrences", h.CreateOccurrence)
	r.Post("/occurrences/{id}/complete", h.Complete)
	r.Post("/occurrences/{id}/fail", h.Fail)
	r.Post("/manual/claim", h.ClaimManual) // 跑步中輪詢：認領後台手動觸發的事件
	return r
}

// POST /events/manual/claim — 跑步中的 /track 輪詢，認領後台手動觸發的事件（測試用）。
// 認領即建立 occurrence（測試觸發不套全域冷卻閘門）。無待認領則 armed=false。
func (h *Handler) ClaimManual(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	ctx := r.Context()
	var defID string
	err := h.db.QueryRow(ctx, `
		UPDATE event_manual_pushes SET consumed_at=NOW()
		WHERE id = (SELECT id FROM event_manual_pushes
		            WHERE target_user_id=$1 AND consumed_at IS NULL AND created_at > NOW()-INTERVAL '3 minutes'
		            ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING def_id::text`, uid).Scan(&defID)
	if errors.Is(err, pgx.ErrNoRows) {
		respondJSON(w, http.StatusOK, map[string]any{"armed": false})
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	def, err := scanDef(h.db.QueryRow(ctx, `SELECT `+defCols+` FROM event_task_defs WHERE id=$1`, defID))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	var occID string
	if err := h.db.QueryRow(ctx, `
		INSERT INTO event_task_occurrences (user_id, def_id, reward_exp, reward_dp, trigger_dist_m, trigger_elapsed_s)
		VALUES ($1,$2,$3,$4,0,0) RETURNING id`,
		uid, defID, def.RewardExp, def.RewardDp).Scan(&occID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"armed": true, "def": def, "occ_id": occID})
}

// GET /events/active — 供跑步引擎的啟用中事件定義
func (h *Handler) Active(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT `+defCols+` FROM event_task_defs WHERE enabled ORDER BY created_at`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	defs := []EventDef{}
	for rows.Next() {
		d, err := scanDef(rows)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		defs = append(defs, d)
	}
	minSec, maxSec := h.eventWaitBounds(r.Context())
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	firstWait := h.stagedFirstWait(r.Context(), uid)
	respondJSON(w, http.StatusOK, map[string]any{"defs": defs, "wait_min_sec": minSec, "wait_max_sec": maxSec, "first_event_wait_sec": firstWait})
}

type occReq struct {
	DefID           string  `json:"def_id"`
	TriggerDistM    float64 `json:"trigger_dist_m"`
	TriggerElapsedS int     `json:"trigger_elapsed_s"`
	FirstOfRun      bool    `json:"first_of_run"` // 新手加速：本趟第一個事件（前 3 趟）→ 放寬閘門，只擋「真的還有進行中任務」
}

// POST /events/occurrences — 觸發時建立實例（快照獎勵）
func (h *Handler) CreateOccurrence(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var req occReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DefID == "" {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// 全域閘門：一次一任務 + 隨機等待地板（跨 Phase A/B）。
	// 但「新手加速」的本趟第一個事件（前 3 趟）不能被上一趟的間隔地板擋掉（地板＝event_wait_min_sec，預設 300 秒，
	// 遠大於 45/90/180 的加速等待）；此時只擋「真的還有進行中任務」，仍守住一次一任務。
	blocked := false
	if req.FirstOfRun {
		var active bool
		if err := h.db.QueryRow(r.Context(), `SELECT EXISTS(
			SELECT 1 FROM event_task_occurrences WHERE user_id=$1 AND status='active'
			UNION ALL
			SELECT 1 FROM event_race_participants WHERE user_id=$1 AND status='joined')`, uid).Scan(&active); err == nil && active {
			blocked = true
		}
	} else if open, err := h.taskGateOpen(r.Context(), uid); err == nil && !open {
		blocked = true
	}
	if blocked {
		respondJSON(w, http.StatusOK, map[string]any{"blocked": true, "message": "任務冷卻中"})
		return
	}
	var id string
	var rexp, rdp int
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO event_task_occurrences (user_id, def_id, reward_exp, reward_dp, trigger_dist_m, trigger_elapsed_s)
		SELECT $1, d.id, d.reward_exp, d.reward_dp, $3, $4 FROM event_task_defs d WHERE d.id=$2 AND d.enabled
		RETURNING id, reward_exp, reward_dp`,
		uid, req.DefID, req.TriggerDistM, req.TriggerElapsedS).Scan(&id, &rexp, &rdp)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "事件不存在或已停用")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"id": id, "reward_exp": rexp, "reward_dp": rdp})
}

type completeReq struct {
	MovedM      float64 `json:"moved_m"`
	WindowS     float64 `json:"window_s"`
	MinSegM     float64 `json:"min_seg_m"`     // hold_pace：所有檢查區間中最小的移動量
	MaxSegM     float64 `json:"max_seg_m"`     // sprint：最佳爆發區間的移動量
	FirstHalfM  float64 `json:"first_half_m"`  // negative_split（舊）：前半段移動
	SecondHalfM float64 `json:"second_half_m"` // negative_split（舊）：後半段移動
	BaselineSpk float64 `json:"baseline_spk"`  // pace_shift：觸發時的平均配速（秒/公里）。Phase A 由伺服器依 occurrence 快照覆寫（權威）；Phase B 用 client 值（夾範圍）
	Taps        int     `json:"taps"`          // tap_burst：點擊次數
	HeldMs      float64 `json:"held_ms"`       // hold_press：累積按住毫秒
	SwipePx     float64      `json:"swipe_px"`  // swipe_charge：累積滑動距離
	Swipes      int          `json:"swipes"`    // dodge_swipe：滑動段數
	ShapePts    [][2]float64 `json:"shape_pts"` // draw_shape：實際筆跡點（伺服器重算辨識，防前端刷分）
	Shape       int          `json:"shape"`     // draw_shape：本次抽到要畫的圖形（3/4/5）
}

// --- 互動型完成（觸控小遊戲）：依完成度分級發獎 ---

func isInteraction(ct string) bool {
	return ct == "tap_burst" || ct == "hold_press" || ct == "swipe_charge" || ct == "dodge_swipe" || ct == "draw_shape"
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// interactionDegree 回傳 0..1 完成度。防弊上限一律用「伺服器權威視窗」params["limit_s"]，
// 不用 client 可控的 window_s（否則送 window_s<=0 可繞過上限刷滿）。
func interactionDegree(ctype string, params map[string]float64, ev completeReq) float64 {
	lim := params["limit_s"]
	if lim <= 0 {
		return 0
	}
	switch ctype {
	case "tap_burst":
		tgt := params["target_taps"]
		if tgt <= 0 {
			return 0
		}
		if float64(ev.Taps) > lim*15+5 { // 每秒最多約 15 下
			return 0
		}
		return clamp01(float64(ev.Taps) / tgt)
	case "hold_press":
		need := params["hold_s"] * 1000
		if need <= 0 {
			return 0
		}
		if ev.HeldMs < 0 || ev.HeldMs > (lim+2)*1000 { // 按住不可超過視窗
			return 0
		}
		return clamp01(ev.HeldMs / need)
	case "swipe_charge":
		tgt := params["target_px"]
		if tgt <= 0 {
			return 0
		}
		if ev.SwipePx < 0 || ev.SwipePx > lim*8000+500 { // 每秒最多約 8000px
			return 0
		}
		return clamp01(ev.SwipePx / tgt)
	case "dodge_swipe":
		tgt := params["target_swipes"]
		if tgt <= 0 {
			return 0
		}
		if float64(ev.Swipes) > lim*8+3 { // 每秒最多約 8 段
			return 0
		}
		return clamp01(float64(ev.Swipes) / tgt)
	case "draw_shape":
		// 伺服器用實際筆跡重算辨識（品質距離 + margin）。本次圖形由前端依權重抽出並回報。
		s := ev.Shape
		if s != 3 && s != 4 && s != 5 {
			return 0
		}
		if params["w3"]+params["w4"]+params["w5"] > 0 && params[fmt.Sprintf("w%d", s)] <= 0 {
			return 0 // 有設權重時，未啟用的圖形不給分
		}
		return shapeDegree(ev.ShapePts, s)
	}
	return 0
}

func starsFor(d float64) int {
	switch {
	case d >= 0.9:
		return 3
	case d >= 0.6:
		return 2
	case d >= 0.3:
		return 1
	}
	return 0
}
func rewardFactor(stars int) float64 {
	switch stars {
	case 3:
		return 1.0
	case 2:
		return 0.6
	case 1:
		return 0.3
	}
	return 0
}
func roundReward(full int, f float64) int { return int(float64(full)*f + 0.5) }

// gradeInteraction 依完成度給基礎獎勵（分級）+ 完美(3★/首次滿分)額外 bonus（讀 completion_params）
func gradeInteraction(ctype string, params map[string]float64, ev completeReq, rexp, rdp int) (giveExp, giveDp, stars, bonusExp, bonusDp int) {
	stars = starsFor(interactionDegree(ctype, params, ev))
	f := rewardFactor(stars)
	if ctype == "draw_shape" {
		// 基礎（共用）+ 該圖形加碼，依畫得準的星等分級
		s := ev.Shape
		baseExp := rexp + int(params[fmt.Sprintf("x%d_exp", s)])
		baseDp := rdp + int(params[fmt.Sprintf("x%d_dp", s)])
		giveExp, giveDp = roundReward(baseExp, f), roundReward(baseDp, f)
		return
	}
	giveExp, giveDp = roundReward(rexp, f), roundReward(rdp, f)
	if stars == 3 {
		bonusExp, bonusDp = int(params["bonus_exp"]), int(params["bonus_dp"])
		giveExp += bonusExp
		giveDp += bonusDp
	}
	return
}

// validateCompletion 依完成模組驗證 evidence（含瞬移防弊）
func validateCompletion(ctype string, params map[string]float64, ev completeReq) bool {
	if ev.MovedM < 0 || ev.WindowS <= 0 || ev.MovedM > maxSpeedMS*ev.WindowS*1.2 {
		return false
	}
	switch ctype {
	case "move_more":
		return ev.WindowS <= params["limit_s"]+2 && ev.MovedM >= params["target_m"]
	case "move_less":
		return ev.WindowS >= params["limit_s"]-2 && ev.MovedM <= params["max_m"]
	case "hold_pace":
		// 全程維持移動：撐滿時間 + 最小區間移動達標（區間移動上限做瞬移防弊）
		return ev.WindowS >= params["limit_s"]-2 && ev.MinSegM >= params["min_m"] &&
			ev.MinSegM <= maxSpeedMS*(params["check_s"]+1)*1.2
	case "sprint":
		// 短時間爆發：最佳爆發區間達標（上限做瞬移防弊）
		return ev.WindowS <= params["limit_s"]+3 && ev.MaxSegM >= params["burst_m"] &&
			ev.MaxSegM <= maxSpeedMS*(params["burst_s"]+1)*1.2
	case "negative_split": // 舊型（型錄已移除，保留驗證給既有 def）
		return ev.WindowS <= params["limit_s"]+2 && ev.FirstHalfM > 5 &&
			ev.SecondHalfM >= ev.FirstHalfM*(params["ratio_pct"]/100)
	case "pace_shift":
		// 變速跑：整段維持「比平均配速快/慢 delta 秒/公里」。撐滿時間 + 視窗均速達標 + 分段防瞬移。
		lim := params["limit_s"]
		if lim <= 0 || ev.WindowS < lim-2 || ev.MovedM <= 0 || ev.BaselineSpk <= 0 {
			return false
		}
		// 分段防瞬移：任一 5 秒區間位移不得超過人類極限（擋單點 GPS 跳動/竄改把「站著不動」算成快跑）
		if ev.MaxSegM > maxSpeedMS*6*1.2 {
			return false
		}
		delta := params["delta_spk"]
		if delta < 0 {
			delta = -delta // 防呆：距離差一律取絕對值，避免負值把「加速」變成可低於平均也算過
		}
		winPace := ev.WindowS / (ev.MovedM / 1000) // 本段均速（秒/公里）
		if params["faster"] >= 0.5 {
			target := ev.BaselineSpk - delta // 需更快 → 秒/公里更小
			return target > 0 && winPace <= target
		}
		// 減速：需更慢（秒/公里更大），但仍須持續移動（不可完全停下、均速 ≥ 0.5 m/s）
		return winPace >= ev.BaselineSpk+delta && ev.MovedM >= ev.WindowS*0.5
	}
	return false
}

// clampBaselineSpk 把平均配速夾在合理範圍 [180, 1200] 秒/公里（3:00–20:00/km），濾除 GPS 噪音極值。
func clampBaselineSpk(x float64) float64 {
	if x < 180 {
		return 180
	}
	if x > 1200 {
		return 1200
	}
	return x
}

// POST /events/occurrences/{id}/complete — 驗證完成 + 發獎（冪等）
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	id := chi.URLParam(r, "id")
	var req completeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	var status, ctype string
	var awarded bool
	var rexp, rdp int
	var cpRaw []byte
	var trigDist float64
	var trigElapsed int
	err := h.db.QueryRow(r.Context(), `
		SELECT o.status, o.awarded, o.reward_exp, o.reward_dp, d.completion_type, d.completion_params, o.trigger_dist_m, o.trigger_elapsed_s
		FROM event_task_occurrences o JOIN event_task_defs d ON d.id=o.def_id
		WHERE o.id=$1 AND o.user_id=$2`, id, uid).Scan(&status, &awarded, &rexp, &rdp, &ctype, &cpRaw, &trigDist, &trigElapsed)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "找不到此事件")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if awarded { // 冪等：已發過就回既有獎勵
		respondJSON(w, http.StatusOK, map[string]any{"completed": true, "reward_exp": rexp, "reward_dp": rdp})
		return
	}
	if status != "active" {
		respondErr(w, http.StatusBadRequest, "此事件已結束")
		return
	}
	var params map[string]float64
	_ = json.Unmarshal(cpRaw, &params)

	// pace_shift：平均配速基準以伺服器儲存的觸發快照為準（權威，覆寫 client 送的值防弊）
	if ctype == "pace_shift" {
		if trigDist > 0 && trigElapsed > 0 {
			req.BaselineSpk = clampBaselineSpk(float64(trigElapsed) / (trigDist / 1000))
		} else {
			req.BaselineSpk = 0 // 無有效觸發快照（距離或時間為 0）→ 無法判定，視為未達成（與前端一致）
		}
	}

	// 互動型：依完成度分級發獎（可能 0★）+ 完美 bonus；其餘：pass/fail 全額
	var giveExp, giveDp, stars, bonusExp, bonusDp int
	if isInteraction(ctype) {
		giveExp, giveDp, stars, bonusExp, bonusDp = gradeInteraction(ctype, params, req, rexp, rdp)
	} else {
		if !validateCompletion(ctype, params, req) {
			respondJSON(w, http.StatusOK, map[string]any{"completed": false, "message": "尚未達成完成條件"})
			return
		}
		giveExp, giveDp, stars = rexp, rdp, 3
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `
		UPDATE event_task_occurrences SET status='completed', completed_at=NOW(), awarded=TRUE, reward_exp=$2, reward_dp=$3
		WHERE id=$1 AND NOT awarded`, id, giveExp, giveDp)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if tag.RowsAffected() == 1 && (giveExp > 0 || giveDp > 0) {
		if _, err := tx.Exec(r.Context(), `UPDATE users SET exp=exp+$1, dp=dp+$2 WHERE id=$3`, giveExp, giveDp, uid); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"completed": true, "reward_exp": giveExp, "reward_dp": giveDp, "stars": stars, "bonus_exp": bonusExp, "bonus_dp": bonusDp})
}

// POST /events/occurrences/{id}/fail — 逾時/放棄
func (h *Handler) Fail(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	id := chi.URLParam(r, "id")
	_, _ = h.db.Exec(r.Context(),
		`UPDATE event_task_occurrences SET status='failed' WHERE id=$1 AND user_id=$2 AND status='active'`, id, uid)
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
