package analytics

import (
	"time"
)

// 本檔全部函式為純函式（不碰 DB/時鐘），供 compute.go 組裝報告使用，並各自附單元測試
// （見 bucket_test.go）。

// taiwanNow 目前的台灣時間（UTC+8 固定 offset 手算；回傳值 Location() 仍是 UTC，但年/月/日/時
// 欄位代表台灣本地時刻——比照 internal/ops/selfcheck.go taiwanNow() 的既有慣例、同樣的理由
// 「distroless 執行環境無 tzdata，禁用 time.LoadLocation("Asia/Taipei")」）。
func taiwanNow() time.Time {
	return time.Now().UTC().Add(8 * time.Hour)
}

// taiwanDayBoundaryUTC 把一個「台灣本地時刻」（例如 taiwanNow() 的回傳值）截斷成當天 00:00，
// 再換算回真正的 UTC 絕對時刻（減 8 小時），可直接拿去跟 timestamptz 欄位比較（timestamptz 比較
// 只看絕對時刻，不看時區標籤）。
func taiwanDayBoundaryUTC(taiwanLocal time.Time) time.Time {
	d := time.Date(taiwanLocal.Year(), taiwanLocal.Month(), taiwanLocal.Day(), 0, 0, 0, 0, time.UTC)
	return d.Add(-8 * time.Hour)
}

// taiwanDaySeries 產生連續 N 天的台灣日期字串序列（YYYY-MM-DD），由舊到新，含 taiwanToday 代表的
// 那一天（taiwanToday 應為「台灣本地時刻」表示法，例如 taiwanNow() 的回傳值；只讀其年/月/日欄位，
// Location 不影響結果）。days<=0 回傳空陣列。供 mergeDateCounts/mergeDateKm 把 SQL 查回的零散
// (date,value) 對照補齊成連續序列（缺的日期補 0），前端畫圖表不必自己處理缺口。
func taiwanDaySeries(taiwanToday time.Time, days int) []string {
	if days <= 0 {
		return []string{}
	}
	base := time.Date(taiwanToday.Year(), taiwanToday.Month(), taiwanToday.Day(), 0, 0, 0, 0, time.UTC)
	out := make([]string, days)
	for i := 0; i < days; i++ {
		out[i] = base.AddDate(0, 0, -(days - 1 - i)).Format("2006-01-02")
	}
	return out
}

// mergeDateCounts 把序列（taiwanDaySeries 產出）與零散計數 map 合併成完整的 []DateCount，
// 缺的日期補 0；series 以外的 key 忽略（理論上不會發生，SQL 查詢的窗口與 series 天數一致）。
func mergeDateCounts(series []string, counts map[string]int) []DateCount {
	out := make([]DateCount, len(series))
	for i, d := range series {
		out[i] = DateCount{Date: d, Count: counts[d]}
	}
	return out
}

// mergeDateKm 同 mergeDateCounts，值為公里數（浮點）。
func mergeDateKm(series []string, kms map[string]float64) []DateKm {
	out := make([]DateKm, len(series))
	for i, d := range series {
		out[i] = DateKm{Date: d, Km: kms[d]}
	}
	return out
}

// bucketList 依固定順序 order 組出 []BucketCount，counts 沒有的桶補 0（保證每次回傳的桶清單完整、
// 順序穩定，前端圖表類別軸不會因為某桶剛好 0 筆而消失或跳動）。
func bucketList(order []string, counts map[string]int) []BucketCount {
	out := make([]BucketCount, len(order))
	for i, b := range order {
		out[i] = BucketCount{Bucket: b, Count: counts[b]}
	}
	return out
}

// groupAvgList 依固定順序 order 組出 []GroupAvg；某群組完全沒有人（含 0 里程使用者）時 avg_km=0。
func groupAvgList(order []string, sums map[string]float64, counts map[string]int) []GroupAvg {
	out := make([]GroupAvg, len(order))
	for i, g := range order {
		n := counts[g]
		var avg float64
		if n > 0 {
			avg = round2(sums[g] / float64(n))
		}
		out[i] = GroupAvg{Group: g, AvgKm: avg, Users: n}
	}
	return out
}

