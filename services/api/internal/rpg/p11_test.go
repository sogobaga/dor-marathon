// p11_test.go：DORPG P11（scratchpad/dorpg_p11/CONTRACT.md，v2 校準後定稿）——怪物強度九級
// 表的純函式測試（ScaleMonsterByRank 公式、levelCurveAt、SummonConfig/RankRow/MonsterRow/
// EncounterRow 的新增驗證規則、group/buildWireEncounterInfo 推導），以及 migration 189 的
// 自我核對（rank CHECK 九值、20 場槽位數＝monster_count、九隻新怪 poster_url 沿用既有五隻）。
// 不連 DB（比照 p10_test.go/p12_test.go 既有慣例）；readMigrationFile 是 p10_test.go 已定義的
// 套件層級 helper，這裡直接沿用，不重複宣告。
package rpg

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ScaleMonsterByRank：公式（含 level_curve、floor 順序）。
// ---------------------------------------------------------------------------

func TestScaleMonsterByRank_MatchesFormula(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", Rank: "B", HPMult: 1.3, AtkMult: 1.1, DefMult: 1.2, SpeedMult: 1}
	r := RankRow{Rank: "B", HPMult: 6, AtkMult: 6, DefMult: 0.8, MdefMult: 0.8, LevelCurve: map[string]float64{}}
	const level = 30
	const encounterScale = 1.0
	const slotScale = 1.0

	ref := RefPlayerStats(cfg, level)
	sm := ScaleMonsterByRank(cfg, m, r, encounterScale, slotScale, level)

	wantHP := int(math.Floor(float64(ref.MaxHP) * r.HPMult * m.HPMult * 1 * encounterScale * slotScale))
	if wantHP < 1 {
		wantHP = 1
	}
	if sm.HPMax != wantHP {
		t.Fatalf("HPMax 不符公式：want %d got %d", wantHP, sm.HPMax)
	}
	wantAtk := int(math.Floor(ref.Atk * r.AtkMult * m.AtkMult * 1 * encounterScale))
	if sm.Atk != wantAtk {
		t.Fatalf("Atk 不符公式：want %d got %d", wantAtk, sm.Atk)
	}
	wantDef := int(math.Floor(ref.Def * r.DefMult * m.DefMult * 1 * encounterScale))
	if sm.Def != wantDef {
		t.Fatalf("Def 不符公式：want %d got %d", wantDef, sm.Def)
	}
	wantMdef := int(math.Floor(ref.Mdef * r.MdefMult * m.DefMult * 1 * encounterScale))
	if sm.Mdef != wantMdef {
		t.Fatalf("Mdef 不符公式：want %d got %d", wantMdef, sm.Mdef)
	}
	// matk 只乘 atk 這條線的倍率，不疊加 encounterScale（同 ScaleMonsterByLevel 既有規則）。
	wantMatk := int(math.Floor(ref.Matk * r.AtkMult * m.AtkMult * 1))
	if sm.Matk != wantMatk {
		t.Fatalf("Matk 不符公式（不應疊加 encounterScale）：want %d got %d", wantMatk, sm.Matk)
	}
	if sm.Level != level {
		t.Fatalf("rank 模式的 Level 應直接等於傳入的 N：want %d got %d", level, sm.Level)
	}
}

func TestScaleMonsterByRank_SlotScaleOnlyAffectsHP(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	r := RankRow{Rank: "F", HPMult: 1.8, AtkMult: 1.8, DefMult: 0.24, MdefMult: 0.24}
	base := ScaleMonsterByRank(cfg, m, r, 1.0, 1.0, 20)
	scaled := ScaleMonsterByRank(cfg, m, r, 1.0, 2.0, 20)
	if scaled.HPMax != base.HPMax*2 {
		t.Fatalf("slotScale 加倍應讓 HPMax 剛好加倍：base=%d got=%d", base.HPMax, scaled.HPMax)
	}
	if scaled.Atk != base.Atk || scaled.Def != base.Def || scaled.Mdef != base.Mdef || scaled.Matk != base.Matk {
		t.Fatalf("slotScale 不應影響 atk/def/mdef/matk：base=%+v got=%+v", base, scaled)
	}
}

func TestScaleMonsterByRank_LevelCurveAppliesWhenPositive(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	base := RankRow{Rank: "A", HPMult: 11.5, AtkMult: 11.5, DefMult: 1.53, MdefMult: 1.53, LevelCurve: map[string]float64{}}
	curved := base
	curved.LevelCurve = map[string]float64{"30": 2.0}

	got := ScaleMonsterByRank(cfg, m, curved, 1.0, 1.0, 30)
	want := ScaleMonsterByRank(cfg, m, base, 2.0 /* 用 encounterScale 模擬同等倍率驗證方向 */, 1.0, 30)
	// 不要求逐位元相同（encounterScale 疊加位置跟 curve 一樣但 matk 不吃 encounterScale，
	// 只驗證「有 curve 的 HP/Atk/Def/Mdef」明顯大於「curve=1 的基準」，方向正確即可）。
	baseNoCurve := ScaleMonsterByRank(cfg, m, base, 1.0, 1.0, 30)
	if got.HPMax <= baseNoCurve.HPMax || got.Atk <= baseNoCurve.Atk {
		t.Fatalf("level_curve[30]=2.0 應讓 HP/Atk 明顯高於 curve=1 的基準：curved=%+v base=%+v", got, baseNoCurve)
	}
	_ = want
}

func TestScaleMonsterByRank_LevelCurveMissingOrNonPositiveTreatedAsOne(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	r1 := RankRow{Rank: "F", HPMult: 1.8, AtkMult: 1.8, DefMult: 0.24, MdefMult: 0.24, LevelCurve: nil}
	r2 := r1
	r2.LevelCurve = map[string]float64{"10": 0} // 值 <=0 視為未設定
	r3 := r1
	r3.LevelCurve = map[string]float64{"99": 5.0} // 查無 level=10 這個鍵

	want := ScaleMonsterByRank(cfg, m, r1, 1.0, 1.0, 10)
	for _, r := range []RankRow{r2, r3} {
		got := ScaleMonsterByRank(cfg, m, r, 1.0, 1.0, 10)
		if got.HPMax != want.HPMax || got.Atk != want.Atk {
			t.Fatalf("level_curve 缺鍵或 <=0 應等同 1：want %+v got %+v", want, got)
		}
	}
}

