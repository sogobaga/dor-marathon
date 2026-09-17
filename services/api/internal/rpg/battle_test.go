// battle_test.go：對抗式審查 dorpg_p5 修正輪 #1【高・CONFIRMED】的根因回歸測試——
// toWireSkillLeveled() 過去把頂層 Coefficient/Flat/MPCost 設成 SkillRow 的原始欄位（Lv1 基準值），
// 沒有依玩家目前等級展開；前端 fromApi.ts mapSkill() 讀的正是這批頂層欄位（不是巢狀 Effect），
// 升級因此從未進入戰鬥結算，且舊版 wireSkill 沒有頂層 Hits 欄位，連段技能永遠只打 1 下。
// 見 battle.go toWireSkillLeveled/toWireSkillLegacy 的修復註解。
package rpg

import "testing"

func TestToWireSkillLeveled_TopLevelFieldsMatchExpandEffect(t *testing.T) {
	s := SkillRow{
		ID: "combo_strike", Name: "連擊", Kind: "damage", Target: "enemy", Weapon: "sword",
		MPCost: 10, CooldownMs: 5000, Coefficient: 1.0, Flat: 5, CastMs: 400, MaxLevel: 10,
		MPCostPerLevel: 1.5,
		Effect: SkillEffect{
			CoefBase: 1.0, CoefPerLevel: 0.2, Hits: 3,
			FlatBase: 5, FlatPerLevel: 2,
		},
	}
	const level = 5
	want := ExpandEffect(s, level)
	w := toWireSkillLeveled(s, level)

	if w.Coefficient != want.Coef {
		t.Fatalf("頂層 Coefficient 應等於 ExpandEffect 展開值：want %v got %v", want.Coef, w.Coefficient)
	}
	if float64(w.Flat) != want.Flat {
		t.Fatalf("頂層 Flat 應等於 ExpandEffect 展開值：want %v got %v", want.Flat, w.Flat)
	}
	if w.Hits != want.Hits {
		t.Fatalf("頂層 Hits 應等於 ExpandEffect 展開值：want %v got %v", want.Hits, w.Hits)
	}
	if float64(w.MPCost) != want.MPCost {
		t.Fatalf("頂層 MPCost 應等於 ExpandEffect 展開值：want %v got %v", want.MPCost, w.MPCost)
	}
	if w.Level != level {
		t.Fatalf("Level 欄位應等於呼叫時傳入的 level：want %d got %d", level, w.Level)
	}
	if w.Effect.Coef != want.Coef || w.Effect.Hits != want.Hits {
		t.Fatalf("巢狀 Effect 欄位應與展開結果一致（供 FRONTEND 顯示鏡射用）")
	}

	// 升級要真的有感：Lv1 與 Lv5 的頂層數值不應相同，否則等於沒有展開（回歸本次審查抓到的缺陷本身）。
	// 注意：Hits 是技能設計上的固定值（SkillEffect 沒有 HitsPerLevel 這種欄位，見 content.go
	// SkillEffect 型別），本來就不隨等級變化——這裡不斷言 Hits 會變，只在上面已確認 Lv5 的頂層
	// Hits 正確等於 ExpandEffect 展開值（=3），這才是回歸 #1 缺陷（曾經是 0，因為舊版 wireSkill
	// 根本沒有頂層 Hits 欄位）真正要測的事。
	w1 := toWireSkillLeveled(s, 1)
	if w1.Coefficient == w.Coefficient {
		t.Fatalf("Lv1 與 Lv5 的頂層 Coefficient 不應相同：Lv1=%v Lv5=%v", w1.Coefficient, w.Coefficient)
	}
	if w1.MPCost == w.MPCost {
		t.Fatalf("Lv1 與 Lv5 的頂層 MPCost 不應相同（mp_cost_per_level>0）：Lv1=%v Lv5=%v", w1.MPCost, w.MPCost)
	}
}

// TestToWireSkillLeveled_BuffKindHitsDefaultsToZeroButEffectCarriesStat 確認非 damage/heal/shield
// 的 kind（buff/debuff/passive/special）不會被 ExpandEffect 誤展開出 Coef/Flat/Hits——這幾個 kind
// 完全靠 Effect.Stat/Value，頂層數字欄位維持零值即可（不影響 combat.ts 的 buff/debuff 結算，那邊
// 只讀 Effect，見 combat.ts resolveBuffDebuff 註解）。
func TestToWireSkillLeveled_BuffKindDoesNotExpandCoefFlatHits(t *testing.T) {
	s := SkillRow{
		ID: "war_cry", Name: "戰吼", Kind: "buff", Target: "self", Weapon: "sword",
		MPCost: 15, MaxLevel: 5,
		Effect: SkillEffect{Stat: "atk_pct", ValueBase: 10, ValuePerLevel: 2, DurationMs: 8000, Target: "self"},
	}
	w := toWireSkillLeveled(s, 3)
	if w.Coefficient != 0 || w.Flat != 0 || w.Hits != 0 {
		t.Fatalf("buff kind 的頂層 Coefficient/Flat/Hits 應維持零值：got coef=%v flat=%v hits=%v", w.Coefficient, w.Flat, w.Hits)
	}
	if w.Effect.Stat != "atk_pct" || w.Effect.Value != 14 { // 10 + 2*(3-1) = 14
		t.Fatalf("buff kind 的 Effect.Stat/Value 應正確展開：got %+v", w.Effect)
	}
}

func TestToWireSkillLegacy_HitsDefaultsToOne(t *testing.T) {
	s := SkillRow{ID: "legacy_slash", Name: "既有技能", Kind: "damage", Target: "enemy", Weapon: "sword", Coefficient: 1.6, Flat: 20, MPCost: 5}
	w := toWireSkillLegacy(s)
	if w.Hits != 1 {
		t.Fatalf("既有技能（無等級/連段概念）頂層 Hits 應固定 1，got %d", w.Hits)
	}
	if w.Level != 1 || w.MaxLevel != 1 {
		t.Fatalf("既有技能 Level/MaxLevel 應固定 1：got Level=%d MaxLevel=%d", w.Level, w.MaxLevel)
	}
	if w.Coefficient != s.Coefficient || w.Flat != s.Flat || w.MPCost != s.MPCost {
		t.Fatalf("既有技能沒有等級可展開，頂層數值應原樣沿用 SkillRow 欄位")
	}
}
