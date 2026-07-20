package partner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
func (r *Repository) ListEnabled(ctx context.Context, uid string) ([]*PartnerShop, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ps.id, ps.name, ps.summary, ps.banner_url, ps.cta_url, ps.cta_label, ps.display_order,
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
			&s.DisplayOrder, &s.IsFavorited); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetDetail 前台詳細：enabled=false 回 ErrNotFound（不外洩下架商家內容）。
func (r *Repository) GetDetail(ctx context.Context, id, uid string) (*PartnerShopDetail, error) {
	d := &PartnerShopDetail{}
	var photoBytes []byte
	err := r.db.QueryRow(ctx, `
		SELECT ps.id, ps.name, ps.summary, ps.banner_url, ps.cta_url, ps.cta_label, ps.display_order,
		       ($2 <> '' AND EXISTS(
		           SELECT 1 FROM partner_shop_favorites f
		           WHERE f.user_id = NULLIF($2,'')::uuid AND f.shop_id = ps.id
		       )),
		       ps.detail_html, ps.photo_urls, ps.video_url
		FROM partner_shops ps
		WHERE ps.id = $1 AND ps.enabled
	`, id, uid).Scan(
		&d.ID, &d.Name, &d.Summary, &d.BannerURL, &d.CTAURL, &d.CTALabel, &d.DisplayOrder, &d.IsFavorited,
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

// AddFavorite 冪等新增收藏（shop_id 若不存在會因 FK 失敗，交給呼叫端處理）。
func (r *Repository) AddFavorite(ctx context.Context, userID, shopID string) error {
	_, err := r.db.Exec(ctx, `
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

const adminSelectCols = `id, name, summary, banner_url, cta_url, cta_label, display_order,
	detail_html, photo_urls, video_url, enabled, created_at, updated_at`

// scanAdminRow 同時吃 pgx.Rows（Query）與 pgx.Row（QueryRow）——兩者皆滿足 Scan(dest ...any) error。
func scanAdminRow(row pgx.Row) (*AdminPartnerShop, error) {
	a := &AdminPartnerShop{}
	var photoBytes []byte
	if err := row.Scan(&a.ID, &a.Name, &a.Summary, &a.BannerURL, &a.CTAURL, &a.CTALabel, &a.DisplayOrder,
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
		    (name, summary, banner_url, detail_html, photo_urls, video_url, cta_url, cta_label, display_order, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING `+adminSelectCols,
		req.Name, req.Summary, req.BannerURL, req.DetailHTML, photoBytes, req.VideoURL,
		req.CTAURL, req.CTALabel, req.DisplayOrder, req.Enabled,
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
		    cta_url=$7, cta_label=$8, display_order=$9, enabled=$10, updated_at=NOW()
		WHERE id=$11
		RETURNING `+adminSelectCols,
		req.Name, req.Summary, req.BannerURL, req.DetailHTML, photoBytes, req.VideoURL,
		req.CTAURL, req.CTALabel, req.DisplayOrder, req.Enabled, id,
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
