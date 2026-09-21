// reflevel.go：DORPG P6（CONTRACT §2）「參考玩家」——沒有職業、沒有被動技能的假想角色，六圍
// 以確定性演算法配滿 TotalStatPoints(N) 點，供怪物等級縮放模式（scaling.go ScaleMonsterByLevel）
// 當作每個等級的絕對基準。純函式，不碰 DB。
//
// TS 端不移植這裡的演算法本身——契約明講改用「資料鏡像」：RefPlayerTable() 算出的 N=1..99
// 表格原封不動輸出成 apps/web/src/lib/dorpg/refPlayerTable.json（cmd/rpg-reftable 產生器），
// 前端 fixture.ts 離線預覽直接查表，TestRefPlayerTable_MatchesJSON 保證兩邊不會漂移——改這裡
// 的任何公式都必須重新跑一次產生器，測試才會過。
package rpg

// refStatAllocate 契約 §2 的確定性配點演算法：六圍從 cfg.InitialStat 開始，把
// TotalStatPoints(cfg,level) 點數依「輪流 +1 給目前最低的素質、成本負擔得起且未達
// cap（=StatCap(cfg,level)）」的規則配完。固定走訪順序 str→agi→vit→dex→int→luk（同值時取
// 這個順序中最前面那個），任何時候重跑都會得到完全相同的六圍——這是「確定性」的唯一要求，
// 不是「戰鬥數值最優」的演算法。
// refStatCap 參考玩家專用的素質上限：固定走 P5 原規則 min(MaxStat, level)，**刻意不吃**
// cfg.StatCapByLevel（2026-09-20 取消玩家端等級上限的那個開關）。WHY：這張表是 ScaleMonsterByLevel
// 與 ScaleMonsterByRank 的唯一基準，P6 的六場與 P11 的九級強度向量都以它校準；若跟著玩家規則浮動，
// 所有怪物數值會一起位移、兩輪校準全部作廢。實務上這個上限在 99 級內從未被觸發（點數成本
// pointCost 隨數值遞增，六圍輪流配點遠達不到 level），所以固定它也不改變任何現有數值——
// TestRefPlayerTable_MatchesJSON 會守住這點。
func refStatCap(cfg Config, level int) int {
	if level < 1 {
		level = 1
	}
	if level > cfg.MaxStat {
		return cfg.MaxStat
	}
	return level
}

func refStatAllocate(cfg Config, level int) Stats {
	statCap := refStatCap(cfg, level)
	budget := TotalStatPoints(cfg, level)
	s := Stats{Str: cfg.InitialStat, Agi: cfg.InitialStat, Vit: cfg.InitialStat, Dex: cfg.InitialStat, Int: cfg.InitialStat, Luk: cfg.InitialStat}
	ptrs := [6]*int{&s.Str, &s.Agi, &s.Vit, &s.Dex, &s.Int, &s.Luk}
	for {
		idx, lowest := -1, 0
		for i, p := range ptrs {
			if *p >= statCap {
				continue
			}
			if idx == -1 || *p < lowest {
				idx, lowest = i, *p
			}
		}
		if idx == -1 {
			break // 六圍全部達到 cap（理論上不會發生：cap×6 遠大於 99 級的總點數），配不完的預算留著不花
		}
		cost := pointCost(cfg, *ptrs[idx])
		if cost > budget {
			break // 這一點負擔不起——pointCost 隨數值單調不減，其餘素質此刻成本只會相同或更高，直接結束
		}
		*ptrs[idx]++
		budget -= cost
	}
	return s
}

// RefPlayerStats 契約 §2：等級 N 的「參考玩家」衍生數值——沒有職業（WeaponType 留空，Compute()
// 退回 cfg.DefaultWeaponType）、沒有被動技能，六圍用 refStatAllocate 配滿。ScaleMonsterByLevel
// 拿它當怪物數值的基準；RefPlayerTable 把 N=1..99 全部算好供 TS 端查表。
func RefPlayerStats(cfg Config, level int) Derived {
	stats := refStatAllocate(cfg, level)
	return Compute(cfg, ComputeInput{BaseLevel: level, Stats: stats})
}

// RefRow RefPlayerTable 的單一等級列，json tag 對齊 apps/web/src/lib/dorpg/refPlayerTable.json
// 的格式——INTEGRATOR 核對（2026-09-18）：改成 camelCase 對齊 ENGINE 已經寫好且不可再改的
// apps/web/src/lib/dorpg/fixture.ts `RefPlayerEntry` 介面與 verify-dorpg-engine.mjs #39 手寫的
// 對照表（兩邊都是 hpMax/mpMax/atk/matk/def/mdef/hit/flee/aspd），原本 BACKEND 產的是
// snake_case（hp_max/mp_max…），兩邊對不上——JS 端 loadRefPlayerTable() 型別檢查不會報錯（JSON
// 照樣被判定是陣列成功載入），但欄位名不對會讓所有 refPlayerAt().hpMax 等讀到 undefined，是一個
// 靜默失敗，只有 verify #40 的「表格已存在時才檢查」會漏掉（因為它讀到的是被 catch 吞掉的 null）。
// HPMax/MPMax 整數（Compute() 已 floor，見 compute.go P6 修改）；其餘衍生值維持 float64 原始
// 精度——encoding/json 對 float64 的編碼是「最短可還原表示法」，寫檔／讀檔會逐位元一致，
// TestRefPlayerTable_MatchesJSON 才能用 == 直接比對不需要容差。
type RefRow struct {
	Level int     `json:"level"`
	HPMax int     `json:"hpMax"`
	MPMax int     `json:"mpMax"`
	Atk   float64 `json:"atk"`
	Matk  float64 `json:"matk"`
	Def   float64 `json:"def"`
	Mdef  float64 `json:"mdef"`
	Hit   float64 `json:"hit"`
	Flee  float64 `json:"flee"`
	Aspd  float64 `json:"aspd"`
}

// RefPlayerTable N=1..99 全部算好，供產生器（cmd/rpg-reftable）與
// TestRefPlayerTable_MatchesJSON 共用同一份來源，避免兩處各自迴圈後對不上。
func RefPlayerTable(cfg Config) []RefRow {
	rows := make([]RefRow, 0, 99)
	for n := 1; n <= 99; n++ {
		d := RefPlayerStats(cfg, n)
		rows = append(rows, RefRow{
			Level: n, HPMax: d.MaxHP, MPMax: d.MaxMP,
			Atk: d.Atk, Matk: d.Matk, Def: d.Def, Mdef: d.Mdef,
			Hit: d.Hit, Flee: d.Flee, Aspd: d.Aspd,
		})
	}
	return rows
}

// refPlayerTable.json 的頂層形狀是純陣列（[]RefRow）——INTEGRATOR 核對（2026-09-18）：拿掉原本
// 的 RefPlayerTableFile{generated_from, rows} 包裝，因為 ENGINE 的 loadRefPlayerTable()
// （fixture.ts）用 `Array.isArray(table)` 判斷載入是否成功，頂層包一層物件會讓它判定失敗、
// 靜默退回 power 模式（catch 吞掉 TypeError，不會報錯，只會在 verify #40 印 SKIP）。
// generated_from 的除錯用途改用檔案開頭的產生器來源註解取代，不進 JSON 本體。
