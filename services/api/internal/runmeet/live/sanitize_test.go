package live

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 不可見字元一律用 rune 值組字串（不在原始碼裡放字面的零寬／bidi／BOM：編輯器看不見、
// Go 編譯器還會拒絕字面 BOM，而且 bidi 覆寫字元放進原始碼本身就是 Trojan Source 型風險）。
func r(code rune) string { return string(code) }

var (
	zwsp = r(0x200B)
	zwnj = r(0x200C)
	zwj  = r(0x200D)
	lrm  = r(0x200E)
	rlm  = r(0x200F)

	lre = r(0x202A)
	rle = r(0x202B)
	pdf = r(0x202C)
	lro = r(0x202D)
	rlo = r(0x202E)

	wj   = r(0x2060)
	fa   = r(0x2061) // function application（不可見數學運算子）
	it   = r(0x2062)
	isep = r(0x2063)
	iplu = r(0x2064)

	lri = r(0x2066)
	rli = r(0x2067)
	fsi = r(0x2068)
	pdi = r(0x2069)

	bom  = r(0xFEFF)
	alm  = r(0x061C)
	shy  = r(0x00AD)
	lsep = r(0x2028)
	psep = r(0x2029)
	tagA = r(0xE0041)

	nel       = r(0x0085)
	nbsp      = r(0x00A0)
	ideoSpace = r(0x3000)
	acute     = r(0x0301) // 組合尖音符
)

