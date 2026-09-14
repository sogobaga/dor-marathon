// scaling_test.go：契約 §3.2 要求至少 12 案例，逐條核對 D2 公式（見 scaling.go 檔頭）。
package rpg

import (
	"math"
	"testing"
)

func approxEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// --- PlayerBattleStatsFrom：保底生效 ---

func TestPlayerBattleStatsFrom_AtkFloorApplies(t *testing.T) {
	cfg := DefaultConfig() // BattlePlayerMinAtk=30
	d := Derived{Atk: 5, Matk: 10, Def: 2, Mdef: 2, MaxHP: 100, MaxMP: 50}
	p := PlayerBattleStatsFrom(cfg, 20, d)
	if p.Atk != cfg.BattlePlayerMinAtk {
		t.Fatalf("Atk floor 未生效：got %v want %v", p.Atk, cfg.BattlePlayerMinAtk)
	}
}

func TestPlayerBattleStatsFrom_HPFloorApplies(t *testing.T) {
	cfg := DefaultConfig() // BattlePlayerMinHP=300
	d := Derived{Atk: 50, MaxHP: 120, MaxMP: 50}
	p := PlayerBattleStatsFrom(cfg, 20, d)
	if p.HPMax != cfg.BattlePlayerMinHP {
		t.Fatalf("HPMax floor 未生效：got %v want %v", p.HPMax, cfg.BattlePlayerMinHP)
	}
}

func TestPlayerBattleStatsFrom_NoFloorWhenAboveMinimum(t *testing.T) {
	cfg := DefaultConfig()
	d := Derived{Atk: 135, Matk: 10, Def: 35, Mdef: 10, MaxHP: 820, MaxMP: 100}
	p := PlayerBattleStatsFrom(cfg, 50, d)
	if p.Atk != 135 || p.HPMax != 820 {
		t.Fatalf("高於保底時不該被覆蓋：got Atk=%v HPMax=%v", p.Atk, p.HPMax)
	}
	if p.Def != 35 || p.Matk != 10 || p.Mdef != 10 || p.MPMax != 100 {
		t.Fatalf("Def/Matk/Mdef/MPMax 不該被保底邏輯動到：%+v", p)
	}
}

// --- ScaleMonster：一般怪 hits 目標成立（打死一般怪的次數落在 battle_mob_hits ± 1）---

func TestScaleMonster_HitsWithinTargetForNormalMonster(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	sm := ScaleMonster(cfg, p, m, 1, 1, 1)

	mobDef := p.Atk * cfg.BattleMobDefRatio * m.DefMult * 1
	hitDamage := p.Atk - mobDef
	hits := float64(sm.HPMax) / hitDamage
	if !approxEqual(hits, cfg.BattleMobHits, 1.0) {
		t.Fatalf("hits=%.2f 超出 battle_mob_hits(%.2f) ± 1", hits, cfg.BattleMobHits)
	}
}

// --- ScaleMonster：boss 顯示等級 +5 ---

func TestScaleMonster_BossLevelPlus5(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	boss := MonsterRow{ID: "boss", HPMult: 7, AtkMult: 1.6, DefMult: 1.5, SpeedMult: 1.1, IsBoss: true}
	sm := ScaleMonster(cfg, p, boss, 1.6, 1, 1)
	if sm.Level != 55 {
		t.Fatalf("boss 顯示等級應為玩家 Base Lv+5=55，got %d", sm.Level)
	}
	normal := MonsterRow{ID: "n", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	smN := ScaleMonster(cfg, p, normal, 1, 1, 1)
	if smN.Level != 50 {
		t.Fatalf("一般怪顯示等級應等於玩家 Base Lv=50，got %d", smN.Level)
	}
}

// --- ScaleMonster：encounterScale 與 slotScale 相乘（只影響 HP，不影響 Def/Atk） ---

func TestScaleMonster_EncounterAndSlotScaleMultiplyHP(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}

	base := ScaleMonster(cfg, p, m, 1, 1, 1)
	scaled := ScaleMonster(cfg, p, m, 1, 3, 1) // slotScale=3，其餘不變

	gotRatio := float64(scaled.HPMax) / float64(base.HPMax)
	if !approxEqual(gotRatio, 3, 0.05) {
		t.Fatalf("slotScale=3 應讓 HP 約略乘 3，got ratio=%.3f (base=%d scaled=%d)", gotRatio, base.HPMax, scaled.HPMax)
	}
	// slotScale 契約明講「只乘進 HP」，Def/Atk 不受 slotScale 影響。
	if scaled.Def != base.Def {
		t.Fatalf("slotScale 不應影響 Def：base=%d scaled=%d", base.Def, scaled.Def)
	}
}

