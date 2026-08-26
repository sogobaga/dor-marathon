package attribution

import (
	"reflect"
	"testing"
)

// TestClassify_Priority 覆蓋契約定案的優先序：referral > utm_source > referrer 網域 > direct。
func TestClassify_Priority(t *testing.T) {
	cases := []struct {
		name        string
		refUserID   string
		landingURL  string
		referrerURL string
		wantSource  string
		wantRefID   string
	}{
		{
			name:        "有推薦人 → referral，優先於 utm/referrer",
			refUserID:   "user-abc",
			landingURL:  "https://www.dor.tw/?utm_source=facebook",
			referrerURL: "https://www.instagram.com/",
			wantSource:  SourceReferral,
			wantRefID:   "user-abc",
		},
		{
			name:        "無推薦人但有 utm_source → 依 utm 對映，優先於 referrer 網域",
			refUserID:   "",
			landingURL:  "https://www.dor.tw/?utm_source=google",
			referrerURL: "https://www.facebook.com/",
			wantSource:  SourceGoogle,
		},
		{
			name:        "無推薦人無 utm_source → 依 referrer 網域判斷",
			refUserID:   "",
			landingURL:  "https://www.dor.tw/",
			referrerURL: "https://www.instagram.com/reel/xyz",
			wantSource:  SourceInstagram,
		},
		{
			name:        "都沒有 → direct",
			refUserID:   "",
			landingURL:  "",
			referrerURL: "",
			wantSource:  SourceDirect,
		},
		{
			name:        "landing_url 完全沒有 query → direct（無 referrer）",
			refUserID:   "",
			landingURL:  "https://www.dor.tw/register",
			referrerURL: "",
			wantSource:  SourceDirect,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.refUserID, c.landingURL, c.referrerURL)
			if got.Source != c.wantSource {
				t.Fatalf("Source = %q, want %q", got.Source, c.wantSource)
			}
			if got.RefUserID != c.wantRefID {
				t.Fatalf("RefUserID = %q, want %q", got.RefUserID, c.wantRefID)
			}
		})
	}
}

// TestClassify_UTMSourceMapping 覆蓋契約列出的別名對映；未列出的值一律 other 但保留原值。
func TestClassify_UTMSourceMapping(t *testing.T) {
	cases := []struct {
		utmSource  string
		wantSource string
	}{
		{"fb", SourceFacebook},
		{"facebook", SourceFacebook},
		{"FACEBOOK", SourceFacebook}, // 大小寫不敏感
		{"ig", SourceInstagram},
		{"instagram", SourceInstagram},
		{"google", SourceGoogle},
		{"adwords", SourceGoogle},
		{"AdWords", SourceGoogle},
		{"line", SourceLine},
		{"newsletter", SourceOther}, // 未列出的別名 → other
		{"threads", SourceOther},    // 契約明定：utm_source 只有列出的四種對映，其餘一律 other
		{"tiktok", SourceOther},
		{"unknown-x", SourceOther},
	}
	for _, c := range cases {
		t.Run(c.utmSource, func(t *testing.T) {
			landing := "https://www.dor.tw/?utm_source=" + c.utmSource
			got := Classify("", landing, "")
			if got.Source != c.wantSource {
				t.Fatalf("utm_source=%q → Source = %q, want %q", c.utmSource, got.Source, c.wantSource)
			}
			// other 對映仍須保留原始值（未被正規化覆蓋）。
			if got.UTM["source"] != c.utmSource {
				t.Fatalf("UTM[source] = %q, want original %q", got.UTM["source"], c.utmSource)
			}
		})
	}
}

// TestClassify_ReferrerDomainMapping 覆蓋契約列出的 referrer 網域對映清單。
func TestClassify_ReferrerDomainMapping(t *testing.T) {
	cases := []struct {
		name        string
		referrerURL string
		wantSource  string
	}{
		{"google.com", "https://www.google.com/search?q=dor", SourceGoogle},
		{"google.com.tw 子網域也算 google.*", "https://www.google.com.tw/", SourceGoogle},
		{"facebook.com", "https://www.facebook.com/somepage", SourceFacebook},
		{"m.facebook.com 子網域", "https://m.facebook.com/somepage", SourceFacebook},
		{"fb.watch", "https://fb.watch/abc123/", SourceFacebook},
		{"fb.me", "https://fb.me/abc", SourceFacebook},
		{"instagram.com", "https://www.instagram.com/dor.tw/", SourceInstagram},
		{"line.me", "https://line.me/R/ti/p/xyz", SourceLine},
		{"threads.net", "https://www.threads.net/@dor", SourceThreads},
		{"tiktok.com", "https://www.tiktok.com/@dor/video/123", SourceTikTok},
		{"其他外部網域 → other", "https://news.example.com/article", SourceOther},
		{"帶 port 的網域仍正確判斷", "https://www.facebook.com:443/somepage", SourceFacebook},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify("", "", c.referrerURL)
			if got.Source != c.wantSource {
				t.Fatalf("referrer=%q → Source = %q, want %q", c.referrerURL, got.Source, c.wantSource)
			}
		})
	}
}

