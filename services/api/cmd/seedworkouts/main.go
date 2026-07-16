// Command seedworkouts 產生「個人任務」完整結構化課表庫：P01→P10 各 100 天。
// 依級別(計畫階段)換算配速區間；課表型隨階段由淺入深；距離隨週次爬升(含減量週+尾段收操)。
// 執行：DATABASE_URL="..." go run ./cmd/seedworkouts
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Seg 一個分段（對齊前端 WorkoutSegment / lib/workout expandSegments）
type Seg struct {
	Kind       string `json:"kind"`
	Label      string `json:"label,omitempty"`
	TargetType string `json:"target_type"`
	Target     int    `json:"target"`
	PaceFast   int    `json:"pace_fast_s,omitempty"`
	PaceSlow   int    `json:"pace_slow_s,omitempty"`
	Reps       int    `json:"reps,omitempty"`
	RestS      int    `json:"rest_s,omitempty"`
}

type Band struct{ F, S int } // 配速區間（秒/公里，F 較快=較小）
type Zones struct{ RC, E, T, I, R Band }

// zones 依級別 L(1..10) 換算配速區間（30/15 秒整數單位）。E=easy fast bound = 450 - 15*(L-1)。
func zones(L int) Zones {
	E := 450 - 15*(L-1)
	return Zones{
		RC: Band{E + 30, E + 90}, // 恢復
		E:  Band{E, E + 60},      // 輕鬆(zone2)
		T:  Band{E - 30, E},      // 節奏
		I:  Band{E - 60, E - 30}, // 間歇
		R:  Band{E - 90, E - 60}, // 反覆(短快)
	}
}

var planMeta = []struct{ Code, Name, Life, Note string }{
	{"P01", "城市探索 · 市民啟動", "L1 市民啟動", "剛起跑的城市市民：以輕鬆、有氧、恢復為主，少量節奏與長跑。"},
	{"P02", "城市巡遊 · 進階市民", "L2 進階市民", "穩定巡遊城市：加入節奏跑，長跑距離漸增。"},
	{"P03", "區域探勘 · 探勘者", "L3 探勘者", "拓展探勘範圍：加入間歇與漸速跑。"},
	{"P04", "街區進擊 · 街區跑者", "L4 街區跑者", "深入街區：間歇拉長、節奏跑加量。"},
	{"P05", "耐力養成 · 耐力者", "L5 耐力者", "耐力養成：長跑加長、多樣間歇。"},
	{"P06", "半馬長征 · 長征者", "L6 長征者", "半馬長征：法特萊克變速、長跑推進。"},
	{"P07", "配速掌控 · 配速者", "L7 配速者", "配速掌控：節奏與間歇並重。"},
	{"P08", "全馬築基 · 築基者", "L8 築基者", "全馬築基：長跑加量、變速跑。"},
	{"P09", "全馬遠征 · 遠征者", "L9 遠征者", "全馬遠征：超長距離、金字塔間歇。"},
	{"P10", "城市極速 · 極速者", "L10 極速者", "城市極速：挪威 4×4、極速間歇、最長長跑。"},
}

var easyBaseArr = []float64{2, 2.5, 3, 3.5, 4, 5, 6, 7, 8, 9}
var longPeakArr = []float64{5, 6, 8, 10, 13, 16, 19, 24, 30, 34}
var tempoBaseArr = []float64{1.5, 2, 2.5, 3, 3.5, 4, 5, 6, 7, 8}

