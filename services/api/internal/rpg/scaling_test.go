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

// =============================================================================
// MonsterRating / CompanionRating / PlayerBattleStatsFrom.Rating / ScaleMonster.Rating：
// P2 修正第 2 輪（暴擊／Miss／無效攻擊上線，SPEC §1）＋第 3 輪（等級基線／AGI 攻速／DEX 詠唱
// 縮減，審查 data.md 缺陷1/2 CONFIRMED 修復）。≥8 案例逐條核對 CombatRating 推導。
//
// 第 3 輪語意改變：MonsterRating 簽名新增 PlayerBattleStats 參數，Hit/Flee 不再是純 config
// 常數，改成「baseLv×per_level + base」（Flee 另乘 speed_mult）。以下測試凡是隻想驗證
// speed_mult/def_mult 這類「與等級無關」的行為時，一律把 p.BaseLevel 設為 0，讓等級項歸零、
// 維持測試原本只驗證單一變數的意圖；驗證等級基線本身的行為則獨立成新案例。
// =============================================================================

// --- MonsterRating：BaseLevel=0 時退化成純 config 基準值（隔離等級項，驗證 speed_mult=1、
// def_mult=1 時 Hit/Flee 恰好等於 base）---

func TestMonsterRating_BaselineWhenLevelZeroAndMultipliersAreOne(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{BaseLevel: 0}
	m := MonsterRow{ID: "m1", SpeedMult: 1, DefMult: 1}
	r := MonsterRating(cfg, p, m)
	if r.Hit != cfg.BattleMonsterHitBase {
		t.Fatalf("Hit want %v got %v", cfg.BattleMonsterHitBase, r.Hit)
	}
	if r.Flee != cfg.BattleMonsterFleeBase {
		t.Fatalf("Flee want %v got %v", cfg.BattleMonsterFleeBase, r.Flee)
	}
	if r.CritPct != cfg.BattleMonsterCritPct {
		t.Fatalf("CritPct want %v got %v", cfg.BattleMonsterCritPct, r.CritPct)
	}
	if r.CritShield != cfg.BattleMonsterCritShieldBase {
		t.Fatalf("CritShield want %v got %v", cfg.BattleMonsterCritShieldBase, r.CritShield)
	}
}

// --- MonsterRating：等級基線對齊——不特別投資 AGI/DEX（維持在 base 偏移量對應的基準值 2）時，
// 玩家自己算出的 Hit/Flee（Compute()）恰好等於怪物的 Flee/Hit，讓 missChance 落在下限
// （審查 data.md 缺陷1/2 CONFIRMED 的核心驗收：兩邊等級基線必須對齊）。 ---

func TestMonsterRating_LevelBaselineMatchesUninvestedPlayerHitFlee(t *testing.T) {
	cfg := DefaultConfig() // HitPerLevel=FleePerLevel=1、HitBase=FleeBase=2，對齊 LvHit=LvFlee=1
	const baseLv = 27
	// Dex=2/Agi=2：SPEC 效果驗算範例的基準點（Lv27 AGI 2 → flee 29 = monsterHit 29）。
	d := Compute(cfg, ComputeInput{BaseLevel: baseLv, Stats: Stats{Agi: 2, Dex: 2}})
	p := PlayerBattleStats{BaseLevel: baseLv}
	m := MonsterRow{ID: "m1", SpeedMult: 1}
	r := MonsterRating(cfg, p, m)
	if !approxEqual(d.Flee, r.Hit, 1e-9) {
		t.Fatalf("不投資時玩家 Flee(%v) 應等於怪物 Hit(%v)（打平，missChance 落在下限）", d.Flee, r.Hit)
	}
	if !approxEqual(d.Hit, r.Flee, 1e-9) {
		t.Fatalf("不投資時玩家 Hit(%v) 應等於怪物 Flee(%v)（打平）", d.Hit, r.Flee)
	}
	if d.Flee != 29 || r.Hit != 29 {
		t.Fatalf("SPEC 效果驗算基準點應為 29，got playerFlee=%v monsterHit=%v", d.Flee, r.Hit)
	}
}