// round2 四捨五入到小數第二位（公里/百分比顯示用，避免 JSON 吐出過長的浮點數尾巴）。只用於本套件
// 內部恆為非負的 avg_km/百分比，未處理負數。加上極小 epsilon 抵銷二進位浮點數無法精確表示十進位
// 小數的誤差（例如字面上的 1.005 實際儲存成 1.00499999999999989...，不加 epsilon 會四捨五入成
// 1.00 而非直覺預期的 1.01；見 bucket_test.go TestRound2）。
func round2(f float64) float64 {
	const epsilon = 1e-9
	return float64(int64(f*100+0.5+epsilon)) / 100
}

// round1 四捨五入到小數第一位（週均跑步天數顯示用）。邏輯同 round2，只是精度不同；同樣只用於本
// 套件內部恆為非負的數值（avg_days_per_week），未處理負數。
func round1(f float64) float64 {
	const epsilon = 1e-9
	return float64(int64(f*10+0.5+epsilon)) / 10
}

// avgPaceSeconds 距離加權平均配速（總秒數/總公里，取整秒，四捨五入而非無條件捨去）。totalKm<=0
// （理論上呼叫端 buildRunners 用 INNER JOIN 保證至少一筆活動、distance_km 恆為非負，這裡防禦性處理
// 避免除以 0）回傳 0。
func avgPaceSeconds(totalDurationS int, totalKm float64) int {
	if totalKm <= 0 {
		return 0
	}
	return int(float64(totalDurationS)/totalKm + 0.5)
}

// avgDaysPerWeek 平均每週跑步天數＝有活動的相異台灣日數 ÷ 經過週數；週數＝(今天−首跑日的天數)÷7，
// 下限 1 週（避免首跑當天、或觀察窗口不滿一週時分母趨近 0 使比值失真膨脹），結果四捨五入到小數
// 第 1 位。runDays<=0（理論上呼叫端只會餵有活動的使用者，這裡防禦性處理）回傳 0。firstDay/today
// 只讀年/月/日欄位（呼叫端應傳入已截斷到當天 00:00 的值，比照 taiwanDaySeries 的既有慣例，
// Location 不影響結果）。
func avgDaysPerWeek(runDays int, firstDay, today time.Time) float64 {
	if runDays <= 0 {
		return 0
	}
	d1 := time.Date(firstDay.Year(), firstDay.Month(), firstDay.Day(), 0, 0, 0, 0, time.UTC)
	d2 := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	days := d2.Sub(d1).Hours() / 24
	weeks := days / 7
	if weeks < 1 {
		weeks = 1
	}
	return round1(float64(runDays) / weeks)
}

// --- 分桶標籤定義（固定順序，供 bucketList 使用）---

var loginFreqBucketOrder = []string{"0", "1-2", "3-9", "10-29", "30+"}

// loginFreqBucket 30 日內登入事件次數分桶：0 / 1-2 / 3-9 / 10-29 / 30+ 次。
func loginFreqBucket(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n <= 2:
		return "1-2"
	case n <= 9:
		return "3-9"
	case n <= 29:
		return "10-29"
	default:
		return "30+"
	}
}

var paceBucketOrder = []string{"<5:00", "5-6", "6-7", "7-8", ">8:00"}

// paceBucket 每人 30 日平均配速（秒/公里）分桶：<5:00 / 5-6 / 6-7 / 7-8 / >8:00（分:秒/公里）。
// avgPaceS<=0（無效值，理論上不會發生——呼叫端只餵有實際里程的使用者）一律歸類到 "<5:00"，
// 避免產生不存在於 paceBucketOrder 的桶名。
func paceBucket(avgPaceS float64) string {
	switch {
	case avgPaceS <= 0:
		return "<5:00"
	case avgPaceS < 300:
		return "<5:00"
	case avgPaceS < 360:
		return "5-6"
	case avgPaceS < 420:
		return "6-7"
	case avgPaceS < 480:
		return "7-8"
	default:
		return ">8:00"
	}
}

var monthlyVolumeBucketOrder = []string{"0", "1-20", "21-50", "51-100", "100+"}

