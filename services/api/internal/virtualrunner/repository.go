package virtualrunner

import (
	"context"
	"errors"
	"fmt"
	"math/rand"

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

// Pool 匯出底層連線池，供 titles.go SyncAllEnabledTitles 之類需要直接操作 db（跨表、非
// Repository 既有 CRUD 方法涵蓋的查詢）的套件內函式使用，不必為每個新查詢都在 Repository 上
// 新增一個一次性方法。
func (r *Repository) Pool() *pgxpool.Pool { return r.db }

// isUniqueViolation 判斷是否為 Postgres 唯一鍵衝突（SQLSTATE 23505），比照 internal/race 慣例。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- 等級範本 ---

const presetSelectCols = `level, label, sort_order, avg_km, monthly_km, pace_fast_s, pace_slow_s`

func scanPreset(row pgx.Row) (*LevelPreset, error) {
	p := &LevelPreset{}
	if err := row.Scan(&p.Level, &p.Label, &p.SortOrder, &p.AvgKm, &p.MonthlyKm, &p.PaceFastS, &p.PaceSlowS); err != nil {
		return nil, err
	}
	return p, nil
}

// ListPresets 全部 8 級，依 sort_order 排序（前台下拉選單順序）。
func (r *Repository) ListPresets(ctx context.Context) ([]*LevelPreset, error) {
	rows, err := r.db.Query(ctx, `SELECT `+presetSelectCols+` FROM vr_level_presets ORDER BY sort_order`)
	if err != nil {
		return nil, fmt.Errorf("list presets: %w", err)
	}
	defer rows.Close()
	out := []*LevelPreset{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPreset 單一等級範本；level 不存在回 ErrPresetNotFound。
func (r *Repository) GetPreset(ctx context.Context, level string) (*LevelPreset, error) {
	row := r.db.QueryRow(ctx, `SELECT `+presetSelectCols+` FROM vr_level_presets WHERE level=$1`, level)
	p, err := scanPreset(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPresetNotFound
	}
	return p, err
}

// UpdatePreset 調整某等級的參數（不回溯已建立的選手，見 migration 146 註解）。
func (r *Repository) UpdatePreset(ctx context.Context, level string, avgKm, monthlyKm float64, paceFastS, paceSlowS int) (*LevelPreset, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE vr_level_presets SET avg_km=$2, monthly_km=$3, pace_fast_s=$4, pace_slow_s=$5
		WHERE level=$1
		RETURNING `+presetSelectCols,
		level, avgKm, monthlyKm, paceFastS, paceSlowS)
	p, err := scanPreset(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPresetNotFound
	}
	return p, err
}

// --- 虛擬選手 CRUD ---

// runnerSelectCols 供 ListRunners/GetRunner 共用；race_count 只算非 cancelled 報名（比照
// AdminDelete 的 has_registrations 判斷口徑一致）。
const runnerSelectCols = `
	u.id, u.name, COALESCE(u.avatar_url,''), COALESCE(p.gender,''), COALESCE(vr.city,''), vr.level, vr.diligence,
	COALESCE(vr.window_hour,0), vr.avg_km, vr.monthly_km, vr.pace_fast_s, vr.pace_slow_s,
	vr.enabled, vr.last_generated_at,
	(SELECT COUNT(*) FROM registrations reg WHERE reg.user_id = u.id AND reg.status <> 'cancelled')`

const runnerFrom = `
	FROM virtual_runners vr
	JOIN users u ON u.id = vr.user_id
	LEFT JOIN user_profiles p ON p.user_id = u.id`

func scanRunner(row pgx.Row) (*Runner, error) {
	rn := &Runner{}
	if err := row.Scan(&rn.UserID, &rn.Name, &rn.AvatarURL, &rn.Gender, &rn.City, &rn.Level, &rn.Diligence,
		&rn.WindowHour, &rn.AvgKm, &rn.MonthlyKm, &rn.PaceFastS, &rn.PaceSlowS,
		&rn.Enabled, &rn.LastGeneratedAt, &rn.RaceCount); err != nil {
		return nil, err
	}
	return rn, nil
}

// ListRunners 後台列表：全部虛擬選手，新建立的排前面。
func (r *Repository) ListRunners(ctx context.Context) ([]*Runner, error) {
	rows, err := r.db.Query(ctx, `SELECT `+runnerSelectCols+runnerFrom+` ORDER BY u.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list virtual runners: %w", err)
	}
	defer rows.Close()
	out := []*Runner{}
	for rows.Next() {
		rn, err := scanRunner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rn)
	}
	return out, rows.Err()
}

// GetRunner 單筆（供 Create/Update 後重新讀出完整列）；user_id 非虛擬選手回 ErrRunnerNotFound。
func (r *Repository) GetRunner(ctx context.Context, userID string) (*Runner, error) {
	row := r.db.QueryRow(ctx, `SELECT `+runnerSelectCols+runnerFrom+` WHERE vr.user_id=$1`, userID)
	rn, err := scanRunner(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRunnerNotFound
	}
	return rn, err
}

const tokenChars = "abcdefghijklmnopqrstuvwxyz0123456789"

// randToken 產生 n 碼隨機英數字串，供虛擬帳號 email/handle 湊唯一值；非安全敏感用途
// （撞號機率低、撞了就重試，見 CreateRunner），比照 internal/auth genHandle 的 randSuffix 手法，
// 用套件級 math/rand（Go 1.20+ 預設已隨機種子、且並行安全），不需要 crypto/rand。
func randToken(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = tokenChars[rand.Intn(len(tokenChars))]
	}
	return string(b)
}

