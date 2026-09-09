package race

import (
	"context"
	"fmt"
	"time"
)

// 寵物雲端馬拉松成績規則（2026-09-09 owner request，migration 174，D4）：新增兩種完賽條件／排名
// 規則——狗狗累積里程、飼主＋狗狗里程加總。本檔案是所有計分呼叫端（leaderboard.go computeFinishersWith
// ／progress.go／my_active.go／settlement.go／expbreakdown.go）共用的核心：兩個純函式
// （EffectivePetScoreMode／ComposeScore，供單元測試涵蓋所有組合，見 pet_scoring_test.go）＋一段共用
// SQL（petActivityGateSQLFragment 的兩種查詢形態：只要總量的 loadPetKmTotals、需要逐筆時間序列以算
// 出精確 completion_at 的 loadPetEvents）。
//
// 非寵物賽事（race.PetKind==""）：EffectivePetScoreMode 恆回 "owner"，呼叫端也應該在 race.PetKind==""
// 時完全跳過 loadPetKmTotals/loadPetEvents（省一次查詢；即使不跳過，這兩個查詢在非寵物賽事下也天生
// 查無資料，因為 registration_pets/pet_activities 只會在寵物賽事被建立）。

const (
	PetScoreModeOwner       = "owner"         // 依飼主里程（預設，等同非寵物賽事今天的行為）
	PetScoreModePet         = "pet"           // 依狗狗累積里程
	PetScoreModeOwnerPetSum = "owner_pet_sum" // 飼主＋狗狗里程加總
)

// EffectivePetScoreMode 算出某筆報名「實際套用」的成績規則——race.pet_score_mode 受該報名所屬組別
// for_owner/for_pet 兩個勾選約束後的最終結果（D4）：
//   - petKind==""（非寵物賽事）或 forPet==false → "owner"（這個分組本來就不採計寵物成績）
//   - forOwner==false 且 mode=="owner_pet_sum" → "pet"（沒有飼主成績可加，退化成純寵物）
//   - 其餘情況 → mode 原樣（""｜"pet"｜"owner_pet_sum"；"" 依 ComposeScore 的定義等同只採計飼主里程，
//     跟 "owner" 是同一件事，這裡不強制正規化成 "owner" 字面值，保留呼叫端看得出「賽事本來就沒設定」
//     還是「賽事設定了但這個分組被 for_pet/for_owner 蓋掉」的差異，供除錯用）
func EffectivePetScoreMode(petKind, mode string, forOwner, forPet bool) string {
	if petKind == "" || !forPet {
		return PetScoreModeOwner
	}
	if !forOwner && mode == PetScoreModeOwnerPetSum {
		return PetScoreModePet
	}
	return mode
}

// ComposeScore 依有效規則（EffectivePetScoreMode 的回傳值）組出計分依據的「score」——完賽／排名／
// EXP 完成判定一律比較這個值，不再直接比較里程本身。mode==""（等同 "owner"）與任何未知字串一律
// 落在 default（只採計飼主里程），確保未來新增/誤帶不明字串時退化成最保守（今天的行為）而不是誤判過關。
func ComposeScore(mode string, ownerKm, petKm float64) float64 {
	switch mode {
	case PetScoreModePet:
		return petKm
	case PetScoreModeOwnerPetSum:
		return ownerKm + petKm
	default:
		return ownerKm
	}
}

// wirePetScoreMode 把內部 sentinel 值 "owner"（EffectivePetScoreMode 對非寵物賽事、或被該分組
// for_pet=false 蓋掉時的回傳值）轉成 D1 契約定義的三值集合（""｜"pet"｜"owner_pet_sum"）裡的空
// 字串，只在「即將寫進會序列化成 JSON 的 *ScoreMode 欄位」這一刻呼叫——內部計分（ComposeScore／
// completion_at 判定）全部繼續吃 EffectivePetScoreMode 的原始回傳值，不受影響。
//
// 這層轉換存在的理由（2026-09-09 review 抓到的 critical bug）：前端一律用「pet_score_mode 是否為
// 非空字串」判斷「這是不是寵物賽事、要不要秀飼主/狗狗拆分」（RaceDetailScreen.tsx／
// RaceRankingScreen.tsx）。若不轉換，非寵物賽事（甚至寵物賽事裡 for_pet=false 的分組）序列化出去
// 的會是非空的 "owner" 字面值，被前端誤判成寵物賽事，在每一場賽事的進度/排行榜上都多長出一行假的
// 「飼主里程／狗狗里程」。呼叫端（progress.go／leaderboard.go／my_active.go／certificate.go）一律
// 在賦值給 JSON 欄位前套用這個函式。
func wirePetScoreMode(mode string) string {
	if mode == PetScoreModeOwner {
		return ""
	}
	return mode
}

// scoreEvent 是單筆「里程貢獻」事件（owner 活動或寵物歸戶紀錄各一筆），供需要精確 completion_at
// 的呼叫端（computeFinishersWith）依時間排序合併後逐筆累加算出「score 跨過目標」當下的時間點。
type scoreEvent struct {
	kind string // "owner" | "pet"
	dist float64
	dur  int
	at   time.Time
}

