package partner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
)

// minKmSettingKey / defaultMinVIPFeaturedKm：VIP 精選解鎖門檻的 app_settings key 與預設值。
// 刻意不登記進 appsettings 的 specs 白名單（比照 monopoly_dup_gp 慣例）：這把由本模組自己的
// 後台端點管理（AdminGetMinKm/AdminSetMinKm，掛在 perm partners），不透過通用系統設定頁面。
const (
	minKmSettingKey         = "partner_vip_featured_min_km"
	defaultMinVIPFeaturedKm = 10
)

// ErrNotFound 找不到商家（或 enabled=false 視同不存在，前台不外洩下架資料）。
var ErrNotFound = errors.New("partner shop not found")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func marshalPhotoURLs(urls []string) ([]byte, error) {
	if urls == nil {
		urls = []string{}
	}
	return json.Marshal(urls)
}

func unmarshalPhotoURLs(b []byte) ([]string, error) {
	urls := []string{}
	if len(b) == 0 {
		return urls, nil
	}
	if err := json.Unmarshal(b, &urls); err != nil {
		return nil, err
	}
	return urls, nil
}

// --- 前台 ---

// ListEnabled 前台列表：僅 enabled=true，依 display_order/created_at 排序。
// uid 為空字串（未登入）時 is_favorited 一律 false，且不可讓 SQL 出錯。
// audience='vip_featured' 商家現在對所有人可見（不再於此過濾）；「不合格」改由
// Service.ListEnabled 事後對每個 shop 蓋上 CtaLocked/CtaLockReason 並清空 CTAURL。
func (r *Repository) ListEnabled(ctx context.Context, uid string) ([]*PartnerShop, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ps.id, ps.name, ps.summary, ps.banner_url, ps.cta_url, ps.cta_label, ps.display_order, ps.audience,
		       ($1 <> '' AND EXISTS(
		           SELECT 1 FROM partner_shop_favorites f
		           WHERE f.user_id = NULLIF($1,'')::uuid AND f.shop_id = ps.id
		       ))
		FROM partner_shops ps
		WHERE ps.enabled
		ORDER BY ps.display_order, ps.created_at
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("list partner shops: %w", err)
	}
	defer rows.Close()

	out := []*PartnerShop{}
	for rows.Next() {
		s := &PartnerShop{}
		if err := rows.Scan(&s.ID, &s.Name, &s.Summary, &s.BannerURL, &s.CTAURL, &s.CTALabel,
			&s.DisplayOrder, &s.Audience, &s.IsFavorited); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountEnabledByAudience 計算指定 audience 的 enabled 商家數（不論呼叫端是否有權看到內容）；
// 供前台 meta.vip_featured_count（鎖定卡「N 家等你解鎖」）使用。
func (r *Repository) CountEnabledByAudience(ctx context.Context, audience string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM partner_shops WHERE enabled AND audience=$1`, audience).Scan(&n)
	return n, err
}

// UserVIPKm 讀使用者目前是否為 VIP（vip_expires_at > now，比照 internal/race Repository.IsUserVIP
// 等既有查法）與累積里程 total_km，供 VIP 精選資格判定用。uid 空字串（未登入）直接回傳零值。
func (r *Repository) UserVIPKm(ctx context.Context, uid string) (isVIP bool, km float64, err error) {
	if uid == "" {
		return false, 0, nil
	}
	err = r.db.QueryRow(ctx, `SELECT COALESCE(vip_expires_at > NOW(), FALSE), total_km FROM users WHERE id=$1`, uid).
		Scan(&isVIP, &km)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	return isVIP, km, err
}

// MinVIPFeaturedKm 讀 VIP 精選解鎖門檻（app_settings.partner_vip_featured_min_km，見 migrations/112）。
func (r *Repository) MinVIPFeaturedKm(ctx context.Context) int {
	return appsettings.GetInt(ctx, r.db, minKmSettingKey, defaultMinVIPFeaturedKm)
}

// SetMinVIPFeaturedKm 寫入 VIP 精選解鎖門檻；呼叫端（Service）需先驗證 km>=0。
func (r *Repository) SetMinVIPFeaturedKm(ctx context.Context, km int) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		minKmSettingKey, strconv.Itoa(km))
	return err
}

// GetDetail 前台詳細：enabled=false 回 ErrNotFound（不外洩下架商家內容）。
// audience='vip_featured' 商家現在對所有人可見（不再視資格擋成 ErrNotFound）；
// 「不合格」改由 Service.GetDetail 事後蓋上 CtaLocked/CtaLockReason 並清空 CTAURL。
func (r *Repository) GetDetail(ctx context.Context, id, uid string) (*PartnerShopDetail, error) {
	d := &PartnerShopDetail{}
	var photoBytes []byte
	err := r.db.QueryRow(ctx, `
		SELECT ps.id, ps.name, ps.summary, ps.banner_url, ps.cta_url, ps.cta_label, ps.display_order, ps.audience,
		       ($2 <> '' AND EXISTS(
		           SELECT 1 FROM partner_shop_favorites f
		           WHERE f.user_id = NULLIF($2,'')::uuid AND f.shop_id = ps.id
		       )),
		       ps.detail_html, ps.photo_urls, ps.video_url
		FROM partner_shops ps
		WHERE ps.id = $1 AND ps.enabled
	`, id, uid).Scan(
		&d.ID, &d.Name, &d.Summary, &d.BannerURL, &d.CTAURL, &d.CTALabel, &d.DisplayOrder, &d.Audience, &d.IsFavorited,
		&d.DetailHTML, &photoBytes, &d.VideoURL,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.PhotoURLs, err = unmarshalPhotoURLs(photoBytes)
	if err != nil {
		return nil, err
	}
	d.DetailHTML = SanitizeDetailHTML(d.DetailHTML) // 輸出前二度消毒
	return d, nil
}

// --- 收藏 ---

// AddFavorite 冪等新增收藏。只要商家存在且 enabled 即可收藏（audience='vip_featured' 商家現在
// 對所有人可見/可收藏，不再要求資格）；shop_id 不存在一律回 ErrNotFound。
func (r *Repository) AddFavorite(ctx context.Context, userID, shopID string) error {
	var audience string
	err := r.db.QueryRow(ctx, `SELECT audience FROM partner_shops WHERE id=$1 AND enabled`, shopID).Scan(&audience)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO partner_shop_favorites (user_id, shop_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, shopID)
	return err
}

// RemoveFavorite 冪等移除收藏。
func (r *Repository) RemoveFavorite(ctx context.Context, userID, shopID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM partner_shop_favorites WHERE user_id=$1 AND shop_id=$2`, userID, shopID)
	return err
}