func TestScaleMonsterByRank_HPFloorAtLeastOne(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 0.0001, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	r := RankRow{Rank: "F", HPMult: 0.0001, AtkMult: 1, DefMult: 1, MdefMult: 1}
	sm := ScaleMonsterByRank(cfg, m, r, 1.0, 1.0, 1)
	if sm.HPMax < 1 {
		t.Fatalf("HPMax 不可小於 1，got %d", sm.HPMax)
	}
}

// TestScaleMonsterByRank_MatchesLv27ReferenceAnchor INTEGRATOR 補（2026-09-20，任務指定）：
// 用任務給定的 Lv27 錨點（Ref(27)＝hp 1046／atk 29／matk 35／def 23／mdef 26，跟
// apps/web/src/lib/dorpg/refPlayerTable.json 的 level=27 那列逐位元相同——該檔由本套件的
// RefPlayerTable(DefaultConfig()) 產生，TestRefPlayerTable_MatchesJSON 鎖住兩邊不漂移，見
// reflevel.go 檔頭）與 migration 189 seed 的九級倍率（F/E/SS 三級，monster mult 全 1.0），
// 手算 hp/atk/def/mdef/matk 的期望值，同一組數字也貼進 apps/web/scripts/verify-dorpg-engine.mjs
// 的對應測試（TS scaleMonsterByRank()）——這是 Go/TS 兩份 ScaleMonsterByRank 實作是否真的逐位元
// 一致的跨語言核對，不是只驗證「公式內部自洽」（TestScaleMonsterByRank_MatchesFormula 已經做過
// 那件事）。首先斷言 RefPlayerStats(DefaultConfig(),27) 真的還是這五個數字——如果之後
// DefaultConfig() 的六圍成長/戰鬥公式被調整，這裡會先炸，提醒維護者「九級校準表的錨點可能已經
// 過期，CONTRACT.md §1 的校準基準需要重新檢視」，而不是讓 rank 模式的怪物數值悄悄跟著漂移卻
// 沒人發現。
func TestScaleMonsterByRank_MatchesLv27ReferenceAnchor(t *testing.T) {
	cfg := DefaultConfig()
	const level = 27
	ref := RefPlayerStats(cfg, level)
	if ref.MaxHP != 1046 || ref.Atk != 29 || ref.Matk != 35 || ref.Def != 23 || ref.Mdef != 26 {
		t.Fatalf("Ref(27) 已偏離任務給定的校準錨點（hp1046/atk29/matk35/def23/mdef26），"+
			"got hp=%d atk=%v matk=%v def=%v mdef=%v——CONTRACT.md §1 的九級校準表可能需要重新校準",
			ref.MaxHP, ref.Atk, ref.Matk, ref.Def, ref.Mdef)
	}

	m := MonsterRow{ID: "anchor", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	cases := []struct {
		rank                                         RankRow
		wantHP, wantAtk, wantDef, wantMdef, wantMatk int
	}{
		// 倍率逐字取自 migration 189 seed（189_rpg_p11_ranks.sql）。
		{RankRow{Rank: "F", HPMult: 1.8, AtkMult: 1.8, DefMult: 0.240, MdefMult: 0.240}, 1882, 52, 5, 6, 63},
		{RankRow{Rank: "E", HPMult: 0.7, AtkMult: 8.75, DefMult: 0.093, MdefMult: 0.093}, 732, 253, 2, 2, 306},
		{RankRow{Rank: "SS", HPMult: 190, AtkMult: 6.5, DefMult: 0.870, MdefMult: 0.870}, 198740, 188, 20, 22, 227},
	}
	for _, c := range cases {
		sm := ScaleMonsterByRank(cfg, m, c.rank, 1.0, 1.0, level)
		if sm.HPMax != c.wantHP || sm.Atk != c.wantAtk || sm.Def != c.wantDef || sm.Mdef != c.wantMdef || sm.Matk != c.wantMatk {
			t.Fatalf("rank=%s：want hp=%d atk=%d def=%d mdef=%d matk=%d，got hp=%d atk=%d def=%d mdef=%d matk=%d（跟 verify-dorpg-engine.mjs 的 TS 對照值不一致，Go/TS 兩份 ScaleMonsterByRank 已經漂移）",
				c.rank.Rank, c.wantHP, c.wantAtk, c.wantDef, c.wantMdef, c.wantMatk,
				sm.HPMax, sm.Atk, sm.Def, sm.Mdef, sm.Matk)
		}
	}
}

func TestLevelCurveAt_DefaultsAndLookup(t *testing.T) {
	if v := levelCurveAt(nil, 30); v != 1 {
		t.Fatalf("nil map 應回 1，got %v", v)
	}
	if v := levelCurveAt(map[string]float64{}, 30); v != 1 {
		t.Fatalf("空 map 應回 1，got %v", v)
	}
	if v := levelCurveAt(map[string]float64{"30": 1.5}, 30); v != 1.5 {
		t.Fatalf("命中鍵應回該值，got %v", v)
	}
	if v := levelCurveAt(map[string]float64{"30": -1}, 30); v != 1 {
		t.Fatalf("負值應退回 1，got %v", v)
	}
}

// ---------------------------------------------------------------------------
// 回歸鎖定：legacy（scaling_mode="legacy"）數值零改動——ScaleMonsterByLevel 本身完全沒有
// 被本輪改動，且不讀 MonsterRow.Rank（P11 新增的欄位語意），用兩個只有 Rank 不同的 MonsterRow
// 驗證輸出逐位元相同，證明「怪物現在多了一個 Rank 欄位」這件事不會悄悄影響既有六場的公式。
// ---------------------------------------------------------------------------

func TestScaleMonsterByLevel_UnaffectedByMonsterRank(t *testing.T) {
	cfg := DefaultConfig()
	withoutRank := MonsterRow{ID: "m1", HPMult: 0.9, AtkMult: 1.0, DefMult: 1.0, SpeedMult: 1}
	withRank := withoutRank
	withRank.Rank = "A" // P11 之後既有怪物都會帶 rank，legacy 公式必須完全無視這個欄位

	a := ScaleMonsterByLevel(cfg, withoutRank, 0.6, 1.0, 40)
	b := ScaleMonsterByLevel(cfg, withRank, 0.6, 1.0, 40)
	if a != b {
		t.Fatalf("ScaleMonsterByLevel 不應吃 Rank 欄位：無 rank=%+v 有 rank=%+v", a, b)
	}
}

func TestScaleMonsterByLevel_StillMatchesP6Formula(t *testing.T) {
	// 鎖定既有公式本身沒有被 P11 動到（同 scaling_level_test.go 既有測試的公式，這裡獨立重跑
	// 一次確認 P11 沒有改到 scaling.go 裡 ScaleMonsterByLevel 這段既有邏輯）。
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 1.8, AtkMult: 1.25, DefMult: 1.35, SpeedMult: 1.15}
	const level = 30
	ref := RefPlayerStats(cfg, level)
	sm := ScaleMonsterByLevel(cfg, m, 1.0, 1.0, level)

	wantHP := int(math.Floor(float64(ref.MaxHP) * m.HPMult * cfg.BattleLvHPRatio * 1.0 * 1.0))
	wantAtk := int(math.Floor(ref.Atk * m.AtkMult * cfg.BattleLvAtkRatio * 1.0))
	if sm.HPMax != wantHP || sm.Atk != wantAtk {
		t.Fatalf("legacy 公式應維持既有 P6 定義：want hp=%d atk=%d got hp=%d atk=%d", wantHP, wantAtk, sm.HPMax, sm.Atk)
	}
}

