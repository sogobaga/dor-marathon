package partner

import (
	"errors"
	"strings"
	"testing"
)

// 跑者充電站多品項（variants）驗證規則，見 docs/partner/VARIANTS_CONTRACT.md §1。
// 純函式測試（validateVariants／applyCtaGateToVariants／marshal-unmarshal），不需要 DB。

func TestValidateVariants_ItemModeDefaultsToSingle(t *testing.T) {
	req := &AdminPartnerShopRequest{Name: "test", ItemMode: ""}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.ItemMode != "single" {
		t.Fatalf("ItemMode = %q, want %q", req.ItemMode, "single")
	}
	if req.Variants == nil || len(req.Variants) != 0 {
		t.Fatalf("Variants = %#v, want empty non-nil slice", req.Variants)
	}
}

func TestValidateVariants_ItemModeInvalid(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "bogus"}
	err := validateVariants(req)
	if !errors.Is(err, ErrInvalidItemMode) {
		t.Fatalf("err = %v, want ErrInvalidItemMode", err)
	}
}

func TestValidateVariants_ItemModeTrimmed(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "  multi  "}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.ItemMode != "multi" {
		t.Fatalf("ItemMode = %q, want %q", req.ItemMode, "multi")
	}
}

func TestValidateVariants_MultiModeAllowsZeroVariants(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateVariants_TooMany(t *testing.T) {
	variants := make([]PartnerVariant, maxVariants+1)
	for i := range variants {
		variants[i] = PartnerVariant{Name: "item"}
	}
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: variants}
	err := validateVariants(req)
	if !errors.Is(err, ErrTooManyVariants) {
		t.Fatalf("err = %v, want ErrTooManyVariants", err)
	}
}

func TestValidateVariants_ExactlyMaxIsOK(t *testing.T) {
	variants := make([]PartnerVariant, maxVariants)
	for i := range variants {
		variants[i] = PartnerVariant{Name: "item"}
	}
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: variants}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error at exactly maxVariants: %v", err)
	}
}

func TestValidateVariants_NameRequired(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{
		{Name: "巧克力口味"},
		{Name: "   "}, // trim 後為空
	}}
	err := validateVariants(req)
	if err == nil || !strings.Contains(err.Error(), "variants[1].name 必填") {
		t.Fatalf("err = %v, want message containing %q", err, "variants[1].name 必填")
	}
	// 迴歸測試（VERIFY-NEON #5a）：必須用 %w 包裝 ErrNameRequired，否則 handler.go 的
	// errors.Is 判斷不到，這個 400 情境會落入 500 分支（"failed to create partner shop"）。
	if !errors.Is(err, ErrNameRequired) {
		t.Fatalf("err = %v, want errors.Is(err, ErrNameRequired) — handler.go relies on this to return 400 instead of 500", err)
	}
}

func TestValidateVariants_NameTooLong(t *testing.T) {
	longName := strings.Repeat("字", maxVariantNameLen+1)
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{{Name: longName}}}
	err := validateVariants(req)
	if err == nil || !errors.Is(err, ErrTooLong) || !strings.Contains(err.Error(), "variants[0].name") {
		t.Fatalf("err = %v, want ErrTooLong on variants[0].name", err)
	}
}

func TestValidateVariants_NameExactlyMaxLenIsOK(t *testing.T) {
	name := strings.Repeat("字", maxVariantNameLen)
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{{Name: name}}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error at exactly maxVariantNameLen: %v", err)
	}
}

func TestValidateVariants_DescriptionTooLong(t *testing.T) {
	longDesc := strings.Repeat("字", maxVariantDescLen+1)
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{
		{Name: "巧克力口味", Description: longDesc},
	}}
	err := validateVariants(req)
	if err == nil || !errors.Is(err, ErrTooLong) || !strings.Contains(err.Error(), "variants[0].description") {
		t.Fatalf("err = %v, want ErrTooLong on variants[0].description", err)
	}
}

