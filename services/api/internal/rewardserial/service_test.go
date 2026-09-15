package rewardserial

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestValidateBundleChildMeta 純函式：組合型序號組（is_bundle=true，migration 150）的子項規則——子面額組
// 須存在、非組合型（防巢狀）、與其餘子項同一商家。涵蓋合法組合、巢狀（子項本身也是組合型）、跨商家、
// 子項不存在（metas 缺該 id）、merchant_id 皆為 nil（未指定商家，視為相同）等邊界。
func TestValidateBundleChildMeta(t *testing.T) {
	merchantA := "merchant-a"
	merchantB := "merchant-b"

	cases := []struct {
		name    string
		items   []GroupBundleItem
		metas   map[string]childGroupMeta
		wantErr bool
	}{
		{
			name:  "合法：兩個子項同商家",
			items: []GroupBundleItem{{ChildGroupID: "g1", Count: 3}, {ChildGroupID: "g2", Count: 1}},
			metas: map[string]childGroupMeta{
				"g1": {IsBundle: false, MerchantID: &merchantA},
				"g2": {IsBundle: false, MerchantID: &merchantA},
			},
			wantErr: false,
		},
		{
			name:  "合法：單一子項",
			items: []GroupBundleItem{{ChildGroupID: "g1", Count: 5}},
			metas: map[string]childGroupMeta{
				"g1": {IsBundle: false, MerchantID: &merchantA},
			},
			wantErr: false,
		},
		{
			name:  "合法：皆未指定商家（nil 視為相同）",
			items: []GroupBundleItem{{ChildGroupID: "g1", Count: 1}, {ChildGroupID: "g2", Count: 1}},
			metas: map[string]childGroupMeta{
				"g1": {IsBundle: false, MerchantID: nil},
				"g2": {IsBundle: false, MerchantID: nil},
			},
			wantErr: false,
		},
		{
			name:  "防巢狀：子項本身也是組合型 → 錯誤",
			items: []GroupBundleItem{{ChildGroupID: "g1", Count: 1}},
			metas: map[string]childGroupMeta{
				"g1": {IsBundle: true, MerchantID: &merchantA},
			},
			wantErr: true,
		},
		{
			name:  "防巢狀：第二個子項才是組合型 → 錯誤",
			items: []GroupBundleItem{{ChildGroupID: "g1", Count: 1}, {ChildGroupID: "g2", Count: 1}},
			metas: map[string]childGroupMeta{
				"g1": {IsBundle: false, MerchantID: &merchantA},
				"g2": {IsBundle: true, MerchantID: &merchantA},
			},
			wantErr: true,
		},
		{
			name:  "跨商家：兩個子項商家不同 → 錯誤",
			items: []GroupBundleItem{{ChildGroupID: "g1", Count: 1}, {ChildGroupID: "g2", Count: 1}},
			metas: map[string]childGroupMeta{
				"g1": {IsBundle: false, MerchantID: &merchantA},
				"g2": {IsBundle: false, MerchantID: &merchantB},
			},
			wantErr: true,
		},
		{
			name:  "跨商家：一個有商家一個 nil → 錯誤",
			items: []GroupBundleItem{{ChildGroupID: "g1", Count: 1}, {ChildGroupID: "g2", Count: 1}},
			metas: map[string]childGroupMeta{
				"g1": {IsBundle: false, MerchantID: &merchantA},
				"g2": {IsBundle: false, MerchantID: nil},
			},
			wantErr: true,
		},
		{
			name:    "子項不存在（metas 缺該 id）→ 錯誤",
			items:   []GroupBundleItem{{ChildGroupID: "missing", Count: 1}},
			metas:   map[string]childGroupMeta{},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateBundleChildMeta(c.items, c.metas)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// TestSamePtrString 供 validateBundleChildMeta 判斷組合子項的 merchant_id 是否一致：皆 nil 視為相同
// （都未指定商家）；一 nil 一非 nil 視為不同；皆非 nil 則比較實際值。
func TestSamePtrString(t *testing.T) {
	s := func(v string) *string { return &v }
	cases := []struct {
		name string
		a, b *string
		want bool
	}{
		{"皆 nil → 相同", nil, nil, true},
		{"一 nil 一非 nil → 不同", nil, s("m1"), false},
		{"非 nil 且值相同 → 相同", s("m1"), s("m1"), true},
		{"非 nil 但值不同 → 不同", s("m1"), s("m2"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := samePtrString(c.a, c.b); got != c.want {
				t.Fatalf("samePtrString(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestDedupeValidUUIDs 供 DeleteSerials／VoidSerialsBatch 共用的批次 id 過濾：trim 空字串丟棄、非合法
// UUID 格式計入 invalidCount（避免打進 DB 的 uuid[] 轉型觸發 500）、重複 id 只保留一次且保留原始順序。
func TestDedupeValidUUIDs(t *testing.T) {
	u1 := "11111111-1111-1111-1111-111111111111"
	u2 := "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name           string
		raw            []string
		wantValid      []string
		wantInvalidCnt int
	}{
		{"全部合法且不重複", []string{u1, u2}, []string{u1, u2}, 0},
		{"重複 id 只保留一次", []string{u1, u1, u2}, []string{u1, u2}, 0},
		{"空字串與空白字串丟棄", []string{u1, "", "   "}, []string{u1}, 0},
		{"非法格式計入 invalidCount", []string{u1, "not-a-uuid"}, []string{u1}, 1},
		{"全部非法", []string{"x", "y"}, nil, 2},
		{"空輸入", []string{}, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			valid, invalidCount := dedupeValidUUIDs(c.raw)
			if !reflect.DeepEqual(valid, c.wantValid) {
				t.Fatalf("valid = %v, want %v", valid, c.wantValid)
			}
			if invalidCount != c.wantInvalidCnt {
				t.Fatalf("invalidCount = %d, want %d", invalidCount, c.wantInvalidCnt)
			}
		})
	}
}

// TestBuildDeleteReasons 驗證批次刪除的跳過原因彙總文字：只有被拒（issued）或查無時才出現對應句子，
// 兩者皆 0 時回傳空陣列（而非 nil，供前端穩定渲染 JSON 陣列）。
func TestBuildDeleteReasons(t *testing.T) {
	cases := []struct {
		name                         string
		skippedIssued, skippedNotFnd int
		want                         []string
	}{
		{"皆 0 → 空陣列", 0, 0, []string{}},
		{"只有 issued 被拒", 2, 0, []string{"已發送的序號不可刪除（2 筆）"}},
		{"只有查無", 0, 3, []string{"序號不存在（3 筆）"}},
		{"兩者皆有", 1, 1, []string{"已發送的序號不可刪除（1 筆）", "序號不存在（1 筆）"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildDeleteReasons(c.skippedIssued, c.skippedNotFnd)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("buildDeleteReasons(%d,%d) = %v, want %v", c.skippedIssued, c.skippedNotFnd, got, c.want)
			}
		})
	}
}

// TestBuildNotFoundReasons 供 VoidSerialsBatch 使用：只有「查無此序號」一種跳過原因（void 不限制當前
// 狀態，不會有 issued 被拒的情況）。
func TestBuildNotFoundReasons(t *testing.T) {
	if got := buildNotFoundReasons(0); !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("buildNotFoundReasons(0) = %v, want []", got)
	}
	if got := buildNotFoundReasons(4); !reflect.DeepEqual(got, []string{"序號不存在（4 筆）"}) {
		t.Fatalf("buildNotFoundReasons(4) = %v, want [序號不存在（4 筆）]", got)
	}
}

// TestCanRevive 匯入「復活搬移」（2026-08-29 實案：序號輸錯→註銷→全系統唯一卡死無法重匯）的資格判斷：
// 只有 status='void' 且 issued_to 為 nil（曾註銷、從未發送過玩家）才符合；available／issued 一律不符，
// 即使 issued_to 為 nil（理論上 available 狀態的 issued_to 恆為 nil，仍須以 status 為準不可誤放行）；
// void 但 issued_to 非 nil（先發送後又被註銷，序號已進過玩家的 user_rewards 生命週期）也不可復活，避免
// 誤把曾經發送過的序號搬去別組。
func TestCanRevive(t *testing.T) {
	someUser := "user-1"
	cases := []struct {
		name     string
		status   string
		issuedTo *string
		want     bool
	}{
		{"void 且從未發送 → 可復活", "void", nil, true},
		{"void 但曾發送過（issued_to 非 nil）→ 不可復活", "void", &someUser, false},
		{"available → 不可復活", "available", nil, false},
		{"issued 且 issued_to 非 nil → 不可復活", "issued", &someUser, false},
		{"issued 但 issued_to 意外為 nil（不應發生，仍不可復活，status 優先）", "issued", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := canRevive(c.status, c.issuedTo); got != c.want {
				t.Fatalf("canRevive(%q, %v) = %v, want %v", c.status, c.issuedTo, got, c.want)
			}
		})
	}
}

// TestBundleOrphanErr 轉組合型防孤兒（2026-08-29 實案：序號組轉組合型後序號管理 UI 被隱藏，殘留序號變成
// 看不到的孤兒）：n<=0（無殘留序號）允許轉型回 nil；n>0 回傳含筆數的錯誤訊息，供前端原樣顯示。
func TestBundleOrphanErr(t *testing.T) {
	if err := bundleOrphanErr(0); err != nil {
		t.Fatalf("bundleOrphanErr(0) = %v, want nil", err)
	}
	if err := bundleOrphanErr(-1); err != nil {
		t.Fatalf("bundleOrphanErr(-1) = %v, want nil", err)
	}
	err := bundleOrphanErr(3)
	if err == nil {
		t.Fatalf("bundleOrphanErr(3) = nil, want error")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bundleOrphanErr(3) should wrap ErrInvalidInput, got %v", err)
	}
	wantMsg := "此序號組仍有 3 筆序號，請先刪除或移轉後再轉為組合型"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Fatalf("bundleOrphanErr(3).Error() = %q, want substring %q", err.Error(), wantMsg)
	}
}
