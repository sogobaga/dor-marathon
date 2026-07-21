// Package htmlsafe 提供全站共用的「使用者輸入 HTML」消毒政策，避免各模組各自維護
// 一份白名單而漂移。目前使用方：跑者充電站（partner 商家介紹）、賽事簡章文字區塊。
package htmlsafe

import (
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// policy 通用 HTML 消毒規則：只允許純排版標籤，連結限 http/https 並強制
// target="_blank" rel="noopener noreferrer"。script/iframe/style/object/embed/form、
// 所有 on* 事件屬性、style 屬性一律不在白名單內，bluemonday 會整段剝除。
var policy = newPolicy()

func newPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("b", "strong", "i", "em", "u", "br", "p", "ul", "ol", "li", "h3", "h4")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https") // 擋 javascript: / data: 等危險 scheme
	p.RequireNoReferrerOnFullyQualifiedLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true) // target=_blank + rel 附加 noopener
	return p
}

// Sanitize 消毒使用者輸入的 HTML（白名單：b strong i em u br p ul ol li h3 h4 a[href]）。
// 呼叫端須在「寫入 DB 前」與「每次輸出前」各呼叫一次（雙重防線，避免有人繞過 API 直接改 DB）。
func Sanitize(raw string) string {
	return strings.TrimSpace(policy.Sanitize(raw))
}
