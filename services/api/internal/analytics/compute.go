package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// --- 唯讀查詢共用小工具（本檔内部使用） ---

// queryDateCountMap 執行「SELECT 'YYYY-MM-DD' 字串, COUNT(*) ... GROUP BY 1」形狀的查詢，回傳
// date→count map（供 mergeDateCounts 補齊成連續序列）。
func queryDateCountMap(ctx context.Context, db *pgxpool.Pool, sql string, args ...any) (map[string]int, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		out[d] = n
	}
	return out, rows.Err()
}

// queryDateKmMap 同 queryDateCountMap，值為 SUM(distance_km) 浮點數。
func queryDateKmMap(ctx context.Context, db *pgxpool.Pool, sql string, args ...any) (map[string]float64, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var d string
		var km float64
		if err := rows.Scan(&d, &km); err != nil {
			return nil, err
		}
		out[d] = km
	}
	return out, rows.Err()
}

// queryLevelConfig 撈整份等級門檻表（依 exp_required 升冪，供 levelFromExp 使用；見 bucket.go）。
func queryLevelConfig(ctx context.Context, db *pgxpool.Pool) ([]levelRow, error) {
	rows, err := db.Query(ctx, `SELECT level, exp_required FROM level_config ORDER BY exp_required`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []levelRow{}
	for rows.Next() {
		var l levelRow
		if err := rows.Scan(&l.Level, &l.ExpRequired); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// queryHourArray 執行「SELECT 小時(0-23)::int, COUNT(*) ... GROUP BY 1」形狀的查詢，回傳長度 24 的
// 陣列（index=小時）；查無資料的小時維持 0。
func queryHourArray(ctx context.Context, db *pgxpool.Pool, sql string, args ...any) ([]int, error) {
	out := make([]int, 24)
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var h, n int
		if err := rows.Scan(&h, &n); err != nil {
			return out, err
		}
		if h >= 0 && h < 24 {
			out[h] = n
		}
	}
	return out, rows.Err()
}

// --- 空區塊建構子（單一區塊查詢失敗時的安全預設值，見 BuildReport） ---

func emptyRegistrations() Registrations {
	return Registrations{New30d: []DateCount{}, ByHour: make([]int, 24), BySource: []SourceCount{}}
}

func emptyLogins() Logins {
	return Logins{Dau30d: []DateCount{}, FreqDist30d: []BucketCount{}, ByHour: make([]int, 24)}
}

func emptyMileage() Mileage {
	return Mileage{
		DailyKm30d:        []DateKm{},
		PaceDist:          []BucketCount{},
		MonthlyVolumeDist: []BucketCount{},
		ByGender:          []GroupAvg{},
		ByAge:             []GroupAvg{},
	}
}

func emptyParticipation() Participation {
	return Participation{Reg30d: []DateCount{}, TopRaces: []TitleCount{}, RepeatDist: []BucketCount{}}
}

func emptyCards() Cards {
	return Cards{CollectionDist: []BucketCount{}, TopCards: []NameCount{}}
}

func emptySystems() Systems {
	return Systems{Usage: []SystemUsage{}}
}

func emptyRunners() []RunnerStat {
	return []RunnerStat{}
}

// --- 六大區塊 ---

// buildRegistrations 註冊區塊：累計會員數（現況）＋近 30 日新增（日序列＋時段分佈）＋來源歸因
// （migration 147，累計至今——該表僅自上線起才有資料，早於 147 註冊的會員不計入 by_source，
// 這是資料收集起點的限制，非查詢錯誤）。
func buildRegistrations(ctx context.Context, db *pgxpool.Pool, today time.Time) (Registrations, error) {
	out := emptyRegistrations()

	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE NOT is_virtual`).Scan(&out.TotalMembers); err != nil {
		return out, fmt.Errorf("total_members: %w", err)
	}

	start := taiwanDayBoundaryUTC(today.AddDate(0, 0, -29))

	dateCounts, err := queryDateCountMap(ctx, db, `
		SELECT to_char(created_at AT TIME ZONE 'Asia/Taipei', 'YYYY-MM-DD'), COUNT(*)
		FROM users WHERE NOT is_virtual AND created_at >= $1
		GROUP BY 1`, start)
	if err != nil {
		return out, fmt.Errorf("new_30d: %w", err)
	}
	out.New30d = mergeDateCounts(taiwanDaySeries(today, 30), dateCounts)

	// by_hour 統計窗口與 new_30d 相同（近 30 日新增會員的註冊時段分佈），而非全站歷史累計——
	// 近期資料較能反映「現在」的使用者行為模式，且與其餘 30 日窗口欄位口徑一致，避免同一報告內
	// 出現兩種不同時間窗口混用而難以互相比對。
	hourArr, err := queryHourArray(ctx, db, `
		SELECT EXTRACT(HOUR FROM created_at AT TIME ZONE 'Asia/Taipei')::int, COUNT(*)
		FROM users WHERE NOT is_virtual AND created_at >= $1
		GROUP BY 1`, start)
	if err != nil {
		return out, fmt.Errorf("by_hour: %w", err)
	}
	out.ByHour = hourArr

	rows, err := db.Query(ctx, `
		SELECT usa.source, COUNT(*) FROM user_signup_attribution usa
		JOIN users u ON u.id = usa.user_id AND NOT u.is_virtual
		GROUP BY usa.source ORDER BY COUNT(*) DESC`)
	if err != nil {
		return out, fmt.Errorf("by_source: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sc SourceCount
		if err := rows.Scan(&sc.Source, &sc.Count); err != nil {
			return out, fmt.Errorf("by_source scan: %w", err)
		}
		out.BySource = append(out.BySource, sc)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("by_source rows: %w", err)
	}

	return out, nil
}

// buildLogins 上線區塊。dau_30d/active_7d/active_30d 採「當日有活動上傳（activities，不限
// source——同步進來的 Strava/Garmin 等資料一樣代表真人在用）OR 有登入事件（user_login_logs）」
// 聯集，彌補 user_login_logs 不記 Refresh 的低估（見 model.go Logins 型別註解的完整口徑警語）。
// freq_dist_30d／by_hour 則刻意只統計登入事件本身（這兩者的意義就是「登入行為」的分佈，聯集活動
// 上傳會失去「登入」的原始語意），population 皆為全部非虛擬會員（含 0 次者）。
func buildLogins(ctx context.Context, db *pgxpool.Pool, today time.Time) (Logins, error) {
	out := emptyLogins()

	start30 := taiwanDayBoundaryUTC(today.AddDate(0, 0, -29))
	start7 := taiwanDayBoundaryUTC(today.AddDate(0, 0, -6))

	dauCounts, err := queryDateCountMap(ctx, db, `
		SELECT day, COUNT(DISTINCT user_id) FROM (
			SELECT a.user_id AS user_id, to_char(a.recorded_at AT TIME ZONE 'Asia/Taipei','YYYY-MM-DD') AS day
			FROM activities a WHERE NOT a.flagged AND a.recorded_at >= $1
			UNION
			SELECT l.user_id AS user_id, to_char(l.created_at AT TIME ZONE 'Asia/Taipei','YYYY-MM-DD') AS day
			FROM user_login_logs l WHERE l.created_at >= $1
		) x
		JOIN users u ON u.id = x.user_id AND NOT u.is_virtual
		GROUP BY day`, start30)
	if err != nil {
		return out, fmt.Errorf("dau_30d: %w", err)
	}
	out.Dau30d = mergeDateCounts(taiwanDaySeries(today, 30), dauCounts)

	activeSQL := `
		SELECT COUNT(DISTINCT x.user_id) FROM (
			SELECT a.user_id FROM activities a WHERE NOT a.flagged AND a.recorded_at >= $1
			UNION
			SELECT l.user_id FROM user_login_logs l WHERE l.created_at >= $1
		) x JOIN users u ON u.id = x.user_id AND NOT u.is_virtual`
	if err := db.QueryRow(ctx, activeSQL, start7).Scan(&out.Active7d); err != nil {
		return out, fmt.Errorf("active_7d: %w", err)
	}
	if err := db.QueryRow(ctx, activeSQL, start30).Scan(&out.Active30d); err != nil {
		return out, fmt.Errorf("active_30d: %w", err)
	}

	freqRows, err := db.Query(ctx, `
		SELECT COALESCE(l.n, 0) FROM users u
		LEFT JOIN (
			SELECT user_id, COUNT(*) AS n FROM user_login_logs WHERE created_at >= $1 GROUP BY user_id
		) l ON l.user_id = u.id
		WHERE NOT u.is_virtual`, start30)
	if err != nil {
		return out, fmt.Errorf("freq_dist_30d: %w", err)
	}
	freqCounts := map[string]int{}
	for freqRows.Next() {
		var n int
		if err := freqRows.Scan(&n); err != nil {
			freqRows.Close()
			return out, fmt.Errorf("freq_dist_30d scan: %w", err)
		}
		freqCounts[loginFreqBucket(n)]++
	}
	if err := freqRows.Err(); err != nil {
		freqRows.Close()
		return out, fmt.Errorf("freq_dist_30d rows: %w", err)
	}
	freqRows.Close()
	out.FreqDist30d = bucketList(loginFreqBucketOrder, freqCounts)

	hourArr, err := queryHourArray(ctx, db, `
		SELECT EXTRACT(HOUR FROM l.created_at AT TIME ZONE 'Asia/Taipei')::int, COUNT(*)
		FROM user_login_logs l JOIN users u ON u.id = l.user_id AND NOT u.is_virtual
		WHERE l.created_at >= $1 GROUP BY 1`, start30)
	if err != nil {
		return out, fmt.Errorf("by_hour: %w", err)
	}
	out.ByHour = hourArr

	return out, nil
}

// buildMileage 里程/配速區塊。里程相關查詢一律加 NOT flagged AND source IS NULL（只認 App GPS，
// 比照 activity-data-source-gate 慣例）。by_gender/by_age/monthly_volume_dist 的population是「全部
// 非虛擬會員」（含近 30 日完全沒有活動的人，計入 0 公里／各自的 0 桶），pace_dist 則只看「近 30 日
// 確實有里程」的使用者（沒有距離就沒有配速可言）。
func buildMileage(ctx context.Context, db *pgxpool.Pool, today time.Time) (Mileage, error) {
	out := emptyMileage()
	start := taiwanDayBoundaryUTC(today.AddDate(0, 0, -29))

	kmByDay, err := queryDateKmMap(ctx, db, `
		SELECT to_char(a.recorded_at AT TIME ZONE 'Asia/Taipei','YYYY-MM-DD'), COALESCE(SUM(a.distance_km),0)
		FROM activities a JOIN users u ON u.id = a.user_id AND NOT u.is_virtual
		WHERE NOT a.flagged AND a.source IS NULL AND a.recorded_at >= $1
		GROUP BY 1`, start)
	if err != nil {
		return out, fmt.Errorf("daily_km_30d: %w", err)
	}
	out.DailyKm30d = mergeDateKm(taiwanDaySeries(today, 30), kmByDay)

	popRows, err := db.Query(ctx, `
		SELECT COALESCE(p.gender, ''), p.birthday, COALESCE(usage.km, 0)
		FROM users u
		LEFT JOIN user_profiles p ON p.user_id = u.id
		LEFT JOIN (
			SELECT user_id, SUM(distance_km) AS km FROM activities
			WHERE NOT flagged AND source IS NULL AND recorded_at >= $1
			GROUP BY user_id
		) usage ON usage.user_id = u.id
		WHERE NOT u.is_virtual`, start)
	if err != nil {
		return out, fmt.Errorf("population: %w", err)
	}
	volumeCounts := map[string]int{}
	genderSums := map[string]float64{}
	genderCounts := map[string]int{}
	ageSums := map[string]float64{}
	ageCounts := map[string]int{}
	now := taiwanNow()
	for popRows.Next() {
		var gender string
		var birthday *time.Time
		var km float64
		if err := popRows.Scan(&gender, &birthday, &km); err != nil {
			popRows.Close()
			return out, fmt.Errorf("population scan: %w", err)
		}
		volumeCounts[monthlyVolumeBucket(km)]++
		g := normalizeGender(gender)
		genderSums[g] += km
		genderCounts[g]++
		a := ageBucket(birthday, now)
		ageSums[a] += km
		ageCounts[a]++
	}
	if err := popRows.Err(); err != nil {
		popRows.Close()
		return out, fmt.Errorf("population rows: %w", err)
	}
	popRows.Close()
	out.MonthlyVolumeDist = bucketList(monthlyVolumeBucketOrder, volumeCounts)
	out.ByGender = groupAvgList(genderGroupOrder, genderSums, genderCounts)
	out.ByAge = groupAvgList(ageBucketOrder, ageSums, ageCounts)

	// 距離加權平均配速（總秒數/總公里，而非「各筆配速的平均」，較不受單筆極端值影響）。
	paceRows, err := db.Query(ctx, `
		SELECT SUM(a.duration_s)::float8 / SUM(a.distance_km)
		FROM activities a JOIN users u ON u.id = a.user_id AND NOT u.is_virtual
		WHERE NOT a.flagged AND a.source IS NULL AND a.recorded_at >= $1 AND a.distance_km > 0
		GROUP BY a.user_id
		HAVING SUM(a.distance_km) > 0`, start)
	if err != nil {
		return out, fmt.Errorf("pace_dist: %w", err)
	}
	paceCounts := map[string]int{}
	for paceRows.Next() {
		var avgPace float64
		if err := paceRows.Scan(&avgPace); err != nil {
			paceRows.Close()
			return out, fmt.Errorf("pace_dist scan: %w", err)
		}
		paceCounts[paceBucket(avgPace)]++
	}
	if err := paceRows.Err(); err != nil {
		paceRows.Close()
		return out, fmt.Errorf("pace_dist rows: %w", err)
	}
	paceRows.Close()
	out.PaceDist = bucketList(paceBucketOrder, paceCounts)

	return out, nil
}

// buildParticipation 參與度區塊。「有效報名」統一採 status <> 'cancelled'（見 model.go
// Participation 型別註解）。
func buildParticipation(ctx context.Context, db *pgxpool.Pool, today time.Time) (Participation, error) {
	out := emptyParticipation()
	start := taiwanDayBoundaryUTC(today.AddDate(0, 0, -29))

	regByDay, err := queryDateCountMap(ctx, db, `
		SELECT to_char(r.created_at AT TIME ZONE 'Asia/Taipei','YYYY-MM-DD'), COUNT(*)
		FROM registrations r JOIN users u ON u.id = r.user_id AND NOT u.is_virtual
		WHERE r.status <> 'cancelled' AND r.created_at >= $1
		GROUP BY 1`, start)
	if err != nil {
		return out, fmt.Errorf("reg_30d: %w", err)
	}
	out.Reg30d = mergeDateCounts(taiwanDaySeries(today, 30), regByDay)

	var regUsers, totalUsers int
	if err := db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT r.user_id) FROM registrations r
		JOIN users u ON u.id = r.user_id AND NOT u.is_virtual
		WHERE r.status <> 'cancelled'`).Scan(&regUsers); err != nil {
		return out, fmt.Errorf("ever_registered (reg_users): %w", err)
	}
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE NOT is_virtual`).Scan(&totalUsers); err != nil {
		return out, fmt.Errorf("ever_registered (total_users): %w", err)
	}
	if totalUsers > 0 {
		out.EverRegisteredPct = round2(float64(regUsers) / float64(totalUsers) * 100)
	}

	raceRows, err := db.Query(ctx, `
		SELECT ra.title, COUNT(*) AS n
		FROM registrations r JOIN races ra ON ra.id = r.race_id
		JOIN users u ON u.id = r.user_id AND NOT u.is_virtual
		WHERE r.status <> 'cancelled'
		GROUP BY ra.id, ra.title ORDER BY n DESC LIMIT 10`)
	if err != nil {
		return out, fmt.Errorf("top_races: %w", err)
	}
	for raceRows.Next() {
		var tc TitleCount
		if err := raceRows.Scan(&tc.Title, &tc.Count); err != nil {
			raceRows.Close()
			return out, fmt.Errorf("top_races scan: %w", err)
		}
		out.TopRaces = append(out.TopRaces, tc)
	}
	if err := raceRows.Err(); err != nil {
		raceRows.Close()
		return out, fmt.Errorf("top_races rows: %w", err)
	}
	raceRows.Close()

	repeatRows, err := db.Query(ctx, `
		SELECT COUNT(DISTINCT r.race_id) FILTER (WHERE r.status <> 'cancelled')
		FROM users u LEFT JOIN registrations r ON r.user_id = u.id
		WHERE NOT u.is_virtual
		GROUP BY u.id`)
	if err != nil {
		return out, fmt.Errorf("repeat_dist: %w", err)
	}
	repeatCounts := map[string]int{}
	for repeatRows.Next() {
		var n int
		if err := repeatRows.Scan(&n); err != nil {
			repeatRows.Close()
			return out, fmt.Errorf("repeat_dist scan: %w", err)
		}
		repeatCounts[repeatBucket(n)]++
	}
	if err := repeatRows.Err(); err != nil {
		repeatRows.Close()
		return out, fmt.Errorf("repeat_dist rows: %w", err)
	}
	repeatRows.Close()
	out.RepeatDist = bucketList(repeatBucketOrder, repeatCounts)

	return out, nil
}

// buildCards 卡片收集區塊（城市探索關主卡：explore_bosses/explore_progress，非大富翁知識卡/貼紙）。
func buildCards(ctx context.Context, db *pgxpool.Pool) (Cards, error) {
	out := emptyCards()

	if err := db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT ep.user_id), COUNT(*)
		FROM explore_progress ep JOIN users u ON u.id = ep.user_id AND NOT u.is_virtual
		WHERE ep.card_obtained`).Scan(&out.Collectors, &out.TotalCollected); err != nil {
		return out, fmt.Errorf("collectors/total_collected: %w", err)
	}

	distRows, err := db.Query(ctx, `
		SELECT COUNT(*) FILTER (WHERE ep.card_obtained)
		FROM users u LEFT JOIN explore_progress ep ON ep.user_id = u.id
		WHERE NOT u.is_virtual
		GROUP BY u.id`)
	if err != nil {
		return out, fmt.Errorf("collection_dist: %w", err)
	}
	distCounts := map[string]int{}
	for distRows.Next() {
		var n int
		if err := distRows.Scan(&n); err != nil {
			distRows.Close()
			return out, fmt.Errorf("collection_dist scan: %w", err)
		}
		distCounts[cardBucket(n)]++
	}
	if err := distRows.Err(); err != nil {
		distRows.Close()
		return out, fmt.Errorf("collection_dist rows: %w", err)
	}
	distRows.Close()
	out.CollectionDist = bucketList(cardBucketOrder, distCounts)

	topRows, err := db.Query(ctx, `
		SELECT eb.name, COUNT(*) AS n
		FROM explore_progress ep
		JOIN explore_bosses eb ON eb.id = ep.boss_id
		JOIN users u ON u.id = ep.user_id AND NOT u.is_virtual
		WHERE ep.card_obtained
		GROUP BY eb.id, eb.name ORDER BY n DESC LIMIT 10`)
	if err != nil {
		return out, fmt.Errorf("top_cards: %w", err)
	}
	for topRows.Next() {
		var nc NameCount
		if err := topRows.Scan(&nc.Name, &nc.Count); err != nil {
			topRows.Close()
			return out, fmt.Errorf("top_cards scan: %w", err)
		}
		out.TopCards = append(out.TopCards, nc)
	}
	if err := topRows.Err(); err != nil {
		topRows.Close()
		return out, fmt.Errorf("top_cards rows: %w", err)
	}
	topRows.Close()

	return out, nil
}

