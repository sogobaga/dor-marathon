// service.go：GPS 距離校正的 DB 讀寫層——候選配對查詢、觸發重算（debounce）、對外生效係數、
// 使用者開關、後台凍結/解凍/重設。estimator.go 的純函式在這裡被餵入真實資料、結果寫回。
package gpscalib

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/appsettings"
)

// ResolveEntry 依系統設定 gps_calib_entry_state/gps_calib_entry_whitelist 解析出這個使用者該看到
// 的入口狀態（hidden/locked/shown）。獨立實作於本套件內（比照 monopoly 套件的前例），不 import
// profile 的未匯出 resolveEntry——避免 profile ↔ gpscalib 循環依賴（profile.Dashboard 需要顯示
// 這裡的欄位，見 membership.go）。email/code/isSuperAdmin 由呼叫端傳入（Dashboard 熱路徑通常已經
// 查過一次，避免重複查詢）。
func ResolveEntry(ctx context.Context, db *pgxpool.Pool, email, code string, isSuperAdmin bool) string {
	if isSuperAdmin {
		return "shown"
	}
	return resolveApplyEntry(ctx, db, email, code)
}

// resolveApplyEntry 與 ResolveEntry 邏輯相同，但**不**套用 super_admin 旁路。對抗式審查修正：
// hidden/locked 是設計上的緊急關閉開關（規格 §7：出問題要能立即回退），但 EffectiveFactor/
// DashboardSummary 若沿用 ResolveEntry 的 super_admin 旁路，超管帳號會不管系統設定寫什麼都繼續套
// 用校正——關閉開關對超管完全失效。卡片可見性（Entry 欄位、requireEntry 端點放行）仍用 ResolveEntry
// （含旁路，比照 cheer_edit/monopoly 既有慣例：超管永遠看得到/管得到功能本身）；只有「校正是否真的
// 動到里程數字」這一步改用這支嚴格版本，兩者都走同一份 app_settings 判斷邏輯，只差 super_admin 那行。
func resolveApplyEntry(ctx context.Context, db *pgxpool.Pool, email, code string) string {
	return applyEntryFrom(
		appsettings.GetString(ctx, db, EntryStateKey, "hidden"),
		appsettings.GetString(ctx, db, EntryWhitelistKey, ""),
		email, code)
}

// EntryStateKey/EntryWhitelistKey 入口設定的 app_settings key（migration 154 種下，後台「系統設定 →
// GPS 校正」可改）。抽成常數是因為後台列表（handler.go AdminList）一次要判上百位會員的入口狀態，
// 必須把兩個設定值撈出來重複使用，不能每列都各查一次。
const (
	EntryStateKey     = "gps_calib_entry_state"
	EntryWhitelistKey = "gps_calib_entry_whitelist"
)

// applyEntryFrom resolveApplyEntry 的純函式版本（設定值由呼叫端傳入）——同一份判斷邏輯，供
// 「一次判一位」（resolveApplyEntry）與「一次判一整頁」（AdminList）共用，避免兩處走樣。
func applyEntryFrom(state, whitelist, email, code string) string {
	switch state {
	case "open":
		return "shown"
	case "locked":
		return "locked"
	case "whitelist":
		if whitelisted(whitelist, email, code) {
			return "shown"
		}
		return "hidden"
	default: // hidden 或未設定
		return "hidden"
	}
}

// whitelisted 比照 profile/membership.go 的 personalWhitelisted：換行/逗號/分號/空白分隔，可填
// 帳號編碼（#可省）或 email，大小寫不敏感。
func whitelisted(list, email, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	code = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(code), "#"))
	for _, tok := range strings.FieldsFunc(list, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		t := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tok), "#"))
		if t == "" {
			continue
		}
		if (email != "" && t == email) || (code != "" && t == code) {
			return true
		}
	}
	return false
}

// NotifyWhitelistKey 站內信通知白名單的系統設定 key（後台「系統設定 → GPS 校正」可編輯，種子值見
// migrations/155_gps_calib_notify.sql）。與 gps_calib_entry_whitelist 分開的理由見 notifyAllowed。
const NotifyWhitelistKey = "gps_calib_notify_whitelist"

// notifyAllowed 純函式：這個使用者該不該收到 GPS 校正的狀態變更站內信。兩個條件必須**同時**成立：
//  1. applyEntry=="shown"——校正對他真的生效（沿用 resolveApplyEntry，不含 super_admin 旁路），
//     否則會通知一個他看不到也沒生效的功能。
//  2. 命中 gps_calib_notify_whitelist——**獨立於入口白名單**的第二道閘門。入口設定
//     （gps_calib_entry_state）日後改成 open 時條件 1 對全站都成立，只剩這道能擋住廣發。
//
// 空白名單刻意回 false（fail-closed，一封都不發），與 gps_calib_entry_whitelist「空值＋open＝全放行」
// 的語意相反——通知是「打擾使用者」的動作，預設值必須是最保守的那一邊。
func notifyAllowed(applyEntry, notifyList, email, code string) bool {
	return applyEntry == "shown" && whitelisted(notifyList, email, code)
}

