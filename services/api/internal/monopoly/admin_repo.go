package monopoly

// admin_repo.go 環台大富翁後台管理（C1）：獎勵池 CRUD、兌換碼批次匯入、知識卡補圖、抽卡設定讀寫。
// 純資料存取；輸入驗證統一在 admin.go 的 handler 層完成（比照 repository.go／partner 套件的分層慣例）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/appsettings"
)

// ErrNotFound 後台管理操作（更新/刪除）找不到對應列。
var ErrNotFound = errors.New("monopoly admin: not found")

// --- 獎勵池 pool editor ---

// PoolEntryInput POST/PATCH /admin/monopoly/pool 的請求 body；驗證與正規化見 admin.go 的
// validatePoolEntryInput（knowledge_card/sticker 會被強制清空 amount/redemption_batch_key）。
type PoolEntryInput struct {
	Pool               string `json:"pool"`
	RewardType         string `json:"reward_type"`
	Weight             int    `json:"weight"`
	Amount             int    `json:"amount"`
	RedemptionBatchKey string `json:"redemption_batch_key"`
	Note               string `json:"note"`
	IsActive           bool   `json:"is_active"`
	SortOrder          int    `json:"sort_order"`
}

// AdminPoolEntry GET /admin/monopoly/pool 單列。
type AdminPoolEntry struct {
	ID string `json:"id"`
	PoolEntryInput
}

const poolEntryCols = `id, pool, reward_type, weight, amount, redemption_batch_key, note, is_active, sort_order`

func scanPoolEntry(row pgx.Row) (*AdminPoolEntry, error) {
	e := &AdminPoolEntry{}
	if err := row.Scan(&e.ID, &e.Pool, &e.RewardType, &e.Weight, &e.Amount,
		&e.RedemptionBatchKey, &e.Note, &e.IsActive, &e.SortOrder); err != nil {
		return nil, err
	}
	return e, nil
}