// --- MonsterRating：投資 AGI 後，玩家 Flee 與怪物 Hit 的差距真的被拉開（缺陷1 的直接驗收：
// AGI 配到越多，missChance 該用的差額越大，不會恆卡在下限） ---

func TestMonsterRating_InvestingAgiWidensFleeVsMonsterHitGap(t *testing.T) {
	cfg := DefaultConfig()
	const baseLv = 27
	p := PlayerBattleStats{BaseLevel: baseLv}
	m := MonsterRow{ID: "m1", SpeedMult: 1}
	monsterHit := MonsterRating(cfg, p, m).Hit

	low := Compute(cfg, ComputeInput{BaseLevel: baseLv, Stats: Stats{Agi: 2}})
	high := Compute(cfg, ComputeInput{BaseLevel: baseLv, Stats: Stats{Agi: 40}})

	gapLow := low.Flee - monsterHit
	gapHigh := high.Flee - monsterHit
	if gapLow > 0.01 {
		t.Fatalf("AGI=2（基準點）時差距應約為 0，got %v", gapLow)
	}
	if gapHigh <= gapLow {
		t.Fatalf("AGI=40 應讓差距比 AGI=2 更大：gapLow=%v gapHigh=%v", gapLow, gapHigh)
	}
	if gapHigh < 30 { // AGI 40 → flee 67，67-29=38，留寬鬆容差防止公式微調就誤報
		t.Fatalf("AGI=40 差距(%v)應顯著為正，AGI 配點才有意義", gapHigh)
	}
}

// --- MonsterRating：flee 隨 speed_mult 縮放，hit/critPct/critShield 不受 speed_mult 影響
// （BaseLevel=0 隔離等級項）---

func TestMonsterRating_FleeScalesWithSpeedMult(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BattleMonsterFleeBase = 10
	p := PlayerBattleStats{BaseLevel: 0}
	m := MonsterRow{ID: "fast", SpeedMult: 2, DefMult: 1}
	r := MonsterRating(cfg, p, m)
	if r.Flee != 20 {
		t.Fatalf("Flee 應為 flee_base(10)×speed_mult(2)=20，got %v", r.Flee)
	}
	if r.Hit != cfg.BattleMonsterHitBase || r.CritPct != cfg.BattleMonsterCritPct {
		t.Fatalf("speed_mult 不該影響 Hit/CritPct：%+v", r)
	}
}

// --- MonsterRating：speed_mult 只影響 flee，即使等級項不為 0 也一樣只乘在 flee 上（跟
// hit 的等級項完全獨立） ---

func TestMonsterRating_SpeedMultOnlyAffectsFleeNotHit(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{BaseLevel: 30}
	slow := MonsterRating(cfg, p, MonsterRow{ID: "slow", SpeedMult: 1})
	fast := MonsterRating(cfg, p, MonsterRow{ID: "fast", SpeedMult: 2})
	if slow.Hit != fast.Hit {
		t.Fatalf("speed_mult 不該影響 Hit：slow=%v fast=%v", slow.Hit, fast.Hit)
	}
	if fast.Flee != slow.Flee*2 {
		t.Fatalf("speed_mult=2 應讓 Flee 剛好乘 2：slow=%v fast=%v", slow.Flee, fast.Flee)
	}
}

// --- MonsterRating：critShield 隨 def_mult 縮放，其餘不受 def_mult 影響（BaseLevel=0 隔離）---

