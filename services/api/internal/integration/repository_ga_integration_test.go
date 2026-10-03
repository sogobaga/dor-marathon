//go:build integration

// migration 198、去重 SQL（直連優先、GPS 時間基準）、DeleteProviderActivities／PurgeCorosMcpUser 的排除與還原——
// 全部在真實 Postgres（本機拋棄式容器 dor_it_ws）上實跑。需要 DOR_TEST_DATABASE_URL。
package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type gaEnv struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	repo *Repository
}

func newGAEnv(t *testing.T) *gaEnv {
	t.Helper()
	pool := corosMcpSetupPool(t)
	return &gaEnv{t: t, ctx: context.Background(), pool: pool, repo: NewRepository(pool)}
}

func (e *gaEnv) user(label string) string {
	id := corosMcpCreateUser(e.t, e.ctx, e.pool, "ga-"+label+"-"+corosMcpRandSuffix()+"@example.com")
	e.t.Cleanup(func() {
		bg := context.Background()
		_, _ = e.pool.Exec(bg, `DELETE FROM mileage_exp_events WHERE user_id=$1`, id)
		_, _ = e.pool.Exec(bg, `DELETE FROM external_award_ledger WHERE user_id=$1`, id)
		_, _ = e.pool.Exec(bg, `DELETE FROM activities WHERE user_id=$1`, id)
	})
	return id
}

// gpsRow 直接塞一筆 App GPS 活動（source IS NULL；recorded_at 存「結束時間」）。
func (e *gaEnv) gpsRow(uid string, start time.Time, durS int, km float64) string {
	e.t.Helper()
	var id string
	end := start.Add(time.Duration(durS) * time.Second)
	if err := e.pool.QueryRow(e.ctx, `
		INSERT INTO activities (user_id, distance_km, duration_s, avg_pace_s, recorded_at, processed)
		VALUES ($1,$2,$3,$4,$5,TRUE) RETURNING id::text`, uid, km, durS, int(float64(durS)/km), end.UTC()).Scan(&id); err != nil {
		e.t.Fatalf("insert gps row: %v", err)
	}
	return id
}

type actState struct {
	Flagged bool
	Reason  string
	DupOf   string
	Exists  bool
}

func (e *gaEnv) state(id string) actState {
	e.t.Helper()
	var s actState
	var reason, dup *string
	err := e.pool.QueryRow(e.ctx, `SELECT flagged, flag_reason, dup_of::text FROM activities WHERE id=$1::uuid`, id).Scan(&s.Flagged, &reason, &dup)
	if err != nil {
		return actState{}
	}
	s.Exists = true
	if reason != nil {
		s.Reason = *reason
	}
	if dup != nil {
		s.DupOf = *dup
	}
	return s
}

func (e *gaEnv) stateByExt(uid, source, ext string) (string, actState) {
	e.t.Helper()
	var id string
	if err := e.pool.QueryRow(e.ctx, `SELECT id::text FROM activities WHERE user_id=$1 AND source=$2 AND external_id=$3`, uid, source, ext).Scan(&id); err != nil {
		return "", actState{}
	}
	return id, e.state(id)
}

func (e *gaEnv) imp(uid, source, ext string, start time.Time, durS int, km float64, fpSameAsPrecise bool) (ImportResult, *NormalizedActivity) {
	e.t.Helper()
	na := &NormalizedActivity{UserID: uid, Source: source, ExternalID: ext, DistanceKm: km, DurationS: durS,
		AvgPaceS: int(float64(durS) / km), RecordedAt: start.UTC()}
	if fpSameAsPrecise {
		na.Fingerprint = fingerprintOf(start.Unix(), km*1000, durS)
	} else {
		na.Fingerprint = fingerprintOf(start.Unix()+7, km*1000+3, durS+5) // 起始秒／距離／時長略有不同（不同裝置的常態）
	}
	res, err := e.repo.ImportActivity(e.ctx, na)
	if err != nil {
		e.t.Fatalf("ImportActivity(%s %s): %v", source, ext, err)
	}
	return res, na
}

