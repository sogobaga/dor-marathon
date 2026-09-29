//go:build integration

// 需要真實 Postgres（Neon 暫存分支，見 docs/gps/GPS_START_GATE_RAWLOG_CONTRACT.md 契約 C：
// 「單元＋Neon 暫存分支上套 195 後的整合測試，測完刪分支並二次確認」）才能跑的整合測試——本 repo
// 沒有 pgxmock/testcontainers 基礎設施（比照 internal/reward/repository_test.go TestSpinConcurrency
// 的既有慣例），故獨立成一個 build tag，預設 `go test ./...` 不會編譯/執行這個檔案，只在
// `go test -tags=integration ./internal/gpsrawlog/...` 且設定 DOR_TEST_DATABASE_URL 時才跑。
package gpsrawlog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
)

// setupPool 用 DOR_TEST_DATABASE_URL（指向 Neon 暫存分支，已套用 migrations/195）建立連線池；
// 未設定時整個檔案的測試一律跳過。
func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DOR_TEST_DATABASE_URL not set; skipping Neon 整合測試")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// createTestUser／createTestRun：最小必要欄位（見 migrations/001_init.sql users、
// migrations/025_gps_runs.sql gps_runs 的 NOT NULL 欄位），直接下 SQL 建測試資料，不經過
// application 邏輯（這裡只測 gpsrawlog 套件本身，不是整條註冊/上傳鏈路）。
func createTestUser(t *testing.T, ctx context.Context, db *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	handle := "gpsrawlog_" + randSuffix()
	err := db.QueryRow(ctx, `
		INSERT INTO users (email, handle, name, password_hash) VALUES ($1,$2,'Test','x')
		RETURNING id::text`, email, handle).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, id)
	})
	return id
}

func createTestRun(t *testing.T, ctx context.Context, db *pgxpool.Pool, userID string) string {
	t.Helper()
	var id string
	now := time.Now()
	err := db.QueryRow(ctx, `
		INSERT INTO gps_runs (user_id, started_at, ended_at, distance_km, duration_s, avg_pace_s,
		                      point_count, km_paces, client_version)
		VALUES ($1::uuid, $2, $3, 5.0, 1800, 360, 100, '{300,360,300}', 'v1.2.999')
		RETURNING id::text`, userID, now.Add(-30*time.Minute), now).Scan(&id)
	if err != nil {
		t.Fatalf("create test run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM gps_runs WHERE id=$1::uuid`, id)
	})
	return id
}

func randSuffix() string {
	return time.Now().Format("150405.000000")
}

func samplePayload() UploadPayload {
	return UploadPayload{
		V:      1,
		Fields: []string{"t_ms", "lat", "lng", "acc", "speed", "heading", "code"},
		Rows: [][]any{
			{float64(1000), 25.033, 121.5645, 8.5, 2.7, 90.0, "a"},
			{float64(2000), 25.0331, 121.5646, 45.0, nil, nil, "p"},
		},
		Truncated:     false,
		ClientVersion: "v1.2.999",
	}
}

