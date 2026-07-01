// Package event 事件任務（Phase A：日常隨機事件）。
// 觸發/完成為模組化的 type + 參數；獎勵直接加 users.exp/dp（非賽事，不走 exp_ledger），
// 以 occurrence 列作紀錄與冪等守門。
package event

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

// TriggerCatalog 觸發條件模組（切片先做兩種，之後可擴充 pace 類）
var TriggerCatalog = []TypeSpec{
	{"distance_below", "近期幾乎沒移動（原地）", []ParamSpec{
		{"window_s", "觀察時間", "秒"},
		{"max_move_m", "此時間內移動小於", "公尺"},
	}},
	{"distance_above", "近期移動很多", []ParamSpec{
		{"window_s", "觀察時間", "秒"},
		{"min_move_m", "此時間內移動大於", "公尺"},
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

// taskGateOpen 全域任務閘門：一個跑者同時只有一個進行中任務、任務間至少 15 分鐘冷卻。
// 只要近 15 分鐘內有任何 Phase A 觸發或 Phase B 加入紀錄 → 閘門關閉（不再觸發/不可加入）。
func (h *Handler) taskGateOpen(ctx context.Context, uid string) (bool, error) {
	var recent bool
	err := h.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM event_task_occurrences WHERE user_id=$1 AND triggered_at > NOW() - INTERVAL '15 minutes'
			UNION ALL
			SELECT 1 FROM event_race_participants WHERE user_id=$1 AND joined_at > NOW() - INTERVAL '15 minutes'
		)`, uid).Scan(&recent)
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
	return r
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
		&d.ImageDayURL, &d.ImageDuskURL, &d.ImageNightURL, &d.RewardExp, &d.RewardDp)
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

const defCols = `id, name, description, enabled, weight, cooldown_sec, trigger_type, trigger_params, completion_type, completion_params, message, image_url, image_day_url, image_dusk_url, image_night_url, reward_exp, reward_dp`

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
			image_day_url, image_dusk_url, image_night_url, reward_exp, reward_dp)
		VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING `+defCols,
		d.Name, d.Description, d.Enabled, d.Weight, d.CooldownSec,
		d.TriggerType, tp, d.CompletionType, cp, d.Message, d.ImageURL,
		d.ImageDayURL, d.ImageDuskURL, d.ImageNightURL, d.RewardExp, d.RewardDp))
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
			reward_exp=$16, reward_dp=$17, updated_at=NOW()
		WHERE id=$1 RETURNING `+defCols,
		id, d.Name, d.Description, d.Enabled, d.Weight, d.CooldownSec,
		d.TriggerType, tp, d.CompletionType, cp, d.Message, d.ImageURL,
		d.ImageDayURL, d.ImageDuskURL, d.ImageNightURL, d.RewardExp, d.RewardDp))
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
	respondJSON(w, http.StatusOK, map[string]any{"defs": defs})
}

type occReq struct {
	DefID           string  `json:"def_id"`
	TriggerDistM    float64 `json:"trigger_dist_m"`
	TriggerElapsedS int     `json:"trigger_elapsed_s"`
}

// POST /events/occurrences — 觸發時建立實例（快照獎勵）
func (h *Handler) CreateOccurrence(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var req occReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DefID == "" {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// 全域閘門：一次一任務 + 15 分冷卻（跨 Phase A/B）
	if open, err := h.taskGateOpen(r.Context(), uid); err == nil && !open {
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
	MovedM  float64 `json:"moved_m"`
	WindowS float64 `json:"window_s"`
}

// validateCompletion 依完成模組驗證 evidence（含瞬移防弊）
func validateCompletion(ctype string, params map[string]float64, movedM, windowS float64) bool {
	if movedM < 0 || windowS <= 0 || movedM > maxSpeedMS*windowS*1.2 {
		return false
	}
	switch ctype {
	case "move_more":
		return windowS <= params["limit_s"]+2 && movedM >= params["target_m"]
	case "move_less":
		return windowS >= params["limit_s"]-2 && movedM <= params["max_m"]
	}
	return false
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
	err := h.db.QueryRow(r.Context(), `
		SELECT o.status, o.awarded, o.reward_exp, o.reward_dp, d.completion_type, d.completion_params
		FROM event_task_occurrences o JOIN event_task_defs d ON d.id=o.def_id
		WHERE o.id=$1 AND o.user_id=$2`, id, uid).Scan(&status, &awarded, &rexp, &rdp, &ctype, &cpRaw)
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
	if !validateCompletion(ctype, params, req.MovedM, req.WindowS) {
		respondJSON(w, http.StatusOK, map[string]any{"completed": false, "message": "尚未達成完成條件"})
		return
	}

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `
		UPDATE event_task_occurrences SET status='completed', completed_at=NOW(), awarded=TRUE
		WHERE id=$1 AND NOT awarded`, id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if tag.RowsAffected() == 1 && (rexp > 0 || rdp > 0) {
		if _, err := tx.Exec(r.Context(), `UPDATE users SET exp=exp+$1, dp=dp+$2 WHERE id=$3`, rexp, rdp, uid); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"completed": true, "reward_exp": rexp, "reward_dp": rdp})
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