// ---------------------------------------------------------------------------
// SummonConfig / RankRow / MonsterRow / EncounterRow：新增驗證規則。
// ---------------------------------------------------------------------------

func TestSummonConfig_Validate_Valid(t *testing.T) {
	s := SummonConfig{Waves: []SummonWave{
		{AtHpPct: 60, Rank: "E", Count: 2, PowerScale: 1},
		{AtHpPct: 20, Rank: "A", Count: 1, MonsterIDs: []string{"DOR-MON-R-A"}, PowerScale: 1},
	}}
	if err := s.Validate(); err != nil {
		t.Fatalf("合法設定不應報錯：%v", err)
	}
}

// TestSummonWave_UnmarshalJSON_DefaultsPowerScaleToOne P11 修正：既有 migration 189 seed／
// 大多數後台先前寫入的 summon JSON 都沒有 power_scale 這個新 key，JSON 缺省時必須解讀成 1.0
// （不折減），而不是 Go 零值 0.0（0.0 會讓 buildSummonPool 算出的 encounterScale 直接歸零）。
// 明確填 0 則要維持 0（不能被預設值悄悄蓋掉），讓後續 Validate 依然能拒絕這筆髒資料。
func TestSummonWave_UnmarshalJSON_DefaultsPowerScaleToOne(t *testing.T) {
	var missing SummonWave
	if err := json.Unmarshal([]byte(`{"at_hp_pct":60,"rank":"E","count":2}`), &missing); err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if missing.PowerScale != 1.0 {
		t.Fatalf("缺省 power_scale 應視為 1.0，got %v", missing.PowerScale)
	}

	var explicit SummonWave
	if err := json.Unmarshal([]byte(`{"at_hp_pct":60,"rank":"E","count":2,"power_scale":0.4}`), &explicit); err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if explicit.PowerScale != 0.4 {
		t.Fatalf("明確填的 power_scale 應原樣保留，got %v", explicit.PowerScale)
	}

	var zero SummonWave
	if err := json.Unmarshal([]byte(`{"at_hp_pct":60,"rank":"E","count":2,"power_scale":0}`), &zero); err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if zero.PowerScale != 0 {
		t.Fatalf("明確填 0 不該被預設值悄悄蓋成 1.0，got %v", zero.PowerScale)
	}
}

func TestSummonConfig_Validate_PowerScaleOutOfRange(t *testing.T) {
	for _, bad := range []float64{0, 0.04, 2.01, -1} {
		s := SummonConfig{Waves: []SummonWave{{AtHpPct: 50, Rank: "E", Count: 1, PowerScale: bad}}}
		if err := s.Validate(); err == nil {
			t.Fatalf("power_scale=%v 應被拒絕", bad)
		}
	}
	for _, ok := range []float64{0.05, 1, 2.0} {
		s := SummonConfig{Waves: []SummonWave{{AtHpPct: 50, Rank: "E", Count: 1, PowerScale: ok}}}
		if err := s.Validate(); err != nil {
			t.Fatalf("power_scale=%v 邊界值不應被拒絕：%v", ok, err)
		}
	}
}

func TestSummonConfig_Validate_AtHpPctOutOfRange(t *testing.T) {
	for _, bad := range []int{0, 100, -1} {
		s := SummonConfig{Waves: []SummonWave{{AtHpPct: bad, Rank: "E", Count: 1}}}
		if err := s.Validate(); err == nil {
			t.Fatalf("at_hp_pct=%d 應被拒絕", bad)
		}
	}
}

func TestSummonConfig_Validate_UnknownRank(t *testing.T) {
	s := SummonConfig{Waves: []SummonWave{{AtHpPct: 50, Rank: "X", Count: 1}}}
	if err := s.Validate(); err == nil {
		t.Fatal("不合法的 rank 應被拒絕")
	}
}

func TestSummonConfig_Validate_CountOutOfRange(t *testing.T) {
	for _, bad := range []int{0, 6, -1} {
		s := SummonConfig{Waves: []SummonWave{{AtHpPct: 50, Rank: "E", Count: bad}}}
		if err := s.Validate(); err == nil {
			t.Fatalf("count=%d 應被拒絕", bad)
		}
	}
}

func TestRankRow_Validate_Valid(t *testing.T) {
	r := RankRow{Rank: "SS", HPMult: 190, AtkMult: 6.5, DefMult: 0.87, MdefMult: 0.87}
	if err := r.Validate(); err != nil {
		t.Fatalf("合法列不應報錯：%v", err)
	}
}

func TestRankRow_Validate_UnknownRankRejected(t *testing.T) {
	r := RankRow{Rank: "X", HPMult: 1, AtkMult: 1, DefMult: 1, MdefMult: 1}
	if err := r.Validate(); err == nil {
		t.Fatal("rank 不在九值白名單應被拒絕")
	}
}

