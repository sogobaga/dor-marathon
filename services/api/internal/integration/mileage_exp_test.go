package integration

import (
	"strings"
	"testing"
)

func TestComputeRewardKm(t *testing.T) {
	cases := []struct {
		name                                       string
		distanceKm                                 float64
		durationS, perKm, dpPerKm, capKm, minPaceS int
		want                                       int
	}{
		{"一般值_無條件捨去", 7.9, 3000, 10, 5, 21, 120, 7},
		{"floor_剛好整數", 8.0, 3000, 10, 5, 21, 120, 8},
		{"單趟上限_超過cap截斷", 25.5, 20000, 10, 5, 21, 120, 21},
		{"配速防造假_低於最小配速時間截斷", 10.0, 600, 10, 5, 21, 120, 5}, // 600s/120s每公里 = 5km 上限
		{"距離小於1_回0", 0.9, 600, 10, 5, 21, 120, 0},
		{"duration為0_回0", 5.0, 0, 10, 5, 21, 120, 0},
		{"perKm與dpPerKm皆為0_回0", 5.0, 3000, 0, 0, 21, 120, 0},
		{"只有dpPerKm有值仍發放", 5.0, 3000, 0, 5, 21, 120, 5},
		{"cap為0代表不設上限", 30.0, 30000, 10, 5, 0, 120, 30},
		{"minPaceS為0代表不設配速防造假", 30.0, 60, 10, 5, 0, 0, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeRewardKm(c.distanceKm, c.durationS, c.perKm, c.dpPerKm, c.capKm, c.minPaceS)
			if got != c.want {
				t.Errorf("computeRewardKm(%v,%v,%v,%v,%v,%v) = %v, want %v",
					c.distanceKm, c.durationS, c.perKm, c.dpPerKm, c.capKm, c.minPaceS, got, c.want)
			}
		})
	}
}

