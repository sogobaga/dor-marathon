// Package integration 第三方運動數據整合（OAuth token 儲存 + 活動匯入）。
package integration

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// Connection 使用者對某 provider 的連線
type Connection struct {
	ID             string
	UserID         string
	Provider       string
	ProviderUserID string
	AccessToken    string
	RefreshToken   string
	ExpiresAt      time.Time
	Scope          string
	AthleteName    string
	// ConnectedAt=本次連接建立時間（user_integrations.created_at）。中斷連接會刪除整列（見 Delete），
	// 重連即為全新列→created_at 自然是重連當下；Save 的 ON CONFLICT 也一併重設 created_at=NOW()，
	// 涵蓋「未先中斷就重新授權」的情形。作為 Strava 匯入的起算 floor：只抓「連接當下之後」的活動，
	// 避免一連接就把整段歷史里程灌入導致 EXP/DP 暴衝（使用者定案：從串接當下起計算）。
	ConnectedAt time.Time
	// Via：連線管道，'direct'（本站既有 OAuth，預設）或 'terra'（透過 Terra 聚合器，見 migrations/165）。
	// (user_id, provider) 唯一鍵兩種管道共用——同品牌只會有一條連線，後連上的一方連 via 一併覆蓋
	// （見 Save/SaveTerra）。
	Via string
}

// --- token 加密（graceful、漸進遷移）---
//
// STRAVA_TOKEN_KEY 未設定（空字串）或格式不對 → stravaTokenKey() 回傳 nil，encrypt/decrypt 全部
// 原樣直通（零風險 fallback，完全維持現狀明碼，不影響任何既有連線）。
// 設定後：新寫入（Save/UpdateTokens）一律加密、加 "enc:" 前綴標記；讀取（scanConn）看前綴——
// 有前綴才解密，沒有前綴當 legacy 明碼直接用。既有明碼連線在下次 token refresh（每 6 小時，見
// StravaHandler.tokenForUser）時自然輪換成密文，不需要一次性批次遷移。
// 不快取金鑰（呼叫頻率低，僅連線建立/token 刷新時才會用到，重新讀取 env 的成本可忽略），
// 好處是設定變更後不必重啟即生效、也方便測試以 t.Setenv 切換情境。
const encPrefix = "enc:"

// parseTokenKey 解析 STRAVA_TOKEN_KEY 原始字串（32 bytes，接受 hex 或 base64 編碼）：
// 回傳 (key, invalid)。raw 為空＝未設定→(nil,false)；有設定但格式不對→(nil,true)；有效→(key,false)。
// 不記 log（供 TokenKeyConfigured／EncryptTokenStrict 這類頻繁或需要 fail-closed 的呼叫端共用，
// 見 tokenkey.go）；stravaTokenKey 保留原本「無效就 warn 一次一次」的行為。
func parseTokenKey(raw string) (key []byte, invalid bool) {
	if raw == "" {
		return nil, false
	}
	if k, err := hex.DecodeString(raw); err == nil && len(k) == 32 {
		return k, false
	}
	if k, err := base64.StdEncoding.DecodeString(raw); err == nil && len(k) == 32 {
		return k, false
	}
	return nil, true
}

// stravaTokenKey 解析 STRAVA_TOKEN_KEY（32 bytes，接受 hex 或 base64 編碼）。
func stravaTokenKey() []byte {
	k, invalid := parseTokenKey(os.Getenv("STRAVA_TOKEN_KEY"))
	if invalid {
		log.Warn().Msg("STRAVA_TOKEN_KEY 已設定但不是有效的 32 bytes hex/base64 金鑰，token 將繼續以明碼儲存")
	}
	return k
}