// AdminListPoolEntries 全部獎勵池項目，依 pool, sort_order 排序（含 is_active=false，後台要看得到全貌）。
func (r *Repository) AdminListPoolEntries(ctx context.Context) ([]*AdminPoolEntry, error) {
	rows, err := r.db.Query(ctx, `SELECT `+poolEntryCols+` FROM monopoly_pool_entries ORDER BY pool, sort_order`)
	if err != nil {
		return nil, fmt.Errorf("list pool entries: %w", err)
	}
	defer rows.Close()
	out := []*AdminPoolEntry{}
	for rows.Next() {
		e, err := scanPoolEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AdminCreatePoolEntry in 已在 handler 層通過 validatePoolEntryInput 驗證/正規化。
func (r *Repository) AdminCreatePoolEntry(ctx context.Context, in PoolEntryInput) (*AdminPoolEntry, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO monopoly_pool_entries (pool, reward_type, weight, amount, redemption_batch_key, note, is_active, sort_order)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+poolEntryCols,
		in.Pool, in.RewardType, in.Weight, in.Amount, in.RedemptionBatchKey, in.Note, in.IsActive, in.SortOrder,
	)
	return scanPoolEntry(row)
}

// AdminUpdatePoolEntry 全量更新（PATCH /pool/{id} 的 body 與 POST 同形狀，不是欄位級部分更新）。
func (r *Repository) AdminUpdatePoolEntry(ctx context.Context, id string, in PoolEntryInput) (*AdminPoolEntry, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE monopoly_pool_entries SET
			pool=$2, reward_type=$3, weight=$4, amount=$5, redemption_batch_key=$6, note=$7, is_active=$8, sort_order=$9,
			updated_at=NOW()
		WHERE id=$1
		RETURNING `+poolEntryCols,
		id, in.Pool, in.RewardType, in.Weight, in.Amount, in.RedemptionBatchKey, in.Note, in.IsActive, in.SortOrder,
	)
	e, err := scanPoolEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (r *Repository) AdminDeletePoolEntry(ctx context.Context, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM monopoly_pool_entries WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- 兌換碼批次 ---

// AdminRedemptionBatch GET /admin/monopoly/redeem/batches 依 batch_key 匯總的一列（kind/label 取
// MIN——同批次理論上是同一次 POST /redeem/batch 建立、kind/label 一致，MIN 只是聚合上的技術需要）。
type AdminRedemptionBatch struct {
	BatchKey  string `json:"batch_key"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Total     int    `json:"total"`
	Used      int    `json:"used"`
	Remaining int    `json:"remaining"`
}

func (r *Repository) AdminListRedemptionBatches(ctx context.Context) ([]*AdminRedemptionBatch, error) {
	rows, err := r.db.Query(ctx, `
		SELECT batch_key, MIN(kind), MIN(label), COUNT(*),
		       COUNT(*) FILTER (WHERE is_used), COUNT(*) FILTER (WHERE NOT is_used)
		FROM monopoly_redemption_codes
		GROUP BY batch_key
		ORDER BY batch_key`)
	if err != nil {
		return nil, fmt.Errorf("list redemption batches: %w", err)
	}
	defer rows.Close()
	out := []*AdminRedemptionBatch{}
	for rows.Next() {
		b := &AdminRedemptionBatch{}
		if err := rows.Scan(&b.BatchKey, &b.Kind, &b.Label, &b.Total, &b.Used, &b.Remaining); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AdminCreateRedemptionBatch 逐筆 INSERT ... ON CONFLICT (batch_key,code) DO NOTHING（codes 需由呼叫端
// 先切行/trim/去空/去重，見 admin.go 的 parseCodesText）。回傳成功插入數與因批次內既有重複碼被跳過數。
func (r *Repository) AdminCreateRedemptionBatch(ctx context.Context, batchKey, kind, label string, codes []string) (inserted, skipped int, err error) {
	for _, code := range codes {
		ct, execErr := r.db.Exec(ctx, `
			INSERT INTO monopoly_redemption_codes (batch_key, kind, label, code)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (batch_key, code) DO NOTHING`, batchKey, kind, label, code)
		if execErr != nil {
			return inserted, skipped, fmt.Errorf("insert redemption code: %w", execErr)
		}
		if ct.RowsAffected() > 0 {
			inserted++
		} else {
			skipped++
		}
	}
	return inserted, skipped, nil
}

// AdminRedemptionCode GET /admin/monopoly/redeem/batch/{key} 單筆碼（不回傳 used_by，後台管碼不需要
// 外洩使用者 id；核對誰用了哪支碼走 monopoly_draws 流水/客服工具）。
type AdminRedemptionCode struct {
	Code   string     `json:"code"`
	IsUsed bool       `json:"is_used"`
	UsedAt *time.Time `json:"used_at,omitempty"`
}

func (r *Repository) AdminListRedemptionCodes(ctx context.Context, batchKey string) ([]*AdminRedemptionCode, error) {
	rows, err := r.db.Query(ctx, `
		SELECT code, is_used, used_at FROM monopoly_redemption_codes
		WHERE batch_key=$1 ORDER BY created_at`, batchKey)
	if err != nil {
		return nil, fmt.Errorf("list redemption codes: %w", err)
	}
	defer rows.Close()
	out := []*AdminRedemptionCode{}
	for rows.Next() {
		c := &AdminRedemptionCode{}
		if err := rows.Scan(&c.Code, &c.IsUsed, &c.UsedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- 知識卡管理（主要＝補圖） ---

type AdminKnowledgeCard struct {
	ID           string `json:"id"`
	Code         string `json:"code"`
	Theme        string `json:"theme"`
	MainCategory string `json:"main_category"`
	Subtopic     string `json:"subtopic"`
	Title        string `json:"title"`
	Rarity       string `json:"rarity"`
	ImageURL     string `json:"image_url"`
	IsActive     bool   `json:"is_active"`
}

// AdminListKnowledgeCards theme 空字串＝不篩選（全部 training+care）。
func (r *Repository) AdminListKnowledgeCards(ctx context.Context, theme string) ([]*AdminKnowledgeCard, error) {
	query := `SELECT id, code, theme, main_category, subtopic, title, rarity, image_url, is_active
		FROM knowledge_card_defs`
	args := []any{}
	if theme != "" {
		query += ` WHERE theme=$1`
		args = append(args, theme)
	}
	query += ` ORDER BY theme, sort_order`
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list knowledge cards: %w", err)
	}
	defer rows.Close()
	out := []*AdminKnowledgeCard{}
	for rows.Next() {
		c := &AdminKnowledgeCard{}
		if err := rows.Scan(&c.ID, &c.Code, &c.Theme, &c.MainCategory, &c.Subtopic, &c.Title, &c.Rarity, &c.ImageURL, &c.IsActive); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AdminUpdateKnowledgeCard 部分更新：nil 的欄位保留原值（COALESCE），由 admin.go 傳入 nil 代表
// 「這次請求沒帶這個欄位」。
func (r *Repository) AdminUpdateKnowledgeCard(ctx context.Context, id string, imageURL, rarity, title, body *string, isActive *bool) (*AdminKnowledgeCard, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE knowledge_card_defs SET
			image_url = COALESCE($2, image_url),
			rarity = COALESCE($3, rarity),
			title = COALESCE($4, title),
			body = COALESCE($5, body),
			is_active = COALESCE($6, is_active),
			updated_at = NOW()
		WHERE id=$1
		RETURNING id, code, theme, main_category, subtopic, title, rarity, image_url, is_active`,
		id, imageURL, rarity, title, body, isActive,
	)
	c := &AdminKnowledgeCard{}
	err := row.Scan(&c.ID, &c.Code, &c.Theme, &c.MainCategory, &c.Subtopic, &c.Title, &c.Rarity, &c.ImageURL, &c.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// --- 完賽公仔貼紙管理（主要＝補圖／換彩圖，Phase 2b C） ---

// AdminStickerPiece GET /admin/monopoly/stickers 單片（set_key 固定 'finisher'，後台不需要顯示）。
type AdminStickerPiece struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	Title    string `json:"title"`
	ImageURL string `json:"image_url"`
	Rarity   string `json:"rarity"`
	IsActive bool   `json:"is_active"`
}

// AdminListStickerPieces set_key='finisher' 全部 9 片（含 is_active=false，後台要看得到全貌），依 position 排序。
func (r *Repository) AdminListStickerPieces(ctx context.Context) ([]*AdminStickerPiece, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, position, title, image_url, rarity, is_active
		FROM sticker_pieces
		WHERE set_key = 'finisher'
		ORDER BY position`)
	if err != nil {
		return nil, fmt.Errorf("list sticker pieces: %w", err)
	}
	defer rows.Close()
	out := []*AdminStickerPiece{}
	for rows.Next() {
		p := &AdminStickerPiece{}
		if err := rows.Scan(&p.ID, &p.Position, &p.Title, &p.ImageURL, &p.Rarity, &p.IsActive); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AdminUpdateStickerPiece 部分更新：nil 的欄位保留原值（COALESCE），比照 AdminUpdateKnowledgeCard。
func (r *Repository) AdminUpdateStickerPiece(ctx context.Context, id string, imageURL, title, rarity *string, isActive *bool) (*AdminStickerPiece, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE sticker_pieces SET
			image_url = COALESCE($2, image_url),
			title = COALESCE($3, title),
			rarity = COALESCE($4, rarity),
			is_active = COALESCE($5, is_active),
			updated_at = NOW()
		WHERE id=$1
		RETURNING id, position, title, image_url, rarity, is_active`,
		id, imageURL, title, rarity, isActive,
	)
	p := &AdminStickerPiece{}
	err := row.Scan(&p.ID, &p.Position, &p.Title, &p.ImageURL, &p.Rarity, &p.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// AdminGetFigureSettings 讀 monopoly_figure_color_url / monopoly_figure_title / monopoly_figure_line_oa /
// monopoly_figure_landing_url，查無時回退與 service.go GetStickers、107_monopoly_figure_redeem.sql
// 種子值一致的硬編碼預設（避免單一壞設定卡死收集頁彩圖展示／兌換引導）。
func (r *Repository) AdminGetFigureSettings(ctx context.Context) (figureColorURL, figureTitle, lineOA, landingURL string) {
	figureTitle = appsettings.GetString(ctx, r.db, "monopoly_figure_title", "完賽跑者公仔")
	figureColorURL = appsettings.GetString(ctx, r.db, "monopoly_figure_color_url", "/source/ui/03_figure/runner_figure_color.png")
	lineOA = appsettings.GetString(ctx, r.db, "monopoly_figure_line_oa", "@855xfwqe")
	landingURL = appsettings.GetString(ctx, r.db, "monopoly_figure_landing_url", "https://runner-figure.bravelog.tw/")
	return figureColorURL, figureTitle, lineOA, landingURL
}

// --- 完賽公仔兌換管理（A 後端） ---

// AdminFigureRedemption GET /admin/monopoly/redemptions 單列／PATCH 回應，JOIN users/user_profiles
// 取顯示名／帳號編碼／email（帳號編碼屬隱私例外，後台管理頁可顯示，見任務背景）。
type AdminFigureRedemption struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	AccountCode string    `json:"account_code"`
	Nickname    string    `json:"nickname"`
	Email       string    `json:"email"`
	Status      string    `json:"status"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
}

// AdminListFigureRedemptions 全部兌換申請，依 created_at DESC（後台要看得到全貌，不篩選 status）。
func (r *Repository) AdminListFigureRedemptions(ctx context.Context) ([]*AdminFigureRedemption, error) {
	rows, err := r.db.Query(ctx, `
		SELECT fr.id, fr.user_id, COALESCE(u.account_code,''),
		       COALESCE(NULLIF(p.nickname,''), u.handle), u.email, fr.status, fr.note, fr.created_at
		FROM monopoly_figure_redemptions fr
		JOIN users u ON u.id = fr.user_id
		LEFT JOIN user_profiles p ON p.user_id = u.id
		ORDER BY fr.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list figure redemptions: %w", err)
	}
	defer rows.Close()
	out := []*AdminFigureRedemption{}
	for rows.Next() {
		e := &AdminFigureRedemption{}
		if err := rows.Scan(&e.ID, &e.UserID, &e.AccountCode, &e.Nickname, &e.Email, &e.Status, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AdminUpdateFigureRedemption 更新狀態＋備註（status 由 admin.go 驗證限 pending/fulfilled/rejected）；
// 找不到該筆回 ErrNotFound。
func (r *Repository) AdminUpdateFigureRedemption(ctx context.Context, id, status, note string) (*AdminFigureRedemption, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE monopoly_figure_redemptions fr SET status=$2, note=$3, updated_at=NOW()
		FROM users u LEFT JOIN user_profiles p ON p.user_id = u.id
		WHERE fr.id=$1 AND u.id = fr.user_id
		RETURNING fr.id, fr.user_id, COALESCE(u.account_code,''),
		          COALESCE(NULLIF(p.nickname,''), u.handle), u.email, fr.status, fr.note, fr.created_at`,
		id, status, note,
	)
	e := &AdminFigureRedemption{}
	err := row.Scan(&e.ID, &e.UserID, &e.AccountCode, &e.Nickname, &e.Email, &e.Status, &e.Note, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// --- 抽卡設定 ---

// AdminGetSettings 讀 monopoly_dup_gp（JSON）/ monopoly_redeem_fallback_gp（int），查無/格式錯誤一律
// 回退硬編碼預設值，與 service.go 的 monopolyDupGP/diceGPCost 讀取邏輯一致（避免單一壞設定卡死抽卡流程）。
func (r *Repository) AdminGetSettings(ctx context.Context) (dupGP map[string]int, fallbackGP int) {
	fallbackGP = appsettings.GetInt(ctx, r.db, "monopoly_redeem_fallback_gp", 10)
	dupGP = map[string]int{"common": 5, "rare": 20}
	raw := appsettings.GetString(ctx, r.db, "monopoly_dup_gp", "")
	if raw == "" {
		return dupGP, fallbackGP
	}
	var m map[string]int
	if err := json.Unmarshal([]byte(raw), &m); err == nil && len(m) > 0 {
		dupGP = m
	}
	return dupGP, fallbackGP
}

// AdminSetSetting 通用 app_settings upsert（比照 appsettings.Handler.Set 的寫法），供寫入
// monopoly_dup_gp / monopoly_redeem_fallback_gp 這兩把不在 appsettings.specs 白名單內的 key
// （monopoly 後台自己管，不需要也不透過 /admin/app-settings 通用頁面）。
func (r *Repository) AdminSetSetting(ctx context.Context, key, value string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, key, value)
	return err
}
