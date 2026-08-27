// Package analytics 提供後台「會員活躍度分析」：每日台灣時間 03:00 彙整六大區塊（註冊/上線/
// 里程配速/參與度/卡片收集/各系統使用足跡）成一份 JSONB 報告存進 member_analytics_reports
// （migration 148），供後台一次讀取，不必每次開頁都重跑一輪全表統計查詢。
//
// 全部統計口徑：
//   - 排除 users.is_virtual（虛擬選手/機器人帳號，比照 internal/ops/dailyreport.go 會員數統計、
//     internal/profile/analytics.go VIP 分析的既有慣例）——注意這與 dailyreport.go 刻意「報名/營收
//     不排除虛擬選手」的取捨不同：本報告目的是反映「真人會員」的行為輪廓，虛擬選手是後台自建的
//     造勢/測試帳號，混進行為統計只會失真，故本套件六大區塊全數排除，不分區塊。
//   - activities 一律加 NOT flagged（去重/防弊旗標）；涉及「里程/配速」的查詢另外加
//     source IS NULL（只認 App GPS，比照 activity-data-source-gate 慣例）；涉及「是否有活動」
//     （DAU 輔證）則不限 source，只要有一筆有效上傳（含 Strava 等同步來源）就算活躍，見
//     buildLogins 註解。
//   - 時區一律台灣日（UTC+8 手算，禁用 time.LoadLocation——distroless 執行環境無 tzdata，理由同
//     internal/ops/selfcheck.go taiwanNow()）；SQL 端用 Postgres `AT TIME ZONE 'Asia/Taipei'` 換算
//     日界線/小時分佈，Go 端窗口邊界則用 taiwanDayBoundaryUTC 手算，兩者結果一致（台灣無日光節約，
//     固定 +8 沒有二義性）。
package analytics

// DateCount 單日筆數（新增會員/新增報名等日序列）。
type DateCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// DateKm 單日里程總和。
type DateKm struct {
	Date string  `json:"date"`
	Km   float64 `json:"km"`
}

// SourceCount 依來源分組的筆數（註冊來源歸因）。
type SourceCount struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
}

// BucketCount 分桶分佈（登入頻率/配速/月跑量/重複報名/卡片收集數，桶名固定順序見各 xxxBucket 純函式）。
type BucketCount struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

// GroupAvg 依群組（性別/年齡層）分組的平均里程與人數。
type GroupAvg struct {
	Group string  `json:"group"`
	AvgKm float64 `json:"avg_km"`
	Users int     `json:"users"`
}

// TitleCount 賽事熱門度（依報名數排序）。
type TitleCount struct {
	Title string `json:"title"`
	Count int    `json:"count"`
}

// NameCount 卡片熱門度（依取得數排序）。
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// SystemUsage 單一系統（VIP/自主訓練/個人任務/大富翁/稱號/充電站/站內信）的使用足跡。
type SystemUsage struct {
	System     string `json:"system"`
	Label      string `json:"label"`
	Users30d   int    `json:"users_30d"`
	UsersTotal int    `json:"users_total"`
}

// Registrations 註冊區塊。
type Registrations struct {
	TotalMembers int           `json:"total_members"`
	New30d       []DateCount   `json:"new_30d"`
	ByHour       []int         `json:"by_hour"` // 24 格，index=小時(0-23)；統計窗口見 buildRegistrations 註解
	BySource     []SourceCount `json:"by_source"`
}

