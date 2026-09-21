package partner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrNameRequired = errors.New("name is required")
	ErrInvalidURL   = errors.New("url must be http or https")
	// 圖片欄位另給一則訊息：它同時接受站內相對路徑，沿用上面那句會誤導後台使用者。
	ErrInvalidImageURL = errors.New("image url must be a site path (/...) or http/https")
	ErrTooLong         = errors.New("field exceeds maximum length")
	ErrInvalidAudience = errors.New("audience must be all or vip_featured")
	ErrInvalidMinKm    = errors.New("min_km must be a non-negative integer")
	ErrInvalidSlug     = errors.New("slug 僅能用小寫英數與連字號，長度 2-64 字且開頭結尾須為英數字")
	ErrInvalidItemMode = errors.New("item_mode must be single or multi")
	ErrTooManyVariants = errors.New("variants 最多 50 筆")
)

// slugRe 自訂連結代碼格式：小寫英數與連字號、開頭結尾須為英數字、長度 2-64
// （比照活動 slug／internal/race 的 /event/{slug} 慣例）。
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// uuidLikeRe 粗擋 UUID 格式的 slug：GetDetail 用同一個路徑參數雙查 id/slug
// （見 Repository.GetDetail 的 `ps.id::text = $1 OR ps.slug = $1`），slug 若長得像 UUID 會混淆兩者。
var uuidLikeRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// DB VARCHAR 上限（migrations/091_partner_shops.sql）：超過就在寫入前擋掉，
// 不要讓 Postgres 丟出無意義的 500。用 []rune 算字數，避免用 byte 切壞 UTF-8 中文。
const (
	maxNameLen     = 200
	maxSummaryLen  = 300
	maxCTALabelLen = 50
)

