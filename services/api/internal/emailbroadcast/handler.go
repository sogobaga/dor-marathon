// Package emailbroadcast 後台向全部玩家發送 Email（Resend 批次寄送）＋ 退訂合規。
// 與 internal/push（Web Push + 既有 email/mail 頻道即時廣播）、internal/mail（站內信）是三個獨立
// 管道；本套件專責「Email 電子報式全體廣播」——落地 email_broadcasts 紀錄表供後台追蹤進度/結果，
// 並附退訂連結（email_unsubscribes）滿足寄送合規，兩者都在 migration 141。
//
// 寄送走 internal/notify.SendEmailBatch（Resend），每批最多 100 人，批次間 sleep 節流（見
// send.go），在 POST 建立廣播紀錄後於背景 goroutine 執行，不卡住 admin 的 HTTP 請求。
package emailbroadcast

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
)

const (
	maxSubjectLen  = 200
	maxBodyHTMLLen = 100 * 1024 // 100KB
)

// Handler 掛載 /email/unsubscribe（公開）與 /admin/email-broadcasts（需 settings 權限）路由。
type Handler struct {
	db        *pgxpool.Pool
	jwtSecret string
	unsubBase string // 例如 https://www.dor.tw/api/v1/email/unsubscribe
}

// NewHandler 建立 Handler。frontendURL 用來組退訂連結的完整網址（比照既有 ECPay 回呼網址從
// FrontendURL 組出的慣例），jwtSecret 沿用 cfg.JWTSecret（同一把密鑰簽退訂 token，不另外新增
// 環境變數）。
func NewHandler(db *pgxpool.Pool, jwtSecret, frontendURL string) *Handler {
	return &Handler{
		db:        db,
		jwtSecret: jwtSecret,
		unsubBase: strings.TrimRight(frontendURL, "/") + "/api/v1/email/unsubscribe",
	}
}

// PublicRouter 公開子路由：掛在 /api/v1/email。
func (h *Handler) PublicRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/unsubscribe", h.Unsubscribe)
	return r
}

// GET /api/v1/email/unsubscribe?u=<userID>&t=<token> — 公開端點（信件內連結，不要求登入）。
// 驗證 HMAC token 通過 → INSERT email_unsubscribes（ON CONFLICT DO NOTHING，重複點擊安全）→
// 回簡單 HTML 頁告知已取消訂閱電子報（站內重要通知不受影響，那是另一條 mail 管道）。
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("u")
	token := r.URL.Query().Get("t")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if !verifyUnsubToken(h.jwtSecret, userID, token) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(unsubscribePage("連結無效", "這個退訂連結不正確或已過期，如需取消訂閱請重新從最近收到的電子報中點擊退訂連結。")))
		return
	}

	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO email_unsubscribes (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID,
	); err != nil {
		log.Error().Err(err).Str("user_id", userID).Msg("email unsubscribe: insert failed")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(unsubscribePage("系統忙碌", "退訂處理失敗，請稍後再試一次。")))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(unsubscribePage("已取消訂閱", "已取消訂閱電子報，你仍會收到站內重要通知。")))
}

// AdminRouter 後台子路由：掛在 /api/v1/admin/email-broadcasts（外層已檢查 settings 權限）。
func (h *Handler) AdminRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Get("/recipient-count", h.RecipientCount)
	r.Post("/", h.Create)
	return r
}

// recipient 一位收件者（用來組個人化的退訂連結）。
type recipient struct {
	ID    string
	Email string
	Name  string
}

// listRecipients 撈寄送對象：email 非空、且未退訂。
// 虛擬選手(is_virtual)不參與 Email 全體廣播。
func (h *Handler) listRecipients(ctx context.Context) ([]recipient, error) {
	rows, err := h.db.Query(ctx, `
		SELECT id::text, email, name FROM users
		WHERE COALESCE(email,'') <> ''
		  AND NOT is_virtual
		  AND id NOT IN (SELECT user_id FROM email_unsubscribes)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []recipient
	for rows.Next() {
		var rc recipient
		if err := rows.Scan(&rc.ID, &rc.Email, &rc.Name); err != nil {
			return nil, err
		}
		// 格式不合法的 email 直接排除（2026-08-23 上線公告首發全滅根因：早期內建帳號 email='admin'
		// 非合法信箱，Resend 批次 API「一封格式錯→整包 422 拒絕」，46 封全標失敗）。與「指定對象」
		// 路徑的 emailFormatRe 同一把尺，兩條路徑永遠一致。
		if !emailFormatRe.MatchString(rc.Email) {
			log.Warn().Str("user_id", rc.ID).Msg("email broadcast: skip malformed email address")
			continue
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

// GET /api/v1/admin/email-broadcasts/recipient-count — 前台送出前先問人數，用來組確認 dialog。
func (h *Handler) RecipientCount(w http.ResponseWriter, r *http.Request) {
	recipients, err := h.listRecipients(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to count recipients")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"count": len(recipients)})
}

// broadcastItem 後台歷史列表一列。
type broadcastItem struct {
	ID         string  `json:"id"`
	Subject    string  `json:"subject"`
	Status     string  `json:"status"`
	Audience   string  `json:"audience"` // 'all' 全部玩家 | 'custom:N人' 指定 N 位會員（migration 142）
	TotalCount int     `json:"total_count"`
	SentCount  int     `json:"sent_count"`
	FailCount  int     `json:"fail_count"`
	ErrorNote  string  `json:"error_note"`
	CreatedAt  string  `json:"created_at"`
	FinishedAt *string `json:"finished_at"`
}

// GET /api/v1/admin/email-broadcasts — 歷史列表（進度輪詢用），最新在前，上限 100 筆。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT id::text, subject, status, audience, total_count, sent_count, fail_count, error_note,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SSZ'),
		       to_char(finished_at, 'YYYY-MM-DD"T"HH24:MI:SSZ')
		FROM email_broadcasts
		ORDER BY created_at DESC
		LIMIT 100`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load broadcasts")
		return
	}
	defer rows.Close()

	out := []broadcastItem{}
	for rows.Next() {
		var it broadcastItem
		var finishedAt *string
		if err := rows.Scan(&it.ID, &it.Subject, &it.Status, &it.Audience, &it.TotalCount, &it.SentCount, &it.FailCount,
			&it.ErrorNote, &it.CreatedAt, &finishedAt); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to scan broadcasts")
			return
		}
		it.FinishedAt = finishedAt
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load broadcasts")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"broadcasts": out})
}