func TestScaleMonster_EncounterScaleRaisesDefAndHP(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}

	low := ScaleMonster(cfg, p, m, 0.5, 1, 1)
	high := ScaleMonster(cfg, p, m, 1.5, 1, 1)
	if high.Def <= low.Def {
		t.Fatalf("encounterScale 提高時 Def 應該提高：low=%d high=%d", low.Def, high.Def)
	}
	if high.HPMax <= low.HPMax {
		t.Fatalf("encounterScale 提高時 HP 應該提高：low=%d high=%d", low.HPMax, high.HPMax)
	}
}

// --- ScaleMonster：speed_mult 影響 act 間隔 ---

func TestScaleMonster_SpeedMultAffectsActInterval(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	slow := MonsterRow{ID: "slow", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	fast := MonsterRow{ID: "fast", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 0.5} // <1 更頻繁

	smSlow := ScaleMonster(cfg, p, slow, 1, 1, 1)
	smFast := ScaleMonster(cfg, p, fast, 1, 1, 1)
	if smFast.ActMinMs >= smSlow.ActMinMs || smFast.ActMaxMs >= smSlow.ActMaxMs {
		t.Fatalf("speed_mult=0.5 應該讓行動間隔更短：slow=[%d,%d] fast=[%d,%d]",
			smSlow.ActMinMs, smSlow.ActMaxMs, smFast.ActMinMs, smFast.ActMaxMs)
	}
	wantMin := int(math.Round(float64(cfg.BattleEnemyActMinMs) * 0.5))
	if smFast.ActMinMs != wantMin {
		t.Fatalf("ActMinMs 應為 enemy_act_min_ms×speed_mult=%d，got %d", wantMin, smFast.ActMinMs)
	}
}

// --- ScaleMonster：行動間隔下限防呆（speed_mult 髒資料為 0 或負值不炸機） ---

func TestScaleMonster_ActIntervalFloorGuardsBadSpeedMult(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	bad := MonsterRow{ID: "bad", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 0}
	sm := ScaleMonster(cfg, p, bad, 1, 1, 1)
	if sm.ActMinMs < minActIntervalMs || sm.ActMaxMs < minActIntervalMs {
		t.Fatalf("speed_mult=0 應被夾住在 minActIntervalMs=%d 以上，got [%d,%d]", minActIntervalMs, sm.ActMinMs, sm.ActMaxMs)
	}
	badNeg := MonsterRow{ID: "badneg", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: -1}
	smNeg := ScaleMonster(cfg, p, badNeg, 1, 1, 1)
	if smNeg.ActMinMs < minActIntervalMs || smNeg.ActMaxMs < minActIntervalMs {
		t.Fatalf("speed_mult=-1 應被夾住在 minActIntervalMs=%d 以上，got [%d,%d]", minActIntervalMs, smNeg.ActMinMs, smNeg.ActMaxMs)
	}
}

// --- ScaleMonster：mobDef 極端高時 hitDamage 仍保底為 1（不會打不死） ---

func TestScaleMonster_HitDamageFloorsAtOne(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 10, Def: 5, HPMax: 300, BaseLevel: 1}
	// def_mult 設超大，讓 mobDef 遠超過 playerAtk。
	m := MonsterRow{ID: "tanky", HPMult: 1, AtkMult: 1, DefMult: 1000, SpeedMult: 1}
	sm := ScaleMonster(cfg, p, m, 1, 1, 1)
	if sm.HPMax <= 0 {
		t.Fatalf("hitDamage 保底為 1 時 HP 仍應為正數（可被打死），got %d", sm.HPMax)
	}
	// 用保底 1 反推：HP 應等於 round(1 × battle_mob_hits × hp_mult × scale × slotScale)。
	want := int(math.Round(1 * cfg.BattleMobHits))
	if sm.HPMax != want {
		t.Fatalf("hitDamage 保底未生效：want HPMax=%d got %d", want, sm.HPMax)
	}
}

