package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

// resendAPIBase Resend API 根網址；宣告成套件變數只為了測試可覆寫指向 httptest mock server
// （比照 payment.BindClient.BaseURL 的測試慣例），正式環境一律用預設值。
var resendAPIBase = "https://api.resend.com"

// ErrEmailNotConfigured 未設定 env RESEND_API_KEY（Email 廣播功能停用）。呼叫端應據此停止寄送、
// 在後台顯示「Email 服務未設定」，不要當成一般寄送失敗重試或跳過單一收件者。
var ErrEmailNotConfigured = errors.New("email service not configured: RESEND_API_KEY unset")

// EmailMsg 一封 batch 郵件。Resend POST /emails/batch 的陣列「每個元素即一封完整獨立的信」——不是
// 一封信寄給多個收件者，因此 To 通常只放單一收件者，讓每人可各自帶不同內容（例如各自的退訂連結）。
type EmailMsg struct {
	To      []string
	Subject string
	HTML    string
}

// ResendAPIError Resend API 回傳的標準錯誤格式（見 resend.com/docs/api-reference/errors：
// {statusCode, name, message}）。呼叫端可用 errors.As 取出 Name 判斷錯誤類型（例如
// IsQuotaExceeded），而不必字串比對 Error() 的組合文字。
type ResendAPIError struct {
	StatusCode int
	Name       string
	Message    string
}

func (e *ResendAPIError) Error() string {
	return fmt.Sprintf("resend api error: status=%d name=%s message=%s", e.StatusCode, e.Name, e.Message)
}

// IsQuotaExceeded 是否為每日／每月寄送額度用盡（Resend 免費層預設 100 封/天）。呼叫端遇到此錯誤
// 應立即停止繼續寄送（後續批次必然還是同樣的配額錯誤），而不是當一般批次失敗繼續硬打下一批。
func IsQuotaExceeded(err error) bool {
	var rerr *ResendAPIError
	if errors.As(err, &rerr) {
		return rerr.Name == "daily_quota_exceeded" || rerr.Name == "monthly_quota_exceeded"
	}
	return false
}

// DefaultFrom 解析寄件者身分：優先讀 env EMAIL_FROM，未設定則用固定預設值。
func DefaultFrom() string {
	if v := os.Getenv("EMAIL_FROM"); v != "" {
		return v
	}
	return "DOR 跑步平台 <noreply@dor.tw>"
}

// SendEmailBatch 一次寄送最多 100 封各自獨立的信（POST https://api.resend.com/emails/batch，
// Bearer 驗證）。憑證讀 env RESEND_API_KEY；未設定 → 直接回 ErrEmailNotConfigured（no-op、
// 不打 API），比照 Telegram() 的 leaf/env-gated no-op 慣例，差別只在「未設定」在這裡是明確錯誤
// 而非靜默成功——呼叫端（後台 Email 廣播）需要能區分「服務未設定」以顯示對應訊息，不能把它
// 誤當成「已寄出 0 封」。
//
// Resend 預設 rate limit 約 2 req/s；本函式每次呼叫只送一個 HTTP request（一批），不在內部重試
// 或睡眠——批次間隔／節流是呼叫端（例如逐批寄送的背景 goroutine）的責任。
func SendEmailBatch(ctx context.Context, from string, msgs []EmailMsg) error {
	if len(msgs) == 0 {
		return nil
	}
	if len(msgs) > 100 {
		return fmt.Errorf("SendEmailBatch: batch too large (%d), Resend 單次上限 100", len(msgs))
	}
	apiKey := os.Getenv("RESEND_API_KEY")
	if apiKey == "" {
		return ErrEmailNotConfigured
	}

	type payloadItem struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		HTML    string   `json:"html"`
	}
	items := make([]payloadItem, len(msgs))
	for i, m := range msgs {
		items[i] = payloadItem{From: from, To: m.To, Subject: m.Subject, HTML: m.HTML}
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("encode resend batch payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendAPIBase+"/emails/batch", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		var decoded struct {
			StatusCode int    `json:"statusCode"`
			Name       string `json:"name"`
			Message    string `json:"message"`
		}
		if jsonErr := json.Unmarshal(raw, &decoded); jsonErr == nil && decoded.Name != "" {
			return &ResendAPIError{StatusCode: resp.StatusCode, Name: decoded.Name, Message: decoded.Message}
		}
		return fmt.Errorf("resend batch send failed: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
