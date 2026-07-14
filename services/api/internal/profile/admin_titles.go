// 後台稱號管理 CRUD（title_defs）。管理對象與 checkAndAwardTitles / GET /profile/titles 圖鑑共用同一張表——
// category 僅限 9 個已知類別（titleCategoryLabels，見 titles.go），未知類別不會被 checkAndAwardTitles 計算，稱號永遠解不開。
package profile

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AdminTitleRow 後台稱號單筆（含已取得人數，供刪除/停用前評估影響範圍）。
type AdminTitleRow struct {
	Code        string  `json:"code"`
	Category    string  `json:"category"`
	Threshold   float64 `json:"threshold"`
	Unit        string  `json:"unit"`
	Name        string  `json:"name"`
	Tier        int     `json:"tier"`
	SortOrder   int     `json:"sort_order"`
	Enabled     bool    `json:"enabled"`
	EarnedCount int     `json:"earned_count"`
}

// TitleAdminRouter 掛載在 /admin/titles（需 titles 權限）。
func (h *Handler) TitleAdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListTitles)
	r.Post("/", h.AdminCreateTitle)
	r.Put("/{code}", h.AdminUpdateTitle)
	r.Delete("/{code}", h.AdminDeleteTitle)
	return r
}

// validTitleCategory 僅允許 checkAndAwardTitles 認得的 9 個類別（titleCategoryLabels）。
func validTitleCategory(c string) bool {
	for _, l := range titleCategoryLabels {
		if l.Key == c {
			return true
		}
	}
	return false
}

// GET /admin/titles — 列所有稱號定義（含已取得人數）+ 9 個已知類別 meta（供前端下拉）。
func (h *Handler) AdminListTitles(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT td.code, td.category, td.threshold, td.unit, td.name, td.tier, td.sort_order, td.enabled,
		       COUNT(ut.user_id)
		FROM title_defs td
		LEFT JOIN user_titles ut ON ut.title_code = td.code
		GROUP BY td.code, td.category, td.threshold, td.unit, td.name, td.tier, td.sort_order, td.enabled
		ORDER BY td.sort_order`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []AdminTitleRow{}
	for rows.Next() {
		var t AdminTitleRow
		if err := rows.Scan(&t.Code, &t.Category, &t.Threshold, &t.Unit, &t.Name, &t.Tier, &t.SortOrder, &t.Enabled, &t.EarnedCount); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "scan failed")
		return
	}
	cats := make([]map[string]string, 0, len(titleCategoryLabels))
	for _, c := range titleCategoryLabels {
		cats = append(cats, map[string]string{"key": c.Key, "label": c.Label})
	}
	respondJSON(w, http.StatusOK, map[string]any{"titles": out, "categories": cats})
}

type titleAdminReq struct {
	Code      string  `json:"code"`
	Category  string  `json:"category"`
	Threshold float64 `json:"threshold"`
	Unit      string  `json:"unit"`
	Name      string  `json:"name"`
	Tier      int     `json:"tier"`
	SortOrder int     `json:"sort_order"`
	Enabled   bool    `json:"enabled"`
}

// validate 共用建立/更新的欄位檢查；requireCode 僅在建立時檢查（更新時 code 取自 URL、不可改）。
func (req titleAdminReq) validate(requireCode bool) string {
	if requireCode && req.Code == "" {
		return "code 必填"
	}
	if !validTitleCategory(req.Category) {
		return "未知類別不會自動發放，請從固定 9 類別中選擇"
	}
	if req.Threshold <= 0 {
		return "threshold 必須大於 0"
	}
	if req.Tier < 1 || req.Tier > 6 {
		return "tier 須介於 1-6"
	}
	if strings.TrimSpace(req.Name) == "" {
		return "name 必填"
	}
	return ""
}

// POST /admin/titles — 新增。
func (h *Handler) AdminCreateTitle(w http.ResponseWriter, r *http.Request) {
	var req titleAdminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if msg := req.validate(true); msg != "" {
		respondErr(w, http.StatusBadRequest, msg)
		return
	}
	_, err := h.db.Exec(r.Context(), `
		INSERT INTO title_defs (code, category, threshold, unit, name, tier, sort_order, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		req.Code, req.Category, req.Threshold, req.Unit, req.Name, req.Tier, req.SortOrder, req.Enabled)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			respondErr(w, http.StatusConflict, "此 code 已存在")
			return
		}
		respondErr(w, http.StatusInternalServerError, "建立失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"title": AdminTitleRow{
		Code: req.Code, Category: req.Category, Threshold: req.Threshold, Unit: req.Unit,
		Name: req.Name, Tier: req.Tier, SortOrder: req.SortOrder, Enabled: req.Enabled,
	}})
}

// PUT /admin/titles/{code} — 更新 name/category/threshold/unit/tier/sort_order/enabled。
// code 為 PK 且 user_titles FK 參照，不可改。
func (h *Handler) AdminUpdateTitle(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var req titleAdminReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := req.validate(false); msg != "" {
		respondErr(w, http.StatusBadRequest, msg)
		return
	}
	ct, err := h.db.Exec(r.Context(), `
		UPDATE title_defs SET category=$1, threshold=$2, unit=$3, name=$4, tier=$5, sort_order=$6, enabled=$7
		WHERE code=$8`,
		req.Category, req.Threshold, req.Unit, req.Name, req.Tier, req.SortOrder, req.Enabled, code)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "更新失敗")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "title not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"title": AdminTitleRow{
		Code: code, Category: req.Category, Threshold: req.Threshold, Unit: req.Unit,
		Name: req.Name, Tier: req.Tier, SortOrder: req.SortOrder, Enabled: req.Enabled,
	}})
}

// DELETE /admin/titles/{code} — user_titles.title_code FK ON DELETE CASCADE 會連帶移除所有玩家的此稱號；
// 先清空指向此 code 的 users.displayed_title，避免刪除後留下懸空引用。回 revoked_from 供前端提示影響人數。
func (h *Handler) AdminDeleteTitle(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var revoked int
	if err := h.db.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM user_titles WHERE title_code=$1`, code).Scan(&revoked); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`UPDATE users SET displayed_title='' WHERE displayed_title=$1`, code); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	ct, err := h.db.Exec(r.Context(), `DELETE FROM title_defs WHERE code=$1`, code)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "刪除失敗")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "title not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true, "revoked_from": revoked})
}