func TestMonsterRating_CritShieldScalesWithDefMult(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BattleMonsterCritShieldBase = 15
	p := PlayerBattleStats{BaseLevel: 0}
	m := MonsterRow{ID: "tanky", SpeedMult: 1, DefMult: 3}
	r := MonsterRating(cfg, p, m)
	if r.CritShield != 45 {
		t.Fatalf("CritShield 應為 crit_shield_base(15)×def_mult(3)=45，got %v", r.CritShield)
	}
	if r.Hit != cfg.BattleMonsterHitBase || r.Flee != cfg.BattleMonsterFleeBase {
		t.Fatalf("def_mult 不該影響 Hit/Flee：%+v", r)
	}
}

// --- MonsterRating：speed_mult/def_mult=0（髒資料）讓對應評級歸零，不 panic、不算出負值
// （BaseLevel=0 隔離等級項，單純驗證倍率=0 的乘法行為）---

func TestMonsterRating_ZeroMultipliersZeroOutFleeAndCritShield(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{BaseLevel: 0}
	m := MonsterRow{ID: "zero", SpeedMult: 0, DefMult: 0}
	r := MonsterRating(cfg, p, m)
	if r.Flee != 0 {
		t.Fatalf("speed_mult=0 應讓 Flee=0，got %v", r.Flee)
	}
	if r.CritShield != 0 {
		t.Fatalf("def_mult=0 應讓 CritShield=0，got %v", r.CritShield)
	}
	// Hit/CritPct 是純 config 基準值，不受這兩個倍率影響，維持原樣。
	if r.Hit != cfg.BattleMonsterHitBase || r.CritPct != cfg.BattleMonsterCritPct {
		t.Fatalf("Hit/CritPct 不該被歸零：%+v", r)
	}
}

// --- MonsterRating：per_level 係數生效——調高 HitPerLevel/FleePerLevel 應讓同一等級算出更高
// 的 Hit/Flee（驗證兩個新欄位真的接進公式，不是擺著沒用）---

func TestMonsterRating_PerLevelCoefficientTakesEffect(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{BaseLevel: 10}
	m := MonsterRow{ID: "m1", SpeedMult: 1}

	base := MonsterRating(cfg, p, m)

	cfg2 := cfg
	cfg2.BattleMonsterHitPerLevel = 2
	cfg2.BattleMonsterFleePerLevel = 3
	scaled := MonsterRating(cfg2, p, m)

	wantHit := 10*2 + cfg.BattleMonsterHitBase
	wantFlee := 10*3 + cfg.BattleMonsterFleeBase
	if scaled.Hit != wantHit {
		t.Fatalf("hit_per_level=2 時 Hit want %v got %v", wantHit, scaled.Hit)
	}
	if scaled.Flee != wantFlee {
		t.Fatalf("flee_per_level=3 時 Flee want %v got %v", wantFlee, scaled.Flee)
	}
	if scaled.Hit <= base.Hit || scaled.Flee <= base.Flee {
		t.Fatalf("提高 per_level 係數應讓 Hit/Flee 都變高：base=%+v scaled=%+v", base, scaled)
	}
}

// --- MonsterRating：Aspd 一律等於 battle_aspd_reference、CastReductionPct 恆為 0（怪物沒有
// 攻速/詠唱概念，見欄位註解）---

func TestMonsterRating_AspdIsFixedReferenceAndNoCastReduction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BattleAspdReference = 160
	p := PlayerBattleStats{BaseLevel: 50}
	r := MonsterRating(cfg, p, MonsterRow{ID: "m1", SpeedMult: 1.5, DefMult: 2})
	if r.Aspd != 160 {
		t.Fatalf("怪物 Aspd 應恆等於 battle_aspd_reference=160，got %v", r.Aspd)
	}
	if r.CastReductionPct != 0 {
		t.Fatalf("怪物 CastReductionPct 應恆為 0，got %v", r.CastReductionPct)
	}
}

// --- MonsterRating：不吃 encounterScale/slotScale（SPEC 明講這兩個縮放只影響 HP）---

