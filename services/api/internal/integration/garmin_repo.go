package integration

// Garmin 直連的資料存取層（user_integrations 的 Garmin 列、integration_events、封鎖清單、活動刪除）。
// 全部是 *Repository 的新方法（本檔唯一擁有者＝Garmin 線；repository.go 不動）。
// 連線列的 token 一律 fail-closed 加密（無有效 STRAVA_TOKEN_KEY 就拒絕保存，不退回明碼）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrGarminAlreadyLinked：這個 Garmin 帳號已綁定另一個 DOR 帳號（唯一索引 23505）。與共用的 ErrProviderAccountLinked 同一個值。
	ErrGarminAlreadyLinked = ErrProviderAccountLinked
	// ErrGarminAccountChanged：既有直連列的 Garmin userId 與這次取得的不同（不覆寫，請使用者先中斷再連新帳號）。
	ErrGarminAccountChanged = errors.New("garmin: authorized garmin account differs from the linked one")
	// ErrGarminNoTokenKey：沒有有效的 token 加密金鑰，拒絕保存 token（fail-closed）。與共用的 ErrTokenKeyMissing 同一個值。
	ErrGarminNoTokenKey = ErrTokenKeyMissing
)

// garminEncryptStrict：fail-closed 的 token 加密（共用的 EncryptTokenStrict）：沒有有效金鑰就回 ErrTokenKeyMissing，
// 絕不把明碼寫進資料庫（repository.go 的 encryptToken 在金鑰缺失時會靜默回明碼，那是 Strava／Terra 的舊行為）。
func garminEncryptStrict(plain string) (string, error) { return EncryptTokenStrict(plain) }

// garminConn：user_integrations 中 provider='garmin' 的一列（含 Garmin 專屬欄位）。
// Tokens 只有用 withTokens=true 讀取時才會解密填入。
type garminConn struct {
	ID               string
	UserID           string
	ProviderUserID   string
	AccessToken      string
	RefreshToken     string
	ExpiresAt        time.Time
	Scope            string
	ConnectedAt      time.Time // created_at：匯入 floor（覆蓋 Terra 列時保留）
	Via              string
	Issuer           string
	RefreshExpiresAt *time.Time
	ReauthRequiredAt *time.Time
	ConsentAt        *time.Time
	ConsentVersion   string
	AuthorizedAt     *time.Time // connected_at：最近一次（重新）授權完成時間
	LastSyncedAt     *time.Time
	UpdatedAt        time.Time
}

func (c *garminConn) hasPermission(p string) bool {
	for _, s := range strings.Split(c.Scope, ",") {
		if strings.TrimSpace(s) == p {
			return true
		}
	}
	return false
}

// paused：已知權限清單且不含 ACTIVITY_EXPORT（使用者在 Garmin Connect 關閉了資料分享）。
// ⚠️ 只用於卡片橫幅，絕不可作為丟棄活動推送的條件（Garmin 端本來就不會再傳資料；見 R-C2）。
func (c *garminConn) paused() bool {
	return c.Via == garminViaDirect && strings.TrimSpace(c.Scope) != "" && !c.hasPermission(garminPermActivityExport)
}

const garminConnCols = `SELECT id::text, user_id::text, provider_user_id, access_token, refresh_token, expires_at,
	COALESCE(scope,''), created_at, COALESCE(via,'direct'), COALESCE(issuer,''), refresh_expires_at, reauth_required_at,
	consent_at, COALESCE(consent_version,''), connected_at, last_synced_at, updated_at FROM user_integrations`