var intensityByKind = map[string]string{
	"easy": "RPE3-4", "recovery": "RPE2-3", "aerobic": "RPE4-5", "tempo": "RPE6-7", "long": "RPE5-6",
	"interval": "RPE8-9", "progression": "RPE6-8", "fartlek": "RPE7-8", "variable": "RPE7-8", "pyramid": "RPE8-9", "norwegian4x4": "RPE9",
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func half(x float64) float64  { return math.Round(x*2) / 2 }
func round1(x float64) float64 { return math.Round(x*10) / 10 }
func round5(x int) int         { return ((x + 2) / 5) * 5 }
func mid(f, s int) int         { return (f + s) / 2 }
func clampf(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
func tierOf(L int) int {
	switch {
	case L <= 2:
		return 1
	case L <= 5:
		return 2
	case L <= 8:
		return 3
	default:
		return 4
	}
}
func rampFactor(week int) float64 {
	f := 0.55 + 0.45*minf(1, float64(week-1)/12)
	if week%4 == 0 {
		f *= 0.82 // 每 4 週減量週
	}
	if week >= 14 {
		f *= 0.7 // 尾段收操
	}
	return f
}
func ivParams(week int) (distM, reps, restS int) {
	switch week % 4 {
	case 1:
		return 400, 6, 60
	case 2:
		return 800, 5, 90
	case 3:
		return 1200, 4, 120
	default:
		return 1600, 3, 150
	}
}

// WO 一份課表
type WO struct {
	Title   string
	WType   string // 課表類型（中文標籤）
	Kind    string // workout_kind
	Segs    []Seg
	BaseExp int
}

func estKm(segs []Seg) float64 {
	m := 0
	for _, s := range segs {
		reps := s.Reps
		if reps < 1 {
			reps = 1
		}
		if s.TargetType == "distance" {
			m += s.Target * reps
		}
	}
	return round1(float64(m) / 1000)
}
func estMin(segs []Seg) int {
	total := 0.0
	for _, s := range segs {
		reps := s.Reps
		if reps < 1 {
			reps = 1
		}
		if s.TargetType == "distance" {
			p := mid(s.PaceFast, s.PaceSlow)
			if p <= 0 {
				p = 420
			}
			total += float64(s.Target) / 1000 * float64(p) * float64(reps)
		} else {
			total += float64(s.Target) * float64(reps)
		}
		if reps > 1 {
			total += float64(s.RestS) * float64(reps-1)
		}
	}
	return int(math.Round(total / 60))
}

func warm(z Zones, m int) Seg { return Seg{Kind: "warmup", Label: "暖身", TargetType: "distance", Target: m, PaceFast: z.E.F, PaceSlow: z.E.S, Reps: 1} }
func cool(z Zones, m int) Seg { return Seg{Kind: "cooldown", Label: "緩和", TargetType: "distance", Target: m, PaceFast: z.E.F, PaceSlow: z.E.S, Reps: 1} }

func easyRun(z Zones, km float64) WO {
	return WO{fmt.Sprintf("輕鬆跑 %.1fK", km), "輕鬆", "easy", []Seg{{Kind: "steady", Label: "輕鬆跑", TargetType: "distance", Target: int(km * 1000), PaceFast: z.E.F, PaceSlow: z.E.S, Reps: 1}}, 30}
}
func recoveryRun(z Zones, km float64) WO {
	return WO{fmt.Sprintf("恢復跑 %.1fK", km), "恢復", "recovery", []Seg{{Kind: "recovery", Label: "恢復跑", TargetType: "distance", Target: int(km * 1000), PaceFast: z.RC.F, PaceSlow: z.RC.S, Reps: 1}}, 25}
}
func aerobicRun(z Zones, km float64) WO {
	return WO{fmt.Sprintf("有氧跑 %.1fK", km), "有氧", "aerobic", []Seg{{Kind: "steady", Label: "有氧跑", TargetType: "distance", Target: int(km * 1000), PaceFast: z.E.F, PaceSlow: z.E.S, Reps: 1}}, 40}
}
func longRun(z Zones, km float64) WO {
	return WO{fmt.Sprintf("長距離 LSD %.1fK", km), "長距離", "long", []Seg{{Kind: "steady", Label: "長距離 LSD", TargetType: "distance", Target: int(km * 1000), PaceFast: z.E.F, PaceSlow: z.E.S, Reps: 1}}, 85}
}
func tempoRun(z Zones, tempoKm float64) WO {
	segs := []Seg{warm(z, 1500), {Kind: "work", Label: "節奏跑", TargetType: "distance", Target: int(tempoKm * 1000), PaceFast: z.T.F, PaceSlow: z.T.S, Reps: 1}, cool(z, 1000)}
	return WO{fmt.Sprintf("節奏跑 %.1fK", tempoKm), "節奏", "tempo", segs, 60}
}
func intervalRun(z Zones, distM, reps, restS int) WO {
	band := z.I
	if distM <= 600 {
		band = z.R
	}
	segs := []Seg{warm(z, 2000), {Kind: "work", Label: "間歇", TargetType: "distance", Target: distM, PaceFast: band.F, PaceSlow: band.S, Reps: reps, RestS: restS}, cool(z, 1500)}
	return WO{fmt.Sprintf("%dm 間歇 ×%d", distM, reps), "間歇", "interval", segs, 70}
}
func progressionRun(z Zones, km float64) WO {
	m := int(km * 1000)
	a := m * 40 / 100
	b := m * 35 / 100
	c := m - a - b
	segs := []Seg{{Kind: "steady", Label: "輕鬆段", TargetType: "distance", Target: a, PaceFast: z.E.F, PaceSlow: z.E.S, Reps: 1}, {Kind: "steady", Label: "節奏段", TargetType: "distance", Target: b, PaceFast: z.T.F, PaceSlow: z.T.S, Reps: 1}, {Kind: "steady", Label: "衝刺段", TargetType: "distance", Target: c, PaceFast: z.I.F, PaceSlow: z.I.S, Reps: 1}}
	return WO{fmt.Sprintf("漸速跑 %.1fK", km), "漸速", "progression", segs, 65}
}
func fartlek(z Zones, surges int) WO {
	segs := []Seg{warm(z, 1500)}
	for i := 0; i < surges; i++ {
		segs = append(segs, Seg{Kind: "surge", Label: "加速", TargetType: "time", Target: 60, PaceFast: z.I.F, PaceSlow: z.I.S, Reps: 1})
		segs = append(segs, Seg{Kind: "recovery", Label: "緩跑", TargetType: "time", Target: 60, PaceFast: z.E.F, PaceSlow: z.E.S, Reps: 1})
	}
	segs = append(segs, cool(z, 1000))
	return WO{fmt.Sprintf("法特萊克變速 ×%d", surges), "變速", "fartlek", segs, 65}
}
func variableRun(z Zones, reps int) WO {
	segs := []Seg{warm(z, 1500)}
	for i := 0; i < reps; i++ {
		segs = append(segs, Seg{Kind: "surge", Label: "目標配速", TargetType: "distance", Target: 400, PaceFast: z.T.F, PaceSlow: z.T.S, Reps: 1})
		segs = append(segs, Seg{Kind: "recovery", Label: "放慢", TargetType: "distance", Target: 200, PaceFast: z.RC.F, PaceSlow: z.RC.S, Reps: 1})
	}
	segs = append(segs, cool(z, 1000))
	return WO{fmt.Sprintf("變速跑 ×%d", reps), "變速", "variable", segs, 65}
}
func pyramid(z Zones) WO {
	ladder := []int{200, 400, 600, 800, 600, 400, 200}
	segs := []Seg{warm(z, 1500)}
	for i, d := range ladder {
		band := z.I
		if d <= 400 {
			band = z.R
		}
		segs = append(segs, Seg{Kind: "work", Label: "衝刺", TargetType: "distance", Target: d, PaceFast: band.F, PaceSlow: band.S, Reps: 1})
		if i < len(ladder)-1 {
			segs = append(segs, Seg{Kind: "rest", Label: "組間休息", TargetType: "time", Target: 90, Reps: 1})
		}
	}
	segs = append(segs, cool(z, 1500))
	return WO{"金字塔間歇 200→800→200", "金字塔", "pyramid", segs, 75}
}
func norwegian(z Zones) WO {
	segs := []Seg{warm(z, 1500), {Kind: "work", Label: "快跑", TargetType: "time", Target: 240, PaceFast: z.I.F, PaceSlow: z.I.S, Reps: 4, RestS: 180}, cool(z, 1500)}
	return WO{"挪威 4×4（4 分快／3 分緩）", "挪威4x4", "norwegian4x4", segs, 75}
}

func buildDay(z Zones, L, day int) WO {
	week := (day + 6) / 7
	dow := day % 7
	r := rampFactor(week)
	eb := clampf(half(easyBaseArr[L-1]*r), 1.5, 100)
	lp := clampf(half(longPeakArr[L-1]*r), eb, 100)
	tb := clampf(half(tempoBaseArr[L-1]*r), 1, 100)
	distM, reps, restS := ivParams(week)
	switch tierOf(L) {
	case 1:
		switch dow {
		case 1:
			return easyRun(z, eb)
		case 2:
			return recoveryRun(z, half(eb*0.6))
		case 3:
			return aerobicRun(z, half(eb*1.2))
		case 4:
			return tempoRun(z, clampf(half(tb*0.7), 1, 100))
		case 5:
			return recoveryRun(z, half(eb*0.6))
		case 6:
			return longRun(z, lp)
		default:
			return easyRun(z, half(eb*0.8))
		}
	case 2:
		switch dow {
		case 1:
			return easyRun(z, eb)
		case 2:
			return intervalRun(z, distM, reps, restS)
		case 3:
			return aerobicRun(z, half(eb*1.2))
		case 4:
			return tempoRun(z, tb)
		case 5:
			return recoveryRun(z, half(eb*0.6))
		case 6:
			return longRun(z, lp)
		default:
			return progressionRun(z, half(eb*1.1))
		}
	case 3:
		switch dow {
		case 1:
			return easyRun(z, eb)
		case 2:
			return intervalRun(z, distM, reps, restS)
		case 3:
			return tempoRun(z, tb)
		case 4:
			return fartlek(z, 4+week%4)
		case 5:
			return recoveryRun(z, half(eb*0.6))
		case 6:
			return longRun(z, lp)
		default:
			return progressionRun(z, half(eb*1.2))
		}
	default:
		switch dow {
		case 1:
			return easyRun(z, eb)
		case 2:
			return intervalRun(z, distM, reps, restS)
		case 3:
			return tempoRun(z, tb)
		case 4:
			switch week % 3 {
			case 0:
				return fartlek(z, 6)
			case 1:
				return variableRun(z, 6)
			default:
				return pyramid(z)
			}
		case 5:
			return recoveryRun(z, half(eb*0.7))
		case 6:
			return longRun(z, lp)
		default:
			if week%2 == 0 {
				return norwegian(z)
			}
			return pyramid(z)
		}
	}
}

func main() {
	db, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()
	const taskSQL = `INSERT INTO personal_tasks
		(plan_id,day,week,seq,title,workout,workout_type,target_km,target_min,intensity,complete_cond,data_source,reward_exp,reward_dp,workout_kind,segments,enabled)
		VALUES ($1,$2,$3,$2,$4,$5,$6,$7,$8,$9,$10,'gps',$11,$12,$13,$14,TRUE)
		ON CONFLICT (plan_id,day) DO UPDATE SET week=$3,seq=$2,title=$4,workout=$5,workout_type=$6,target_km=$7,target_min=$8,
			intensity=$9,complete_cond=$10,data_source='gps',reward_exp=$11,reward_dp=$12,workout_kind=$13,segments=$14,enabled=TRUE`

	// 先建 10 計畫拿 id，再把 1000 筆課表用 pgx.Batch 一次管線化送出（避免逐筆往返 Neon 太慢）
	batch := &pgx.Batch{}
	nTasks := 0
	for L := 1; L <= 10; L++ {
		pm := planMeta[L-1]
		z := zones(L)
		pz, _ := json.Marshal(map[string][2]int{
			"recovery": {z.RC.F, z.RC.S}, "easy": {z.E.F, z.E.S}, "tempo": {z.T.F, z.T.S}, "interval": {z.I.F, z.I.S}, "rep": {z.R.F, z.R.S},
		})
		var planID string
		if err := db.QueryRow(ctx, `INSERT INTO personal_plans (code,name,lifecycle,stage_order,entry_note,data_source,pace_zones,enabled)
			VALUES ($1,$2,$3,$4,$5,'gps',$6,TRUE)
			ON CONFLICT (code) DO UPDATE SET name=$2,lifecycle=$3,stage_order=$4,entry_note=$5,pace_zones=$6,enabled=TRUE RETURNING id`,
			pm.Code, pm.Name, pm.Life, L, pm.Note, pz).Scan(&planID); err != nil {
			panic(err)
		}
		for day := 1; day <= 100; day++ {
			wo := buildDay(z, L, day)
			segJSON, _ := json.Marshal(wo.Segs)
			rExp := round5(int(float64(wo.BaseExp) * (1 + 0.12*float64(L-1))))
			rDp := rExp / 5
			if rDp < 1 {
				rDp = 1
			}
			// title 不含「Day N ·」——前台面板/卡片已自帶 Day {day}，避免顯示兩次
			batch.Queue(taskSQL, planID, day, (day+6)/7, wo.Title, wo.Title, wo.WType, estKm(wo.Segs), estMin(wo.Segs),
				intensityByKind[wo.Kind], "完成整份課表；主課配速達成度決定星數", rExp, rDp, wo.Kind, segJSON)
			nTasks++
		}
	}
	br := db.SendBatch(ctx, batch)
	for i := 0; i < nTasks; i++ {
		if _, err := br.Exec(); err != nil {
			br.Close()
			panic(err)
		}
	}
	if err := br.Close(); err != nil {
		panic(err)
	}
	// 停用舊「示範 · 400m 間歇」(DEMO)：P-level 間歇已涵蓋；保留 TESTWO 流程測試
	_, _ = db.Exec(ctx, `UPDATE personal_plans SET enabled=FALSE WHERE code='DEMO'`)
	fmt.Printf("seeded plans=10 tasks=%d\n", nTasks)
}
