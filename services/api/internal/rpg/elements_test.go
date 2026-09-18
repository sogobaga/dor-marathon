// elements_test.go：DORPG P7（CONTRACT §1）ElementMultiplier 純函式測試——五行＋光暗全表、
// neutral、weak_elements 取大、管理者覆寫優先序，以及中文/英文屬性表示法的正規化。
//
// TestGenerateElementCasesFixture 額外把整組案例（輸入＋期望倍率）寫成
// scratchpad/dorpg_p7/element_cases.json，供 ENGINE 用同一份資料跑 TS 鏡像測試（WIRE 要求「兩邊
// 雙邊測試」）。這裡刻意讓一支 go test 順便產生固定路徑的檔案——不是本檔案一貫做法，但這份資料
// 本質上就是「ElementMultiplier 的黃金測試資料」，寫檔與驗證共用同一份計算結果，兩邊永遠同步，
// 比另外維護一支產生器程式更不容易漂移（比照 cmd/rpg-reftable 的精神，但受限於本輪只能動
// internal/rpg/** 不能新增 cmd/，改用 go test 完成同樣的事）。
package rpg

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// elementsAll 8 種屬性（七行 + neutral），用來窮舉 8×8＝64 組合。
var elementsAll = []string{"metal", "wood", "water", "fire", "earth", "light", "dark", "neutral"}

// fiveBeatsIndependent 五行相剋方向表——刻意在測試檔獨立重寫一份（不是直接引用 elements.go 的
// elementBeats），這樣測試才是「拿契約文字重新推導一次答案」去核對實作，而不是拿實作對自己回歸，
// 真的能抓到「相剋方向抄錯」這類錯誤。
var fiveBeatsIndependent = map[string]string{
	"metal": "wood", "wood": "earth", "earth": "water", "water": "fire", "fire": "metal",
}

// wantRelation 依 CONTRACT §1 文字重新推導期望的關係："same"｜"advantage"｜"disadvantage"｜
// "neutral"（含任一方是 neutral，或兩者五行/光暗都無關聯，例如 metal vs water）。
func wantRelation(a, m string) string {
	if a == "neutral" || m == "neutral" {
		return "neutral"
	}
	if a == m {
		return "same"
	}
	if (a == "light" && m == "dark") || (a == "dark" && m == "light") {
		return "advantage"
	}
	if fiveBeatsIndependent[a] == m {
		return "advantage"
	}
	if fiveBeatsIndependent[m] == a {
		return "disadvantage"
	}
	return "neutral"
}

func wantMultiplier(cfg Config, relation string) float64 {
	switch relation {
	case "same":
		return 1 + cfg.BattleElementSamePct/100
	case "advantage":
		return 1 + cfg.BattleElementAdvantagePct/100
	case "disadvantage":
		return 1 + cfg.BattleElementDisadvantagePct/100
	default:
		return 1.0
	}
}

// TestElementMultiplier_FiveElementCycle 8×8＝64 組合，逐格核對（五行單向剋制、光暗互剋、
// 同屬性懲罰、neutral 恆 1）。
func TestElementMultiplier_FiveElementCycle(t *testing.T) {
	cfg := DefaultConfig()
	for _, a := range elementsAll {
		for _, m := range elementsAll {
			want := wantMultiplier(cfg, wantRelation(a, m))
			got := ElementMultiplier(cfg, a, m, nil)
			if !almostEqual(got, want) {
				t.Errorf("ElementMultiplier(%s→%s)：want %.4f got %.4f（relation=%s）", a, m, want, got, wantRelation(a, m))
			}
		}
	}
}

func TestElementMultiplier_ChineseAttributeCanonicalization(t *testing.T) {
	cfg := DefaultConfig()
	pairs := []struct{ zh, en string }{
		{"金", "metal"}, {"木", "wood"}, {"水", "water"}, {"火", "fire"},
		{"土", "earth"}, {"光", "light"}, {"闇", "dark"}, {"無", "neutral"},
	}
	for _, p := range pairs {
		gotZh := ElementMultiplier(cfg, "fire", p.zh, nil)
		gotEn := ElementMultiplier(cfg, "fire", p.en, nil)
		if !almostEqual(gotZh, gotEn) {
			t.Errorf("中文屬性 %s 與英文 %s 應算出同一個倍率，got zh=%.4f en=%.4f", p.zh, p.en, gotZh, gotEn)
		}
	}
}