// systemUsageDef 單一系統的使用足跡查詢定義（total/last30 各自獨立 SQL，欄位/JOIN 目標各系統不同，
// 無法共用同一份泛用模板——見探查文件表格，各系統各有各的例外，勉強硬套抽象反而更難讀）。
type systemUsageDef struct {
	system    string
	label     string
	totalSQL  string
	last30SQL string
}

// buildSystems 各系統使用足跡區塊：VIP／自主訓練／個人任務／大富翁／稱號／充電站／站內信，共 7
// 個系統。users_total／users_30d 口徑逐系統列於下方 SQL 旁註解（詳細探查依據見任務交接文件）。
func buildSystems(ctx context.Context, db *pgxpool.Pool, today time.Time) (Systems, error) {
	out := emptySystems()
	start30 := taiwanDayBoundaryUTC(today.AddDate(0, 0, -29))

	defs := []systemUsageDef{
		{
			system: "vip",
			label:  "VIP 訂閱（含試用）",
			// 含 14 天試用（試用只寫 users.vip_plan='trial'，不進 vip_subscriptions，見
			// internal/auth/repository.go）；若只想算「付費訂閱」需改查 vip_subscriptions distinct。
			totalSQL: `SELECT COUNT(*) FROM users
				WHERE NOT is_virtual AND (COALESCE(vip_plan,'') <> '' OR vip_expires_at IS NOT NULL)`,
			// 近 30 日「開始使用」：訂閱新建/異動於近 30 日內，或試用於近 30 日內開始（vip_since）。
			last30SQL: `SELECT COUNT(DISTINCT uid) FROM (
				SELECT user_id AS uid FROM vip_subscriptions
				WHERE started_at >= $1 OR updated_at >= $1
				UNION
				SELECT id AS uid FROM users
				WHERE NOT is_virtual AND vip_plan = 'trial' AND vip_since >= $1
			) x`,
		},
		{
			system: "training",
			label:  "自主訓練課表",
			totalSQL: `SELECT COUNT(DISTINCT s.user_id) FROM user_training_schedule s
				JOIN users u ON u.id = s.user_id AND NOT u.is_virtual`,
			// 近 30 日用 created_at（排課動作時間），非 scheduled_date（排定日可能是未來）。
			last30SQL: `SELECT COUNT(DISTINCT s.user_id) FROM user_training_schedule s
				JOIN users u ON u.id = s.user_id AND NOT u.is_virtual
				WHERE s.created_at >= $1`,
		},
		{
			system: "personal_tasks",
			label:  "個人任務（跑者生命週期）",
			totalSQL: `SELECT COUNT(DISTINCT p.user_id) FROM personal_task_progress p
				JOIN users u ON u.id = p.user_id AND NOT u.is_virtual`,
			last30SQL: `SELECT COUNT(DISTINCT p.user_id) FROM personal_task_progress p
				JOIN users u ON u.id = p.user_id AND NOT u.is_virtual
				WHERE p.completed_at >= $1`,
		},
		{
			system: "monopoly",
			label:  "環台大富翁",
			totalSQL: `SELECT COUNT(DISTINCT m.user_id) FROM monopoly_player_state m
				JOIN users u ON u.id = m.user_id AND NOT u.is_virtual`,
			// 近 30 日用實際擲骰行為（monopoly_rolls），比 player_state.updated_at 更精確。
			last30SQL: `SELECT COUNT(DISTINCT r.user_id) FROM monopoly_rolls r
				JOIN users u ON u.id = r.user_id AND NOT u.is_virtual
				WHERE r.created_at >= $1`,
		},
		{
			system: "titles",
			label:  "稱號",
			totalSQL: `SELECT COUNT(DISTINCT t.user_id) FROM user_titles t
				JOIN users u ON u.id = t.user_id AND NOT u.is_virtual`,
			last30SQL: `SELECT COUNT(DISTINCT t.user_id) FROM user_titles t
				JOIN users u ON u.id = t.user_id AND NOT u.is_virtual
				WHERE t.earned_at >= $1`,
		},
		{
			system: "partners",
			label:  "跑者充電站（收藏）",
			totalSQL: `SELECT COUNT(DISTINCT f.user_id) FROM partner_shop_favorites f
				JOIN users u ON u.id = f.user_id AND NOT u.is_virtual`,
			last30SQL: `SELECT COUNT(DISTINCT f.user_id) FROM partner_shop_favorites f
				JOIN users u ON u.id = f.user_id AND NOT u.is_virtual
				WHERE f.created_at >= $1`,
		},
		{
			system: "mail",
			label:  "站內信（已讀互動）",
			// read_at IS NOT NULL 才算「真的用了這功能」（created_at 是系統發信時間，非玩家行為）。
			totalSQL: `SELECT COUNT(DISTINCT m.user_id) FROM user_mail m
				JOIN users u ON u.id = m.user_id AND NOT u.is_virtual
				WHERE m.read_at IS NOT NULL`,
			last30SQL: `SELECT COUNT(DISTINCT m.user_id) FROM user_mail m
				JOIN users u ON u.id = m.user_id AND NOT u.is_virtual
				WHERE m.read_at >= $1`,
		},
	}

	for _, d := range defs {
		var total, last30 int
		if err := db.QueryRow(ctx, d.totalSQL).Scan(&total); err != nil {
			return out, fmt.Errorf("systems.%s total: %w", d.system, err)
		}
		if err := db.QueryRow(ctx, d.last30SQL, start30).Scan(&last30); err != nil {
			return out, fmt.Errorf("systems.%s last30: %w", d.system, err)
		}
		out.Usage = append(out.Usage, SystemUsage{System: d.system, Label: d.label, Users30d: last30, UsersTotal: total})
	}

	return out, nil
}

