package partner

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	ErrNameRequired = errors.New("name is required")
	ErrInvalidURL   = errors.New("url must be http or https")
	// 圖片欄位另給一則訊息：它同時接受站內相對路徑，沿用上面那句會誤導後台使用者。
	ErrInvalidImageURL = errors.New("image url must be a site path (/...) or http/https")
	ErrTooLong         = errors.New("field exceeds maximum length")
)

// DB VARCHAR 上限（migrations/091_partner_shops.sql）：超過就在寫入前擋掉，
// 不要讓 Postgres 丟出無意義的 500。用 []rune 算字數，避免用 byte 切壞 UTF-8 中文。
const (
	maxNameLen     = 200
	maxSummaryLen  = 300
	maxCTALabelLen = 50
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// --- 前台 ---

func (s *Service) ListEnabled(ctx context.Context, uid string) ([]*PartnerShop, error) {
	return s.repo.ListEnabled(ctx, uid)
}

func (s *Service) GetDetail(ctx context.Context, id, uid string) (*PartnerShopDetail, error) {
	return s.repo.GetDetail(ctx, id, uid)
}

// --- 收藏 ---

func (s *Service) AddFavorite(ctx context.Context, userID, shopID string) error {
	return s.repo.AddFavorite(ctx, userID, shopID)
}

func (s *Service) RemoveFavorite(ctx context.Context, userID, shopID string) error {
	return s.repo.RemoveFavorite(ctx, userID, shopID)
}

// --- 後台 ---

func (s *Service) AdminList(ctx context.Context) ([]*AdminPartnerShop, error) {
	return s.repo.AdminList(ctx)
}

func (s *Service) AdminCreate(ctx context.Context, req *AdminPartnerShopRequest) (*AdminPartnerShop, error) {
	if err := normalizeAndValidate(req); err != nil {
		return nil, err
	}
	return s.repo.AdminCreate(ctx, req)
}

func (s *Service) AdminUpdate(ctx context.Context, id string, req *AdminPartnerShopRequest) (*AdminPartnerShop, error) {
	if err := normalizeAndValidate(req); err != nil {
		return nil, err
	}
	return s.repo.AdminUpdate(ctx, id, req)
}

func (s *Service) AdminDelete(ctx context.Context, id string) error {
	return s.repo.AdminDelete(ctx, id)
}

// normalizeAndValidate 檢查必填 + 長度上限 + URL 格式，並在寫入前消毒 detail_html（存進 DB 就是乾淨的）。
func normalizeAndValidate(req *AdminPartnerShopRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return ErrNameRequired
	}
	if n := len([]rune(req.Name)); n > maxNameLen {
		return fmt.Errorf("name: %w（上限 %d 字，目前 %d 字）", ErrTooLong, maxNameLen, n)
	}
	if n := len([]rune(req.Summary)); n > maxSummaryLen {
		return fmt.Errorf("summary: %w（上限 %d 字，目前 %d 字）", ErrTooLong, maxSummaryLen, n)
	}
	if n := len([]rune(req.CTALabel)); n > maxCTALabelLen {
		return fmt.Errorf("cta_label: %w（上限 %d 字，目前 %d 字）", ErrTooLong, maxCTALabelLen, n)
	}
	if !validHTTPURL(req.CTAURL) {
		return fmt.Errorf("cta_url: %w", ErrInvalidURL)
	}
	if !validHTTPURL(req.VideoURL) {
		return fmt.Errorf("video_url: %w", ErrInvalidURL)
	}
	if !validImageURL(req.BannerURL) {
		return fmt.Errorf("banner_url: %w", ErrInvalidImageURL)
	}
	if req.PhotoURLs == nil {
		req.PhotoURLs = []string{}
	}
	for i, u := range req.PhotoURLs {
		if !validImageURL(u) {
			return fmt.Errorf("photo_urls[%d]: %w", i, ErrInvalidImageURL)
		}
	}
	req.DetailHTML = SanitizeDetailHTML(req.DetailHTML)
	return nil
}

// validImageURL 圖片欄位（banner_url／photo_urls）專用。除了 http/https 絕對網址外，**也允許站內
// 相對路徑**——後台圖片上傳（POST /admin/images）回傳的正是 `/api/v1/images/{id}` 這種相對路徑，
// 若比照 cta_url 只收 http/https，自家上傳的圖片會全部被擋（曾發生：banner_url: url must be http or https）。
// 刻意排除 `//` 開頭的 protocol-relative 網址：它看似相對路徑、實際指向外部主機，會繞過「相對路徑＝
// 站內資源」的信任前提。
func validImageURL(raw string) bool {
	if raw == "" {
		return true
	}
	if strings.HasPrefix(raw, "//") {
		return false
	}
	if strings.HasPrefix(raw, "/") {
		return true
	}
	return validHTTPURL(raw)
}

// validHTTPURL 空字串允許；非空時須為 http/https 且有 host。外部連結（cta_url／video_url）用。
func validHTTPURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}
