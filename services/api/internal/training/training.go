// Package training 自主訓練（P1）：課表庫 + 配速等級表，VIP 限定功能。
//
// 前端拿到 TemplateSegment（以「效度 effort」表達強度：easy/marathon/threshold/interval/rep）＋
// 玩家自選的 PaceLevel，在前端解析成既有 WorkoutSegment（帶實際配速秒/公里），沿用 /track 既有
// 分段課表引擎（見 apps/web/src/lib/workout.ts）。P1 只提供清單，不新增任何完成/獎勵端點——
// 跑步照常走 GPS 上傳自動發里程 EXP。
package training

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

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
	r.Get("/templates", h.Templates) // GET /training/templates — 課表庫 + 配速等級表（VIP 限定）
	return r
}

// Templates GET /training/templates — VIP 專屬：課表庫 + 配速等級表。
func (h *Handler) Templates(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var isVip bool
	_ = h.db.QueryRow(r.Context(), `SELECT COALESCE(vip_expires_at > NOW(), FALSE) FROM users WHERE id=$1`, uid).Scan(&isVip)
	if !isVip {
		respondErr(w, http.StatusForbidden, "vip_only")
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

// --- 共用 ---

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