// ---------- migration 198 ----------

func TestIT_Migration198_IdempotentAndPartialUniqueIndex(t *testing.T) {
	e := newGAEnv(t)
	b, err := os.ReadFile("../../migrations/198_coros_mcp_ga.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(b)
	for i := 0; i < 2; i++ { // 重複套用兩次必須成功（IF NOT EXISTS／ON CONFLICT）
		if _, err := e.pool.Exec(e.ctx, sqlText); err != nil {
			t.Fatalf("apply #%d: %v", i+1, err)
		}
	}
	var hasCol, hasIdx bool
	_ = e.pool.QueryRow(e.ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='user_integrations' AND column_name='reauth_required_at')`).Scan(&hasCol)
	_ = e.pool.QueryRow(e.ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename='user_integrations' AND indexname='user_integrations_provider_account_uniq')`).Scan(&hasIdx)
	if !hasCol || !hasIdx {
		t.Fatalf("migration 198 objects missing: column=%v index=%v", hasCol, hasIdx)
	}
	// 索引是部分唯一索引，且謂詞為 provider_user_id <> ''
	var def string
	_ = e.pool.QueryRow(e.ctx, `SELECT indexdef FROM pg_indexes WHERE indexname='user_integrations_provider_account_uniq'`).Scan(&def)
	if !strings.Contains(def, "UNIQUE") || !strings.Contains(def, "(provider, provider_user_id)") || !strings.Contains(def, "provider_user_id)::text <> ''") {
		t.Fatalf("unexpected index definition: %s", def)
	}
	// 預設設定列存在（缺鍵時程式也有預設）；重跑不覆蓋後台已改的值
	var v string
	if err := e.pool.QueryRow(e.ctx, `SELECT value FROM app_settings WHERE key='coros_mcp_autosync_enabled'`).Scan(&v); err != nil || (v != "1" && v != "0") {
		t.Fatalf("default setting row missing: %v %q", err, v)
	}
	// 套用前的唯讀預檢查查詢：全新庫必為空
	var dupGroups int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM (SELECT provider, provider_user_id FROM user_integrations WHERE provider_user_id <> '' GROUP BY 1,2 HAVING count(*) > 1) x`).Scan(&dupGroups); err != nil || dupGroups != 0 {
		t.Fatalf("pre-check query must be empty on a clean DB: %d %v", dupGroups, err)
	}

	// 索引語意
	u1, u2, u3 := e.user("idx1"), e.user("idx2"), e.user("idx3")
	ins := func(uid, provider, pid string) error {
		_, err := e.pool.Exec(e.ctx, `INSERT INTO user_integrations (user_id, provider, provider_user_id, access_token, refresh_token, expires_at)
			VALUES ($1,$2,$3,'a','r',NOW()+INTERVAL '1 day')`, uid, provider, pid)
		return err
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM user_integrations WHERE user_id = ANY($1::uuid[])`, []string{u1, u2, u3})
	})
	if err := ins(u1, "coros_mcp", "coros:acct-1"); err != nil {
		t.Fatal(err)
	}
	// 同 provider 同識別、不同 DOR 帳號 → 23505，且被 IsProviderAccountConflict 認出
	err = ins(u2, "coros_mcp", "coros:acct-1")
	if err == nil || !IsProviderAccountConflict(err) {
		t.Fatalf("same (provider, provider_user_id) on another user must violate the index and be recognised, got %v", err)
	}
	// 不同 provider 同識別 → 允許
	if err := ins(u2, "garmin", "coros:acct-1"); err != nil {
		t.Fatalf("different provider, same id must be allowed: %v", err)
	}
	// 空字串不受約束（舊連線、COROS 沒發 id_token 的連線）：多列都可以
	if err := ins(u2, "coros_mcp", ""); err != nil {
		t.Fatalf("empty provider_user_id must not conflict: %v", err)
	}
	if err := ins(u3, "coros_mcp", ""); err != nil {
		t.Fatalf("second empty provider_user_id must not conflict: %v", err)
	}
	// (user_id, provider) 的衝突不是帳號綁定衝突
	err = ins(u1, "coros_mcp", "coros:other")
	if err == nil || IsProviderAccountConflict(err) {
		t.Fatalf("(user_id, provider) duplicate is a different constraint, got conflict=%v err=%v", IsProviderAccountConflict(err), err)
	}
}

