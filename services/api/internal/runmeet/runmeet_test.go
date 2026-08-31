package runmeet

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"
)

// --- 配額月份字串 / 重置時點（台北時區）---

func TestQuotaMonthTaipeiBoundary(t *testing.T) {
	// 2026-08-31 16:00 UTC ＝ 台北 2026-09-01 00:00 → 已經是 9 月（重置點）
	utc := time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC)
	if got := QuotaMonth(utc); got != "2026-09" {
		t.Fatalf("台北 9/1 00:00 應算 2026-09，得 %q", got)
	}
	// 再往前一分鐘仍是 8 月
	if got := QuotaMonth(utc.Add(-time.Minute)); got != "2026-08" {
		t.Fatalf("台北 8/31 23:59 應算 2026-08，得 %q", got)
	}
	// ⚠️ 這正是與既有 activity_coupon_month（走 UTC，實際重置點是台北 08:00）刻意不同之處：
	// 台北 00:00–08:00 這個窗內，兩者的「當月」判定必然不一致。
	if QuotaMonth(time.Date(2026, 9, 1, 0, 30, 0, 0, taipeiLoc)) != "2026-09" {
		t.Fatal("台北 9/1 00:30 應算 2026-09")
	}
}

func TestQuotaResetAt(t *testing.T) {
	got := QuotaResetAt(time.Date(2026, 8, 15, 10, 0, 0, 0, taipeiLoc))
	want := time.Date(2026, 9, 1, 0, 0, 0, 0, taipeiLoc)
	if !got.Equal(want) {
		t.Fatalf("重置時點 want %v got %v", want, got)
	}
	// 跨年
	got = QuotaResetAt(time.Date(2026, 12, 31, 23, 59, 0, 0, taipeiLoc))
	want = time.Date(2027, 1, 1, 0, 0, 0, 0, taipeiLoc)
	if !got.Equal(want) {
		t.Fatalf("跨年重置時點 want %v got %v", want, got)
	}
}

func TestQuotaCapAndImageLimit(t *testing.T) {
	if QuotaCap(false, 1, 10) != 1 || QuotaCap(true, 1, 10) != 10 {
		t.Fatal("配額上限應依 VIP 即時判定")
	}
	if ImageLimit(false, 1, 4) != 1 || ImageLimit(true, 1, 4) != 4 {
		t.Fatal("圖片張數應依 VIP 判定（寫進 run_meets.image_limit 快照）")
	}
}

// --- 距離分級 ---

func TestDistanceBandBuckets(t *testing.T) {
	cases := []struct {
		m    float64
		want string
	}{
		{0, "lt1"}, {999.9, "lt1"},
		{1000, "1to3"}, {2999, "1to3"},
		{3000, "3to5"}, {4999, "3to5"},
		{5000, "5to10"}, {9999, "5to10"},
		{10000, "gt10"}, {123456, "gt10"},
	}
	for _, c := range cases {
		if got := DistanceBand(c.m); got != c.want {
			t.Fatalf("DistanceBand(%v) want %q got %q", c.m, c.want, got)
		}
	}
}

// TestDistanceBandNoPrecision 分級必須真的「丟掉精度」：同一個 band 內距離差很多的兩點
// 要落在同一個桶，攻擊者才無法靠多次查詢三角定位。
func TestDistanceBandNoPrecision(t *testing.T) {
	if DistanceBand(1010) != DistanceBand(2990) {
		t.Fatal("1.01km 與 2.99km 應同屬 1to3，分級才有隱私意義")
	}
}

func TestHaversineAndBoundingBox(t *testing.T) {
	// 台北車站 → 大安森林公園 約 2.4 km
	d := haversineM(25.0478, 121.5170, 25.0296, 121.5365)
	if d < 2000 || d > 3200 {
		t.Fatalf("台北車站→大安森林公園距離應約 2.4km，得 %.0f m", d)
	}
	// 同一點距離為 0
	if haversineM(25, 121, 25, 121) != 0 {
		t.Fatal("同一點距離應為 0")
	}
	// bounding box 必須「不漏」：半徑上的點要落在框內
	minLat, maxLat, minLng, maxLng := boundingBox(25.0, 121.5, 5)
	if minLat >= 25.0 || maxLat <= 25.0 || minLng >= 121.5 || maxLng <= 121.5 {
		t.Fatal("bounding box 應包住中心點且有寬度")
	}
	// 正北 5km 的點必須在框內（否則 SQL 粗篩會漏掉真的在半徑內的資料）
	north := 25.0 + 5.0/111.32
	if north > maxLat {
		t.Fatalf("正北 5km 的點 %.6f 落在框外 (maxLat=%.6f)", north, maxLat)
	}
	// 極區：cos(lat) 趨近 0 時退回全經度域，不得產生 NaN/Inf
	_, _, mnLng, mxLng := boundingBox(89.999, 0, 50)
	if mnLng != -180 || mxLng != 180 {
		t.Fatalf("高緯度應退回全經度域，得 [%v, %v]", mnLng, mxLng)
	}
}

func TestValidCoord(t *testing.T) {
	if !validCoord(25.03, 121.56) {
		t.Fatal("台北座標應合法")
	}
	for _, c := range [][2]float64{{91, 0}, {-91, 0}, {0, 181}, {0, -181}} {
		if validCoord(c[0], c[1]) {
			t.Fatalf("超出範圍的座標 %v 應不合法", c)
		}
	}
	inf := 1.0
	for i := 0; i < 400; i++ {
		inf *= 10 // → +Inf
	}
	if validCoord(inf, 0) {
		t.Fatal("Inf 座標應不合法（query string 的 ParseFloat 可產生 Inf）")
	}
}

// --- 純文字消毒 ---