// petActivityJoinGateSQL 是 pet_activities 一律套用的資料來源 gate（migration 174，D4）：
// NOT pa.flagged；activity_id 有值時（來源 owner_run，即某趟飼主自己的活動），該活動也必須
// NOT flagged 且通過與飼主里程完全相同的資料來源 gate（App GPS 恆算／外部來源僅
// race.external_data 開啟時才算／Strava 永遠排除，見 activity-data-source-gate）——owner_run
// 來源的寵物里程本來就是同一趟活動複製來的，不能自己另一套標準。source='platform'/'manual'
// （activity_id 為 NULL，未來串接用）沒有活動可比對，只看 pa.flagged 本身。
//
// ⚠️ 2026-09-09 review 修正：一律再加「JOIN registrations reg ON reg.id = pa.registration_id AND
// reg.status <> 'cancelled'」——pet_activities.registration_id 是「登記當下」那一筆報名，不會因為
// 使用者事後取消／退費該筆報名而消失（SettleCancellation 只翻 registrations.status，不動
// registration_pets/pet_activities）；非 personal 賽事允許取消後用新報名重報同一場（service.go
// 「允許重新報名同一賽事」），此時舊報名底下的 registration_pets 是另一批列（新報名會登記新的
// registration_pets 列），若不擋 reg.status，舊報名(已取消/退費)當年歸戶的寵物里程會透過
// pa.user_id 一路疊加進新報名的 score，讓使用者拿已取消報名的寵物里程騙過新報名的完賽判定。
// 飼主里程側（LoadRaceActivities／computeFinishersWith 的 activities 查詢）本來就有等價的
// `JOIN registrations reg ON ... AND reg.status <> 'cancelled'` 過濾，這裡補齊寵物里程側同款規則，
// 且比飼主里程更精確——直接鎖只認目前這筆「未取消」報名底下的 pet_activities.registration_id，
// 不是鬆散地只認 user_id。
//
// 使用前提：SQL 中已 JOIN races rc ON rc.id = pa.race_id、LEFT JOIN activities la ON la.id = pa.activity_id。
const petActivityJoinGateSQL = `NOT pa.flagged AND (pa.activity_id IS NULL OR (NOT la.flagged AND (la.source IS NULL OR (rc.external_data AND la.source <> 'strava'))))`

// petActivityRegJoinSQL 補上「只認未取消報名底下的寵物里程」這道閘門（見 petActivityJoinGateSQL
// 註解）；使用前提：SQL 中已 JOIN pet_activities pa。
const petActivityRegJoinSQL = `JOIN registrations reg ON reg.id = pa.registration_id AND reg.status <> 'cancelled'`

// loadPetKmTotals 依 user_id 加總某賽事「賽事期間」內的寵物累積里程（不需要逐筆時間序列的呼叫端
// 共用：progress.go／my_active.go／settlement.go／expbreakdown.go）。start/end 通常是
// race.start_date/end_date；personal 模式有各自的挑戰窗口且目前不支援寵物成績（見各呼叫端註解），
// 不應對 personal 賽事呼叫這個函式。
func loadPetKmTotals(ctx context.Context, db pgQueryer, raceID string, start, end time.Time) (map[string]float64, error) {
	rows, err := db.Query(ctx, `
		SELECT pa.user_id::text, COALESCE(SUM(pa.distance_km),0)
		FROM pet_activities pa
		JOIN races rc ON rc.id = pa.race_id
		`+petActivityRegJoinSQL+`
		LEFT JOIN activities la ON la.id = pa.activity_id
		WHERE pa.race_id = $1 AND pa.recorded_at BETWEEN $2 AND $3 AND `+petActivityJoinGateSQL+`
		GROUP BY pa.user_id`, raceID, start, end)
	if err != nil {
		return nil, fmt.Errorf("load pet km totals: %w", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var uid string
		var km float64
		if err := rows.Scan(&uid, &km); err != nil {
			return nil, err
		}
		out[uid] = km
	}
	return out, rows.Err()
}

// loadPetEventsByUser 撈出某賽事「賽事期間」內逐筆寵物里程貢獻事件，依 user_id 分組（組內依
// recorded_at 排序），供 computeFinishersWith 與 owner 活動事件合併，算出精確的 completion_at
// （見該函式）。回傳 map 而非扁平陣列——呼叫端本來就是逐 user 處理，省去再分組一次。
func loadPetEventsByUser(ctx context.Context, db pgQueryer, raceID string) (map[string][]scoreEvent, error) {
	rows, err := db.Query(ctx, `
		SELECT pa.user_id::text, pa.distance_km, pa.duration_s, pa.recorded_at
		FROM pet_activities pa
		JOIN races rc ON rc.id = pa.race_id
		`+petActivityRegJoinSQL+`
		LEFT JOIN activities la ON la.id = pa.activity_id
		WHERE pa.race_id = $1 AND pa.recorded_at BETWEEN rc.start_date AND rc.end_date AND `+petActivityJoinGateSQL+`
		ORDER BY pa.user_id, pa.recorded_at`, raceID)
	if err != nil {
		return nil, fmt.Errorf("load pet events: %w", err)
	}
	defer rows.Close()
	out := map[string][]scoreEvent{}
	for rows.Next() {
		var uid string
		var e scoreEvent
		e.kind = "pet"
		if err := rows.Scan(&uid, &e.dist, &e.dur, &e.at); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], e)
	}
	return out, rows.Err()
}