// buildRunners 第七區塊「跑步數據分析排行」：對 activities（WHERE NOT flagged）JOIN users 逐人
// 彙總累積里程/時間/配速/筆數/每週跑步天數。刻意只加 NOT flagged、不像 buildMileage 那樣另外限制
// source IS NULL——flagged 本身已涵蓋跨來源去重（見 strava-dedup 系列邏輯：同一筆活動只有其中一個
// 來源會是「未 flagged 的主紀錄」），故這裡全來源（App GPS + Strava/Garmin 等同步）計入，反映「這
// 位跑者總共跑了多少」，用途與 buildMileage「只認 App GPS 的官方排名口徑」不同。含虛擬選手（含
// IsVirtual 欄位，是否隱藏交給前端 client-side 過濾，因為資料量小、200 列以內全部一次回傳更省一次
// API 往返），依 total_km DESC 取前 200 名；0 筆活動的會員靠 INNER JOIN 天然不進榜，不必額外過濾。
func buildRunners(ctx context.Context, db *pgxpool.Pool, today time.Time) ([]RunnerStat, error) {
	out := emptyRunners()
	// today 只取年/月/日（截斷到當天 00:00，Location 不影響 avgDaysPerWeek 的天數差計算），比照
	// taiwanDaySeries 的既有慣例。
	todayDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)

	// 等級門檻表只需查一次（後台可調但單次報告計算期間視為不變），供下方逐人 levelFromExp 換算，
	// 比在 SQL 端用 CASE WHEN 展開門檻表更簡單、且與會員面板算法（同樣是 Go 端迴圈）一致。
	levels, err := queryLevelConfig(ctx, db)
	if err != nil {
		return out, fmt.Errorf("runners level_config: %w", err)
	}

	rows, err := db.Query(ctx, `
		SELECT u.id::text,
		       COALESCE(NULLIF(u.name,''), u.handle) AS name,
		       u.handle,
		       u.is_virtual,
		       u.exp,
		       u.dp,
		       u.gp,
		       COALESCE(SUM(a.distance_km), 0) AS total_km,
		       COALESCE(SUM(a.duration_s), 0) AS total_duration_s,
		       COUNT(*) AS runs,
		       COUNT(DISTINCT (a.recorded_at AT TIME ZONE 'Asia/Taipei')::date) AS run_days,
		       MIN((a.recorded_at AT TIME ZONE 'Asia/Taipei')::date) AS first_day
		FROM activities a
		JOIN users u ON u.id = a.user_id
		WHERE NOT a.flagged
		GROUP BY u.id, u.name, u.handle, u.is_virtual, u.exp, u.dp, u.gp
		ORDER BY total_km DESC
		LIMIT 200`)
	if err != nil {
		return out, fmt.Errorf("runners query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, handle string
		var isVirtual bool
		var exp, dp, gp int
		var totalKm float64
		var totalDurationS, runs, runDays int
		var firstDay time.Time
		if err := rows.Scan(&id, &name, &handle, &isVirtual, &exp, &dp, &gp, &totalKm, &totalDurationS, &runs, &runDays, &firstDay); err != nil {
			return out, fmt.Errorf("runners scan: %w", err)
		}
		out = append(out, RunnerStat{
			Name:           name,
			Handle:         handle,
			IsVirtual:      isVirtual,
			TotalKm:        round2(totalKm),
			TotalDurationS: totalDurationS,
			AvgPaceS:       avgPaceSeconds(totalDurationS, totalKm),
			Runs:           runs,
			AvgDaysPerWeek: avgDaysPerWeek(runDays, firstDay, todayDate),
			Level:          levelFromExp(exp, levels),
			Dp:             dp,
			Gp:             gp,
		})
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("runners rows: %w", err)
	}

	return out, nil
}