// resolveUserIdentity 查該使用者的 email/account_code/is_super_admin（ResolveEntry 所需）。
func resolveUserIdentity(ctx context.Context, db *pgxpool.Pool, userID string) (email, code string, isSuperAdmin bool, err error) {
	err = db.QueryRow(ctx, `SELECT COALESCE(email,''), COALESCE(account_code,''), is_super_admin FROM users WHERE id=$1`, userID).
		Scan(&email, &code, &isSuperAdmin)
	return
}

// EffectiveMeta EffectiveFactor 的中繼資料，供呼叫端記錄/顯示（例如結束畫面判斷是否要標示
// 「已依手錶紀錄校正」）。
type EffectiveMeta struct {
	Applied bool   // 這次是否真的套用了非 1.0 的係數
	Entry   string // hidden|locked|shown
	Status  string // warming|active|unstable|stale|frozen（entry 非 shown 時為空字串）
}

// EffectiveState 「這位會員此刻到底有沒有在校正」的判定結果。純資料，由下面的 effectiveState
// 算出，EffectiveFactor（GPS 上傳實際入帳那一步）與所有讀取端（GET /me/gps-calib、後台詳情、
// 後台列表）共用同一份，確保後台看到的數字＝實際入帳的數字。
//
// 對抗式審查修正（high finding）：Recompute 是刻意的「影子模式」——對全體使用者無條件執行，
// 非白名單會員一樣會被寫成 status='active'、factor=0.97xx。讀取端若只看 user_gps_calib 的
// status/factor（甚至只補了懶判 stale 一種），就會把這些「只算不套」的會員顯示成「校正中
// ×0.97xx」，而 EffectiveFactor 對他們其實回 1.0、gps_runs.calib_distance_km 存的是原始距離。
// enabled=false（使用者自己關掉）同理。因此讀取端一律改走這支。
type EffectiveState struct {
	Factor  float64 // 生效係數（任何一步判定不套用一律 1.0）
	Applied bool    // 是否真的套用了校正
	// Reason 不套用的原因，供後台顯示（生效時為空字串）：
	//   entry    入口非 shown（隱藏/鎖定/未在 gps_calib_entry_whitelist）＝影子模式，只算不套
	//   no_data  尚無 user_gps_calib 列（從未有過候選配對）
	//   disabled 使用者自己在個人資料頁關掉
	//   status   狀態非 active/frozen（warming/unstable）
	//   stale    active 但超過 StaleDays 沒有新配對（讀取端懶判，不寫 DB）
	Reason string
}

// effectiveState EffectiveFactor 判定順序 1/3/4/5/6/7 的純函式版本（第 2 步「查無列」由 hasRow
// 表達）。不碰 DB，所以列表端可以一次判一整頁，也讓判定邏輯能被單元測試釘住。
func effectiveState(applyEntry string, hasRow, enabled bool, status string, factor float64, lastPairAt *time.Time, now time.Time) EffectiveState {
	if applyEntry != "shown" {
		return EffectiveState{Factor: 1.0, Reason: "entry"}
	}
	if !hasRow {
		return EffectiveState{Factor: 1.0, Reason: "no_data"}
	}
	if !enabled {
		return EffectiveState{Factor: 1.0, Reason: "disabled"}
	}
	if status == "frozen" {
		return EffectiveState{Factor: factor, Applied: factor > 0}
	}
	if status != "active" {
		if status == "stale" {
			return EffectiveState{Factor: 1.0, Reason: "stale"}
		}
		return EffectiveState{Factor: 1.0, Reason: "status"}
	}
	if lastPairAt != nil && now.Sub(*lastPairAt) > StaleDays*24*time.Hour {
		return EffectiveState{Factor: 1.0, Reason: "stale"}
	}
	return EffectiveState{Factor: factor, Applied: true}
}

