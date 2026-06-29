package race

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/promo"
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
	       status, event_mode, goal_type, distances, group_type, group_mode,
	       slots_total, entry_fee, registration_start, registration_end,
	       start_date, end_date, config, required_fields,
	       control_status, starting_soon_days, COALESCE(brochure_title,'') as brochure_title,
	       allow_team_groups,
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
			event_mode=$16, goal_type=$17, registration_start=$18, registration_end=$19,
			updated_at=NOW()
		WHERE id=$20`,
		race.Slug, race.Title, race.Subtitle, race.World, race.Blurb, race.HeroImageURL,
		race.Status, dist32, race.GroupType, race.GroupMode,
		race.SlotsTotal, race.EntryFee, race.StartDate, race.EndDate, cfgBytes,
		race.EventMode, race.GoalType, race.RegStart, race.RegEnd,
		race.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("update race: %w", err)
	}
	return r.GetByID(ctx, race.ID)
}

// CreateWithChildren 在單一交易內建立賽事 + 分組 + 加購 + 物資。
// 物資的 GroupIndex 對應 req.Groups 陣列索引，插完分組後回填成實際 group UUID。
func (r *Repository) CreateWithChildren(ctx context.Context, req *CreateRaceRequest) (*RaceDetail, error) {
	race := &req.Race

	cfgBytes, err := configToBytes(race.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
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

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	// nil（payload 未帶此鍵）→ 套用預設；明確空陣列 []→ 尊重「全部選填」
	requiredFields := race.RequiredFields
	if requiredFields == nil {
		requiredFields = []string{"real_name", "phone"}
	}

	controlStatus := race.ControlStatus
	if controlStatus == "" {
		controlStatus = "active"
	}
	startingSoonDays := race.StartingSoonDays
	if startingSoonDays <= 0 {
		startingSoonDays = 5
	}

	var raceID string
	err = tx.QueryRow(ctx, `
		INSERT INTO races (slug, title, subtitle, world, blurb, hero_image_url,
		                   status, event_mode, goal_type, distances, group_type, group_mode,
		                   slots_total, entry_fee, registration_start, registration_end,
		                   start_date, end_date, config, created_by, review_status, required_fields,
		                   control_status, starting_soon_days, brochure_title, allow_team_groups)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
		RETURNING id`,
		race.Slug, race.Title, race.Subtitle, race.World, race.Blurb, race.HeroImageURL,
		race.Status, race.EventMode, race.GoalType, dist32, race.GroupType, race.GroupMode,
		race.SlotsTotal, race.EntryFee, race.RegStart, race.RegEnd,
		race.StartDate, race.EndDate, cfgBytes, createdBy, reviewStatus, requiredFields,
		controlStatus, startingSoonDays, race.BrochureTitle, race.AllowTeamGroups,
	).Scan(&raceID)
	if err != nil {
		return nil, fmt.Errorf("insert race: %w", err)
	}

	// 測試白名單
	for _, email := range req.TestWhitelist {
		if e := normEmail(email); e != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO race_test_whitelist (race_id, email) VALUES ($1,$2) ON CONFLICT DO NOTHING`, raceID, e); err != nil {
				return nil, fmt.Errorf("insert whitelist: %w", err)
			}
		}
	}

	// 簡章區塊
	if err = insertBrochure(ctx, tx, raceID, req.Brochure); err != nil {
		return nil, err
	}

	// 分組（記錄索引 → 實際 UUID，供物資對應）
	groupIDByIndex := make([]string, len(req.Groups))
	for i := range req.Groups {
		g := &req.Groups[i]
		var gid string
		err = tx.QueryRow(ctx, `
			INSERT INTO race_groups (race_id, name, description, display_order,
			                         slot_limit, gender_limit, age_min, age_max, target_distance_km,
			                         requires_key, group_key)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			RETURNING id`,
			raceID, g.Name, nullStr(g.Description), g.DisplayOrder,
			g.SlotLimit, defaultStr(g.GenderLimit, "any"), g.AgeMin, g.AgeMax, g.TargetDistanceKm,
			g.RequiresKey, groupKeyVal(g.RequiresKey, g.GroupKey),
		).Scan(&gid)
		if err != nil {
			return nil, fmt.Errorf("insert group %d: %w", i, err)
		}
		groupIDByIndex[i] = gid
	}

	// 加購
	for i := range req.Addons {
		a := &req.Addons[i]
		_, err = tx.Exec(ctx, `
			INSERT INTO race_addons (race_id, name, description, image_url, price_cents,
			                         per_user_limit, total_stock, display_order, active)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			raceID, a.Name, nullStr(a.Description), nullStr(a.ImageURL), a.PriceCents,
			a.PerUserLimit, a.TotalStock, a.DisplayOrder, a.Active,
		)
		if err != nil {
			return nil, fmt.Errorf("insert addon %d: %w", i, err)
		}
	}

	// 物資（GroupIndex → group UUID；nil 或越界 = 賽事層級共用）
	for i := range req.Supplies {
		s := &req.Supplies[i]
		var groupID interface{}
		if s.GroupIndex != nil && *s.GroupIndex >= 0 && *s.GroupIndex < len(groupIDByIndex) {
			groupID = groupIDByIndex[*s.GroupIndex]
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO race_supplies (race_id, group_id, kind, name, description, image_url, display_order)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			raceID, groupID, s.Kind, s.Name, nullStr(s.Description), nullStr(s.ImageURL), s.DisplayOrder,
		)
		if err != nil {
			return nil, fmt.Errorf("insert supply %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return r.GetDetail(ctx, raceID)
}

// UpdateWithChildren 在單一交易內更新賽事 + 同步分組/加購/物資。
// 分組與加購用 id upsert（保留 slots_taken/sold_count 計數器）並刪除 payload 中沒有的；
// 物資直接整批刪除重建（無計數器、無外部 FK 依賴）。
// 物資的 GroupIndex 對應 req.Groups 陣列索引（含更新後的最終 group id）。
func (r *Repository) UpdateWithChildren(ctx context.Context, raceID string, req *CreateRaceRequest) (*RaceDetail, error) {
	race := &req.Race

	cfgBytes, err := configToBytes(race.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	dist32 := make([]int32, len(race.Distances))
	for i, d := range race.Distances {
		dist32[i] = int32(d)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	requiredFields := race.RequiredFields
	if requiredFields == nil {
		requiredFields = []string{"real_name", "phone"}
	}

	controlStatus := race.ControlStatus
	if controlStatus == "" {
		controlStatus = "active"
	}
	startingSoonDays := race.StartingSoonDays
	if startingSoonDays <= 0 {
		startingSoonDays = 5
	}

	// 1. 更新賽事本體
	_, err = tx.Exec(ctx, `
		UPDATE races SET
			slug=$1, title=$2, subtitle=$3, world=$4, blurb=$5, hero_image_url=$6,
			status=$7, distances=$8, group_type=$9, group_mode=$10,
			slots_total=$11, entry_fee=$12, start_date=$13, end_date=$14, config=$15,
			event_mode=$16, goal_type=$17, registration_start=$18, registration_end=$19,
			required_fields=$20, control_status=$21, starting_soon_days=$22, brochure_title=$23,
			allow_team_groups=$24, updated_at=NOW()
		WHERE id=$25`,
		race.Slug, race.Title, race.Subtitle, race.World, race.Blurb, race.HeroImageURL,
		race.Status, dist32, race.GroupType, race.GroupMode,
		race.SlotsTotal, race.EntryFee, race.StartDate, race.EndDate, cfgBytes,
		race.EventMode, race.GoalType, race.RegStart, race.RegEnd,
		requiredFields, controlStatus, startingSoonDays, race.BrochureTitle, race.AllowTeamGroups, raceID,
	)
	if err != nil {
		return nil, fmt.Errorf("update race: %w", err)
	}

	// 測試白名單：整批重建
	if _, err = tx.Exec(ctx, `DELETE FROM race_test_whitelist WHERE race_id=$1`, raceID); err != nil {
		return nil, fmt.Errorf("clear whitelist: %w", err)
	}
	for _, email := range req.TestWhitelist {
		if e := normEmail(email); e != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO race_test_whitelist (race_id, email) VALUES ($1,$2) ON CONFLICT DO NOTHING`, raceID, e); err != nil {
				return nil, fmt.Errorf("insert whitelist: %w", err)
			}
		}
	}

	// 簡章區塊：整批重建
	if _, err = tx.Exec(ctx, `DELETE FROM race_brochure_blocks WHERE race_id=$1`, raceID); err != nil {
		return nil, fmt.Errorf("clear brochure: %w", err)
	}
	if err = insertBrochure(ctx, tx, raceID, req.Brochure); err != nil {
		return nil, err
	}

	// 2. 同步分組（upsert by id，記錄最終 id 供物資對應），刪除已移除的
	finalGroupIDs := make([]string, len(req.Groups))
	keptGroups := make([]string, 0, len(req.Groups))
	for i := range req.Groups {
		g := &req.Groups[i]
		if g.ID != "" {
			_, err = tx.Exec(ctx, `
				UPDATE race_groups SET name=$1, description=$2, display_order=$3,
				    slot_limit=$4, gender_limit=$5, age_min=$6, age_max=$7, target_distance_km=$8,
				    requires_key=$9, group_key=$10
				WHERE id=$11 AND race_id=$12`,
				g.Name, nullStr(g.Description), g.DisplayOrder,
				g.SlotLimit, defaultStr(g.GenderLimit, "any"), g.AgeMin, g.AgeMax, g.TargetDistanceKm,
				g.RequiresKey, groupKeyVal(g.RequiresKey, g.GroupKey), g.ID, raceID,
			)
			if err != nil {
				return nil, fmt.Errorf("update group %d: %w", i, err)
			}
			finalGroupIDs[i] = g.ID
		} else {
			var gid string
			err = tx.QueryRow(ctx, `
				INSERT INTO race_groups (race_id, name, description, display_order,
				                         slot_limit, gender_limit, age_min, age_max, target_distance_km,
				                         requires_key, group_key)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
				raceID, g.Name, nullStr(g.Description), g.DisplayOrder,
				g.SlotLimit, defaultStr(g.GenderLimit, "any"), g.AgeMin, g.AgeMax, g.TargetDistanceKm,
				g.RequiresKey, groupKeyVal(g.RequiresKey, g.GroupKey),
			).Scan(&gid)
			if err != nil {
				return nil, fmt.Errorf("insert group %d: %w", i, err)
			}
			finalGroupIDs[i] = gid
		}
		keptGroups = append(keptGroups, finalGroupIDs[i])
	}
	// 刪除 payload 中不存在的「官方」分組；前台自建分組(created_by 非空)永不被後台儲存誤刪。
	// （若該分組已有報名，FK RESTRICT 會讓交易失敗 → 正確阻擋）
	if _, err = tx.Exec(ctx,
		`DELETE FROM race_groups WHERE race_id=$1 AND created_by IS NULL AND NOT (id = ANY($2::uuid[]))`,
		raceID, keptGroups,
	); err != nil {
		return nil, fmt.Errorf("delete removed groups: %w", err)
	}

	// 3. 同步加購（upsert by id，保留 sold_count），刪除已移除的
	keptAddons := make([]string, 0, len(req.Addons))
	for i := range req.Addons {
		a := &req.Addons[i]
		if a.ID != "" {
			_, err = tx.Exec(ctx, `
				UPDATE race_addons SET name=$1, description=$2, image_url=$3, price_cents=$4,
				    per_user_limit=$5, total_stock=$6, display_order=$7, active=$8
				WHERE id=$9 AND race_id=$10`,
				a.Name, nullStr(a.Description), nullStr(a.ImageURL), a.PriceCents,
				a.PerUserLimit, a.TotalStock, a.DisplayOrder, a.Active, a.ID, raceID,
			)
			if err != nil {
				return nil, fmt.Errorf("update addon %d: %w", i, err)
			}
			keptAddons = append(keptAddons, a.ID)
		} else {
			var aid string
			err = tx.QueryRow(ctx, `
				INSERT INTO race_addons (race_id, name, description, image_url, price_cents,
				                         per_user_limit, total_stock, display_order, active)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
				raceID, a.Name, nullStr(a.Description), nullStr(a.ImageURL), a.PriceCents,
				a.PerUserLimit, a.TotalStock, a.DisplayOrder, a.Active,
			).Scan(&aid)
			if err != nil {
				return nil, fmt.Errorf("insert addon %d: %w", i, err)
			}
			keptAddons = append(keptAddons, aid)
		}
	}
	if _, err = tx.Exec(ctx,
		`DELETE FROM race_addons WHERE race_id=$1 AND NOT (id = ANY($2::uuid[]))`,
		raceID, keptAddons,
	); err != nil {
		return nil, fmt.Errorf("delete removed addons: %w", err)
	}

	// 4. 物資整批重建
	if _, err = tx.Exec(ctx, `DELETE FROM race_supplies WHERE race_id=$1`, raceID); err != nil {
		return nil, fmt.Errorf("clear supplies: %w", err)
	}
	for i := range req.Supplies {
		s := &req.Supplies[i]
		var groupID interface{}
		if s.GroupIndex != nil && *s.GroupIndex >= 0 && *s.GroupIndex < len(finalGroupIDs) {
			groupID = finalGroupIDs[*s.GroupIndex]
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO race_supplies (race_id, group_id, kind, name, description, image_url, display_order)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			raceID, groupID, s.Kind, s.Name, nullStr(s.Description), nullStr(s.ImageURL), s.DisplayOrder,
		); err != nil {
			return nil, fmt.Errorf("insert supply %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return r.GetDetail(ctx, raceID)
}

// Create 建立賽事（無子資料；供合作方提交等簡單流程沿用）
func (r *Repository) Create(ctx context.Context, race *Race) (*Race, error) {
	detail, err := r.CreateWithChildren(ctx, &CreateRaceRequest{Race: *race})
	if err != nil {
		return nil, err
	}
	return &detail.Race, nil
}

// GetDetail 取得賽事 + 巢狀分組/加購/物資（後台編輯載入用）
func (r *Repository) GetDetail(ctx context.Context, raceID string) (*RaceDetail, error) {
	race, err := r.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil {
		return nil, nil
	}
	groups, err := r.GetGroups(ctx, raceID)
	if err != nil {
		return nil, err
	}
	addons, err := r.GetAddons(ctx, raceID)
	if err != nil {
		return nil, err
	}
	supplies, err := r.GetSupplies(ctx, raceID)
	if err != nil {
		return nil, err
	}
	whitelist, err := r.GetTestWhitelist(ctx, raceID)
	if err != nil {
		return nil, err
	}
	brochure, err := r.GetBrochure(ctx, raceID)
	if err != nil {
		return nil, err
	}
	return &RaceDetail{Race: *race, Groups: groups, Addons: addons, Supplies: supplies, TestWhitelist: whitelist, Brochure: brochure}, nil
}

// insertBrochure 依陣列順序寫入簡章區塊（交易內，呼叫前須先清空舊區塊）
func insertBrochure(ctx context.Context, tx pgx.Tx, raceID string, blocks []BrochureBlock) error {
	for i := range blocks {
		b := &blocks[i]
		if b.BlockType == "" || b.Content == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO race_brochure_blocks (race_id, block_type, content, caption, display_order)
			VALUES ($1,$2,$3,NULLIF($4,''),$5)`,
			raceID, b.BlockType, b.Content, b.Caption, i); err != nil {
			return fmt.Errorf("insert brochure block %d: %w", i, err)
		}
	}
	return nil
}

// GetBrochure 取得賽事簡章區塊（依顯示順序）
func (r *Repository) GetBrochure(ctx context.Context, raceID string) ([]BrochureBlock, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, block_type, content, COALESCE(caption,''), display_order
		FROM race_brochure_blocks WHERE race_id=$1 ORDER BY display_order, created_at`, raceID)
	if err != nil {
		return nil, fmt.Errorf("get brochure: %w", err)
	}
	defer rows.Close()
	out := []BrochureBlock{}
	for rows.Next() {
		var b BrochureBlock
		if err := rows.Scan(&b.ID, &b.BlockType, &b.Content, &b.Caption, &b.DisplayOrder); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- 測試白名單 ---

// GetTestWhitelist 取得某賽事的專屬白名單 email
func (r *Repository) GetTestWhitelist(ctx context.Context, raceID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT email FROM race_test_whitelist WHERE race_id=$1 ORDER BY email`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// IsEmailWhitelisted 該 email 是否在「該賽事白名單 ∪ 全域預設白名單」內
func (r *Repository) IsEmailWhitelisted(ctx context.Context, raceID, email string) (bool, error) {
	e := normEmail(email)
	if e == "" {
		return false, nil
	}
	var ok bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM race_test_whitelist WHERE race_id=$1 AND lower(email)=$2)
		    OR EXISTS(SELECT 1 FROM default_test_whitelist WHERE lower(email)=$2)`,
		raceID, e).Scan(&ok)
	return ok, err
}

// GetUserEmail 取得使用者 email（白名單比對用）
func (r *Repository) GetUserEmail(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", nil
	}
	var email string
	err := r.db.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, userID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return email, err
}

func (r *Repository) ListDefaultWhitelist(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT email FROM default_test_whitelist ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repository) AddDefaultWhitelist(ctx context.Context, email string) error {
	e := normEmail(email)
	if e == "" {
		return fmt.Errorf("email required")
	}
	_, err := r.db.Exec(ctx, `INSERT INTO default_test_whitelist (email) VALUES ($1) ON CONFLICT DO NOTHING`, e)
	return err
}

func (r *Repository) RemoveDefaultWhitelist(ctx context.Context, email string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM default_test_whitelist WHERE lower(email)=$1`, normEmail(email))
	return err
}

// GetGroups 取得賽事的所有分組
func (r *Repository) GetGroups(ctx context.Context, raceID string) ([]RaceGroup, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, race_id, name, COALESCE(description,''), display_order,
		       slot_limit, slots_taken, gender_limit, age_min, age_max, target_distance_km,
		       requires_key, COALESCE(group_key,''), COALESCE(created_by::text,'')
		FROM race_groups WHERE race_id=$1 ORDER BY display_order, created_at`, raceID)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	groups := []RaceGroup{}
	for rows.Next() {
		var g RaceGroup
		if err := rows.Scan(&g.ID, &g.RaceID, &g.Name, &g.Description, &g.DisplayOrder,
			&g.SlotLimit, &g.SlotsTaken, &g.GenderLimit, &g.AgeMin, &g.AgeMax, &g.TargetDistanceKm,
			&g.RequiresKey, &g.GroupKey, &g.CreatedBy); err != nil {
			return nil, err
		}
		g.IsUserCreated = g.CreatedBy != ""
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// GetAddons 取得賽事的所有加購項目
func (r *Repository) GetAddons(ctx context.Context, raceID string) ([]RaceAddon, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, race_id, name, COALESCE(description,''), COALESCE(image_url,''),
		       price_cents, per_user_limit, total_stock, sold_count, display_order, active
		FROM race_addons WHERE race_id=$1 ORDER BY display_order, created_at`, raceID)
	if err != nil {
		return nil, fmt.Errorf("list addons: %w", err)
	}
	defer rows.Close()

	addons := []RaceAddon{}
	for rows.Next() {
		var a RaceAddon
		if err := rows.Scan(&a.ID, &a.RaceID, &a.Name, &a.Description, &a.ImageURL,
			&a.PriceCents, &a.PerUserLimit, &a.TotalStock, &a.SoldCount, &a.DisplayOrder, &a.Active); err != nil {
			return nil, err
		}
		addons = append(addons, a)
	}
	return addons, rows.Err()
}

// GetSupplies 取得賽事的所有物資（含共用與分組）
func (r *Repository) GetSupplies(ctx context.Context, raceID string) ([]RaceSupply, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, race_id, COALESCE(group_id::text,''), kind, name,
		       COALESCE(description,''), COALESCE(image_url,''), display_order
		FROM race_supplies WHERE race_id=$1 ORDER BY display_order, created_at`, raceID)
	if err != nil {
		return nil, fmt.Errorf("list supplies: %w", err)
	}
	defer rows.Close()

	supplies := []RaceSupply{}
	for rows.Next() {
		var s RaceSupply
		if err := rows.Scan(&s.ID, &s.RaceID, &s.GroupID, &s.Kind, &s.Name,
			&s.Description, &s.ImageURL, &s.DisplayOrder); err != nil {
			return nil, err
		}
		supplies = append(supplies, s)
	}
	return supplies, rows.Err()
}

// GetStandings 取得某賽事的所有分組成績（worker 預聚合的 race_group_standings）
func (r *Repository) GetStandings(ctx context.Context, raceID string) ([]GroupStanding, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.group_id, g.name, s.total_km, s.member_count, s.avg_km, s.avg_pace_s, s.finish_total_s
		FROM race_group_standings s
		JOIN race_groups g ON g.id = s.group_id
		WHERE s.race_id = $1`, raceID)
	if err != nil {
		return nil, fmt.Errorf("get standings: %w", err)
	}
	defer rows.Close()

	standings := []GroupStanding{}
	for rows.Next() {
		var s GroupStanding
		if err := rows.Scan(&s.GroupID, &s.GroupName, &s.TotalKm, &s.MemberCount,
			&s.AvgKm, &s.AvgPaceS, &s.FinishTotalS); err != nil {
			return nil, err
		}
		standings = append(standings, s)
	}
	return standings, rows.Err()
}

// GetUserRegistrations 取得使用者所有報名的精簡狀態（race_id → 狀態），供賽事列表附帶
func (r *Repository) GetUserRegistrations(ctx context.Context, userID string) (map[string]MyRegLite, error) {
	rows, err := r.db.Query(ctx,
		`SELECT race_id, status, group_revealed FROM registrations WHERE user_id=$1`, userID)
	if err != nil {
		return nil, fmt.Errorf("get user registrations: %w", err)
	}
	defer rows.Close()

	m := map[string]MyRegLite{}
	for rows.Next() {
		var raceID string
		var lite MyRegLite
		if err := rows.Scan(&raceID, &lite.Status, &lite.GroupRevealed); err != nil {
			return nil, err
		}
		m[raceID] = lite
	}
	return m, rows.Err()
}

// GetUserGroupID 取得使用者在某賽事報名的分組 id（無報名或未分組回空字串）
func (r *Repository) GetUserGroupID(ctx context.Context, userID, raceID string) (string, error) {
	var gid *string
	err := r.db.QueryRow(ctx,
		`SELECT group_id::text FROM registrations WHERE user_id=$1 AND race_id=$2`,
		userID, raceID).Scan(&gid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if gid == nil {
		return "", nil
	}
	return *gid, nil
}

// --- Group presets ---

// ListPresets 取得分組預設選單
func (r *Repository) ListPresets(ctx context.Context) ([]GroupPreset, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, default_distance_km, is_system
		FROM group_presets ORDER BY is_system DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("list presets: %w", err)
	}
	defer rows.Close()

	presets := []GroupPreset{}
	for rows.Next() {
		var p GroupPreset
		if err := rows.Scan(&p.ID, &p.Name, &p.DefaultDistanceKm, &p.IsSystem); err != nil {
			return nil, err
		}
		presets = append(presets, p)
	}
	return presets, rows.Err()
}

// CreatePreset 新增分組預設（後台擴充選單用）。name 重複時回傳既有的。
func (r *Repository) CreatePreset(ctx context.Context, name string, distanceKm *float64) (*GroupPreset, error) {
	p := &GroupPreset{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO group_presets (name, default_distance_km, is_system)
		VALUES ($1, $2, FALSE)
		ON CONFLICT (name) DO UPDATE SET default_distance_km = EXCLUDED.default_distance_km
		RETURNING id, name, default_distance_km, is_system`,
		name, distanceKm,
	).Scan(&p.ID, &p.Name, &p.DefaultDistanceKm, &p.IsSystem)
	if err != nil {
		return nil, fmt.Errorf("create preset: %w", err)
	}
	return p, nil
}

// normEmail 正規化 email（去空白、小寫）
func normEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// nullStr 空字串轉 nil，讓 DB 存 NULL 而非空字串
func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// defaultStr 空字串時回傳預設值
func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// groupKeyVal 只有在「需要鑰匙且鑰匙非空」時才存明碼，否則存 NULL
func groupKeyVal(requiresKey bool, key string) interface{} {
	if !requiresKey || key == "" {
		return nil
	}
	return key
}

// UserCanCreateTeamGroup 讀取使用者是否具「開放建立跑團分組」權限
func (r *Repository) UserCanCreateTeamGroup(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT can_create_team_group FROM users WHERE id=$1`, userID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return ok, err
}

// CreateTeamGroup 前台跑團成員自建分組（competition + allow_team_groups 已於 service 驗證）。
// display_order 接在現有分組之後；slot_limit 不限；created_by 記錄自建者。
func (r *Repository) CreateTeamGroup(ctx context.Context, in CreateTeamGroupRequest) (*RaceGroup, error) {
	var nextOrder int
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(display_order),0)+1 FROM race_groups WHERE race_id=$1`, in.RaceID,
	).Scan(&nextOrder); err != nil {
		return nil, fmt.Errorf("next order: %w", err)
	}
	g := &RaceGroup{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO race_groups (race_id, name, description, display_order,
		                         gender_limit, target_distance_km, requires_key, group_key, created_by)
		VALUES ($1,$2,$3,$4,'any',$5,$6,$7,$8)
		RETURNING id, race_id, name, COALESCE(description,''), display_order,
		          slot_limit, slots_taken, gender_limit, age_min, age_max, target_distance_km,
		          requires_key, COALESCE(created_by::text,'')`,
		in.RaceID, in.Name, nullStr(in.Description), nextOrder,
		in.TargetDistanceKm, in.RequiresKey, groupKeyVal(in.RequiresKey, in.GroupKey), in.UserID,
	).Scan(&g.ID, &g.RaceID, &g.Name, &g.Description, &g.DisplayOrder,
		&g.SlotLimit, &g.SlotsTaken, &g.GenderLimit, &g.AgeMin, &g.AgeMax, &g.TargetDistanceKm,
		&g.RequiresKey, &g.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("insert team group: %w", err)
	}
	g.IsUserCreated = g.CreatedBy != ""
	g.GroupKey = "" // 不回傳明碼
	return g, nil
}

// CountRegistrations 計算某賽事的報名筆數（刪除前檢查用）
func (r *Repository) CountRegistrations(ctx context.Context, raceID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM registrations WHERE race_id=$1`, raceID).Scan(&n)
	return n, err
}

