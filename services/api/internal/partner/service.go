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
	ErrTooLong      = errors.New("field exceeds maximum length")
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
	if !validHTTPURL(req.BannerURL) {
		return fmt.Errorf("banner_url: %w", ErrInvalidURL)
	}
	if req.PhotoURLs == nil {
		req.PhotoURLs = []string{}
	}
	for i, u := range req.PhotoURLs {
		if !validHTTPURL(u) {
			return fmt.Errorf("photo_urls[%d]: %w", i, ErrInvalidURL)
		}
	}
	req.DetailHTML = SanitizeDetailHTML(req.DetailHTML)
	return nil
}

// validHTTPURL 空字串允許；非空時須為 http/https 且有 host。
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
