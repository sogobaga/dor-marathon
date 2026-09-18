// cmd/rpg-reftable：DORPG P6（CONTRACT §2「資料鏡像」）產生器——把 internal/rpg.RefPlayerTable()
// 用 DefaultConfig() 算出的 N=1..99 表格寫成 apps/web/src/lib/dorpg/refPlayerTable.json，供前端
// fixture.ts 離線預覽查表用（TS 不移植 Go 的配點/Compute 公式，改吃這份資料鏡像）。
//
// 用法（從 services/api 目錄執行）：
//
//	go run ./cmd/rpg-reftable
//
// 改了 reflevel.go 的 refStatAllocate/RefPlayerStats 或 config.go 的 DefaultConfig() 之後，
// 必須重新跑一次這支程式並 commit 新的 JSON——internal/rpg 的 TestRefPlayerTable_MatchesJSON
// 會逐列比對，JSON 沒跟著重生會直接 FAIL 並在錯誤訊息裡提示要跑這支指令。
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/dor/api/internal/rpg"
)

func main() {
	out := flag.String("out", "../../apps/web/src/lib/dorpg/refPlayerTable.json",
		"輸出路徑（預設值假設從 services/api 目錄執行 go run）")
	flag.Parse()

	cfg := rpg.DefaultConfig()
	// INTEGRATOR 核對（2026-09-18）：頂層直接是陣列，不包 {generated_from, rows} ——
	// 對齊 ENGINE 的 loadRefPlayerTable()（fixture.ts）用 Array.isArray(table) 判斷載入成功。
	rows := rpg.RefPlayerTable(cfg)
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		log.Fatalf("write %s: %v", *out, err)
	}
	log.Printf("wrote %s (%d rows)", *out, len(rows))
}