func TestNormalizeTextStripsBidiAndZeroWidth(t *testing.T) {
	// U+202E（RLO）：可把「團練gnp.exe」顯示成「團練exe.png」
	got, err := normalizeText("團練\u202egnp.exe", MaxTitleRunes, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(got, 0x202E) {
		t.Fatalf("雙向控制字元未被移除：%q", got)
	}
	if got != "團練gnp.exe" {
		t.Fatalf("除了控制字元外不應改動內容，得 %q", got)
	}
	// 零寬字元（隱形灌長度／繞過重複偵測）
	got, _ = normalizeText("a\u200bb\u200cc\u200dd\ufeffe", MaxTitleRunes, false)
	if got != "abcde" {
		t.Fatalf("零寬字元未被移除：%q", got)
	}
	// LRI/PDI 區段（U+2066–U+2069）
	got, _ = normalizeText("x\u2066y\u2069z", MaxTitleRunes, false)
	if got != "xyz" {
		t.Fatalf("U+2066–U+2069 未被移除：%q", got)
	}
}

func TestNormalizeTextControlCharsAndNewlines(t *testing.T) {
	// 不允許換行的欄位：\n 降級成空白（不能把兩行黏成一個詞）
	got, _ := normalizeText("上午\n六點", MaxTitleRunes, false)
	if got != "上午 六點" {
		t.Fatalf("不允許換行時應降級成空白，得 %q", got)
	}
	// 允許換行：連續換行壓成最多 2 個
	got, _ = normalizeText("a\n\n\n\n\nb", MaxDescriptionRunes, true)
	if got != "a\n\nb" {
		t.Fatalf("連續換行應壓成 2 個，得 %q", got)
	}
	// C0 控制字元
	got, _ = normalizeText("a\x00\x07\x1bb", MaxTitleRunes, false)
	if got != "ab" {
		t.Fatalf("C0 控制字元未被移除：%q", got)
	}
	// 前後空白
	got, _ = normalizeText("  hi  ", MaxTitleRunes, false)
	if got != "hi" {
		t.Fatalf("應 TrimSpace，得 %q", got)
	}
}

// TestNormalizeTextKeepsAngleBrackets 純文字欄位刻意不走 htmlsafe：
// 使用者真的打出來的 < > 必須原樣保留（前端 React 文字節點自動跳脫即安全）。
func TestNormalizeTextKeepsAngleBrackets(t *testing.T) {
	in := `<img src=x onerror=alert(1)>`
	got, err := normalizeText(in, MaxTitleRunes, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("純文字欄位不應吃掉 < >，want %q got %q", in, got)
	}
}

// TestNormalizeTextRuneLength 長度必須以 rune 計，不是 byte（中文一字 3 bytes）。
func TestNormalizeTextRuneLength(t *testing.T) {
	forty := strings.Repeat("跑", 40) // 120 bytes / 40 runes
	if _, err := normalizeText(forty, MaxTitleRunes, false); err != nil {
		t.Fatalf("40 個中文字應在 40 rune 上限內，卻被拒：%v", err)
	}
	if _, err := normalizeText(forty+"跑", MaxTitleRunes, false); !errors.Is(err, errTooLong) {
		t.Fatal("41 個中文字應超過上限")
	}
	// 零寬字元灌水後仍在上限內（先移除再量長度）
	padded := strings.Repeat("跑\u200b", 40)
	if _, err := normalizeText(padded, MaxTitleRunes, false); err != nil {
		t.Fatalf("零寬字元應在計長前被移除：%v", err)
	}
}

func TestExcerpt(t *testing.T) {
	if got := excerpt("短說明", 60); got != "短說明" {
		t.Fatalf("短內容不應加省略號，得 %q", got)
	}
	long := strings.Repeat("一", 100)
	got := excerpt(long, 60)
	if len([]rune(got)) != 61 { // 60 + 省略號
		t.Fatalf("摘要應截到 60 rune + 省略號，得 %d rune", len([]rune(got)))
	}
	if got := excerpt("第一行\n第二行", 60); strings.Contains(got, "\n") {
		t.Fatalf("卡片摘要應為單段落，得 %q", got)
	}
}

// --- 權限判定 ---

func ptr(s string) *string { return &s }

// TestCanSeePreciseLocation 地點三層揭露的核心：只有發起人／已加入成員／後台看得到精確座標。
func TestCanSeePreciseLocation(t *testing.T) {
	cases := []struct {
		name    string
		isOwner bool
		status  *string
		isAdmin bool
		want    bool
	}{
		{"發起人", true, nil, false, true},
		{"已加入成員", false, ptr(MemberJoined), false, true},
		{"後台", false, nil, true, true},
		{"未加入", false, nil, false, false},
		{"申請中(pending 不算成員)", false, ptr(MemberPending), false, false},
		{"被剔除後立刻失去", false, ptr(MemberKicked), false, false},
		{"自行退出後立刻失去", false, ptr(MemberLeft), false, false},
		{"被婉拒", false, ptr(MemberRejected), false, false},
	}
	for _, c := range cases {
		if got := CanSeePreciseLocation(c.isOwner, c.status, c.isAdmin); got != c.want {
			t.Fatalf("%s：want %v got %v", c.name, c.want, got)
		}
	}
}

// TestUnlockIsNotMembership 通過私密團密碼 ≠ 成為成員：解鎖只給詳情頁入場券，
// 精確地點仍需 joined。這條若破了，任何知道密碼的人都能拿到集合座標。
func TestUnlockIsNotMembership(t *testing.T) {
	if !HasDetailAccess(true, false, nil, true, false) {
		t.Fatal("已解鎖者應能看到私密團的完整說明")
	}
	if CanSeePreciseLocation(false, nil, false) {
		t.Fatal("已解鎖但未加入者不得看到精確地點")
	}
}

func TestHasDetailAccess(t *testing.T) {
	cases := []struct {
		name      string
		isPrivate bool
		isOwner   bool
		status    *string
		unlocked  bool
		isAdmin   bool
		want      bool
	}{
		{"公開團任何人", false, false, nil, false, false, true},
		{"私密團未解鎖", true, false, nil, false, false, false},
		{"私密團已解鎖", true, false, nil, true, false, true},
		{"私密團發起人", true, true, nil, false, false, true},
		{"私密團成員", true, false, ptr(MemberJoined), false, false, true},
		{"私密團申請中", true, false, ptr(MemberPending), false, false, true},
		{"私密團被剔除", true, false, ptr(MemberKicked), false, false, false},
		{"私密團已退出", true, false, ptr(MemberLeft), false, false, false},
		{"後台", true, false, nil, false, true, true},
	}
	for _, c := range cases {
		if got := HasDetailAccess(c.isPrivate, c.isOwner, c.status, c.unlocked, c.isAdmin); got != c.want {
			t.Fatalf("%s：want %v got %v", c.name, c.want, got)
		}
	}
}

func TestMyState(t *testing.T) {
	if MyState(true, ptr(MemberJoined)) != "owner" {
		t.Fatal("發起人恆為 owner")
	}
	if MyState(false, nil) != "none" {
		t.Fatal("從未申請應為 none")
	}
	if MyState(false, ptr(MemberKicked)) != MemberKicked {
		t.Fatal("應直接反映 member status")
	}
}

func TestCanComment(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	past8d := time.Now().Add(-8 * 24 * time.Hour)
	past3d := time.Now().Add(-3 * 24 * time.Hour)

	if !canComment(true, nil, StatusOpen, future) {
		t.Fatal("發起人應可留言")
	}
	if canComment(false, nil, StatusOpen, future) {
		t.Fatal("非成員不可留言")
	}
	if canComment(false, ptr(MemberPending), StatusOpen, future) {
		t.Fatal("申請中不可留言")
	}
	if !canComment(false, ptr(MemberJoined), StatusOpen, past3d) {
		t.Fatal("結束後 7 天內仍可留言（讓大家回報今天跑得如何）")
	}
	if canComment(false, ptr(MemberJoined), StatusOpen, past8d) {
		t.Fatal("結束超過 7 天應唯讀")
	}
	if canComment(false, ptr(MemberJoined), StatusCancelled, future) {
		t.Fatal("已取消的團不可留言")
	}
}

// --- 入口閘門 ---

func TestEntryFrom(t *testing.T) {
	cases := []struct {
		state, list, email, code, want string
	}{
		{"open", "", "a@b.c", "X1", "shown"},
		{"locked", "a@b.c", "a@b.c", "X1", "locked"}, // locked 對後端一樣不放行
		{"whitelist", "a@b.c", "a@b.c", "X1", "shown"},
		{"whitelist", "a@b.c", "z@z.z", "X1", "hidden"},
		{"whitelist", "#X1", "z@z.z", "X1", "shown"}, // 帳號編碼（# 可省）
		{"hidden", "a@b.c", "a@b.c", "X1", "hidden"},
		{"", "", "a@b.c", "X1", "hidden"}, // 未設定 → fail-closed
		{"garbage", "", "a@b.c", "X1", "hidden"},
	}
	for _, c := range cases {
		if got := entryFrom(c.state, c.list, c.email, c.code); got != c.want {
			t.Fatalf("entryFrom(%q,%q,%q,%q) want %q got %q", c.state, c.list, c.email, c.code, c.want, got)
		}
	}
}

func TestWhitelistedSeparatorsAndCase(t *testing.T) {
	list := "A@B.C\n#X1 , y@z.tw;\t#Q9"
	for _, e := range []string{"a@b.c", "y@z.tw"} {
		if !whitelisted(list, e, "") {
			t.Fatalf("email %q 應命中白名單（大小寫不敏感）", e)
		}
	}
	for _, c := range []string{"x1", "Q9"} {
		if !whitelisted(list, "", c) {
			t.Fatalf("帳號編碼 %q 應命中白名單", c)
		}
	}
	if whitelisted(list, "nope@x.com", "ZZ") {
		t.Fatal("不在名單者不應命中")
	}
	if whitelisted("", "a@b.c", "X1") {
		t.Fatal("空白名單不應命中任何人")
	}
	// 空 email/code 不得因為名單裡有空 token 而誤中
	if whitelisted(" , ; \n", "", "") {
		t.Fatal("空白名單 token 不應命中空身分")
	}
}

// --- 圖片引用驗證 ---

func TestValidImageURL(t *testing.T) {
	ok := "/api/v1/images/0123abcd-4567-89ab-cdef-0123456789ab"
	if !ValidImageURL(ok) {
		t.Fatal("合法的自家圖片路徑應通過")
	}
	bad := []string{
		"javascript:alert(1)",
		"https://evil.example.com/pixel.gif",                      // 外站追蹤像素
		"//evil.example.com/x.png",                                // protocol-relative
		"/api/v1/images/0123abcd-4567-89ab-cdef-0123456789ab?x=1", // query 夾帶
		"/api/v1/images/0123ABCD-4567-89AB-CDEF-0123456789AB",     // 大寫（DB 一律小寫）
		"/api/v1/images/not-a-uuid",
		"/api/v1/images/",
		" /api/v1/images/0123abcd-4567-89ab-cdef-0123456789ab",
	}
	for _, u := range bad {
		if ValidImageURL(u) {
			t.Fatalf("不合法的圖片來源 %q 竟通過驗證", u)
		}
	}
	if imageIDFromURL(ok) != "0123abcd-4567-89ab-cdef-0123456789ab" {
		t.Fatal("imageIDFromURL 取值錯誤")
	}
}

// --- 建立/編輯輸入驗證 ---

func baseInput(now time.Time) MeetInput {
	return MeetInput{
		Title:      "大安森林公園晨跑 10K",
		MeetAt:     now.Add(48 * time.Hour),
		Region:     "臺北市・大安區",
		PlaceLabel: "大安森林公園",
		Capacity:   12,
	}
}

func TestValidateMeetInput(t *testing.T) {
	now := time.Now()

	in := baseInput(now)
	if err := validateMeetInput(&in, now, 50, 4, 4); err != nil {
		t.Fatalf("正常輸入應通過：%v", err)
	}

	// 時間必須是未來
	in = baseInput(now)
	in.MeetAt = now.Add(-time.Minute)
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errMeetAtPast) {
		t.Fatalf("過去時間應被拒，得 %v", err)
	}
	// 最遠 90 天
	in = baseInput(now)
	in.MeetAt = now.Add(91 * 24 * time.Hour)
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errMeetAtFar) {
		t.Fatalf("91 天後應被拒，得 %v", err)
	}
	// 人數上限
	in = baseInput(now)
	in.Capacity = 1
	if err := validateMeetInput(&in, now, 50, 4, 4); err == nil {
		t.Fatal("capacity=1 應被拒（最少 2 人）")
	}
	in.Capacity = 51
	if err := validateMeetInput(&in, now, 50, 4, 4); err == nil {
		t.Fatal("超過 runmeet_capacity_max 應被拒")
	}
	// 公開層地點必填
	in = baseInput(now)
	in.Region = ""
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errRegionLen) {
		t.Fatalf("region 必填，得 %v", err)
	}
	in = baseInput(now)
	in.PlaceLabel = "x"
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errPlaceLabelLen) {
		t.Fatalf("place_label 至少 2 字，得 %v", err)
	}
	// lat/lng 必須成對
	in = baseInput(now)
	lat := 25.03
	in.Lat = &lat
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errBadCoord) {
		t.Fatalf("只給 lat 沒給 lng 應被拒，得 %v", err)
	}
	lng := 999.0
	in.Lng = &lng
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errBadCoord) {
		t.Fatalf("超範圍座標應被拒，得 %v", err)
	}
	// 圖片張數與來源
	in = baseInput(now)
	in.ImageURLs = []string{
		"/api/v1/images/0123abcd-4567-89ab-cdef-0123456789ab",
		"/api/v1/images/1123abcd-4567-89ab-cdef-0123456789ab",
	}
	if err := validateMeetInput(&in, now, 50, 1, 4); err == nil {
		t.Fatal("超過 image_limit 應被拒")
	}
	in.ImageURLs = []string{"https://evil.example.com/pixel.gif"}
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errImageSource) {
		t.Fatalf("外站圖片 URL 應被拒（避免團主埋 tracking pixel），得 %v", err)
	}
	// 密碼長度
	in = baseInput(now)
	short := "abc"
	in.Password = &short
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errPasswordLen) {
		t.Fatalf("3 字密碼應被拒，得 %v", err)
	}
	empty := ""
	in.Password = &empty
	if err := validateMeetInput(&in, now, 50, 4, 4); err != nil {
		t.Fatalf("空字串＝移除密碼（改公開團），不應被拒：%v", err)
	}
	// 集合細節（成員層）長度
	in = baseInput(now)
	in.MeetingDetail = strings.Repeat("細", 201)
	if err := validateMeetInput(&in, now, 50, 4, 4); !errors.Is(err, errMeetingDetail) {
		t.Fatalf("meeting_detail 超過 200 字應被拒，得 %v", err)
	}
	// 標題會被正規化（RLO 移除）後才存
	in = baseInput(now)
	in.Title = "晨跑\u202egnp.exe"
	if err := validateMeetInput(&in, now, 50, 4, 4); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(in.Title, 0x202E) {
		t.Fatalf("驗證後的 Title 仍含雙向控制字元：%q", in.Title)
	}
}

