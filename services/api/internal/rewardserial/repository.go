package rewardserial

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrMerchantInUse = errors.New("此商家仍有序號組使用中，請先刪除或改指定其他商家")
)

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// --- 合作商家 ---

func (r *Repository) ListMerchants(ctx context.Context) ([]Merchant, error) {
	rows, err := r.db.Query(ctx, `SELECT id, name, COALESCE(note,''), created_at FROM reward_merchants ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Merchant{}
	for rows.Next() {
		var m Merchant
		if err := rows.Scan(&m.ID, &m.Name, &m.Note, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repository) CreateMerchant(ctx context.Context, name, note string) (*Merchant, error) {
	m := &Merchant{Name: name, Note: note}
	err := r.db.QueryRow(ctx,
		`INSERT INTO reward_merchants (name, note) VALUES ($1, NULLIF($2,'')) RETURNING id, created_at`,
		name, note).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create merchant: %w", err)
	}
	return m, nil
}

func (r *Repository) UpdateMerchant(ctx context.Context, id, name, note string) (*Merchant, error) {
	m := &Merchant{ID: id, Name: name, Note: note}
	err := r.db.QueryRow(ctx,
		`UPDATE reward_merchants SET name=$1, note=NULLIF($2,'') WHERE id=$3 RETURNING created_at`,
		name, note, id).Scan(&m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (r *Repository) DeleteMerchant(ctx context.Context, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM reward_merchants WHERE id=$1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation：仍有序號組引用此商家
			return ErrMerchantInUse
		}
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- 序號組 ---

const groupCols = `g.id, g.merchant_id, COALESCE(m.name,''), g.name, COALESCE(g.item_label,''), g.is_line_point,
	g.face_value, g.valid_from, g.valid_until, g.use_limit_type, g.use_limit_count, g.grant_count, g.applies_all_races,
	COALESCE(g.usage_note,''), COALESCE(g.icon_url,''), COALESCE(g.description,''), g.created_at`

func scanGroup(row pgx.Row) (*Group, error) {
	g := &Group{}
	var merchantID *string
	err := row.Scan(&g.ID, &merchantID, &g.MerchantName, &g.Name, &g.ItemLabel, &g.IsLinePoint,
		&g.FaceValue, &g.ValidFrom, &g.ValidUntil, &g.UseLimitType, &g.UseLimitCount, &g.GrantCount, &g.AppliesAllRaces,
		&g.UsageNote, &g.IconURL, &g.Description, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	g.MerchantID = merchantID
	return g, nil
}

type serialStat struct{ available, issued, void, total int }

// serialStatsByGroup 各序號組的序號庫存統計（未發送/已發送/註銷/總數）
func (r *Repository) serialStatsByGroup(ctx context.Context, groupIDs []string) (map[string]serialStat, error) {
	rows, err := r.db.Query(ctx, `
		SELECT group_id::text,
			COUNT(*) FILTER (WHERE status='available'),
			COUNT(*) FILTER (WHERE status='issued'),
			COUNT(*) FILTER (WHERE status='void'),
			COUNT(*)
		FROM reward_serials WHERE group_id = ANY($1::uuid[]) GROUP BY group_id`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]serialStat{}
	for rows.Next() {
		var gid string
		var s serialStat
		if err := rows.Scan(&gid, &s.available, &s.issued, &s.void, &s.total); err != nil {
			return nil, err
		}
		out[gid] = s
	}
	return out, rows.Err()
}

// raceIDsByGroup 各序號組指定的活動 id 清單（applies_all_races=false 時用）
func (r *Repository) raceIDsByGroup(ctx context.Context, groupIDs []string) (map[string][]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT group_id::text, race_id::text FROM reward_serial_group_races WHERE group_id = ANY($1::uuid[])`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var gid, rid string
		if err := rows.Scan(&gid, &rid); err != nil {
			return nil, err
		}
		out[gid] = append(out[gid], rid)
	}
	return out, rows.Err()
}

// hydrateGroups 補上 race_ids + 序號庫存統計（List/Get 共用）
func (r *Repository) hydrateGroups(ctx context.Context, groups []Group) error {
	if len(groups) == 0 {
		return nil
	}
	ids := make([]string, len(groups))
	for i := range groups {
		ids[i] = groups[i].ID
	}
	raceMap, err := r.raceIDsByGroup(ctx, ids)
	if err != nil {
		return err
	}
	statMap, err := r.serialStatsByGroup(ctx, ids)
	if err != nil {
		return err
	}
	for i := range groups {
		groups[i].RaceIDs = raceMap[groups[i].ID]
		if groups[i].RaceIDs == nil {
			groups[i].RaceIDs = []string{}
		}
		st := statMap[groups[i].ID]
		groups[i].AvailableCount, groups[i].IssuedCount, groups[i].VoidCount, groups[i].TotalCount = st.available, st.issued, st.void, st.total
	}
	return nil
}

func (r *Repository) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+groupCols+`
		FROM reward_serial_groups g LEFT JOIN reward_merchants m ON m.id = g.merchant_id
		ORDER BY g.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.hydrateGroups(ctx, groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (r *Repository) GetGroup(ctx context.Context, id string) (*Group, error) {
	g, err := scanGroup(r.db.QueryRow(ctx, `
		SELECT `+groupCols+`
		FROM reward_serial_groups g LEFT JOIN reward_merchants m ON m.id = g.merchant_id
		WHERE g.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	groups := []Group{*g}
	if err := r.hydrateGroups(ctx, groups); err != nil {
		return nil, err
	}
	return &groups[0], nil
}