// --- 後台 ---

const adminSelectCols = `id, name, summary, banner_url, cta_url, cta_label, display_order, audience,
	detail_html, photo_urls, video_url, enabled, created_at, updated_at`

// scanAdminRow 同時吃 pgx.Rows（Query）與 pgx.Row（QueryRow）——兩者皆滿足 Scan(dest ...any) error。
func scanAdminRow(row pgx.Row) (*AdminPartnerShop, error) {
	a := &AdminPartnerShop{}
	var photoBytes []byte
	if err := row.Scan(&a.ID, &a.Name, &a.Summary, &a.BannerURL, &a.CTAURL, &a.CTALabel, &a.DisplayOrder, &a.Audience,
		&a.DetailHTML, &photoBytes, &a.VideoURL, &a.Enabled, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	urls, err := unmarshalPhotoURLs(photoBytes)
	if err != nil {
		return nil, err
	}
	a.PhotoURLs = urls
	a.DetailHTML = SanitizeDetailHTML(a.DetailHTML) // 輸出前二度消毒
	return a, nil
}

// AdminList 後台列表：全部商家（含下架），依 display_order/created_at 排序。
func (r *Repository) AdminList(ctx context.Context) ([]*AdminPartnerShop, error) {
	rows, err := r.db.Query(ctx, `SELECT `+adminSelectCols+` FROM partner_shops ORDER BY display_order, created_at`)
	if err != nil {
		return nil, fmt.Errorf("admin list partner shops: %w", err)
	}
	defer rows.Close()

	out := []*AdminPartnerShop{}
	for rows.Next() {
		a, err := scanAdminRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AdminCreate 新增商家（detail_html 需由呼叫端先消毒過）。
func (r *Repository) AdminCreate(ctx context.Context, req *AdminPartnerShopRequest) (*AdminPartnerShop, error) {
	photoBytes, err := marshalPhotoURLs(req.PhotoURLs)
	if err != nil {
		return nil, fmt.Errorf("marshal photo_urls: %w", err)
	}
	row := r.db.QueryRow(ctx, `
		INSERT INTO partner_shops
		    (name, summary, banner_url, detail_html, photo_urls, video_url, cta_url, cta_label, display_order, enabled, audience)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING `+adminSelectCols,
		req.Name, req.Summary, req.BannerURL, req.DetailHTML, photoBytes, req.VideoURL,
		req.CTAURL, req.CTALabel, req.DisplayOrder, req.Enabled, req.Audience,
	)
	return scanAdminRow(row)
}

// AdminUpdate 更新商家（含上下架 enabled；detail_html 需由呼叫端先消毒過）。
func (r *Repository) AdminUpdate(ctx context.Context, id string, req *AdminPartnerShopRequest) (*AdminPartnerShop, error) {
	photoBytes, err := marshalPhotoURLs(req.PhotoURLs)
	if err != nil {
		return nil, fmt.Errorf("marshal photo_urls: %w", err)
	}
	row := r.db.QueryRow(ctx, `
		UPDATE partner_shops SET
		    name=$1, summary=$2, banner_url=$3, detail_html=$4, photo_urls=$5, video_url=$6,
		    cta_url=$7, cta_label=$8, display_order=$9, enabled=$10, audience=$11, updated_at=NOW()
		WHERE id=$12
		RETURNING `+adminSelectCols,
		req.Name, req.Summary, req.BannerURL, req.DetailHTML, photoBytes, req.VideoURL,
		req.CTAURL, req.CTALabel, req.DisplayOrder, req.Enabled, req.Audience, id,
	)
	a, err := scanAdminRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// AdminDelete 刪除商家（收藏表靠 FK ON DELETE CASCADE 一併清除）。
func (r *Repository) AdminDelete(ctx context.Context, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM partner_shops WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