func TestRankRow_Validate_NonPositiveMultRejected(t *testing.T) {
	base := RankRow{Rank: "F", HPMult: 1, AtkMult: 1, DefMult: 1, MdefMult: 1}
	for _, mutate := range []func(*RankRow){
		func(r *RankRow) { r.HPMult = 0 },
		func(r *RankRow) { r.AtkMult = -1 },
		func(r *RankRow) { r.DefMult = 0 },
		func(r *RankRow) { r.MdefMult = -0.5 },
	} {
		r := base
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Fatalf("非正倍率應被拒絕：%+v", r)
		}
	}
}

func TestRankRow_Validate_OverUpperBoundRejected(t *testing.T) {
	// 審查 PLAUSIBLE：倍率沒有上限的話，後台誤填超大數字會讓 ScaleMonsterByRank 算出天文數字
	// HP/ATK；1000 對齊 DB NUMERIC(8,3) 欄位量級，超過應在 Validate 就擋下而非等到套用後才發現。
	base := RankRow{Rank: "F", HPMult: 1, AtkMult: 1, DefMult: 1, MdefMult: 1}
	for _, mutate := range []func(*RankRow){
		func(r *RankRow) { r.HPMult = 1001 },
		func(r *RankRow) { r.AtkMult = 1001 },
		func(r *RankRow) { r.DefMult = 1001 },
		func(r *RankRow) { r.MdefMult = 1001 },
	} {
		r := base
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Fatalf("超過 1000 上限的倍率應被拒絕：%+v", r)
		}
	}
	edge := base
	edge.HPMult = 1000
	if err := edge.Validate(); err != nil {
		t.Fatalf("剛好等於 1000 的邊界值不應被拒絕：%v", err)
	}
}

func TestRankRow_Validate_PropagatesSummonError(t *testing.T) {
	r := RankRow{
		Rank: "A", HPMult: 1, AtkMult: 1, DefMult: 1, MdefMult: 1,
		Summon: SummonConfig{Waves: []SummonWave{{AtHpPct: 200, Rank: "E", Count: 1}}},
	}
	if err := r.Validate(); err == nil {
		t.Fatal("summon 設定不合法時 RankRow.Validate 也應報錯")
	}
}