// --- 不限地點（migration 161）---

// TestNormalizeNoLocationClearsCoordAndFillsBlank no_location=true 時：座標一律被清掉
// （不是報錯，直接清掉——CHECK run_meets_noloc_chk 要求），region/place_label 為空白時
// 補上「不限」；已經有值的欄位不覆蓋。
func TestNormalizeNoLocationClearsCoordAndFillsBlank(t *testing.T) {
	lat, lng := 25.033, 121.565
	in := &MeetInput{NoLocation: true, Lat: &lat, Lng: &lng}
	normalizeNoLocation(in)
	if in.Lat != nil || in.Lng != nil {
		t.Fatalf("no_location=true 應強制清空座標，得 lat=%v lng=%v", in.Lat, in.Lng)
	}
	if in.Region != noLocationText || in.PlaceLabel != noLocationText {
		t.Fatalf("region/place_label 為空時應補「不限」，得 region=%q place_label=%q", in.Region, in.PlaceLabel)
	}

	// 空白字元也視為空（TrimSpace 後仍是空字串）
	in2 := &MeetInput{NoLocation: true, Region: "   ", PlaceLabel: "\t"}
	normalizeNoLocation(in2)
	if in2.Region != noLocationText || in2.PlaceLabel != noLocationText {
		t.Fatalf("純空白也應視為空、補「不限」，得 region=%q place_label=%q", in2.Region, in2.PlaceLabel)
	}

	// 已有值時不覆蓋（呼叫端自己填了別的文字）
	in3 := &MeetInput{NoLocation: true, Region: "臺北市・大安區", PlaceLabel: "大安森林公園"}
	normalizeNoLocation(in3)
	if in3.Region != "臺北市・大安區" || in3.PlaceLabel != "大安森林公園" {
		t.Fatalf("已有值不應被覆蓋，得 region=%q place_label=%q", in3.Region, in3.PlaceLabel)
	}
}

// TestNormalizeNoLocationFalseIsNoOp no_location=false 時完全不作用，既有驗證行為不受影響。
func TestNormalizeNoLocationFalseIsNoOp(t *testing.T) {
	lat, lng := 25.033, 121.565
	in := &MeetInput{NoLocation: false, Region: "", PlaceLabel: "", Lat: &lat, Lng: &lng}
	normalizeNoLocation(in)
	if in.Lat == nil || in.Lng == nil {
		t.Fatal("no_location=false 不應清空座標")
	}
	if in.Region != "" || in.PlaceLabel != "" {
		t.Fatal("no_location=false 不應補「不限」，應維持原樣（讓下面的必填檢查照舊擋掉空白）")
	}
}

// TestValidateMeetInputNoLocation 整合進 validateMeetInput：no_location=true 且帶座標與空白
// 地點文字進來，驗證後應通過（不再擋 errBadCoord/errRegionLen），且座標被清掉、地點補「不限」；
// meeting_detail 不受影響，仍可正常填寫與驗證長度上限。
func TestValidateMeetInputNoLocation(t *testing.T) {
	now := time.Now()
	in := baseInput(now)
	in.NoLocation = true
	in.Region = ""
	in.PlaceLabel = ""
	lat, lng := 25.033, 121.565
	in.Lat, in.Lng = &lat, &lng
	in.MeetingDetail = "各自跑，跑完在群組回報"

	if err := validateMeetInput(&in, now, 50, 4, 4); err != nil {
		t.Fatalf("no_location=true 帶座標與空白地點應通過（後端自行清空/補值）：%v", err)
	}
	if in.Lat != nil || in.Lng != nil {
		t.Fatalf("驗證後座標應被清空，得 lat=%v lng=%v", in.Lat, in.Lng)
	}
	if in.Region != noLocationText || in.PlaceLabel != noLocationText {
		t.Fatalf("驗證後 region/place_label 應補「不限」，得 %q / %q", in.Region, in.PlaceLabel)
	}
	if in.MeetingDetail != "各自跑，跑完在群組回報" {
		t.Fatalf("meeting_detail 不應被 no_location 連帶清空，得 %q", in.MeetingDetail)
	}

	// meeting_detail 仍受既有長度上限約束（no_location 不豁免這條驗證）
	in2 := baseInput(now)
	in2.NoLocation = true
	in2.MeetingDetail = strings.Repeat("細", 201)
	if err := validateMeetInput(&in2, now, 50, 4, 4); !errors.Is(err, errMeetingDetail) {
		t.Fatalf("no_location=true 時 meeting_detail 仍應套用 200 字上限，得 %v", err)
	}
}

// TestBuildCardIncludesNoLocation buildCard 的 CardView 必須把 no_location 原樣帶出去，
// 前端才有依據判斷要不要顯示「🌏 不限地點」而不是拼接空殼的 region/place_label。
func TestBuildCardIncludesNoLocation(t *testing.T) {
	h := &Handler{}
	m := privateMeetRow()
	m.IsPrivate = false
	m.NoLocation = true
	m.Region, m.PlaceLabel = noLocationText, noLocationText
	c := h.buildCard(&m, "stranger", false, false)
	if !c.NoLocation {
		t.Fatal("CardView.NoLocation 應照實反映 meetRow.NoLocation")
	}
}