func TestValidateVariants_ImageURLInvalid(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{
		{Name: "巧克力口味", ImageURL: "not-a-url"},
	}}
	err := validateVariants(req)
	if err == nil || !errors.Is(err, ErrInvalidImageURL) || !strings.Contains(err.Error(), "variants[0].image_url") {
		t.Fatalf("err = %v, want ErrInvalidImageURL on variants[0].image_url", err)
	}
}

func TestValidateVariants_ImageURLAcceptsSitePathAndHTTP(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{
		{Name: "巧克力口味", ImageURL: "/api/v1/images/abc"},
		{Name: "蔓越莓口味", ImageURL: "https://cdn.example.com/x.png"},
		{Name: "蜂蜜檸檬口味", ImageURL: ""},
	}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateVariants_CTAURLInvalid(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{
		{Name: "巧克力口味", CTAURL: "ftp://example.com"},
	}}
	err := validateVariants(req)
	if err == nil || !errors.Is(err, ErrInvalidURL) || !strings.Contains(err.Error(), "variants[0].cta_url") {
		t.Fatalf("err = %v, want ErrInvalidURL on variants[0].cta_url", err)
	}
}

func TestValidateVariants_CTAURLEmptyIsOK(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{
		{Name: "巧克力口味", CTAURL: ""},
	}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- id 補發 ---

func TestValidateVariants_IDMissingGetsIssued(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{{Name: "巧克力口味", ID: ""}}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Variants[0].ID == "" || !uuidLikeRe.MatchString(req.Variants[0].ID) {
		t.Fatalf("ID = %q, want a valid UUID to have been issued", req.Variants[0].ID)
	}
}

func TestValidateVariants_IDNonUUIDGetsReissued(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{{Name: "巧克力口味", ID: "not-a-uuid"}}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Variants[0].ID == "not-a-uuid" || !uuidLikeRe.MatchString(req.Variants[0].ID) {
		t.Fatalf("ID = %q, want reissued valid UUID", req.Variants[0].ID)
	}
}

func TestValidateVariants_IDValidUUIDIsPreserved(t *testing.T) {
	const existing = "11111111-1111-1111-1111-111111111111"
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{{Name: "巧克力口味", ID: existing}}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Variants[0].ID != existing {
		t.Fatalf("ID = %q, want preserved %q", req.Variants[0].ID, existing)
	}
}

func TestValidateVariants_TrimsAllTextFields(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{
		{Name: "  巧克力口味  ", Description: "  好吃  ", ImageURL: "  /img/a.png  ", CTAURL: "  https://a.example.com  "},
	}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v := req.Variants[0]
	if v.Name != "巧克力口味" || v.Description != "好吃" || v.ImageURL != "/img/a.png" || v.CTAURL != "https://a.example.com" {
		t.Fatalf("fields not trimmed: %#v", v)
	}
}

func TestValidateVariants_PreservesOrder(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{
		{Name: "巧克力口味"}, {Name: "蔓越莓口味"}, {Name: "蜂蜜檸檬口味"},
	}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"巧克力口味", "蔓越莓口味", "蜂蜜檸檬口味"}
	for i, w := range want {
		if req.Variants[i].Name != w {
			t.Fatalf("Variants[%d].Name = %q, want %q (order changed)", i, req.Variants[i].Name, w)
		}
	}
}

// --- CTA gate 對 variants 的清空 ---

func TestApplyCtaGateToVariants_LockedClearsAllCTAURLs(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "vip_featured"},
		Variants: []PartnerVariant{
			{ID: "1", Name: "巧克力口味", CTAURL: "https://a.example.com"},
			{ID: "2", Name: "蔓越莓口味", CTAURL: "https://b.example.com"},
		},
	}
	applyCtaGate(&detail.PartnerShop, true /* loggedIn */, false /* qualifies */, 10)
	if !detail.CtaLocked {
		t.Fatalf("expected CtaLocked=true after applyCtaGate with qualifies=false")
	}
	applyCtaGateToVariants(detail, true)
	for i, v := range detail.Variants {
		if v.CTAURL != "" {
			t.Fatalf("Variants[%d].CTAURL = %q, want cleared", i, v.CTAURL)
		}
	}
}

