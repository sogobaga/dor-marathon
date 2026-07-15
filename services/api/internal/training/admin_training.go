// 後台自主訓練管理 CRUD（workout_templates 課表庫 + pace_levels 配速等級表）。
// 比照 profile/admin_titles.go 的權限/驗證模式；掛在 /admin/training（需 training 權限）。
// 前台 GET /training/templates 只回 enabled AND library_visible 的課表；本檔給後台看「全部」（含
// library_visible=false 的距離變體，那些只給 P3 產生器排課用）。
package training

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AdminWorkoutTemplate 後台課表庫單筆；segments 直接回原始 jsonb（形狀見 package doc）。
type AdminWorkoutTemplate struct {
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	Category       string          `json:"category"`
	Description    string          `json:"description"`
	Segments       json.RawMessage `json:"segments"`
	SortOrder      int             `json:"sort_order"`
	Enabled        bool            `json:"enabled"`
	LibraryVisible bool            `json:"library_visible"`
	AdjustType     string          `json:"adjust_type"`
}

// AdminPaceLevel 後台配速等級表單筆；paces 直接回原始 jsonb。
type AdminPaceLevel struct {
	ID      int             `json:"id"`
	Label   string          `json:"label"`
	Paces   json.RawMessage `json:"paces"`
	Enabled bool            `json:"enabled"`
}

// validAdjustTypes workout_templates.adjust_type 合法值（migration 085）。
var validAdjustTypes = map[string]bool{"distance": true, "reps": true, "pyramid": true, "none": true}

// validJSONArray 檢查 raw 是否為合法 JSON「陣列」（segments 的形狀要求）。
func validJSONArray(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var arr []json.RawMessage
	return json.Unmarshal(raw, &arr) == nil
}

// validJSON 檢查 raw 是否為合法 JSON（paces 的形狀要求，不限型別）。
func validJSON(raw json.RawMessage) bool {
	return len(raw) > 0 && json.Valid(raw)
}

// AdminRouter 掛載在 /admin/training（需 training 權限）。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/data", h.AdminTrainingData)
	r.Post("/templates", h.AdminCreateTemplate)
	r.Put("/templates/{code}", h.AdminUpdateTemplate)
	r.Delete("/templates/{code}", h.AdminDeleteTemplate)
	r.Post("/pace-levels", h.AdminCreatePaceLevel)
	r.Put("/pace-levels/{id}", h.AdminUpdatePaceLevel)
	r.Delete("/pace-levels/{id}", h.AdminDeletePaceLevel)
	return r
}