// CreateRunner 建立一位虛擬選手：users(is_virtual=TRUE，不建 user_identities＝天然無法登入) +
// user_profiles(nickname=real_name=綽號、gender) + virtual_runners(能力值)。in.Name 是
// namepool.go RandomNickname 產生的跑者綽號（非真人姓名）；real_name 也填同一值——虛擬帳號
// 沒有「真實姓名」的需求，這欄只是跟著 schema 補值，實際顯示用的是 nickname。
// email/handle 用隨機字尾湊唯一值，撞號（UNIQUE violation）就換一組重試，最多 6 次。
func (r *Repository) CreateRunner(ctx context.Context, in CreateRunnerInput) (*Runner, error) {
	const maxAttempts = 6
	var userID string
	for attempt := 0; attempt < maxAttempts; attempt++ {
		suffix := randToken(10)
		email := "virtual+" + suffix + "@dor.internal"
		handle := "vr_" + suffix

		tx, err := r.db.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin tx: %w", err)
		}

		err = tx.QueryRow(ctx, `
			INSERT INTO users (email, handle, name, password_hash, is_virtual)
			VALUES ($1,$2,$3,NULL,TRUE) RETURNING id`,
			email, handle, in.Name).Scan(&userID)
		if isUniqueViolation(err) {
			tx.Rollback(ctx)
			continue // email/handle 撞號，換一組隨機字尾重試
		}
		if err != nil {
			tx.Rollback(ctx)
			return nil, fmt.Errorf("insert virtual user: %w", err)
		}

		if _, err = tx.Exec(ctx, `
			INSERT INTO user_profiles (user_id, real_name, nickname, gender)
			VALUES ($1,$2,$2,$3)`, userID, in.Name, in.Gender); err != nil {
			tx.Rollback(ctx)
			return nil, fmt.Errorf("insert profile: %w", err)
		}

		if _, err = tx.Exec(ctx, `
			INSERT INTO virtual_runners
				(user_id, level, diligence, city, window_hour, avg_km, monthly_km, pace_fast_s, pace_slow_s)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			userID, in.Level, in.Diligence, in.City, in.WindowHour,
			in.Ability.AvgKm, in.Ability.MonthlyKm, in.Ability.PaceFastS, in.Ability.PaceSlowS); err != nil {
			tx.Rollback(ctx)
			return nil, fmt.Errorf("insert virtual_runners: %w", err)
		}

		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return r.GetRunner(ctx, userID)
	}
	return nil, fmt.Errorf("failed to allocate unique virtual account after %d attempts", maxAttempts)
}

// UpdateRunner 局部更新（nil 欄位不變）。level/gender 若給值一併更新 user_profiles.gender；name 若給值＝改名，同步 users.name 與 user_profiles.nickname/real_name。
// 「level 變更時能力值重新帶入抖動」的決策在呼叫端（admin.go）做——由那裡先算好新的
// Ability 再一起傳進來，本函式只單純套用 COALESCE 局部覆寫，不含業務判斷。
func (r *Repository) UpdateRunner(ctx context.Context, userID string, in UpdateRunnerInput) (*Runner, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE virtual_runners SET
			level       = COALESCE($2, level),
			diligence   = COALESCE($3, diligence),
			city        = COALESCE($4, city),
			window_hour = COALESCE($5, window_hour),
			avg_km      = COALESCE($6, avg_km),
			monthly_km  = COALESCE($7, monthly_km),
			pace_fast_s = COALESCE($8, pace_fast_s),
			pace_slow_s = COALESCE($9, pace_slow_s),
			enabled     = COALESCE($10, enabled),
			updated_at  = NOW()
		WHERE user_id = $1`,
		userID, in.Level, in.Diligence, in.City, in.WindowHour,
		in.AvgKm, in.MonthlyKm, in.PaceFastS, in.PaceSlowS, in.Enabled)
	if err != nil {
		return nil, fmt.Errorf("update virtual_runners: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrRunnerNotFound
	}

	if in.Gender != nil {
		if _, err = tx.Exec(ctx, `
			UPDATE user_profiles SET gender=$2, updated_at=NOW() WHERE user_id=$1`, userID, *in.Gender); err != nil {
			return nil, fmt.Errorf("update profile gender: %w", err)
		}
	}

	if in.Name != nil {
		// 改名同步兩處（與 RegenerateAllNames 同口徑）：users.name 是玩家可見顯示名的來源
		//（全站 COALESCE(u.name, handle)），user_profiles.nickname/real_name 跟著寫齊。
		// is_virtual=TRUE 防呆：本路徑只准動虛擬帳號，永遠不得改到真人。
		if _, err = tx.Exec(ctx, `
			UPDATE users SET name=$2 WHERE id=$1 AND is_virtual=TRUE`, userID, *in.Name); err != nil {
			return nil, fmt.Errorf("update user name: %w", err)
		}
		if _, err = tx.Exec(ctx, `
			UPDATE user_profiles SET real_name=$2, nickname=$2, updated_at=NOW() WHERE user_id=$1`, userID, *in.Name); err != nil {
			return nil, fmt.Errorf("update profile name: %w", err)
		}
	}

	if in.AvatarURL != nil {
		// 頭像與玩家自改走同一欄位 users.avatar_url（NULLIF：空字串＝清除，前台回退字首圓）。
		// is_virtual=TRUE 防呆同改名。
		if _, err = tx.Exec(ctx, `
			UPDATE users SET avatar_url=NULLIF($2,''), updated_at=NOW() WHERE id=$1 AND is_virtual=TRUE`, userID, *in.AvatarURL); err != nil {
			return nil, fmt.Errorf("update user avatar: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return r.GetRunner(ctx, userID)
}

// DeleteRunner 硬刪虛擬選手。仍有非 cancelled 報名回 ErrHasRegistrations（409，呼叫端不得刪）。
//
// registrations.user_id / orders.user_id 對 users 皆是普通 REFERENCES（無 ON DELETE CASCADE，
// 見 001_init.sql / 003_events.sql），因此就算已確認「沒有非 cancelled 報名」，這位選手名下如果
// 還留著「已取消」的舊報名/訂單列，直接 DELETE users 仍會被 FK 擋下——必須在同一交易內先手動清掉
// 這些 cancelled 的歷史列（虛擬選手的報名/訂單本來就是本套件自己造的 0 元資料，沒有真實金流/
// 財務紀錄价值，可以安全清除），order_items 則靠 ON DELETE CASCADE 隨 orders 一併清除。
// virtual_runners / user_profiles 靠 ON DELETE CASCADE 隨 users 一併清除
// （見 migration 146 / 003_events.sql）。
func (r *Repository) DeleteRunner(ctx context.Context, userID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var isVirtual bool
	err = tx.QueryRow(ctx, `SELECT is_virtual FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&isVirtual)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRunnerNotFound
	}
	if err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	if !isVirtual {
		return ErrRunnerNotFound
	}

	var hasActive bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM registrations WHERE user_id=$1 AND status<>'cancelled')`, userID).
		Scan(&hasActive); err != nil {
		return fmt.Errorf("check registrations: %w", err)
	}
	if hasActive {
		return ErrHasRegistrations
	}

	if _, err = tx.Exec(ctx, `DELETE FROM orders WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("delete orders: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM registrations WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("delete registrations: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM users WHERE id=$1 AND is_virtual=TRUE`, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return tx.Commit(ctx)
}

// --- 綽號批次重新產生 ---

// AllRunnerUserIDs 全部虛擬選手 user_id（不論 enabled），供 POST /regenerate-names 端點使用
// ——「全部重新取名」刻意涵蓋停用中的選手，維運端要的是整批洗掉舊綽號，不是只洗還在用的。
func (r *Repository) AllRunnerUserIDs(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id FROM virtual_runners`)
	if err != nil {
		return nil, fmt.Errorf("list all virtual runner ids: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RegenerateAllNames 在單一交易內把 names（user_id -> 新綽號）逐筆寫回 users.name /
// user_profiles.real_name+nickname；整批要嘛全部成功要嘛全部不變，避免半途失敗留下部分
// 選手改名、部分沒改的不一致狀態。呼叫端（admin.go RegenerateNames）負責保證 names 內的值
// 彼此不重複（RandomNickname 本身的黏接防呆之外，另外做同批次去重）。
func (r *Repository) RegenerateAllNames(ctx context.Context, names map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for userID, name := range names {
		if _, err := tx.Exec(ctx, `UPDATE users SET name=$2 WHERE id=$1 AND is_virtual=TRUE`, userID, name); err != nil {
			return fmt.Errorf("update user name: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE user_profiles SET real_name=$2, nickname=$2, updated_at=NOW() WHERE user_id=$1`,
			userID, name); err != nil {
			return fmt.Errorf("update profile name: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// --- 賽事名額指派 ---

// RaceExists 供 admin.go 在 RaceStatus/Assign 前擋 404，避免對不存在的賽事回一堆空陣列誤導後台。
func (r *Repository) RaceExists(ctx context.Context, raceID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM races WHERE id=$1)`, raceID).Scan(&ok)
	return ok, err
}

// GroupExists 供 admin.go 在 Assign 指定 group_id 時，一次性驗證該組屬於這場賽事（比每位使用者
// 各鎖一次更省事——lock 的正確性仍由 AssignUser 交易內的 FOR UPDATE 保證，這裡只是前置防呆）。
func (r *Repository) GroupExists(ctx context.Context, raceID, groupID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM race_groups WHERE id=$1 AND race_id=$2)`, groupID, raceID).Scan(&ok)
	return ok, err
}

// RaceGroups GET /race/{raceID} 的 groups[]。
func (r *Repository) RaceGroups(ctx context.Context, raceID string) ([]*GroupSlot, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, slot_limit, slots_taken FROM race_groups
		WHERE race_id=$1 ORDER BY display_order, created_at`, raceID)
	if err != nil {
		return nil, fmt.Errorf("list race groups: %w", err)
	}
	defer rows.Close()
	out := []*GroupSlot{}
	for rows.Next() {
		g := &GroupSlot{}
		if err := rows.Scan(&g.ID, &g.Name, &g.SlotLimit, &g.SlotsTaken); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AssignedRunners GET /race/{raceID} 的 assigned[]：這場賽事目前所有非 cancelled 報名的虛擬選手。
func (r *Repository) AssignedRunners(ctx context.Context, raceID string) ([]*AssignedRunner, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.name, COALESCE(p.gender,''), vr.level, rg.id, rg.name, r.status
		FROM registrations r
		JOIN virtual_runners vr ON vr.user_id = r.user_id
		JOIN users u ON u.id = r.user_id
		LEFT JOIN user_profiles p ON p.user_id = u.id
		JOIN race_groups rg ON rg.id = r.group_id
		WHERE r.race_id = $1 AND r.status <> 'cancelled'
		ORDER BY u.name`, raceID)
	if err != nil {
		return nil, fmt.Errorf("list assigned virtual runners: %w", err)
	}
	defer rows.Close()
	out := []*AssignedRunner{}
	for rows.Next() {
		a := &AssignedRunner{}
		if err := rows.Scan(&a.UserID, &a.Name, &a.Gender, &a.Level, &a.GroupID, &a.GroupName, &a.RegStatus); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CandidatesCount 尚可被指派到這場賽事的虛擬選手數：enabled 且尚未在此賽事有非 cancelled 報名。
// 與 RandomCandidateIDs 的候選池定義口徑一致。
func (r *Repository) CandidatesCount(ctx context.Context, raceID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM virtual_runners vr
		WHERE vr.enabled AND NOT EXISTS (
			SELECT 1 FROM registrations r WHERE r.user_id = vr.user_id AND r.race_id = $1 AND r.status <> 'cancelled'
		)`, raceID).Scan(&n)
	return n, err
}

// RandomCandidateIDs 隨機抽 n 位候選虛擬選手 user_id（enabled 且尚未在此賽事有非 cancelled 報名），
// 供 POST /race/{raceID}/assign 的 random_count 模式使用；不足 n 位就回實際能抽到的數量。
func (r *Repository) RandomCandidateIDs(ctx context.Context, raceID string, n int) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT vr.user_id FROM virtual_runners vr
		WHERE vr.enabled AND NOT EXISTS (
			SELECT 1 FROM registrations r WHERE r.user_id = vr.user_id AND r.race_id = $1 AND r.status <> 'cancelled'
		)
		ORDER BY random() LIMIT $2`, raceID, n)
	if err != nil {
		return nil, fmt.Errorf("random candidates: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// pickRandomGroup 由候選分組中隨機挑一個「尚有名額」的組（slot_limit=nil 視為不限）；全滿
// （無合格候選，含分組為空的情況）回 ok=false。純函式：呼叫端傳入已上鎖讀出的分組快照與 rng，
// 供單元測試注入固定種子驗證挑選只落在有名額的候選、以及全滿時正確回報 false。
func pickRandomGroup(groups []GroupSlot, rng *rand.Rand) (groupID string, ok bool) {
	var eligible []string
	for _, g := range groups {
		if g.HasCapacity() {
			eligible = append(eligible, g.ID)
		}
	}
	if len(eligible) == 0 {
		return "", false
	}
	return eligible[rng.Intn(len(eligible))], true
}

// AssignUser 把一位虛擬選手加入賽事（交易內完成鎖名額+寫報名+寫 0 元訂單，比照
// internal/race Repository.RegisterWithOrder 的「鎖分組→查名額→寫 registration→寫 order」骨架，
// 但拿掉加購/發票/序號/優惠券等前台輸入——虛擬選手報名恆 0 元、恆 paid，不需要那些機制。
// groupID 空字串＝在有名額的組中隨機挑一（見 pickRandomGroup）；非空＝鎖定指定組。
// 回傳非空 AssignSkipReason 代表「這位使用者被跳過」（非錯誤，呼叫端收進 skipped[]）；
// error 非 nil 才是真正的系統錯誤（500）。
func (r *Repository) AssignUser(ctx context.Context, raceID, userID, groupID string, rng *rand.Rand) (AssignSkipReason, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. 確認是啟用中的虛擬選手（FOR UPDATE：避免同一使用者被兩個併發 assign 請求同時通過檢查）
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM virtual_runners WHERE user_id=$1 FOR UPDATE`, userID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return SkipNotFound, nil
	}
	if err != nil {
		return "", fmt.Errorf("lock virtual runner: %w", err)
	}
	if !enabled {
		return SkipDisabled, nil
	}

	// 2. 重複報名預檢（uq_registrations_active_user_race 於 insert 時仍是最終防線，見下方）
	var dup bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM registrations WHERE user_id=$1 AND race_id=$2 AND status<>'cancelled')`,
		userID, raceID).Scan(&dup); err != nil {
		return "", fmt.Errorf("check duplicate: %w", err)
	}
	if dup {
		return SkipDuplicate, nil
	}

	// 3. 選組 + 鎖名額
	chosenGroupID := groupID
	var targetKm *float64
	if groupID != "" {
		var slotLimit *int
		var slotsTaken int
		err = tx.QueryRow(ctx, `
			SELECT slot_limit, slots_taken, target_distance_km FROM race_groups
			WHERE id=$1 AND race_id=$2 FOR UPDATE`, groupID, raceID).Scan(&slotLimit, &slotsTaken, &targetKm)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrGroupNotFound
		}
		if err != nil {
			return "", fmt.Errorf("lock group: %w", err)
		}
		if slotLimit != nil && slotsTaken >= *slotLimit {
			return SkipGroupFull, nil
		}
	} else {
		rows, err := tx.Query(ctx, `
			SELECT id, slot_limit, slots_taken, target_distance_km FROM race_groups
			WHERE race_id=$1 FOR UPDATE`, raceID)
		if err != nil {
			return "", fmt.Errorf("lock groups: %w", err)
		}
		type row struct {
			slot   GroupSlot
			target *float64
		}
		var candidates []row
		for rows.Next() {
			var rr row
			if err := rows.Scan(&rr.slot.ID, &rr.slot.SlotLimit, &rr.slot.SlotsTaken, &rr.target); err != nil {
				rows.Close()
				return "", err
			}
			candidates = append(candidates, rr)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
		slots := make([]GroupSlot, len(candidates))
		for i, c := range candidates {
			slots[i] = c.slot
		}
		picked, ok := pickRandomGroup(slots, rng)
		if !ok {
			return SkipGroupFull, nil
		}
		chosenGroupID = picked
		for _, c := range candidates {
			if c.slot.ID == picked {
				targetKm = c.target
				break
			}
		}
	}

	if _, err = tx.Exec(ctx, `UPDATE race_groups SET slots_taken = slots_taken + 1 WHERE id=$1`, chosenGroupID); err != nil {
		return "", fmt.Errorf("bump slots: %w", err)
	}

	distance := 0
	if targetKm != nil {
		distance = int(*targetKm)
	}

	// 4. 報名（恆 paid、恆 0 元）＋ 0 元訂單。group_revealed=TRUE：這是後台明確指派的分組
	// （不是 faction_battle 的隨機平衡分組要賽前保密），直接可見。
	var regID string
	err = tx.QueryRow(ctx, `
		INSERT INTO registrations (user_id, race_id, group_id, distance, status, amount, paid_at, group_revealed)
		VALUES ($1,$2,$3,$4,'paid',0,NOW(),TRUE) RETURNING id`,
		userID, raceID, chosenGroupID, distance).Scan(&regID)
	if isUniqueViolation(err) {
		return SkipDuplicate, nil // 併發下的最後防線；tx 未 commit，前面的 slots_taken+1 隨 defer rollback 一併撤銷
	}
	if err != nil {
		return "", fmt.Errorf("insert registration: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO orders (user_id, race_id, registration_id, total_cents, status, payment_ref, paid_at)
		VALUES ($1,$2,$3,0,'paid','VIRTUAL',NOW())`, userID, raceID, regID); err != nil {
		return "", fmt.Errorf("insert order: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return "", nil
}

// RemoveFromRace 移除該選手在此賽事的報名：registrations/orders → cancelled，race_groups.slots_taken
// -1（不低於 0）。找不到有效報名視為冪等成功（本來就沒有，等同已移除），不回錯誤。
func (r *Repository) RemoveFromRace(ctx context.Context, raceID, userID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var regID, groupID string
	err = tx.QueryRow(ctx, `
		SELECT id, group_id FROM registrations
		WHERE user_id=$1 AND race_id=$2 AND status<>'cancelled' FOR UPDATE`,
		userID, raceID).Scan(&regID, &groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock registration: %w", err)
	}

	if _, err = tx.Exec(ctx, `UPDATE registrations SET status='cancelled' WHERE id=$1`, regID); err != nil {
		return fmt.Errorf("cancel registration: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='cancelled' WHERE registration_id=$1`, regID); err != nil {
		return fmt.Errorf("cancel order: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE race_groups SET slots_taken = GREATEST(slots_taken - 1, 0) WHERE id=$1`, groupID); err != nil {
		return fmt.Errorf("decrement slots: %w", err)
	}
	return tx.Commit(ctx)
}