func TestElementMultiplier_WeakElementsTakesMax(t *testing.T) {
	cfg := DefaultConfig() // advantage=+25%/disadvantage=-25%/same=-25%/weakness=+25%
	// fire 對 wood：五行無關聯（neutral base=1.0），但 wood 弱點含 fire → 應取弱點加成 1.25。
	got := ElementMultiplier(cfg, "fire", "wood", []string{"fire"})
	if !almostEqual(got, 1.25) {
		t.Fatalf("neutral 關係下弱點加成應生效，want 1.25 got %.4f", got)
	}
	// fire 對 metal：fire 剋 metal 本來就是 advantage 1.25，弱點清單也含 fire → 取較大者仍是
	// 1.25（不會疊加成 1.5）。
	got2 := ElementMultiplier(cfg, "fire", "metal", []string{"fire"})
	if !almostEqual(got2, 1.25) {
		t.Fatalf("剋制+弱點同時成立應取較大值而非相加，want 1.25 got %.4f", got2)
	}
	// fire 對 water：water 剋 fire 是 disadvantage(-25%=0.75)，但弱點清單含 fire → 取較大值 1.25。
	got3 := ElementMultiplier(cfg, "fire", "water", []string{"fire"})
	if !almostEqual(got3, 1.25) {
		t.Fatalf("被剋但同時是弱點時應取較大值 1.25，got %.4f", got3)
	}
	// 弱點清單不含攻擊屬性時完全不受影響。
	got4 := ElementMultiplier(cfg, "fire", "wood", []string{"water"})
	if !almostEqual(got4, 1.0) {
		t.Fatalf("弱點清單不含攻擊屬性不應有加成，want 1.0 got %.4f", got4)
	}
}

func TestElementMultiplier_WeakElementsIgnoresNeutralMonsterAttribute(t *testing.T) {
	// P5 既有先例（migration 180 灰白獸人 attribute=無/neutral、weak_elements=['earth']）：
	// monster attribute 是 neutral 不代表弱點清單失效，兩者互相獨立。
	cfg := DefaultConfig()
	got := ElementMultiplier(cfg, "earth", "neutral", []string{"earth"})
	if !almostEqual(got, 1.25) {
		t.Fatalf("monster attribute=neutral 時弱點加成仍應生效，want 1.25 got %.4f", got)
	}
}

func TestElementMultiplier_ChartOverrideWinsOverEverything(t *testing.T) {
	cfg := DefaultConfig()
	// 正常關係下 light 對 dark 是 advantage(1.25)，弱點也含 light；管理者覆寫成 3.0 應該整個蓋過。
	cfg.BattleElementChart = map[string]map[string]float64{
		"dark": {"light": 3.0},
	}
	got := ElementMultiplier(cfg, "light", "dark", []string{"light"})
	if !almostEqual(got, 3.0) {
		t.Fatalf("管理者覆寫應最優先，want 3.0 got %.4f", got)
	}
	// 覆寫表對「查無」的組合完全不影響既有規則。
	got2 := ElementMultiplier(cfg, "metal", "wood", nil)
	if !almostEqual(got2, 1.25) {
		t.Fatalf("覆寫表沒有涵蓋的組合應維持既有規則，want 1.25 got %.4f", got2)
	}
}

func TestElementMultiplier_UnknownInputTreatedAsNeutral(t *testing.T) {
	cfg := DefaultConfig()
	// 無法辨識的字串（既不是中文也不是英文代碼）：不 panic，也不誤觸發任何相剋，回中性 1.0。
	got := ElementMultiplier(cfg, "unknown_element", "metal", nil)
	if !almostEqual(got, 1.0) {
		t.Fatalf("無法辨識的攻擊屬性應回中性 1.0，got %.4f", got)
	}
}

// ---------------------------------------------------------------------------
// 產生 element_cases.json 給 ENGINE 跑 TS 鏡像測試（WIRE：「Go↔TS：ElementMultiplier 鏡像測試
// 同輸入同輸出」）。
// ---------------------------------------------------------------------------

// elementCaseFixture 一筆測試案例：輸入＋這組 cfg 下的期望倍率。ChartOverride 非 nil 時代表這筆
// 案例要先把 cfg.BattleElementChart 設成這個值再呼叫（TS 鏡像測試據此建構對應的 config）。
type elementCaseFixture struct {
	Label         string             `json:"label"`
	AttackElement string             `json:"attack_element"`
	MonsterAttr   string             `json:"monster_attr"`
	WeakElements  []string           `json:"weak_elements"`
	ChartOverride map[string]float64 `json:"chart_override,omitempty"` // 只有這筆案例的 monster_attr 那一列
	Expected      float64            `json:"expected"`
}

type elementCasesFile struct {
	Config struct {
		AdvantagePct    float64 `json:"advantage_pct"`
		DisadvantagePct float64 `json:"disadvantage_pct"`
		SamePct         float64 `json:"same_pct"`
		WeaknessPct     float64 `json:"weakness_pct"`
	} `json:"config"`
	Cases []elementCaseFixture `json:"cases"`
}

