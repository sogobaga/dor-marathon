// 指定帳號/Email 對象（migration 142）：管理員可貼上一份 email 清單，取代預設「全部玩家」。
// 邏輯拆成兩層方便測試：cleanRecipientEmails 是離線可測的純函式（trim/小寫/去重/格式驗證），
// resolveCustomRecipients 才碰 DB，把清洗過的 email 對到 users 並排除已退訂者（合規一致，比照
// listRecipients）。
package emailbroadcast

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// maxCustomRecipients 指定收件人模式：單次輸入筆數上限（防呆，避免貼上超巨大清單塞爆請求／
// 拖慢 DB 查詢；500 對電子報用途綽綽有餘，真的要寄給更多人應該用「全部玩家」）。
const maxCustomRecipients = 500

// emailFormatRe 寬鬆格式驗證：非空白/@ 的帳號部分 + @ + 非空白/@ 的網域部分 + 至少一個 "."。
// 只用來擋明顯打錯的輸入，真正「是否存在這個帳號」交給後面的 DB 比對。
var emailFormatRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// cleanRecipientEmails 清洗＋驗證管理員輸入的 email 清單：trim、轉小寫、去重（依輸入順序保留
// 第一次出現者）、基本格式驗證。回傳 valid（格式合法、值得拿去查 DB 的清單）與 invalid（格式
// 不合法，不必查 DB 就能斷定「查無會員」）。
func cleanRecipientEmails(raw []string) (valid, invalid []string) {
	seen := make(map[string]bool, len(raw))
	for _, e := range raw {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		if emailFormatRe.MatchString(e) {
			valid = append(valid, e)
		} else {
			invalid = append(invalid, e)
		}
	}
	return valid, invalid
}

// customAudienceLabel 存進 email_broadcasts.audience 的字串（見 migration 142）。
func customAudienceLabel(n int) string {
	return fmt.Sprintf("custom:%d人", n)
}

// customRecipientsResult resolveCustomRecipients 的查詢結果。
type customRecipientsResult struct {
	Recipients           []recipient // 實際寄送對象（比對到會員、且未退訂）
	NotFound             []string    // 格式不合法，或格式合法但查無此會員的 email（原樣小寫）
	UnsubscribedExcluded int         // 比對到會員、但已退訂而被排除的筆數
	MatchedCount         int         // 在 users 表比對到的筆數（不論是否已退訂）——用來判斷「一個都沒匹配」
}

// resolveCustomRecipients 依管理員輸入的 email 清單查詢寄送對象。「找到會員」與「未退訂」分開
// 判斷，才能精準回報 not_found（查無此會員）vs unsubscribed_excluded（有此會員但已退訂）兩種
// 不同情況給前台顯示。
func (h *Handler) resolveCustomRecipients(ctx context.Context, raw []string) (customRecipientsResult, error) {
	valid, invalid := cleanRecipientEmails(raw)
	result := customRecipientsResult{NotFound: append([]string{}, invalid...)}

	if len(valid) == 0 {
		return result, nil
	}

	rows, err := h.db.Query(ctx, `
		SELECT id::text, email, name, (id IN (SELECT user_id FROM email_unsubscribes)) AS is_unsub
		FROM users
		WHERE LOWER(email) = ANY($1)`, valid)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	matched := make(map[string]bool, len(valid))
	for rows.Next() {
		var rc recipient
		var isUnsub bool
		if err := rows.Scan(&rc.ID, &rc.Email, &rc.Name, &isUnsub); err != nil {
			return result, err
		}
		matched[strings.ToLower(rc.Email)] = true
		if isUnsub {
			result.UnsubscribedExcluded++
			continue
		}
		result.Recipients = append(result.Recipients, rc)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	result.MatchedCount = len(matched)
	for _, e := range valid {
		if !matched[e] {
			result.NotFound = append(result.NotFound, e)
		}
	}
	return result, nil
}