func TestMonsterRating_IgnoresEncounterAndSlotScale(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", SpeedMult: 1.5, DefMult: 1.5}
	// MonsterRating 簽名本身就沒有 encounterScale/slotScale 參數——用 ScaleMonster 傳入不同
	// encounterScale/slotScale，驗證組出來的 sm.Rating 完全相同（只吃 m 本身的倍率）。
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	smLow := ScaleMonster(cfg, p, m, 1, 1, 1)
	smHigh := ScaleMonster(cfg, p, m, 3, 5, 1)
	if smLow.Rating != smHigh.Rating {
		t.Fatalf("encounterScale/slotScale 不該影響 Rating：low=%+v high=%+v", smLow.Rating, smHigh.Rating)
	}
}

// --- ScaleMonster：回傳的 Rating 與獨立呼叫 MonsterRating 結果一致 ---

func TestScaleMonster_RatingMatchesMonsterRating(t *testing.T) {
	cfg := DefaultConfig()
	p := PlayerBattleStats{Atk: 135, Def: 35, HPMax: 820, BaseLevel: 50}
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1.2, SpeedMult: 0.8}
	sm := ScaleMonster(cfg, p, m, 1, 1, 1)
	want := MonsterRating(cfg, p, m)
	if sm.Rating != want {
		t.Fatalf("sm.Rating=%+v 應等於 MonsterRating(cfg,p,m)=%+v", sm.Rating, want)
	}
}

// --- CompanionRating：Hit/Flee/CritPct/CritShield 目前無 migration 可加倍率欄位，一律原樣
// 沿用玩家評級（SPEC §1「沒有的話沿用玩家值」）；Aspd/CastReductionPct 是 P2 修正第 3 輪的
// 例外——固定走 battle_aspd_reference/0，不隨玩家配點連動（見 CompanionRating 函式註解）。---

func TestCompanionRating_FallsBackToPlayerRatingExceptAspd(t *testing.T) {
	cfg := DefaultConfig()
	playerRating := CombatRating{Hit: 42, Flee: 7, CritPct: 3.5, CritShield: 1, Aspd: 175, CastReductionPct: 30}
	xiaomi := CompanionRow{ID: "char_xiaomi", HPMult: 0.7, MatkMult: 1.2}
	got := CompanionRating(cfg, playerRating, xiaomi)
	want := playerRating
	want.Aspd = cfg.BattleAspdReference
	want.CastReductionPct = 0
	if got != want {
		t.Fatalf("CompanionRating 應沿用玩家 Hit/Flee/CritPct/CritShield、但固定 Aspd/CastReductionPct：want %+v got %+v", want, got)
	}
}

// --- CompanionRating：不同 CompanionRow 內容不影響結果（證明目前完全不吃 companion 欄位）---

func TestCompanionRating_IgnoresCompanionRowFields(t *testing.T) {
	cfg := DefaultConfig()
	playerRating := CombatRating{Hit: 100, Flee: 20, CritPct: 10, CritShield: 5}
	a := CompanionRow{ID: "a", HPMult: 0.5, AtkMult: 2, DefMult: 3}
	b := CompanionRow{ID: "b", HPMult: 5, AtkMult: 0.1, DefMult: 0.1}
	gotA := CompanionRating(cfg, playerRating, a)
	gotB := CompanionRating(cfg, playerRating, b)
	if gotA != gotB {
		t.Fatalf("不同 companion 欄位不該產生不同評級：gotA=%+v gotB=%+v", gotA, gotB)
	}
	if gotA.Hit != playerRating.Hit || gotA.Flee != playerRating.Flee {
		t.Fatalf("Hit/Flee 應沿用玩家評級：gotA=%+v playerRating=%+v", gotA, playerRating)
	}
}

// --- PlayerBattleStatsFrom：Rating 直接取自 Derived.Hit/Flee/CritPct/CritShield/Aspd/
// CastReductionPct，不做任何換算/保底 ---