// POST /api/v1/admin/email-broadcasts { subject, body_html, recipients?, dry_run? }
// 建立廣播紀錄（total_count=符合對象數）→ 啟動背景 goroutine 分批寄送 → 立即回應（不等寄送完成）。
// 防呆：subject/body_html 必填、長度上限；同時只允許一個 status='sending' 的廣播（409）。
//
// recipients 選填（migration 142）：空/缺＝寄給全部玩家（沿用既有 listRecipients，audience='all'）；
// 有值＝只寄給清單內比對到會員且未退訂的 email（audience='custom:N人'），查無會員/已退訂者從回應
// 的 not_found／unsubscribed_excluded 回報給前台，讓管理員核對——不寄給非平台會員。
//
// dry_run=true 時只做清洗＋查詢＋回報數字，不建立廣播紀錄、不觸發寄送，讓前台能在真正送出前用
// 這支端點的真實回應數字組確認 dialog（不影響「同時只能一則 sending」的防呆判斷）。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	var body struct {
		Subject    string   `json:"subject"`
		BodyHTML   string   `json:"body_html"`
		Recipients []string `json:"recipients"`
		DryRun     bool     `json:"dry_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	subject := strings.TrimSpace(body.Subject)
	bodyHTML := strings.TrimSpace(body.BodyHTML)
	if subject == "" {
		respondErr(w, http.StatusBadRequest, "請填寫主旨")
		return
	}
	if len(subject) > maxSubjectLen {
		respondErr(w, http.StatusBadRequest, "主旨過長")
		return
	}
	if bodyHTML == "" {
		respondErr(w, http.StatusBadRequest, "請填寫內文")
		return
	}
	if len(bodyHTML) > maxBodyHTMLLen {
		respondErr(w, http.StatusBadRequest, "內容過長（上限 100KB）")
		return
	}
	if len(body.Recipients) > maxCustomRecipients {
		respondErr(w, http.StatusBadRequest, fmt.Sprintf("收件人數量上限 %d 筆", maxCustomRecipients))
		return
	}

	if !body.DryRun {
		var sendingCount int
		if err := h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM email_broadcasts WHERE status='sending'`).Scan(&sendingCount); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to check in-flight broadcasts")
			return
		}
		if sendingCount > 0 {
			respondErr(w, http.StatusConflict, "上一則廣播寄送中")
			return
		}
	}

	var recipients []recipient
	notFound := []string{}
	unsubExcluded := 0
	audience := "all"

	if len(body.Recipients) == 0 {
		var err error
		recipients, err = h.listRecipients(r.Context())
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to load recipients")
			return
		}
	} else {
		cr, err := h.resolveCustomRecipients(r.Context(), body.Recipients)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to resolve recipients")
			return
		}
		if cr.MatchedCount == 0 {
			respondErr(w, http.StatusBadRequest, "查無符合的會員")
			return
		}
		recipients = cr.Recipients
		notFound = cr.NotFound
		unsubExcluded = cr.UnsubscribedExcluded
		audience = customAudienceLabel(len(recipients))
	}

	if body.DryRun {
		respondJSON(w, http.StatusOK, map[string]any{
			"total": len(recipients), "audience": audience,
			"not_found": notFound, "unsubscribed_excluded": unsubExcluded,
		})
		return
	}

	var id string
	if err := h.db.QueryRow(r.Context(), `
		INSERT INTO email_broadcasts (subject, body_html, status, total_count, created_by, audience)
		VALUES ($1, $2, 'sending', $3, NULLIF($4, '')::uuid, $5)
		RETURNING id::text`, subject, bodyHTML, len(recipients), userID, audience).Scan(&id); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create broadcast")
		return
	}

	log.Info().Str("broadcast_id", id).Int("total", len(recipients)).Str("admin", userID).Str("audience", audience).
		Msg("email broadcast: created, starting background send")
	go h.runBroadcastSafe(id, subject, bodyHTML, recipients)

	respondJSON(w, http.StatusOK, map[string]any{
		"id": id, "total": len(recipients), "audience": audience,
		"not_found": notFound, "unsubscribed_excluded": unsubExcluded,
	})
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