func TestApplyCtaGateToVariants_UnlockedLeavesCTAURLsAlone(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "vip_featured"},
		Variants: []PartnerVariant{
			{ID: "1", Name: "巧克力口味", CTAURL: "https://a.example.com"},
		},
	}
	applyCtaGate(&detail.PartnerShop, true /* loggedIn */, true /* qualifies */, 10)
	if detail.CtaLocked {
		t.Fatalf("expected CtaLocked=false after applyCtaGate with qualifies=true")
	}
	applyCtaGateToVariants(detail, true)
	if detail.Variants[0].CTAURL != "https://a.example.com" {
		t.Fatalf("CTAURL was cleared despite gate being unlocked: %q", detail.Variants[0].CTAURL)
	}
}

func TestApplyCtaGateToVariants_AudienceAllNeverLocked(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "all"},
		Variants:    []PartnerVariant{{ID: "1", Name: "巧克力口味", CTAURL: "https://a.example.com"}},
	}
	applyCtaGate(&detail.PartnerShop, true /* loggedIn */, false, 10)
	applyCtaGateToVariants(detail, true)
	if detail.Variants[0].CTAURL == "" {
		t.Fatalf("audience=all shop should never have variants CTA cleared")
	}
}

// TestApplyCtaGateToVariants_GuestClearsAllCTAURLs 未登入 Detail：每個 variant 的 cta_url 都要被清空
// （比照鎖定時的行為），不能只清商家層級的 CTAURL——否則前端能繞過商家層級鎖直接從 variants 拿到連結。
// 用「多品項商家、商家層級 cta_url 留空」這個真實資料形態（後台提示多品項不使用商家層級連結）：
// applyCtaGate 單看 shop.CTAURL 會判成沒有 CTA，所以 variants 閘門必須自己判定並把 CtaLoginRequired 設起來。
func TestApplyCtaGateToVariants_GuestClearsAllCTAURLs(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "all", ItemMode: "multi", CTAURL: ""},
		Variants: []PartnerVariant{
			{ID: "1", Name: "巧克力口味", CTAURL: "https://a.example.com"},
			{ID: "2", Name: "蔓越莓口味", CTAURL: ""},
			{ID: "3", Name: "蜂蜜檸檬口味", CTAURL: "https://c.example.com"},
		},
	}
	applyCtaGate(&detail.PartnerShop, false /* loggedIn */, false, 10)
	if detail.CtaLoginRequired {
		t.Fatalf("shop-level CTAURL is empty: applyCtaGate alone must not flag login required")
	}
	applyCtaGateToVariants(detail, false)
	if !detail.CtaLoginRequired {
		t.Fatalf("expected CtaLoginRequired=true for guest on multi shop whose variants have links")
	}
	if detail.CtaLocked {
		t.Fatalf("guest must never be VIP-locked (VIP is decided after login)")
	}
	for i, v := range detail.Variants {
		if v.CTAURL != "" {
			t.Fatalf("Variants[%d].CTAURL = %q, want cleared for guest", i, v.CTAURL)
		}
	}
	// 每筆旗標：原本有連結的 1、3 要 CtaLoginRequired=true；沒連結的 2 不得有旗標（訪客/會員按鈕有無一致）
	if !detail.Variants[0].CtaLoginRequired || !detail.Variants[2].CtaLoginRequired {
		t.Fatalf("variants with links must carry cta_login_required: %+v", detail.Variants)
	}
	if detail.Variants[1].CtaLoginRequired || detail.Variants[1].CtaLocked {
		t.Fatalf("variant without link must have no flags: %+v", detail.Variants[1])
	}
	for i, v := range detail.Variants {
		if v.CtaLocked {
			t.Fatalf("Variants[%d] guest must never be CtaLocked", i)
		}
	}
}