// TestClassify_OwnDomainIsDirect 本站網域（含子網域）當 referrer 視同無 referrer → direct，
// 不落入 other。
func TestClassify_OwnDomainIsDirect(t *testing.T) {
	cases := []string{
		"https://dor.tw/races",
		"https://www.dor.tw/races",
		"https://admin.dor.tw/login",
		"http://localhost:3000/",
		"http://127.0.0.1:3000/",
	}
	for _, referrer := range cases {
		t.Run(referrer, func(t *testing.T) {
			got := Classify("", "", referrer)
			if got.Source != SourceDirect {
				t.Fatalf("referrer=%q → Source = %q, want %q", referrer, got.Source, SourceDirect)
			}
		})
	}
}

// TestClassify_BadInput 壞輸入（空字串/格式不良網址/純空白）皆不應 panic，一律優雅落回 direct
// 或 other，不因解析失敗而誤判成 referral/facebook 等。
func TestClassify_BadInput(t *testing.T) {
	cases := []struct {
		name        string
		refUserID   string
		landingURL  string
		referrerURL string
		wantSource  string
	}{
		{"全部空字串 → direct", "", "", "", SourceDirect},
		// 注意：函式本身只判斷 refUserID 是否為空字串，不 trim——信任呼叫端已解析好合法值。
		{"refUserID 非空字串（即使純空白）仍視為已解析推薦人", "   ", "", "", SourceReferral},
		{"landing_url 格式不良（控制字元）→ 不 panic，落回 direct", "", "://not a url\x00", "", SourceDirect},
		{"referrer_url 格式不良 → 不 panic，落回 direct", "", "", "://not a url\x00", SourceDirect},
		{"referrer_url 無 scheme（無 host 可解析）→ 落回 direct", "", "", "www.facebook.com", SourceDirect},
		{"landing_url 只有 query 沒有 host → utm 仍可解析", "", "?utm_source=line", "", SourceLine},
		{"utm_source 純空白視為未帶 utm → 落回 direct", "", "https://www.dor.tw/?utm_source=%20%20", "", SourceDirect},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.refUserID, c.landingURL, c.referrerURL)
			if got.Source != c.wantSource {
				t.Fatalf("Classify(%q,%q,%q).Source = %q, want %q", c.refUserID, c.landingURL, c.referrerURL, got.Source, c.wantSource)
			}
		})
	}
}

// TestClassify_UTMOnlyPresentKeysStored 驗證「utm JSONB 存 {source,medium,campaign} 有值才存」：
// 缺席的鍵不應出現在回傳的 map 裡（而非存成空字串）。
func TestClassify_UTMOnlyPresentKeysStored(t *testing.T) {
	got := Classify("", "https://www.dor.tw/?utm_source=facebook&utm_campaign=spring2026", "")
	want := map[string]string{"source": "facebook", "campaign": "spring2026"}
	if !reflect.DeepEqual(got.UTM, want) {
		t.Fatalf("UTM = %#v, want %#v", got.UTM, want)
	}

	none := Classify("", "https://www.dor.tw/register", "")
	if len(none.UTM) != 0 {
		t.Fatalf("UTM = %#v, want empty map", none.UTM)
	}
}

// TestClassify_ReferralIgnoresUTMButStillParsesIt referral 分支優先於 utm 分類，但 utm 仍應
// 一併解析回傳（供呼叫端存檔留存，即使不影響 source 判斷）。
func TestClassify_ReferralIgnoresUTMButStillParsesIt(t *testing.T) {
	got := Classify("referrer-id", "https://www.dor.tw/?utm_source=google&utm_medium=cpc", "")
	if got.Source != SourceReferral {
		t.Fatalf("Source = %q, want %q", got.Source, SourceReferral)
	}
	if got.RefUserID != "referrer-id" {
		t.Fatalf("RefUserID = %q, want %q", got.RefUserID, "referrer-id")
	}
	want := map[string]string{"source": "google", "medium": "cpc"}
	if !reflect.DeepEqual(got.UTM, want) {
		t.Fatalf("UTM = %#v, want %#v", got.UTM, want)
	}
}
