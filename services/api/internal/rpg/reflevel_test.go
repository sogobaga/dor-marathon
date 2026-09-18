// reflevel_test.go：DORPG P6（CONTRACT §2/§5）RefPlayerStats 的確定性/預算不變式，以及
// refPlayerTable.json 的資料鏡像同步測試——改了 reflevel.go 或 DefaultConfig() 卻忘記重跑
// cmd/rpg-reftable 產生器，這個測試會 FAIL 並在訊息裡提示怎麼補。
package rpg

import (
	"encoding/json"
	"os"
	"testing"
)

// --- refStatAllocate / RefPlayerStats：確定性與預算/上限不變式（N=1,10,27,50,99）---

func TestRefStatAllocate_Deterministic(t *testing.T) {
	cfg := DefaultConfig()
	for _, lv := range []int{1, 10, 27, 50, 99} {
		a := refStatAllocate(cfg, lv)
		b := refStatAllocate(cfg, lv)
		if a != b {
			t.Fatalf("Lv%d: 重跑兩次應得到完全相同的六圍：%+v vs %+v", lv, a, b)
		}
	}
}

func TestRefStatAllocate_NeverBelowCapNeverExceedsBudget(t *testing.T) {
	cfg := DefaultConfig()
	for _, lv := range []int{1, 10, 27, 50, 99} {
		s := refStatAllocate(cfg, lv)
		statCap := StatCap(cfg, lv)
		for _, v := range []int{s.Str, s.Agi, s.Vit, s.Dex, s.Int, s.Luk} {
			if v < cfg.InitialStat {
				t.Fatalf("Lv%d: 素質不可低於初始值 %d，got %d", lv, cfg.InitialStat, v)
			}
			if v > statCap {
				t.Fatalf("Lv%d: 素質不可超過 cap=%d，got %d", lv, statCap, v)
			}
		}
		spent := TotalSpentStats(cfg, s)
		budget := TotalStatPoints(cfg, lv)
		if spent > budget {
			t.Fatalf("Lv%d: 花費 %d 不可超過預算 %d：%+v", lv, spent, budget, s)
		}
	}
}

// TestRefStatAllocate_Lv1CannotAllocate Lv1 的 cap=min(MaxStat,1)=1=InitialStat，六圍全部已經
// 「達到上限」——比照玩家配點規則「Lv1 不能加點」的既有語意（compute.go StatCap 註解）。
func TestRefStatAllocate_Lv1CannotAllocate(t *testing.T) {
	cfg := DefaultConfig()
	s := refStatAllocate(cfg, 1)
	want := Stats{Str: cfg.InitialStat, Agi: cfg.InitialStat, Vit: cfg.InitialStat, Dex: cfg.InitialStat, Int: cfg.InitialStat, Luk: cfg.InitialStat}
	if s != want {
		t.Fatalf("Lv1 六圍應維持初始值：want %+v got %+v", want, s)
	}
}

// TestRefPlayerStats_HigherLevelNeverWeaker 等級越高，參考玩家的 HPMax/Atk 不應該變小
// （TotalStatPoints/TotalStatPoints 都是非遞減函式，Compute() 的等級項也非遞減）——粗略但
// 有用的健全性檢查，抓「等級縮放曲線倒退」這種明顯錯誤。
func TestRefPlayerStats_HigherLevelNeverWeaker(t *testing.T) {
	cfg := DefaultConfig()
	prev := RefPlayerStats(cfg, 1)
	for lv := 2; lv <= 99; lv++ {
		cur := RefPlayerStats(cfg, lv)
		if cur.MaxHP < prev.MaxHP {
			t.Fatalf("Lv%d 的 HPMax(%d) 不應小於 Lv%d 的 HPMax(%d)", lv, cur.MaxHP, lv-1, prev.MaxHP)
		}
		if cur.Atk < prev.Atk {
			t.Fatalf("Lv%d 的 Atk(%v) 不應小於 Lv%d 的 Atk(%v)", lv, cur.Atk, lv-1, prev.Atk)
		}
		prev = cur
	}
}

// --- refPlayerTable.json 資料鏡像同步測試（契約 §2：「TS 鏡像：不移植 Compute...改為資料
// 鏡像...Go 測試 TestRefPlayerTable_MatchesJSON 保證同步」）---

const refPlayerTableJSONPath = "../../../../apps/web/src/lib/dorpg/refPlayerTable.json"

func TestRefPlayerTable_MatchesJSON(t *testing.T) {
	data, err := os.ReadFile(refPlayerTableJSONPath)
	if err != nil {
		t.Fatalf("讀取 %s 失敗（%v）——改了 reflevel.go 的公式或 DefaultConfig() 之後，"+
			"必須在 services/api 目錄下重跑 `go run ./cmd/rpg-reftable` 重新產生這份 JSON",
			refPlayerTableJSONPath, err)
	}
	// INTEGRATOR 核對（2026-09-18）：頂層是純陣列（見 reflevel.go 註解），不再有 generated_from
	// 包裝物件，對齊 ENGINE 的 loadRefPlayerTable() 用 Array.isArray(table) 判斷載入成功。
	var rows []RefRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("解析 %s 失敗：%v", refPlayerTableJSONPath, err)
	}

	want := RefPlayerTable(DefaultConfig())
	if len(rows) != len(want) {
		t.Fatalf("JSON 有 %d 列，RefPlayerTable() 算出 %d 列——重跑 `go run ./cmd/rpg-reftable` 重新產生",
			len(rows), len(want))
	}
	for i, w := range want {
		got := rows[i]
		if got != w {
			t.Fatalf("第 %d 列（Lv%d）不一致——重跑 `go run ./cmd/rpg-reftable` 重新產生 refPlayerTable.json。\nJSON: %+v\nGo:   %+v",
				i, w.Level, got, w)
		}
	}
}