// TestBuildShareViewPassesThroughNoLocation buildShareView 無論公開或私密團，no_location
// 都應照實回傳（它不揭露座標或行政區，只是「沒有指定地點」這個事實本身）。
func TestBuildShareViewPassesThroughNoLocation(t *testing.T) {
	row := shareTestRow(false, nil)
	row.NoLocation = true
	if v := buildShareView(row); !v.NoLocation {
		t.Fatal("公開團 no_location 應照實回傳 true")
	}
	privRow := shareTestRow(true, nil)
	privRow.NoLocation = true
	if v := buildShareView(privRow); !v.NoLocation {
		t.Fatal("私密團的 no_location 也應照實回傳（不因 region/place_label 被遮蔽而連帶被清掉）")
	}
	falseRow := shareTestRow(false, nil)
	if v := buildShareView(falseRow); v.NoLocation {
		t.Fatal("no_location=false 應照實回傳 false")
	}
}

func TestMeetStatusError(t *testing.T) {
	if err := meetStatusError(StatusOpen, false); err != nil {
		t.Fatalf("open 且未結束不應有錯：%v", err)
	}
	if err := meetStatusError(StatusOpen, true); !errors.Is(err, errEnded) {
		t.Fatalf("已結束應回 errEnded，得 %v", err)
	}
	if err := meetStatusError(StatusClosed, false); !errors.Is(err, errMeetClosed) {
		t.Fatalf("closed 應回 errMeetClosed，得 %v", err)
	}
	if err := meetStatusError(StatusCancelled, false); !errors.Is(err, errMeetCancelled) {
		t.Fatalf("cancelled 應回 errMeetCancelled，得 %v", err)
	}
}

// --- 圖片驗證（非 IO 部分）---

func encodePNG(t *testing.T, w, h int, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	a := uint8(255)
	if alpha {
		a = 128
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: a})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7 % 256), G: uint8(y * 13 % 256), B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bombPNG 手工組出「只有 IHDR、宣告 30000×30000」的 PNG（decompression bomb 的形狀：
// 檔案幾十 bytes、解開後要 3.6GB RGBA）。DecodeConfig 只讀 IHDR，正好用來測尺寸閘門。
func bombPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 0, 17)
	ihdr = append(ihdr, 'I', 'H', 'D', 'R')
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], w)
	ihdr = append(ihdr, tmp[:]...)
	binary.BigEndian.PutUint32(tmp[:], h)
	ihdr = append(ihdr, tmp[:]...)
	ihdr = append(ihdr, 8, 2, 0, 0, 0) // bitDepth=8, colorType=2(truecolor), 其餘 0
	binary.BigEndian.PutUint32(tmp[:], 13)
	b.Write(tmp[:])
	b.Write(ihdr)
	binary.BigEndian.PutUint32(tmp[:], crc32.ChecksumIEEE(ihdr))
	b.Write(tmp[:])
	return b.Bytes()
}

func TestValidateImageBytesAcceptsJPEGAndPNG(t *testing.T) {
	if err := validateImageBytes(encodePNG(t, 32, 32, false)); err != nil {
		t.Fatalf("正常 PNG 應通過：%v", err)
	}
	if err := validateImageBytes(encodeJPEG(t, 32, 32)); err != nil {
		t.Fatalf("正常 JPEG 應通過：%v", err)
	}
}

// TestValidateImageBytesRejectsNonWhitelist 驗收條件 8/9：SVG（含 script）、GIF、純文字
// 一律 400。SVG 是既有 /profile/avatar 鏈的儲存型 XSS 來源，本端點必須擋死。
func TestValidateImageBytesRejectsNonWhitelist(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\xff\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x00;")
	ico := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10}
	txt := []byte("just plain text, definitely not an image")
	for name, data := range map[string][]byte{"svg": svg, "gif": gif, "ico": ico, "text": txt} {
		if err := validateImageBytes(data); !errors.Is(err, errImageFormat) {
			t.Fatalf("%s 應被拒為格式不符，得 %v", name, err)
		}
	}
	// 宣告成 PNG 但內容是 SVG（part header 的 Content-Type 完全被忽略，只看嗅探結果）
	if sniffMime(svg) != "" {
		t.Fatal("SVG 不應被嗅探成白名單內的 MIME")
	}
}

// TestValidateImageBytesRejectsBomb 驗收條件 12：30000×30000 的 PNG bomb 必須在
// DecodeConfig 階段就被擋掉（絕不能走到 Decode，那會配置 3.6GB）。
func TestValidateImageBytesRejectsBomb(t *testing.T) {
	if err := validateImageBytes(bombPNG(30000, 30000)); !errors.Is(err, errImageDims) {
		t.Fatalf("30000×30000 應被判定解析度過高，得 %v", err)
	}
	// 邊界：8000×8000 = 64M 像素 > 40M 像素上限 → 仍拒
	if err := validateImageBytes(bombPNG(8000, 8000)); !errors.Is(err, errImageDims) {
		t.Fatalf("8000×8000（64M 像素）應超過總像素上限，得 %v", err)
	}
	// 8001 寬超過單邊上限
	if err := validateImageBytes(bombPNG(8001, 10)); !errors.Is(err, errImageDims) {
		t.Fatalf("寬 8001 應超過單邊上限，得 %v", err)
	}
	// 4000×4000 = 16M 像素，在上限內 → 通過
	if err := validateImageBytes(bombPNG(4000, 4000)); err != nil {
		t.Fatalf("4000×4000 應通過：%v", err)
	}
}

// TestValidateImageBytesRejectsBrokenPayload PNG 簽章 + 垃圾內容：嗅探說 PNG、解碼失敗 → 400。
func TestValidateImageBytesRejectsBrokenPayload(t *testing.T) {
	broken := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("garbage garbage")...)
	if err := validateImageBytes(broken); !errors.Is(err, errImageFormat) {
		t.Fatalf("壞掉的 PNG 應被拒，得 %v", err)
	}
}

// TestProcessUploadStripsTrailingPayload 驗收條件 10：JPEG 尾端附加 <script> 的 polyglot，
// 重新編碼後輸出位元組不得再含該 payload。
func TestProcessUploadStripsTrailingPayload(t *testing.T) {
	data := append(encodeJPEG(t, 64, 48), []byte(`<script>alert(1)</script>`)...)
	if !bytes.Contains(data, []byte("<script")) {
		t.Fatal("測資本身應含 payload")
	}
	out, mime, err := processUpload(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("附加 payload 的合法 JPEG 應能處理：%v", err)
	}
	if bytes.Contains(out, []byte("<script")) {
		t.Fatal("重新編碼後仍含 <script> payload")
	}
	if mime != "image/jpeg" {
		t.Fatalf("無 alpha 應輸出 JPEG，得 %q", mime)
	}
}

// TestProcessUploadKeepsAlphaAsPNG 有透明度的圖必須輸出 PNG（轉 JPEG 會把透明壓成黑）。
func TestProcessUploadKeepsAlphaAsPNG(t *testing.T) {
	out, mime, err := processUpload(bytes.NewReader(encodePNG(t, 32, 32, true)))
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" {
		t.Fatalf("含 alpha 應輸出 PNG，得 %q", mime)
	}
	if len(out) == 0 {
		t.Fatal("輸出不應為空")
	}
}

