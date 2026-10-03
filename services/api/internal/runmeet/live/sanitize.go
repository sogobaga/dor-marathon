package live

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MaxNameRunes 顯示名稱上限（rune）。超過就截斷並加「…」。
const MaxNameRunes = 20

// FallbackName 名稱與 handle 都消毒成空字串時的最後退路。
const FallbackName = "跑者"

// SanitizeDisplayName 團練同步跑的玩家顯示名稱消毒（契約 §5，Go 端；/live/start 取名後呼叫）。
//
//	name   = COALESCE(NULLIF(u.name,''), u.handle) 的 name 部分（display-name-convention：禁讀個資暱稱）
//	handle = u.handle，name 消毒後變空字串時的退路
//
// 步驟：移除控制字元（Cc）與零寬／bidi／不可見格式字元 → NFC 正規化 → trim →
// 超過 MaxNameRunes 個 rune 截斷加「…」；結果為空就退回 handle（同樣消毒），再空 → 「跑者」。
//
// ⚠️ 這裡刻意**不做 HTML 跳脫**：`<script>` 之類會原樣保留成純文字。XSS 的防線在 client——
// Leaflet tooltip 一律傳 DOM 元素（textContent），禁字串（契約 §5 / C2）；canvas fillText 本來就不解析 HTML。
// 在伺服器端跳脫反而會讓 `&` 之類被雙重編碼成亂碼。
//
// 移除的不可見字元（雙向覆寫可把「團練gnp.exe」顯示成「團練exe.png」；零寬字元可隱形灌長度）：
//
//	U+200B–U+200F（零寬空白／ZWNJ／ZWJ／LRM／RLM）、U+202A–U+202E（LRE/RLE/PDF/LRO/RLO）、
//	U+2060–U+2064（WJ 與不可見數學運算子）、U+2066–U+2069（LRI/RLI/FSI/PDI）、U+FEFF（BOM/ZWNBSP）
//
// 契約明列以上；另外一併移除同類的不可見欺騙字元（不影響一般文字與 emoji）：
// U+061C（阿拉伯字母標記，bidi）、U+00AD（軟連字號）、U+034F（組合字元連接符）、U+180E、
// U+2028/U+2029（行／段分隔，Zl/Zp）、U+FFF9–U+FFFB（行間註記）、U+E0000–U+E007F（tag 字元，
// 可夾帶隱形文字）。
func SanitizeDisplayName(name, handle string) string {
	if s := cleanName(name); s != "" {
		return s
	}
	if s := cleanName(handle); s != "" {
		return s
	}
	return FallbackName
}

func cleanName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isInvisible(r) {
			continue
		}
		b.WriteRune(r)
	}
	// NFC 放在移除之後：移除不可見字元（例如 ZWJ 夾在 base 與組合符號之間）可能讓兩段變成
	// 相鄰、可組合的序列，先移除再正規化才會得到真正的 NFC。
	out := strings.TrimSpace(norm.NFC.String(b.String()))

	rs := []rune(out)
	if len(rs) > MaxNameRunes {
		out = strings.TrimSpace(string(rs[:MaxNameRunes])) + "…"
	}
	return out
}

// isInvisible 控制字元（Cc）與零寬／bidi／不可見格式字元。
func isInvisible(r rune) bool {
	if unicode.IsControl(r) { // Cc：U+0000–001F、U+007F–009F
		return true
	}
	switch {
	case r >= 0x200B && r <= 0x200F,
		r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x2064,
		r >= 0x2066 && r <= 0x2069,
		r == 0xFEFF,
		r == 0x061C, r == 0x00AD, r == 0x034F, r == 0x180E,
		r == 0x2028, r == 0x2029,
		r >= 0xFFF9 && r <= 0xFFFB,
		r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return false
}