// EffectiveFactor 回傳「目前生效」的校正係數——GPS 上傳（activity/gps.go SaveGPSRun）與
// Dashboard 顯示共用同一個函式，確保「跑者看到的＝入帳的」單一事實來源。查詢失敗或任何一步
// 判定不套用一律回退 1.0（原始值），絕不因為校正子系統的暫時性錯誤擋住跑步紀錄。
//
// 判定順序：
//  1. 入口非 shown（隱藏/鎖定/未在白名單）→ 1.0。
//  2. 尚無 user_gps_calib 列（從未有過候選配對）→ 1.0。
//  3. enabled=false（使用者自行關閉）→ 1.0。
//  4. status≠'active' 且 status≠'frozen'（warming/unstable/stale）→ 1.0——這種情況下 Recompute
//     本來就已經把 factor 欄位寫回 1.0（見 Recompute：Publish 對這幾種狀態回傳 Factor=1.0），
//     這裡的判斷只是防禦性保險，不依賴那份保證也拿得到正確結果。
//  5. status='frozen' → factor（AdminFreeze 寫入時 factor 與 frozen_factor 同步更新，見
//     AdminFreeze，這裡不必再多查一欄）。
//  6. status='active' 但 last_pair_at 超過 120 天未更新 → 1.0（讀取端懶判 stale，不寫 DB；
//     下次真的有新配對觸發 Recompute 才會把 status 欄位改寫成 stale）。
//  7. 其餘（status='active' 且未過期）→ factor。
func EffectiveFactor(ctx context.Context, db *pgxpool.Pool, userID string) (float64, EffectiveMeta) {
	email, code, isSuperAdmin, err := resolveUserIdentity(ctx, db, userID)
	if err != nil {
		return 1.0, EffectiveMeta{}
	}
	meta := EffectiveMeta{Entry: ResolveEntry(ctx, db, email, code, isSuperAdmin)}
	// 對抗式審查修正：套用與否一律用 resolveApplyEntry（不含 super_admin 旁路）判斷，meta.Entry 仍用
	// 上面含旁路的版本（給呼叫端顯示用，不影響是否真的套用），見 resolveApplyEntry 註解。
	applyEntry := resolveApplyEntry(ctx, db, email, code)
	if applyEntry != "shown" {
		return 1.0, meta
	}

	var enabled bool
	var status string
	var factor float64
	var lastPairAt *time.Time
	err = db.QueryRow(ctx, `SELECT enabled, status, factor, last_pair_at FROM user_gps_calib WHERE user_id=$1`, userID).
		Scan(&enabled, &status, &factor, &lastPairAt)
	if err != nil {
		return 1.0, meta // 查無列或查詢失敗：一律不套用
	}
	meta.Status = status
	// 判定本身走 effectiveState（純函式），與讀取端（GetStatus／AdminList）共用同一份，確保後台
	// 顯示的「生效係數」＝這裡實際入帳的係數。
	es := effectiveState(applyEntry, true, enabled, status, factor, lastPairAt, time.Now())
	meta.Applied = es.Applied
	return es.Factor, meta
}

// DashboardSummary 給 profile.Dashboard 用的精簡讀模型（不含 pairs/log，那些留給 GET
// /me/gps-calib 詳情頁）：入口可見性 + 目前生效係數（與 EffectiveFactor 同一判斷，供前台總覽卡片
// 顯示）+ 狀態 + 視窗內配對數 + 使用者開關現況。email/code/isSuperAdmin 由呼叫端傳入（Dashboard
// 熱路徑通常已經查過一次 users 表，避免重複查詢）。
func DashboardSummary(ctx context.Context, db *pgxpool.Pool, userID, email, code string, isSuperAdmin bool) (entry string, factor float64, status string, nPairs int, enabled bool) {
	entry = ResolveEntry(ctx, db, email, code, isSuperAdmin)
	factor, status, enabled = 1.0, "warming", true
	// 對抗式審查修正：entry（卡片可見性，含 super_admin 旁路）與「是否真的套用」分開判斷，
	// 見 resolveApplyEntry 註解——否則超管的 Dashboard 會顯示已套用校正，但 EffectiveFactor
	// （GPS 上傳當下實際採用）卻因為嚴格版本判定不套用而回 1.0，兩邊「看到的」與「入帳的」對不上。
	if resolveApplyEntry(ctx, db, email, code) != "shown" {
		return
	}
	var lastPairAt *time.Time
	var dbFactor float64
	err := db.QueryRow(ctx, `SELECT enabled, status, factor, n_pairs, last_pair_at FROM user_gps_calib WHERE user_id=$1`, userID).
		Scan(&enabled, &status, &dbFactor, &nPairs, &lastPairAt)
	if err != nil {
		status, enabled = "warming", true
		return
	}
	factor = 1.0
	switch {
	case !enabled:
		// factor 保持 1.0（未套用），但 status/n_pairs 仍如實回報現況供 UI 顯示。
	case status == "frozen":
		factor = dbFactor
	case status == "active":
		if lastPairAt == nil || time.Since(*lastPairAt) <= StaleDays*24*time.Hour {
			factor = dbFactor
		} else {
			status = "stale" // 讀取端懶判，不寫 DB（比照 EffectiveFactor）
		}
	}
	return
}