// encryptToken AES-256-GCM 加密；金鑰未設定/無效或輸入為空字串時原樣回傳（明碼 fallback）。
func encryptToken(plain string) string {
	key := stravaTokenKey()
	if len(key) == 0 || plain == "" {
		return plain
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		log.Error().Err(err).Msg("strava token encrypt: cipher init failed，本次以明碼儲存")
		return plain
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Error().Err(err).Msg("strava token encrypt: gcm init failed，本次以明碼儲存")
		return plain
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		log.Error().Err(err).Msg("strava token encrypt: nonce 產生失敗，本次以明碼儲存")
		return plain
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.RawURLEncoding.EncodeToString(ct)
}

// decryptToken 依前綴判斷：無 "enc:" 前綴 → legacy 明碼直接回傳；有前綴則解密——
// 金鑰未設定或解密失敗一律回傳清楚的 error（絕不把密文當 token 用、也不 panic），
// 交由呼叫端（GetByUser/GetByProviderUser）視為查詢失敗處理。
func decryptToken(stored string) (string, error) {
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, nil
	}
	key := stravaTokenKey()
	if len(key) == 0 {
		return "", fmt.Errorf("token 已加密但 STRAVA_TOKEN_KEY 未設定，無法解密")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted token: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("token cipher init: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("token gcm init: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted token ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt token: %w", err)
	}
	return string(plain), nil
}

// Save upsert（依 user_id+provider）：本站既有 OAuth 直連流程專用（Strava/COROS）。
// access_token/refresh_token 寫入前經 encryptToken 處理（未設金鑰時為 no-op，回傳原字串）。
// via 恆寫 'direct'：即使這個 (user_id, provider) 之前是一條 Terra 連線（via='terra'），使用者改走
// 本站直連 OAuth 重新授權時，也要把 via 一併覆蓋成 'direct'，讓 /status 卡片與 DeleteProviderActivities
// 的行為都反映「現在真正在用的管道」（見 migrations/165、SaveTerra 的對稱處理）。
func (r *Repository) Save(ctx context.Context, c *Connection) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_integrations
			(user_id, provider, provider_user_id, access_token, refresh_token, expires_at, scope, athlete_name, via)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'direct')
		ON CONFLICT (user_id, provider) DO UPDATE SET
			provider_user_id = EXCLUDED.provider_user_id,
			access_token     = EXCLUDED.access_token,
			refresh_token    = EXCLUDED.refresh_token,
			expires_at       = EXCLUDED.expires_at,
			scope            = EXCLUDED.scope,
			athlete_name     = EXCLUDED.athlete_name,
			via              = 'direct',
			updated_at       = NOW(),
			created_at       = NOW()`, // 重新授權(即使未先中斷)也重設連接起算點 → 匯入 floor 前移，不倒灌中斷/未連接期間的歷史里程
		c.UserID, c.Provider, c.ProviderUserID, encryptToken(c.AccessToken), encryptToken(c.RefreshToken),
		c.ExpiresAt, c.Scope, c.AthleteName)
	return err
}

// SaveTerra upsert 一條 Terra 聚合器連線（user_id+provider=底層品牌小寫，如 "garmin"）。
// 與 Save 的三個刻意差異：
//  1. via 恆寫 'terra'（即使覆蓋掉同品牌一條既有的 direct 連線，見 Save 對稱處理／migrations/165）。
//  2. access_token/refresh_token 恆空字串：Terra 不會把底層品牌的 OAuth token 曝露給我方，我方也
//     不需要——活動资料由 Terra webhook 推播，不用我方自己拿 token 去品牌 API 拉資料。
//  3. ⚠️ 與 Save 最大的不同：ON CONFLICT 時「不」覆寫 created_at（=ConnectedAt，匯入 floor）。
//     Terra 使用者可能因為手錶重新配對、App 重新授權等原因觸發 auth/user_reauth 事件重連，這些都
//     不是「使用者主動在本站中斷再重連」，floor 不該因此往後移動而漏抓中間這段時間的活動
//     （對稱地：也不該往前移動而倒灌連接前的歷史）——只有全新列（INSERT 分支）才是 NOW()。
func (r *Repository) SaveTerra(ctx context.Context, c *Connection) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_integrations
			(user_id, provider, provider_user_id, access_token, refresh_token, expires_at, scope, athlete_name, via)
		VALUES ($1,$2,$3,'','',$4,$5,'','terra')
		ON CONFLICT (user_id, provider) DO UPDATE SET
			provider_user_id = EXCLUDED.provider_user_id,
			access_token     = '',
			refresh_token    = '',
			expires_at       = EXCLUDED.expires_at,
			scope            = EXCLUDED.scope,
			via              = 'terra',
			updated_at       = NOW()`,
		c.UserID, c.Provider, c.ProviderUserID, c.ExpiresAt, c.Scope)
	return err
}

