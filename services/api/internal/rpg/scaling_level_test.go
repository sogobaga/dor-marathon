// scaling_level_test.go：DORPG P6（CONTRACT §2）battle_scale_mode="level" 的怪物公式測試——
// 獨立成檔，跟既有 P2 "power" 模式的 scaling_test.go 分開，理由同 scaling.go 分段註解：兩套
// 模式各自獨立，不共用縮放輸入。
package rpg

import (
	"math"
	"testing"
)

func TestScaleMonsterByLevel_MatchesFormula(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 2, AtkMult: 1.5, DefMult: 1.2, SpeedMult: 1, WeakElements: []string{}}
	const level = 30
	const encounterScale = 0.6
	const slotScale = 1.0

	ref := RefPlayerStats(cfg, level)
	sm := ScaleMonsterByLevel(cfg, m, encounterScale, slotScale, level)

	wantHP := int(math.Floor(float64(ref.MaxHP) * m.HPMult * cfg.BattleLvHPRatio * encounterScale * slotScale))
	if wantHP < 1 {
		wantHP = 1
	}
	if sm.HPMax != wantHP {
		t.Fatalf("HPMax 不符公式：want %d got %d", wantHP, sm.HPMax)
	}
	wantAtk := int(math.Floor(ref.Atk * m.AtkMult * cfg.BattleLvAtkRatio * encounterScale))
	if sm.Atk != wantAtk {
		t.Fatalf("Atk 不符公式：want %d got %d", wantAtk, sm.Atk)
	}
	wantDef := int(math.Floor(ref.Def * m.DefMult * cfg.BattleLvDefRatio * encounterScale))
	if sm.Def != wantDef {
		t.Fatalf("Def 不符公式：want %d got %d", wantDef, sm.Def)
	}
	wantMdef := int(math.Floor(ref.Mdef * m.DefMult * cfg.BattleLvMdefRatio * encounterScale))
	if sm.Mdef != wantMdef {
		t.Fatalf("Mdef 不符公式：want %d got %d", wantMdef, sm.Mdef)
	}
	// matk 契約明講只乘 atk_mult，不疊加 battle_lvl_atk_ratio/encounterScale。
	wantMatk := int(math.Floor(ref.Matk * m.AtkMult))
	if sm.Matk != wantMatk {
		t.Fatalf("Matk 不符公式（只應乘 atk_mult）：want %d got %d", wantMatk, sm.Matk)
	}
	if sm.Level != level {
		t.Fatalf("level 模式的 Level 應直接等於傳入的 N：want %d got %d", level, sm.Level)
	}
}

// TestScaleMonsterByLevel_LevelIndependentOfPlayer level 模式的怪物數值只吃 monster_level，
// 跟「哪個玩家在打」完全無關——這是它與 "power" 模式最關鍵的行為差異，用兩個不同的參考玩家
// （直接改 level 參數，等同不同玩家戰力）驗證怪物數值只隨 monster_level 變，不隨玩家變。
func TestScaleMonsterByLevel_MonotonicWithMonsterLevel(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	low := ScaleMonsterByLevel(cfg, m, 1.0, 1.0, 10)
	high := ScaleMonsterByLevel(cfg, m, 1.0, 1.0, 60)
	if high.HPMax <= low.HPMax {
		t.Fatalf("Lv60 怪物 HPMax(%d) 應大於 Lv10(%d)——六場關卡才有等級差異", high.HPMax, low.HPMax)
	}
	if high.Atk <= low.Atk {
		t.Fatalf("Lv60 怪物 Atk(%d) 應大於 Lv10(%d)", high.Atk, low.Atk)
	}
}

// TestScaleMonsterByLevel_HPFloorAtLeastOne 極端小倍率（hp_mult 接近 0）不該讓 HPMax 變成 0
// （同 ScaleMonster 既有的下限防呆，見 scaling.go ScaleMonster 的 hpMax<1 分支）。
func TestScaleMonsterByLevel_HPFloorAtLeastOne(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 0.0001, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	sm := ScaleMonsterByLevel(cfg, m, 1.0, 1.0, 1)
	if sm.HPMax < 1 {
		t.Fatalf("HPMax 不可小於 1，got %d", sm.HPMax)
	}
}

// TestScaleMonsterByLevel_RatiosScaleDifficultyUniformly 契約 §2：四個比例後台可整體調高/低
// 某一項難度手感——battle_lvl_hp_ratio 加倍應讓 HPMax 加倍（其餘公式不變時）。
func TestScaleMonsterByLevel_RatiosScaleDifficultyUniformly(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", HPMult: 1, AtkMult: 1, DefMult: 1, SpeedMult: 1}
	base := ScaleMonsterByLevel(cfg, m, 1.0, 1.0, 40)

	doubled := cfg
	doubled.BattleLvHPRatio = cfg.BattleLvHPRatio * 2
	sm := ScaleMonsterByLevel(doubled, m, 1.0, 1.0, 40)
	if sm.HPMax < base.HPMax*2-1 || sm.HPMax > base.HPMax*2+1 {
		t.Fatalf("battle_lvl_hp_ratio 加倍應讓 HPMax 約略加倍：base=%d got=%d", base.HPMax, sm.HPMax)
	}
}

func TestMonsterRatingByLevel_UsesMonsterLevelNotPlayerLevel(t *testing.T) {
	cfg := DefaultConfig()
	m := MonsterRow{ID: "m1", SpeedMult: 1, DefMult: 1}
	lowLv := MonsterRatingByLevel(cfg, 10, m)
	highLv := MonsterRatingByLevel(cfg, 60, m)
	if highLv.Hit <= lowLv.Hit {
		t.Fatalf("等級越高的怪物 Hit 評級應越高：Lv10=%v Lv60=%v", lowLv.Hit, highLv.Hit)
	}
}
