// Package push 實作 Web Push（VAPID）：使用者訂閱管理 + 後台廣播推播。
// 契約：訂閱物件＝瀏覽器 PushSubscription JSON { endpoint, keys:{p256dh,auth} }。
// 未設齊 VAPID_PUBLIC_KEY / VAPID_PRIVATE_KEY / VAPID_SUBJECT 時 enabled()=false，
// /push/vapid 回 enabled:false，發送一律 no-op（不報錯）。
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

// Config VAPID 設定，全來自環境變數（由 cmd/api/main.go 傳入）。
type Config struct {
	PublicKey  string
	PrivateKey string
	Subject    string // mailto: 或 https 開頭
}

func (c Config) enabled() bool {
	return c.PublicKey != "" && c.PrivateKey != "" && c.Subject != ""
}

// Subscription 訂閱物件（對應瀏覽器 PushSubscription JSON）。
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// PushMessage 送到 Service Worker 的推播內容 payload。
type PushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// Handler 掛載 /push（需登入）與 /admin/push（需 settings 權限）路由。
type Handler struct {
	db  *pgxpool.Pool
	cfg Config
}

// NewHandler 建立 push Handler。
func NewHandler(db *pgxpool.Pool, cfg Config) *Handler {
	return &Handler{db: db, cfg: cfg}
}

// Router 需登入子路由：掛在 /api/v1/push。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/vapid", h.GetVAPID)
	r.Post("/subscribe", h.Subscribe)
	r.Post("/unsubscribe", h.Unsubscribe)
	return r
}

// AdminRouter 後台子路由：掛在 /api/v1/admin/push（外層已檢查 settings 權限）。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/broadcast", h.Broadcast)
	return r
}

// GET /api/v1/push/vapid
func (h *Handler) GetVAPID(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"public_key": h.cfg.PublicKey,
		"enabled":    h.cfg.enabled(),
	})
}

// POST /api/v1/push/subscribe
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var sub Subscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		respondErr(w, http.StatusBadRequest, "missing endpoint or keys")
		return
	}

	_, err := h.db.Exec(r.Context(), `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (endpoint) DO UPDATE
		SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth
	`, userID, sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save subscription")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/push/unsubscribe
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	_, err := h.db.Exec(r.Context(), `
		DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2
	`, userID, body.Endpoint)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to remove subscription")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/admin/push/broadcast
func (h *Handler) Broadcast(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title         string   `json:"title"`
		Body          string   `json:"body"`
		URL           string   `json:"url,omitempty"`
		TargetUserIDs []string `json:"target_user_ids,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	sent, failed := 0, 0

	if h.cfg.enabled() {
		subs, err := h.listSubscriptions(r.Context(), body.TargetUserIDs)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to load subscriptions")
			return
		}

		msg := PushMessage{Title: body.Title, Body: body.Body, URL: body.URL}
		payload, err := json.Marshal(msg)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to encode payload")
			return
		}

		for _, s := range subs {
			if err := h.send(r.Context(), s, payload); err != nil {
				failed++
			} else {
				sent++
			}
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{"sent": sent, "failed": failed})
}

// storedSubscription 內部查詢結果（含 id/user_id 供刪除失效訂閱用）。
type storedSubscription struct {
	ID       string
	UserID   string
	Endpoint string
	P256dh   string
	Auth     string
}

func (h *Handler) listSubscriptions(ctx context.Context, targetUserIDs []string) ([]storedSubscription, error) {
	var rows pgx.Rows
	var err error
	if len(targetUserIDs) == 0 {
		rows, err = h.db.Query(ctx, `SELECT id, user_id, endpoint, p256dh, auth FROM push_subscriptions`)
	} else {
		rows, err = h.db.Query(ctx, `
			SELECT id, user_id, endpoint, p256dh, auth FROM push_subscriptions
			WHERE user_id = ANY($1)
		`, targetUserIDs)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []storedSubscription
	for rows.Next() {
		var s storedSubscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SendToUser 推播給單一使用者的所有訂閱裝置（給其他模組呼叫，例如任務完成通知）。
// enabled()=false 時直接 no-op。
func (h *Handler) SendToUser(ctx context.Context, userID string, msg PushMessage) error {
	if !h.cfg.enabled() {
		return nil
	}

	subs, err := h.listSubscriptions(ctx, []string{userID})
	if err != nil {
		return err
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	var firstErr error
	for _, s := range subs {
		if err := h.send(ctx, s, payload); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// send 送出單一訂閱的推播；端點回 404/410 表示訂閱已失效，從 DB 刪除。
func (h *Handler) send(ctx context.Context, s storedSubscription, payload []byte) error {
	if !h.cfg.enabled() {
		return nil
	}

	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: s.Endpoint,
		Keys: webpush.Keys{
			P256dh: s.P256dh,
			Auth:   s.Auth,
		},
	}, &webpush.Options{
		Subscriber:      h.cfg.Subject,
		VAPIDPublicKey:  h.cfg.PublicKey,
		VAPIDPrivateKey: h.cfg.PrivateKey,
		TTL:             86400,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		_, _ = h.db.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, s.ID)
		return errors.New("subscription expired")
	}

	if resp.StatusCode >= 300 {
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		return errors.New(strings.TrimSpace("push send failed: " + resp.Status + " " + buf.String()))
	}

	return nil
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