// 多品項（variants）欄位上限，見 docs/partner/VARIANTS_CONTRACT.md §1。
const (
	maxVariants       = 50
	maxVariantNameLen = 60
	maxVariantDescLen = 300
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// --- 前台 ---

// vipFeaturedEligibility 判定使用者是否符合 VIP 精選資格：VIP 身分（vip_expires_at > now）
// 且累積里程（users.total_km）達後台門檻（partner_vip_featured_min_km，預設 10km）。
// uid 空字串（未登入）一律不合格。
func (s *Service) vipFeaturedEligibility(ctx context.Context, uid string) (isVIP bool, km float64, minKm int, qualifies bool, err error) {
	isVIP, km, err = s.repo.UserVIPKm(ctx, uid)
	if err != nil {
		return false, 0, 0, false, err
	}
	minKm = s.repo.MinVIPFeaturedKm(ctx)
	qualifies = isVIP && km >= float64(minKm)
	return isVIP, km, minKm, qualifies, nil
}

// ctaLockReason 組出 CtaLocked=true 時的鎖定原因說明；minKm 來自 app_settings，不可寫死。
func ctaLockReason(minKm int) string {
	return fmt.Sprintf("欲前往須滿足 VIP 會員身分，及累積跑步里程至少 %dKM", minKm)
}

// applyCtaGate 把「是否合格」套用到單一 shop 的 CTA 動作上：audience='vip_featured' 且不合格時，
// 鎖住前往按鈕（CtaLocked=true + 原因文案）並清空 CTAURL（防前端被繞過直接讀連結）；
// 商家本身內容（名稱/簡介/圖片/detail_html）一律對所有人可見，不受此影響。
func applyCtaGate(shop *PartnerShop, qualifies bool, minKm int) {
	if shop.Audience == "vip_featured" && !qualifies {
		shop.CtaLocked = true
		shop.CtaLockReason = ctaLockReason(minKm)
		shop.CTAURL = ""
		return
	}
	shop.CtaLocked = false
	shop.CtaLockReason = ""
}

// ListEnabled 回傳前台商家列表 + 資格 meta。現在所有 audience='vip_featured' 商家內容對所有人可見；
// 不合格者只在該商家的「前往」動作上被鎖住（見 applyCtaGate），meta 仍用於區塊頂端門檻說明。
func (s *Service) ListEnabled(ctx context.Context, uid string) ([]*PartnerShop, *PartnerListMeta, error) {
	isVIP, km, minKm, qualifies, err := s.vipFeaturedEligibility(ctx, uid)
	if err != nil {
		return nil, nil, err
	}
	shops, err := s.repo.ListEnabled(ctx, uid)
	if err != nil {
		return nil, nil, err
	}
	for _, shop := range shops {
		applyCtaGate(shop, qualifies, minKm)
	}
	vipCount, err := s.repo.CountEnabledByAudience(ctx, "vip_featured")
	if err != nil {
		return nil, nil, err
	}
	meta := &PartnerListMeta{
		IsVIP:            isVIP,
		UserKm:           math.Round(km*100) / 100,
		MinKm:            minKm,
		Qualifies:        qualifies,
		VIPFeaturedCount: vipCount,
	}
	return shops, meta, nil
}

// GetDetail 前台詳細；vip_featured 商家內容現在對所有人可見，不合格者只在 CTA 動作上被鎖住
// （見 applyCtaGate）。
func (s *Service) GetDetail(ctx context.Context, id, uid string) (*PartnerShopDetail, error) {
	_, _, minKm, qualifies, err := s.vipFeaturedEligibility(ctx, uid)
	if err != nil {
		return nil, err
	}
	detail, err := s.repo.GetDetail(ctx, id, uid)
	if err != nil {
		return nil, err
	}
	applyCtaGate(&detail.PartnerShop, qualifies, minKm)
	applyCtaGateToVariants(detail)
	return detail, nil
}

// applyCtaGateToVariants 把 applyCtaGate 鎖在商家層級的同一把鎖，也套用到每個 variant 的前往連結：
// 伺服器端真 gate，避免前端被繞過直接讀連結（見 docs/partner/VARIANTS_CONTRACT.md §1「CTA gate 擴充」）。
// 必須在 applyCtaGate(&detail.PartnerShop, ...) 之後呼叫，讀它算出的 CtaLocked。
func applyCtaGateToVariants(detail *PartnerShopDetail) {
	if !detail.CtaLocked {
		return
	}
	for i := range detail.Variants {
		detail.Variants[i].CTAURL = ""
	}
}

// --- 收藏 ---

// AddFavorite 收藏不受 VIP 精選資格限制，所有人皆可收藏任何 enabled 商家。
func (s *Service) AddFavorite(ctx context.Context, userID, shopID string) error {
	return s.repo.AddFavorite(ctx, userID, shopID)
}

func (s *Service) RemoveFavorite(ctx context.Context, userID, shopID string) error {
	return s.repo.RemoveFavorite(ctx, userID, shopID)
}

// --- VIP 精選門檻（後台） ---

func (s *Service) MinVIPFeaturedKm(ctx context.Context) int {
	return s.repo.MinVIPFeaturedKm(ctx)
}

func (s *Service) SetMinVIPFeaturedKm(ctx context.Context, km int) error {
	if km < 0 {
		return ErrInvalidMinKm
	}
	return s.repo.SetMinVIPFeaturedKm(ctx, km)
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
	// slug（自訂連結代碼）：空字串＝不設定，允許；非空時需符合格式且不得長得像 UUID
	// （避免與 GetDetail 的 id/slug 雙查混淆，見 uuidLikeRe 註解）。
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Slug != "" && (uuidLikeRe.MatchString(req.Slug) || !slugRe.MatchString(req.Slug)) {
		return ErrInvalidSlug
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
	if req.VideoURLs == nil {
		req.VideoURLs = []string{}
	}
	for i, u := range req.VideoURLs {
		if !validHTTPURL(u) {
			return fmt.Errorf("video_urls[%d]: %w", i, ErrInvalidURL)
		}
	}
	if !validImageURL(req.BannerURL) {
		return fmt.Errorf("banner_url: %w", ErrInvalidImageURL)
	}
	// audience 空字串正規化為 all（既有前端/呼叫端尚未帶這個新欄位時，行為等同過去的全體會員商家）。
	req.Audience = strings.TrimSpace(req.Audience)
	if req.Audience == "" {
		req.Audience = "all"
	}
	if req.Audience != "all" && req.Audience != "vip_featured" {
		return ErrInvalidAudience
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
	return validateVariants(req)
}

// validateVariants 正規化並驗證 item_mode／variants（見 docs/partner/VARIANTS_CONTRACT.md §1）。
// item_mode 空字串正規化為 single；variants 上限 50 筆；每筆 trim 後驗證，id 缺漏或非 UUID
// 格式時伺服器補發新 uuid（前端可回傳既有 id 以保留關聯，不視為錯誤）。錯誤訊息帶索引與欄位
// 名稱（例：variants[2].name 必填），供後台原樣顯示。
func validateVariants(req *AdminPartnerShopRequest) error {
	req.ItemMode = strings.TrimSpace(req.ItemMode)
	if req.ItemMode == "" {
		req.ItemMode = "single"
	}
	if req.ItemMode != "single" && req.ItemMode != "multi" {
		return ErrInvalidItemMode
	}
	if req.Variants == nil {
		req.Variants = []PartnerVariant{}
	}
	if len(req.Variants) > maxVariants {
		return fmt.Errorf("variants: %w（目前 %d 筆）", ErrTooManyVariants, len(req.Variants))
	}
	for i := range req.Variants {
		v := &req.Variants[i]
		v.ID = strings.TrimSpace(v.ID)
		if v.ID == "" || !uuidLikeRe.MatchString(v.ID) {
			v.ID = uuid.NewString()
		}
		v.Name = strings.TrimSpace(v.Name)
		if v.Name == "" {
			// 必須用 %w 包裝 ErrNameRequired，handler.go 的 errors.Is 判斷才抓得到、回 400
			// 而非落入 500（曾發生：VERIFY-NEON #5a 實測回 500 "failed to create partner shop"）。
			return fmt.Errorf("variants[%d].name 必填: %w", i, ErrNameRequired)
		}
		if n := len([]rune(v.Name)); n > maxVariantNameLen {
			return fmt.Errorf("variants[%d].name: %w（上限 %d 字，目前 %d 字）", i, ErrTooLong, maxVariantNameLen, n)
		}
		v.Description = strings.TrimSpace(v.Description)
		if n := len([]rune(v.Description)); n > maxVariantDescLen {
			return fmt.Errorf("variants[%d].description: %w（上限 %d 字，目前 %d 字）", i, ErrTooLong, maxVariantDescLen, n)
		}
		v.ImageURL = strings.TrimSpace(v.ImageURL)
		if !validImageURL(v.ImageURL) {
			return fmt.Errorf("variants[%d].image_url: %w", i, ErrInvalidImageURL)
		}
		v.CTAURL = strings.TrimSpace(v.CTAURL)
		if !validHTTPURL(v.CTAURL) {
			return fmt.Errorf("variants[%d].cta_url: %w", i, ErrInvalidURL)
		}
	}
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