func scanGarminConn(row pgx.Row, withTokens bool) (*garminConn, error) {
	c := &garminConn{}
	var access, refresh string
	err := row.Scan(&c.ID, &c.UserID, &c.ProviderUserID, &access, &refresh, &c.ExpiresAt,
		&c.Scope, &c.ConnectedAt, &c.Via, &c.Issuer, &c.RefreshExpiresAt, &c.ReauthRequiredAt,
		&c.ConsentAt, &c.ConsentVersion, &c.AuthorizedAt, &c.LastSyncedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if withTokens {
		if c.AccessToken, err = decryptToken(access); err != nil {
			return nil, fmt.Errorf("garmin conn %s: decrypt access token: %w", c.ID, err)
		}
		if c.RefreshToken, err = decryptToken(refresh); err != nil {
			return nil, fmt.Errorf("garmin conn %s: decrypt refresh token: %w", c.ID, err)
		}
	}
	return c, nil
}

// GetGarminByUser 取使用者的 garmin 列（不分 via：Terra 舊列也回來，供 /status 的 legacy_terra 判斷）。
func (r *Repository) GetGarminByUser(ctx context.Context, userID string, withTokens bool) (*garminConn, error) {
	return scanGarminConn(r.db.QueryRow(ctx, garminConnCols+` WHERE user_id=$1 AND provider='garmin'`, userID), withTokens)
}

// GetGarminDirectByUserID 以 Garmin userId 找直連列（刻意不用 GetByProviderUser：避免撞到 Terra 的 UUID）。
func (r *Repository) GetGarminDirectByUserID(ctx context.Context, garminUserID string, withTokens bool) (*garminConn, error) {
	return scanGarminConn(r.db.QueryRow(ctx,
		garminConnCols+` WHERE provider='garmin' AND via='direct' AND provider_user_id=$1`, garminUserID), withTokens)
}

// KnownGarminUsers 回傳 ids 中「對得到直連連線且未被封鎖」者：garminUserId → DOR user id。
// 一次查詢濾掉未知 userId（未知者只計數、不落地）。
func (r *Repository) KnownGarminUsers(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT ui.provider_user_id, ui.user_id::text
		FROM user_integrations ui
		WHERE ui.provider='garmin' AND ui.via='direct' AND ui.provider_user_id = ANY($1)
		  AND NOT EXISTS (SELECT 1 FROM garmin_blocklist b
		                   WHERE b.user_id = ui.user_id OR b.garmin_user_id = ui.provider_user_id)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var gid, uid string
		if err := rows.Scan(&gid, &uid); err != nil {
			return nil, err
		}
		out[gid] = uid
	}
	return out, rows.Err()
}

// GarminBlocked：DOR 帳號或 Garmin userId 是否在封鎖清單。
func (r *Repository) GarminBlocked(ctx context.Context, userID, garminUserID string) (bool, error) {
	var blocked bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM garmin_blocklist
		               WHERE (user_id IS NOT NULL AND user_id = NULLIF($1,'')::uuid)
		                  OR (garmin_user_id IS NOT NULL AND garmin_user_id = NULLIF($2,'')))`,
		userID, garminUserID).Scan(&blocked)
	return blocked, err
}

// AddGarminBlock 寫入封鎖清單（同一列同時記 DOR user_id 與 Garmin userId；已存在的目標不重複寫入）。
func (r *Repository) AddGarminBlock(ctx context.Context, userID, garminUserID, reason, createdBy string) error {
	if userID == "" && garminUserID == "" {
		return errors.New("garmin block: a target is required")
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO garmin_blocklist (user_id, garmin_user_id, reason, created_by)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,''), $3, NULLIF($4,'')::uuid)
		ON CONFLICT DO NOTHING`, userID, garminUserID, truncateRunes(reason, 120), createdBy)
	return err
}

