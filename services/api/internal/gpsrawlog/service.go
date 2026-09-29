package gpsrawlog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
)

// WhitelistKey app_settings key（見契約 B「白名單／開關」段：「逗號分隔 email，缺鍵＝
// sogobaga@gmail.com」）。獨立於全站泛用的 *_entry_whitelist 家族（見 internal/appsettings.specs
// 的 isWhitelist 那一批）——這個白名單不控制「入口顯不顯示」，是「要不要真的把定位點寫進 DB」的
// 資料揭露閘門，語意上更接近 internal/gpscalib.NotifyWhitelistKey（獨立、fail-closed）。
const WhitelistKey = "gps_raw_log_whitelist"

// defaultWhitelist 缺鍵時的預設值（見契約 B）。
const defaultWhitelist = "sogobaga@gmail.com"

// retentionDays 保存期限（見契約 B「後端」段：「保存期限：...30 days」）。
const retentionDays = 30

// Allowed 這個 email 是否在白名單內——刻意「無 super_admin 旁路」（契約 B 原話：「超管要看資料走
// 後台端點，不代表自己的跑步要被記錄」），與全站其餘 *_entry 系列的 resolveEntry（多半對
// super_admin 放行）刻意不同，故獨立實作不共用那份邏輯。供 Upload 寫入端與
// internal/profile.Dashboard 的 gps_raw_log 欄位共用同一份判定。
func Allowed(ctx context.Context, db *pgxpool.Pool, email string) bool {
	wl := appsettings.GetString(ctx, db, WhitelistKey, defaultWhitelist)
	return whitelisted(wl, email)
}

// whitelisted 純函式，方便單元測試（見 service_test.go）：逗號分隔（亦接受換行/分號/空白，比照
// 全站既有 whitelist 慣例，見 internal/profile/membership.go personalWhitelisted），大小寫不敏感、
// 前後空白皆 trim。空 email 恆回 false。
func whitelisted(list, email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, tok := range strings.FieldsFunc(list, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		if strings.ToLower(strings.TrimSpace(tok)) == email {
			return true
		}
	}
	return false
}

var (
	// ErrRunNotFound：run 不存在或不屬於呼叫者——兩種情況刻意回同一個錯誤（比照 internal/activity
	// GetUserGPSRun 的既有慣例），不讓呼叫端能靠錯誤訊息差異探測「這個 run id 存在但是別人的」。
	ErrRunNotFound = errors.New("run not found")
	// ErrNotAllowed：呼叫者不在白名單內（契約 B：「403 raw_log_not_allowed」）。
	ErrNotAllowed = errors.New("raw_log_not_allowed")
)

// checkRunOwnership 查 run 是否存在且屬於 userID。
func checkRunOwnership(ctx context.Context, db *pgxpool.Pool, runID, userID string) error {
	var owner string
	err := db.QueryRow(ctx, `SELECT user_id::text FROM gps_runs WHERE id=$1`, runID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRunNotFound
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrRunNotFound
	}
	return nil
}

// Upload 驗證通過後的寫入（POST /me/gps-runs/{runId}/raw-points）：ownership → 白名單 → upsert
// （同一 run 重送＝覆寫，見契約 B）。呼叫端（handler）已完成 payload.Validate()；這裡只做
// DB 相關的檢查與寫入。email 由呼叫端傳入（handler 已查過 users 表，不在這裡重複查一次）。
func Upload(ctx context.Context, db *pgxpool.Pool, runID, userID, email string, p UploadPayload) error {
	if err := checkRunOwnership(ctx, db, runID, userID); err != nil {
		return err
	}
	if !Allowed(ctx, db, email) {
		return ErrNotAllowed
	}
	sp := storedPoints{V: p.V, Fields: p.Fields, Rows: p.Rows, Truncated: p.Truncated}
	raw, err := json.Marshal(sp)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO gps_run_raw_points (run_id, user_id, point_count, points, client_version, created_at)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),NOW())
		ON CONFLICT (run_id) DO UPDATE SET
			point_count=EXCLUDED.point_count, points=EXCLUDED.points,
			client_version=EXCLUDED.client_version, created_at=NOW()`,
		runID, userID, len(p.Rows), raw, p.ClientVersion)
	return err
}

// Get 後台 GET /admin/gps-runs/{runId}/raw-points：LEFT JOIN gps_runs 附上基本資訊（見 Record 註解）。
// 查無 gps_run_raw_points 列（尚未上傳過，或非白名單帳號從未記錄）回 ErrRunNotFound。
func Get(ctx context.Context, db *pgxpool.Pool, runID string) (*Record, error) {
	var rec Record
	var pointsRaw []byte
	var runDistanceKm *float64
	var runDurationS *int
	var runKmPaces []int
	var runClientVersion *string
	err := db.QueryRow(ctx, `
		SELECT p.run_id::text, p.point_count, p.points, COALESCE(p.client_version,''), p.created_at,
		       r.distance_km, r.duration_s, r.km_paces, r.client_version
		FROM gps_run_raw_points p
		LEFT JOIN gps_runs r ON r.id = p.run_id
		WHERE p.run_id = $1`, runID).
		Scan(&rec.RunID, &rec.PointCount, &pointsRaw, &rec.ClientVersion, &rec.CreatedAt,
			&runDistanceKm, &runDurationS, &runKmPaces, &runClientVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	rec.Points = json.RawMessage(pointsRaw)
	if runDistanceKm != nil {
		rec.Run.DistanceKm = *runDistanceKm
	}
	if runDurationS != nil {
		rec.Run.DurationS = *runDurationS
	}
	rec.Run.KmPaces = runKmPaces
	if runClientVersion != nil {
		rec.Run.ClientVersion = *runClientVersion
	}
	return &rec, nil
}

// retentionDeleteSQL 是 PurgeExpired 的純函式核心：組出 DELETE 語句與保存天數參數，方便單元測試
// 斷言 SQL 文字與天數本身，不必真的連 DB（見 service_test.go）。用 make_interval(days => $1) 而非
// 字串拼接組 interval 字面值，避免 SQL injection 疑慮（雖然 retentionDays 是編譯期常數，仍比照
// 全站慣例一律走參數化查詢）。
func retentionDeleteSQL() (string, int) {
	return `DELETE FROM gps_run_raw_points WHERE created_at < now() - make_interval(days => $1)`, retentionDays
}

// PurgeExpired 刪除超過保存期限（見 retentionDays）的原始定位點記錄，回傳刪除筆數。供
// internal/ops 每日報告排程呼叫（見契約 B「保存期限」段：「不另開週期性 DB 查詢」，掛在既有排程，
// 見 internal/ops RunDailyReportLoop／buildDailyReportData）。表尚未建立（migration 195 未套用）
// 時查詢會回錯誤——呼叫端 warn 後略過即可，不影響報告其餘段落（比照 einvoice/wearable 兩段的既有慣例）。
func PurgeExpired(ctx context.Context, db *pgxpool.Pool) (int, error) {
	sql, days := retentionDeleteSQL()
	ct, err := db.Exec(ctx, sql, days)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}
