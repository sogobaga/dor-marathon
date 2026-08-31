package runmeet

import (
	"hash/fnv"
	"math"
)

// haversineM 兩點間大圓距離（公尺）。
// 來源：複製自 services/api/internal/explore/explore.go:24 的同名函式（該處是城市探索打卡半徑判定）。
// 刻意複製而非抽共用套件：explore 已 import 一堆自己的相依，runmeet 反過來 import explore
// 會把探索系統整包拉進來（且語意上兩者無關）；這是 12 行無狀態純函式，重複的維護成本低於耦合成本。
func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// DistanceBand 把距離（公尺）轉成分級字串。
//
// ⚠️ 這是「附近搜尋」的資安核心，不是 UI 便利函式。回應中**不得出現精確距離數值**。
//
// ⚠️ 但只做分級**擋不住定位**，這點以前的註解寫錯過，務必看懂再改：band 的邊界
// （1/3/5/10 km）是「以查詢者自選座標為圓心」的精確圓。攻擊者只要移動 near_lat/near_lng
// 找出 lt1↔1to3 的翻轉點，就得到一條半徑正好 1 公里的圓；三條圓聯立即可解出精確座標。
// 同理，若過濾用的 radius_km 是連續可控值，「有沒有出現在結果裡」就是一個
// 「距離 < X 嗎」的布林神諭，對 X 二分搜尋約 25 次就能把距離收斂到公尺級。
//
// 所以附近搜尋的防護是**三件事一起**，缺一不可（見 snapRadiusKm / snapCoord）：
//  1. radius_km 只接受與 band 邊界重合的離散值（1/3/5/10）→ 過濾條件不比 band 多洩漏任何資訊。
//  2. 距離一律以 snapCoord() 量化過的座標計算 → 攻擊者再怎麼三角定位，也只能還原出格點中心。
//  3. 回應只給 band 字串（排序才用精確距離，且只發生在後端記憶體裡）。
//
// 前端顯示：lt1「1 公里內」／1to3「1–3 公里」／3to5「3–5 公里」／5to10「5–10 公里」／gt10「10 公里以上」。
func DistanceBand(meters float64) string {
	switch {
	case meters < 1000:
		return "lt1"
	case meters < 3000:
		return "1to3"
	case meters < 5000:
		return "3to5"
	case meters < 10000:
		return "5to10"
	default:
		return "gt10"
	}
}

// allowedRadiiKm 附近搜尋允許的半徑（公里）。刻意與 DistanceBand 的邊界完全重合。
//
// ⚠️ 不得改成「任意浮點數」或新增非邊界值：只要 radius 能落在兩個 band 邊界之間，
// 「這個團練有沒有出現在結果裡」就變成比 band 更細的距離神諭，可二分搜尋出精確距離。
var allowedRadiiKm = [...]float64{1, 3, 5, 10}

// snapRadiusKm 把呼叫端要求的半徑吸附到最接近的**允許值**（往上取，超過上限就取最大）。
// 刻意不回 400：合法前端只送 10，而惡意呼叫端送 0.001 也只會拿到 1 —— 直接讓神諭失效，
// 比回錯誤更不容易被試探出「哪些值有效」。
func snapRadiusKm(v float64) float64 {
	for _, r := range allowedRadiiKm {
		if v <= r {
			return r
		}
	}
	return allowedRadiiKm[len(allowedRadiiKm)-1]
}

// geoCellMeters 附近搜尋的座標量化格線邊長（公尺）。
//
// 取捨：格子愈大隱私愈好、附近搜尋愈不準。500 m 的意思是——就算攻擊者用無限次查詢把
// 「格點中心」完整還原出來，真實集合點仍均勻散布在該格內（最遠約 354 m），
// 而 band 的最細粒度本來就是 1 公里，所以對正常使用幾乎無感。
const geoCellMeters = 500.0

// snapCoord 把團練座標吸附到「每個團練各自隨機平移」的格線中心點。
//
// ⚠️ 這是附近搜尋唯一真正擋住三角定位的機制（band 與 radius 離散化只是不再洩漏**更多**）。
// salt 用團練 id：同一個團練每次查詢都吸附到同一個格點（否則多次查詢平均一下雜訊就沒了），
// 不同團練的格線偏移不同（否則攻擊者知道格線位置，能反推真值落在格內哪半邊）。
//
// 回傳值只用於「算距離、排序、分級」，**永遠不會寫回資料庫、也不會出現在任何回應裡**。
func snapCoord(lat, lng float64, salt string) (float64, float64) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(salt))
	sum := h.Sum64()
	// 兩個獨立的 [0,1) 偏移量（各取 32 bits）
	ox := float64(uint32(sum)) / float64(1<<32)
	oy := float64(uint32(sum>>32)) / float64(1<<32)

	const kmPerDegLat = 111.32
	dLat := geoCellMeters / (kmPerDegLat * 1000)
	sLat := math.Round((lat-ox*dLat)/dLat)*dLat + ox*dLat

	// ⚠️ 經度格寬必須用**吸附後**的緯度算：用原始 lat 的話，同一個緯度格內每個點的 cos(lat)
	// 都略有不同 → 經度格線跟著飄 → 量化其實沒生效（同一格內仍能還原出公尺級差異）。
	c := math.Cos(sLat * math.Pi / 180)
	if c < 0.01 {
		c = 0.01 // 極區保護：避免 dLng 爆掉
	}
	dLng := geoCellMeters / (kmPerDegLat * 1000 * c)
	sLng := math.Round((lng-oy*dLng)/dLng)*dLng + oy*dLng
	return clampF(sLat, -90, 90), clampF(sLng, -180, 180)
}

// boundingBox 依中心點與半徑（公里）算出粗篩用的經緯度範圍。
// 用途：先讓 Postgres 走 idx_run_meets_geo 的 B-tree 範圍掃描把候選集縮小，再由 Go 端用
// haversineM 精算距離、排序、分級。刻意不引入 PostGIS（環境未安裝，且這個資料量用不上）。
//
// 緯度 1 度 ≈ 111.32 km（固定）；經度 1 度 ≈ 111.32 × cos(lat) km（隨緯度收縮）。
// 高緯度時 cos(lat) 趨近 0，除法會爆成整個經度域——夾在 [-180,180] 並在 cos 太小時直接放寬到全域，
// 反正後面還有精算那關把關（bounding box 只求「不漏」，寬一點只是多算幾筆）。
func boundingBox(lat, lng, radiusKm float64) (minLat, maxLat, minLng, maxLng float64) {
	const kmPerDegLat = 111.32
	dLat := radiusKm / kmPerDegLat
	minLat = clampF(lat-dLat, -90, 90)
	maxLat = clampF(lat+dLat, -90, 90)

	c := math.Cos(lat * math.Pi / 180)
	if c < 0.01 {
		return minLat, maxLat, -180, 180
	}
	dLng := radiusKm / (kmPerDegLat * c)
	if dLng >= 180 {
		return minLat, maxLat, -180, 180
	}
	return minLat, maxLat, clampF(lng-dLng, -180, 180), clampF(lng+dLng, -180, 180)
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// validCoord 座標合法性（NaN/Inf 也擋掉——JSON 進不來但 query string 的 ParseFloat 會產生 Inf）。
func validCoord(lat, lng float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) {
		return false
	}
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}
