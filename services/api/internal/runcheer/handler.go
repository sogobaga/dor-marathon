// Package runcheer 跑步鼓勵語（run_cheer_messages）：GPS 跑步頁每跨一整公里彈出一句。
// 兩池：phase='before'（完成目標 50% 前，累積式文案，含佔位符 {done}）、
// phase='after'（超過 50% 後，剩餘式文案，含 {remain}）。佔位符為前台字串取代，後端不解析、不驗證內容格式。
// 仿 internal/profile/admin_titles.go：handler 直接用 *pgxpool.Pool，無獨立 repository 層。
package runcheer

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uuidRE 驗證路徑帶入的 id 是否為合法 UUID 格式，避免把非 UUID 字串丟給 Postgres 的 uuid 欄位比較
// （不合法直接回 400，而不是讓 pgx 丟型別轉換錯誤變成 500）。
var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool { return uuidRE.MatchString(s) }

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

// Message 後台管理單筆（對應 apps/web RunCheerMessage）。
type Message struct {
	ID        string `json:"id"`
	Phase     string `json:"phase"`
	Text      string `json:"text"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at"`
}

// messageReq 建立/更新共用輸入（對應 apps/web RunCheerInput）。
type messageReq struct {
	Phase     string `json:"phase"`
	Text      string `json:"text"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sort_order"`
}

func validPhase(p string) bool { return p == "before" || p == "after" }

// validate 建立/更新共用欄位檢查：phase 僅限 before/after；text 以 rune 計長度（中文字），
// trim 後需 1-120 字元；sort_order 不可為負。回傳非空字串代表驗證失敗訊息。
func (req messageReq) validate() string {
	if !validPhase(req.Phase) {
		return "phase 必須是 before 或 after"
	}
	n := len([]rune(strings.TrimSpace(req.Text)))
	if n < 1 || n > 120 {
		return "text 需為 1-120 字元"
	}
	if req.SortOrder < 0 {
		return "sort_order 不可為負數"
	}
	return ""
}

// AdminRouter 掛 /admin/run-cheers（需 run_cheers 權限）。
func (h *Handler) AdminRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.AdminList)
	r.Post("/", h.AdminCreate)
	r.Put("/{id}", h.AdminUpdate)
	r.Delete("/{id}", h.AdminDelete)
	return r
}

// GET /admin/run-cheers — 列全部（含 disabled），依 phase, sort_order, created_at 排序。
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT id, phase, text, enabled, sort_order, created_at
		FROM run_cheer_messages
		ORDER BY phase, sort_order, created_at`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var createdAt time.Time
		if err := rows.Scan(&m.ID, &m.Phase, &m.Text, &m.Enabled, &m.SortOrder, &createdAt); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		m.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "scan failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": out})
}

// POST /admin/run-cheers — 新增。
func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	var req messageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if msg := req.validate(); msg != "" {
		respondErr(w, http.StatusBadRequest, msg)
		return
	}
	var m Message
	var createdAt time.Time
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO run_cheer_messages (phase, text, enabled, sort_order)
		VALUES ($1,$2,$3,$4)
		RETURNING id, phase, text, enabled, sort_order, created_at`,
		req.Phase, req.Text, req.Enabled, req.SortOrder,
	).Scan(&m.ID, &m.Phase, &m.Text, &m.Enabled, &m.SortOrder, &createdAt)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "建立失敗")
		return
	}
	m.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	respondJSON(w, http.StatusOK, map[string]any{"item": m})
}

// PUT /admin/run-cheers/{id} — 全欄位更新（phase/text/enabled/sort_order），updated_at=now()。
func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	var req messageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if msg := req.validate(); msg != "" {
		respondErr(w, http.StatusBadRequest, msg)
		return
	}
	var m Message
	var createdAt time.Time
	err := h.db.QueryRow(r.Context(), `
		UPDATE run_cheer_messages
		SET phase=$1, text=$2, enabled=$3, sort_order=$4, updated_at=now()
		WHERE id=$5
		RETURNING id, phase, text, enabled, sort_order, created_at`,
		req.Phase, req.Text, req.Enabled, req.SortOrder, id,
	).Scan(&m.ID, &m.Phase, &m.Text, &m.Enabled, &m.SortOrder, &createdAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondErr(w, http.StatusNotFound, "not found")
			return
		}
		respondErr(w, http.StatusInternalServerError, "更新失敗")
		return
	}
	m.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	respondJSON(w, http.StatusOK, map[string]any{"item": m})
}

// DELETE /admin/run-cheers/{id}。
func (h *Handler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusBadRequest, "id is invalid")
		return
	}
	ct, err := h.db.Exec(r.Context(), `DELETE FROM run_cheer_messages WHERE id=$1`, id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "刪除失敗")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// Public GET /run-cheers — 免登入，只回 enabled=true 的文案，依 sort_order, created_at 排序。
// 5 分鐘 CDN/瀏覽器快取：文案異動頻率低，容忍短暫延遲生效。
func (h *Handler) Public(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT phase, text
		FROM run_cheer_messages
		WHERE enabled = TRUE
		ORDER BY sort_order, created_at`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	before := []string{}
	after := []string{}
	for rows.Next() {
		var phase, text string
		if err := rows.Scan(&phase, &text); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		if phase == "before" {
			before = append(before, text)
		} else {
			after = append(after, text)
		}
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "scan failed")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	respondJSON(w, http.StatusOK, map[string]any{"before": before, "after": after})
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]any{"error": msg})
}
