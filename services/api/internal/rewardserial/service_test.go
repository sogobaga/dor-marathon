package rewardserial

import "testing"

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