// ---------- 去重 SQL ----------

// 情境 ①：Strava 先到、直連後到（指紋相同與僅時間重疊兩種）→ 直連列計入、Strava 列被取代。
func TestIT_Dedup_StravaFirstDirectLater(t *testing.T) {
	for _, fpSame := range []bool{true, false} {
		e := newGAEnv(t)
		uid := e.user("strava-first")
		start := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Second)
		sres, _ := e.imp(uid, "strava", "str-"+corosMcpRandSuffix(), start, 2433, 5.31, fpSame)
		if sres.Status != "inserted" {
			t.Fatalf("strava seed: %+v", sres)
		}
		mres, mna := e.imp(uid, "coros", "mcp:"+corosMcpRandSuffix(), start, 2433, 5.31, fpSame)
		if mres.Status != "inserted" || mres.Superseded != 1 || mres.Reason != "" {
			t.Fatalf("fpSame=%v: direct row must be inserted and supersede the Strava row: %+v", fpSame, mres)
		}
		if st := e.state(mres.ID); st.Flagged {
			t.Fatalf("fpSame=%v: direct row must count: %+v", fpSame, st)
		}
		st := e.state(sres.ID)
		if !st.Flagged || st.Reason != "cross_source_duplicate" || st.DupOf != mres.ID {
			t.Fatalf("fpSame=%v: Strava row must be cross_source_duplicate → direct row, got %+v", fpSame, st)
		}
		// 再次匯入同一筆直連（COROS 每次同步都會重抓最近幾天）→ exists，不會再翻盤、不重複計
		if again, _ := e.imp(uid, "coros", mna.ExternalID, start, 2433, 5.31, fpSame); again.Status != "exists" {
			t.Fatalf("fpSame=%v: re-fetching the same direct activity must be 'exists', got %+v", fpSame, again)
		}
		// 中斷 COROS 直連：Strava 列自動還原成計入
		if _, _, err := e.repo.PurgeCorosMcpUser(e.ctx, uid); err != nil {
			t.Fatal(err)
		}
		if st := e.state(sres.ID); st.Flagged || st.Reason != "" || st.DupOf != "" {
			t.Fatalf("fpSame=%v: after the direct rows are purged the superseded Strava row counts again: %+v", fpSame, st)
		}
	}
}

// 情境 ②：直連先到、Strava 後到 → Strava 新列標重複，直連列保留。
func TestIT_Dedup_DirectFirstStravaLater(t *testing.T) {
	e := newGAEnv(t)
	uid := e.user("direct-first")
	start := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Second)
	mres, _ := e.imp(uid, "coros", "mcp:"+corosMcpRandSuffix(), start, 2433, 5.31, true)
	if mres.Status != "inserted" {
		t.Fatalf("direct seed: %+v", mres)
	}
	sres, _ := e.imp(uid, "strava", "str-"+corosMcpRandSuffix(), start, 2433, 5.31, false)
	if sres.Status != "duplicate" || sres.Reason != "multi_device_duplicate" || sres.Superseded != 0 {
		t.Fatalf("Strava arriving after a direct row is the flagged one: %+v", sres)
	}
	if st := e.state(sres.ID); !st.Flagged || st.DupOf != mres.ID {
		t.Fatalf("Strava row must point at the direct row: %+v", st)
	}
	if st := e.state(mres.ID); st.Flagged {
		t.Fatalf("direct row must stay counted: %+v", st)
	}
}