// Logins 上線區塊。
//
// 口徑警語（務必保留，前台亦應顯示）：user_login_logs 刻意不記 Refresh（見 migration
// 123_user_login_logs.sql 檔頭），「上線」＝一次登入事件（password/google/register），不是
// session 存續——同一 token 整天用 refresh 續期、沒有重新登入，不會在這張表留下第二筆列。
// 因此 freq_dist_30d／by_hour（純粹統計登入事件本身）會低估「真正在線的人」；dau_30d／
// active_7d／active_30d 為了不被這個限制誤導，改用「當日有活動上傳（activities）OR 有登入事件
// （user_login_logs）」的聯集當作「活躍」判準，比單一登入事件更貼近真實使用情形（見 buildLogins）。
type Logins struct {
	Dau30d      []DateCount   `json:"dau_30d"`
	Active7d    int           `json:"active_7d"`
	Active30d   int           `json:"active_30d"`
	FreqDist30d []BucketCount `json:"freq_dist_30d"`
	ByHour      []int         `json:"by_hour"`
}

// Mileage 里程/配速區塊。
type Mileage struct {
	DailyKm30d        []DateKm      `json:"daily_km_30d"`
	PaceDist          []BucketCount `json:"pace_dist"`
	MonthlyVolumeDist []BucketCount `json:"monthly_volume_dist"`
	ByGender          []GroupAvg    `json:"by_gender"`
	ByAge             []GroupAvg    `json:"by_age"`
}

// Participation 參與度區塊。「有效報名」統一採 status <> 'cancelled'（比照
// internal/race/leaderboard.go:121 既有口徑：涵蓋 pending/paid/completed/expired，只排除主動取消）。
type Participation struct {
	Reg30d            []DateCount   `json:"reg_30d"`
	EverRegisteredPct float64       `json:"ever_registered_pct"`
	TopRaces          []TitleCount  `json:"top_races"`
	RepeatDist        []BucketCount `json:"repeat_dist"`
}

// Cards 卡片收集區塊（城市探索關主卡，非大富翁知識卡/貼紙）。
type Cards struct {
	Collectors     int           `json:"collectors"`
	TotalCollected int           `json:"total_collected"`
	CollectionDist []BucketCount `json:"collection_dist"`
	TopCards       []NameCount   `json:"top_cards"`
}

// Systems 各系統使用足跡區塊。
type Systems struct {
	Usage []SystemUsage `json:"usage"`
}

// RunnerStat 單一跑者的累積跑步數據（第七區塊「跑步數據分析排行」單一列）。含虛擬選手
// （IsVirtual 隨列帶出，是否隱藏交給前端 client-side 過濾，見 buildRunners 註解）。
type RunnerStat struct {
	Name           string  `json:"name"` // COALESCE(NULLIF(u.name,''), u.handle)，比照全站顯示名慣例
	Handle         string  `json:"handle"`
	IsVirtual      bool    `json:"is_virtual"`
	TotalKm        float64 `json:"total_km"`          // 累積里程（公里，四捨五入到小數第 2 位）
	TotalDurationS int     `json:"total_duration_s"`  // 累積時間（秒）
	AvgPaceS       int     `json:"avg_pace_s"`        // 距離加權平均配速（秒/公里，取整秒）
	Runs           int     `json:"runs"`              // 累積活動筆數
	AvgDaysPerWeek float64 `json:"avg_days_per_week"` // 平均每週跑步天數（四捨五入到小數第 1 位）
	// Level/Dp/Gp：目前等級／DP／GP 餘額（u.exp/u.dp/u.gp 現況快照，非本報告統計窗口內的累積量）。
	// Level 由後端依 levelFromExp（見 bucket.go，移植自 internal/profile/membership.go computeLevel）
	// 算好才進 JSON，前端只負責顯示、不重算，確保與會員面板 MemberPanel「Lv.X」一致。舊日報（本三欄
	// 上線前算出的）JSONB 裡沒有這三個鍵，前端 AnalyticsRunner 型別對應為 optional，容忍 undefined。
	Level int `json:"level"`
	Dp    int `json:"dp"`
	Gp    int `json:"gp"`
	// RankDelta／IsNew：與「上週或更早最近一份」報告比較的真人榜名次升降（見 compute.go
	// applyRankDeltas／bucket.go buildHandleRankMap）。只在真人列（IsVirtual=false）填值；虛擬列
	// 兩者永遠缺省（RankDelta=nil、IsNew=false），因為名次升降只在「真人榜」概念下有意義——虛擬
	// 選手是後台自建帳號，混進排名升降統計沒有意義。RankDelta＝上週名次－本週名次（正值＝上升，
	// 用 *int 而非 int：nil 代表「該筆缺乏可比較的上週資料」，別跟 0＝「名次不變」搞混，故
	// omitempty）。IsNew：上週報告確實有 runners 資料、但這位這次才第一次出現在真人榜（新進榜）；
	// 若上週報告根本沒有 runners 欄位（例如 migration 148 上線未滿一週），RankDelta 與 IsNew 兩者
	// 皆缺省（nil／false），無法判斷，前端顯示為「—」並附口徑小字（見 admin/analytics/page.tsx）。
	RankDelta *int `json:"rank_delta,omitempty"`
	IsNew     bool `json:"is_new"`
}