func TestPlayerBattleStatsFrom_RatingPassesThroughDerivedFields(t *testing.T) {
	cfg := DefaultConfig()
	d := Derived{Atk: 135, Matk: 80, Def: 35, Mdef: 28, MaxHP: 820, MaxMP: 100,
		Hit: 63.5, Flee: 41.2, CritPct: 9, CritShield: 2, Aspd: 168.5, CastReductionPct: 22}
	p := PlayerBattleStatsFrom(cfg, 50, d)
	want := CombatRating{Hit: 63.5, Flee: 41.2, CritPct: 9, CritShield: 2, Aspd: 168.5, CastReductionPct: 22}
	if p.Rating != want {
		t.Fatalf("Rating 應直接取自 Derived 對應欄位：want %+v got %+v", want, p.Rating)
	}
}

// --- PlayerBattleStatsFrom：Atk/HPMax 保底生效時，Rating 完全不受影響（保底只套 Atk/HPMax） ---

func TestPlayerBattleStatsFrom_RatingUnaffectedByAtkHPFloor(t *testing.T) {
	cfg := DefaultConfig() // BattlePlayerMinAtk=30、BattlePlayerMinHP=300
	d := Derived{Atk: 5, MaxHP: 100, MaxMP: 50, Hit: 12, Flee: 3, CritPct: 0, CritShield: 0}
	p := PlayerBattleStatsFrom(cfg, 5, d)
	if p.Atk != cfg.BattlePlayerMinAtk || p.HPMax != cfg.BattlePlayerMinHP {
		t.Fatalf("前置條件錯誤：保底應已生效，got Atk=%v HPMax=%v", p.Atk, p.HPMax)
	}
	if p.Rating.Hit != 12 || p.Rating.Flee != 3 {
		t.Fatalf("Rating 不該被 Atk/HPMax 保底邏輯連帶影響：got %+v", p.Rating)
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

// 高等級保護（2026-09-14 對抗式審查 CONFIRMED）：Base Lv 夠高時怪物命中必須被 battle_monster_hit_max
// 夾住，否則會超過玩家 flee 的硬性上限（flee_cap_pct），AGI 配到滿也無法離開 missChance 下限。
func TestMonsterRating_HitIsCappedAtHighLevelSoAgiStaysUseful(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{SpeedMult: 1, DefMult: 1}

	// 低等級：還沒碰到上限，維持等級基線
	low := MonsterRating(cfg, PlayerBattleStats{BaseLevel: 27}, m)
	if want := 27*cfg.BattleMonsterHitPerLevel + cfg.BattleMonsterHitBase; low.Hit != want {
		t.Fatalf("Lv27 未觸及上限時 Hit 應為 %v，實得 %v", want, low.Hit)
	}

	// 高等級：被夾在 battle_monster_hit_max，且必須嚴格小於 flee_cap_pct 才留得住投資空間
	for _, lv := range []int{93, 99, 150} {
		got := MonsterRating(cfg, PlayerBattleStats{BaseLevel: lv}, m)
		if got.Hit != cfg.BattleMonsterHitMax {
			t.Fatalf("Lv%d 應被夾到 %v，實得 %v", lv, cfg.BattleMonsterHitMax, got.Hit)
		}
		if got.Hit >= cfg.FleeCapPct {
			t.Fatalf("Lv%d 的怪物命中 %v 不該 >= flee_cap_pct %v（AGI 會失效）", lv, got.Hit, cfg.FleeCapPct)
		}
	}
}

// Validate 必須擋下「怪物命中上限 >= 迴避上限」這種會讓 AGI 永遠無效的設定。
func TestValidate_RejectsMonsterHitMaxAboveFleeCap(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BattleMonsterHitMax = cfg.FleeCapPct
	if err := cfg.Validate(); err == nil {
		t.Fatal("battle_monster_hit_max 等於 flee_cap_pct 時應被 Validate 擋下")
	}
	cfg.BattleMonsterHitMax = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("battle_monster_hit_max = 0 時應被 Validate 擋下")
	}
}