func TestMonsterRow_Validate_RankMustBeOneOfNine(t *testing.T) {
	base := MonsterRow{ID: "m1", Name: "測試怪", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	bad := base
	bad.Rank = "X級"
	if err := bad.Validate(); err == nil {
		t.Fatal("不在九值白名單的 rank 應被拒絕")
	}
	good := base
	good.Rank = "SA"
	if err := good.Validate(); err != nil {
		t.Fatalf("九值之一應通過：%v", err)
	}
}

func validEncounterRowBase() EncounterRow {
	return EncounterRow{
		Code: "c1", Title: "t", SceneID: "s1", SceneKind: "normal", Difficulty: 1, PowerScale: 1,
		MonsterLevel: 10, EscapeChance: 0.35, ScalingMode: "legacy", LevelMode: "fixed",
	}
}

func TestEncounterRow_Validate_LegacyDefaultsPass(t *testing.T) {
	e := validEncounterRowBase()
	if err := e.Validate(); err != nil {
		t.Fatalf("legacy/fixed 且 rank=nil 應通過：%v", err)
	}
}

func TestEncounterRow_Validate_UnknownScalingOrLevelModeRejected(t *testing.T) {
	e := validEncounterRowBase()
	e.ScalingMode = "weird"
	if err := e.Validate(); err == nil {
		t.Fatal("不合法的 scaling_mode 應被拒絕")
	}
	e2 := validEncounterRowBase()
	e2.LevelMode = "weird"
	if err := e2.Validate(); err == nil {
		t.Fatal("不合法的 level_mode 應被拒絕")
	}
}

func TestEncounterRow_Validate_RankModeRequiresValidRank(t *testing.T) {
	e := validEncounterRowBase()
	e.ScalingMode = "rank"
	e.LevelMode = "player"
	if err := e.Validate(); err == nil {
		t.Fatal("scaling_mode=rank 但 rank=nil 應被拒絕")
	}
	bad := "X"
	e.Rank = &bad
	if err := e.Validate(); err == nil {
		t.Fatal("scaling_mode=rank 但 rank 不在九值白名單應被拒絕")
	}
	good := "SS"
	e.Rank = &good
	if err := e.Validate(); err != nil {
		t.Fatalf("合法的 rank 模式應通過：%v", err)
	}
}

func TestEncounterRow_Validate_MonsterCountMustBePositiveWhenSet(t *testing.T) {
	e := validEncounterRowBase()
	bad := 0
	e.MonsterCount = &bad
	if err := e.Validate(); err == nil {
		t.Fatal("monster_count=0 應被拒絕")
	}
}

// ---------------------------------------------------------------------------
// encounterGroup / buildWireEncounterInfo：純函式，不連 DB。
// ---------------------------------------------------------------------------

func TestEncounterGroup(t *testing.T) {
	if g := encounterGroup("legacy"); g != "story" {
		t.Fatalf("legacy 應歸 story，got %s", g)
	}
	if g := encounterGroup("rank"); g != "rank" {
		t.Fatalf("rank 應歸 rank，got %s", g)
	}
}

func TestBuildWireEncounterInfo_LegacyHasNilRankFields(t *testing.T) {
	e := validEncounterRowBase()
	info := buildWireEncounterInfo(e, "img.webp", 10, map[string]RankRow{})
	if info.Group != "story" {
		t.Fatalf("legacy 應歸 story，got %s", info.Group)
	}
	if info.Rank != nil || info.RankLabel != nil || info.BadgeColor != nil {
		t.Fatalf("legacy 場次 rank 相關欄位應維持 nil，got %+v", info)
	}
	if info.ScalingMode != "legacy" || info.LevelMode != "fixed" {
		t.Fatalf("scaling_mode/level_mode 應原樣帶出，got %+v", info)
	}
}

func TestBuildWireEncounterInfo_RankModeFillsLabelAndBadge(t *testing.T) {
	e := validEncounterRowBase()
	e.ScalingMode = "rank"
	e.LevelMode = "player"
	rk := "F"
	e.Rank = &rk
	ranksByID := map[string]RankRow{"F": {Rank: "F", Label: "F級", BadgeColor: "#9e9e9e"}}
	info := buildWireEncounterInfo(e, "img.webp", 27, ranksByID)
	if info.Group != "rank" {
		t.Fatalf("rank 模式應歸 rank 組，got %s", info.Group)
	}
	if info.Rank == nil || *info.Rank != "F" {
		t.Fatalf("rank 應為 F，got %+v", info.Rank)
	}
	if info.RankLabel == nil || *info.RankLabel != "F級" {
		t.Fatalf("rank_label 應查表填入 F級，got %+v", info.RankLabel)
	}
	if info.BadgeColor == nil || *info.BadgeColor != "#9e9e9e" {
		t.Fatalf("badge_color 應查表填入，got %+v", info.BadgeColor)
	}
	if info.MonsterLevel != 27 {
		t.Fatalf("monster_level 應直接採用呼叫端傳入值（level_mode=player 時＝玩家有效等級），got %d", info.MonsterLevel)
	}
}

func TestBuildWireEncounterInfo_RankNotFoundLeavesLabelNil(t *testing.T) {
	e := validEncounterRowBase()
	e.ScalingMode = "rank"
	rk := "F"
	e.Rank = &rk
	info := buildWireEncounterInfo(e, "img.webp", 10, map[string]RankRow{}) // migration 189 未套用/查無資料
	if info.Rank == nil || *info.Rank != "F" {
		t.Fatalf("rank 欄位本身應照樣帶出，got %+v", info.Rank)
	}
	if info.RankLabel != nil || info.BadgeColor != nil {
		t.Fatalf("查無 rank 資料時 label/badge 應維持 nil，got %+v", info)
	}
}

// ---------------------------------------------------------------------------
// migration 189 自我核對（純字串/正則解析，不連 DB，比照 p10_test.go/p12_test.go 既有慣例）。
// ---------------------------------------------------------------------------

func TestMigration189_RankCheckConstraintListsExactlyNineValues(t *testing.T) {
	content := readMigrationFile(t, "189_rpg_p11_ranks.sql")
	re := regexp.MustCompile(`CHECK \(rank IN \(([^)]+)\)\)`)
	m := re.FindStringSubmatch(content)
	if m == nil {
		t.Fatal("找不到 rpg_monster_ranks.rank 的 CHECK (rank IN (...)) 子句")
	}
	got := map[string]bool{}
	for _, part := range strings.Split(m[1], ",") {
		got[strings.Trim(strings.TrimSpace(part), "'")] = true
	}
	if len(got) != len(validMonsterRanks) {
		t.Fatalf("CHECK 應恰好列出九值：got %v want keys of %v", got, validMonsterRanks)
	}
	for want := range validMonsterRanks {
		if !got[want] {
			t.Errorf("CHECK 缺少 %s", want)
		}
	}
}

func TestMigration189_ScalingAndLevelModeChecksPresent(t *testing.T) {
	content := readMigrationFile(t, "189_rpg_p11_ranks.sql")
	if !strings.Contains(content, "CHECK (scaling_mode IN ('legacy','rank'))") {
		t.Error("189 應含 scaling_mode 的 CHECK ('legacy','rank')")
	}
	if !strings.Contains(content, "CHECK (level_mode IN ('fixed','player'))") {
		t.Error("189 應含 level_mode 的 CHECK ('fixed','player')")
	}
}

// encounterSeedRow 189 的 rpg_encounters INSERT 裡一列的關鍵欄位（用於下面兩個測試）。
type encounterSeedRow struct {
	code  string
	rank  string
	count int
}

func parseEncounterSeedRows(t *testing.T, content string) []encounterSeedRow {
	t.Helper()
	re := regexp.MustCompile(`\('(rank_[a-z0-9]+_x\d)',.+'rank',\s*'player',\s*'([A-Z]+)',\s*(\d+)\)`)
	matches := re.FindAllStringSubmatch(content, -1)
	out := make([]encounterSeedRow, 0, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(m[3])
		if err != nil {
			t.Fatalf("monster_count 解析失敗：%v", err)
		}
		out = append(out, encounterSeedRow{code: m[1], rank: m[2], count: n})
	}
	return out
}

func TestMigration189_TwentyEncounterSeedsMatchContract(t *testing.T) {
	content := readMigrationFile(t, "189_rpg_p11_ranks.sql")
	rows := parseEncounterSeedRows(t, content)
	if len(rows) != 20 {
		t.Fatalf("應恰好 20 場強度挑戰，got %d：%+v", len(rows), rows)
	}

	want := map[string][2]any{} // code -> [rank, count]
	for _, rk := range []string{"F", "E", "D", "C", "B"} {
		for _, n := range []int{1, 3, 5} {
			want[fmt.Sprintf("rank_%s_x%d", strings.ToLower(rk), n)] = [2]any{rk, n}
		}
	}
	for _, n := range []int{1, 3} {
		want[fmt.Sprintf("rank_a_x%d", n)] = [2]any{"A", n}
	}
	want["rank_sa_x1"] = [2]any{"SA", 1}
	want["rank_s_x1"] = [2]any{"S", 1}
	want["rank_ss_x1"] = [2]any{"SS", 1}

	if len(want) != 20 {
		t.Fatalf("測試自身的期望表應是 20 筆，got %d（測試邏輯有誤）", len(want))
	}

	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.code] = true
		exp, ok := want[row.code]
		if !ok {
			t.Errorf("非預期的場次代碼：%s", row.code)
			continue
		}
		if row.rank != exp[0] || row.count != exp[1] {
			t.Errorf("%s：want rank=%v count=%v，got rank=%s count=%d", row.code, exp[0], exp[1], row.rank, row.count)
		}
	}
	for code := range want {
		if !seen[code] {
			t.Errorf("缺少預期場次：%s", code)
		}
	}
}

