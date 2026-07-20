package partner

import (
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// sanitizePolicy detail_html 消毒規則：只允許純排版標籤，連結限 http/https 並強制
// target="_blank" rel="noopener noreferrer"。script/iframe/style/object/embed/form、
// 所有 on* 事件屬性、style 屬性一律不在白名單內，bluemonday 會整段剝除。
var sanitizePolicy = newSanitizePolicy()

func newSanitizePolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("b", "strong", "i", "em", "u", "br", "p", "ul", "ol", "li", "h3", "h4")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https") // 擋 javascript: / data: 等危險 scheme
	p.RequireNoReferrerOnFullyQualifiedLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true) // target=_blank + rel 附加 noopener
	return p
}

// SanitizeDetailHTML 消毒商家介紹 HTML。寫入 DB 前呼叫一次、每次輸出前再呼叫一次
// （第二道防線，避免有人繞過 API 直接改 DB）。
func SanitizeDetailHTML(raw string) string {
	return strings.TrimSpace(sanitizePolicy.Sanitize(raw))
}