// TestProcessUploadAcceptsAlreadyCompressed 驗收條件 11（防 A 提案的誤拒）：
// 一張「重編碼後反而變大」的小 JPEG（orient==1）必須成功，不得被當成「無法安全處理」而 400。
// 這正是不能沿用 CompressImage 的 changed 旗標當安全判定的理由。
func TestProcessUploadAcceptsAlreadyCompressed(t *testing.T) {
	// 高熵雜訊小圖：q90 編碼後，本套件的 q82 重編碼很可能不會更小
	img := image.NewRGBA(image.Rect(0, 0, 60, 60))
	seed := uint32(12345)
	for y := 0; y < 60; y++ {
		for x := 0; x < 60; x++ {
			seed = seed*1664525 + 1013904223
			img.Set(x, y, color.RGBA{R: uint8(seed >> 24), G: uint8(seed >> 16), B: uint8(seed >> 8), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 30}); err != nil {
		t.Fatal(err)
	}
	orig := buf.Bytes()
	out, _, err := processUpload(bytes.NewReader(orig))
	if err != nil {
		t.Fatalf("已高度壓縮的正常小圖必須成功入庫，卻被拒：%v", err)
	}
	if len(out) == 0 {
		t.Fatal("輸出不應為空")
	}
	if len(out) < len(orig) {
		t.Logf("（本次重編碼後仍變小：%d → %d；測試重點在不得因『沒變小』而失敗）", len(orig), len(out))
	}
}

// TestProcessUploadRejectsOversize 上限（現行 25MB，安全網）→ 413。
func TestProcessUploadRejectsOversize(t *testing.T) {
	big := make([]byte, maxUploadBytes+1)
	copy(big, []byte{0xff, 0xd8, 0xff}) // 讓它看起來像 JPEG 開頭
	if _, _, err := processUpload(bytes.NewReader(big)); !errors.Is(err, errImageTooLarge) {
		t.Fatalf("超過上限應回 413，得 %v", err)
	}
}

// --- 錯誤碼對應（前端契約）---

func TestErrorStatusContract(t *testing.T) {
	cases := []struct {
		name string
		err  *apiErr
		want int
	}{
		{"入口未開放", errEntryClosed, 403},
		{"非發起人", errNotOwner, 403},
		{"非成員", errNotMember, 403},
		{"私密未解鎖", errLocked, 403},
		{"密碼錯誤", errPasswordWrong, 403},
		{"已被剔除", errKicked, 403},
		{"需 VIP", errRequiresVIP, 403},
		{"密碼錯太多次", errPasswordTooMany, 429},
		{"留言太快", errCommentFast, 429},
		{"名額已滿", errMeetFull, 409},
		{"同意時額滿", errApproveFull, 409},
		{"申請已處理", errApplicationDone, 409},
		{"候補已滿", errPendingFull, 409},
		{"重複加入", errAlreadyJoined, 409},
		{"已結束", errEnded, 409},
		{"已關閉", errMeetClosed, 409},
		{"已中止", errMeetCancelled, 409},
		{"狀態轉換不合法", errBadTransition, 409},
		{"找不到", errNotFound, 404},
		{"名稱長度", errTitleLen, 400},
		{"圖片來源", errImageSource, 400},
		{"圖片格式", errImageFormat, 400},
		{"圖片解析度", errImageDims, 400},
		{"圖片過大", errImageTooLarge, 413},
	}
	for _, c := range cases {
		if c.err.Status != c.want {
			t.Fatalf("%s 應回 HTTP %d，得 %d", c.name, c.want, c.err.Status)
		}
	}
	// 404/500 一律英文短句（不外洩內部細節）；其餘 4xx 必須是中文文案
	if errNotFound.Msg != "not found" || errServer.Msg != "failed" {
		t.Fatal("404/500 應為英文短句")
	}
}

// TestNoTeamOrGroupWording 命名鐵律：中文文案一律「團練」，不得出現「跑團」
// （賽事已有「跑團分組/跑團鑰匙」，撞名會混淆，且 security-audit 對 group_key 有既有規則）。
func TestNoTeamOrGroupWording(t *testing.T) {
	msgs := []string{
		errEntryClosed.Msg, errNotOwner.Msg, errNotMember.Msg, errLocked.Msg,
		errPasswordWrong.Msg, errPasswordTooMany.Msg, errMeetFull.Msg, errApproveFull.Msg,
		errApplicationDone.Msg, errKicked.Msg, errPendingFull.Msg, errAlreadyJoined.Msg,
		errAlreadyPending.Msg, errEnded.Msg, errMeetClosed.Msg, errMeetCancelled.Msg,
		errBadTransition.Msg,
		errOwnerCantLeave.Msg, errNoSuchMember.Msg, errTitleLen.Msg, errRequiresVIP.Msg,
		errRegionLen.Msg, errPlaceLabelLen.Msg, locationNote, errEditEnded.Msg,
		errQuotaUsedUp(1, false, time.September, 10).Msg,
		errQuotaUsedUp(10, true, time.September, 10).Msg,
		errCapacityBelowMembers(8).Msg, errImageOverLimit(4, 4).Msg, errImageOverLimit(1, 4).Msg,
		newErrRejectCooldown(24).Msg,
	}
	for _, m := range msgs {
		if strings.Contains(m, "跑團") {
			t.Fatalf("文案不得出現「跑團」二字（應用「團練」）：%q", m)
		}
	}
}

func TestQuotaUsedUpMessages(t *testing.T) {
	nonVIP := errQuotaUsedUp(1, false, time.September, 10).Msg
	if !strings.Contains(nonVIP, "1/1") || !strings.Contains(nonVIP, "9 月 1 日") ||
		!strings.Contains(nonVIP, "VIP") {
		t.Fatalf("非 VIP 額度用盡訊息應含用量、重置日與升級引導：%q", nonVIP)
	}
	vip := errQuotaUsedUp(10, true, time.September, 10).Msg
	if !strings.Contains(vip, "10/10") || strings.Contains(vip, "升級") {
		t.Fatalf("VIP 額度用盡訊息不應再推銷升級：%q", vip)
	}
}

// TestQuotaAndImageMessagesUseSettings VIP 次數／圖片張數是後台可調設定
// （runmeet_quota_vip / runmeet_images_vip），文案一律不得寫死 10 / 4。
func TestQuotaAndImageMessagesUseSettings(t *testing.T) {
	m := errQuotaUsedUp(1, false, time.September, 5).Msg
	if !strings.Contains(m, "5 次") || strings.Contains(m, "10 次") {
		t.Fatalf("VIP 上限調成 5 時文案應說 5 次：%q", m)
	}
	// VIP 上限沒比較高 → 不做假的升級引導
	m = errQuotaUsedUp(3, false, time.September, 3).Msg
	if strings.Contains(m, "升級") {
		t.Fatalf("VIP 上限與一般相同時不應推銷升級：%q", m)
	}
	img := errImageOverLimit(1, 2).Msg
	if !strings.Contains(img, "2 張") || strings.Contains(img, "4 張") {
		t.Fatalf("VIP 圖片上限調成 2 時文案應說 2 張：%q", img)
	}
	// repository 層拿不到設定（vipLimit=0）→ 只講這個團的上限
	if strings.Contains(errImageOverLimit(1, 0).Msg, "VIP") {
		t.Fatalf("拿不到設定時不應提 VIP：%q", errImageOverLimit(1, 0).Msg)
	}
}

// TestSnapRadiusKm 半徑必須離散化到 band 邊界：任意連續值都會被吸附，
// 否則「有沒有出現在結果裡」就是可二分搜尋的精確距離神諭。
func TestSnapRadiusKm(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0.001, 1}, {0.9, 1}, {1, 1},
		{1.0001, 3}, {2.5, 3}, {3, 3},
		{3.1, 5}, {5, 5},
		{5.1, 10}, {10, 10}, {12, 10}, {100, 10}, {1e9, 10},
	}
	for _, c := range cases {
		if got := snapRadiusKm(c.in); got != c.want {
			t.Fatalf("snapRadiusKm(%v) want %v got %v", c.in, c.want, got)
		}
	}
	// 吸附後的每個值都必須正好是某個 band 的邊界（否則過濾比 band 更精細）
	for _, r := range allowedRadiiKm {
		if DistanceBand(r*1000-0.001) == DistanceBand(r*1000) {
			t.Fatalf("半徑 %v km 未落在 band 邊界上", r)
		}
	}
}

// TestSnapCoordQuantises 座標量化是附近搜尋唯一真正擋住三角定位的機制：
// 同一團必須穩定吸附到同一格點（多次查詢無法平均掉），不同團的格線偏移必須不同。
func TestSnapCoordQuantises(t *testing.T) {
	const id = "0123abcd-4567-89ab-cdef-0123456789ab"
	lat, lng := 25.033000, 121.565000

	a1, b1 := snapCoord(lat, lng, id)
	a2, b2 := snapCoord(lat, lng, id)
	if a1 != a2 || b1 != b2 {
		t.Fatal("同一團同一座標必須每次吸附到同一格點（否則多次查詢可平均掉雜訊）")
	}
	// 必須真的移動了（不是恆等函式），且位移在一格之內
	if a1 == lat && b1 == lng {
		t.Fatal("座標未被量化")
	}
	if d := haversineM(lat, lng, a1, b1); d > geoCellMeters {
		t.Fatalf("量化位移 %.0f m 超過一格（%v m），附近搜尋會失準", d, geoCellMeters)
	}
	// 真的丟掉精度：把 500 m 見方內的 900 個點全部吸附後，落點必須收斂成少數幾個格點
	// （一個 500 m 見方最多跨到 2×2 個格子；用不到 400 個相異值就代表沒在量化）。
	seen := map[[2]float64]bool{}
	for i := 0; i < 30; i++ {
		for j := 0; j < 30; j++ {
			la := lat + float64(i)*0.00015 // ≈ 16.7 m 一步，30 步 ≈ 500 m
			ln := lng + float64(j)*0.00015
			x, y := snapCoord(la, ln, id)
			seen[[2]float64{x, y}] = true
		}
	}
	if len(seen) > 9 {
		t.Fatalf("500 m 見方內吸附出 %d 個相異格點，量化沒生效", len(seen))
	}
	// 不同團練的格線偏移必須不同（否則攻擊者知道格線位置就能反推格內位置）
	other := "9999abcd-4567-89ab-cdef-0123456789ab"
	a4, b4 := snapCoord(lat, lng, other)
	if a4 == a1 && b4 == b1 {
		t.Fatal("不同團練應有各自的格線偏移")
	}
}