// RemoveGarminBlock 解除某 DOR 使用者的封鎖（刪掉以 user_id 為目標的列），回傳刪除筆數。
func (r *Repository) RemoveGarminBlock(ctx context.Context, userID string) (int64, error) {
	ct, err := r.db.Exec(ctx, `DELETE FROM garmin_blocklist WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// garminSaveInput：callback 取得授權後要保存的內容。
type garminSaveInput struct {
	UserID           string
	GarminUserID     string
	AccessToken      string
	RefreshToken     string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
	Scope            string // 使用者授權的權限清單（逗號分隔，非 OAuth scope）
	Issuer           string // App 世代（garmin-app:<hash 前 8 碼>）
	ConsentAt        time.Time
	ConsentVersion   string
}

type garminSaveResult struct {
	ID          string
	ConnectedAt time.Time // created_at（匯入 floor）
	Inserted    bool      // true＝全新列；false＝覆蓋既有列（Terra 舊列或重新授權）
	PrevVia     string    // 覆蓋前的 via（全新列為 ""）
}

// SaveGarmin 保存（或覆蓋）使用者的 Garmin 直連列。與 Save() 的刻意差異：
//   - ON CONFLICT 時「不」覆寫 created_at（匯入 floor）：覆蓋 Terra 舊列、重新授權都不前移也不倒灌；
//   - token fail-closed 加密（沒有有效金鑰回 ErrGarminNoTokenKey，不寫入）；
//   - 既有直連列的 Garmin userId 與這次不同 → ErrGarminAccountChanged（不覆寫）；
//   - 這個 Garmin userId 已被另一個 DOR 帳號綁定（唯一索引）→ ErrGarminAlreadyLinked（整筆不寫入）。
func (r *Repository) SaveGarmin(ctx context.Context, in garminSaveInput) (garminSaveResult, error) {
	var res garminSaveResult
	access, err := garminEncryptStrict(in.AccessToken)
	if err != nil {
		return res, err
	}
	refresh, err := garminEncryptStrict(in.RefreshToken)
	if err != nil {
		return res, err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(context.Background())

	var prevVia, prevPUID string
	err = tx.QueryRow(ctx, `SELECT COALESCE(via,'direct'), provider_user_id FROM user_integrations
		WHERE user_id=$1 AND provider='garmin' FOR UPDATE`, in.UserID).Scan(&prevVia, &prevPUID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		prevVia, prevPUID = "", ""
	case err != nil:
		return res, err
	}
	if prevVia == garminViaDirect && prevPUID != "" && prevPUID != in.GarminUserID {
		return res, ErrGarminAccountChanged
	}

	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO user_integrations
			(user_id, provider, provider_user_id, access_token, refresh_token, expires_at, scope, athlete_name, via,
			 issuer, refresh_expires_at, reauth_required_at, consent_at, consent_version, connected_at)
		VALUES ($1,'garmin',$2,$3,$4,$5,$6,'','direct',$7,$8,NULL,$9,$10,NOW())
		ON CONFLICT (user_id, provider) DO UPDATE SET
			provider_user_id   = EXCLUDED.provider_user_id,
			access_token       = EXCLUDED.access_token,
			refresh_token      = EXCLUDED.refresh_token,
			expires_at         = EXCLUDED.expires_at,
			scope              = EXCLUDED.scope,
			via                = 'direct',
			issuer             = EXCLUDED.issuer,
			refresh_expires_at = EXCLUDED.refresh_expires_at,
			reauth_required_at = NULL,
			consent_at         = EXCLUDED.consent_at,
			consent_version    = EXCLUDED.consent_version,
			connected_at       = NOW(),
			updated_at         = NOW()
		WHERE user_integrations.via <> 'direct'
		   OR user_integrations.provider_user_id IN ('', EXCLUDED.provider_user_id)
		RETURNING id::text, created_at, (xmax = 0)`,
		in.UserID, in.GarminUserID, access, refresh, in.ExpiresAt, in.Scope,
		in.Issuer, in.RefreshExpiresAt, nullableTime(in.ConsentAt), strings.TrimSpace(in.ConsentVersion)).
		Scan(&res.ID, &res.ConnectedAt, &inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, ErrGarminAccountChanged // 被 WHERE 擋下（理論上已在上面檢查過；競態時的保險）
	}
	if err != nil {
		if garminIsUniqueViolation(err) {
			return res, ErrGarminAlreadyLinked
		}
		return res, err
	}
	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	res.Inserted = inserted
	res.PrevVia = prevVia
	return res, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// garminIsUniqueViolation：SaveGarmin 的 INSERT 已用 ON CONFLICT (user_id, provider) 處理同使用者衝突，
// 所以任何殘留的 user_integrations 唯一違反都來自「同一個 provider 帳號」的唯一索引
// （Garmin 專用索引 uq_user_integrations_garmin_uid，或 198 的全供應商索引）。
func garminIsUniqueViolation(err error) bool {
	if IsProviderAccountConflict(err) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.TableName == "user_integrations"
}

// UpdateGarminTokens 保存刷新後的 token（輪替：新 refresh 必須落盤）。fail-closed 加密；連線不存在回錯。
func (r *Repository) UpdateGarminTokens(ctx context.Context, id, access, refresh string, expiresAt, refreshExpiresAt time.Time) error {
	a, err := garminEncryptStrict(access)
	if err != nil {
		return err
	}
	rt, err := garminEncryptStrict(refresh)
	if err != nil {
		return err
	}
	ct, err := r.db.Exec(ctx, `
		UPDATE user_integrations
		   SET access_token=$1, refresh_token=$2, expires_at=$3, refresh_expires_at=$4,
		       reauth_required_at=NULL, updated_at=NOW()
		 WHERE id=$5::uuid AND provider='garmin' AND via='direct'`, a, rt, expiresAt, refreshExpiresAt, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("garmin: connection not found for token update")
	}
	return nil
}

// MarkGarminReauth 標記連線需要重新授權（已標記者保留最早時間）。
func (r *Repository) MarkGarminReauth(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `UPDATE user_integrations SET reauth_required_at = COALESCE(reauth_required_at, NOW())
		WHERE id=$1::uuid AND provider='garmin' AND via='direct'`, id)
	return err
}