// 契約 §5 單元測試：<script>、RTL、超長、emoji、空字串→handle→「跑者」。
func TestSanitizeDisplayName(t *testing.T) {
	long25 := strings.Repeat("跑", 25)
	cases := []struct {
		name, handle string
		want         string
		why          string
	}{
		{"小明", "xm", "小明", "一般名稱原樣"},
		{"  小明  ", "xm", "小明", "trim"},
		{"Alice Chen", "ac", "Alice Chen", "內部空白保留"},

		// <script>：不做 HTML 跳脫（XSS 防線在 client：Leaflet tooltip 傳 DOM 元素 textContent）
		{"<b>x</b>", "h", "<b>x</b>", "HTML 原樣保留成純文字（不跳脫、不剝除）"},
		{"<script>alert(1)</script>", "h", "<script>alert(1)</sc…", "超過 20 rune 截斷加…（<script> 仍是純文字）"},
		{`<img src=x onerror=alert(1)>`, "h", "<img src=x onerror=a…", "同上（onerror）"},

		// RTL／bidi 覆寫：「團練gnp.exe」會被顯示成「團練exe.png」
		{"團練" + rlo + "gnp.exe", "h", "團練gnp.exe", "RLO 移除"},
		{rlo + "evil" + pdf, "h", "evil", "RLO/PDF 移除"},
		{"a" + lri + "b" + rli + "c" + fsi + "d" + pdi + "e", "h", "abcde", "LRI/RLI/FSI/PDI 移除"},
		{"a" + lrm + "b" + rlm + "c", "h", "abc", "LRM/RLM 移除"},
		{lre + rle + pdf + lro + rlo + "X", "h", "X", "LRE/RLE/PDF/LRO/RLO 全移除"},
		{"x" + alm + "y", "h", "xy", "阿拉伯字母標記（bidi）移除"},

		// 零寬／不可見
		{"a" + zwsp + "b" + zwnj + "c" + zwj + "d" + bom + "e", "h", "abcde", "ZWSP/ZWNJ/ZWJ/BOM 移除"},
		{"a" + wj + "b" + fa + "c" + it + "d" + isep + "e" + iplu + "f", "h", "abcdef", "U+2060–2064 移除"},
		{"a" + shy + "b", "h", "ab", "軟連字號移除"},
		{"a" + tagA + "b", "h", "ab", "tag 字元（隱形文字夾帶）移除"},
		{"a" + lsep + "b" + psep + "c", "h", "abc", "行／段分隔符移除"},

		// 控制字元（Cc）
		{"a" + r(0) + "b" + r('\t') + "c" + r('\n') + "d" + r('\r') + "e" + r(0x7f) + "f" + nel + "g", "h", "abcdefg", "C0／DEL／C1 控制字元全移除"},

		// 長度：以 rune 計（不是 byte），超過 20 截斷加「…」
		{strings.Repeat("跑", 20), "h", strings.Repeat("跑", 20), "剛好 20 rune 不截"},
		{strings.Repeat("跑", 21), "h", strings.Repeat("跑", 20) + "…", "21 rune → 20 + …"},
		{long25, "h", strings.Repeat("跑", 20) + "…", "超長截斷"},
		{strings.Repeat("a", 100), "h", strings.Repeat("a", 20) + "…", "ASCII 超長"},
		{"abcdefghijklmnopqrs tuvw", "h", "abcdefghijklmnopqrs…", "截斷點落在空白：先 trim 再加…"},

		// emoji：每個基本 emoji 是 1 個 rune；計數與截斷都以 rune 為單位
		{"🏃小明", "h", "🏃小明", "emoji 保留"},
		{strings.Repeat("😀", 20), "h", strings.Repeat("😀", 20), "20 個 emoji 不截"},
		{strings.Repeat("😀", 21), "h", strings.Repeat("😀", 20) + "…", "21 個 emoji → 截成 20 + …"},

		// NFC 正規化：decomposed é（e + U+0301）→ composed é（U+00E9）
		{"cafe" + acute, "h", "caf" + r(0xE9), "NFC"},
		// 先移除不可見字元、再 NFC：ZWJ 夾在 base 與組合符號之間時，移除後兩者相鄰才會被組合
		{"e" + zwj + acute, "h", r(0xE9), "移除後再 NFC"},

		// 空字串 → handle → 「跑者」
		{"", "runner_01", "runner_01", "name 空 → handle"},
		{"   ", "runner_01", "runner_01", "name 只有空白 → handle"},
		{zwsp + lrm + rlo, "runner_01", "runner_01", "name 只有不可見字元 → handle"},
		{ideoSpace + nbsp, "runner_01", "runner_01", "全形空白／NBSP 也算空白"},
		{"", "<i>" + rlo + "x", "<i>x", "handle 同樣消毒"},
		{"", strings.Repeat("h", 30), strings.Repeat("h", 20) + "…", "handle 同樣截斷"},
		{"", "", "跑者", "兩者皆空 → 跑者"},
		{zwsp, rlo, "跑者", "兩者消毒後皆空 → 跑者"},
		{"  ", "  ", "跑者", "兩者只有空白 → 跑者"},
	}
	for _, c := range cases {
		got := SanitizeDisplayName(c.name, c.handle)
		if got != c.want {
			t.Errorf("%s\n  SanitizeDisplayName(%q, %q) = %q (%U)\n  want %q", c.why, c.name, c.handle, got, []rune(got), c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: output is not valid UTF-8: %q", c.why, got)
		}
	}
}

// 任何輸入的輸出都不含 bidi／零寬／控制字元，且 rune 數 ≤ 21（20 + …）。
func TestSanitizeNeverLeavesInvisibleOrOverlong(t *testing.T) {
	inputs := []string{
		"a" + rlo + "b" + zwsp + "c", lri + rli + fsi + pdi, strings.Repeat(zwsp+"團", 40),
		"x" + r(0) + "y" + r(0x1f) + "z" + r(0x7f), strings.Repeat("😀"+zwj, 30), bom + bom + bom + "名字" + bom,
		strings.Repeat("a"+rlo, 100),
	}
	for _, in := range inputs {
		out := SanitizeDisplayName(in, "")
		if utf8.RuneCountInString(out) > MaxNameRunes+1 {
			t.Errorf("SanitizeDisplayName(%q) has %d runes", in, utf8.RuneCountInString(out))
		}
		for _, ch := range out {
			if isInvisible(ch) {
				t.Errorf("SanitizeDisplayName(%q) = %q still contains invisible %U", in, out, ch)
			}
		}
		if out == "" {
			t.Errorf("SanitizeDisplayName(%q) returned empty string", in)
		}
	}
}