// 情境 ③（§2.8）：手機 GPS 比手錶晚停 → 要判為重複；GPS 結束後 N 分鐘才開始的獨立跑步 → 不能被誤判重複。
func TestIT_Dedup_GpsTimeBasis(t *testing.T) {
	e := newGAEnv(t)
	base := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Second)

	// (a) GPS 08:00–08:45（recorded_at＝結束 08:45）；手錶 08:02–08:40 → 重疊 → multi_device_duplicate
	uidA := e.user("gps-late")
	gpsA := e.gpsRow(uidA, base, 45*60, 8.1)
	resA, _ := e.imp(uidA, "coros", "mcp:"+corosMcpRandSuffix(), base.Add(2*time.Minute), 38*60, 7.9, false)
	if resA.Status != "duplicate" || resA.Reason != "multi_device_duplicate" {
		t.Fatalf("GPS that stopped later than the watch must still be recognised as the same run: %+v", resA)
	}
	if st := e.state(resA.ID); st.DupOf != gpsA {
		t.Fatalf("dup_of must point at the GPS row: %+v (gps=%s)", st, gpsA)
	}

	// (b) GPS 08:00–08:30；手錶獨立跑步 08:40–09:10（GPS 結束後 10 分鐘）→ 不是重複，要計入
	uidB := e.user("gps-then-run")
	e.gpsRow(uidB, base, 30*60, 5.0)
	resB, _ := e.imp(uidB, "coros", "mcp:"+corosMcpRandSuffix(), base.Add(40*time.Minute), 30*60, 5.2, false)
	if resB.Status != "inserted" {
		t.Fatalf("an independent run starting 10 minutes after the GPS run ended must NOT be flagged as a duplicate (old SQL treated GPS's END time as its start): %+v", resB)
	}

	// (c) 反向：手錶先、GPS 時間較長包住它 → 同一趟
	uidC := e.user("gps-wraps")
	gpsC := e.gpsRow(uidC, base, 50*60, 9.0)
	resC, _ := e.imp(uidC, "strava", "str-"+corosMcpRandSuffix(), base.Add(5*time.Minute), 40*60, 7.0, false)
	if resC.Status != "duplicate" || e.state(resC.ID).DupOf != gpsC {
		t.Fatalf("watch run wrapped by the GPS run is the same run: %+v", resC)
	}

	// (d) GPS 與手錶完全不相干（隔 3 小時）→ 計入
	uidD := e.user("gps-far")
	e.gpsRow(uidD, base, 30*60, 5.0)
	resD, _ := e.imp(uidD, "coros", "mcp:"+corosMcpRandSuffix(), base.Add(4*time.Hour), 30*60, 5.1, false)
	if resD.Status != "inserted" {
		t.Fatalf("unrelated run: %+v", resD)
	}
}

// 直連列與「非 Strava 的既有列」重疊 → 不翻盤（先到先贏），而且 Strava 候選不會被連帶動到。
func TestIT_Dedup_NoFlipWhenTerraRowPresent(t *testing.T) {
	e := newGAEnv(t)
	uid := e.user("terra-first")
	start := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Second)
	tres, _ := e.imp(uid, "coros", "terra-"+corosMcpRandSuffix(), start, 2433, 5.31, false)
	if tres.Status != "inserted" {
		t.Fatalf("terra seed: %+v", tres)
	}
	// 距離／時長略有不同（指紋不同，只靠時間重疊判斷）：兩個裝置記同一趟的常態
	mres, _ := e.imp(uid, "coros", "mcp:"+corosMcpRandSuffix(), start.Add(20*time.Second), 2440, 5.33, false)
	if mres.Status != "duplicate" || mres.Reason != "multi_device_duplicate" || mres.Superseded != 0 {
		t.Fatalf("direct row after a Terra row is flagged (first come first served): %+v", mres)
	}
	if e.state(tres.ID).Flagged {
		t.Fatal("the Terra row stays counted")
	}
}

