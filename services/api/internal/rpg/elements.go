// elements.go：DORPG P7（CONTRACT §1）屬性相剋純函式——五行（金木水土火）＋光暗互剋＋同屬性
// 懲罰＋弱點加成＋管理者覆寫表，五個規則的優先序都收斂在 ElementMultiplier 這一支純函式裡，方便
// 逐格單元測試（見 elements_test.go），也讓 ENGINE 依同一份規則寫 TS 鏡像（elementMatrix 測試
// 資料由 elements_test.go 產生，見該檔）。不碰 DB、不吃 Config 以外的外部狀態。
package rpg

// elementBeats 五行相剋方向表：key 剋 value（CONTRACT §1「剋＝金→木、木→土、土→水、水→火、
// 火→金」）。光/闇不放進這張表——它們是「互剋」（雙向都算 advantage），跟五行的單向剋制不同，
// 另外在 elementRelation 特判。
var elementBeats = map[string]string{
	"metal": "wood",
	"wood":  "earth",
	"earth": "water",
	"water": "fire",
	"fire":  "metal",
}

// elementCanon 把中文原文（rpg_monsters.attribute/weak_elements 後台可能填的顯示字）與英文
// ElementKind 代碼都正規化成同一組英文代碼，讓 ElementMultiplier 不管收到哪種表示法都能正確判斷
// ——現況 rpg_monsters.attribute 欄位實際存的是中文（見 content.go 檔頭「migration 176 實際 seed
// 的是中文原文」），但 weak_elements／技能 element／WeaponProfile.Element 存的是英文代碼，兩邊
// 混用是既有事實不是本檔造成的，這裡負責把兩種表示法收斂成一個判斷基準。查無對照者回空字串
// （呼叫端視為「無法判斷關係」，不參與相剋計算，也不會 panic）。
var elementCanon = map[string]string{
	"metal": "metal", "wood": "wood", "water": "water", "fire": "fire", "earth": "earth",
	"light": "light", "dark": "dark", "neutral": "neutral",
	"金": "metal", "木": "wood", "水": "water", "火": "fire", "土": "earth",
	"光": "light", "闇": "dark", "暗": "dark", "無": "neutral",
}

func canonicalElement(s string) string {
	return elementCanon[s]
}

// elementRelation "same"｜"advantage"｜"disadvantage"｜""（無關係，兩者皆已正規化過的英文代碼，
// 且都不是 neutral／空字串，呼叫端 ElementMultiplier 負責先擋 neutral/未知輸入）。
func elementRelation(attack, monster string) string {
	if attack == monster {
		return "same"
	}
	// 光↔闇互剋（CONTRACT §1）：雙向都是 advantage，跟五行的單向剋制不同，不會有一邊是
	// disadvantage 的情形。
	if (attack == "light" && monster == "dark") || (attack == "dark" && monster == "light") {
		return "advantage"
	}
	if elementBeats[attack] == monster {
		return "advantage"
	}
	if elementBeats[monster] == attack {
		return "disadvantage"
	}
	return ""
}

// ElementMultiplier CONTRACT §1：攻擊屬性 attackElement 對怪物屬性 monsterAttr（+其 weakElements
// 弱點清單）的傷害倍率。優先序（由高到低）：
//  1. cfg.BattleElementChart[monsterAttr][attackElement] 管理者覆寫——字面 key 比對，沿用
//     config.go 既有的「外層 key＝怪物 attribute（可能中文），內層 key＝攻擊 element（英文）」
//     慣例，命中即直接回傳、完全略過下面所有規則。
//  2. 五行＋光暗相剋表：A 剋 M → 1+advantage_pct/100；M 剋 A → 1+disadvantage_pct/100；
//     A＝M → 1+same_pct/100；任一方 neutral 或無法辨識 → 1（base）。
//  3. weakElements 弱點加成：attackElement ∈ weakElements → 至少 1+weakness_bonus_pct/100，
//     跟第 2 步算出的 base 取兩者較大（弱點加成獨立於屬性關係之外——既有 P5 seed 就有
//     neutral 屬性怪物仍設弱點的先例，見 migration 180 灰白獸人）。
func ElementMultiplier(cfg Config, attackElement, monsterAttr string, weakElements []string) float64 {
	if row, ok := cfg.BattleElementChart[monsterAttr]; ok {
		if v, ok2 := row[attackElement]; ok2 {
			return v
		}
	}

	a := canonicalElement(attackElement)
	m := canonicalElement(monsterAttr)

	base := 1.0
	if a != "" && m != "" && a != "neutral" && m != "neutral" {
		switch elementRelation(a, m) {
		case "same":
			base = 1 + cfg.BattleElementSamePct/100
		case "advantage":
			base = 1 + cfg.BattleElementAdvantagePct/100
		case "disadvantage":
			base = 1 + cfg.BattleElementDisadvantagePct/100
		}
	}

	// 只有攻擊屬性真的落在弱點清單裡才需要跟 base 比較——不能把「沒有弱點加成」的預設值直接
	// 當成 1.0 去跟 base 取 max，那樣會在 base<1.0（disadvantage/same）時被 1.0 蓋掉，讓被剋/
	// 同屬性的懲罰整個失效（曾經是這裡的 bug，見 elements_test.go
	// TestElementMultiplier_FiveElementCycle 抓到的迴歸）。
	isWeak := false
	for _, w := range weakElements {
		if a != "" && canonicalElement(w) == a {
			isWeak = true
			break
		}
	}
	if !isWeak {
		return base
	}
	weakMul := 1 + cfg.BattleWeaknessBonusPct/100
	if weakMul > base {
		return weakMul
	}
	return base
}
