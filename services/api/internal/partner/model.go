// Package partner 跑者充電站（合作商家目錄）：前台列表/詳細/收藏 + 後台 CRUD。
package partner

import "time"

// PartnerShop 前台列表用（僅 enabled=true 的商家會被回傳；audience='vip_featured' 商家現在對所有人
// 可見，「不合格」改成鎖在 CTA 動作上——見 CtaLocked/CtaLockReason，以及 Service.applyCtaGate）。
type PartnerShop struct {
	ID            string `json:"id"`
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
	DetailHTML string   `json:"detail_html"`
	PhotoURLs  []string `json:"photo_urls"`
	VideoURL   string   `json:"video_url"`
}

// AdminPartnerShop 後台管理用：PartnerShop 欄位（不含 is_favorited）+ 詳細欄位 + enabled。
type AdminPartnerShop struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Summary      string    `json:"summary"`
	BannerURL    string    `json:"banner_url"`
	CTAURL       string    `json:"cta_url"`
	CTALabel     string    `json:"cta_label"`
	DisplayOrder int       `json:"display_order"`
	Audience     string    `json:"audience"` // all | vip_featured
	DetailHTML   string    `json:"detail_html"`
	PhotoURLs    []string  `json:"photo_urls"`
	VideoURL     string    `json:"video_url"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AdminPartnerShopRequest 後台新增/更新請求 body。
type AdminPartnerShopRequest struct {
	Name         string   `json:"name"`
	Summary      string   `json:"summary"`
	BannerURL    string   `json:"banner_url"`
	DetailHTML   string   `json:"detail_html"`
	PhotoURLs    []string `json:"photo_urls"`
	VideoURL     string   `json:"video_url"`
	CTAURL       string   `json:"cta_url"`
	CTALabel     string   `json:"cta_label"`
	DisplayOrder int      `json:"display_order"`
	Audience     string   `json:"audience"` // all | vip_featured；空字串正規化為 all（見 normalizeAndValidate）
	Enabled      bool     `json:"enabled"`
}