// --- mobMatk == mobAtk、mobMdef == mobDef（P2 敵人不分物魔） ---

func TestScaleMonster_MatkEqualsAtkAndMdefEqualsDef(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	m := MonsterRow{ID: "m1", HPMult: 1.2, AtkMult: 1.1, DefMult: 1.3, SpeedMult: 0.9}
	sm := ScaleMonster(cfg, p, m, 1.2, 1, 0.4)
	if sm.Matk != sm.Atk {
		t.Fatalf("P2 敵人不分物魔：Matk(%d) 應等於 Atk(%d)", sm.Matk, sm.Atk)
	}
	if sm.Mdef != sm.Def {
		t.Fatalf("P2 敵人不分物魔：Mdef(%d) 應等於 Def(%d)", sm.Mdef, sm.Def)
	}
}

// --- AtkMultShares：除以 0 防護 ---

func TestAtkMultShares_ZeroSumFallsBackToEqualSplit(t *testing.T) {
	shares := AtkMultShares([]float64{0, 0, 0})
	if len(shares) != 3 {
		t.Fatalf("長度應保留 3，got %d", len(shares))
	}
	for i, s := range shares {
		if !approxEqual(s, 1.0/3.0, 1e-9) {
			t.Fatalf("shares[%d]=%v 應退化為平均分配 1/3", i, s)
		}
	}
}

func TestAtkMultShares_EmptyInputNoPanic(t *testing.T) {
	shares := AtkMultShares(nil)
	if len(shares) != 0 {
		t.Fatalf("空輸入應回空陣列，got %v", shares)
	}
}

func TestAtkMultShares_NormalCaseSumsToOne(t *testing.T) {
	shares := AtkMultShares([]float64{1, 1, 2})
	want := []float64{0.25, 0.25, 0.5}
	sum := 0.0
	for i, s := range shares {
		if !approxEqual(s, want[i], 1e-9) {
			t.Fatalf("shares[%d]=%v want %v", i, s, want[i])
		}
		sum += s
	}
	if !approxEqual(sum, 1, 1e-9) {
		t.Fatalf("份額總和應為 1，got %v", sum)
	}
}

// --- 全場 DPS 合計落在目標 ±15%（隔離 playerDef 為 0，單純驗證 enemyShare×actSec 的分配邏輯，
// 契約 D2 的目標公式本身就只涵蓋「playerHp × battle_enemy_dps_ratio」這一項，playerDef 是額外
// 疊加的基礎值，交給 BALANCE 用真實怪物調參數時一併考量）。---

func TestScaleMonster_AggregateDPSNearTarget(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 0, HPMax: 820, BaseLevel: 50}
	monsters := []MonsterRow{
		{ID: "a", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
		{ID: "b", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
		{ID: "c", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1},
	}
	atkMults := make([]float64, len(monsters))
	for i, m := range monsters {
		atkMults[i] = m.AtkMult
	}
	shares := AtkMultShares(atkMults)

	totalDPS := 0.0
	for i, m := range monsters {
		sm := ScaleMonster(cfg, p, m, 1, 1, shares[i])
		actSec := (float64(cfg.BattleEnemyActMinMs+cfg.BattleEnemyActMaxMs) / 2000) * m.SpeedMult
		totalDPS += float64(sm.Atk) / actSec
	}
	target := p.HPMax * cfg.BattleEnemyDPSRatio
	tol := target * 0.15
	if !approxEqual(totalDPS, target, tol) {
		t.Fatalf("全場 DPS 合計 %.4f 超出目標 %.4f 的 ±15%%（容差 %.4f）", totalDPS, target, tol)
	}
}

// --- ScaleCompanion：倍率直接乘在玩家數值上 ---

func TestScaleCompanion_MultipliesPlayerStats(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 100, Matk: 20, Def: 30, Mdef: 15, HPMax: 800, MPMax: 200, BaseLevel: 50}
	// 比照契約 seed 草案：char_xiaomi（治療 staff）matk_mult 1.2、hp_mult 0.7。
	xiaomi := CompanionRow{ID: "char_xiaomi", HPMult: 0.7, MPMult: 1, AtkMult: 1, MatkMult: 1.2, DefMult: 1, MdefMult: 1}
	stats := ScaleCompanion(cfg, p, xiaomi)
	if !approxEqual(stats.HPMax, 560, 1e-9) {
		t.Fatalf("HPMax want 560 got %v", stats.HPMax)
	}
	if !approxEqual(stats.Matk, 24, 1e-9) {
		t.Fatalf("Matk want 24 got %v", stats.Matk)
	}
	if !approxEqual(stats.Atk, 100, 1e-9) || !approxEqual(stats.Def, 30, 1e-9) || !approxEqual(stats.Mdef, 15, 1e-9) {
		t.Fatalf("倍率=1 的欄位不該被改動：%+v", stats)
	}
}