func TestClampDeltaInt(t *testing.T) {
	cases := []struct {
		name string
		a, b int
		want int
	}{
		{"a大於b_回差值", 8, 7, 1},
		{"a小於b_只補不扣回0", 7, 8, 0},
		{"a等於b_回0", 7, 7, 0},
		{"皆為0", 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampDeltaInt(c.a, c.b); got != c.want {
				t.Errorf("clampDeltaInt(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestClampDeltaFloat(t *testing.T) {
	cases := []struct {
		name string
		a, b float64
		want float64
	}{
		{"a大於b_回差值", 8.2, 7.1, 1.1},
		{"a小於b_只補不扣回0", 7.1, 8.2, 0},
		{"a等於b_回0", 7.5, 7.5, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clampDeltaFloat(c.a, c.b)
			// 浮點誤差容許
			diff := got - c.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 1e-9 {
				t.Errorf("clampDeltaFloat(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestDeltaCompensationScenario 對照背景描述的真實案例：GPS 7.1K 先發、Strava 8.2K 後到（時間重疊）
// → 應只補差額 1 點（floor(8.2)-floor(7.1) = 8-7 = 1），且方向反過來時「只補不扣」回 0。
func TestDeltaCompensationScenario(t *testing.T) {
	const perKm, dpPerKm, capKm, minPaceS = 10, 5, 21, 120

	gpsReward := computeRewardKm(7.1, 3000, perKm, dpPerKm, capKm, minPaceS)    // floor(7.1)=7
	stravaReward := computeRewardKm(8.2, 3200, perKm, dpPerKm, capKm, minPaceS) // floor(8.2)=8

	if gpsReward != 7 {
		t.Fatalf("gpsReward = %v, want 7", gpsReward)
	}
	if stravaReward != 8 {
		t.Fatalf("stravaReward = %v, want 8", stravaReward)
	}

	// 情境一：GPS(7.1) 先發 → Strava(8.2) 後到，重疊已發放筆是 GPS(7.1) → 應補 1 點差額
	if delta := clampDeltaInt(stravaReward, gpsReward); delta != 1 {
		t.Errorf("7.1 vs 8.2 情境（8.2 為本筆）delta = %v, want 1", delta)
	}
	if deltaKm := clampDeltaFloat(8.2, 7.1); deltaKm < 1.0999 || deltaKm > 1.1001 {
		t.Errorf("8.2 vs 7.1 deltaKm = %v, want ~1.1", deltaKm)
	}

	// 情境二：Strava(8.2) 先發 → GPS(7.1) 後到，重疊已發放筆是 Strava(8.2) → 只補不扣，回 0
	if delta := clampDeltaInt(gpsReward, stravaReward); delta != 0 {
		t.Errorf("8.2 vs 7.1 情境（7.1 為本筆）delta = %v, want 0", delta)
	}
	if deltaKm := clampDeltaFloat(7.1, 8.2); deltaKm != 0 {
		t.Errorf("7.1 vs 8.2 deltaKm = %v, want 0", deltaKm)
	}

	// 情境三：兩筆距離相等 → 差額為 0
	if delta := clampDeltaInt(gpsReward, gpsReward); delta != 0 {
		t.Errorf("相等情境 delta = %v, want 0", delta)
	}
}

// --- 對抗式審查 CRITICAL-2：external_award_ledger 去重雜湊測試 ---
//
// externalAwardHash 是 external_award_ledger（migrations/131）去重的唯一鍵材料來源（見
// AwardMileageExp ③.5）：AwardMileageExp/awardMileageDedup 本身需要一顆真正的 Postgres 才能跑
// （advisory lock、多表交易），這個套件目前沒有、也未引入任何 DB-backed 測試基礎設施（testcontainers/
// sqlmock 等 — 這個 repo 迄今所有 Go 測試皆為純函式單元測試），故這裡改為對「帳本去重能否正確辨識
// 同一筆 vs 不同筆外部活動」的核心依賴——雜湊本身——做嚴謹的單元測試：同一 (source, external_id)
// 必須產生同一把鍵（ledger 才擋得住重複發放）、不同 external_id 或不同 source 必須產生不同鍵
// （否則會誤傷不相干的活動）。實際交易層的 dedup 行為（命中不再 credit／未命中 credit 後寫入帳本）
// 已在 mileage_exp.go 逐行走查（見對抗式審查回報的 trace），並靠這裡的雜湊正確性做地基保證。

func TestExternalAwardHashDeterministic(t *testing.T) {
	h1 := externalAwardHash("strava", "123456789")
	h2 := externalAwardHash("strava", "123456789")
	if h1 != h2 {
		t.Fatalf("same (source, external_id) should hash identically: %q vs %q", h1, h2)
	}
	if len(h1) != 64 { // sha256 hex 固定 64 字元
		t.Fatalf("expected 64-char hex sha256 digest, got %d chars: %q", len(h1), h1)
	}
}

func TestExternalAwardHashDiffersByExternalID(t *testing.T) {
	h1 := externalAwardHash("strava", "123456789")
	h2 := externalAwardHash("strava", "987654321")
	if h1 == h2 {
		t.Fatalf("different external_id must not collide: both hashed to %q", h1)
	}
}

func TestExternalAwardHashDiffersBySource(t *testing.T) {
	// 同一個 external_id 字串若剛好在不同 provider 撞號，不該被誤判成同一筆（沒有 ":" 分隔會有
	// "strava"+"a1"+"strava" 的字串前綴混淆風險，這裡驗證分隔確實有效區分 source）。
	h1 := externalAwardHash("strava", "12345")
	h2 := externalAwardHash("garmin", "12345")
	if h1 == h2 {
		t.Fatalf("different source with same external_id must not collide: both hashed to %q", h1)
	}
}

func TestExternalAwardHashNoDelimiterConfusion(t *testing.T) {
	// source+external_id 相接後若無分隔符，"st"+"rava123" 與 "stra"+"va123" 會撞成同一個明文——
	// 用 ":" 分隔後兩者必須不同，防止這類邊界情況被誤判為同一筆外部活動。
	h1 := externalAwardHash("st", "rava123")
	h2 := externalAwardHash("stra", "va123")
	if h1 == h2 {
		t.Fatalf("delimiter should prevent concatenation ambiguity: both hashed to %q", h1)
	}
}

// --- 2026-09-03 owner 回收決策：overlap 查詢 NOT IN(...) 清單的純函式測試 ---
//
// benignReasonsSQLIn 把 benignFlagReasons map 轉成 SQL IN(...) 用的逗號分隔清單，供 AwardMileageExp
// 的 overlap 查詢排除「非良性標記已發放列」（見 mileage_exp.go 該函式與 benignReasonsSQLIn 的註解）。
// 這裡驗證：清單內容跟 benignFlagReasons 完全一致（一個不多一個不少）、每個 key 都正確加上單引號、
// 輸出結果穩定（sort 過，不受 map 疊代順序影響）。

func TestBenignReasonsSQLIn_ContainsExactlyMapKeys(t *testing.T) {
	got := benignReasonsSQLIn(benignFlagReasons)
	parts := strings.Split(got, ",")
	if len(parts) != len(benignFlagReasons) {
		t.Fatalf("benignReasonsSQLIn produced %d entries, want %d (from map): %q", len(parts), len(benignFlagReasons), got)
	}
	for _, p := range parts {
		if len(p) < 2 || p[0] != '\'' || p[len(p)-1] != '\'' {
			t.Fatalf("entry %q is not single-quoted", p)
		}
		key := p[1 : len(p)-1]
		if !benignFlagReasons[key] {
			t.Fatalf("entry %q (key=%q) is not a real benignFlagReasons key", p, key)
		}
	}
	for key := range benignFlagReasons {
		if !strings.Contains(got, "'"+key+"'") {
			t.Fatalf("benignReasonsSQLIn missing key %q, got %q", key, got)
		}
	}
}

func TestBenignReasonsSQLIn_StableOrder(t *testing.T) {
	// 同一份 map 連續呼叫兩次應該產生完全相同的字串（sort 過，不受 map 疊代隨機順序影響）——
	// 這個 SQL 字串在套件初始化時只算一次（package-level var），穩定性本身不是執行期風險，但
	// 用測試釘住這個不變量，避免日後有人把 sort.Strings 誤刪掉又沒發現（測試才會浮動失敗）。
	a := benignReasonsSQLIn(benignFlagReasons)
	b := benignReasonsSQLIn(benignFlagReasons)
	if a != b {
		t.Fatalf("benignReasonsSQLIn is not stable across calls: %q vs %q", a, b)
	}
}

func TestBenignReasonsSQLIn_EmptyMap(t *testing.T) {
	if got := benignReasonsSQLIn(map[string]bool{}); got != "" {
		t.Fatalf("empty map should produce empty string, got %q", got)
	}
}
