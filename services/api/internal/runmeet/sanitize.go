package runmeet

import (
	"strings"
	"unicode/utf8"
)

// ⚠️ 本套件所有使用者輸入都是**純文字**，刻意不走 internal/htmlsafe。
// htmlsafe 是 HTML 白名單（給賽事簡章、商家介紹那種富文字），強套在這裡會把使用者真的打出來的
// `<` `>` 吃掉，也讓「留言不能有 HTML」這條規則變模糊。前端 React 文字節點自動跳脫即安全，
// 說明欄用 white-space: pre-wrap；RunMeet* 元件內 dangerouslySetInnerHTML 必須零出現。

// 各欄位長度上限（規格 4.6）。單位一律是 **rune**（不是 byte）——中文一字 3 bytes，
// 用 len() 會讓「40 字標題」實際只能打 13 個中文字（沿用 runcheer/training 的既有做法）。
const (
	MaxTitleRunes         = 40
	MinTitleRunes         = 2
	MaxRegionRunes        = 30
	MinRegionRunes        = 2
	MaxPlaceLabelRunes    = 60
	MinPlaceLabelRunes    = 2
	MaxMeetingDetailRunes = 200
	MaxDescriptionRunes   = 500
	MaxApplyNoteRunes     = 60
	MaxCommentRunes       = 200
	MaxReportReasonRunes  = 300
)

// normalizeText 純文字正規化（規格 4.6 的六個步驟）。回傳正規化後字串；超長回錯誤。
//
//  1. TrimSpace
//  2. 移除 C0/C1 控制字元（allowNewline 時保留 \n）
//  3. 移除 Unicode 雙向控制字元 U+202A–U+202E、U+2066–U+2069
//     （防 RLO 顯示偽造：「團練gnp.exe」會被顯示成「團練exe.png」）
//  4. 移除零寬字元 U+200B–U+200D、U+FEFF（防隱形灌長度／繞過重複偵測）
//  5. 連續換行壓成最多 2 個
//  6. 以 rune 數（不是 byte 數）檢查長度
//
// 空字串是否合法由呼叫端決定（title 必填、description 可空），本函式不判空。
func normalizeText(s string, maxRunes int, allowNewline bool) (string, error) {
	s = strings.TrimSpace(s)

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			if allowNewline {
				b.WriteRune('\n')
			} else {
				b.WriteRune(' ') // 不允許換行的欄位：換行降級成空白，不要把兩行黏成一個詞
			}
		case r == '\r':
			// 一律丟棄（\r\n 由上面的 \n 分支處理）
		case r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			// C0 / DEL / C1 控制字元
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			// 雙向控制字元（RLO/LRO/PDF/LRI/RLI/FSI/PDI）
		case r >= 0x200B && r <= 0x200D, r == 0xFEFF:
			// 零寬字元
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()

	if allowNewline {
		out = collapseNewlines(out)
	}
	out = strings.TrimSpace(out)

	if utf8.RuneCountInString(out) > maxRunes {
		return "", errTooLong
	}
	return out, nil
}

// collapseNewlines 把 3 個以上連續換行壓成 2 個（保留段落感，擋「整頁空白刷版面」）。
// 也順手把每行行尾空白去掉。
func collapseNewlines(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	var out []string
	blank := 0
	for _, ln := range lines {
		if ln == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// excerpt 取列表卡片用的摘要（規格 5.3：swrCache 單筆 100KB 上限，列表不得回完整 description）。
// 以 rune 計，超過就截斷加省略號；換行一律換成空白（卡片是單段落）。
func excerpt(s string, maxRunes int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(rs[:maxRunes])) + "…"
}