// RunnersSummary 第七區塊表格上方的「總覽統計列」（見 compute.go buildRunnersSummary）。
// 「跑者」＝至少 1 筆 NOT flagged 活動的使用者（不限 source，比照 RunnerStat 的全來源口徑，非
// buildMileage 只認 App GPS 的口徑）；「昨日」＝統計基準日（Report.Day）的前一個台灣日整天；
// 「近 7 日」比照 Logins.Active7d 的既有慣例（今天起算含今天共 7 天，開放式往後不設上界）。
// Real／Virtual 分列，前端依「隱藏虛擬選手」開關決定只顯示 real 或 real+virtual 加總（見
// admin/analytics/page.tsx RunnersSection）。舊日報（本欄位上線前算出的）沒有這個鍵，前端需容忍
// undefined、不顯示這一列統計（比照 Runners 欄位的既有慣例）。
type RunnersSummary struct {
	RanYesterdayReal    int `json:"ran_yesterday_real"`
	RanYesterdayVirtual int `json:"ran_yesterday_virtual"`
	Ran7dReal           int `json:"ran_7d_real"`
	Ran7dVirtual        int `json:"ran_7d_virtual"`
	RunnersTotalReal    int `json:"runners_total_real"`
	RunnersTotalVirtual int `json:"runners_total_virtual"`
	MembersReal         int `json:"members_real"`
	MembersVirtual      int `json:"members_virtual"`
}

// Report 完整報告契約（member_analytics_reports.report 的 JSONB 內容）。
type Report struct {
	Day           string        `json:"day"`
	GeneratedAt   string        `json:"generated_at"`
	Registrations Registrations `json:"registrations"`
	Logins        Logins        `json:"logins"`
	Mileage       Mileage       `json:"mileage"`
	Participation Participation `json:"participation"`
	Cards         Cards         `json:"cards"`
	Systems       Systems       `json:"systems"`
	// Runners 第七區塊「跑步數據分析排行」：依 total_km DESC 取前 200 名，0 筆活動的會員不進榜。
	// 舊日報（本欄位上線前算出的）沒有這個鍵，前端需容忍 undefined（見 admin/analytics/page.tsx）。
	// 注意：此欄位型別是「鍵缺失 vs. 空陣列」在 encoding/json unmarshal 語意上有差異的關鍵——
	// compute.go BuildReport 算 rank_delta 時就是利用這個既有行為（缺鍵→nil slice）判斷「上週報告
	// 根本沒有 runners 欄位」，見 applyRankDeltas 註解，改動這裡的型別/tag 前務必留意這個依賴。
	Runners []RunnerStat `json:"runners"`
	// RunnersSummary 第七區塊表格上方的總覽統計列，見 RunnersSummary 型別註解。舊日報（本欄位上線
	// 前算出的）沒有這個鍵，前端需容忍 undefined（比照 Runners 欄位的既有慣例）。
	RunnersSummary RunnersSummary `json:"runners_summary"`
}
