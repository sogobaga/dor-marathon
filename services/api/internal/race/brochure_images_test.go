package race

import "testing"

// 向後相容：最舊格式(單一字串)、舊格式(純字串陣列)、新格式(物件陣列)都要能解析，不能 panic/出錯。
func TestParseBrochureImages_BackwardCompat(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string // 期望解析出的 URL 順序
	}{
		{"empty", "", nil},
		{"legacy single url", "https://img.example.com/a.jpg", []string{"https://img.example.com/a.jpg"}},
		{"legacy array of strings", `["https://img.example.com/a.jpg","https://img.example.com/b.jpg"]`, []string{"https://img.example.com/a.jpg", "https://img.example.com/b.jpg"}},
		{"new array of objects", `[{"url":"https://img.example.com/a.jpg","caption":"起點","link":"/event/abc"},{"url":"https://img.example.com/b.jpg"}]`, []string{"https://img.example.com/a.jpg", "https://img.example.com/b.jpg"}},
		{"mixed string+object", `["https://img.example.com/a.jpg",{"url":"https://img.example.com/b.jpg","caption":"終點"}]`, []string{"https://img.example.com/a.jpg", "https://img.example.com/b.jpg"}},
		{"invalid json falls back to empty", `[{"url":`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items := ParseBrochureImages(c.content)
			if len(items) != len(c.want) {
				t.Fatalf("got %d items, want %d (%+v)", len(items), len(c.want), items)
			}
			for i, w := range c.want {
				if items[i].URL != w {
					t.Errorf("item %d url = %q, want %q", i, items[i].URL, w)
				}
			}
		})
	}
}

// caption/link 要能正確解析出來（新格式）。
func TestParseBrochureImages_CaptionAndLink(t *testing.T) {
	items := ParseBrochureImages(`[{"url":"https://img.example.com/a.jpg","caption":"起點合影","link":"https://dor.tw/event/abc"}]`)
	if len(items) != 1 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0].Caption != "起點合影" {
		t.Errorf("caption = %q", items[0].Caption)
	}
	if items[0].Link != "https://dor.tw/event/abc" {
		t.Errorf("link = %q", items[0].Link)
	}
}

// EncodeBrochureImages：空白 URL 的項目要被濾掉，文字欄位要 trim。
func TestEncodeBrochureImages_FiltersEmptyAndTrims(t *testing.T) {
	got := EncodeBrochureImages([]BrochureImageItem{
		{URL: "  https://img.example.com/a.jpg  ", Caption: " 說明 ", Link: " /event/abc "},
		{URL: "   "}, // 空白 URL 應被濾掉
		{URL: "https://img.example.com/b.jpg"},
	})
	items := ParseBrochureImages(got)
	if len(items) != 2 {
		t.Fatalf("got %d items after encode round-trip, want 2 (%s)", len(items), got)
	}
	if items[0].URL != "https://img.example.com/a.jpg" || items[0].Caption != "說明" || items[0].Link != "/event/abc" {
		t.Errorf("item0 mismatch: %+v", items[0])
	}
	if items[1].URL != "https://img.example.com/b.jpg" || items[1].Caption != "" || items[1].Link != "" {
		t.Errorf("item1 mismatch: %+v", items[1])
	}
}

// 危險 scheme（javascript: 等）的連結必須被清空，不可寫入 DB；http/https/相對路徑允許通過。
func TestSanitizeBrochureLink(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"https://dor.tw/event/abc", "https://dor.tw/event/abc"},
		{"http://example.com/x", "http://example.com/x"},
		{"/event/abc?x=1#y", "/event/abc?x=1#y"},
		{"javascript:alert(1)", ""},
		{"data:text/html;base64,abc", ""},
		{"  javascript:alert(1)  ", ""},
		{"//evil.example.com/phish", ""}, // protocol-relative，視為外部危險路徑，擋下
	}
	for _, c := range cases {
		if got := sanitizeBrochureLink(c.in); got != c.want {
			t.Errorf("sanitizeBrochureLink(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