// SetGarminScope 更新使用者目前的權限清單（逗號分隔）。
func (r *Repository) SetGarminScope(ctx context.Context, id, scope string) error {
	_, err := r.db.Exec(ctx, `UPDATE user_integrations SET scope=$2 WHERE id=$1::uuid AND provider='garmin' AND via='direct'`, id, scope)
	return err
}

// TouchGarminSynced 記錄「剛收到該使用者的活動推送」（同一分鐘內只寫一次，降低寫入量）。
func (r *Repository) TouchGarminSynced(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `UPDATE user_integrations SET last_synced_at=NOW()
		WHERE id=$1::uuid AND provider='garmin' AND (last_synced_at IS NULL OR last_synced_at < NOW() - interval '1 minute')`, id)
	return err
}

// DeleteGarminConnection 刪除使用者的 garmin 連線列（不分 via）。
func (r *Repository) DeleteGarminConnection(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, userID)
	return err
}

// garminDeleteActivitiesTx 在交易內刪除使用者「全部」source='garmin' 的活動（含舊 Terra 列，R-M4：與同意文案、
// Terra 現行行為一致）；先解除指向這些列的良性重複標記（原因見 repository.go DeleteProviderActivities 註解）。
func garminDeleteActivitiesTx(ctx context.Context, tx pgx.Tx, userID string) (int64, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE activities
		   SET dup_of = NULL,
		       flagged = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN FALSE ELSE flagged END,
		       flag_reason = CASE WHEN flag_reason IN ('cross_source_duplicate','multi_device_duplicate') THEN NULL ELSE flag_reason END
		 WHERE user_id = $1
		   AND dup_of IN (SELECT id FROM activities WHERE user_id = $1 AND source = 'garmin')`, userID); err != nil {
		return 0, fmt.Errorf("clear stale dup_of before garmin activity delete: %w", err)
	}
	ct, err := tx.Exec(ctx, `DELETE FROM activities WHERE user_id=$1 AND source='garmin'`, userID)
	if err != nil {
		return 0, fmt.Errorf("delete garmin activities: %w", err)
	}
	return ct.RowsAffected(), nil
}

// DeleteGarminActivities 單獨刪除（交易內）；回傳刪除筆數。PurgeUser 另有把它與連線／事件合成同一交易的版本。
func (r *Repository) DeleteGarminActivities(ctx context.Context, userID string) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	n, err := garminDeleteActivitiesTx(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// LegacyTwinExists 舊孿生探測：同使用者是否已有「舊 Terra-garmin 列」——external_id 為裸 summaryId，或 "garmin:<開始秒>"。
// 命中視為已存在（略過，不發獎勵），避免直連列與 Terra 舊列雙算。
func (r *Repository) LegacyTwinExists(ctx context.Context, userID, bareID string, startUnix int64) (bool, error) {
	var exists bool
	ids := []string{"garmin:" + fmt.Sprint(startUnix)}
	if bareID != "" {
		ids = append(ids, bareID)
	}
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM activities WHERE user_id=$1 AND source='garmin' AND external_id = ANY($2))`,
		userID, ids).Scan(&exists)
	return exists, err
}

