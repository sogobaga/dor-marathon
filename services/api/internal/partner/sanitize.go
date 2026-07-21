package partner

import "github.com/dor/api/internal/htmlsafe"

// SanitizeDetailHTML 消毒商家介紹 HTML。寫入 DB 前呼叫一次、每次輸出前再呼叫一次
// （第二道防線，避免有人繞過 API 直接改 DB）。政策定義於共用套件 internal/htmlsafe，
// 供全站其他「使用者輸入 HTML」欄位（如賽事簡章文字區塊）共用，避免政策漂移。
func SanitizeDetailHTML(raw string) string {
	return htmlsafe.Sanitize(raw)
}