// candidateSQL 撈候選配對（GPS 側讀 gps_runs，結構性保證原始值、不需 COALESCE；外部側讀
// activities）。resetAt 非 nil 時只取 reset 之後的 GPS 趟（後台「重設」只用之後的配對）。
//
// 對抗式審查修正：Pair.ExtDurS（供 G3 partial／G5 edge 時間對齊用）改讀 COALESCE(a.elapsed_s,
// a.duration_s) 而非直接讀 a.duration_s——Strava 的 activities.duration_s 存的是 moving_time（扣掉
// 停等），而 gps_runs.duration_s 是 GPS 端真實經過時間（含紅綠燈停等）；兩個不同語意的量直接相減/
// 相除比對，會讓任何停等超過約 20-35 秒的乾淨跑步系統性被 partial/edge 閘門誤拒。elapsed_s 只有
// Strava 會填（見 integration/strava.go ElapsedTime），COROS/Terra 為 NULL 時 COALESCE 退回
// duration_s（其語意本來就接近經過時間，不受影響）。distance 比值（LogRatio）不受此影響，仍用
// a.duration_s 算出的 AvgPaceS 只在其他 7 個讀取點使用、與這裡無關。
const candidateSQL = `
	SELECT g.id::text, g.distance_km, g.duration_s, g.ended_at - make_interval(secs=>g.duration_s) AS gps_start,
	       a.id::text, a.source, a.distance_km, COALESCE(a.elapsed_s, a.duration_s), a.recorded_at AS ext_start, COALESCE(a.flag_reason,'')
	FROM gps_runs g JOIN activities a ON a.user_id = g.user_id
	WHERE g.user_id = $1 AND NOT g.flagged AND g.review_action IS DISTINCT FROM 'rejected'
	  AND g.ended_at >= now() - interval '120 days'
	  AND ($2::timestamptz IS NULL OR g.started_at >= $2)
	  AND a.source IN ('strava','garmin','coros') AND a.external_id IS NOT NULL AND a.duration_s > 0
	  AND abs(extract(epoch from (a.recorded_at - (g.ended_at - make_interval(secs=>g.duration_s))))) <= 600`