// BuildReport 依序算出六大區塊，組成完整報告。單一區塊查詢失敗只記 log 並填入該區塊的安全預設值
// （空陣列/0），不讓整份報告因為一段查詢出錯就完全算不出來——六個區塊彼此獨立、互不依賴，這樣的
// 容錯策略比「整份重試」更划算（詳見任務規格：容忍 0 列、單一函式失敗填空區塊並記 log）。
func BuildReport(ctx context.Context, db *pgxpool.Pool) Report {
	today := taiwanNow()
	rpt := Report{
		Day:         today.Format("2006-01-02"),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if v, err := buildRegistrations(ctx, db, today); err != nil {
		log.Error().Err(err).Msg("member analytics: registrations block failed, falling back to empty block")
		rpt.Registrations = emptyRegistrations()
	} else {
		rpt.Registrations = v
	}

	if v, err := buildLogins(ctx, db, today); err != nil {
		log.Error().Err(err).Msg("member analytics: logins block failed, falling back to empty block")
		rpt.Logins = emptyLogins()
	} else {
		rpt.Logins = v
	}

	if v, err := buildMileage(ctx, db, today); err != nil {
		log.Error().Err(err).Msg("member analytics: mileage block failed, falling back to empty block")
		rpt.Mileage = emptyMileage()
	} else {
		rpt.Mileage = v
	}

	if v, err := buildParticipation(ctx, db, today); err != nil {
		log.Error().Err(err).Msg("member analytics: participation block failed, falling back to empty block")
		rpt.Participation = emptyParticipation()
	} else {
		rpt.Participation = v
	}

	if v, err := buildCards(ctx, db); err != nil {
		log.Error().Err(err).Msg("member analytics: cards block failed, falling back to empty block")
		rpt.Cards = emptyCards()
	} else {
		rpt.Cards = v
	}

	if v, err := buildSystems(ctx, db, today); err != nil {
		log.Error().Err(err).Msg("member analytics: systems block failed, falling back to empty block")
		rpt.Systems = emptySystems()
	} else {
		rpt.Systems = v
	}

	if v, err := buildRunners(ctx, db, today); err != nil {
		log.Error().Err(err).Msg("member analytics: runners block failed, falling back to empty block")
		rpt.Runners = emptyRunners()
	} else {
		rpt.Runners = v
	}

	return rpt
}
