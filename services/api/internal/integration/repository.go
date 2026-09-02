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

// stravaTokenKey 解析 STRAVA_TOKEN_KEY（32 bytes，接受 hex 或 base64 編碼）。
func stravaTokenKey() []byte {
	raw := os.Getenv("STRAVA_TOKEN_KEY")
	if raw == "" {
		return nil
	}
	if k, err := hex.DecodeString(raw); err == nil && len(k) == 32 {
		return k
	}
	if k, err := base64.StdEncoding.DecodeString(raw); err == nil && len(k) == 32 {
		return k
	}
	log.Warn().Msg("STRAVA_TOKEN_KEY 已設定但不是有效的 32 bytes hex/base64 金鑰，token 將繼續以明碼儲存")
	return nil
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
		  AND dup_of IN (SELECT id FROM activities WHERE user_id = $1 AND source = $2)`,
		userID, provider); err != nil {
		return fmt.Errorf("clear stale dup_of before provider activity delete: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM activities WHERE user_id=$1 AND source=$2`, userID, provider); err != nil {
		return fmt.Errorf("delete provider activities: %w", err)
	}
	return tx.Commit(ctx)
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
}

// ImportResult 匯入結果
type ImportResult struct {
	Status string // inserted | exists | duplicate
	Reason string // flagged 原因（duplicate 時）
	ID     string // 新插入活動的 activities.id（inserted/duplicate 時才有值；供去重感知里程 EXP/DP 發放用）
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

// detectDuplicate 回傳 (flagged, reason, dupOfID)。
// 1) 跨帳號精確指紋相同 → cross_account_duplicate（同帳號則 duplicate）
// 2) 同帳號時間區間重疊 → multi_device_duplicate（多裝置同一筆活動）
func (r *Repository) detectDuplicate(ctx context.Context, a *NormalizedActivity) (bool, string, string) {
	if a.Fingerprint != "" {
		var id, uid string
		err := r.db.QueryRow(ctx,
			`SELECT id::text, user_id::text FROM activities WHERE fingerprint=$1 AND NOT flagged LIMIT 1`,
			a.Fingerprint).Scan(&id, &uid)
		if err == nil {
			reason := "cross_account_duplicate"
			if uid == a.UserID {
				reason = "duplicate"
			}
			return true, reason, id
		}
	}
	// 同帳號時間重疊（多裝置）。新活動區間 [start, start+dur]；限 24h 窗加速。
	start := a.RecordedAt
	end := a.RecordedAt.Add(time.Duration(a.DurationS) * time.Second)
	var id string
	err := r.db.QueryRow(ctx, `
		SELECT id::text FROM activities
		WHERE user_id=$1 AND NOT flagged
		  AND recorded_at >= $2 AND recorded_at <= $3
		  AND (recorded_at + (duration_s || ' seconds')::interval) >= $4
		LIMIT 1`,
		a.UserID, start.Add(-24*time.Hour), end, start).Scan(&id)
	if err == nil {
		return true, "multi_device_duplicate", id
	}
	return false, "", ""
}

// ImportActivity 寫入活動：source+external_id 去重；偵測重複/跨帳號洗資料 → flag 且不計入賽事。
func (r *Repository) ImportActivity(ctx context.Context, a *NormalizedActivity) (ImportResult, error) {
	flagged, reason, dupOf := r.detectDuplicate(ctx, a)

	var raceArg, dupArg, reasonArg interface{}
	if flagged {
		reasonArg = reason
		dupArg = dupOf
		// flagged → race_id 留 NULL，不計入賽事
	} else {
		if raceID, ok, err := r.FindRegisteredRace(ctx, a.UserID, a.RecordedAt); err != nil {
			return ImportResult{}, err
		} else if ok {
			raceArg = raceID
		}
	}

	var newID string
	err := r.db.QueryRow(ctx, `
		INSERT INTO activities
			(user_id, race_id, distance_km, duration_s, avg_pace_s, ascent_m, avg_hr, recorded_at,
			 processed, source, external_id, fingerprint, flagged, flag_reason, dup_of, ext_manual, elapsed_s)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (source, external_id) DO NOTHING
		RETURNING id::text`,
		a.UserID, raceArg, a.DistanceKm, a.DurationS, a.AvgPaceS, a.AscentM, a.AvgHR, a.RecordedAt,
		a.Source, a.ExternalID, a.Fingerprint, flagged, reasonArg, dupArg, a.Manual, a.ElapsedS).Scan(&newID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ImportResult{Status: "exists"}, nil
	}
	if err != nil {
		return ImportResult{}, fmt.Errorf("insert activity: %w", err)
	}
	if flagged {
		return ImportResult{Status: "duplicate", Reason: reason, ID: newID}, nil
	}
	return ImportResult{Status: "inserted", ID: newID}, nil
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
}

// ListActivities 取得使用者活動（最新 N 筆，含賽事名稱與 flagged 狀態）
func (r *Repository) ListActivities(ctx context.Context, userID string, limit int) ([]ActivityRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.db.Query(ctx, `
		SELECT a.id::text, COALESCE(a.source,'manual'), a.distance_km, a.duration_s, a.avg_pace_s,
		       a.ascent_m, a.avg_hr, a.recorded_at,
		       CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END AS started_at,
		       COALESCE(r.title,''), a.flagged, COALESCE(a.flag_reason,''),
		       COALESCE(a.external_id,''), COALESCE(a.raw_distance_km, a.distance_km), a.calib_factor
		FROM activities a LEFT JOIN races r ON r.id = a.race_id
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
			&a.RawDistanceKm, &a.CalibFactor); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
