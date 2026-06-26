package race

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// selectCols 是所有查詢共用的 SELECT 欄位（含新增的 created_by、review_status、review_note）
const selectCols = `
	SELECT id, slug, title, COALESCE(subtitle,'') as subtitle,
	       COALESCE(world,'') as world, COALESCE(blurb,'') as blurb,
	       COALESCE(hero_image_url,'') as hero_image_url,
	       status, distances, group_type, group_mode,
	       slots_total, entry_fee, start_date, end_date, config,
	       COALESCE(created_by::text,'') as created_by,
	       review_status,
	       COALESCE(review_note,'') as review_note,
	       created_at
	FROM races`

// List 取得賽事列表，可依 status 過濾（空字串 = 全部）
// 公開端點只顯示 review_status=approved 的賽事
func (r *Repository) List(ctx context.Context, status string) ([]*Race, error) {
	query := selectCols + " WHERE review_status = 'approved'"
	args := []any{}
	if status != "" {
		query += " AND status = $1"
		args = append(args, status)
	}
	query += " ORDER BY start_date DESC"

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list races: %w", err)
	}
	defer rows.Close()

	return scanRaces(rows)
}

// GetByID 取得單一賽事
func (r *Repository) GetByID(ctx context.Context, id string) (*Race, error) {
	return r.getBy(ctx, "id", id)
}

// GetBySlug 取得單一賽事（slug）
func (r *Repository) GetBySlug(ctx context.Context, slug string) (*Race, error) {
	return r.getBy(ctx, "slug", slug)
}

func (r *Repository) getBy(ctx context.Context, col, val string) (*Race, error) {
	row := r.db.QueryRow(ctx, selectCols+" WHERE "+col+" = $1", val)

	race, err := scanRaceRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return race, err
}

// UpdateStatus 更新賽事狀態（admin 用）
func (r *Repository) UpdateStatus(ctx context.Context, raceID, status string) error {
	_, err := r.db.Exec(ctx, `UPDATE races SET status=$1, updated_at=NOW() WHERE id=$2`, status, raceID)
	return err
}

// Update 更新賽事所有可編輯欄位（admin 用），不動 review_status / created_by
func (r *Repository) Update(ctx context.Context, race *Race) (*Race, error) {
	cfgBytes, err := configToBytes(race.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	dist32 := make([]int32, len(race.Distances))
	for i, d := range race.Distances {
		dist32[i] = int32(d)
	}

	_, err = r.db.Exec(ctx, `
		UPDATE races SET
			slug=$1, title=$2, subtitle=$3, world=$4, blurb=$5, hero_image_url=$6,
			status=$7, distances=$8, group_type=$9, group_mode=$10,
			slots_total=$11, entry_fee=$12, start_date=$13, end_date=$14, config=$15,
			updated_at=NOW()
		WHERE id=$16`,
		race.Slug, race.Title, race.Subtitle, race.World, race.Blurb, race.HeroImageURL,
		race.Status, dist32, race.GroupType, race.GroupMode,
		race.SlotsTotal, race.EntryFee, race.StartDate, race.EndDate, cfgBytes,
		race.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("update race: %w", err)
	}
	return r.GetByID(ctx, race.ID)
}

// Create 新增賽事（admin 用）
func (r *Repository) Create(ctx context.Context, race *Race) (*Race, error) {
	cfgBytes, err := configToBytes(race.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	// 將 []int 轉為 pgx 可接受的 int32 slice
	dist32 := make([]int32, len(race.Distances))
	for i, d := range race.Distances {
		dist32[i] = int32(d)
	}

	reviewStatus := race.ReviewStatus
	if reviewStatus == "" {
		reviewStatus = "approved"
	}
	var createdBy interface{}
	if race.CreatedBy != "" {
		createdBy = race.CreatedBy
	}

	var id string
	err = r.db.QueryRow(ctx, `
		INSERT INTO races (slug, title, subtitle, world, blurb, hero_image_url,
		                   status, distances, group_type, group_mode,
		                   slots_total, entry_fee, start_date, end_date, config,
		                   created_by, review_status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING id`,
		race.Slug, race.Title, race.Subtitle, race.World, race.Blurb, race.HeroImageURL,
		race.Status, dist32, race.GroupType, race.GroupMode,
		race.SlotsTotal, race.EntryFee, race.StartDate, race.EndDate, cfgBytes,
		createdBy, reviewStatus,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create race: %w", err)
	}
	return r.GetByID(ctx, id)
}

// --- Registration ---

// GetRegistration 查詢使用者在某賽事的報名
func (r *Repository) GetRegistration(ctx context.Context, userID, raceID string) (*Registration, error) {
	reg := &Registration{}
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, race_id, distance, COALESCE(faction,'') as faction,
		       status, paid_at, amount
		FROM registrations WHERE user_id=$1 AND race_id=$2
	`, userID, raceID).Scan(
		&reg.ID, &reg.UserID, &reg.RaceID, &reg.Distance, &reg.Faction,
		&reg.Status, &reg.PaidAt, &reg.Amount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get registration: %w", err)
	}
	return reg, nil
}

// CreateRegistration 新增報名記錄
func (r *Repository) CreateRegistration(ctx context.Context, reg *Registration) (*Registration, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO registrations (user_id, race_id, distance, faction, status, amount)
		VALUES ($1, $2, $3, NULLIF($4,''), $5, $6)
		RETURNING id
	`, reg.UserID, reg.RaceID, reg.Distance, reg.Faction, reg.Status, reg.Amount).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create registration: %w", err)
	}
	reg.ID = id
	return reg, nil
}

