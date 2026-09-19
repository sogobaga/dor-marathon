// strategies_test.go：DORPG P9（CONTRACT §1、WIRE）AI 戰鬥策略——STRATEGY_IDS／
// isKnownStrategyID／buildAiStrategiesWire 純函式測試，不連 DB（is_active 檢查需要查
// rpg_ai_strategies，留給 Neon 分支整合測試，比照本套件既有慣例）。
package rpg

import "testing"

func TestSTRATEGY_IDS_HasSixKnownEntries(t *testing.T) {
	want := map[string]bool{
		"balanced": true, "mp_conserve": true, "skill_aggressive": true,
		"protect_allies": true, "focus_fire": true, "element_advantage": true,
	}
	if len(STRATEGY_IDS) != len(want) {
		t.Fatalf("CONTRACT §2 六種策略，got %d 個：%v", len(STRATEGY_IDS), STRATEGY_IDS)
	}
	for _, id := range STRATEGY_IDS {
		if !want[id] {
			t.Fatalf("未預期的策略 id：%s（CONTRACT §2 只點名六種）", id)
		}
	}
}

func TestDefaultStrategyID_IsAmongSTRATEGY_IDS(t *testing.T) {
	if !isKnownStrategyID(DefaultStrategyID) {
		t.Fatalf("DefaultStrategyID=%q 必須是 STRATEGY_IDS 白名單成員之一", DefaultStrategyID)
	}
	if DefaultStrategyID != "balanced" {
		t.Fatalf(`CONTRACT §1「未知 id 一律退回 balanced」，DefaultStrategyID 應為 "balanced"，got %q`, DefaultStrategyID)
	}
}

func TestIsKnownStrategyID_UnknownRejected(t *testing.T) {
	for _, bad := range []string{"", "unknown", "Balanced", "mp_conserve "} {
		if isKnownStrategyID(bad) {
			t.Fatalf("%q 不應被視為合法策略 id", bad)
		}
	}
}

func TestIsKnownStrategyID_AllSixAccepted(t *testing.T) {
	for _, id := range STRATEGY_IDS {
		if !isKnownStrategyID(id) {
			t.Fatalf("%q 應被 isKnownStrategyID 接受（STRATEGY_IDS 本身的成員）", id)
		}
	}
}

func TestBuildAiStrategiesWire_MapsIdToParams(t *testing.T) {
	rows := []StrategyRow{
		{ID: "balanced", Params: map[string]any{"heal_pct": float64(50)}},
		{ID: "focus_fire", Params: nil}, // 尚未設定 params 的列不該讓輸出變成 nil map（前端 JSON.parse 才好處理）
	}
	wire := buildAiStrategiesWire(rows)
	if len(wire) != 2 {
		t.Fatalf("應輸出兩個 id，got %d", len(wire))
	}
	if wire["balanced"].Params["heal_pct"] != float64(50) {
		t.Fatalf("balanced 的 params 應原樣帶出，got %+v", wire["balanced"].Params)
	}
	if wire["focus_fire"].Params == nil {
		t.Fatalf("params 為 nil 時應補成空物件，避免序列化成 JSON null")
	}
}

// 2026-09-19 修復：StrategyDTO 原本漏帶 IsActive，後台編輯表單抓 GET 回來的 DTO 原樣送回時
// IsActive 永遠讀到零值 false，等於每次編輯都把該策略靜默停用——這條測試釘住 toStrategyDTO
// 必須原樣帶出 IsActive（true 與 false 都要驗，避免只測 true 讓「永遠回 true」之類的誤修也通過）。
func TestToStrategyDTO_PreservesIsActive(t *testing.T) {
	active := toStrategyDTO(StrategyRow{ID: "balanced", IsActive: true})
	if !active.IsActive {
		t.Fatalf("is_active=true 的策略列，DTO 應保留 IsActive=true，got %+v", active)
	}
	inactive := toStrategyDTO(StrategyRow{ID: "focus_fire", IsActive: false})
	if inactive.IsActive {
		t.Fatalf("is_active=false 的策略列，DTO 應保留 IsActive=false，got %+v", inactive)
	}
}

func TestBuildAiStrategiesWire_OnlyIncludesGivenRows(t *testing.T) {
	// WIRE：「只含 is_active 的 id」——這個篩選由呼叫端 listActiveStrategies() 的 SQL WHERE
	// 負責（連 DB，留給 Neon 分支整合測試），buildAiStrategiesWire 本身只負責攤平，這裡驗證
	// 它不會偷偷加料或漏資料。
	rows := []StrategyRow{{ID: "balanced", Params: map[string]any{}}}
	wire := buildAiStrategiesWire(rows)
	if _, ok := wire["mp_conserve"]; ok {
		t.Fatalf("不在輸入清單內的 id 不應出現在輸出")
	}
	if _, ok := wire["balanced"]; !ok {
		t.Fatalf("輸入清單內的 id 應該出現在輸出")
	}
}