func loadCandidatePairs(ctx context.Context, tx pgx.Tx, userID string, resetAt *time.Time) ([]Pair, error) {
	rows, err := tx.Query(ctx, candidateSQL, userID, resetAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pair
	for rows.Next() {
		var p Pair
		if err := rows.Scan(&p.GpsRunID, &p.RawGpsKm, &p.GpsDurS, &p.GpsStart,
			&p.ExtActivityID, &p.ExtSource, &p.ExtKm, &p.ExtDurS, &p.ExtStart, &p.ExtFlagReason); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// pickRefSource：user_profiles.preferred_data_source 在 {strava,garmin,coros} 內就用它；否則試
// 三個候選來源各自當 refSource 跑一次 Gate()，取「若選它為參考來源、會有最多筆通過 G1-G7 全部
// 閘門」者，同數依 garmin > coros > strava。候選為空、或三個來源都是 0 筆 accepted（使用者從未
// 接過任何支援的穿戴/第三方來源，或現有候選全被其他閘門擋下）回空字串。
//
// 對抗式審查修正（medium-1 finding 附帶的次要問題）：舊版本以「候選出現次數」計數，會把明顯
// 不合格的候選（partial/short/edge/range 都會被擋）也算進去，使自動挑選的來源可能其實一筆都
// 進不了估計視窗；改用「實際會被 accepted 的筆數」才是真正有意義的挑選依據，且與 pickRefSource
// 被呼叫時「還沒真的跑過 Gate」的時序無關——直接借用 Gate() 本身反覆試跑三次即可（候選量通常
// 只有數十筆，成本可忽略）。
func pickRefSource(preferred string, pairs []Pair) string {
	switch preferred {
	case "strava", "garmin", "coros":
		return preferred
	}
	best, bestN := "", 0
	for _, src := range []string{"garmin", "coros", "strava"} {
		n := 0
		for _, g := range Gate(pairs, src) {
			if g.Accepted {
				n++
			}
		}
		if n > bestN {
			bestN = n
			best = src
		}
	}
	if best != "" {
		return best
	}
	// 對抗式審查修正（low-1 finding）：三個來源都 0 筆 accepted 時（常見於還沒累積到任何一組能通過
	// 全部閘門的候選，例如剛連上手錶／時長差太多），舊版本直接回空字串——refSource 留空會讓
	// Gate() 的 G2「p.ExtSource != refSource」對每一筆候選都成立，所有配對一律被誤標
	// reject_reason='other_source'，蓋掉真正原因（partial/short/edge/range），使用者在「最近配對表」
	// 完全看不出問題出在哪。改用「候選筆數最多」的來源當 refSource（即使還沒有任何一筆會被
	// accepted，也能讓 Gate() 標出真正的拒絕原因）；仍然全部是 0 筆候選（pairs 為空）才回空字串。
	best, bestN = "", 0
	for _, p := range pairs {
		n := 0
		for _, q := range pairs {
			if q.ExtSource == p.ExtSource {
				n++
			}
		}
		if n > bestN {
			bestN = n
			best = p.ExtSource
		}
	}
	return best
}

func pairKey(gpsRunID, extActivityID string) string { return gpsRunID + "|" + extActivityID }

// Recompute 重新查候選配對、跑估計器（estimator.go）、upsert user_gps_calib + 全量重寫
// gps_calib_pairs（每次重算所有閘門結果都重評，冪等 ON CONFLICT DO UPDATE）+ 係數/狀態有變才寫
// user_gps_calib_log。reason/actor 見 migrations/154 的 user_gps_calib_log 欄位註解。
//
// per-user 序列化：SELECT pg_advisory_xact_lock（交易內），比照 integration/mileage_exp.go 的慣例，
// 避免同一使用者的多個觸發點（GPS 上傳、Strava/COROS/Terra 匯入）併發重算互相打架。
func Recompute(ctx context.Context, db *pgxpool.Pool, userID, reason, actor string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('gpscalib:'||$1))`, userID); err != nil {
		return err
	}

	var prevFactor = 1.0
	var prevStatus, refSource, prefSource, prevFingerprint string
	var lastPairAt, resetAt *time.Time
	var version int
	err = tx.QueryRow(ctx, `SELECT factor, status, ref_source, last_pair_at, reset_at, version, COALESCE(window_fingerprint,'') FROM user_gps_calib WHERE user_id=$1 FOR UPDATE`, userID).
		Scan(&prevFactor, &prevStatus, &refSource, &lastPairAt, &resetAt, &version, &prevFingerprint)
	isNew := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !isNew {
		return err
	}

	pairs, err := loadCandidatePairs(ctx, tx, userID, resetAt)
	if err != nil {
		return err
	}
	if isNew && len(pairs) == 0 {
		return nil // 從未有過候選、這次也沒有：不建立空白列，避免灌爆表
	}

	// ref_source 每次都重新挑選（對抗式審查修正，見 medium-1 finding）：舊版本只在 refSource 為
	// 空字串時才呼叫 pickRefSource，之後永久沿用第一次的結果——使用者換手錶/改偏好來源
	// （SetDataSource）後新配對會被全部標成 other_source、係數再也無法更新，只能靠後台
	// AdminReset 救回。這裡只在挑出「非空」結果時才覆寫，避免本輪候選剛好是空（例如 reset 之後
	// 尚無新趟）時把既有的 ref_source 洗掉。
	_ = tx.QueryRow(ctx, `SELECT COALESCE(preferred_data_source,'') FROM user_profiles WHERE user_id=$1`, userID).Scan(&prefSource)
	if picked := pickRefSource(prefSource, pairs); picked != "" {
		refSource = picked
	}

	now := time.Now().UTC()
	result := Compute(pairs, refSource, prevFactor, lastPairAt, now)

	// 全量重寫本次候選涵蓋到的配對（含被拒絕的，供「最近配對表」顯示）。
	inlierByKey := map[string]float64{}
	for i, g := range result.Est.Window {
		inlierByKey[pairKey(g.GpsRunID, g.ExtActivityID)] = result.Est.InlierW[i]
	}
	var newestAt *time.Time
	for _, g := range result.Gated {
		var inlier interface{}
		if v, ok := inlierByKey[pairKey(g.GpsRunID, g.ExtActivityID)]; ok {
			inlier = v
		}
		var reject interface{}
		if g.Reason != "" {
			reject = g.Reason
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO gps_calib_pairs
				(user_id, gps_run_id, ext_activity_id, ext_source, gps_km, ext_km, gps_dur_s, ext_dur_s,
				 start_gap_s, end_gap_s, log_ratio, dist_w, accepted, reject_reason, inlier_w, activity_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now())
			ON CONFLICT (gps_run_id, ext_activity_id) DO UPDATE SET
				ext_source=EXCLUDED.ext_source, gps_km=EXCLUDED.gps_km, ext_km=EXCLUDED.ext_km,
				gps_dur_s=EXCLUDED.gps_dur_s, ext_dur_s=EXCLUDED.ext_dur_s,
				start_gap_s=EXCLUDED.start_gap_s, end_gap_s=EXCLUDED.end_gap_s,
				log_ratio=EXCLUDED.log_ratio, dist_w=EXCLUDED.dist_w, accepted=EXCLUDED.accepted,
				reject_reason=EXCLUDED.reject_reason, inlier_w=EXCLUDED.inlier_w,
				activity_at=EXCLUDED.activity_at, updated_at=now()`,
			userID, g.GpsRunID, g.ExtActivityID, g.ExtSource, round2(g.RawGpsKm), round2(g.ExtKm),
			g.GpsDurS, g.ExtDurS, g.StartGapS, g.EndGapS, g.LogRatio, g.DistW, g.Accepted, reject, inlier,
			g.GpsStart); err != nil {
			return err
		}
		if newestAt == nil || g.GpsStart.After(*newestAt) {
			t := g.GpsStart
			newestAt = &t
		}
	}
	if newestAt == nil {
		newestAt = lastPairAt // 這次沒有任何候選（例如 reset 之後尚無新趟）：沿用舊值
	}

	newFingerprint := WindowFingerprint(result.Est.Window)
	newFactor, newStatus := result.Pub.Factor, result.Pub.Status
	switch {
	case prevStatus == "frozen":
		newFactor, newStatus = prevFactor, "frozen" // frozen：只更新 pairs/log_mu/sigma，不覆寫對外係數
	case prevStatus == "active" && newStatus == "active" && newFingerprint != "" && newFingerprint == prevFingerprint:
		// 對抗式審查修正（medium-2 finding）：這批 accepted 視窗跟上次 publish 時完全相同——
		// 沒有新配對進來，只是又被觸發了一次 Recompute（使用者連按「重新計算」、或無關的
		// webhook）。±2% 遲滯步幅的設計本意是「每有一組新證據最多前進一步」，若對「同一份
		// 證據」重複計算也照樣前進，使用者只要在 60 秒限流間隔外連續按幾次就能把步幅繞過、
		// 幾分鐘內衝到收斂值；這裡直接凍結在 prevFactor，等真的有新/舊配對讓視窗組成改變
		// （或超出 120 天視窗被淘汰）才繼續往 mu 走。
		newFactor = prevFactor
	}
	changed := isNew || newFactor != prevFactor || newStatus != prevStatus
	if changed {
		version++
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_gps_calib (user_id, ref_source, factor, log_mu, sigma, n_pairs, n_eff, eff_weight,
		                             status, last_pair_at, computed_at, version, window_fingerprint, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now(),$11,$12,now())
		ON CONFLICT (user_id) DO UPDATE SET
			ref_source=EXCLUDED.ref_source, factor=EXCLUDED.factor, log_mu=EXCLUDED.log_mu, sigma=EXCLUDED.sigma,
			n_pairs=EXCLUDED.n_pairs, n_eff=EXCLUDED.n_eff, eff_weight=EXCLUDED.eff_weight, status=EXCLUDED.status,
			last_pair_at=EXCLUDED.last_pair_at, computed_at=EXCLUDED.computed_at, version=EXCLUDED.version,
			window_fingerprint=EXCLUDED.window_fingerprint, updated_at=now()`,
		userID, refSource, newFactor, result.Est.LogMu, result.Est.Sigma, len(result.Est.Window),
		result.Est.NEff, result.Est.EffWeight, newStatus, newestAt, version, nullIfEmpty(newFingerprint)); err != nil {
		return err
	}

	if changed {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_gps_calib_log (user_id, version, factor_before, factor_after, status, log_mu, sigma, n_eff, reason, actor)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			userID, version, prevFactor, newFactor, newStatus, result.Est.LogMu, result.Est.Sigma, result.Est.NEff, reason, actor); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// 站內信通知（規格 §3.4，對抗式審查修正——low-4 finding：原規格完全未實作）：只在「首次
	// active」與「由 active 掉回 1.0（stale）」各發一封，逐次係數微調不發。放在 tx.Commit 成功
	// 之後才送，避免交易被回滾卻已經發信；mailer 未 wiring（例如測試環境）時直接 no-op。
	//
	// 對抗式審查修正：Recompute 對「全體使用者」無條件執行（規格刻意的影子模式：hidden 時只算不
	// 套），但站內信只該發給校正「真的會套用」的人——用 resolveApplyEntry（不含 super_admin 旁路，
	// 與 EffectiveFactor 同一判斷）先過濾，否則非白名單使用者會收到「已自動校正」的信，但
	// EffectiveFactor 對他們恆回 1.0、Dashboard 卡片也不顯示、GET /me/gps-calib 還會 403——通知一個
	// 使用者看不到也沒生效的功能。resolveUserIdentity 查詢失敗保守跳過，不因此擋住既有信件邏輯。
	//
	// 第二道閘門（使用者需求：「不應該發送給任何人，除了指定帳號」）：resolveApplyEntry 綁的是「校正
	// 對他生效」，一旦 gps_calib_entry_state 改成 open（正式全站開放）全體都會收信；因此再 AND 一份
	// **獨立的** gps_calib_notify_whitelist，空字串＝一封都不發（fail-closed，見 notifyAllowed）。
	if changed {
		if email, code, _, err := resolveUserIdentity(ctx, db, userID); err == nil {
			applyEntry := resolveApplyEntry(ctx, db, email, code)
			notifyList := appsettings.GetString(ctx, db, NotifyWhitelistKey, "")
			switch {
			case notifyAllowed(applyEntry, notifyList, email, code):
				notifyStatusChange(ctx, userID, prevStatus, newStatus, newFactor)
			case applyEntry == "shown" && strings.TrimSpace(notifyList) == "":
				// 對抗式審查修正（medium finding）：fail-closed 的代價是「設定根本不存在」與「刻意
				// 關掉通知」長得一模一樣。校正對他確實生效、狀態也真的變了，卻因為名單是空的而不
				// 發信時留一行 warn——migration 155 尚未套用（app_settings 查無 key → GetString 回
				// 空字串）就是走這條路徑，否則整件事完全靜默、沒有任何線索可查。
				log.Warn().Str("user", userID).Str("setting", NotifyWhitelistKey).
					Msg("gpscalib: 通知白名單為空（migration 155 是否已套用？），本次狀態變更不發站內信")
			}
		}
	}

	return nil
}

// nullIfEmpty：空字串轉 SQL NULL（window_fingerprint 允許 NULL，代表「這欄從未有意義過」，跟空
// 字串是同一語意但欄位型別用 NULL 表達更乾淨，不必額外判斷空字串）。
func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// --- 站內信通知（見 Recompute 尾端呼叫）：獨立小介面 + 套件級注入，比照 race.MailInserter /
// push.MailInserter 既有慣例（見 internal/race/service.go、internal/push/push.go）。gpscalib 的
// Recompute 是自由函式（不是綁在某個 struct 上），沒有天然的欄位可以放依賴，因此用套件級變數＋
// SetMailInserter 在 main.go 啟動時晚繫結（mailHandler 建構需要 wsManager，兩者初始化順序與
// gpscalib.NewHandler 無關，但沿用同一 setter 慣例最簡單）。未呼叫 SetMailInserter（測試環境／
// 尚未 wiring）時 notifyStatusChange 直接跳過，不影響 Recompute 本身——比照
// payment.BindHandler.sendRenewalMail 的取捨。---

// MailInserter 站內信最小介面，由 mail.Handler 實作。
type MailInserter interface {
	InsertForUsers(ctx context.Context, userIDs []string, level, title, body, url string) (int, error)
}

var mailer MailInserter

// SetMailInserter 見上方套件註解；main.go 於 gpscalib.NewHandler 之後呼叫一次即可。
func SetMailInserter(m MailInserter) { mailer = m }

// notifyStatusChange 依規格 §3.4 判斷是否要發站內信：非 active→active（首次啟用）、
// active→stale（太久沒新配對，係數已退回 1.0）各發一封；其餘狀態轉換（含 warming/unstable
// 之間互轉、active 內部係數微調）不發，避免騷擾。
func notifyStatusChange(ctx context.Context, userID, prevStatus, newStatus string, newFactor float64) {
	if mailer == nil {
		return
	}
	var title, body string
	switch {
	case prevStatus != "active" && newStatus == "active":
		title = "GPS 距離校正已啟用"
		body = fmt.Sprintf("系統依你手錶（Strava/Garmin/COROS）紀錄比對，偵測到 App 跑步距離有系統性偏差，已自動校正，目前係數 ×%.4f（只會讓距離變短，不影響原始紀錄）。可在個人資料頁查看詳情或關閉。", newFactor)
	case prevStatus == "active" && newStatus == "stale":
		title = "GPS 距離校正暫停中"
		body = "太久沒有新的手錶紀錄可比對，距離校正已暫時退回原始值；之後若有新的同步資料，系統會自動恢復校正。"
	default:
		return
	}
	if _, err := mailer.InsertForUsers(ctx, []string{userID}, "normal", title, body, ""); err != nil {
		log.Error().Err(err).Str("user", userID).Msg("gpscalib: notify status change failed")
	}
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// --- RecomputeAsync：per-user debounce（5 秒無新觸發才真正跑），避免一次 Strava 同步 30 筆
// 活動、或 GPS 上傳緊接著 Strava webhook 進來時重複整套重算。timer 逾時後在獨立 goroutine 執行，
// 15 秒逾時保護（Recompute 本身是單一 user 的小交易，正常應遠低於此）。---

var recomputeTimers sync.Map // userID -> *time.Timer

func RecomputeAsync(db *pgxpool.Pool, userID string) {
	if userID == "" {
		return
	}
	if v, ok := recomputeTimers.Load(userID); ok {
		if timer, ok := v.(*time.Timer); ok {
			timer.Stop()
		}
	}
	timer := time.AfterFunc(5*time.Second, func() {
		recomputeTimers.Delete(userID)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := Recompute(ctx, db, userID, "recompute", "system"); err != nil {
			log.Error().Err(err).Str("user", userID).Msg("gpscalib: recompute failed")
		}
	})
	recomputeTimers.Store(userID, timer)
}

// SetEnabled 使用者自行開關（只向前生效——不追溯改寫已入帳的活動）。
func SetEnabled(ctx context.Context, db *pgxpool.Pool, userID string, enabled bool) error {
	reason := "disable"
	if enabled {
		reason = "enable"
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `UPDATE user_gps_calib SET enabled=$2, updated_at=now() WHERE user_id=$1`, userID, enabled)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		// 尚無列（從未有過候選配對）：開關本身仍要留存，建立一列最小狀態的紀錄。
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_gps_calib (user_id, enabled, status, updated_at) VALUES ($1,$2,'warming',now())
			ON CONFLICT (user_id) DO UPDATE SET enabled=EXCLUDED.enabled, updated_at=now()`, userID, enabled); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_gps_calib_log (user_id, version, reason, actor)
		SELECT user_id, version, $2, 'user' FROM user_gps_calib WHERE user_id=$1`, userID, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdminFreeze 後台釘住係數（factor 必須落在 [ClampLo, ClampHi]，比照估計器只准向下的限制）。
func AdminFreeze(ctx context.Context, db *pgxpool.Pool, userID string, factor float64, actor string) error {
	if factor < ClampLo || factor > ClampHi {
		return errors.New("factor 超出允許範圍")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var prevFactor float64
	var version int
	err = tx.QueryRow(ctx, `SELECT factor, version FROM user_gps_calib WHERE user_id=$1 FOR UPDATE`, userID).Scan(&prevFactor, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_gps_calib (user_id, factor, status, frozen_factor, version, updated_at)
			VALUES ($1,$2,'frozen',$2,1,now())`, userID, factor); err != nil {
			return err
		}
		version = 1
	} else if err != nil {
		return err
	} else {
		version++
		if _, err := tx.Exec(ctx, `
			UPDATE user_gps_calib SET status='frozen', frozen_factor=$2, factor=$2, version=$3, updated_at=now()
			WHERE user_id=$1`, userID, factor, version); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_gps_calib_log (user_id, version, factor_before, factor_after, status, reason, actor)
		VALUES ($1,$2,$3,$4,'frozen','admin_freeze',$5)`, userID, version, prevFactor, factor, actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdminUnfreeze 解除後台釘住，並立即重算讓係數回到資料驅動的真實結果（而非停留在 frozen 狀態
// 直到下次自然觸發，見 T1-T4；後台操作屬低頻，這裡直接同步重算不必比照 RecomputeAsync 走 debounce）。
func AdminUnfreeze(ctx context.Context, db *pgxpool.Pool, userID, actor string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	var prevFactor float64
	var version int
	err = tx.QueryRow(ctx, `SELECT factor, version FROM user_gps_calib WHERE user_id=$1 AND status='frozen' FOR UPDATE`, userID).Scan(&prevFactor, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(ctx)
		return nil // 本來就不是 frozen：no-op
	}
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	version++
	// 對抗式審查修正：factor 一併重設回 1.0（比照 AdminReset 的既有作法）——沿用管理員釘住值當
	// prevFactor 會被下面 Recompute 的 ±2% 步幅遲滯卡住（lnK = ln(prevFactor) + clamp(...,±2%)），
	// 且一旦這次 publish 出的 window_fingerprint 沒變，後續每次重算都會命中「同一指紋 → 沿用
	// prevFactor」的凍結分支（見 Recompute），係數永遠回不到資料真值，必須等全新的配對進來才會
	// 再前進一步。從 1.0 起算才是「回到資料驅動的真實結果」，符合本函式的既有註解意圖。
	if _, err := tx.Exec(ctx, `
		UPDATE user_gps_calib SET status='warming', factor=1.0, frozen_factor=NULL, version=$2, updated_at=now() WHERE user_id=$1`,
		userID, version); err != nil {
		tx.Rollback(ctx)
		return err
	}
	// 對抗式審查修正（low finding）：factor_after 一併寫入（＝上面 UPDATE 實際寫進去的 1.0）。
	// 少了它，後台「歷史校正變化」這一列會顯示成「0.9500 → —」，而係數軌跡折線圖只取
	// factor_after 非 null 的點，整個漏掉這次由 0.95 掉回 1.0 的變動；若解凍後重算結果仍是
	// warming/1.0，Recompute 的 changed 為 false 不會補寫另一列，這個斷點就永久留在歷史裡。
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_gps_calib_log (user_id, version, factor_before, factor_after, status, reason, actor)
		VALUES ($1,$2,$3,1.0,'warming','admin_unfreeze',$4)`, userID, version, prevFactor, actor); err != nil {
		tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return Recompute(ctx, db, userID, "recompute", actor)
}

