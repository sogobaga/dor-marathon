// Package attribution 會員註冊來源歸因：判斷一筆新註冊是透過推廣連結、社群/搜尋外部來源、
// 還是直接進站。刻意獨立成 leaf 套件（比照 internal/referral），核心 Classify 為純函式（不做
// 任何 I/O），供 internal/auth 的 Register/LoginWithGoogle「新建用戶」路徑呼叫；I/O 包裝（解析
// referrer user id、寫入 user_signup_attribution）見同套件的 record.go。
package attribution

import (
	"net/url"
	"strings"
)

// 合法的 source 值（見 migrations/147_signup_attribution.sql 表註解）。
const (
	SourceReferral  = "referral"
	SourceFacebook  = "facebook"
	SourceInstagram = "instagram"
	SourceLine      = "line"
	SourceGoogle    = "google"
	SourceThreads   = "threads"
	SourceTikTok    = "tiktok"
	SourceX         = "x"
	SourceYoutube   = "youtube"
	SourceDcard     = "dcard"
	SourcePTT       = "ptt"
	SourceOther     = "other"
	SourceDirect    = "direct"
)

// ownDomains 视为「本站」的網域（referrer_url 命中這些不算外部來源，落回 direct）；
// dor.tw 與其子網域（www.dor.tw 等）皆算本站，比照 config.ECPayProdOrigins 的正式站網域。
var ownDomains = []string{"dor.tw"}

// Result 為 Classify 的分類結果。UTM 只含有值的鍵（source/medium/campaign），可能為空 map。
type Result struct {
	Source    string
	RefUserID string // 只在 Source==SourceReferral 時非空
	UTM       map[string]string
}

// Classify 依優先序判斷本次註冊來源，純函式、不做任何 I/O：
//  1. refUserID 非空（呼叫端已用既有 referrals 資料解析出推薦人）→ referral
//  2. 否則 landingURL 的 utm_source query 有值 → 依 utm_source 值對映（見 mapUTMSource）
//  3. 否則 referrerURL 的網域判斷（見 mapReferrerDomain），本站網域視同無 referrer
//  4. 都沒有 → direct
//
// utm 一律解析並回傳（不論最終走哪個分支），供呼叫端存檔留存 landing_url 帶的 utm_medium/
// campaign（即使該次分類是靠 referral 或 referrer 網域判斷）。
func Classify(refUserID, landingURL, referrerURL string) Result {
	utm := parseUTM(landingURL)

	if refUserID != "" {
		return Result{Source: SourceReferral, RefUserID: refUserID, UTM: utm}
	}

	if src := utm["source"]; src != "" {
		return Result{Source: mapUTMSource(src), UTM: utm}
	}

	if referrerURL != "" {
		if host := hostOf(referrerURL); host != "" && !isOwnDomain(host) {
			return Result{Source: mapReferrerDomain(host), UTM: utm}
		}
	}

	return Result{Source: SourceDirect, UTM: utm}
}

// parseUTM 解析 landingURL 的 utm_source/utm_medium/utm_campaign query 參數，僅保留 trim 後
// 非空的值。landingURL 可以是完整絕對網址、只有 path+query、或格式不良的字串——皆不視為錯誤，
// 解析失敗（url.Parse 出錯）或無對應參數一律回空 map。
func parseUTM(landingURL string) map[string]string {
	m := map[string]string{}
	if strings.TrimSpace(landingURL) == "" {
		return m
	}
	u, err := url.Parse(landingURL)
	if err != nil {
		return m
	}
	q := u.Query()
	if v := strings.TrimSpace(q.Get("utm_source")); v != "" {
		m["source"] = v
	}
	if v := strings.TrimSpace(q.Get("utm_medium")); v != "" {
		m["medium"] = v
	}
	if v := strings.TrimSpace(q.Get("utm_campaign")); v != "" {
		m["campaign"] = v
	}
	return m
}

// mapUTMSource 把 utm_source 原始值對映到標準 source（契約定案：僅列出的別名才對映，
// 其餘一律 other——原始值仍保留在 Result.UTM["source"]，不因對映為 other 而遺失）。
func mapUTMSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "fb", "facebook":
		return SourceFacebook
	case "ig", "instagram":
		return SourceInstagram
	case "google", "adwords":
		return SourceGoogle
	case "line":
		return SourceLine
	case "x", "twitter":
		return SourceX
	case "youtube":
		return SourceYoutube
	case "dcard":
		return SourceDcard
	case "ptt":
		return SourcePTT
	default:
		return SourceOther
	}
}

// hostOf 取出網址的 host（小寫，不含 port）；解析失敗或無 host 一律回空字串。
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// isOwnDomain 判斷 referrer 的 host 是否為本站（或本機開發環境），本站/本機一律不算外部來源。
func isOwnDomain(host string) bool {
	if host == "" || host == "localhost" || host == "127.0.0.1" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	for _, d := range ownDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// mapReferrerDomain 依 referrer host 網域對映 source（契約定案清單）；不在清單內的外部網域 → other。
func mapReferrerDomain(host string) string {
	switch {
	case strings.Contains(host, "google."):
		return SourceGoogle
	case matchesDomain(host, "facebook.com", "fb.watch", "fb.me"):
		return SourceFacebook
	case matchesDomain(host, "instagram.com"):
		return SourceInstagram
	case matchesDomain(host, "line.me"):
		return SourceLine
	case matchesDomain(host, "threads.net"):
		return SourceThreads
	case matchesDomain(host, "tiktok.com"):
		return SourceTikTok
	case matchesDomain(host, "x.com", "twitter.com", "t.co"):
		return SourceX
	case matchesDomain(host, "youtube.com", "youtu.be"):
		return SourceYoutube
	case matchesDomain(host, "dcard.tw"):
		return SourceDcard
	case matchesDomain(host, "ptt.cc"):
		return SourcePTT
	default:
		return SourceOther
	}
}

// matchesDomain 判斷 host 是否等於或為 domains 任一者的子網域。
func matchesDomain(host string, domains ...string) bool {
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
