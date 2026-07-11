// Package mail 站內信（in-app mail）：後台廣播/系統事件寫入，前台鈴鐺列表 + 未讀數。
// 與 push（推播）、mailer（email）是三個獨立管道；本套件只管 user_mail 資料表本身。
package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/realtime"
)

type Handler struct {
	db *pgxpool.Pool
	rt *realtime.Manager
}

func NewHandler(db *pgxpool.Pool, rt *realtime.Manager) *Handler { return &Handler{db: db, rt: rt} }

// Router 需登入子路由：掛在 /api/v1/mail。
func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Get("/unread-count", h.UnreadCount)
	r.Post("/read", h.MarkRead)
	return r
}

type mailItem struct {
	ID        string    `json:"id"`
	Level     string    `json:"level"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

// List GET /mail — 近 3 個月站內信（最多 200 筆），含未讀數。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT id, level, title, body, url, (read_at IS NOT NULL) AS read, created_at
		FROM user_mail
		WHERE user_id=$1 AND created_at > now() - interval '3 months'
		ORDER BY created_at DESC
		LIMIT 200`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []mailItem{}
	unread := 0
	for rows.Next() {
		var m mailItem
		if err := rows.Scan(&m.ID, &m.Level, &m.Title, &m.Body, &m.URL, &m.Read, &m.CreatedAt); err != nil {
			continue
		}
		if !m.Read {
			unread++
		}
		out = append(out, m)
	}
	respondJSON(w, http.StatusOK, map[string]any{"mail": out, "unread_count": unread})
}

// UnreadCount GET /mail/unread-count — 近 3 個月未讀數（供鈴鐺角標，輕量輪詢用）。
func (h *Handler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var count int
	if err := h.db.QueryRow(r.Context(), `
		SELECT count(*) FROM user_mail
		WHERE user_id=$1 AND read_at IS NULL AND created_at > now() - interval '3 months'`, uid).Scan(&count); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"unread_count": count})
}

type markReadReq struct {
	IDs []string `json:"ids"`
	All bool     `json:"all"`
}

// MarkRead POST /mail/read — body:{ids:[uuid...], all:bool}。all=true 時標記全部未讀為已讀。
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req markReadReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.All {
		tag, err := h.db.Exec(r.Context(), `UPDATE user_mail SET read_at=now() WHERE user_id=$1 AND read_at IS NULL`, uid)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"ok": true, "marked": tag.RowsAffected()})
		return
	}

	if len(req.IDs) == 0 {
		respondJSON(w, http.StatusOK, map[string]any{"ok": true, "marked": 0})
		return
	}
	tag, err := h.db.Exec(r.Context(), `
		UPDATE user_mail SET read_at=now()
		WHERE user_id=$1 AND id = ANY($2::uuid[]) AND read_at IS NULL`, uid, req.IDs)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "marked": tag.RowsAffected()})
}

var validLevels = map[string]bool{"normal": true, "important": true, "urgent": true}

// InsertForUsers 為每位 userID 寫一封站內信，回傳實際寫入筆數。給後台廣播（push 套件）等其他模組呼叫。
func (h *Handler) InsertForUsers(ctx context.Context, userIDs []string, level, title, body, url string) (int, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	if !validLevels[level] {
		level = "normal"
	}
	tag, err := h.db.Exec(ctx, `
		INSERT INTO user_mail (user_id, level, title, body, url)
		SELECT unnest($1::uuid[]), $2, $3, $4, $5`, userIDs, level, title, body, url)
	if err != nil {
		return 0, err
	}
	// 即時通知收件人：前台未讀紅點/清單立刻更新（走全站 data_updated，topic=mail，僅推給收件人）
	if h.rt != nil {
		h.rt.PublishData(ctx, "mail", userIDs)
	}
	return int(tag.RowsAffected()), nil
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