func (r *Repository) replaceGroupRaces(ctx context.Context, tx pgx.Tx, groupID string, raceIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM reward_serial_group_races WHERE group_id=$1`, groupID); err != nil {
		return err
	}
	for _, rid := range raceIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO reward_serial_group_races (group_id, race_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			groupID, rid); err != nil {
			return fmt.Errorf("link race: %w", err)
		}
	}
	return nil
}

func (r *Repository) CreateGroup(ctx context.Context, in GroupInput, validFrom, validUntil *time.Time) (*Group, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO reward_serial_groups
			(merchant_id, name, item_label, is_line_point, face_value, valid_from, valid_until, use_limit_type, use_limit_count, grant_count, applies_all_races,
			 usage_note, icon_url, description)
		VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),NULLIF($13,''),NULLIF($14,''))
		RETURNING id`,
		in.MerchantID, in.Name, in.ItemLabel, in.IsLinePoint, in.FaceValue, validFrom, validUntil, in.UseLimitType, in.UseLimitCount, in.GrantCount, in.AppliesAllRaces,
		in.UsageNote, in.IconURL, in.Description).
		Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}
	if !in.AppliesAllRaces {
		if err := r.replaceGroupRaces(ctx, tx, id, in.RaceIDs); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetGroup(ctx, id)
}

func (r *Repository) UpdateGroup(ctx context.Context, id string, in GroupInput, validFrom, validUntil *time.Time) (*Group, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE reward_serial_groups SET
			merchant_id=$1, name=$2, item_label=NULLIF($3,''), is_line_point=$4, face_value=$5, valid_from=$6, valid_until=$7,
			use_limit_type=$8, use_limit_count=$9, grant_count=$10, applies_all_races=$11,
			usage_note=NULLIF($12,''), icon_url=NULLIF($13,''), description=NULLIF($14,'')
		WHERE id=$15`,
		in.MerchantID, in.Name, in.ItemLabel, in.IsLinePoint, in.FaceValue, validFrom, validUntil,
		in.UseLimitType, in.UseLimitCount, in.GrantCount, in.AppliesAllRaces,
		in.UsageNote, in.IconURL, in.Description, id)
	if err != nil {
		return nil, fmt.Errorf("update group: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	raceIDs := in.RaceIDs
	if in.AppliesAllRaces {
		raceIDs = nil
	}
	if err := r.replaceGroupRaces(ctx, tx, id, raceIDs); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetGroup(ctx, id)
}

// DeleteGroup 刪除序號組；reward_serials / reward_serial_group_races 皆為 ON DELETE CASCADE，
// 會一併刪除底下所有序號（含已發送/已使用者）。前端須先向管理員明確警示此為不可逆操作。
func (r *Repository) DeleteGroup(ctx context.Context, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM reward_serial_groups WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- 序號 ---

const serialCols = `id, group_id, code, COALESCE(link,''), status, used, used_at, issued_to, issued_at, created_at`

func scanSerial(row pgx.Row) (*Serial, error) {
	s := &Serial{}
	var issuedTo *string
	err := row.Scan(&s.ID, &s.GroupID, &s.Code, &s.Link, &s.Status, &s.Used, &s.UsedAt, &issuedTo, &s.IssuedAt, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	s.IssuedTo = issuedTo
	return s, nil
}

// ListSerials 某序號組的序號清單，status=”=全部；limit/offset 分頁。
func (r *Repository) ListSerials(ctx context.Context, groupID, status string, limit, offset int) ([]Serial, int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM reward_serials WHERE group_id=$1 AND ($2='' OR status=$2)`, groupID, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+serialCols+` FROM reward_serials
		WHERE group_id=$1 AND ($2='' OR status=$2)
		ORDER BY created_at DESC LIMIT $3 OFFSET $4`, groupID, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Serial{}
	for rows.Next() {
		s, err := scanSerial(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *s)
	}
	return out, total, rows.Err()
}

// ImportSerials 匯入序號，全系統唯一去重：撞碼（跨任何序號組，含本次批次內部重複）一律跳過不建立，
// 回報 imported/skipped/duplicates 供前端提示管理員。用 ON CONFLICT (code) DO NOTHING RETURNING code
// 判斷實際是否寫入，避免 check-then-insert 的競態。
func (r *Repository) ImportSerials(ctx context.Context, groupID string, items []ImportInput) (*ImportResult, error) {
	res := &ImportResult{Duplicates: []string{}}
	seen := map[string]bool{}
	uniq := make([]ImportInput, 0, len(items))
	for _, it := range items {
		code := strings.TrimSpace(it.Code)
		if code == "" {
			continue
		}
		if seen[code] {
			res.Skipped++
			res.Duplicates = append(res.Duplicates, code)
			continue
		}
		seen[code] = true
		uniq = append(uniq, ImportInput{Code: code, Link: strings.TrimSpace(it.Link)})
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	for _, it := range uniq {
		var got string
		err := tx.QueryRow(ctx, `
			INSERT INTO reward_serials (group_id, code, link)
			VALUES ($1,$2,NULLIF($3,''))
			ON CONFLICT (code) DO NOTHING
			RETURNING code`, groupID, it.Code, it.Link).Scan(&got)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				res.Skipped++
				res.Duplicates = append(res.Duplicates, it.Code)
				continue
			}
			return nil, fmt.Errorf("import serial: %w", err)
		}
		res.Imported++
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

func (r *Repository) VoidSerial(ctx context.Context, groupID, serialID string) error {
	ct, err := r.db.Exec(ctx,
		`UPDATE reward_serials SET status='void' WHERE id=$1 AND group_id=$2`, serialID, groupID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
