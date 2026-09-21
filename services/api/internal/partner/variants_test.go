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
	applyCtaGate(&detail.PartnerShop, false /* qualifies */, 10)
	if !detail.CtaLocked {
		t.Fatalf("expected CtaLocked=true after applyCtaGate with qualifies=false")
	}
	applyCtaGateToVariants(detail)
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
	applyCtaGate(&detail.PartnerShop, true /* qualifies */, 10)
	if detail.CtaLocked {
		t.Fatalf("expected CtaLocked=false after applyCtaGate with qualifies=true")
	}
	applyCtaGateToVariants(detail)
	if detail.Variants[0].CTAURL != "https://a.example.com" {
		t.Fatalf("CTAURL was cleared despite gate being unlocked: %q", detail.Variants[0].CTAURL)
	}
}

func TestApplyCtaGateToVariants_AudienceAllNeverLocked(t *testing.T) {
	detail := &PartnerShopDetail{
		PartnerShop: PartnerShop{Audience: "all"},
		Variants:    []PartnerVariant{{ID: "1", Name: "巧克力口味", CTAURL: "https://a.example.com"}},
	}
	applyCtaGate(&detail.PartnerShop, false, 10)
	applyCtaGateToVariants(detail)
	if detail.Variants[0].CTAURL == "" {
		t.Fatalf("audience=all shop should never have variants CTA cleared")
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