// Delete 刪除賽事。子表（race_groups/race_addons/race_supplies/race_group_standings）
// 由 FK ON DELETE CASCADE 連帶清除；呼叫方須先確認無報名等阻擋資料。
func (r *Repository) Delete(ctx context.Context, raceID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM races WHERE id=$1`, raceID)
	if err != nil {
		return fmt.Errorf("delete race: %w", err)
	}
	return nil
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

// RegisterTxInput 報名交易輸入（service 已完成登入/必填/性別年齡驗證）
type RegisterTxInput struct {
	UserID        string
	RaceID        string
	GroupID       string
	GroupKey      string
	EntryFee      int
	GroupRevealed bool
	Distance      int
	Addons        []AddonSelection
	Participant   ParticipantInfo
	PromoCode     string
}

// RegisterWithOrder 在單一交易內完成報名：分組名額 row-lock 防超賣、加購庫存、
// 寫 registration + order/order_items、個資回填（只補空欄位）。
func (r *Repository) RegisterWithOrder(ctx context.Context, in RegisterTxInput) (*RegisterResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. 鎖分組、檢查名額、驗證跑團鑰匙
	var slotLimit *int
	var slotsTaken int
	var groupName string
	var requiresKey bool
	var groupKey *string
	err = tx.QueryRow(ctx, `
		SELECT slot_limit, slots_taken, name, requires_key, group_key FROM race_groups
		WHERE id=$1 AND race_id=$2 FOR UPDATE`,
		in.GroupID, in.RaceID).Scan(&slotLimit, &slotsTaken, &groupName, &requiresKey, &groupKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock group: %w", err)
	}
	if requiresKey {
		want := ""
		if groupKey != nil {
			want = *groupKey
		}
		if want != "" && in.GroupKey != want {
			return nil, ErrGroupKeyWrong
		}
	}
	if slotLimit != nil && slotsTaken >= *slotLimit {
		return nil, ErrGroupFull
	}
	if _, err = tx.Exec(ctx, `UPDATE race_groups SET slots_taken = slots_taken + 1 WHERE id=$1`, in.GroupID); err != nil {
		return nil, fmt.Errorf("bump slots: %w", err)
	}

	// 2. 加購：鎖庫存、檢查、扣量、累計加購金額
	addonsTotal := 0
	type lineItem struct {
		addonID   string
		qty       int
		unitCents int
	}
	var items []lineItem
	for _, a := range in.Addons {
		if a.Qty <= 0 {
			continue
		}
		var price int
		var totalStock, perUserLimit *int
		var soldCount int
		err = tx.QueryRow(ctx, `
			SELECT price_cents, total_stock, per_user_limit, sold_count FROM race_addons
			WHERE id=$1 AND race_id=$2 AND active FOR UPDATE`,
			a.AddonID, in.RaceID).Scan(&price, &totalStock, &perUserLimit, &soldCount)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddonNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("lock addon: %w", err)
		}
		if perUserLimit != nil && a.Qty > *perUserLimit {
			return nil, ErrAddonLimit
		}
		if totalStock != nil && soldCount+a.Qty > *totalStock {
			return nil, ErrAddonSoldOut
		}
		if _, err = tx.Exec(ctx, `UPDATE race_addons SET sold_count = sold_count + $1 WHERE id=$2`, a.Qty, a.AddonID); err != nil {
			return nil, fmt.Errorf("bump sold_count: %w", err)
		}
		addonsTotal += price * a.Qty
		items = append(items, lineItem{a.AddonID, a.Qty, price})
	}

	// 2b. 優惠序號：鎖定 + 驗證 + 計算折抵（只折報名費）
	discount := 0
	var appliedPromo *promo.PromoCode
	if in.PromoCode != "" {
		ap, err := promo.LockAndValidateTx(ctx, tx, in.PromoCode, in.RaceID, in.UserID, time.Now())
		if err != nil {
			return nil, err
		}
		appliedPromo = ap
		discount = promo.DiscountCents(ap, in.EntryFee)
	}

	// 應付 = max(0, 報名費-折抵) + 加購；不足 0.5 元（<50 分）視為 0、直接完成不跳金流
	payable := in.EntryFee - discount
	if payable < 0 {
		payable = 0
	}
	payable += addonsTotal
	paid := payable < 50
	regStatus := "pending"
	var paymentRef interface{}
	if paid {
		regStatus = "paid"
		if appliedPromo != nil {
			paymentRef = "PROMO:" + appliedPromo.Code
		}
	}

	// 3. registration（UNIQUE(user_id,race_id) 衝突 → 已報名）
	reg := &Registration{
		UserID: in.UserID, RaceID: in.RaceID, GroupID: in.GroupID,
		Distance: in.Distance, GroupRevealed: in.GroupRevealed,
		Status: regStatus, Amount: payable,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO registrations
			(user_id, race_id, group_id, distance, status, amount,
			 group_revealed, snap_real_name, snap_phone, snap_address, paid_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),
		        CASE WHEN $11 THEN NOW() ELSE NULL END)
		RETURNING id`,
		in.UserID, in.RaceID, in.GroupID, in.Distance, regStatus, payable, in.GroupRevealed,
		in.Participant.RealName, in.Participant.Phone, in.Participant.Address, paid,
	).Scan(&reg.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyRegistered
		}
		return nil, fmt.Errorf("insert registration: %w", err)
	}

	// 4. order + order_items
	order := &Order{TotalCents: payable, Status: regStatus}
	if err = tx.QueryRow(ctx, `
		INSERT INTO orders (user_id, race_id, registration_id, total_cents, status, payment_ref, paid_at)
		VALUES ($1,$2,$3,$4,$5,$6,CASE WHEN $7 THEN NOW() ELSE NULL END) RETURNING id`,
		in.UserID, in.RaceID, reg.ID, payable, regStatus, paymentRef, paid).Scan(&order.ID); err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}
	if in.EntryFee > 0 {
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_items (order_id, item_type, qty, unit_price_cents, subtotal_cents)
			VALUES ($1,'entry',1,$2,$2)`, order.ID, in.EntryFee); err != nil {
			return nil, fmt.Errorf("insert entry item: %w", err)
		}
	}
	if discount > 0 {
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_items (order_id, item_type, qty, unit_price_cents, subtotal_cents)
			VALUES ($1,'discount',1,$2,$2)`, order.ID, -discount); err != nil {
			return nil, fmt.Errorf("insert discount item: %w", err)
		}
	}
	for _, it := range items {
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_items (order_id, item_type, addon_id, qty, unit_price_cents, subtotal_cents)
			VALUES ($1,'addon',$2,$3,$4,$5)`,
			order.ID, it.addonID, it.qty, it.unitCents, it.unitCents*it.qty); err != nil {
			return nil, fmt.Errorf("insert addon item: %w", err)
		}
	}

	// 4b. 記錄序號使用
	if appliedPromo != nil {
		if err = promo.RecordUsageTx(ctx, tx, appliedPromo.ID, in.UserID, in.RaceID, reg.ID, order.ID, discount); err != nil {
			return nil, fmt.Errorf("record promo usage: %w", err)
		}
	}

	// 5. 個資回填：只補目前為空的欄位
	p := in.Participant
	if _, err = tx.Exec(ctx, `
		INSERT INTO user_profiles (user_id, real_name, nickname, phone, address, birthday, gender, updated_at)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), NULLIF($6,'')::date, NULLIF($7,''), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			real_name = COALESCE(user_profiles.real_name, EXCLUDED.real_name),
			nickname  = COALESCE(user_profiles.nickname,  EXCLUDED.nickname),
			phone     = COALESCE(user_profiles.phone,     EXCLUDED.phone),
			address   = COALESCE(user_profiles.address,   EXCLUDED.address),
			birthday  = COALESCE(user_profiles.birthday,  EXCLUDED.birthday),
			gender    = COALESCE(user_profiles.gender,    EXCLUDED.gender),
			updated_at = NOW()`,
		in.UserID, p.RealName, p.Nickname, p.Phone, p.Address, p.Birthday, p.Gender,
	); err != nil {
		return nil, fmt.Errorf("upsert profile: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &RegisterResult{
		Registration:  reg,
		Order:         order,
		AssignedGroup: groupName,
		GroupRevealed: in.GroupRevealed,
		DiscountCents: discount,
		PayableCents:  payable,
		Paid:          paid,
	}, nil
}

// isUniqueViolation 判斷是否為 Postgres 唯一鍵衝突（SQLSTATE 23505）
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
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

// --- 後台報名 / 訂單管理 ---

// ListSignups 列出某賽事的報名（含會員、分組、訂單狀態），q 可搜尋姓名/email/手機
func (r *Repository) ListSignups(ctx context.Context, raceID, q string) ([]SignupRow, error) {
	like := "%" + q + "%"
	rows, err := r.db.Query(ctx, `
		SELECT reg.id, u.name, u.email, COALESCE(g.name,''), reg.status,
		       reg.group_revealed, COALESCE(reg.snap_real_name,''), COALESCE(reg.snap_phone,''),
		       reg.created_at,
		       COALESCE(o.id::text,''), COALESCE(o.total_cents,0), COALESCE(o.status,'')
		FROM registrations reg
		JOIN users u ON u.id = reg.user_id
		LEFT JOIN race_groups g ON g.id = reg.group_id
		LEFT JOIN orders o ON o.registration_id = reg.id
		WHERE reg.race_id = $1
		  AND ($2='' OR u.name ILIKE $3 OR u.email ILIKE $3
		       OR COALESCE(reg.snap_real_name,'') ILIKE $3 OR COALESCE(reg.snap_phone,'') ILIKE $3)
		ORDER BY reg.created_at DESC`, raceID, q, like)
	if err != nil {
		return nil, fmt.Errorf("list signups: %w", err)
	}
	defer rows.Close()

	out := []SignupRow{}
	for rows.Next() {
		var s SignupRow
		if err := rows.Scan(&s.ID, &s.UserName, &s.UserEmail, &s.GroupName, &s.Status,
			&s.GroupRevealed, &s.SnapRealName, &s.SnapPhone, &s.CreatedAt,
			&s.OrderID, &s.OrderTotal, &s.OrderStatus); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListOrders 列出訂單（race_id/status 可選過濾）
func (r *Repository) ListOrders(ctx context.Context, raceID, status string, limit, offset int) ([]OrderRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT o.id, u.name, u.email, rc.title, o.total_cents, o.status,
		       COALESCE(o.payment_ref,''), o.paid_at, o.created_at, COALESCE(o.registration_id::text,'')
		FROM orders o
		JOIN users u ON u.id = o.user_id
		JOIN races rc ON rc.id = o.race_id
		WHERE ($1='' OR o.race_id = $1::uuid)
		  AND ($2='' OR o.status = $2)
		ORDER BY o.created_at DESC
		LIMIT $3 OFFSET $4`, raceID, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	out := []OrderRow{}
	for rows.Next() {
		var o OrderRow
		if err := rows.Scan(&o.ID, &o.UserName, &o.UserEmail, &o.RaceTitle, &o.TotalCents,
			&o.Status, &o.PaymentRef, &o.PaidAt, &o.CreatedAt, &o.RegistrationID); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetOrderDetail 取得訂單 + 明細
func (r *Repository) GetOrderDetail(ctx context.Context, orderID string) (*OrderDetail, error) {
	var o OrderRow
	err := r.db.QueryRow(ctx, `
		SELECT o.id, u.name, u.email, rc.title, o.total_cents, o.status,
		       COALESCE(o.payment_ref,''), o.paid_at, o.created_at, COALESCE(o.registration_id::text,'')
		FROM orders o JOIN users u ON u.id=o.user_id JOIN races rc ON rc.id=o.race_id
		WHERE o.id=$1`, orderID).Scan(
		&o.ID, &o.UserName, &o.UserEmail, &o.RaceTitle, &o.TotalCents, &o.Status,
		&o.PaymentRef, &o.PaidAt, &o.CreatedAt, &o.RegistrationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT oi.item_type, COALESCE(a.name,''), oi.qty, oi.unit_price_cents, oi.subtotal_cents
		FROM order_items oi LEFT JOIN race_addons a ON a.id = oi.addon_id
		WHERE oi.order_id=$1 ORDER BY oi.item_type`, orderID)
	if err != nil {
		return nil, fmt.Errorf("get order items: %w", err)
	}
	defer rows.Close()

	detail := &OrderDetail{OrderRow: o, Items: []OrderItemRow{}}
	for rows.Next() {
		var it OrderItemRow
		if err := rows.Scan(&it.ItemType, &it.AddonName, &it.Qty, &it.UnitPriceCents, &it.SubtotalCents); err != nil {
			return nil, err
		}
		detail.Items = append(detail.Items, it)
	}
	return detail, rows.Err()
}

// MarkOrderPaid 標記訂單已付，並連動其對應 registration（交易）
func (r *Repository) MarkOrderPaid(ctx context.Context, orderID, paymentRef string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var regID *string
	err = tx.QueryRow(ctx, `
		UPDATE orders SET status='paid', paid_at=NOW(), payment_ref=NULLIF($2,'')
		WHERE id=$1 RETURNING registration_id`, orderID, paymentRef).Scan(&regID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOrderNotFound
	}
	if err != nil {
		return fmt.Errorf("mark order paid: %w", err)
	}
	if regID != nil {
		if _, err = tx.Exec(ctx, `UPDATE registrations SET status='paid', paid_at=NOW() WHERE id=$1`, *regID); err != nil {
			return fmt.Errorf("mark reg paid: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// MarkRegistrationPaid 標記報名已付，並連動其對應 order（交易）
func (r *Repository) MarkRegistrationPaid(ctx context.Context, regID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `UPDATE registrations SET status='paid', paid_at=NOW() WHERE id=$1`, regID)
	if err != nil {
		return fmt.Errorf("mark reg paid: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrRegistrationNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='paid', paid_at=NOW() WHERE registration_id=$1 AND status<>'paid'`, regID); err != nil {
		return fmt.Errorf("mark order paid: %w", err)
	}
	return tx.Commit(ctx)
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
		&race.Status, &race.EventMode, &race.GoalType,
		&dist32, &race.GroupType, &race.GroupMode,
		&race.SlotsTotal, &race.EntryFee, &race.RegStart, &race.RegEnd,
		&race.StartDate, &race.EndDate, &cfgBytes, &race.RequiredFields,
		&race.ControlStatus, &race.StartingSoonDays, &race.BrochureTitle,
		&race.AllowTeamGroups,
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
		&race.Status, &race.EventMode, &race.GoalType,
		&dist32, &race.GroupType, &race.GroupMode,
		&race.SlotsTotal, &race.EntryFee, &race.RegStart, &race.RegEnd,
		&race.StartDate, &race.EndDate, &cfgBytes, &race.RequiredFields,
		&race.ControlStatus, &race.StartingSoonDays, &race.BrochureTitle,
		&race.AllowTeamGroups,
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