// AdminReset 後台重設：既有配對全標記 superseded（不再參與估計/顯示為採用），係數回到
// 1.0/warming，reset_at=now() 讓下次 Recompute 只吃「之後」的新配對（只向前生效，比照整體
// 「不回溯」政策）。
func AdminReset(ctx context.Context, db *pgxpool.Pool, userID, actor string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var prevFactor float64
	var version int
	err = tx.QueryRow(ctx, `SELECT factor, version FROM user_gps_calib WHERE user_id=$1 FOR UPDATE`, userID).Scan(&prevFactor, &version)
	isNew := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !isNew {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE gps_calib_pairs SET accepted=FALSE, reject_reason='superseded', updated_at=now() WHERE user_id=$1`, userID); err != nil {
		return err
	}
	version++
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_gps_calib (user_id, ref_source, factor, log_mu, sigma, n_pairs, n_eff, eff_weight,
		                             status, frozen_factor, reset_at, last_pair_at, computed_at, version, window_fingerprint, updated_at)
		VALUES ($1,'',1.0,0,NULL,0,0,0,'warming',NULL,now(),NULL,now(),$2,NULL,now())
		ON CONFLICT (user_id) DO UPDATE SET
			ref_source='', factor=1.0, log_mu=0, sigma=NULL, n_pairs=0, n_eff=0, eff_weight=0,
			status='warming', frozen_factor=NULL, reset_at=now(), last_pair_at=NULL, computed_at=now(),
			version=EXCLUDED.version, window_fingerprint=NULL, updated_at=now()`, userID, version); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_gps_calib_log (user_id, version, factor_before, factor_after, status, reason, actor)
		VALUES ($1,$2,$3,1.0,'warming','reset',$4)`, userID, version, prevFactor, actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