// ListTerraConnections 回傳某使用者「經 Terra 連線」的全部品牌（via='terra'）。
// 供 /api/v1/integrations/terra/status 用——同品牌若是 direct 連線（如 COROS 官方直連），
// 屬於那張卡片自己的 GetByUser 查詢，不會出現在這裡（見 migrations/165 註解）。
func (r *Repository) ListTerraConnections(ctx context.Context, userID string) ([]*Connection, error) {
	rows, err := r.db.Query(ctx, connCols+` WHERE user_id=$1 AND via='terra'`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Connection
	for rows.Next() {
		c := &Connection{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.Provider, &c.ProviderUserID,
			&c.AccessToken, &c.RefreshToken, &c.ExpiresAt, &c.Scope, &c.AthleteName, &c.ConnectedAt, &c.Via); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListAllTerraConnections 回傳全站經 Terra 連線的清單（via='terra'，不分使用者），依 provider 排序、
// 上限 limit 筆——供每日營運報告「穿戴串接」段落使用（見 ops.WearableReporter／
// TerraHandler.ProviderStatuses）。跟 ListTerraConnections 一樣不需要 decryptConnFields：
// Terra 連線沒有我方需要刷新的 access/refresh token（見 Connection.ConnectedAt 欄位註解）。
func (r *Repository) ListAllTerraConnections(ctx context.Context, limit int) ([]*Connection, error) {
	rows, err := r.db.Query(ctx, connCols+` WHERE via='terra' ORDER BY provider LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Connection
	for rows.Next() {
		c := &Connection{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.Provider, &c.ProviderUserID,
			&c.AccessToken, &c.RefreshToken, &c.ExpiresAt, &c.Scope, &c.AthleteName, &c.ConnectedAt, &c.Via); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UserExists 檢查某 id 是否為既有使用者。Terra 的 webhook/callback 只帶回 reference_id（我方連接
// widget 時塞進去的 DOR user id）這個裸字串，任何人都能偽造 webhook 帶任意 reference_id——
// 寫入 user_integrations 前必須先確認它真的對應一個存在的使用者（否則 FK 會直接報錯，但那是在
// 交易失敗之後才發現；這裡先查一次也讓呼叫端能提早、乾淨地拒絕並記 log）。
func (r *Repository) UserExists(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, userID).Scan(&exists)
	return exists, err
}

// decryptConnFields 就地解密 Connection 的 access/refresh token（scanConn 與 ListByProviderUser 共用）。
func decryptConnFields(c *Connection) error {
	var err error
	if c.AccessToken, err = decryptToken(c.AccessToken); err != nil {
		return fmt.Errorf("decrypt access token: %w", err)
	}
	if c.RefreshToken, err = decryptToken(c.RefreshToken); err != nil {
		return fmt.Errorf("decrypt refresh token: %w", err)
	}
	return nil
}

// scanConn 讀出後以 decryptToken 還原 access_token/refresh_token（legacy 明碼直通、密文則解密）。
func scanConn(row pgx.Row) (*Connection, error) {
	c := &Connection{}
	err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.ProviderUserID,
		&c.AccessToken, &c.RefreshToken, &c.ExpiresAt, &c.Scope, &c.AthleteName, &c.ConnectedAt, &c.Via)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := decryptConnFields(c); err != nil {
		return nil, fmt.Errorf("scan connection %s: %w", c.ID, err)
	}
	return c, nil
}

// COALESCE(via,'direct')：欄位本身是 NOT NULL DEFAULT 'direct'（migrations/165），理論上不會是
// NULL，這裡仍比照專案慣例（COALESCE-safe）防禦性處理，避免未來欄位定義若被放寬時整批查詢炸開。
const connCols = `SELECT id, user_id, provider, provider_user_id, access_token, refresh_token,
	expires_at, COALESCE(scope,''), COALESCE(athlete_name,''), created_at, COALESCE(via,'direct') FROM user_integrations`

func (r *Repository) GetByUser(ctx context.Context, userID, provider string) (*Connection, error) {
	return scanConn(r.db.QueryRow(ctx, connCols+` WHERE user_id=$1 AND provider=$2`, userID, provider))
}

func (r *Repository) GetByProviderUser(ctx context.Context, provider, providerUserID string) (*Connection, error) {
	return scanConn(r.db.QueryRow(ctx, connCols+` WHERE provider=$1 AND provider_user_id=$2`, provider, providerUserID))
}

// ListByProviderUser 回傳所有綁定同一 provider 帳號（如同一個 Strava athlete id）的連線列。
// (provider, provider_user_id) 沒有唯一約束（見 migrations/014_integrations.sql，只建了一般索引，
// 未加 UNIQUE），同一個 Strava 帳號理論上可能被多個 DOR 帳號各自連接。撤權事件（webhook 的
// object_type=athlete）是整個 Strava 帳號層級的事件，用這個函式才能找出「全部」受影響的連線，
// 只用 GetByProviderUser 取第一筆會漏掉其餘帳號（見 handleDeauthorizeEvent）。
func (r *Repository) ListByProviderUser(ctx context.Context, provider, providerUserID string) ([]*Connection, error) {
	rows, err := r.db.Query(ctx, connCols+` WHERE provider=$1 AND provider_user_id=$2`, provider, providerUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Connection
	for rows.Next() {
		c := &Connection{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.Provider, &c.ProviderUserID,
			&c.AccessToken, &c.RefreshToken, &c.ExpiresAt, &c.Scope, &c.AthleteName, &c.ConnectedAt, &c.Via); err != nil {
			return nil, err
		}
		if err := decryptConnFields(c); err != nil {
			return nil, fmt.Errorf("list connections %s: %w", c.ID, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateTokens 寫入前對 access/refresh 經 encryptToken 處理（見上方「token 加密」說明）。
func (r *Repository) UpdateTokens(ctx context.Context, id, access, refresh string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE user_integrations SET access_token=$1, refresh_token=$2, expires_at=$3, updated_at=NOW() WHERE id=$4`,
		encryptToken(access), encryptToken(refresh), expiresAt, id)
	return err
}

func (r *Repository) Delete(ctx context.Context, userID, provider string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_integrations WHERE user_id=$1 AND provider=$2`, userID, provider)
	return err
}

// ResetPreferredSource 中斷連接（或撤權）後，若使用者的 user_profiles.preferred_data_source 剛好
// 就是這個 provider，改回預設 'gps'（見 profile/dedup.go SetDataSource 的「只能選已連接來源」防呆——
// 斷線後若不重置，偏好會卡在一個已經沒有連線的來源上）。userID/provider 皆為小寫品牌字串，與
// user_integrations.provider、user_profiles.preferred_data_source 同口徑。呼叫端一律只 log 失敗、
// 不擋斷線本身（比照 DeleteProviderActivities 的呼叫模式）。
func (r *Repository) ResetPreferredSource(ctx context.Context, userID, provider string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE user_profiles SET preferred_data_source='gps', updated_at=NOW() WHERE user_id=$1 AND preferred_data_source=$2`,
		userID, provider)
	return err
}

// DeleteProviderActivities 刪除使用者某來源（如 "strava"）已匯入的活動。
//
// 依 Strava API Agreement：使用者中斷連接（本站主動 disconnect）或於 Strava 端撤銷授權
// （webhook object_type=athlete、updates.authorized=false）後，須於 30 天內刪除已匯入的活動資料——
// 這裡兩個路徑都立即刪除，滿足該義務。已發放的 EXP/DP/total_km 一律不追回（使用者拍板：獎勵已發放
// 視為既定事實，不因資料來源事後撤權而剝奪玩家）。
//
// dup_of 有 FK ON DELETE SET NULL（見 migrations/015_activity_dedup.sql），所以直接 DELETE 不會因
// FK 約束報錯；但若某活動曾因為與這批 Strava 活動時間重疊而被標記為良性重複——
// flag_reason='cross_source_duplicate'（見 profile/dedup.go reResolveUser 的跨來源去重邏輯，
// 典型是 App GPS 列）或 flag_reason='multi_device_duplicate'（見 detectDuplicate，典型是使用者
// 同時接 Strava 又接 Terra/Garmin/COROS 時，同一趟被記了兩筆），dup_of 皆指向被刪的 Strava
// 那筆——Strava 活動刪除後，這些標記就已經沒有依據：FK 只會把 dup_of 設回 NULL，flagged/
// flag_reason 不會自動清掉，所以必須在刪除前先手動解除兩者，否則那筆本來正常的活動會被錯誤地
// 永久排除在賽事計算/統計之外。⚠️ 只解除這兩種良性原因；cross_account_duplicate（跨帳號洗資料
// 的作弊標記）刻意不在此列——那是另一個使用者的可疑活動，不因這筆 Strava 資料被刪就該被平反。
// worker 的跨來源去重排程重跑只兜底 cross_source_duplicate 這一種（resolveCrossSourceDups 的
// 自癒邏輯），不涵蓋 multi_device_duplicate，故這裡兩種都必須主動處理，不能只依賴排程自癒。
func (r *Repository) DeleteProviderActivities(ctx context.Context, userID, provider string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE activities
		SET dup_of = NULL,
		    flagged = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN FALSE ELSE flagged END,
		    flag_reason = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN NULL ELSE flag_reason END
		WHERE user_id = $1
		  AND dup_of IN (SELECT id FROM activities WHERE user_id = $1 AND source = $2
		                   AND NOT `+DirectWatchSQL("")+`)`,
		userID, provider); err != nil {
		return fmt.Errorf("clear stale dup_of before provider activity delete: %w", err)
	}
	// 直連手錶匯入的列（COROS MCP：source='coros'＋external_id 'mcp:' 開頭；Garmin 直連：source='garmin'＋'gc:'
	// 開頭）與 Terra／Partner 的同品牌列共用 source 字串：中斷 Terra／Partner 連線時不可連帶刪掉——那批只能由
	// 各自直連的中斷流程刪（COROS：DeleteCorosMcpActivities；Garmin：Garmin 線自己的 Purge）。
	// COROS 部分是 Stage 2 審查發現（從 Terra 切換到直連時一定會踩到）；Garmin 部分為 GA 契約 §2／計畫 S4 新增。
	// 排除條件走 DirectWatchSQL（directwatch.go）單一定義，gpscalib／去重排序／這裡三處一致。
	if _, err := tx.Exec(ctx, `DELETE FROM activities WHERE user_id=$1 AND source=$2
		AND NOT `+DirectWatchSQL(""), userID, provider); err != nil {
		return fmt.Errorf("delete provider activities: %w", err)
	}
	return tx.Commit(ctx)
}

// DeleteCorosMcpActivities 刪除使用者「經 COROS MCP 直連匯入」的活動（source='coros' AND external_id LIKE 'mcp:%'）。
//
// 與 DeleteProviderActivities 的差異：COROS 的 source 字串 'coros' 同時被 Terra（external_id=summary_id）與
// Partner API（純 labelId）使用，只看 source 會連它們的紀錄一起刪，所以這裡額外以 MCP 專屬的 'mcp:' 前綴限定，
// 絕不動 Terra／Partner 的 coros 列。dup_of／flag 的善後處理與 DeleteProviderActivities 相同（原因見該函式註解），
// 且同樣不回收已發的 EXP／total_km（external_award_ledger 防重發）。回傳刪除筆數。
func (r *Repository) DeleteCorosMcpActivities(ctx context.Context, userID string) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE activities
		SET dup_of = NULL,
		    flagged = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN FALSE ELSE flagged END,
		    flag_reason = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN NULL ELSE flag_reason END
		WHERE user_id = $1
		  AND dup_of IN (SELECT id FROM activities WHERE user_id = $1 AND source = 'coros' AND external_id LIKE 'mcp:%')`,
		userID); err != nil {
		return 0, fmt.Errorf("clear stale dup_of before coros mcp activity delete: %w", err)
	}
	ct, err := tx.Exec(ctx, `DELETE FROM activities WHERE user_id=$1 AND source='coros' AND external_id LIKE 'mcp:%'`, userID)
	if err != nil {
		return 0, fmt.Errorf("delete coros mcp activities: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// PurgeCorosMcpUser 使用者中斷 COROS MCP 時的資料清除，**單一交易**（GA 契約 §3.3）：
//  1. 解除因這批 mcp 列而標記的良性重複（dup_of／cross_source_duplicate／multi_device_duplicate，原因同
//     DeleteProviderActivities 註解；包含直連優先去重翻盤的 Strava 列：mcp 列一刪，Strava 列重新計入）；
//  2. 查這位使用者的 GPS 校正配對是否含 mcp 列（回傳 hadCalibPairs，呼叫端據此重設校正係數）；
//  3. 刪除 source='coros'＋external_id 'mcp:%' 的活動（回傳筆數；gps_calib_pairs 隨活動 CASCADE）；
//  4. 刪除這位使用者的 coros_mcp_probe_logs；
//  5. 刪除 provider='coros_mcp' 的連線列（絕不動 provider='coros' 的 Terra／Partner 連線）。
//
// 不回收已發放的 EXP／DP／total_km（external_award_ledger 防重發；使用者 2026-08 拍板：獎勵已發視為既定事實）。
func (r *Repository) PurgeCorosMcpUser(ctx context.Context, userID string) (deleted int64, hadCalibPairs bool, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE activities
		SET dup_of = NULL,
		    flagged = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN FALSE ELSE flagged END,
		    flag_reason = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN NULL ELSE flag_reason END
		WHERE user_id = $1
		  AND dup_of IN (SELECT id FROM activities WHERE user_id = $1 AND source = 'coros' AND external_id LIKE 'mcp:%')`,
		userID); err != nil {
		return 0, false, fmt.Errorf("clear stale dup_of before coros mcp purge: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM gps_calib_pairs p JOIN activities a ON a.id = p.ext_activity_id
		               WHERE p.user_id = $1 AND a.source = 'coros' AND a.external_id LIKE 'mcp:%')`,
		userID).Scan(&hadCalibPairs); err != nil {
		return 0, false, fmt.Errorf("check calib pairs before coros mcp purge: %w", err)
	}
	ct, err := tx.Exec(ctx, `DELETE FROM activities WHERE user_id=$1 AND source='coros' AND external_id LIKE 'mcp:%'`, userID)
	if err != nil {
		return 0, false, fmt.Errorf("delete coros mcp activities: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM coros_mcp_probe_logs WHERE user_id=$1`, userID); err != nil {
		return 0, false, fmt.Errorf("delete coros mcp probe logs: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_integrations WHERE user_id=$1 AND provider='coros_mcp'`, userID); err != nil {
		return 0, false, fmt.Errorf("delete coros mcp connection: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return ct.RowsAffected(), hadCalibPairs, nil
}

// NormalizedActivity 各 provider 正規化後的活動
type NormalizedActivity struct {
	UserID      string
	Source      string
	ExternalID  string
	Fingerprint string // 精確指紋（起始秒|距離公尺|移動秒）
	DistanceKm  float64
	DurationS   int
	AvgPaceS    int
	AscentM     *float64
	AvgHR       *int
	RecordedAt  time.Time
	// Manual：Strava summary.manual（人工輸入的活動，非裝置紀錄）。目前只存不判——見
	// migrations/154_gps_calibration.sql 的 activities.ext_manual 欄位註解：未來若要解除 GPS
	// 距離校正係數只准向下的上限（k>1），必須先能辨識/排除人工輸入活動，這是前置資料準備。
	// 本期 gpscalib 估計器完全不讀這個欄位。非 Strava 來源（COROS/Terra）目前無對應資訊，恆 false。
	Manual bool
	// ElapsedS：外部來源回報的「總經過時間」（含停等）。只有 Strava 填（elapsed_time，區別於
	// DurationS=moving_time）；nil 代表無對應資訊（COROS/Terra，其 Duration 語意本來就接近經過時間）。
	// 供 gpscalib 候選查詢時間對齊用（COALESCE 回 duration_s），DurationS/AvgPaceS 口徑不受影響。
	ElapsedS *int
	// DeviceName：資料來源裝置型號（migration 197 activities.device_name，VARCHAR(60)）。只有 COROS MCP 匯入會填
	// （queryDevices 第一支裝置）；其他 importer 一律 nil（寫入 NULL，行為不變）。
	DeviceName *string
	// Kind：活動大類，"run"／"walk"，空字串視為 run（既有 importer 都不設）。只給合理性檢查
	// （CheckPlausible）選配速門檻用：走路類門檻 4:00/km，跑步類 2:30/km（見 plausibility.go）。
	Kind string
}

// ImportResult 匯入結果
type ImportResult struct {
	// Status：
	//   inserted  新寫入、未標記（計入賽事／獎勵）
	//   exists    (source, external_id) 已存在，沒有寫入
	//   duplicate 已寫入但標記為不計入（Reason＝flag_reason：duplicate／multi_device_duplicate／
	//             cross_account_duplicate／implausible_pace／implausible_distance）
	//   skipped   合理性檢查判定不該匯入（未來時間、距離／時間過短），沒有寫入，Reason 帶原因
	Status string
	Reason string // flagged 原因（duplicate 時）或略過原因（skipped 時）
	ID     string // 新插入活動的 activities.id（inserted/duplicate 時才有值；供去重感知里程 EXP/DP 發放用）
	// Superseded：這筆新列是「直連手錶」且取代了幾筆既有重疊的 Strava 列（那些 Strava 列已被改標
	// cross_source_duplicate、dup_of 指向這筆；見 detectDuplicate，GA 契約 §2.7）。>0 時這一趟的體力（SP）
	// 在 Strava 那筆匯入時已扣過，呼叫端不可再扣一次（AfterImport 已處理）。
	Superseded int
}

// FindRegisteredRace 找出 recordedAt 落在賽事期間、且該使用者有報名的賽事（取最近一場）
func (r *Repository) FindRegisteredRace(ctx context.Context, userID string, recordedAt time.Time) (string, bool, error) {
	var raceID string
	err := r.db.QueryRow(ctx, `
		SELECT r.id::text FROM races r
		JOIN registrations reg ON reg.race_id = r.id
		WHERE reg.user_id = $1 AND reg.status <> 'cancelled'
		  AND $2 BETWEEN r.start_date AND r.end_date
		ORDER BY r.start_date DESC
		LIMIT 1`, userID, recordedAt).Scan(&raceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return raceID, true, nil
}

// dupCandidate 一筆可能與新活動重複的既有活動（detectDuplicate 的 SQL 結果列；Source 空字串＝App GPS，
// 即 activities.source IS NULL）。
type dupCandidate struct {
	ID         string
	UserID     string
	Source     string
	ExternalID string
}

// dupDecision detectDuplicate 的結論。
type dupDecision struct {
	Flagged bool
	Reason  string
	DupOf   string // 被保留的那筆活動 id（Flagged 時；implausible_* 沒有）
	// SupersedeIDs：新列是「直連手錶」，且與它重疊的既有列全是 Strava → 那些 Strava 列要改標
	// cross_source_duplicate（dup_of＝新列）、新列保持計入（GA 契約 §2.7）。只有 Flagged=false 時才可能非空。
	SupersedeIDs []string
}

// decideDuplicate 純函式（不碰 DB，三種情境的單元測試見 repository_dedup_test.go）：給定新活動、
// 「精確指紋相同」與「同帳號時間重疊」兩組既有未標記候選，決定新列怎麼標。
//
//  1. 指紋相同、屬於「別的帳號」→ cross_account_duplicate（洗資料，非良性，不發獎勵）；優先於其餘判斷。
//  2. 指紋相同、同帳號 → duplicate；同帳號時間區間重疊 → multi_device_duplicate（多裝置同一筆活動）。
//     先到先贏：新列標重複、既有列保留。
//  3. 例外（GA 契約 §2.7「直連手錶優先於 Strava」）：新列是直連手錶（IsDirectWatch），且所有命中的同帳號
//     既有列都是 Strava → 不標新列，改回傳 SupersedeIDs，由 ImportActivity 在同一交易把那些 Strava 列
//     改標 cross_source_duplicate。理由：Strava 列永遠被賽事閘門排除（source<>'strava'），先到先贏會讓
//     「COROS→Strava 自動同步」的使用者整趟在任何賽事都不計。只要命中任一筆非 Strava 的列（App GPS、Terra、
//     另一支直連…），新列照舊標重複，不翻盤。
func decideDuplicate(a *NormalizedActivity, fpMatches, overlaps []dupCandidate) dupDecision {
	direct := IsDirectWatch(a.Source, a.ExternalID)
	var supersede []string
	seen := map[string]bool{}
	addSupersede := func(id string) {
		if !seen[id] {
			seen[id] = true
			supersede = append(supersede, id)
		}
	}
	var sameUserFP []dupCandidate
	for _, c := range fpMatches {
		if c.UserID != a.UserID {
			return dupDecision{Flagged: true, Reason: "cross_account_duplicate", DupOf: c.ID}
		}
		sameUserFP = append(sameUserFP, c)
	}
	for _, c := range sameUserFP {
		if direct && c.Source == "strava" {
			addSupersede(c.ID)
			continue
		}
		return dupDecision{Flagged: true, Reason: "duplicate", DupOf: c.ID}
	}
	for _, c := range overlaps {
		if c.UserID != a.UserID {
			continue // SQL 已限同帳號，防禦性略過
		}
		if direct && c.Source == "strava" {
			addSupersede(c.ID)
			continue
		}
		return dupDecision{Flagged: true, Reason: "multi_device_duplicate", DupOf: c.ID}
	}
	if len(supersede) > 0 {
		return dupDecision{SupersedeIDs: supersede}
	}
	return dupDecision{}
}

// overlapCandidatesSQL 同帳號、未標記、時間區間與新活動 [$4,$5] 重疊的既有活動。
//
// ⚠️ 時間基準（GA 契約 §2.8）：App GPS 列（source IS NULL）的 recorded_at 是「結束」時間，外部來源列
// （Strava／Terra／COROS／Garmin）存的是「開始」時間——必須先各自正規化成 [候選起點, 候選終點] 再判重疊
// （比照 mileage_exp.go AwardMileageExp 與 worker resolveCrossSourceDups 的 CASE）。舊寫法把 GPS 的結束
// 時間當開始，造成兩種錯誤：GPS 比手錶晚停→漏判重複（短暫雙算）；GPS 結束後 N 分鐘才開始的獨立跑步→
// 誤判重複（永久被排除）。
// $2／$3 只是讓 idx_activities_user_recorded_unflagged（user_id, recorded_at）能做範圍掃描的粗略視窗
// （新活動前後各 24 小時：GPS 列的 recorded_at 可能比新活動結束晚一個 GPS 時長），精確判斷在 CASE。
// 排序：非 Strava 在前（dup_of 優先指向較權威的來源）、再依時間。
const overlapCandidatesSQL = `
	SELECT id::text, user_id::text, COALESCE(source,''), COALESCE(external_id,'')
	FROM activities
	WHERE user_id=$1 AND NOT flagged
	  AND recorded_at >= $2 AND recorded_at <= $3
	  AND (CASE WHEN source IS NULL THEN recorded_at - make_interval(secs => duration_s) ELSE recorded_at END) <= $5
	  AND (CASE WHEN source IS NULL THEN recorded_at ELSE recorded_at + make_interval(secs => duration_s) END) >= $4
	ORDER BY (COALESCE(source,'') = 'strava'), recorded_at
	LIMIT 20`

func scanDupCandidates(rows pgx.Rows) ([]dupCandidate, error) {
	defer rows.Close()
	var out []dupCandidate
	for rows.Next() {
		var c dupCandidate
		if err := rows.Scan(&c.ID, &c.UserID, &c.Source, &c.ExternalID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// detectDuplicate 查兩組候選後交給 decideDuplicate：
//  1. 精確指紋相同（跨帳號 → cross_account_duplicate；同帳號 → duplicate）
//  2. 同帳號時間區間重疊 → multi_device_duplicate（多裝置同一筆活動）；直連手錶取代 Strava 見 decideDuplicate
//
// 查詢失敗回 error（舊版靜默當成「沒有重複」會造成雙算；呼叫端的匯入會重試，比雙算安全）。
func (r *Repository) detectDuplicate(ctx context.Context, a *NormalizedActivity) (dupDecision, error) {
	var fp []dupCandidate
	if a.Fingerprint != "" {
		rows, err := r.db.Query(ctx, `
			SELECT id::text, user_id::text, COALESCE(source,''), COALESCE(external_id,'')
			FROM activities WHERE fingerprint=$1 AND NOT flagged
			ORDER BY (user_id::text <> $2) DESC, created_at LIMIT 5`, a.Fingerprint, a.UserID)
		if err != nil {
			return dupDecision{}, fmt.Errorf("dedup fingerprint query: %w", err)
		}
		if fp, err = scanDupCandidates(rows); err != nil {
			return dupDecision{}, fmt.Errorf("dedup fingerprint scan: %w", err)
		}
	}
	// 同帳號時間重疊（多裝置）。新活動區間 [start, start+dur]。
	start := a.RecordedAt
	end := a.RecordedAt.Add(time.Duration(a.DurationS) * time.Second)
	rows, err := r.db.Query(ctx, overlapCandidatesSQL, a.UserID, start.Add(-24*time.Hour), end.Add(24*time.Hour), start, end)
	if err != nil {
		return dupDecision{}, fmt.Errorf("dedup overlap query: %w", err)
	}
	ov, err := scanDupCandidates(rows)
	if err != nil {
		return dupDecision{}, fmt.Errorf("dedup overlap scan: %w", err)
	}
	return decideDuplicate(a, fp, ov), nil
}

const insertActivitySQL = `
	INSERT INTO activities
		(user_id, race_id, distance_km, duration_s, avg_pace_s, ascent_m, avg_hr, recorded_at,
		 processed, source, external_id, fingerprint, flagged, flag_reason, dup_of, ext_manual, elapsed_s, device_name)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE,$9,$10,$11,$12,$13,$14,$15,$16,$17)
	ON CONFLICT (source, external_id) DO NOTHING
	RETURNING id::text`

// ImportActivity 寫入活動：先過合理性檢查（CheckPlausible，所有外部來源共用），再 source+external_id 去重；
// 偵測重複/跨帳號洗資料 → flag 且不計入賽事。
//
//   - 合理性 skip（未來時間／距離或時間過短）→ ImportResult{Status:"skipped", Reason}，不寫 DB。
//   - 合理性 flag（配速或距離不可能）→ 照常寫入但 flagged=TRUE、flag_reason=implausible_*，回 Status "duplicate"
//     （與其他「已寫入但不計入」同一個狀態，既有 importer 的 switch 與三段尾巴不必改；非良性原因 →
//     AwardMileageExp 不發獎勵、不參與差額補償基準）。
//   - 直連手錶取代 Strava（decideDuplicate 例外）：新列＋把 Strava 列改標 cross_source_duplicate 在同一交易，
//     回 Status "inserted"、Superseded=被取代筆數。
func (r *Repository) ImportActivity(ctx context.Context, a *NormalizedActivity) (ImportResult, error) {
	action, plausReason := CheckPlausible(a, time.Now())
	if action == PlausibleSkip {
		return ImportResult{Status: "skipped", Reason: plausReason}, nil
	}

	// 已匯入過（例如 COROS 每次同步都會重抓最近幾天）：直接回 exists，省掉去重候選查詢，
	// 也避免新列自己被當成候選（直連列的指紋與自己相同）。
	if a.Source != "" && a.ExternalID != "" {
		var one int
		err := r.db.QueryRow(ctx, `SELECT 1 FROM activities WHERE source=$1 AND external_id=$2`, a.Source, a.ExternalID).Scan(&one)
		if err == nil {
			return ImportResult{Status: "exists"}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return ImportResult{}, fmt.Errorf("check existing activity: %w", err)
		}
	}

	var dec dupDecision
	if action == PlausibleFlag {
		// 不可能的數據：標記優先於重複判斷（非良性原因，不發獎勵、不當補償基準），也不去翻盤任何 Strava 列。
		dec = dupDecision{Flagged: true, Reason: plausReason}
	} else {
		var err error
		if dec, err = r.detectDuplicate(ctx, a); err != nil {
			return ImportResult{}, err
		}
	}

	var raceArg, dupArg, reasonArg interface{}
	if dec.Flagged {
		reasonArg = dec.Reason
		if dec.DupOf != "" {
			dupArg = dec.DupOf
		}
		// flagged → race_id 留 NULL，不計入賽事
	} else {
		if raceID, ok, err := r.FindRegisteredRace(ctx, a.UserID, a.RecordedAt); err != nil {
			return ImportResult{}, err
		} else if ok {
			raceArg = raceID
		}
	}
	args := []interface{}{a.UserID, raceArg, a.DistanceKm, a.DurationS, a.AvgPaceS, a.AscentM, a.AvgHR, a.RecordedAt,
		a.Source, a.ExternalID, a.Fingerprint, dec.Flagged, reasonArg, dupArg, a.Manual, a.ElapsedS, a.DeviceName}

	var newID string
	superseded := 0
	if len(dec.SupersedeIDs) == 0 {
		err := r.db.QueryRow(ctx, insertActivitySQL, args...).Scan(&newID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ImportResult{Status: "exists"}, nil
		}
		if err != nil {
			return ImportResult{}, fmt.Errorf("insert activity: %w", err)
		}
	} else {
		tx, err := r.db.Begin(ctx)
		if err != nil {
			return ImportResult{}, fmt.Errorf("begin import tx: %w", err)
		}
		defer tx.Rollback(ctx) // 已 Commit 後為 no-op
		if err := tx.QueryRow(ctx, insertActivitySQL, args...).Scan(&newID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ImportResult{Status: "exists"}, nil // 併發下別人先寫入：不翻盤任何東西
			}
			return ImportResult{}, fmt.Errorf("insert activity: %w", err)
		}
		// 只動「同帳號、仍未標記的 Strava 列」：AND NOT flagged 讓併發下已被別人標記的列不被覆寫，
		// source='strava' 是防呆（候選本來就只收 Strava）。
		ct, err := tx.Exec(ctx, `
			UPDATE activities SET flagged=TRUE, flag_reason='cross_source_duplicate', dup_of=$2
			WHERE id = ANY($1::uuid[]) AND user_id=$3 AND source='strava' AND NOT flagged`,
			dec.SupersedeIDs, newID, a.UserID)
		if err != nil {
			return ImportResult{}, fmt.Errorf("supersede strava duplicates: %w", err)
		}
		superseded = int(ct.RowsAffected())
		if err := tx.Commit(ctx); err != nil {
			return ImportResult{}, fmt.Errorf("commit import tx: %w", err)
		}
	}
	if dec.Flagged {
		return ImportResult{Status: "duplicate", Reason: dec.Reason, ID: newID}, nil
	}
	return ImportResult{Status: "inserted", ID: newID, Superseded: superseded}, nil
}

// ActivityRow 個人活動清單單筆
type ActivityRow struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	DistanceKm float64   `json:"distance_km"`
	DurationS  int       `json:"duration_s"`
	AvgPaceS   int       `json:"avg_pace_s"`
	AscentM    *float64  `json:"ascent_m,omitempty"`
	AvgHR      *int      `json:"avg_hr,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	StartedAt  time.Time `json:"started_at"`
	RaceTitle  string    `json:"race_title,omitempty"`
	Flagged    bool      `json:"flagged"`
	FlagReason string    `json:"flag_reason,omitempty"`
	ExternalID string    `json:"external_id,omitempty"` // provider 活動 id（Strava→「View on Strava」回連）
	// GPS 距離校正（見 internal/gpscalib）：只有 App GPS 上傳（source IS NULL）的活動可能非 NULL；
	// 外部來源(Strava/COROS/Terra)匯入的活動這兩欄恆 NULL（distance_km 就是原始值，無校正概念）。
	// COALESCE 成 DistanceKm：舊資料/外部活動未套過校正時，前端「原始 vs 校正」對照直接退化成
	// 「兩者相同」，不必額外判斷 null。
	RawDistanceKm float64  `json:"raw_distance_km"`
	CalibFactor   *float64 `json:"calib_factor,omitempty"`
	// 跨來源去重（見 internal/profile/dedup.go）：flagged=TRUE 時 dup_of 指向「被保留」的那筆活動。
	// DupOfID/DupOfSource 只在保留活動屬於「同一個使用者」時才非空（SQL 端已用 d.user_id = a.user_id
	// 的 join 條件擋掉 cross_account_duplicate 這種 dup_of 指向別人活動的情況，見 ListActivities）——
	// 這個端點是使用者本人的活動清單，絕不能把別人的活動 id/來源帶出去。
	// 空字串＝沒有對應的保留活動（未被標重複、保留活動已被刪除、或保留活動屬於別的帳號）。
	// DupOfSource 是保留活動的來源，讓前端能顯示「⚠ 與 {DupOfSource} 來源資料重複」；
	// 這裡 NULL→'gps'（不是 'manual'，見下方 COALESCE(a.source,'manual') 旁的說明），
	// 因為前端 label 對 null/'manual'/'gps' 一律顯示同一個「App GPS」字樣，兩種 fallback 值對使用者透明。
	DupOfID     string `json:"dup_of_id,omitempty"`
	DupOfSource string `json:"dup_of_source,omitempty"`
	// DeviceName：資料來源裝置型號（migration 197），前台顯示「Data provided by COROS · <型號>」；NULL 時不出現。
	DeviceName *string `json:"device_name,omitempty"`
}

// ListActivities 取得使用者活動（最新 N 筆，含賽事名稱與 flagged 狀態）
func (r *Repository) ListActivities(ctx context.Context, userID string, limit int) ([]ActivityRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.db.Query(ctx, `
		SELECT a.id::text,
		       -- NULL source 涵蓋兩種寫入路徑：一般 App GPS 上傳，以及後台補登里程 AdminAddMileage
		       -- （見 internal/activity/service.go AdminAddMileage → 同一 Redis 事件 → internal/activity/
		       -- repository.go 的 INSERT，該路徑本來就不寫 source 欄位）。兩者概念上都算「App GPS 上傳」，
		       -- 沒有可回連的外部裝置，故維持既有 COALESCE 成 'manual'、不改為 'gps'；前端對
		       -- null/'manual'/'gps' 三個值一律顯示「App GPS」即可蓋掉這個歷史差異，不必在這裡動 DB 值。
		       COALESCE(a.source,'manual'), a.distance_km, a.duration_s, a.avg_pace_s,
		       a.ascent_m, a.avg_hr, a.recorded_at,
		       CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END AS started_at,
		       COALESCE(r.title,''), a.flagged, COALESCE(a.flag_reason,''),
		       COALESCE(a.external_id,''), COALESCE(a.raw_distance_km, a.distance_km), a.calib_factor,
		       CASE WHEN d.id IS NOT NULL THEN a.dup_of::text ELSE '' END,
		       CASE WHEN d.id IS NOT NULL THEN COALESCE(d.source,'gps') ELSE '' END,
		       a.device_name
		FROM activities a
		LEFT JOIN races r ON r.id = a.race_id
		-- d.user_id 限同帳號：dup_of 在 cross_account_duplicate 時指向「別人」的活動列（見 detectDuplicate/
		-- ImportActivity），此端點是使用者本人的活動清單，絕不可把別人的活動 id/來源透過這個 join 帶出去。
		-- 加上 user_id 條件後，跨帳號那筆的 d 不會 join 到任何列，d.id 為 NULL，上面兩個 CASE 就回傳空字串，
		-- 等同前端既有邏輯（DUP_SOURCE_FLAG_REASONS 本就不含 cross_account_duplicate）：資料庫層也守住，
		-- 不只是前端顯示層懶得秀而已。
		LEFT JOIN activities d ON d.id = a.dup_of AND d.user_id = a.user_id
		WHERE a.user_id=$1
		ORDER BY a.recorded_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	defer rows.Close()
	out := []ActivityRow{}
	for rows.Next() {
		var a ActivityRow
		if err := rows.Scan(&a.ID, &a.Source, &a.DistanceKm, &a.DurationS, &a.AvgPaceS,
			&a.AscentM, &a.AvgHR, &a.RecordedAt, &a.StartedAt, &a.RaceTitle, &a.Flagged, &a.FlagReason, &a.ExternalID,
			&a.RawDistanceKm, &a.CalibFactor, &a.DupOfID, &a.DupOfSource, &a.DeviceName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