// monthlyVolumeBucket 每人 30 日總跑量（公里）分桶：0 / 1-20 / 21-50 / 51-100 / 100+ km。
func monthlyVolumeBucket(km float64) string {
	switch {
	case km <= 0:
		return "0"
	case km <= 20:
		return "1-20"
	case km <= 50:
		return "21-50"
	case km <= 100:
		return "51-100"
	default:
		return "100+"
	}
}

var repeatBucketOrder = []string{"0", "1", "2-3", "4+"}

// repeatBucket 每人累計報名場次（distinct race_id，status<>'cancelled'）分桶：0 / 1 / 2-3 / 4+ 場。
func repeatBucket(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n == 1:
		return "1"
	case n <= 3:
		return "2-3"
	default:
		return "4+"
	}
}

var cardBucketOrder = []string{"0", "1-5", "6-20", "21-50", "50+"}

// cardBucket 每人取得的城市探索卡片數分桶：0 / 1-5 / 6-20 / 21-50 / 50+ 張。
func cardBucket(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n <= 5:
		return "1-5"
	case n <= 20:
		return "6-20"
	case n <= 50:
		return "21-50"
	default:
		return "50+"
	}
}

var ageBucketOrder = []string{"<18", "18-24", "25-34", "35-44", "45-54", "55+", "未填"}

// ageBucket 年齡層分桶：<18 / 18-24 / 25-34 / 35-44 / 45-54 / 55+ / 未填（birthday 為 nil，即
// user_profiles.birthday 未填）。asOf 為計算基準時刻（傳 taiwanNow() 或測試用固定時刻）；週歲以
// 「asOf 是否已過今年生日」判斷，不做時區敏感處理（年齡以「天」為粒度已足夠精準，不需要糾結到
// 台灣/UTC 的整點差異）。負值年齡（生日填在未來，理論上被前端表單擋掉，這裡防禦性地歸到 "<18"）。
func ageBucket(birthday *time.Time, asOf time.Time) string {
	if birthday == nil {
		return "未填"
	}
	age := asOf.Year() - birthday.Year()
	if asOf.Month() < birthday.Month() || (asOf.Month() == birthday.Month() && asOf.Day() < birthday.Day()) {
		age--
	}
	switch {
	case age < 18:
		return "<18"
	case age <= 24:
		return "18-24"
	case age <= 34:
		return "25-34"
	case age <= 44:
		return "35-44"
	case age <= 54:
		return "45-54"
	default:
		return "55+"
	}
}

// levelRow 等級門檻（level_config 表單列，只取 levelFromExp 需要的兩欄；後台可調，見
// internal/profile/membership.go LevelConfig／admin/levels 頁）。
type levelRow struct {
	Level       int
	ExpRequired int
}

// levelFromExp 依 exp 與等級門檻表（由 queryLevelConfig 依 exp_required 升冪查回）推導目前等級。
// 邏輯逐行對照移植自 internal/profile/membership.go:83-94 computeLevel（會員面板 MemberPanel.tsx
// 顯示「Lv.X」的權威來源、GET /api/v1/profile/dashboard 回傳的 DashboardInfo.Level）——這裡只需要
// 排行榜要顯示的等級數字本身，不需要 title/本級門檻/下一級門檻，故簡化回傳值；levels 為空（理論上
// 不會發生，level_config 至少有 1 級種子資料，防禦性處理）回傳預設 1 級，比照來源函式的預設值。
func levelFromExp(exp int, levels []levelRow) int {
	level := 1
	for i := range levels {
		if exp >= levels[i].ExpRequired {
			level = levels[i].Level
		} else {
			break
		}
	}
	return level
}

var genderGroupOrder = []string{"male", "female", "other", "unspecified"}

// normalizeGender 把 user_profiles.gender（male/female/other/NULL/空字串）正規化成固定分組鍵；
// 空值統一歸 "unspecified"（未填），維持與 genderGroupOrder 一致，非預期值（理論上 Go 層已驗證，
// 不會發生）也會落在 "unspecified" 而不是產生 bucketList 找不到的孤兒鍵。
func normalizeGender(g string) string {
	switch g {
	case "male", "female", "other":
		return g
	default:
		return "unspecified"
	}
}