// SaveTerraUnlessDirect 是 SaveTerra 的守衛版：目標 (user,provider) 已有 via='direct' 列時「不」覆寫
// （SaveTerra 會把 token 清空、via 改回 terra，等於毀掉直連連線）。單一語句、原子（無「先查後寫」競態）。
// saved=false 代表被直連列擋下（呼叫端略過並記 log）。其餘行為與 SaveTerra 相同（不覆寫 created_at）。
func (r *Repository) SaveTerraUnlessDirect(ctx context.Context, c *Connection) (saved bool, err error) {
	var id string
	err = r.db.QueryRow(ctx, `
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
			updated_at       = NOW()
		WHERE user_integrations.via = 'terra'
		RETURNING id::text`,
		c.UserID, c.Provider, c.ProviderUserID, c.ExpiresAt, c.Scope).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// --- integration_events ---

const garminInsertChunk = 500

// InsertGarminEvents 批次落地事件。ON CONFLICT (provider, dedupe_key) DO NOTHING：重送／重複的事件不會產生新列，
// 回傳值只含「這次真的新增」的事件。dedupe_key 為 NULL（deregistration）的事件彼此不衝突。
// 租約：新列的 next_attempt_at 預設為 now()+2 分鐘（見 migration 199），就地處理期間不被掃描器搶走。
func (r *Repository) InsertGarminEvents(ctx context.Context, evs []garminEventIn) ([]garminEvent, error) {
	var out []garminEvent
	for start := 0; start < len(evs); start += garminInsertChunk {
		end := start + garminInsertChunk
		if end > len(evs) {
			end = len(evs)
		}
		chunk := evs[start:end]
		types := make([]string, len(chunk))
		uids := make([]string, len(chunk))
		keys := make([]*string, len(chunk))
		payloads := make([]string, len(chunk))
		for i, e := range chunk {
			types[i], uids[i], keys[i], payloads[i] = e.EventType, e.ProviderUserID, e.DedupeKey, string(e.Payload)
		}
		rows, err := r.db.Query(ctx, `
			INSERT INTO integration_events (provider, event_type, provider_user_id, dedupe_key, payload)
			SELECT 'garmin', t.et, t.uid, t.dk, t.pl::jsonb
			  FROM unnest($1::text[], $2::text[], $3::text[], $4::text[]) AS t(et, uid, dk, pl)
			ON CONFLICT (provider, dedupe_key) DO NOTHING
			RETURNING id::text, event_type, provider_user_id, dedupe_key, payload::text, attempts, received_at`,
			types, uids, keys, payloads)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e garminEvent
			var payload string
			if err := rows.Scan(&e.ID, &e.EventType, &e.ProviderUserID, &e.DedupeKey, &payload, &e.Attempts, &e.ReceivedAt); err != nil {
				rows.Close()
				return nil, err
			}
			e.Payload = []byte(payload)
			out = append(out, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func scanGarminEvents(rows pgx.Rows) ([]garminEvent, error) {
	defer rows.Close()
	var out []garminEvent
	for rows.Next() {
		var e garminEvent
		var payload string
		if err := rows.Scan(&e.ID, &e.EventType, &e.ProviderUserID, &e.DedupeKey, &payload, &e.Attempts, &e.ReceivedAt); err != nil {
			return nil, err
		}
		e.Payload = []byte(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClaimDueGarminEvents 領取「已到期」的事件（pending／error 且 next_attempt_at<=now()）：用 FOR UPDATE SKIP LOCKED
// 並把租約延長 garminSweepLease，多副本同時呼叫也不會重複領取。
func (r *Repository) ClaimDueGarminEvents(ctx context.Context, limit int) ([]garminEvent, error) {
	if limit <= 0 {
		limit = garminSweepBatch
	}
	rows, err := r.db.Query(ctx, `
		UPDATE integration_events
		   SET next_attempt_at = now() + make_interval(secs => $2)
		 WHERE id IN (SELECT id FROM integration_events
		               WHERE provider='garmin' AND status IN ('pending','error') AND next_attempt_at <= now()
		               ORDER BY next_attempt_at
		               LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING id::text, event_type, provider_user_id, dedupe_key, payload::text, attempts, received_at`,
		limit, garminSweepLease.Seconds())
	if err != nil {
		return nil, err
	}
	return scanGarminEvents(rows)
}

// ClaimGarminEventByID 重試計時器用：只領取這一筆（需為 pending／error 且租約已到期）。
func (r *Repository) ClaimGarminEventByID(ctx context.Context, id string) (*garminEvent, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE integration_events
		   SET next_attempt_at = now() + make_interval(secs => $2)
		 WHERE id = (SELECT id FROM integration_events
		              WHERE id=$1::uuid AND provider='garmin' AND status IN ('pending','error') AND next_attempt_at <= now() + interval '2 seconds'
		              FOR UPDATE SKIP LOCKED)
		RETURNING id::text, event_type, provider_user_id, dedupe_key, payload::text, attempts, received_at`,
		id, garminSweepLease.Seconds())
	if err != nil {
		return nil, err
	}
	evs, err := scanGarminEvents(rows)
	if err != nil || len(evs) == 0 {
		return nil, err
	}
	return &evs[0], nil
}

// MarkGarminEventDone 事件處理完成。
func (r *Repository) MarkGarminEventDone(ctx context.Context, id, result string) error {
	// 只從 pending／error 轉 done（單向：done／dead 不會被遲到的回報蓋回去）
	_, err := r.db.Exec(ctx, `UPDATE integration_events
		   SET status='done', result=$2, last_error=NULL, processed_at=now()
		 WHERE id=$1::uuid AND status IN ('pending','error')`, id, truncateRunes(result, 40))
	return err
}

// MarkGarminEventError 處理失敗：attempts+1；達到 maxAttempts 轉 dead，否則 error 並排下次時間。
// 回傳最新的 attempts 與是否已 dead。
func (r *Repository) MarkGarminEventError(ctx context.Context, id, errCode string, next time.Time, maxAttempts int) (attempts int, dead bool, err error) {
	var status string
	err = r.db.QueryRow(ctx, `
		UPDATE integration_events
		   SET attempts = attempts + 1,
		       status = CASE WHEN attempts + 1 >= $4 THEN 'dead' ELSE 'error' END,
		       last_error = $2,
		       next_attempt_at = $3,
		       processed_at = CASE WHEN attempts + 1 >= $4 THEN now() ELSE processed_at END
		 WHERE id=$1::uuid AND status IN ('pending','error')
		RETURNING attempts, status`, id, truncateRunes(errCode, 300), next, maxAttempts).Scan(&attempts, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil // 事件已是 done／dead（別的處理者先完成了）：不覆蓋
	}
	return attempts, status == garminStDead, err
}

// MarkGarminEventDead 直接標為 dead（不可重試的事件）。
func (r *Repository) MarkGarminEventDead(ctx context.Context, id, reason string) error {
	_, err := r.db.Exec(ctx, `UPDATE integration_events
		   SET status='dead', last_error=$2, processed_at=now()
		 WHERE id=$1::uuid`, id, truncateRunes(reason, 300))
	return err
}

// DeferGarminEvent 不改狀態、不計失敗次數，只把下次嘗試時間往後排（處理器尚未就緒／使用者忙碌時用）。
func (r *Repository) DeferGarminEvent(ctx context.Context, id, note string, next time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE integration_events SET next_attempt_at=$2, last_error=$3
		 WHERE id=$1::uuid AND status IN ('pending','error')`, id, next, truncateRunes(note, 300))
	return err
}

// ReleaseGarminLeases 優雅關閉時把尚未處理完的事件租約釋放（next_attempt_at=now()），讓新行程的啟動掃描能立刻接手。
func (r *Repository) ReleaseGarminLeases(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.db.Exec(ctx, `UPDATE integration_events SET next_attempt_at=now()
		 WHERE provider='garmin' AND status IN ('pending','error') AND id = ANY($1::uuid[])`, ids)
	return err
}

// PurgeGarminEvents 依保存期限清理（OD-11）：done 14 天、dead／error 與久未處理的 pending 30 天。回傳刪除筆數。
func (r *Repository) PurgeGarminEvents(ctx context.Context) (int64, error) {
	ct, err := r.db.Exec(ctx, `
		DELETE FROM integration_events
		 WHERE provider='garmin'
		   AND ( (status='done' AND COALESCE(processed_at, received_at) < now() - make_interval(days => $1))
		      OR (status <> 'done' AND received_at < now() - make_interval(days => $2)) )`,
		garminDoneRetentionDays, garminDeadRetentionDays)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// DeleteGarminEventsForUser 刪除某 Garmin userId 的「所有狀態」事件列（PurgeUser／斷線用；R-M9）。
func (r *Repository) DeleteGarminEventsForUser(ctx context.Context, garminUserID string) (int64, error) {
	ct, err := r.db.Exec(ctx, `DELETE FROM integration_events WHERE provider='garmin' AND provider_user_id=$1`, garminUserID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// GarminEventStats 事件表統計（純 DB）。
func (r *Repository) GarminEventStats(ctx context.Context) (garminEventStats, error) {
	var s garminEventStats
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE received_at > now() - interval '24 hours'),
		       count(*) FILTER (WHERE status='done' AND processed_at > now() - interval '24 hours'),
		       count(*) FILTER (WHERE result='inserted' AND processed_at > now() - interval '24 hours'),
		       count(*) FILTER (WHERE left(COALESCE(result,''),8)='skipped_' AND processed_at > now() - interval '24 hours'),
		       count(*) FILTER (WHERE status IN ('pending','error') AND next_attempt_at < now() - interval '10 minutes'),
		       count(*) FILTER (WHERE status='dead')
		  FROM integration_events WHERE provider='garmin'`).
		Scan(&s.Received24h, &s.Done24h, &s.Imported24h, &s.Skipped24h, &s.PendingStale, &s.Dead)
	return s, err
}

// WithGarminUserLock 以 Postgres advisory xact lock 序列化同一個 Garmin 使用者的處理（匯入／清理／重新授權）。
// 鎖由一個開著的交易持有、交易結束即釋放：與 PgBouncer transaction pooling 相容（不依賴 session 層 advisory lock），
// 工作本身在其他連線上做。拿不到鎖回 errGarminBusy（暫時性，呼叫端稍後重試）。key 用 Garmin userId。
func (r *Repository) WithGarminUserLock(ctx context.Context, key string, fn func(ctx context.Context) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var got bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1))`, "garmin:"+key).Scan(&got); err != nil {
		return err
	}
	if !got {
		return errGarminBusy
	}
	if err := fn(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// --- 清除、統計、保活候選 ---

// PurgeGarminUser 單一交易清除一位使用者的全部 Garmin 資料（見 GarminHandler.PurgeUser 的說明）：
//  1. 把屬於這些活動的 mileage_exp_events 匿名化（去掉距離明細、開始時間與活動連結，保留 EXP／DP／公里帳務語意）；
//  2. 刪除全部 source='garmin' 的活動（先解除良性重複標記）；
//  3. 刪除該 Garmin userId 的所有事件列（任何狀態；garminUserID 為空則略過）；
//  4. 刪除 garmin 連線列；
//  5. 偏好來源若是 garmin 就重設回 gps。
//
// 不動 external_award_ledger（只存雜湊）。冪等。
func (r *Repository) PurgeGarminUser(ctx context.Context, userID, garminUserID string) (GarminPurgeResult, error) {
	var res GarminPurgeResult
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(context.Background())

	ct, err := tx.Exec(ctx, `
		UPDATE mileage_exp_events
		   SET recorded_at = NULL, activity_id = NULL, distance_km = km_added
		 WHERE user_id = $1
		   AND activity_id IN (SELECT id FROM activities WHERE user_id = $1 AND source = 'garmin')`, userID)
	if err != nil {
		return res, fmt.Errorf("anonymize mileage events: %w", err)
	}
	res.AnonymizedEvents = ct.RowsAffected()
	if res.DeletedActivities, err = garminDeleteActivitiesTx(ctx, tx, userID); err != nil {
		return res, err
	}
	if garminUserID != "" {
		ct, err = tx.Exec(ctx, `DELETE FROM integration_events WHERE provider='garmin' AND provider_user_id=$1`, garminUserID)
		if err != nil {
			return res, fmt.Errorf("delete garmin events: %w", err)
		}
		res.DeletedEvents = ct.RowsAffected()
	}
	ct, err = tx.Exec(ctx, `DELETE FROM user_integrations WHERE user_id=$1 AND provider='garmin'`, userID)
	if err != nil {
		return res, fmt.Errorf("delete garmin connection: %w", err)
	}
	res.HadConnection = ct.RowsAffected() > 0
	if _, err = tx.Exec(ctx, `UPDATE user_profiles SET preferred_data_source='gps', updated_at=NOW()
		WHERE user_id=$1 AND preferred_data_source='garmin'`, userID); err != nil {
		return res, fmt.Errorf("reset preferred data source: %w", err)
	}
	return res, tx.Commit(ctx)
}

// LatestGarminDeviceName 這位使用者最近一筆有型號的 garmin 活動的 device_name（沒有回空字串）。
func (r *Repository) LatestGarminDeviceName(ctx context.Context, userID string) (string, error) {
	var name string
	err := r.db.QueryRow(ctx, `SELECT device_name FROM activities
		WHERE user_id=$1 AND source='garmin' AND device_name IS NOT NULL AND device_name <> ''
		ORDER BY recorded_at DESC LIMIT 1`, userID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// garminConnStats：直連連線的人數統計（日報用，純 DB）。
type garminConnStats struct {
	Connected       int
	Active24h       int
	NeedsReauth     int
	Paused          int
	Stale3d         int
	PausedButActive int // 暫停旗標但 24 小時內仍有推送（異常訊號：暫停不該有推送）
}

// GarminConnStats 直連連線統計。generation＝目前 App 世代（issuer 不符視為需重新授權）。
func (r *Repository) GarminConnStats(ctx context.Context, generation string) (garminConnStats, error) {
	var s garminConnStats
	err := r.db.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE last_synced_at > now() - interval '24 hours'),
		       count(*) FILTER (WHERE reauth_required_at IS NOT NULL OR COALESCE(issuer,'') <> $1
		                          OR (refresh_expires_at IS NOT NULL AND refresh_expires_at <= now())),
		       count(*) FILTER (WHERE COALESCE(scope,'') <> '' AND position('ACTIVITY_EXPORT' in scope) = 0),
		       count(*) FILTER (WHERE COALESCE(connected_at, created_at) < now() - interval '3 days'
		                          AND (last_synced_at IS NULL OR last_synced_at < now() - interval '3 days')),
		       count(*) FILTER (WHERE COALESCE(scope,'') <> '' AND position('ACTIVITY_EXPORT' in scope) = 0
		                          AND last_synced_at > now() - interval '24 hours')
		  FROM user_integrations WHERE provider='garmin' AND via='direct'`, generation).
		Scan(&s.Connected, &s.Active24h, &s.NeedsReauth, &s.Paused, &s.Stale3d, &s.PausedButActive)
	return s, err
}

// GarminKeepaliveCandidates 需要保活的連線（refresh token 將在 within 內到期、未標記需重新授權、App 世代一致），
// 最近到期者優先，最多 limit 筆。不含 token（刷新時會在鎖內重讀）。
func (r *Repository) GarminKeepaliveCandidates(ctx context.Context, generation string, within time.Duration, limit int) ([]*garminConn, error) {
	rows, err := r.db.Query(ctx, garminConnCols+` WHERE provider='garmin' AND via='direct' AND reauth_required_at IS NULL
		AND COALESCE(issuer,'') = $1 AND refresh_expires_at IS NOT NULL AND refresh_expires_at > now()
		AND refresh_expires_at < now() + make_interval(secs => $2)
		ORDER BY refresh_expires_at LIMIT $3`, generation, within.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*garminConn
	for rows.Next() {
		c := &garminConn{}
		var access, refresh string
		if err := rows.Scan(&c.ID, &c.UserID, &c.ProviderUserID, &access, &refresh, &c.ExpiresAt,
			&c.Scope, &c.ConnectedAt, &c.Via, &c.Issuer, &c.RefreshExpiresAt, &c.ReauthRequiredAt,
			&c.ConsentAt, &c.ConsentVersion, &c.AuthorizedAt, &c.LastSyncedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