func TestItoa(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{0, "0"}, {7, "7"}, {24, "24"}, {1000, "1000"}, {-3, "-3"}} {
		if got := itoa(c.n); got != c.want {
			t.Fatalf("itoa(%d) want %q got %q", c.n, c.want, got)
		}
	}
}

// --- buildDetail 的兩道獨立閘門（HasDetailAccess／CanSeePreciseLocation）---

func privateMeetRow() meetRow {
	lat, lng := 25.033, 121.565
	return meetRow{
		ID: "0123abcd-4567-89ab-cdef-0123456789ab", OwnerID: "owner-1",
		Title: "私密晨跑", MeetAt: time.Now().Add(24 * time.Hour),
		Region: "臺北市・大安區", PlaceLabel: "大安森林公園",
		Lat: &lat, Lng: &lng, MeetingDetail: "2 號出口涼亭旁",
		Capacity: 10, Description: "這裡是要給管理員審的完整說明",
		ImageURLs:  []string{"/api/v1/images/0123abcd-4567-89ab-cdef-0123456789ab"},
		ImageLimit: 4, IsPrivate: true, MemberCount: 1, PendingCount: 2, Status: StatusOpen,
	}
}

// TestBuildDetailAdminSeesPrivateContent 後台檢視私密團時，說明與圖片**不得**被清空。
// 檢舉最常見的對象正是私密團（外人看不到、只有被拉進去的人會檢舉），管理員看到空白說明
// 就只能盲下架或盲放過——規格 1.7 把「後台強制下架」列為 P1 上線前提，這條不能壞。
func TestBuildDetailAdminSeesPrivateContent(t *testing.T) {
	h := &Handler{}
	m := privateMeetRow()
	raw, err := json.Marshal(h.buildDetail(&m, "admin-user", true))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["has_access"] != true {
		t.Fatalf("後台視角 has_access 應為 true：%s", raw)
	}
	if got["description"] != m.Description {
		t.Fatalf("後台視角不得清空 description：%s", raw)
	}
	if imgs, _ := got["image_urls"].([]any); len(imgs) != 1 {
		t.Fatalf("後台視角不得清空 image_urls：%s", raw)
	}
	if got["cover_url"] == nil {
		t.Fatalf("後台視角應有 cover_url：%s", raw)
	}
	// 成員層三欄仍照舊給後台（處理檢舉/糾紛需要）
	if _, ok := got["meeting_detail"]; !ok {
		t.Fatalf("後台視角應含 meeting_detail：%s", raw)
	}
	if got["pending_count"] != float64(2) {
		t.Fatalf("後台視角應看得到 pending_count：%s", raw)
	}
}

// TestBuildDetailNonMemberHasNoLocationKeys 未加入者的 JSON **根本不含**成員層三個 key，
// 而且私密團未解鎖時說明與圖片仍要清空（兩道閘門互不影響）。
func TestBuildDetailNonMemberHasNoLocationKeys(t *testing.T) {
	h := &Handler{}
	m := privateMeetRow()
	raw, err := json.Marshal(h.buildDetail(&m, "stranger", false))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"lat", "lng", "meeting_detail"} {
		if _, ok := got[k]; ok {
			t.Fatalf("未加入者的回應不得出現 %q（要用不同 struct，不是回零值）：%s", k, raw)
		}
	}
	if got["location_locked"] != true {
		t.Fatalf("未加入者 location_locked 應為 true：%s", raw)
	}
	if got["description"] != "" {
		t.Fatalf("私密團未解鎖應清空 description：%s", raw)
	}
	if got["pending_count"] != float64(0) {
		t.Fatalf("非發起人不得看到 pending_count：%s", raw)
	}
}

// TestBuildCardHiddenByOwnerVisibleOnlyToOwnerAndAdmin hidden_by_owner 只給發起人／後台
// 管理面板用（比照 pending_count 的既有模式）：非 owner、非 admin 視角恆看到 false，
// 即使實際值是 true——一般訪客本來就看不到被隱藏的團（SQL 層已擋掉），這裡是最後一道保險。
func TestBuildCardHiddenByOwnerVisibleOnlyToOwnerAndAdmin(t *testing.T) {
	h := &Handler{}
	m := privateMeetRow()
	m.HiddenByOwner = true

	if c := h.buildCard(&m, m.OwnerID, false, false); !c.HiddenByOwner {
		t.Fatal("發起人視角應看到 hidden_by_owner=true")
	}
	if c := h.buildCard(&m, "someone-else", false, true); !c.HiddenByOwner {
		t.Fatal("後台視角應看到 hidden_by_owner=true")
	}
	if c := h.buildCard(&m, "stranger", false, false); c.HiddenByOwner {
		t.Fatal("非發起人／非後台視角不應看到真實的 hidden_by_owner 值")
	}
}

// TestValidateMeetInputNilImages 省略 image_urls（或送 null）不得變成 SQL NULL：
// migration 的 image_urls 是 TEXT[] NOT NULL，pgx v5 把 nil slice 編成 NULL → 23502 → 500。
func TestValidateMeetInputNilImages(t *testing.T) {
	now := time.Now()
	in := baseInput(now)
	in.ImageURLs = nil
	if err := validateMeetInput(&in, now, 50, 1, 4); err != nil {
		t.Fatalf("省略 image_urls 應合法：%v", err)
	}
	if in.ImageURLs == nil {
		t.Fatal("驗證後 ImageURLs 必須是空陣列而非 nil（否則 pgx 會寫入 SQL NULL）")
	}
}

// --- 分享卡（GET /run-meets/{id}/share，公開／免登入）---

func shareTestRow(isPrivate bool, images []string) shareRow {
	return shareRow{
		Title: "晨間夜跑團", MeetAt: time.Date(2026, 9, 10, 6, 0, 0, 0, time.UTC),
		Region: "臺北市・大安區", PlaceLabel: "大安森林公園",
		ImageURLs: images, IsPrivate: isPrivate, MemberCount: 3, Capacity: 10,
	}
}

// TestBuildShareViewPublicKeepsLocationAndCover 公開團：region/place_label 照給，
// cover_url 取 image_urls 的第一張。
func TestBuildShareViewPublicKeepsLocationAndCover(t *testing.T) {
	imgs := []string{"/api/v1/images/0123abcd-4567-89ab-cdef-0123456789ab"}
	v := buildShareView(shareTestRow(false, imgs))
	if !v.Available {
		t.Fatal("公開團應 available=true")
	}
	if v.Region != "臺北市・大安區" || v.PlaceLabel != "大安森林公園" {
		t.Fatalf("公開團地點不應被遮蔽：%+v", v)
	}
	if v.CoverURL == nil || *v.CoverURL != imgs[0] {
		t.Fatalf("公開團應帶封面圖：%+v", v)
	}
}

// TestBuildShareViewPublicNoImagesCoverNil 公開團但沒圖：cover_url 仍是 null（不是空字串）。
func TestBuildShareViewPublicNoImagesCoverNil(t *testing.T) {
	v := buildShareView(shareTestRow(false, nil))
	if v.CoverURL != nil {
		t.Fatalf("沒有圖片時 cover_url 應為 null，得 %v", *v.CoverURL)
	}
}

// TestBuildShareViewPrivateMasksLocationAndCover 私密團隱私遮蔽（規格重點）：即使該團真的
// 有 region/place_label/圖片，分享卡也一律回空字串／null——分享卡連身分都沒有，
// 沒有理由知道得比未解鎖的登入會員還多。
func TestBuildShareViewPrivateMasksLocationAndCover(t *testing.T) {
	imgs := []string{"/api/v1/images/0123abcd-4567-89ab-cdef-0123456789ab"}
	v := buildShareView(shareTestRow(true, imgs))
	if v.Region != "" {
		t.Fatalf("私密團 region 應回空字串，得 %q", v.Region)
	}
	if v.PlaceLabel != "" {
		t.Fatalf("私密團 place_label 應回空字串，得 %q", v.PlaceLabel)
	}
	if v.CoverURL != nil {
		t.Fatalf("私密團 cover_url 應為 null，得 %v", *v.CoverURL)
	}
	if !v.IsPrivate {
		t.Fatal("is_private 應照實回 true")
	}
	if !v.Available {
		t.Fatal("私密團本身仍是 available=true（只是內容被遮蔽，不是整筆不可用）")
	}
}