// --- PlayerPower：顯示戰力公式 ---

func TestPlayerPower_Formula(t *testing.T) {
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820}
	got := PlayerPower(p)
	want := int(math.Round(135 + 35 + 820.0/10))
	if got != want {
		t.Fatalf("PlayerPower want %d got %d", want, got)
	}
}

func TestPlayerPower_FloorAppliedStats(t *testing.T) {
	cfg := DefaultConfig()
	d := Derived{Atk: 5, Def: 2, MaxHP: 100, MaxMP: 50}
	p := PlayerBattleStatsFrom(cfg, 1, d)
	got := PlayerPower(p)
	want := int(math.Round(cfg.BattlePlayerMinAtk + 2 + cfg.BattlePlayerMinHP/10))
	if got != want {
		t.Fatalf("套保底後的 PlayerPower want %d got %d", want, got)
	}
}

// =============================================================================
// ScaleSkill / ScaleItem：P2 修正第 1 輪「參考 HP 縮放」（根因見 config.go
// BattleReferenceHP/MP 欄位註解、審查 data.md 缺陷1）。DefaultConfig() 的
// battle_reference_hp=300、battle_reference_mp=100，以下測試皆以此為基準推算 ratio。
// =============================================================================

// --- heal/shield flat 隨 ratioHP 縮放 ---

func TestScaleSkill_HealFlatScalesWithReferenceRatio(t *testing.T) {
	cfg := DefaultConfig() // battle_reference_hp=300
	p := PlayerBattleStats{HPMax: 900}
	heal := SkillRow{ID: "heal", Kind: "heal", Flat: 80, Coefficient: 1.5, MPCost: 10, CooldownMs: 4000, CastMs: 300}
	out := ScaleSkill(cfg, p, heal)
	// ratioHP = 900/300 = 3 → flat = round(80*3) = 240
	if out.Flat != 240 {
		t.Fatalf("heal flat 應縮放為 240，got %d", out.Flat)
	}
	// coefficient/mp_cost/cooldown_ms/cast_ms 契約明講不動。
	if out.Coefficient != heal.Coefficient || out.MPCost != heal.MPCost || out.CooldownMs != heal.CooldownMs || out.CastMs != heal.CastMs {
		t.Fatalf("coefficient/mp_cost/cooldown_ms/cast_ms 不該被 ScaleSkill 動到：%+v", out)
	}
}

func TestScaleSkill_ShieldFlatScalesWithReferenceRatio(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{HPMax: 150} // ratioHP = 150/300 = 0.5
	shield := SkillRow{ID: "shield", Kind: "shield", Flat: 50}
	out := ScaleSkill(cfg, p, shield)
	if out.Flat != 25 {
		t.Fatalf("shield flat 應縮放為 25，got %d", out.Flat)
	}
}

// --- damage 完全不動（契約明講：flat 是加在玩家 ATK 上，ATK 已有自己的保底/縮放）---