// ConfirmPayment 確認付款（mock）
func (r *Repository) ConfirmPayment(ctx context.Context, regID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE registrations SET status='paid', paid_at=NOW() WHERE id=$1
	`, regID)
	return err
}

// ListRegistrations 列出某賽事的全部報名（admin 用）
func (r *Repository) ListRegistrations(ctx context.Context, raceID string) ([]*Registration, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, race_id, distance, COALESCE(faction,'') as faction,
		       status, paid_at, amount
		FROM registrations WHERE race_id=$1 ORDER BY created_at DESC
	`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var regs []*Registration
	for rows.Next() {
		reg := &Registration{}
		if err := rows.Scan(&reg.ID, &reg.UserID, &reg.RaceID, &reg.Distance,
			&reg.Faction, &reg.Status, &reg.PaidAt, &reg.Amount); err != nil {
			return nil, err
		}
		regs = append(regs, reg)
	}
	return regs, rows.Err()
}

// GetUserHandles 批次查詢 userID → handle/name（排行榜用）
func (r *Repository) GetUserHandles(ctx context.Context, userIDs []string) (map[string][2]string, error) {
	if len(userIDs) == 0 {
		return map[string][2]string{}, nil
	}
	rows, err := r.db.Query(ctx, `SELECT id, handle, name FROM users WHERE id = ANY($1)`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][2]string, len(userIDs))
	for rows.Next() {
		var id, handle, name string
		if err := rows.Scan(&id, &handle, &name); err != nil {
			return nil, err
		}
		result[id] = [2]string{handle, name}
	}
	return result, rows.Err()
}

// GetFactionByUser 查詢使用者在某賽事的陣營
func (r *Repository) GetFactionByUser(ctx context.Context, userID, raceID string) (string, error) {
	var faction string
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(faction,'') FROM registrations WHERE user_id=$1 AND race_id=$2
	`, userID, raceID).Scan(&faction)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return faction, err
}

// --- Scan helpers ---

func scanRaces(rows pgx.Rows) ([]*Race, error) {
	var races []*Race
	for rows.Next() {
		race, err := scanRaceFromRow(rows)
		if err != nil {
			return nil, err
		}
		races = append(races, race)
	}
	return races, rows.Err()
}

// scanRaceRow scans from pgx.Row (single row query)
func scanRaceRow(row pgx.Row) (*Race, error) {
	race := &Race{}
	var dist32 []int32
	var cfgBytes []byte
	err := row.Scan(
		&race.ID, &race.Slug, &race.Title, &race.Subtitle,
		&race.World, &race.Blurb, &race.HeroImageURL,
		&race.Status, &dist32, &race.GroupType, &race.GroupMode,
		&race.SlotsTotal, &race.EntryFee,
		&race.StartDate, &race.EndDate, &cfgBytes,
		&race.CreatedBy, &race.ReviewStatus, &race.ReviewNote,
		&race.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	race.Distances = make([]int, len(dist32))
	for i, d := range dist32 {
		race.Distances[i] = int(d)
	}
	race.Config, err = bytesToConfig(cfgBytes)
	return race, err
}

// scanRaceFromRow scans from pgx.Rows (multi row query)
func scanRaceFromRow(rows pgx.Rows) (*Race, error) {
	race := &Race{}
	var dist32 []int32
	var cfgBytes []byte
	err := rows.Scan(
		&race.ID, &race.Slug, &race.Title, &race.Subtitle,
		&race.World, &race.Blurb, &race.HeroImageURL,
		&race.Status, &dist32, &race.GroupType, &race.GroupMode,
		&race.SlotsTotal, &race.EntryFee,
		&race.StartDate, &race.EndDate, &cfgBytes,
		&race.CreatedBy, &race.ReviewStatus, &race.ReviewNote,
		&race.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	race.Distances = make([]int, len(dist32))
	for i, d := range dist32 {
		race.Distances[i] = int(d)
	}
	race.Config, err = bytesToConfig(cfgBytes)
	return race, err
}