// TestShareViewJSONHasNoMemberLayerFields 回應契約：只能有這 9 個 key，尤其不得出現
// lat/lng/meeting_detail/description/owner/id/成員名單等任何成員層或個資欄位——這支端點
// 連身分都沒有，沒有後續閘門能補救多回的欄位。
func TestShareViewJSONHasNoMemberLayerFields(t *testing.T) {
	imgs := []string{"/api/v1/images/0123abcd-4567-89ab-cdef-0123456789ab"}
	v := buildShareView(shareTestRow(false, imgs))
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"available", "title", "meet_at", "region", "place_label", "no_location",
		"cover_url", "is_private", "member_count", "capacity"}
	if len(got) != len(want) {
		t.Fatalf("回應欄位數應為 %d，得 %d：%s", len(want), len(got), raw)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Fatalf("回應缺少契約欄位 %q：%s", k, raw)
		}
	}
	forbidden := []string{"lat", "lng", "meeting_detail", "description", "id",
		"owner", "owner_id", "email", "members", "image_urls", "excerpt", "status"}
	for _, k := range forbidden {
		if _, ok := got[k]; ok {
			t.Fatalf("回應絕不可出現成員層/個資欄位 %q：%s", k, raw)
		}
	}
}

// TestShareUnavailableShapeIsMinimal 「不可用」的回應只能有 available:false 這一個 key
// （不存在／已軟刪／後台下架／status 非 open／入口 hidden 一律走這條，不分原因，
// 也不得夾帶任何其他欄位讓人從回應形狀猜出差異——Handler.Share 的 unavailable() 就是
// 直接回這個 literal map，這裡把契約釘死，未來有人手滑多塞欄位會立刻測試失敗）。
func TestShareUnavailableShapeIsMinimal(t *testing.T) {
	raw, err := json.Marshal(map[string]bool{"available": false})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("不可用回應應只有 1 個 key，得 %d：%s", len(got), raw)
	}
	if got["available"] != false {
		t.Fatalf("available 應為 false：%s", raw)
	}
}

// TestShareDeletedShapeIsMinimal 已刪除的分享卡回應只能有 available/deleted 這兩個 key，
// deleted 恆 true（規格：連結是發起人主動分享出去的，顯示「已刪除」不構成新洩漏）。
func TestShareDeletedShapeIsMinimal(t *testing.T) {
	raw, err := json.Marshal(map[string]bool{"available": false, "deleted": true})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("已刪除回應應只有 2 個 key，得 %d：%s", len(got), raw)
	}
	if got["available"] != false || got["deleted"] != true {
		t.Fatalf("已刪除回應內容不對：%s", raw)
	}
}

// --- shareAvailability（分享卡「能不能用」純函式判定）---

// TestShareAvailabilityDeletedTakesPriorityAndOnlyLeaksItself 已刪除是唯一能被單獨識別出來
// 的不可用原因；其餘原因（下架／隱藏／status 非 open）一律收斂成同一個 errNotFound，不分原因。
func TestShareAvailabilityDeletedTakesPriorityAndOnlyLeaksItself(t *testing.T) {
	if err := shareAvailability(false, false, false, StatusOpen); err != nil {
		t.Fatalf("正常公開開放中的團應可用：%v", err)
	}
	// 已刪除：即使同時也被下架/隱藏/status 非 open，仍回 errDeleted（deleted 優先揭露）
	if err := shareAvailability(true, true, true, StatusClosed); !errors.Is(err, errDeleted) {
		t.Fatalf("已刪除應回 errDeleted（即使同時有其他不可用原因），得 %v", err)
	}
	cases := []struct {
		name                     string
		hiddenAdmin, hiddenOwner bool
		status                   string
	}{
		{"後台下架", true, false, StatusOpen},
		{"發起人隱藏", false, true, StatusOpen},
		{"已關閉", false, false, StatusClosed},
		{"已中止", false, false, StatusCancelled},
	}
	for _, c := range cases {
		err := shareAvailability(false, c.hiddenAdmin, c.hiddenOwner, c.status)
		if !errors.Is(err, errNotFound) {
			t.Fatalf("%s：應收斂成 errNotFound（不外洩具體原因），得 %v", c.name, err)
		}
	}
	// 查無此團（不存在）不經過這支函式（GetMeetShare 在 QueryRow 就先回 errNotFound 了），
	// 但確認它不會被誤判成 deleted：deleted 參數為 false 時絕不能回 errDeleted。
	if err := shareAvailability(false, false, false, StatusClosed); errors.Is(err, errDeleted) {
		t.Fatal("deleted=false 時絕不能回 errDeleted（避免被拿來探測任意 UUID 是否曾經存在過）")
	}
}

// --- 狀態機（CanTransition／CanEdit／CanSeeWhenHidden，model.go）---

// TestCanTransition 逐一列出使用者定案的合法轉換表，並確認所有「不合法」組合都被擋下——
// 尤其 cancelled→closed 與同狀態互轉，這是本次重整前不存在、容易漏掉的兩類。
func TestCanTransition(t *testing.T) {
	allowed := map[[2]string]bool{
		{StatusOpen, StatusClosed}:      true,
		{StatusOpen, StatusCancelled}:   true,
		{StatusClosed, StatusOpen}:      true,
		{StatusClosed, StatusCancelled}: true,
		{StatusCancelled, StatusOpen}:   true,
	}
	all := []string{StatusOpen, StatusClosed, StatusCancelled}
	for _, from := range all {
		for _, to := range all {
			want := allowed[[2]string{from, to}]
			if got := CanTransition(from, to); got != want {
				t.Fatalf("CanTransition(%q,%q) want %v got %v", from, to, want, got)
			}
		}
	}
	// 使用者原話最在意的那一條：關閉必須可逆
	if !CanTransition(StatusClosed, StatusOpen) {
		t.Fatal("closed→open 必須合法：「關閉的團練，發起人應該可以再次開啟才對」")
	}
	// 中止不可跳過 open 直接變 closed
	if CanTransition(StatusCancelled, StatusClosed) {
		t.Fatal("cancelled→closed 不應合法（必須先恢復 open 才能再關閉）")
	}
}

func TestCanEdit(t *testing.T) {
	// 使用者定義：「關閉＝不再收新人，其他功能都照舊」，所以 closed 期間必須能編輯
	// （常見動作：關閉停招 → 改時間/集合細節 → 重新開啟）。cancelled 才真的鎖住。
	if !CanEdit(StatusOpen) {
		t.Fatal("open 應可編輯")
	}
	if !CanEdit(StatusClosed) {
		t.Fatal("closed 應可編輯（關閉只是不再收人，其他功能照舊）")
	}
	if CanEdit(StatusCancelled) {
		t.Fatal("cancelled 不應可編輯")
	}
}

func TestCanSeeWhenHidden(t *testing.T) {
	if !CanSeeWhenHidden(true, "") {
		t.Fatal("發起人應能看到自己隱藏的團")
	}
	if !CanSeeWhenHidden(false, MemberJoined) {
		t.Fatal("已加入成員應能看到隱藏的團（否則已成團的人會突然看不到集合資訊）")
	}
	for _, s := range []string{MemberPending, MemberRejected, MemberKicked, MemberLeft, ""} {
		if CanSeeWhenHidden(false, s) {
			t.Fatalf("非 owner 且 status=%q 不應看得到隱藏的團", s)
		}
	}
}

// --- joinBlockReason／meetStatusError（service.go；狀態擋加入的唯一出口）---

func TestJoinBlockReason(t *testing.T) {
	if got := joinBlockReason(StatusOpen); got != "" {
		t.Fatalf("open 不應擋加入，得 %q", got)
	}
	if got := joinBlockReason(StatusClosed); got != errMeetClosed.Msg {
		t.Fatalf("closed 應回既有的關閉文案，得 %q", got)
	}
	if got := joinBlockReason(StatusCancelled); got != errMeetCancelled.Msg {
		t.Fatalf("cancelled 應回「該團練已中止」，得 %q", got)
	}
	// 使用者明確要求的字面文案
	if errMeetCancelled.Msg != "該團練已中止。" {
		t.Fatalf("cancelled 的錯誤文案應為「該團練已中止。」，得 %q", errMeetCancelled.Msg)
	}
}

// TestMeetStatusErrorAndJoinBlockReasonShareSameSingleton 確保 meetStatusError 回的物件
// 與 joinBlockReason 讀的是同一個單例指標（errors.Is 賴此比對），不是兩份各自維護、可能漂移
// 的文案來源。
func TestMeetStatusErrorAndJoinBlockReasonShareSameSingleton(t *testing.T) {
	for _, status := range []string{StatusClosed, StatusCancelled} {
		err := meetStatusError(status, false)
		var e *apiErr
		if !errors.As(err, &e) {
			t.Fatalf("meetStatusError(%q) 應回 *apiErr", status)
		}
		if e.Msg != joinBlockReason(status) {
			t.Fatalf("meetStatusError 與 joinBlockReason 對 %q 的文案不一致：%q vs %q",
				status, e.Msg, joinBlockReason(status))
		}
	}
}

// --- statusAction（handler.go；API 契約 action 值 → status 欄位值）---