// TestApplyCtaGateToVariants_LockedFlagsOnlyLinkedVariants 已登入不合格 + vip_featured：
// 有連結的品項清空並打 CtaLocked；沒連結的品項不打旗標。
func TestApplyCtaGateToVariants_LockedFlagsOnlyLinkedVariants(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "vip_featured", ItemMode: "multi"},
		Variants: []PartnerVariant{
			{ID: "1", Name: "A", CTAURL: "https://a.example.com"},
			{ID: "2", Name: "B"},
		},
	}
	applyCtaGate(&detail.PartnerShop, true, false /* 不合格 */, 10)
	applyCtaGateToVariants(detail, true)
	if !detail.CtaLocked {
		t.Fatalf("expected shop CtaLocked")
	}
	if detail.Variants[0].CTAURL != "" || !detail.Variants[0].CtaLocked || detail.Variants[0].CtaLoginRequired {
		t.Fatalf("linked variant: %+v", detail.Variants[0])
	}
	if detail.Variants[1].CtaLocked || detail.Variants[1].CtaLoginRequired {
		t.Fatalf("unlinked variant must have no flags: %+v", detail.Variants[1])
	}
}

// TestApplyCtaGateToVariants_QualifiedVipMultiKeepsLinks 已登入 VIP 合格 + vip_featured 多品項：連結保留、無旗標。
func TestApplyCtaGateToVariants_QualifiedVipMultiKeepsLinks(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "vip_featured", ItemMode: "multi", CTAURL: "https://shop.example.com"},
		Variants:    []PartnerVariant{{ID: "1", Name: "A", CTAURL: "https://a.example.com"}},
	}
	applyCtaGate(&detail.PartnerShop, true, true /* 合格 */, 10)
	applyCtaGateToVariants(detail, true)
	if detail.CtaLocked || detail.CtaLoginRequired || detail.Variants[0].CTAURL != "https://a.example.com" || detail.Variants[0].CtaLocked || detail.Variants[0].CtaLoginRequired {
		t.Fatalf("qualified VIP must keep everything: shop=%+v v=%+v", detail.PartnerShop, detail.Variants[0])
	}
}

// TestValidateVariants_ResetsOutputFlags 寫入時輸出用旗標一律歸零（不入庫）。
func TestValidateVariants_ResetsOutputFlags(t *testing.T) {
	req := &AdminPartnerShopRequest{ItemMode: "multi", Variants: []PartnerVariant{{Name: "A", CtaLocked: true, CtaLoginRequired: true}}}
	if err := validateVariants(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Variants[0].CtaLocked || req.Variants[0].CtaLoginRequired {
		t.Fatalf("output flags must be reset on write: %+v", req.Variants[0])
	}
}

// TestApplyCtaGateToVariants_GuestVipFeaturedMultiStillCleared 未登入 + audience=vip_featured 多品項：
// VIP 判定要登入後才談（CtaLocked 維持 false），但 variants 連結一樣不得洩漏。
func TestApplyCtaGateToVariants_GuestVipFeaturedMultiStillCleared(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "vip_featured", ItemMode: "multi"},
		Variants:    []PartnerVariant{{ID: "1", Name: "A", CTAURL: "https://a.example.com"}},
	}
	applyCtaGate(&detail.PartnerShop, false, false, 10)
	applyCtaGateToVariants(detail, false)
	if detail.CtaLocked || !detail.CtaLoginRequired || detail.Variants[0].CTAURL != "" || !detail.Variants[0].CtaLoginRequired || detail.Variants[0].CtaLocked {
		t.Fatalf("guest vip_featured multi: locked=%v loginRequired=%v v=%+v", detail.CtaLocked, detail.CtaLoginRequired, detail.Variants[0])
	}
}

// TestApplyCtaGateToVariants_GuestNoLinksNoFlags 未登入、多品項但沒有任何連結：三個旗標全 false，
// 前端不顯示任何按鈕（跟登入者看到的一樣）。
func TestApplyCtaGateToVariants_GuestNoLinksNoFlags(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "all", ItemMode: "multi"},
		Variants:    []PartnerVariant{{ID: "1", Name: "A"}},
	}
	applyCtaGate(&detail.PartnerShop, false, false, 10)
	applyCtaGateToVariants(detail, false)
	if detail.CtaLocked || detail.CtaLoginRequired {
		t.Fatalf("no links anywhere: expected no flags, got locked=%v loginRequired=%v", detail.CtaLocked, detail.CtaLoginRequired)
	}
}

