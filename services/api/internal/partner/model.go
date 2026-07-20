// Package partner 跑者充電站（合作商家目錄）：前台列表/詳細/收藏 + 後台 CRUD。
package partner

import "time"

// PartnerShop 前台列表用（僅 enabled=true 的商家會被回傳）。
type PartnerShop struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Summary      string `json:"summary"`
	BannerURL    string `json:"banner_url"`
	CTAURL       string `json:"cta_url"`
	CTALabel     string `json:"cta_label"`
	DisplayOrder int    `json:"display_order"`
	IsFavorited  bool   `json:"is_favorited"`
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
	Enabled      bool     `json:"enabled"`
}