// TestMigration189_EncounterMonstersSlotCaseMatchesMonsterCount CONTRACT §2「×1 front_center；
// ×3 front 三格；×5 全五格」——直接解析 rpg_encounter_monsters 那段 INSERT 的 CASE WHEN
// ARRAY[...] 字面量，核對「槽位數＝monster_count」對映本身正確，且每個槽位名稱都在
// validEncounterSlots 值域內（content.go 既有白名單，同package 直接引用）。
func TestMigration189_EncounterMonstersSlotCaseMatchesMonsterCount(t *testing.T) {
	content := readMigrationFile(t, "189_rpg_p11_ranks.sql")
	re := regexp.MustCompile(`WHEN (\d+) THEN ARRAY\[([^\]]*)\]`)
	matches := re.FindAllStringSubmatch(content, -1)
	if len(matches) != 3 {
		t.Fatalf("應恰好三個 WHEN n THEN ARRAY[...] 分支（1/3/5），got %d：%v", len(matches), matches)
	}
	seenCounts := map[int]bool{}
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("WHEN 後的數字解析失敗：%v", err)
		}
		seenCounts[n] = true
		var slots []string
		for _, part := range strings.Split(m[2], ",") {
			slots = append(slots, strings.Trim(strings.TrimSpace(part), "'"))
		}
		if len(slots) != n {
			t.Errorf("monster_count=%d 應對映 %d 個槽位，got %d：%v", n, n, len(slots), slots)
		}
		seenSlot := map[string]bool{}
		for _, s := range slots {
			if !validEncounterSlots[s] {
				t.Errorf("monster_count=%d 的槽位含不合法值：%s", n, s)
			}
			if seenSlot[s] {
				t.Errorf("monster_count=%d 的槽位重複：%s", n, s)
			}
			seenSlot[s] = true
		}
	}
	for _, want := range []int{1, 3, 5} {
		if !seenCounts[want] {
			t.Errorf("缺少 monster_count=%d 的槽位對映", want)
		}
	}
}

// TestMigration189_NineNewMonsterPosterURLsReuseExistingFive CONTRACT §1：「沿用內容包五張圖」
// ——九隻新分級怪的 poster_url 必須是既有五隻怪物（migration 176 seed）已經在用的其中一個 URL，
// 不能是新路徑（R2/本機都沒有對應的新圖，見 189 檔頭與 battle.go wireEnemy 註解：sprite 由
// 前端從 poster_url 反解資料夾 id，沿用舊圖代表沿用舊圖的四動作戰鬥動畫）。
func TestMigration189_NineNewMonsterPosterURLsReuseExistingFive(t *testing.T) {
	existingContent := readMigrationFile(t, "176_rpg_battle_content.sql")
	urlRe := regexp.MustCompile(`'(/ui/dorpg/mon/[^']+)'`)

	existingURLs := map[string]bool{}
	for _, line := range strings.Split(existingContent, "\n") {
		if !strings.Contains(line, "DOR-MON-") || strings.Contains(line, "DOR-MON-R-") {
			continue
		}
		if m := urlRe.FindStringSubmatch(line); m != nil {
			existingURLs[m[1]] = true
		}
	}
	if len(existingURLs) != 5 {
		t.Fatalf("176 應恰好有 5 隻既有怪物的 poster_url，got %d：%v", len(existingURLs), existingURLs)
	}

	newContent := readMigrationFile(t, "189_rpg_p11_ranks.sql")
	idRe := regexp.MustCompile(`'(DOR-MON-R-[A-Z]+)'`)
	found := 0
	for _, line := range strings.Split(newContent, "\n") {
		if !strings.Contains(line, "'DOR-MON-R-") {
			continue
		}
		idm := idRe.FindStringSubmatch(line)
		urlm := urlRe.FindStringSubmatch(line)
		if idm == nil || urlm == nil {
			continue
		}
		found++
		if !existingURLs[urlm[1]] {
			t.Errorf("%s 的 poster_url=%s 不在既有五隻怪物的圖集內", idm[1], urlm[1])
		}
	}
	if found != 9 {
		t.Fatalf("應找到九隻新分級怪的 poster_url 宣告，got %d", found)
	}
}

func TestMigration189_NineNewMonsterRankFKUsesValidRanks(t *testing.T) {
	content := readMigrationFile(t, "189_rpg_p11_ranks.sql")
	idRe := regexp.MustCompile(`'(DOR-MON-R-([A-Z]+))'`)
	found := map[string]string{}
	for _, m := range idRe.FindAllStringSubmatch(content, -1) {
		found[m[1]] = m[2]
	}
	if len(found) != 9 {
		t.Fatalf("應恰好九隻分級怪，got %d：%v", len(found), found)
	}
	for id, rank := range found {
		if !validMonsterRanks[rank] {
			t.Errorf("%s 的 rank 尾碼 %s 不在九值白名單", id, rank)
		}
		if "DOR-MON-R-"+rank != id {
			t.Errorf("id 與 rank 尾碼不一致：%s vs %s", id, rank)
		}
	}
}

func TestMigration189_SchemaMigrationsInsertPresent(t *testing.T) {
	content := readMigrationFile(t, "189_rpg_p11_ranks.sql")
	if !strings.Contains(content, "INSERT INTO schema_migrations (version) VALUES ('189')") {
		t.Error("189 應在檔尾寫入 schema_migrations")
	}
}

// ---------------------------------------------------------------------------
// 審查修補：buildSummonPool 只該在 scaling_mode="rank" 動作、count 髒資料防呆、
// monster_ids 指定時 Rank/RankLabel/BadgeColor 三欄來源統一。以下測試皆用
// &Handler{}（db=nil）呼叫——只要不觸發 extraRankIDs/extraMonsterIDs 分支（傳入的
// monsterRows/ranksByID 已經齊全）就不會碰到 nil db，比照本檔其餘測試「不連 DB」的既有慣例。
// ---------------------------------------------------------------------------

func TestShouldBuildSummonPool_OnlyRankMode(t *testing.T) {
	if shouldBuildSummonPool(EncounterRow{ScalingMode: "legacy"}) {
		t.Fatal("scaling_mode=legacy 不該建立召喚池")
	}
	if !shouldBuildSummonPool(EncounterRow{ScalingMode: "rank"}) {
		t.Fatal("scaling_mode=rank 應該建立召喚池")
	}
}