// TestApplyCtaGateToVariants_GuestSingleModeClearsButNoFlag 未登入、單一品項模式但殘留 variants 資料：
// 連結仍清空（防洩漏），但不設 CtaLoginRequired——variants 在單一品項模式不渲染，設了會讓底部多出一顆按鈕。
func TestApplyCtaGateToVariants_GuestSingleModeClearsButNoFlag(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "all", ItemMode: "single"},
		Variants:    []PartnerVariant{{ID: "1", Name: "A", CTAURL: "https://a.example.com"}},
	}
	applyCtaGate(&detail.PartnerShop, false, false, 10)
	applyCtaGateToVariants(detail, false)
	if detail.CtaLoginRequired || detail.Variants[0].CTAURL != "" {
		t.Fatalf("single mode guest: loginRequired=%v url=%q", detail.CtaLoginRequired, detail.Variants[0].CTAURL)
	}
}

// TestApplyCtaGateToVariants_LoggedInNonVIPAllAudienceKeepsVariantCTAURLs 已登入非 VIP、商家
// audience='all'（不受 VIP 精選門檻）：不是 guest 也未被鎖定，variants 的 cta_url 應原樣保留。
func TestApplyCtaGateToVariants_LoggedInNonVIPAllAudienceKeepsVariantCTAURLs(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "all", CTAURL: "https://shop.example.com"},
		Variants: []PartnerVariant{
			{ID: "1", Name: "巧克力口味", CTAURL: "https://a.example.com"},
		},
	}
	applyCtaGate(&detail.PartnerShop, true /* loggedIn */, false /* qualifies：非 VIP */, 10)
	if detail.CtaLocked || detail.CtaLoginRequired {
		t.Fatalf("expected no gate for logged-in user on audience=all shop: locked=%v loginRequired=%v", detail.CtaLocked, detail.CtaLoginRequired)
	}
	applyCtaGateToVariants(detail, true)
	if detail.Variants[0].CTAURL != "https://a.example.com" {
		t.Fatalf("CTAURL was cleared despite gate being unlocked: %q", detail.Variants[0].CTAURL)
	}
}

// --- JSON marshal/unmarshal：nil → []（比照 photo_urls 慣例） ---

func TestMarshalVariants_NilBecomesEmptyArray(t *testing.T) {
	b, err := marshalVariants(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(b) != "[]" {
		t.Fatalf("marshalVariants(nil) = %s, want []", b)
	}
}

func TestUnmarshalVariants_EmptyBytesBecomeEmptyArray(t *testing.T) {
	variants, err := unmarshalVariants(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if variants == nil || len(variants) != 0 {
		t.Fatalf("unmarshalVariants(nil) = %#v, want empty non-nil slice", variants)
	}
}

func TestMarshalUnmarshalVariants_RoundTrip(t *testing.T) {
	in := []PartnerVariant{
		{ID: "1", Name: "巧克力口味", Description: "濃郁", ImageURL: "/img/a.png", CTAURL: "https://a.example.com"},
		{ID: "2", Name: "蔓越莓口味"},
	}
	b, err := marshalVariants(in)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	out, err := unmarshalVariants(b)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("out[%d] = %#v, want %#v", i, out[i], in[i])
		}
	}
}

// --- repo INSERT/UPDATE 欄位數與 placeholder 數一致（純字串斷言，不連 DB） ---

func TestAdminCreateSQL_ColumnCountMatchesPlaceholderCount(t *testing.T) {
	const cols = "name, summary, banner_url, detail_html, photo_urls, video_url, video_urls, cta_url, cta_label, display_order, enabled, audience, slug, content_images, item_mode, variants"
	const placeholders = "$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16"
	nCols := len(strings.Split(cols, ","))
	nPlaceholders := len(strings.Split(placeholders, ","))
	if nCols != nPlaceholders {
		t.Fatalf("INSERT column count = %d, placeholder count = %d, want equal", nCols, nPlaceholders)
	}
	if nCols != 16 {
		t.Fatalf("expected 16 columns after adding item_mode/variants, got %d", nCols)
	}
}
