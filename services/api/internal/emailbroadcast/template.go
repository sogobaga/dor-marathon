package emailbroadcast

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/url"
)

// unsubTokenLen 取 HMAC-SHA256 前 16 hex 字元（比照既有 state token 慣例的簡短化取捨——這裡不需要
// 防重放時效性，只需要「沒有密鑰就無法偽造某個 user_id 的退訂連結」，16 hex＝64 bit 已足夠防碰撞猜測）。
const unsubTokenLen = 16

// unsubToken 為 userID 產生退訂 token：HMAC(secret, "email-unsub:"+userID) 前 16 hex。
func unsubToken(secret, userID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("email-unsub:" + userID))
	return hex.EncodeToString(mac.Sum(nil))[:unsubTokenLen]
}

// verifyUnsubToken 常數時間比對，避免時序攻擊猜出合法 token。
func verifyUnsubToken(secret, userID, token string) bool {
	if userID == "" || token == "" {
		return false
	}
	expected := unsubToken(secret, userID)
	return hmac.Equal([]byte(expected), []byte(token))
}

// unsubscribeURL 組出某位使用者專屬的退訂連結（每封信各自帶入，見 send.go buildEmailHTML）。
func (h *Handler) unsubscribeURL(userID string) string {
	return h.unsubBase + "?u=" + url.QueryEscape(userID) + "&t=" + unsubToken(h.jwtSecret, userID)
}

// buildEmailHTML 組出寄給單一使用者的完整 HTML 內容：品牌 header + 主旨 + 內文（admin 輸入，
// 允許簡單 HTML，故不逃逸——僅限有 settings 權限的後台管理員可觸發，信任範圍等同其他後台可自由
// 輸入 HTML 的欄位）+ footer（比照 support/terms/privacy 頁「只留 Email+統編，不列地址電話」的
// 對外聯絡政策）+ 退訂連結。
func buildEmailHTML(subject, bodyHTML, unsubURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><body style="font-family:sans-serif;color:#222;max-width:600px;margin:0 auto;padding:24px 20px;">
<div style="color:#888;font-size:12px;letter-spacing:1px;">DOR 跑步平台</div>
<h2 style="margin:10px 0 20px;">%s</h2>
<div style="line-height:1.75;font-size:15px;">%s</div>
<hr style="margin:36px 0 16px;border:none;border-top:1px solid #eee;" />
<div style="font-size:12px;color:#999;line-height:1.9;">
DOR 跑步平台　·　service@dor.tw　·　統一編號：83005678<br />
不想再收到這類 Email？<a href="%s" style="color:#999;">取消訂閱</a>
</div>
</body></html>`, html.EscapeString(subject), bodyHTML, html.EscapeString(unsubURL))
}

// unsubscribePage 退訂成功/失敗的簡單 HTML 頁（白底、繁中，見 handler.go Unsubscribe）。
func unsubscribePage(title, message string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8" /><title>%s</title></head>
<body style="font-family:sans-serif;background:#fff;color:#222;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;">
<div style="max-width:420px;padding:32px;text-align:center;">
<h2 style="margin:0 0 16px;">%s</h2>
<p style="line-height:1.8;color:#555;">%s</p>
</div>
</body></html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}