// AdminTrainingData GET /admin/training/data — 回所有課表(含 library_visible=false) + 所有配速等級表。
func (h *Handler) AdminTrainingData(w http.ResponseWriter, r *http.Request) {
	tRows, err := h.db.Query(r.Context(), `
		SELECT code, name, category, description, segments, sort_order, enabled, library_visible, adjust_type
		FROM workout_templates ORDER BY sort_order`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	templates := []AdminWorkoutTemplate{}
	for tRows.Next() {
		var t AdminWorkoutTemplate
		if err := tRows.Scan(&t.Code, &t.Name, &t.Category, &t.Description, &t.Segments, &t.SortOrder, &t.Enabled, &t.LibraryVisible, &t.AdjustType); err != nil {
			tRows.Close()
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		templates = append(templates, t)
	}
	tRows.Close()
	if err := tRows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "scan failed")
		return
	}

	pRows, err := h.db.Query(r.Context(), `SELECT id, label, paces, enabled FROM pace_levels ORDER BY id`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	paceLevels := []AdminPaceLevel{}
	for pRows.Next() {
		var p AdminPaceLevel
		if err := pRows.Scan(&p.ID, &p.Label, &p.Paces, &p.Enabled); err != nil {
			pRows.Close()
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		paceLevels = append(paceLevels, p)
	}
	pRows.Close()
	if err := pRows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "scan failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"templates": templates, "pace_levels": paceLevels})
}

// templateAdminReq 建立/更新課表的請求體。
type templateAdminReq struct {
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	Category       string          `json:"category"`
	Description    string          `json:"description"`
	Segments       json.RawMessage `json:"segments"`
	SortOrder      int             `json:"sort_order"`
	Enabled        bool            `json:"enabled"`
	LibraryVisible bool            `json:"library_visible"`
	AdjustType     string          `json:"adjust_type"`
}

// validate 共用建立/更新的欄位檢查；requireCode 僅在建立時檢查（更新時 code 取自 URL、不可改）。
// 呼叫端須先把空字串 AdjustType 補成 "none"（預設值）再呼叫本函式。
func (req templateAdminReq) validate(requireCode bool) string {
	if requireCode && req.Code == "" {
		return "code 必填"
	}
	if strings.TrimSpace(req.Name) == "" {
		return "name 必填"
	}
	if strings.TrimSpace(req.Category) == "" {
		return "category 必填"
	}
	if !validJSONArray(req.Segments) {
		return "segments 須為合法 JSON 陣列"
	}
	if !validAdjustTypes[req.AdjustType] {
		return "adjust_type 須為 distance/reps/pyramid/none 其中之一"
	}
	return ""
}

// AdminCreateTemplate POST /admin/training/templates — 新增課表。
func (h *Handler) AdminCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req templateAdminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.AdjustType == "" {
		req.AdjustType = "none"
	}
	if msg := req.validate(true); msg != "" {
		respondErr(w, http.StatusBadRequest, msg)
		return
	}
	_, err := h.db.Exec(r.Context(), `
		INSERT INTO workout_templates (code, name, category, description, segments, sort_order, enabled, library_visible, adjust_type)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		req.Code, req.Name, req.Category, req.Description, req.Segments, req.SortOrder, req.Enabled, req.LibraryVisible, req.AdjustType)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			respondErr(w, http.StatusConflict, "此 code 已存在")
			return
		}
		respondErr(w, http.StatusInternalServerError, "建立失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"template": AdminWorkoutTemplate{
		Code: req.Code, Name: req.Name, Category: req.Category, Description: req.Description,
		Segments: req.Segments, SortOrder: req.SortOrder, Enabled: req.Enabled,
		LibraryVisible: req.LibraryVisible, AdjustType: req.AdjustType,
	}})
}

// AdminUpdateTemplate PUT /admin/training/templates/{code} — 更新；code 為 PK（不可改）。
func (h *Handler) AdminUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var req templateAdminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AdjustType == "" {
		req.AdjustType = "none"
	}
	if msg := req.validate(false); msg != "" {
		respondErr(w, http.StatusBadRequest, msg)
		return
	}
	ct, err := h.db.Exec(r.Context(), `
		UPDATE workout_templates
		SET name=$1, category=$2, description=$3, segments=$4, sort_order=$5, enabled=$6, library_visible=$7, adjust_type=$8
		WHERE code=$9`,
		req.Name, req.Category, req.Description, req.Segments, req.SortOrder, req.Enabled, req.LibraryVisible, req.AdjustType, code)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "更新失敗")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "template not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"template": AdminWorkoutTemplate{
		Code: code, Name: req.Name, Category: req.Category, Description: req.Description,
		Segments: req.Segments, SortOrder: req.SortOrder, Enabled: req.Enabled,
		LibraryVisible: req.LibraryVisible, AdjustType: req.AdjustType,
	}})
}

// AdminDeleteTemplate DELETE /admin/training/templates/{code} — 刪除。
// user_training_schedule.template_code 為文字參照、無 FK，刪除會使既有排程無法解析（前端 resolveTemplate
// 找不到 code），屬 admin 操作責任，本端不做額外擋下或清理。
func (h *Handler) AdminDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	ct, err := h.db.Exec(r.Context(), `DELETE FROM workout_templates WHERE code=$1`, code)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "刪除失敗")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "template not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// paceLevelAdminReq 建立/更新配速等級表的請求體。
type paceLevelAdminReq struct {
	ID      int             `json:"id"`
	Label   string          `json:"label"`
	Paces   json.RawMessage `json:"paces"`
	Enabled bool            `json:"enabled"`
}

// validate requireID 僅在建立時檢查（更新時 id 取自 URL、不可改）。
func (req paceLevelAdminReq) validate(requireID bool) string {
	if requireID && req.ID <= 0 {
		return "id 須為正整數"
	}
	if strings.TrimSpace(req.Label) == "" {
		return "label 必填"
	}
	if !validJSON(req.Paces) {
		return "paces 須為合法 JSON"
	}
	return ""
}

// AdminCreatePaceLevel POST /admin/training/pace-levels — 新增配速等級表。
func (h *Handler) AdminCreatePaceLevel(w http.ResponseWriter, r *http.Request) {
	var req paceLevelAdminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := req.validate(true); msg != "" {
		respondErr(w, http.StatusBadRequest, msg)
		return
	}
	_, err := h.db.Exec(r.Context(), `INSERT INTO pace_levels (id, label, paces, enabled) VALUES ($1,$2,$3,$4)`,
		req.ID, req.Label, req.Paces, req.Enabled)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			respondErr(w, http.StatusConflict, "此 id 已存在")
			return
		}
		respondErr(w, http.StatusInternalServerError, "建立失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"pace_level": AdminPaceLevel{
		ID: req.ID, Label: req.Label, Paces: req.Paces, Enabled: req.Enabled,
	}})
}

// AdminUpdatePaceLevel PUT /admin/training/pace-levels/{id} — 更新；id 為 PK（不可改，URL 決定）。
func (h *Handler) AdminUpdatePaceLevel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		respondErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req paceLevelAdminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := req.validate(false); msg != "" {
		respondErr(w, http.StatusBadRequest, msg)
		return
	}
	ct, err := h.db.Exec(r.Context(), `UPDATE pace_levels SET label=$1, paces=$2, enabled=$3 WHERE id=$4`,
		req.Label, req.Paces, req.Enabled, id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "更新失敗")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "pace level not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"pace_level": AdminPaceLevel{
		ID: id, Label: req.Label, Paces: req.Paces, Enabled: req.Enabled,
	}})
}

// AdminDeletePaceLevel DELETE /admin/training/pace-levels/{id} — 刪除。
func (h *Handler) AdminDeletePaceLevel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		respondErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.db.Exec(r.Context(), `DELETE FROM pace_levels WHERE id=$1`, id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "刪除失敗")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "pace level not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