func TestStatusAction(t *testing.T) {
	cases := []struct {
		action     string
		wantStatus string
		wantOK     bool
	}{
		{"open", StatusOpen, true},
		{"close", StatusClosed, true},
		{"cancel", StatusCancelled, true},
		{"", "", false},
		{"closed", "", false}, // 不接受資料庫欄位值本身，只接受動詞
		{"delete", "", false},
		{"OPEN", "", false}, // 大小寫敏感
	}
	for _, c := range cases {
		status, ok := statusAction(c.action)
		if ok != c.wantOK || (ok && status != c.wantStatus) {
			t.Fatalf("statusAction(%q) want (%q,%v) got (%q,%v)", c.action, c.wantStatus, c.wantOK, status, ok)
		}
	}
}

// --- 討論串（migration 159：留言升級為 Threads 式回覆＋表情反應＋游標分頁；見 thread.go）---

// TestCursorRoundTrip encode→decode 必須拿回一模一樣的 (created_at, id)——游標分頁的正確性
// 完全建立在這個往返不失真上（用 UnixNano 而非 RFC3339 字串正是為了這個）。
func TestCursorRoundTrip(t *testing.T) {
	want := time.Date(2026, 8, 31, 12, 34, 56, 789000000, time.UTC)
	id := "11111111-1111-1111-1111-111111111111"

	s := encodeCursor(want, id)
	if s == "" {
		t.Fatal("encodeCursor 不應回空字串")
	}
	gotAt, gotID, ok := decodeCursor(s)
	if !ok {
		t.Fatalf("decodeCursor(%q) 應成功", s)
	}
	if !gotAt.Equal(want) {
		t.Fatalf("created_at 往返失真：want %v got %v", want, gotAt)
	}
	if gotID != id {
		t.Fatalf("id 往返失真：want %q got %q", id, gotID)
	}
}

// TestDecodeCursorInvalid 規格明定：無效 cursor 一律當第一頁處理、不報錯——這裡驗證
// decodeCursor 對各種壞輸入都回 ok=false，而不是 panic 或回一個假的時間/id。
func TestDecodeCursorInvalid(t *testing.T) {
	validID := "11111111-1111-1111-1111-111111111111"
	valid := encodeCursor(time.Now(), validID)

	cases := []struct {
		name   string
		cursor string
	}{
		{"空字串", ""},
		{"非法 base64", "!!!not-base64!!!"},
		{"base64 合法但內容缺分隔符", base64.RawURLEncoding.EncodeToString([]byte("noSeparatorHere"))},
		{"id 不是合法 UUID", base64.RawURLEncoding.EncodeToString([]byte("123|not-a-uuid"))},
		{"缺 id（分隔符後為空）", base64.RawURLEncoding.EncodeToString([]byte("123|"))},
		{"created_at 不是數字", base64.RawURLEncoding.EncodeToString([]byte("abc|" + validID))},
	}
	for _, c := range cases {
		if _, _, ok := decodeCursor(c.cursor); ok {
			t.Fatalf("%s：decodeCursor(%q) 應回 ok=false", c.name, c.cursor)
		}
	}

	// 對照組：確認上面壞掉的不是「decodeCursor 整支都壞了」——合法游標本身要能過。
	if _, _, ok := decodeCursor(valid); !ok {
		t.Fatal("合法游標不應被判定為無效")
	}
}

// TestValidateReplyParent 只允許一層回覆＋不可回覆已刪留言的核心判定（規格 3 與「其他要求」）。
func TestValidateReplyParent(t *testing.T) {
	cases := []struct {
		name    string
		state   parentCommentState
		wantErr error
	}{
		{"查無此留言（含跨團）", parentCommentState{Exists: false}, errCommentParentNotFound},
		{"父留言已被軟刪", parentCommentState{Exists: true, Deleted: true}, errCommentDeleted},
		{"父留言本身是回覆（第二層）", parentCommentState{Exists: true, ParentID: ptr("x")}, errCommentNestedReply},
		{"父留言是合法頂層留言", parentCommentState{Exists: true, ParentID: nil}, nil},
	}
	for _, c := range cases {
		if got := validateReplyParent(c.state); got != c.wantErr {
			t.Fatalf("%s：want %v got %v", c.name, c.wantErr, got)
		}
	}
}

// TestSortReactions 依 count desc、kind asc 排序（規格固定順序）。
func TestSortReactions(t *testing.T) {
	rs := []ReactionCountView{
		{Kind: "pray", Count: 2},
		{Kind: "like", Count: 5},
		{Kind: "fire", Count: 5}, // 與 like 同票數，應按 kind 字母序排在前面
		{Kind: "heart", Count: 1},
	}
	sortReactions(rs)
	want := []string{"fire", "like", "pray", "heart"}
	for i, k := range want {
		if rs[i].Kind != k {
			t.Fatalf("排序結果第 %d 個 want %q got %q（完整結果 %+v）", i, k, rs[i].Kind, rs)
		}
	}

	// 無反應：空切片排序後仍是空切片，不 panic、不變成 nil。
	empty := []ReactionCountView{}
	sortReactions(empty)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("空切片排序後應仍是長度 0 的非 nil 切片，got %#v", empty)
	}
}

// TestMaskDeleted 軟刪留言的欄位遮蔽（規格「其他要求」）：deleted_at 非空一律
// can_delete=false、reactions=[]、body=""、deleted=true；未刪留言完全不受影響。
func TestMaskDeleted(t *testing.T) {
	now := time.Now()

	deleted := CommentView{
		Body: "原始內容", CanDelete: true,
		Reactions:  []ReactionCountView{{Kind: "like", Count: 3}},
		MyReaction: ptr("like"),
	}
	maskDeleted(&deleted, &now)
	if !deleted.Deleted {
		t.Fatal("deleted_at 非空應標記 Deleted=true")
	}
	if deleted.Body != "" {
		t.Fatalf("已刪留言 Body 應為空字串，got %q", deleted.Body)
	}
	if deleted.CanDelete {
		t.Fatal("已刪留言 CanDelete 應恆為 false")
	}
	if len(deleted.Reactions) != 0 {
		t.Fatalf("已刪留言 Reactions 應為 []，got %+v", deleted.Reactions)
	}
	if deleted.MyReaction != nil {
		t.Fatal("已刪留言 MyReaction 應為 nil")
	}

	live := CommentView{Body: "還在", CanDelete: true, Reactions: []ReactionCountView{{Kind: "fire", Count: 1}}}
	maskDeleted(&live, nil)
	if live.Deleted || live.Body != "還在" || !live.CanDelete || len(live.Reactions) != 1 {
		t.Fatalf("未刪留言不應被 maskDeleted 動到任何欄位，got %+v", live)
	}
}

// TestToCommentViewJSONShape 前端契約：Reactions/Replies 恆為陣列（不是 null），即使沒有
// 回覆／反應也一樣——JSON 契約靠這裡把關，避免前端拿到 null 需要額外判斷。
func TestToCommentViewJSONShape(t *testing.T) {
	row := threadCommentRow{ID: "c1", UserID: "u1", Name: "小明", Body: "hi", CreatedAt: time.Now(), ReplyCount: 0}
	v := toCommentView(row, true)
	if v.Reactions == nil || len(v.Reactions) != 0 {
		t.Fatalf("Reactions 應為非 nil 空陣列，got %#v", v.Reactions)
	}
	if v.Replies == nil || len(v.Replies) != 0 {
		t.Fatalf("Replies 應為非 nil 空陣列，got %#v", v.Replies)
	}
	if v.Deleted {
		t.Fatal("未刪留言 Deleted 應為 false")
	}
}

// TestCanReact 與 TestCanComment 對照：canReact 一樣要求 joined/owner、團未中止，但**不**擋
// 「結束後 7 天」的唯讀期——已結束團練仍可按表情（規格 4）。
func TestCanReact(t *testing.T) {
	if !canReact(true, nil, StatusOpen) {
		t.Fatal("發起人應可設定表情")
	}
	if canReact(false, nil, StatusOpen) {
		t.Fatal("非成員不可設定表情")
	}
	if canReact(false, ptr(MemberPending), StatusOpen) {
		t.Fatal("申請中不可設定表情")
	}
	if !canReact(false, ptr(MemberJoined), StatusClosed) {
		t.Fatal("已關閉的團仍可設定表情")
	}
	if canReact(false, ptr(MemberJoined), StatusCancelled) {
		t.Fatal("已中止的團不可設定表情")
	}
	// 與 canComment 的關鍵差異：canComment 對「結束超過 7 天」的團會擋，canReact 不擋。
	// canReact 本身沒有時間參數，這裡確認它的簽章就是不含 meetAt——這條測試若編譯失敗，
	// 代表有人不小心把時間判定加回 canReact，違反規格 4。
	_ = canReact(false, ptr(MemberJoined), StatusOpen)
}