// elementCasesFixturePath 由使用者在任務指示中指定的固定輸出路徑（scratchpad，非 repo 內）。
const elementCasesFixturePath = `C:/Users/paris/AppData/Local/Temp/claude/c--Project-Dev-04-Online-Marathon-Project/6f259465-0f7d-431c-a122-bf21a4e9a8eb/scratchpad/dorpg_p7/element_cases.json`

// TestGenerateElementCasesFixture 產生 8×8 全表 + weak_elements 案例 + override 案例，每筆都用
// ElementMultiplier() 現算 Expected（不是手抄常數）——這樣寫檔與這支測試本身永遠一致，之後
// elements.go 若改了任何係數，重跑這支測試就能重新產生正確的 fixture，不會漂移成過期資料。
func TestGenerateElementCasesFixture(t *testing.T) {
	cfg := DefaultConfig()

	var out elementCasesFile
	out.Config.AdvantagePct = cfg.BattleElementAdvantagePct
	out.Config.DisadvantagePct = cfg.BattleElementDisadvantagePct
	out.Config.SamePct = cfg.BattleElementSamePct
	out.Config.WeaknessPct = cfg.BattleWeaknessBonusPct

	// 8×8 基本表。
	for _, a := range elementsAll {
		for _, m := range elementsAll {
			mul := ElementMultiplier(cfg, a, m, nil)
			out.Cases = append(out.Cases, elementCaseFixture{
				Label: "matrix", AttackElement: a, MonsterAttr: m, WeakElements: []string{}, Expected: mul,
			})
		}
	}

	// weak_elements 取大案例（涵蓋 neutral 關係、剋制關係、被剋關係三種底色）。
	weakCases := []struct {
		label string
		a, m  string
		weak  []string
	}{
		{"weak_over_neutral", "fire", "wood", []string{"fire"}},
		{"weak_with_advantage_no_stack", "fire", "metal", []string{"fire"}},
		{"weak_overrides_disadvantage", "fire", "water", []string{"fire"}},
		{"weak_not_matching_attack", "fire", "wood", []string{"water"}},
		{"weak_on_neutral_monster_attribute", "earth", "neutral", []string{"earth"}},
		// 審查#4【低】：正式庫 weak_elements 可能存中文（後台輸入習慣跟 attribute 一樣），
		// ElementMultiplier 對 weakElements 逐項 canonicalElement 正規化（見 elements.go 第 93 行）
		// 才能命中——這筆案例確保 TS 鏡像（formulas.ts elementMultiplier）也對 weakElements 逐項
		// 正規化，不是只比對原始字串（該項曾經是 TS 端獨有的 bug，Go 端本來就正確）。
		{"weak_elements_chinese", "fire", "wood", []string{"火"}},
	}
	for _, c := range weakCases {
		mul := ElementMultiplier(cfg, c.a, c.m, c.weak)
		out.Cases = append(out.Cases, elementCaseFixture{
			Label: c.label, AttackElement: c.a, MonsterAttr: c.m, WeakElements: c.weak, Expected: mul,
		})
	}

	// 中文屬性表示法（wire 實際會送的格式）。
	zhCases := []struct {
		label string
		a, m  string
	}{
		{"chinese_metal", "metal", "金"}, {"chinese_dark_light_counter", "light", "闇"},
		{"chinese_same_element", "fire", "火"}, {"chinese_neutral", "fire", "無"},
	}
	for _, c := range zhCases {
		mul := ElementMultiplier(cfg, c.a, c.m, nil)
		out.Cases = append(out.Cases, elementCaseFixture{
			Label: c.label, AttackElement: c.a, MonsterAttr: c.m, WeakElements: []string{}, Expected: mul,
		})
	}

	// 管理者覆寫案例：覆寫值刻意設成關係表/弱點都不會算出的 3.0，證明覆寫優先序最高。
	overrideCfg := DefaultConfig()
	overrideCfg.BattleElementChart = map[string]map[string]float64{"dark": {"light": 3.0}}
	overrideMul := ElementMultiplier(overrideCfg, "light", "dark", []string{"light"})
	out.Cases = append(out.Cases, elementCaseFixture{
		Label: "chart_override", AttackElement: "light", MonsterAttr: "dark",
		WeakElements: []string{"light"}, ChartOverride: map[string]float64{"light": 3.0}, Expected: overrideMul,
	})

	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal 失敗：%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(elementCasesFixturePath), 0o755); err != nil {
		t.Fatalf("建立目錄失敗：%v", err)
	}
	if err := os.WriteFile(elementCasesFixturePath, raw, 0o644); err != nil {
		t.Fatalf("寫入 fixture 失敗：%v", err)
	}
	t.Logf("已產生 %d 筆案例 → %s", len(out.Cases), elementCasesFixturePath)
}