// 跨帳號複製：指紋完全相同的直連列（另一個 DOR 帳號）→ cross_account_duplicate，不翻盤任何東西。
func TestIT_Dedup_CrossAccountCopyIsFlagged(t *testing.T) {
	e := newGAEnv(t)
	u1, u2 := e.user("owner"), e.user("copier")
	start := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Second)
	r1, _ := e.imp(u1, "coros", "mcp:"+corosMcpRandSuffix(), start, 2433, 5.31, true)
	if r1.Status != "inserted" {
		t.Fatal("seed")
	}
	r2, _ := e.imp(u2, "coros", "mcp:"+corosMcpRandSuffix(), start, 2433, 5.31, true)
	if r2.Status != "duplicate" || r2.Reason != "cross_account_duplicate" {
		t.Fatalf("identical activity on another account is a cross-account copy: %+v", r2)
	}
}

// 合理性檢查（經 ImportActivity）：skip 不寫、flag 寫入但標記；既有 importer 的 "skipped" 不崩。
func TestIT_ImportActivity_Plausibility(t *testing.T) {
	e := newGAEnv(t)
	uid := e.user("plaus")
	start := time.Now().UTC().Add(-5 * time.Hour).Truncate(time.Second)
	// skip：距離過短
	res, err := e.repo.ImportActivity(e.ctx, &NormalizedActivity{UserID: uid, Source: "coros", ExternalID: "mcp:short", DistanceKm: 0.05, DurationS: 120, AvgPaceS: 2400, RecordedAt: start})
	if err != nil || res.Status != "skipped" || res.Reason != SkipTooShortDist || res.ID != "" {
		t.Fatalf("too short: %+v %v", res, err)
	}
	var n int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM activities WHERE user_id=$1 AND external_id='mcp:short'`, uid).Scan(&n)
	if n != 0 {
		t.Fatal("skipped activity must not be written")
	}
	// flag：不可能的配速 → 寫入、flagged、implausible_pace、dup_of 為空；回 duplicate（既有 switch 相容）
	res, err = e.repo.ImportActivity(e.ctx, &NormalizedActivity{UserID: uid, Source: "strava", ExternalID: "str-fast-" + corosMcpRandSuffix(), DistanceKm: 10, DurationS: 600, AvgPaceS: 60, RecordedAt: start})
	if err != nil || res.Status != "duplicate" || res.Reason != FlagImplausiblePace || res.ID == "" {
		t.Fatalf("impossible pace: %+v %v", res, err)
	}
	if st := e.state(res.ID); !st.Flagged || st.Reason != "implausible_pace" || st.DupOf != "" {
		t.Fatalf("stored row: %+v", st)
	}
	// 走路類門檻 4:00/km：同樣資料以 walk 判定
	res, err = e.repo.ImportActivity(e.ctx, &NormalizedActivity{UserID: uid, Source: "strava", ExternalID: "str-walk-" + corosMcpRandSuffix(), DistanceKm: 5, DurationS: 900, AvgPaceS: 180, RecordedAt: start.Add(-3 * time.Hour), Kind: KindWalk})
	if err != nil || res.Reason != FlagImplausiblePace {
		t.Fatalf("walk pace 3:00/km must be flagged: %+v %v", res, err)
	}
	// 正常活動照常寫入；同一筆再匯入一次 → exists（先於去重查詢）
	normal := &NormalizedActivity{UserID: uid, Source: "strava", ExternalID: "x-" + corosMcpRandSuffix(), DistanceKm: 5, DurationS: 1500, AvgPaceS: 300, RecordedAt: start.Add(-20 * time.Hour)}
	first, err := e.repo.ImportActivity(e.ctx, normal)
	if err != nil || first.Status != "inserted" {
		t.Fatalf("normal: %+v %v", first, err)
	}
	if again, err := e.repo.ImportActivity(e.ctx, normal); err != nil || again.Status != "exists" {
		t.Fatalf("re-import: %+v %v", again, err)
	}
}

// ---------- DeleteProviderActivities 排除 mcp:／gc: ----------

func TestIT_DeleteProviderActivities_KeepsDirectWatchRows(t *testing.T) {
	e := newGAEnv(t)
	uid := e.user("delete")
	ts := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	sfx := corosMcpRandSuffix() // (source, external_id) 全域唯一：每次執行加唯一尾碼（前綴 mcp:／gc: 語意不變）
	seed := func(source, ext string, off time.Duration) string {
		na := &NormalizedActivity{UserID: uid, Source: source, ExternalID: ext + "-" + sfx, DistanceKm: 5, DurationS: 1500, AvgPaceS: 300, RecordedAt: ts.Add(off), Fingerprint: fingerprintOf(ts.Add(off).Unix(), 5000, 1500)}
		res, err := e.repo.ImportActivity(e.ctx, na)
		if err != nil || res.Status != "inserted" {
			t.Fatalf("seed %s %s: %+v %v", source, ext, res, err)
		}
		return res.ID
	}
	terraCoros := seed("coros", "terra-1", 0)
	partnerCoros := seed("coros", mcpTestLabel(7), 4*time.Hour) // Partner API：純 labelId（每次執行唯一，避免撞 (source, external_id) 唯一鍵）
	mcp := seed("coros", "mcp:1", 8*time.Hour)
	terraGarmin := seed("garmin", "terra-2", 12*time.Hour)
	gc := seed("garmin", "gc:77", 16*time.Hour)
	strava := seed("strava", "str-1", 20*time.Hour)
	exists := func(id string) bool { return e.state(id).Exists }

	if err := e.repo.DeleteProviderActivities(e.ctx, uid, "coros"); err != nil {
		t.Fatal(err)
	}
	if exists(terraCoros) || exists(partnerCoros) || !exists(mcp) {
		t.Fatalf("coros: Terra/Partner rows deleted, MCP kept: terra=%v partner=%v mcp=%v", exists(terraCoros), exists(partnerCoros), exists(mcp))
	}
	if err := e.repo.DeleteProviderActivities(e.ctx, uid, "garmin"); err != nil {
		t.Fatal(err)
	}
	if exists(terraGarmin) || !exists(gc) {
		t.Fatalf("garmin: Terra row deleted, direct gc: row kept: terra=%v gc=%v", exists(terraGarmin), exists(gc))
	}
	if !exists(strava) {
		t.Fatal("strava untouched")
	}
	if err := e.repo.DeleteProviderActivities(e.ctx, uid, "strava"); err != nil {
		t.Fatal(err)
	}
	if exists(strava) || !exists(mcp) || !exists(gc) {
		t.Fatal("strava delete must not touch direct rows")
	}
	// DeleteCorosMcpActivities（舊入口）仍只刪 mcp:
	if n, err := e.repo.DeleteCorosMcpActivities(e.ctx, uid); err != nil || n != 1 || exists(mcp) || !exists(gc) {
		t.Fatalf("DeleteCorosMcpActivities: n=%d err=%v mcp=%v gc=%v", n, err, exists(mcp), exists(gc))
	}
}

// 被刪的 Terra／Strava 列留下的「重複」標記要還原；但指向直連列的標記不能被 Terra 中斷誤清。
func TestIT_DeleteProviderActivities_ClearsOnlyStaleDupFlags(t *testing.T) {
	e := newGAEnv(t)
	uid := e.user("stale")
	start := time.Now().UTC().Add(-30 * time.Hour).Truncate(time.Second)
	terra, _ := e.imp(uid, "coros", "terra-"+corosMcpRandSuffix(), start, 2433, 5.31, false)
	// 另一個 App 的活動與 Terra 列重疊 → multi_device_duplicate → terra
	other, _ := e.imp(uid, "polar", "pol-"+corosMcpRandSuffix(), start.Add(time.Minute), 2400, 5.2, false)
	if other.Status != "duplicate" || e.state(other.ID).DupOf != terra.ID {
		t.Fatalf("setup: %+v", other)
	}
	if err := e.repo.DeleteProviderActivities(e.ctx, uid, "coros"); err != nil {
		t.Fatal(err)
	}
	if st := e.state(other.ID); st.Flagged || st.DupOf != "" || st.Reason != "" {
		t.Fatalf("flag pointing at the deleted Terra row must be cleared: %+v", st)
	}
}
