// config_test.go：ParseConfig 的 BattleElementChart 特殊處理（P2 修正第 3 輪，審查 data.md
// 缺陷3 CONFIRMED）＋新增 §3 欄位的 Validate() 邊界檢查。見 config.go ParseConfig 函式註解。
package rpg

import "testing"

// --- ParseConfig：raw 完全沒有 battle_element_chart key，維持 DefaultConfig() 的預設表
// （既有「未出現的欄位維持預設值」慣例，不能被 map 特殊處理破壞）---

func TestParseConfig_ElementChartAbsentKeepsDefault(t *testing.T) {
	cfg, err := ParseConfig(`{"battle_mob_hits": 99}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := DefaultConfig().BattleElementChart
	if len(cfg.BattleElementChart) != len(want) {
		t.Fatalf("raw 沒有 battle_element_chart key 時應保留預設表（%d 個屬性），got %d 個: %+v",
			len(want), len(cfg.BattleElementChart), cfg.BattleElementChart)
	}
	for attr, row := range want {
		gotRow, ok := cfg.BattleElementChart[attr]
		if !ok || len(gotRow) != len(row) {
			t.Fatalf("預設屬性 %q 應完整保留，got %+v", attr, cfg.BattleElementChart[attr])
		}
	}
	// 其餘欄位仍正常覆蓋（確認沒有因為這個修復連帶壞掉一般欄位的 PUT）。
	if cfg.BattleMobHits != 99 {
		t.Fatalf("battle_mob_hits 應被覆蓋為 99，got %v", cfg.BattleMobHits)
	}
}

// --- ParseConfig：raw 提供完整表，整體覆蓋（不與預設表合併）---

func TestParseConfig_ElementChartFullTableReplacesDefault(t *testing.T) {
	raw := `{"battle_element_chart": {"新屬性": {"fire": 2.0}}}`
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.BattleElementChart) != 1 {
		t.Fatalf("應整體覆蓋為只有 1 個屬性，got %d 個: %+v", len(cfg.BattleElementChart), cfg.BattleElementChart)
	}
	row, ok := cfg.BattleElementChart["新屬性"]
	if !ok || row["fire"] != 2.0 {
		t.Fatalf("新屬性表應生效：got %+v", cfg.BattleElementChart)
	}
	// 預設表的舊屬性（例如「金」）不該復活。
	if _, stillThere := cfg.BattleElementChart["金"]; stillThere {
		t.Fatalf("預設屬性「金」不該在整體覆蓋後復活：%+v", cfg.BattleElementChart)
	}
}

// --- ParseConfig：raw 有 battle_element_chart key、且刪掉某個屬性底下的某個 element
// sub-key——該 sub-key 真的消失，不被 DefaultConfig() 的舊值復活（審查 data.md 缺陷3 的
// 具體重現場景：管理者刪掉「金」屬性的 "fire" 相剋值後存檔）---

func TestParseConfig_ElementChartDeletedSubKeyStaysDeleted(t *testing.T) {
	def := DefaultConfig()
	if _, ok := def.BattleElementChart["金"]["fire"]; !ok {
		t.Fatalf("前置條件錯誤：預設表「金」應該有 fire 這個 key")
	}
	// 管理者存檔時只保留「金」的 water、刪掉 fire；也保留其餘屬性維持預設（模擬後台整包 PUT
	// 但這個管理者的編輯畫面只送出他改過的屬性列——用完整表模擬更貼近真實 PUT 語意，因為
	// 後台是「JSON 整包 PUT」，不是 PATCH）。
	raw := `{"battle_element_chart": {"金": {"water": 0.0}}}`
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	row, ok := cfg.BattleElementChart["金"]
	if !ok {
		t.Fatalf("「金」屬性應存在：%+v", cfg.BattleElementChart)
	}
	if _, stillThere := row["fire"]; stillThere {
		t.Fatalf("刪掉的 fire 這個 sub-key 不該被預設值復活：%+v", row)
	}
	if len(row) != 1 || row["water"] != 0.0 {
		t.Fatalf("「金」屬性應只剩 water=0.0：%+v", row)
	}
	// 其餘預設屬性（沒出現在這次 raw 裡）也不該存在——這次 PUT 送出的是完整表，只有「金」一項。
	if len(cfg.BattleElementChart) != 1 {
		t.Fatalf("整體覆蓋語意下，raw 沒提到的屬性不該殘留：%+v", cfg.BattleElementChart)
	}
}

// --- ParseConfig：raw 的 battle_element_chart 值是空物件 {}——視為「有這個 key」，整體覆蓋成
// 空表，不是被當成「沒有這個 key」而保留預設值（探測邏輯是看 key 是否存在，不是看值是否為空）---

func TestParseConfig_ElementChartEmptyObjectClearsTable(t *testing.T) {
	cfg, err := ParseConfig(`{"battle_element_chart": {}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.BattleElementChart) != 0 {
		t.Fatalf("battle_element_chart={} 應讓表完全清空，got %+v", cfg.BattleElementChart)
	}
}

// --- ParseConfig：空字串直接回預設值（既有行為，確認沒被這輪修改破壞）---

func TestParseConfig_EmptyStringReturnsDefault(t *testing.T) {
	cfg, err := ParseConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := DefaultConfig()
	if len(cfg.BattleElementChart) != len(want.BattleElementChart) {
		t.Fatalf("空字串應回完整預設表，got %+v", cfg.BattleElementChart)
	}
}

// =============================================================================
// Validate()：P2 修正第 3 輪新增欄位的邊界檢查（等級基線 per_level、aspd_reference、
// 兩個 min_ms）。
// =============================================================================

func TestConfig_ValidateRejectsNegativeMonsterPerLevel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BattleMonsterHitPerLevel = -1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("battle_monster_hit_per_level=-1 應被拒絕")
	}
}

func TestConfig_ValidateRejectsAspdReferenceOutOfRange(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BattleAspdReference = 200
	if err := cfg.Validate(); err == nil {
		t.Fatalf("battle_aspd_reference=200 應被拒絕（公式天花板，>=200 會除零/反向）")
	}
	cfg2 := DefaultConfig()
	cfg2.BattleAspdReference = 0
	if err := cfg2.Validate(); err == nil {
		t.Fatalf("battle_aspd_reference=0 應被拒絕")
	}
}

func TestConfig_ValidateRejectsAttackCooldownMinAboveBase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BattleAttackCooldownMinMs = cfg.BattleAttackCooldownMs + 1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("battle_attack_cooldown_min_ms 超過 battle_attack_cooldown_ms 應被拒絕")
	}
}

func TestConfig_ValidateRejectsCastMinAboveDefaultCast(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BattleCastMinMs = cfg.BattleDefaultCastMs + 1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("battle_cast_min_ms 超過 battle_default_cast_ms 應被拒絕")
	}
}

func TestConfig_ValidateAcceptsDefaults(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("DefaultConfig() 本身應通過 Validate：%v", err)
	}
}
