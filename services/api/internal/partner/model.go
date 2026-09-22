// Package partner 跑者充電站（合作商家目錄）：前台列表/詳細/收藏 + 後台 CRUD。
package partner

import "time"

// PartnerVariant 單一商家底下的細項商品（多品項模式，見 docs/partner/VARIANTS_CONTRACT.md）：
// 例如同系列不同口味。ID 由伺服器產生（Service.validateVariants 補發缺漏/非法者），前端可回傳
// 既有 id 以保留關聯；陣列順序＝顯示順序（後台調整順序後整包 PUT）。
type PartnerVariant struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
	CTAURL      string `json:"cta_url"`
	// 以下兩個是「輸出用」旗標（前台 Detail 才有意義；寫入時 validateVariants 一律歸零、不存進 JSONB）：
	// 該品項原本有連結、但因未登入／VIP 鎖定被伺服器清空 → 前台據此顯示「立即登入」／「🔒 前往」。
	// 沒連結的品項兩者皆 false，訪客與會員看到的按鈕有無才會一致（2026-09-22 複審抓到）。
	CtaLocked        bool `json:"cta_locked,omitempty"`
	CtaLoginRequired bool `json:"cta_login_required,omitempty"`
}

// PartnerShop 前台列表用（僅 enabled=true 的商家會被回傳；audience='vip_featured' 商家現在對所有人
// 可見，「不合格」改成鎖在 CTA 動作上——見 CtaLocked/CtaLockReason，以及 Service.applyCtaGate）。
type PartnerShop struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"` // 自訂連結代碼（選填）；有值時 /shop/{slug} 可取代 /shop/{id}，見 Repository.GetDetail
	Name          string `json:"name"`
	Summary       string `json:"summary"`
	BannerURL     string `json:"banner_url"`
	CTAURL        string `json:"cta_url"`
	CTALabel      string `json:"cta_label"`
	DisplayOrder  int    `json:"display_order"`
	Audience      string `json:"audience"` // all | vip_featured
	IsFavorited   bool   `json:"is_favorited"`
	CtaLocked     bool   `json:"cta_locked"`      // true：audience=='vip_featured' 且使用者不合格；此時 CTAURL 已被清空
	CtaLockReason string `json:"cta_lock_reason"` // CtaLocked=true 時的鎖定原因說明；否則空字串
	// CtaLoginRequired true：未登入（uid 空字串）且商家有設定前往連結；此時 CTAURL 已被清空、CtaLocked=false。
	// 前台把「前往」換成「立即登入」（優惠是會員才享有，見 Service.applyCtaGate）。登入後重新請求即回到一般判定。
	CtaLoginRequired bool   `json:"cta_login_required"`
	ItemMode         string `json:"item_mode"` // single | multi；多品項時前台入口卡只留「詳細」（見 apps/web ShopCard）
}

// PartnerListMeta 隨列表一併回傳的 VIP 精選資格資訊，供前端顯示鎖定提示卡；
// 刻意不在使用者不合格時多回傳任何 vip_featured 商家內容，只給數量。
type PartnerListMeta struct {
	IsVIP            bool    `json:"is_vip"`
	UserKm           float64 `json:"user_km"`
	MinKm            int     `json:"min_km"`
	Qualifies        bool    `json:"qualifies"`
	VIPFeaturedCount int     `json:"vip_featured_count"` // 全站 enabled 的 vip_featured 商家總數（不論本次是否回傳其內容）
}

// PartnerShopDetail 前台詳細頁用；DetailHTML 已由後端消毒過（寫入 + 輸出皆消毒）。
type PartnerShopDetail struct {
	PartnerShop
	DetailHTML    string           `json:"detail_html"`
	PhotoURLs     []string         `json:"photo_urls"`
	VideoURL      string           `json:"video_url"` // 保留（back-compat），新版請用 VideoURLs
	VideoURLs     []string         `json:"video_urls"`
	ContentImages []string         `json:"content_images"` // 滿版長圖（產品 DM／長圖）；詳細頁滿版直列顯示，與 PhotoURLs 輪播分開
	Variants      []PartnerVariant `json:"variants"`       // 多品項（item_mode=='multi'）；一律回陣列，nil→[]（見 marshal/unmarshalVariants）
}

// AdminPartnerShop 後台管理用：PartnerShop 欄位（不含 is_favorited）+ 詳細欄位 + enabled。
type AdminPartnerShop struct {
	ID            string           `json:"id"`
	Slug          string           `json:"slug"` // 自訂連結代碼（選填）；空字串＝未設定
	Name          string           `json:"name"`
	Summary       string           `json:"summary"`
	BannerURL     string           `json:"banner_url"`
	CTAURL        string           `json:"cta_url"`
	CTALabel      string           `json:"cta_label"`
	DisplayOrder  int              `json:"display_order"`
	Audience      string           `json:"audience"` // all | vip_featured
	DetailHTML    string           `json:"detail_html"`
	PhotoURLs     []string         `json:"photo_urls"`
	VideoURL      string           `json:"video_url"` // 保留（back-compat），新版請用 VideoURLs
	VideoURLs     []string         `json:"video_urls"`
	ContentImages []string         `json:"content_images"` // 滿版長圖（產品 DM／長圖）；詳細頁滿版直列顯示，與 PhotoURLs 輪播分開
	ItemMode      string           `json:"item_mode"`      // single | multi；預設 single（既有商家零改動）
	Variants      []PartnerVariant `json:"variants"`       // 多品項細項商品；一律回陣列，nil→[]
	Enabled       bool             `json:"enabled"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// AdminPartnerShopRequest 後台新增/更新請求 body。
type AdminPartnerShopRequest struct {
	Slug          string           `json:"slug"` // 自訂連結代碼（選填）；空字串正規化為 NULL（見 Repository.AdminCreate/AdminUpdate）
	Name          string           `json:"name"`
	Summary       string           `json:"summary"`
	BannerURL     string           `json:"banner_url"`
	DetailHTML    string           `json:"detail_html"`
	PhotoURLs     []string         `json:"photo_urls"`
	VideoURL      string           `json:"video_url"` // 保留（back-compat），新版請用 VideoURLs
	VideoURLs     []string         `json:"video_urls"`
	ContentImages []string         `json:"content_images"` // 滿版長圖（產品 DM／長圖）；詳細頁滿版直列顯示，與 PhotoURLs 輪播分開
	CTAURL        string           `json:"cta_url"`
	CTALabel      string           `json:"cta_label"`
	DisplayOrder  int              `json:"display_order"`
	Audience      string           `json:"audience"`  // all | vip_featured；空字串正規化為 all（見 normalizeAndValidate）
	ItemMode      string           `json:"item_mode"` // single | multi；空字串正規化為 single（見 Service.validateVariants）
	Variants      []PartnerVariant `json:"variants"`  // 多品項細項商品；nil 視同 []
	Enabled       bool             `json:"enabled"`
}