func TestScaleSkill_DamageKindUnchanged(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{HPMax: 3000} // ratioHP 若套用會是 10（clamp 前），刻意選一個會被 clamp 影響的值以確認完全沒套用
	dmg := SkillRow{ID: "slash", Kind: "damage", Flat: 20, Coefficient: 1.2}
	out := ScaleSkill(cfg, p, dmg)
	if out.Flat != 20 {
		t.Fatalf("kind=damage 的 flat 不應被縮放，got %d", out.Flat)
	}
	if out != dmg {
		t.Fatalf("kind=damage 應原樣回傳，got %+v want %+v", out, dmg)
	}
}

// --- ScaleItem：hp/mp/revive 三種道具 ---

func TestScaleItem_HPKindScalesWithReferenceRatio(t *testing.T) {
	cfg := DefaultConfig() // battle_reference_hp=300
	p := PlayerBattleStats{HPMax: 900}
	hp := ItemRow{ID: "hp_potion", Kind: "hp", Amount: 150}
	out := ScaleItem(cfg, p, hp)
	if out.Amount != 450 { // round(150*3)
		t.Fatalf("hp 道具 amount 應縮放為 450，got %d", out.Amount)
	}
}

func TestScaleItem_MPKindScalesWithReferenceRatio(t *testing.T) {
	cfg := DefaultConfig() // battle_reference_mp=100
	p := PlayerBattleStats{MPMax: 50}
	mp := ItemRow{ID: "mp_potion", Kind: "mp", Amount: 80}
	out := ScaleItem(cfg, p, mp)
	if out.Amount != 40 { // round(80*0.5)
		t.Fatalf("mp 道具 amount 應縮放為 40，got %d", out.Amount)
	}
}

func TestScaleItem_ReviveKindUnchanged(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{HPMax: 9999} // 刻意用極端 HPMax 確認 revive 真的完全不受影響
	revive := ItemRow{ID: "revive_feather", Kind: "revive", Amount: 50}
	out := ScaleItem(cfg, p, revive)
	if out.Amount != 50 {
		t.Fatalf("revive 的 amount 是百分比，不該被縮放，want 50 got %d", out.Amount)
	}
}

// --- clamp 上下限（0.25~8 倍） ---

func TestScaleSkill_ClampUpperBound(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{HPMax: 100000} // 900/300=333x 遠超過上限，應被夾在 8
	heal := SkillRow{Kind: "heal", Flat: 10}
	out := ScaleSkill(cfg, p, heal)
	if out.Flat != 80 { // round(10*8)
		t.Fatalf("ratio 應被夾在上限 8，want flat=80 got %d", out.Flat)
	}
}

func TestScaleItem_ClampLowerBoundNeverGoesToZero(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{HPMax: 1} // ratio 遠低於下限，應被夾在 0.25
	hp := ItemRow{Kind: "hp", Amount: 1}
	out := ScaleItem(cfg, p, hp)
	// round(1*0.25)=0，但 ScaleItem 保底 max(1,...)：全新角色不該被縮放到補不了血。
	if out.Amount != 1 {
		t.Fatalf("clamp 下限 + 保底 1 應讓 amount 至少為 1，got %d", out.Amount)
	}
}

// --- ratio=1 時完全不變（HPMax/MPMax 剛好等於參考值） ---

func TestScaleSkill_RatioOneLeavesFlatUnchanged(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{HPMax: cfg.BattleReferenceHP} // ratioHP 恰好 = 1
	heal := SkillRow{Kind: "heal", Flat: 80}
	out := ScaleSkill(cfg, p, heal)
	if out.Flat != 80 {
		t.Fatalf("ratio=1 時 flat 不該改變，want 80 got %d", out.Flat)
	}
}

func TestScaleItem_RatioOneLeavesAmountUnchanged(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{HPMax: cfg.BattleReferenceHP, MPMax: cfg.BattleReferenceMP}
	hp := ItemRow{Kind: "hp", Amount: 150}
	mp := ItemRow{Kind: "mp", Amount: 60}
	if out := ScaleItem(cfg, p, hp); out.Amount != 150 {
		t.Fatalf("ratioHP=1 時 hp amount 不該改變，want 150 got %d", out.Amount)
	}
	if out := ScaleItem(cfg, p, mp); out.Amount != 60 {
		t.Fatalf("ratioMP=1 時 mp amount 不該改變，want 60 got %d", out.Amount)
	}
}
