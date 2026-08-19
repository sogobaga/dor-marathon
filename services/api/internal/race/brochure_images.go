package race

import (
	"encoding/json"
	"net/url"
	"strings"
)

// BrochureImageItem 簡章「圖片」區塊中，單張圖片的結構。
//
// race_brochure_blocks.content（block_type=image）歷史上有三種格式，讀寫都要相容：
//  1. 最舊：單一字串，就是圖片網址（如 "https://…/a.jpg"）。
//  2. 舊：JSON 陣列，元素是純字串網址（如 ["https://…/a.jpg","https://…/b.jpg"]）。
//  3. 新（2026-08 起）：JSON 陣列，元素可為物件 {"url":"…","caption":"…","link":"…"}，
//     支援每張圖各自的說明文字＋點擊連結；陣列中仍可混雜純字串元素（等同只有 url）。
//
// 一律用 ParseBrochureImages 讀、EncodeBrochureImages 寫，確保三種格式都能正確解析、
// 且寫回時一律正規化＋驗證，不會把舊資料寫壞。
type BrochureImageItem struct {
	URL     string `json:"url"`
	Caption string `json:"caption,omitempty"`
	Link    string `json:"link,omitempty"`
}

// UnmarshalJSON 讓陣列元素可以是純字串(舊格式，視為 url)或物件(新格式)。
func (b *BrochureImageItem) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		b.URL, b.Caption, b.Link = s, "", ""
		return nil
	}
	type alias BrochureImageItem
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*b = BrochureImageItem(a)
	return nil
}

// ParseBrochureImages 解析 image 區塊的 content，相容上述三種歷史格式。
// 解析失敗（不是合法 JSON 陣列）時回傳 nil，呼叫端應視同無圖片。
func ParseBrochureImages(content string) []BrochureImageItem {
	c := strings.TrimSpace(content)
	if c == "" {
		return nil
	}
	if strings.HasPrefix(c, "[") {
		var items []BrochureImageItem
		if err := json.Unmarshal([]byte(c), &items); err != nil {
			return nil
		}
		return items
	}
	return []BrochureImageItem{{URL: c}}
}

// EncodeBrochureImages 寫入前的正規化：去除空白 URL 的項目、trim 文字欄位、驗證
// link 的網址 scheme（見 sanitizeBrochureLink）。輸出固定為 JSON 陣列字串（可能是 "[]"）。
func EncodeBrochureImages(items []BrochureImageItem) string {
	out := make([]BrochureImageItem, 0, len(items))
	for _, it := range items {
		u := strings.TrimSpace(it.URL)
		if u == "" {
			continue
		}
		out = append(out, BrochureImageItem{
			URL:     u,
			Caption: strings.TrimSpace(it.Caption),
			Link:    sanitizeBrochureLink(it.Link),
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// sanitizeBrochureLink 驗證簡章圖片的點擊連結：僅允許 http/https 網址，或站內相對路徑
// （"/" 開頭且非 "//" protocol-relative）。比照 htmlsafe.AllowURLSchemes 的精神——擋
// javascript: data: 等危險 scheme；不合法一律清空（不整筆擋，僅該連結欄位失效)。
func sanitizeBrochureLink(raw string) string {
	link := strings.TrimSpace(raw)
	if link == "" {
		return ""
	}
	if strings.HasPrefix(link, "/") && !strings.HasPrefix(link, "//") {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return link
	default:
		return ""
	}
}
