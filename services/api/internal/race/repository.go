package race

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	       status, event_mode, goal_type, distances, group_type, group_mode,
	       slots_total, entry_fee, registration_start, registration_end,
	       start_date, end_date, config, required_fields,
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

	var raceID string
	err = tx.QueryRow(ctx, `
		INSERT INTO races (slug, title, subtitle, world, blurb, hero_image_url,
		                   status, event_mode, goal_type, distances, group_type, group_mode,
		                   slots_total, entry_fee, registration_start, registration_end,
		                   start_date, end_date, config, created_by, review_status, required_fields)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
		RETURNING id`,
		race.Slug, race.Title, race.Subtitle, race.World, race.Blurb, race.HeroImageURL,
		race.Status, race.EventMode, race.GoalType, dist32, race.GroupType, race.GroupMode,
		race.SlotsTotal, race.EntryFee, race.RegStart, race.RegEnd,
		race.StartDate, race.EndDate, cfgBytes, createdBy, reviewStatus, requiredFields,
	).Scan(&raceID)
	if err != nil {
		return nil, fmt.Errorf("insert race: %w", err)
	}

	// 分組（記錄索引 → 實際 UUID，供物資對應）
	groupIDByIndex := make([]string, len(req.Groups))
	for i := range req.Groups {
		g := &req.Groups[i]
		var gid string
		err = tx.QueryRow(ctx, `
			INSERT INTO race_groups (race_id, name, description, display_order,
			                         slot_limit, gender_limit, age_min, age_max, target_distance_km)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			RETURNING id`,
			raceID, g.Name, nullStr(g.Description), g.DisplayOrder,
			g.SlotLimit, defaultStr(g.GenderLimit, "any"), g.AgeMin, g.AgeMax, g.TargetDistanceKm,
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

	// 1. 更新賽事本體
	_, err = tx.Exec(ctx, `
		UPDATE races SET
			slug=$1, title=$2, subtitle=$3, world=$4, blurb=$5, hero_image_url=$6,
			status=$7, distances=$8, group_type=$9, group_mode=$10,
			slots_total=$11, entry_fee=$12, start_date=$13, end_date=$14, config=$15,
			event_mode=$16, goal_type=$17, registration_start=$18, registration_end=$19,
			required_fields=$20, updated_at=NOW()
		WHERE id=$21`,
		race.Slug, race.Title, race.Subtitle, race.World, race.Blurb, race.HeroImageURL,
		race.Status, dist32, race.GroupType, race.GroupMode,
		race.SlotsTotal, race.EntryFee, race.StartDate, race.EndDate, cfgBytes,
		race.EventMode, race.GoalType, race.RegStart, race.RegEnd,
		requiredFields, raceID,
	)
	if err != nil {
		return nil, fmt.Errorf("update race: %w", err)
	}

	// 2. 同步分組（upsert by id，記錄最終 id 供物資對應），刪除已移除的
	finalGroupIDs := make([]string, len(req.Groups))
	keptGroups := make([]string, 0, len(req.Groups))
	for i := range req.Groups {
		g := &req.Groups[i]
		if g.ID != "" {
			_, err = tx.Exec(ctx, `
				UPDATE race_groups SET name=$1, description=$2, display_order=$3,
				    slot_limit=$4, gender_limit=$5, age_min=$6, age_max=$7, target_distance_km=$8
				WHERE id=$9 AND race_id=$10`,
				g.Name, nullStr(g.Description), g.DisplayOrder,
				g.SlotLimit, defaultStr(g.GenderLimit, "any"), g.AgeMin, g.AgeMax, g.TargetDistanceKm,
				g.ID, raceID,
			)
			if err != nil {
				return nil, fmt.Errorf("update group %d: %w", i, err)
			}
			finalGroupIDs[i] = g.ID
		} else {
			var gid string
			err = tx.QueryRow(ctx, `
				INSERT INTO race_groups (race_id, name, description, display_order,
				                         slot_limit, gender_limit, age_min, age_max, target_distance_km)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
				raceID, g.Name, nullStr(g.Description), g.DisplayOrder,
				g.SlotLimit, defaultStr(g.GenderLimit, "any"), g.AgeMin, g.AgeMax, g.TargetDistanceKm,
			).Scan(&gid)
			if err != nil {
				return nil, fmt.Errorf("insert group %d: %w", i, err)
			}
			finalGroupIDs[i] = gid
		}
		keptGroups = append(keptGroups, finalGroupIDs[i])
	}
	// 刪除 payload 中不存在的分組（若該分組已有報名，FK RESTRICT 會讓交易失敗 → 正確阻擋）
	if _, err = tx.Exec(ctx,
		`DELETE FROM race_groups WHERE race_id=$1 AND NOT (id = ANY($2::uuid[]))`,
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
	return &RaceDetail{Race: *race, Groups: groups, Addons: addons, Supplies: supplies}, nil
}

// GetGroups 取得賽事的所有分組
func (r *Repository) GetGroups(ctx context.Context, raceID string) ([]RaceGroup, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, race_id, name, COALESCE(description,''), display_order,
		       slot_limit, slots_taken, gender_limit, age_min, age_max, target_distance_km
		FROM race_groups WHERE race_id=$1 ORDER BY display_order, created_at`, raceID)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	groups := []RaceGroup{}
	for rows.Next() {
		var g RaceGroup
		if err := rows.Scan(&g.ID, &g.RaceID, &g.Name, &g.Description, &g.DisplayOrder,
			&g.SlotLimit, &g.SlotsTaken, &g.GenderLimit, &g.AgeMin, &g.AgeMax, &g.TargetDistanceKm); err != nil {
			return nil, err
		}
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
	EntryFee      int
	GroupRevealed bool
	Distance      int
	Addons        []AddonSelection
	Participant   ParticipantInfo
}

// RegisterWithOrder 在單一交易內完成報名：分組名額 row-lock 防超賣、加購庫存、
// 寫 registration + order/order_items、個資回填（只補空欄位）。
func (r *Repository) RegisterWithOrder(ctx context.Context, in RegisterTxInput) (*RegisterResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. 鎖分組、檢查名額
	var slotLimit *int
	var slotsTaken int
	var groupName string
	err = tx.QueryRow(ctx, `
		SELECT slot_limit, slots_taken, name FROM race_groups
		WHERE id=$1 AND race_id=$2 FOR UPDATE`,
		in.GroupID, in.RaceID).Scan(&slotLimit, &slotsTaken, &groupName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock group: %w", err)
	}
	if slotLimit != nil && slotsTaken >= *slotLimit {
		return nil, ErrGroupFull
	}
	if _, err = tx.Exec(ctx, `UPDATE race_groups SET slots_taken = slots_taken + 1 WHERE id=$1`, in.GroupID); err != nil {
		return nil, fmt.Errorf("bump slots: %w", err)
	}

	// 2. 加購：鎖庫存、檢查、扣量、累計金額
	total := in.EntryFee
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
		total += price * a.Qty
		items = append(items, lineItem{a.AddonID, a.Qty, price})
	}

	// 3. registration（UNIQUE(user_id,race_id) 衝突 → 已報名）
	reg := &Registration{
		UserID: in.UserID, RaceID: in.RaceID, GroupID: in.GroupID,
		Distance: in.Distance, GroupRevealed: in.GroupRevealed,
		Status: "pending", Amount: in.EntryFee,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO registrations
			(user_id, race_id, group_id, distance, status, amount,
			 group_revealed, snap_real_name, snap_phone, snap_address)
		VALUES ($1,$2,$3,$4,'pending',$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''))
		RETURNING id`,
		in.UserID, in.RaceID, in.GroupID, in.Distance, in.EntryFee, in.GroupRevealed,
		in.Participant.RealName, in.Participant.Phone, in.Participant.Address,
	).Scan(&reg.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyRegistered
		}
		return nil, fmt.Errorf("insert registration: %w", err)
	}

	// 4. order + order_items
	order := &Order{TotalCents: total, Status: "pending"}
	if err = tx.QueryRow(ctx, `
		INSERT INTO orders (user_id, race_id, registration_id, total_cents, status)
		VALUES ($1,$2,$3,$4,'pending') RETURNING id`,
		in.UserID, in.RaceID, reg.ID, total).Scan(&order.ID); err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}
	if in.EntryFee > 0 {
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_items (order_id, item_type, qty, unit_price_cents, subtotal_cents)
			VALUES ($1,'entry',1,$2,$2)`, order.ID, in.EntryFee); err != nil {
			return nil, fmt.Errorf("insert entry item: %w", err)
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