// TestIntegration_OwnershipAndUpload：ownership 檢查 + 白名單 403 + upsert 覆寫，見契約 B 全流程。
func TestIntegration_OwnershipAndUpload(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()

	ownerEmail := "gpsrawlog-owner-" + randSuffix() + "@example.com"
	otherEmail := "gpsrawlog-other-" + randSuffix() + "@example.com"
	ownerID := createTestUser(t, ctx, pool, ownerEmail)
	otherID := createTestUser(t, ctx, pool, otherEmail)
	runID := createTestRun(t, ctx, pool, ownerID)

	// 白名單只放 ownerEmail（覆寫預設值，避免撞到正式白名單設定；測完還原）。
	setWhitelist(t, ctx, pool, ownerEmail)

	// 1) ownership：非本人（otherID）上傳自己並不擁有的 run → ErrRunNotFound。
	if err := Upload(ctx, pool, runID, otherID, otherEmail, samplePayload()); err != ErrRunNotFound {
		t.Fatalf("expected ErrRunNotFound for non-owner, got %v", err)
	}

	// 2) run 不存在 → 同樣 ErrRunNotFound（不洩漏「id 格式合法但不存在」與「屬於別人」的差異）。
	if err := Upload(ctx, pool, "00000000-0000-0000-0000-000000000000", ownerID, ownerEmail, samplePayload()); err != ErrRunNotFound {
		t.Fatalf("expected ErrRunNotFound for missing run, got %v", err)
	}

	// 3) 本人但不在白名單 → ErrNotAllowed（403 raw_log_not_allowed）。
	notWhitelistedEmail := "gpsrawlog-nw-" + randSuffix() + "@example.com"
	if err := Upload(ctx, pool, runID, ownerID, notWhitelistedEmail, samplePayload()); err != ErrNotAllowed {
		t.Fatalf("expected ErrNotAllowed for non-whitelisted email, got %v", err)
	}

	// 4) 本人且在白名單 → 成功寫入。
	if err := Upload(ctx, pool, runID, ownerID, ownerEmail, samplePayload()); err != nil {
		t.Fatalf("expected upload success, got %v", err)
	}
	var pointCount int
	if err := pool.QueryRow(ctx, `SELECT point_count FROM gps_run_raw_points WHERE run_id=$1::uuid`, runID).Scan(&pointCount); err != nil {
		t.Fatalf("read back point_count: %v", err)
	}
	if pointCount != 2 {
		t.Fatalf("expected point_count=2, got %d", pointCount)
	}

	// 5) 重送同一 run＝覆寫（ON CONFLICT DO UPDATE），不是新增第二列。
	p2 := samplePayload()
	p2.Rows = p2.Rows[:1] // 只留 1 列
	if err := Upload(ctx, pool, runID, ownerID, ownerEmail, p2); err != nil {
		t.Fatalf("expected re-upload success, got %v", err)
	}
	var rowCount, pointCount2 int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*), MAX(point_count) FROM gps_run_raw_points WHERE run_id=$1::uuid`, runID).Scan(&rowCount, &pointCount2); err != nil {
		t.Fatalf("read back after re-upload: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly 1 row after re-upload (upsert), got %d", rowCount)
	}
	if pointCount2 != 1 {
		t.Fatalf("expected point_count=1 after re-upload overwrite, got %d", pointCount2)
	}

	// 6) 後台 Get：附帶 run 基本資訊（distance_km/duration_s/km_paces/client_version）。
	rec, err := Get(ctx, pool, runID)
	if err != nil {
		t.Fatalf("admin get: %v", err)
	}
	if rec.Run.DistanceKm != 5.0 || rec.Run.DurationS != 1800 {
		t.Fatalf("unexpected run summary: %+v", rec.Run)
	}
	if len(rec.Run.KmPaces) != 3 {
		t.Fatalf("expected 3 km_paces, got %v", rec.Run.KmPaces)
	}

	// 清理本測試寫入的 raw points 列（run/user 由上面的 t.Cleanup 級聯刪除，這裡先手動清一次避免
	// 下一段 retention 測試撈到不相關的列）。
	_, _ = pool.Exec(ctx, `DELETE FROM gps_run_raw_points WHERE run_id=$1::uuid`, runID)
}

// TestIntegration_AdminGetNotFound：查無資料回 ErrRunNotFound。
func TestIntegration_AdminGetNotFound(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()
	if _, err := Get(ctx, pool, "00000000-0000-0000-0000-000000000000"); err != ErrRunNotFound {
		t.Fatalf("expected ErrRunNotFound, got %v", err)
	}
}

// TestIntegration_PurgeExpired：30 天保存期限——超過的列被刪、未超過的留著。
func TestIntegration_PurgeExpired(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()

	email := "gpsrawlog-retention-" + randSuffix() + "@example.com"
	userID := createTestUser(t, ctx, pool, email)
	oldRunID := createTestRun(t, ctx, pool, userID)
	freshRunID := createTestRun(t, ctx, pool, userID)

	sp := storedPoints{V: 1, Fields: []string{"t_ms"}, Rows: [][]any{{float64(1)}}}
	raw, _ := json.Marshal(sp)

	// 舊列：created_at 直接寫回 31 天前（繞過 DEFAULT now()，模擬「早就過保存期限」）。
	if _, err := pool.Exec(ctx, `
		INSERT INTO gps_run_raw_points (run_id, user_id, point_count, points, created_at)
		VALUES ($1::uuid,$2::uuid,1,$3, now() - interval '31 days')`, oldRunID, userID, raw); err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	// 新列：今天。
	if _, err := pool.Exec(ctx, `
		INSERT INTO gps_run_raw_points (run_id, user_id, point_count, points, created_at)
		VALUES ($1::uuid,$2::uuid,1,$3, now())`, freshRunID, userID, raw); err != nil {
		t.Fatalf("insert fresh row: %v", err)
	}

	purged, err := PurgeExpired(ctx, pool)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if purged < 1 {
		t.Fatalf("expected at least 1 purged row, got %d", purged)
	}

	var stillThere bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gps_run_raw_points WHERE run_id=$1::uuid)`, oldRunID).Scan(&stillThere); err != nil {
		t.Fatalf("check old row: %v", err)
	}
	if stillThere {
		t.Fatal("old row should have been purged")
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gps_run_raw_points WHERE run_id=$1::uuid)`, freshRunID).Scan(&stillThere); err != nil {
		t.Fatalf("check fresh row: %v", err)
	}
	if !stillThere {
		t.Fatal("fresh row should NOT have been purged")
	}
	_, _ = pool.Exec(ctx, `DELETE FROM gps_run_raw_points WHERE run_id=$1::uuid`, freshRunID)
}

// TestIntegration_HandlerAuthOwnership403：走完整 HTTP handler（含 chi 路由的 runId 參數解析），
// 驗證契約 B 明列的狀態碼：未登入 401／run 不屬於本人 404／不在白名單 403 raw_log_not_allowed／
// 成功 204。比照 internal/activity handler 慣例，用 context.WithValue 直接注入 auth.CtxKeyUserID
// （不需要真的簽發 JWT——RequireAuth 中介層本身不是這個套件的職責）。
func TestIntegration_HandlerAuthOwnership403(t *testing.T) {
	pool := setupPool(t)
	ctx := context.Background()
	h := NewHandler(pool)

	ownerEmail := "gpsrawlog-h-owner-" + randSuffix() + "@example.com"
	otherEmail := "gpsrawlog-h-other-" + randSuffix() + "@example.com"
	ownerID := createTestUser(t, ctx, pool, ownerEmail)
	otherID := createTestUser(t, ctx, pool, otherEmail)
	runID := createTestRun(t, ctx, pool, ownerID)
	setWhitelist(t, ctx, pool, ownerEmail)

	doUpload := func(userID string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/"+runID+"/raw-points", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, userID))
		rw := httptest.NewRecorder()
		h.Router().ServeHTTP(rw, req)
		return rw
	}

	payloadJSON := `{"v":1,"fields":["t_ms","lat","lng","acc","speed","heading","code"],
		"rows":[[1000,25.033,121.5645,8.5,2.7,90.0,"a"]],"truncated":false,"client_version":"v1.2.999"}`

	// 未登入：context 沒有 userID。
	req := httptest.NewRequest(http.MethodPost, "/"+runID+"/raw-points", strings.NewReader(payloadJSON))
	rw := httptest.NewRecorder()
	h.Router().ServeHTTP(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated, got %d: %s", rw.Code, rw.Body.String())
	}

	// 登入但不是這趟 run 的擁有者 → 404。
	if rw := doUpload(otherID, payloadJSON); rw.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner, got %d: %s", rw.Code, rw.Body.String())
	}

	// 是擁有者但不在白名單（先把白名單改成只有 otherEmail，讓 ownerID 落到「本人但非白名單」這條路徑）。
	setWhitelist(t, ctx, pool, otherEmail)
	rw = doUpload(ownerID, payloadJSON)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-whitelisted owner, got %d: %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "raw_log_not_allowed") {
		t.Fatalf("expected body to mention raw_log_not_allowed, got %s", rw.Body.String())
	}

	// 換回白名單放行 → 204。
	setWhitelist(t, ctx, pool, ownerEmail)
	rw = doUpload(ownerID, payloadJSON)
	if rw.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on success, got %d: %s", rw.Code, rw.Body.String())
	}

	// 後台 GET（不經過 RequireSuper——那層中介層是 main.go 掛的，這裡只測 handler 本身）。
	getReq := httptest.NewRequest(http.MethodGet, "/admin/gps-runs/"+runID+"/raw-points", nil)
	getReq = withChiURLParam(getReq, "runId", runID)
	getRw := httptest.NewRecorder()
	h.AdminGetRawPoints(getRw, getReq)
	if getRw.Code != http.StatusOK {
		t.Fatalf("expected 200 from admin get, got %d: %s", getRw.Code, getRw.Body.String())
	}

	_, _ = pool.Exec(ctx, `DELETE FROM gps_run_raw_points WHERE run_id=$1::uuid`, runID)
}

// withChiURLParam 手動塞入 chi 的 URL 參數（AdminGetRawPoints 不是掛在 chi 子路由裡呼叫，直接呼叫
// handler 函式本身時 chi.URLParam 讀不到路徑參數，需要這樣手動組一個帶 RouteContext 的 request，
// 比照 main.go 實際掛法：admin GET 是直接註冊的單一路由，不像 member POST 那樣經過 h.Router()）。
func withChiURLParam(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// setWhitelist 覆寫 gps_raw_log_whitelist 設定值（測完還原成套用前的值，避免污染分支上其餘測試）。
// 直接下 SQL 寫 app_settings（繞過 appsettings.Handler.Set），必須手動呼叫
// appsettings.InvalidateCache()——比照 internal/profile.PutCheerLayout 同款直寫 app_settings 的既有
// 慣例（見 appsettings.go InvalidateCache 註解）：appsettings.GetString 有 60 秒 process-local 快取
// （見該套件檔頭註解），不清掉的話同一個 go test process 內後續呼叫會在快取視窗內讀到舊值，跟本測
// 試「連續切換白名單、每次都要立即生效」的假設不符——第一版測試沒清快取就踩到這個坑（403 而非預期
// 的 204），這裡修正並保留這段說明避免下次重蹈覆轍。
func setWhitelist(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) {
	t.Helper()
	var prev *string
	_ = pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, WhitelistKey).Scan(&prev)
	_, err := pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`, WhitelistKey, email)
	if err != nil {
		t.Fatalf("set whitelist: %v", err)
	}
	appsettings.InvalidateCache()
	t.Cleanup(func() {
		ctx := context.Background()
		if prev == nil {
			_, _ = pool.Exec(ctx, `DELETE FROM app_settings WHERE key=$1`, WhitelistKey)
		} else {
			_, _ = pool.Exec(ctx, `UPDATE app_settings SET value=$2 WHERE key=$1`, WhitelistKey, *prev)
		}
		appsettings.InvalidateCache()
	})
}