func TestBuildSummonPool_LegacyScalingModeReturnsEmptyEvenWhenMonsterRankHasWaves(t *testing.T) {
	// 審查 CONFIRMED：legacy 六場的怪物（例如 taipei101_boss 的 DOR-MON-A）剛好也是
	// rank="A"，而 A 級在 migration 189 確實定義了 summon.waves——若不依 scaling_mode 把關，
	// 這裡就會無中生也出召喚怪，違反契約「既有六場零改動」。
	h := &Handler{}
	cfg := DefaultConfig()
	enc := EncounterRow{
		ScalingMode: "legacy", PowerScale: 1,
		Monsters: []EncounterMonsterRow{{Slot: "boss", MonsterID: "legacy-boss", PowerScale: 1}},
	}
	monsterRows := map[string]MonsterRow{
		"legacy-boss": {ID: "legacy-boss", Rank: "A", Name: "既有六場BOSS", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
	}
	ranksByID := map[string]RankRow{
		"A": {Rank: "A", Label: "A級", BadgeColor: "gold", Summon: SummonConfig{Waves: []SummonWave{
			{AtHpPct: 50, Rank: "A", Count: 1, PowerScale: 1},
		}}},
	}
	pool, err := h.buildSummonPool(context.Background(), cfg, enc, monsterRows, ranksByID, 27)
	if err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if len(pool) != 0 {
		t.Fatalf("legacy 場次不該有召喚池，got %+v", pool)
	}
}

func TestBuildSummonPool_NonPositiveCountSkipped_AboveFiveClamped(t *testing.T) {
	h := &Handler{}
	cfg := DefaultConfig()
	enc := EncounterRow{
		ScalingMode: "rank", PowerScale: 1,
		Monsters: []EncounterMonsterRow{{Slot: "boss", MonsterID: "boss-mon", PowerScale: 1}},
	}
	monsterRows := map[string]MonsterRow{
		"boss-mon":    {ID: "boss-mon", Rank: "A", Name: "強度挑戰BOSS", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
		"DOR-MON-R-B": {ID: "DOR-MON-R-B", Rank: "B", Name: "B級分級怪", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
	}
	ranksByID := map[string]RankRow{
		"A": {Rank: "A", Label: "A級", BadgeColor: "gold", Summon: SummonConfig{Waves: []SummonWave{
			{AtHpPct: 50, Rank: "B", Count: 0, PowerScale: 1}, // 髒資料：count<=0，整波應略過而非 panic
			{AtHpPct: 30, Rank: "B", Count: 7, PowerScale: 1}, // 髒資料：count>5，應夾回 5
		}}},
		"B": {Rank: "B", Label: "B級", BadgeColor: "silver"},
	}
	pool, err := h.buildSummonPool(context.Background(), cfg, enc, monsterRows, ranksByID, 27)
	if err != nil {
		t.Fatalf("不應報錯（也不該 panic）：%v", err)
	}
	if len(pool) != 1 {
		t.Fatalf("count<=0 的波次應完全略過，只剩一波：got %d 波", len(pool))
	}
	if pool[0].AtHpPct != 30 {
		t.Fatalf("剩下的波次應是 at_hp_pct=30 那筆，got %d", pool[0].AtHpPct)
	}
	if len(pool[0].Enemies) != 5 {
		t.Fatalf("count=7 應被夾到 5，got %d 隻", len(pool[0].Enemies))
	}
}

func TestBuildSummonPool_MonsterIDsRankUnifiedToWaveRank(t *testing.T) {
	// 審查 CONFIRMED：monster_ids 指定的怪物自己的 rank（這裡故意設成 "F"）可能跟
	// wave.Rank（"A"）不同，強度計算已經照 waveRank 算，Rank/RankLabel/BadgeColor 三欄
	// 也該統一用 waveRank，不能讓 Rank 欄位獨自沿用 monRow.Rank 造成前端徽章跟數值對不上。
	h := &Handler{}
	cfg := DefaultConfig()
	enc := EncounterRow{
		ScalingMode: "rank", PowerScale: 1,
		Monsters: []EncounterMonsterRow{{Slot: "boss", MonsterID: "boss-mon2", PowerScale: 1}},
	}
	monsterRows := map[string]MonsterRow{
		"boss-mon2":    {ID: "boss-mon2", Rank: "S", Name: "強度挑戰BOSS2", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
		"specific-mon": {ID: "specific-mon", Rank: "F", Name: "指定怪", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
	}
	ranksByID := map[string]RankRow{
		"S": {Rank: "S", Label: "S級", BadgeColor: "purple", Summon: SummonConfig{Waves: []SummonWave{
			{AtHpPct: 50, Rank: "A", Count: 1, MonsterIDs: []string{"specific-mon"}, PowerScale: 1},
		}}},
		"A": {Rank: "A", Label: "A級", BadgeColor: "gold"},
	}
	pool, err := h.buildSummonPool(context.Background(), cfg, enc, monsterRows, ranksByID, 27)
	if err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if len(pool) != 1 || len(pool[0].Enemies) != 1 {
		t.Fatalf("應恰好一波一隻：got %+v", pool)
	}
	got := pool[0].Enemies[0]
	if got.Rank != "A" || got.RankLabel != "A級" || got.BadgeColor != "gold" {
		t.Fatalf("Rank/RankLabel/BadgeColor 應統一來自 waveRank(A)，not monRow.Rank(F)：got rank=%q label=%q badge=%q",
			got.Rank, got.RankLabel, got.BadgeColor)
	}
}

// TestBuildSummonPool_PowerScaleMultipliesEncounterScale P11 修正（2026-09-20）：召喚怪整體
// 強度折減——buildSummonPool 算召喚怪數值時，encounterScale 應該是 enc.PowerScale ×
// wave.PowerScale（不是只有 enc.PowerScale），直接對照 ScaleMonsterByRank 鏡像算出的期望值，
// 而不是只斷言「數值變小了」這種弱斷言，確保乘法真的乘在對的位置上。
func TestBuildSummonPool_PowerScaleMultipliesEncounterScale(t *testing.T) {
	h := &Handler{}
	cfg := DefaultConfig()
	enc := EncounterRow{
		ScalingMode: "rank", PowerScale: 2, // 刻意不是 1，確保兩個 PowerScale 真的相乘而不是互相覆蓋
		Monsters: []EncounterMonsterRow{{Slot: "boss", MonsterID: "boss-mon3", PowerScale: 1}},
	}
	monsterRows := map[string]MonsterRow{
		"boss-mon3":   {ID: "boss-mon3", Rank: "A", Name: "強度挑戰BOSS3", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
		"DOR-MON-R-E": {ID: "DOR-MON-R-E", Rank: "E", Name: "E級分級怪", HPMult: 2, AtkMult: 3, DefMult: 0.5, SpeedMult: 1},
	}
	eRank := RankRow{Rank: "E", Label: "E級", BadgeColor: "green", HPMult: 1.5, AtkMult: 2, DefMult: 0.6, MdefMult: 0.6}
	ranksByID := map[string]RankRow{
		"A": {Rank: "A", Label: "A級", BadgeColor: "gold", Summon: SummonConfig{Waves: []SummonWave{
			{AtHpPct: 60, Rank: "E", Count: 1, PowerScale: 0.5},
		}}},
		"E": eRank,
	}
	pool, err := h.buildSummonPool(context.Background(), cfg, enc, monsterRows, ranksByID, 27)
	if err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if len(pool) != 1 || len(pool[0].Enemies) != 1 {
		t.Fatalf("應恰好一波一隻：got %+v", pool)
	}
	want := ScaleMonsterByRank(cfg, monsterRows["DOR-MON-R-E"], eRank, enc.PowerScale*0.5, 1.0, 27)
	got := pool[0].Enemies[0]
	if got.HPMax != want.HPMax {
		t.Fatalf("HPMax 應是 enc.PowerScale(2)×wave.PowerScale(0.5) 算出的 encounterScale=1.0：want %d got %d", want.HPMax, got.HPMax)
	}
	if got.Stats == nil || int(got.Stats.Atk) != want.Atk {
		t.Fatalf("Atk 應同樣套用 encounterScale=enc.PowerScale×wave.PowerScale：want %d got %+v", want.Atk, got.Stats)
	}

	// 對照組：wave.PowerScale=1（不折減）應該只等於 enc.PowerScale 本身，數值明顯比上面 0.5 那組大，
	// 證明真的是「相乘」而不是「wave.PowerScale 蓋掉 enc.PowerScale」或反過來。
	ranksByID["A"] = RankRow{Rank: "A", Label: "A級", BadgeColor: "gold", Summon: SummonConfig{Waves: []SummonWave{
		{AtHpPct: 60, Rank: "E", Count: 1, PowerScale: 1},
	}}}
	poolFull, err := h.buildSummonPool(context.Background(), cfg, enc, monsterRows, ranksByID, 27)
	if err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	wantFull := ScaleMonsterByRank(cfg, monsterRows["DOR-MON-R-E"], eRank, enc.PowerScale*1.0, 1.0, 27)
	if poolFull[0].Enemies[0].HPMax != wantFull.HPMax {
		t.Fatalf("power_scale=1 這組 HPMax 應對照 encounterScale=enc.PowerScale(2)：want %d got %d", wantFull.HPMax, poolFull[0].Enemies[0].HPMax)
	}
	if wantFull.HPMax <= want.HPMax {
		t.Fatalf("power_scale=1 應該比 power_scale=0.5 折減更少（HP 更高）：full=%d half=%d", wantFull.HPMax, want.HPMax)
	}
}

// TestBuildSummonPool_MultipleWavesSameAtHpPct_EachKeepsOwnPowerScale CONTRACT §1「一波可多筆」
// 合併成同一個 wireSummonWave 時（見 buildSummonPool 檔頭註解），每一筆原始 SummonWave 必須保留
// 自己的 power_scale，不能被合併群組共用同一個折減值——否則後台校準第二筆波次的 power_scale 會
// 悄悄影響到第一筆波次的怪物數值。
func TestBuildSummonPool_MultipleWavesSameAtHpPct_EachKeepsOwnPowerScale(t *testing.T) {
	h := &Handler{}
	cfg := DefaultConfig()
	enc := EncounterRow{
		ScalingMode: "rank", PowerScale: 1,
		Monsters: []EncounterMonsterRow{{Slot: "boss", MonsterID: "boss-mon4", PowerScale: 1}},
	}
	monsterRows := map[string]MonsterRow{
		"boss-mon4":   {ID: "boss-mon4", Rank: "SS", Name: "強度挑戰BOSS4", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
		"DOR-MON-R-B": {ID: "DOR-MON-R-B", Rank: "B", Name: "B級分級怪", HPMult: 2, AtkMult: 2, DefMult: 0.5, SpeedMult: 1},
	}
	bRank := RankRow{Rank: "B", Label: "B級", BadgeColor: "silver", HPMult: 1.2, AtkMult: 1.5, DefMult: 0.7, MdefMult: 0.7}
	ranksByID := map[string]RankRow{
		"SS": {Rank: "SS", Label: "特S級", BadgeColor: "black", Summon: SummonConfig{Waves: []SummonWave{
			{AtHpPct: 20, Rank: "B", Count: 1, PowerScale: 1.0},  // 第一筆：不折減
			{AtHpPct: 20, Rank: "B", Count: 1, PowerScale: 0.25}, // 第二筆：同 at_hp_pct，強折減
		}}},
		"B": bRank,
	}
	pool, err := h.buildSummonPool(context.Background(), cfg, enc, monsterRows, ranksByID, 27)
	if err != nil {
		t.Fatalf("不應報錯：%v", err)
	}
	if len(pool) != 1 {
		t.Fatalf("同一 at_hp_pct 的兩筆 wave 應合併成一個 wireSummonWave：got %d 波", len(pool))
	}
	if len(pool[0].Enemies) != 2 {
		t.Fatalf("合併後應有兩隻怪（各自的 count=1）：got %d 隻", len(pool[0].Enemies))
	}
	wantFull := ScaleMonsterByRank(cfg, monsterRows["DOR-MON-R-B"], bRank, 1.0*1.0, 1.0, 27)
	wantQuarter := ScaleMonsterByRank(cfg, monsterRows["DOR-MON-R-B"], bRank, 1.0*0.25, 1.0, 27)
	if pool[0].Enemies[0].HPMax != wantFull.HPMax {
		t.Fatalf("第一隻（power_scale=1.0）HPMax 不應被第二筆的 power_scale 影響：want %d got %d", wantFull.HPMax, pool[0].Enemies[0].HPMax)
	}
	if pool[0].Enemies[1].HPMax != wantQuarter.HPMax {
		t.Fatalf("第二隻（power_scale=0.25）HPMax 應套用自己的折減：want %d got %d", wantQuarter.HPMax, pool[0].Enemies[1].HPMax)
	}
	if pool[0].Enemies[0].HPMax <= pool[0].Enemies[1].HPMax {
		t.Fatalf("power_scale=1.0 那隻應該明顯比 power_scale=0.25 那隻血更多：got %d vs %d", pool[0].Enemies[0].HPMax, pool[0].Enemies[1].HPMax)
	}
}
