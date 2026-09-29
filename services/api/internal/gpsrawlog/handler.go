// handler.go：GPS 原始定位點記錄 HTTP 端點——使用者上傳（POST /me/gps-runs/{runId}/raw-points）
// + 後台查詢（GET /admin/gps-runs/{runId}/raw-points，外層由 main.go 套 adminAcctHandler.RequireSuper，
// 見契約 B「後端」段：「無 super_admin 旁路（超管要看資料走後台端點...）」——那句話講的是「寫入」
// 不給超管旁路，讀取本來就該限超管，兩者不衝突）。
package gpsrawlog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func respondErr(w http.ResponseWriter, code int, msg string) {
	respondJSON(w, code, map[string]any{"error": msg})
}

// Router 掛 /me/gps-runs（外層由 main.go 套 middleware.RequireAuth，比照 /me/gps-calib 前例）。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/{runId}/raw-points", h.UploadRawPoints)
	return r
}

// maxUploadBodyBytes：POST body 上限（見契約 B「後端」段：「body 上限 3 MB」）。用
// http.MaxBytesReader 在解碼前就擋，避免一個超大 body 先被整包讀進記憶體解碼才發現超限。
const maxUploadBodyBytes = 3 * 1024 * 1024

// UploadRawPoints POST /api/v1/me/gps-runs/{runId}/raw-points（見契約 B「後端」段）。前端是
// fire-and-forget 呼叫（失敗不重試、不顯示錯誤給使用者），這裡仍照一般 REST 慣例回正確的狀態碼，
// 供除錯/監控用，不代表前端會處理這些狀態碼。
func (h *Handler) UploadRawPoints(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	runID := chi.URLParam(r, "runId")

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBodyBytes)
	var p UploadPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			respondErr(w, http.StatusRequestEntityTooLarge, "body too large")
			return
		}
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := p.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}

	email, err := lookupEmail(r.Context(), h.db, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	err = Upload(r.Context(), h.db, runID, userID, email, p)
	switch {
	case errors.Is(err, ErrRunNotFound):
		respondErr(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, ErrNotAllowed):
		respondErr(w, http.StatusForbidden, "raw_log_not_allowed")
		return
	case err != nil:
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminGetRawPoints GET /api/v1/admin/gps-runs/{runId}/raw-points（外層由 main.go 直接掛
// adminAcctHandler.RequireSuper，比照 /admin/admins、/admin/audit 前例的註冊方式——不是掛在
// internal/activity 既有的 /admin/gps-runs Mount 底下，那個 Mount 只套 perm("gps_review")，
// 定位點屬於個資／位置資料，需要比一般 GPS 審核更高一層的權限）。
func (h *Handler) AdminGetRawPoints(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	rec, err := Get(r.Context(), h.db, runID)
	if errors.Is(err, ErrRunNotFound) {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, rec)
}

func lookupEmail(ctx context.Context, db *pgxpool.Pool, userID string) (string, error) {
	var email string
	err := db.QueryRow(ctx, `SELECT COALESCE(email,'') FROM users WHERE id=$1`, userID).Scan(&email)
	return email, err
}
