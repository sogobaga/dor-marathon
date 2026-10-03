package runmeet

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// live_start_audit_dedupe_test.go：同意稽核列的去重視窗（15 分鐘）。
//
// 去重本身在 SQL 裡（pgLiveRepo.InsertConsentAudit 的單一 INSERT … SELECT … WHERE NOT EXISTS），這支檔案能驗的是：
//  1. handler 的行為不變——每次 reauth:false 的 start 仍然「先要求寫證據、再建 grant」，grant 每次都會建立；
//     fake repo 模擬同一個去重語意，確認重複 start 不會讓稽核列堆積（頁面重新整理、start 在證據寫入之後才失敗的重試）；
//  2. SQL 的結構（單一語句、去重條件、欄位與 meta 不變）——避免有人無意中拿掉 NOT EXISTS。
//  SQL 實際對 PostgreSQL 的行為：live_start_integration_test.go 的 04b／06b（build tag integration，本機沒有資料庫，
//  這次沒有執行）。

func TestLiveStartRepeatedFirstStartsKeepOneAuditRowPerWindow(t *testing.T) {
	e := newStartEnv(t)
	clock := time.Date(2026, 10, 10, 11, 50, 0, 0, taipei)
	e.repo.auditNow = func() time.Time { return clock }
	uid := e.member(MemberJoined)
	first := clock

	// client 合法地一再送 reauth:false：頁面重新整理後自動接續、離開再回來……（2 分鐘一次，全在視窗內）
	for i := 1; i <= 4; i++ {
		rec := e.start(uid)
		if rec.Code != http.StatusOK {
			t.Fatalf("start #%d: %d %s", i, rec.Code, rec.Body.String())
		}
		clock = clock.Add(2 * time.Minute)
	}
	if got := len(e.repo.audit); got != 1 {
		t.Fatalf("audit rows after 4 first-starts inside the window = %d, want 1", got)
	}
	if e.repo.auditAttempts != 4 {
		t.Fatalf("InsertConsentAudit was asked %d times, want 4: the handler must still ask on every non-reauth start "+
			"(the dedupe lives in the repository, the handler's evidence-before-grant ordering is unchanged)", e.repo.auditAttempts)
	}
	// grant 每次都會建立（證據已存在 ≠ 不建 grant）
	if e.mr.HGet("rml:{"+liveMeetUUID+"}:grants", uid) == "" {
		t.Fatal("the grant must exist")
	}

	// 視窗過後（距第一列 16 分鐘）→ 再寫一列
	clock = first.Add(16 * time.Minute)
	if rec := e.start(uid); rec.Code != http.StatusOK {
		t.Fatalf("start after the window: %d", rec.Code)
	}
	if got := len(e.repo.audit); got != 2 {
		t.Fatalf("audit rows after the window expired = %d, want 2", got)
	}

	// 不同使用者／不同團練各自獨立
	other := e.member(MemberJoined)
	if rec := e.start(other); rec.Code != http.StatusOK {
		t.Fatalf("other user: %d", rec.Code)
	}
	if got := len(e.repo.audit); got != 3 {
		t.Fatalf("a different user needs their own evidence row: rows = %d, want 3", got)
	}
	otherMeet := uuid.NewString()
	if rec := e.post(uid, otherMeet, startBody(consentV1, false)); rec.Code != http.StatusOK {
		t.Fatalf("other meet: %d %s", rec.Code, rec.Body.String())
	}
	if got := len(e.repo.audit); got != 4 {
		t.Fatalf("a different meet needs its own evidence row: rows = %d, want 4", got)
	}
}

// 「證據寫入之後 start 才失敗」的重試（例如 Redis 的 Lua 逾時 → 503，或名額已滿 → 429）：
// 每次重試都是 reauth:false，不去重就是每次一列。這裡用名額已滿當代表（Lua 在證據之後才拒絕）。
func TestLiveStartRetriesAfterAFailedStartDoNotPileUpAuditRows(t *testing.T) {
	e := newStartEnv(t)
	e.repo.settings.Max = 1
	holder, waiting := e.member(MemberJoined), e.member(MemberJoined)
	if rec := e.start(holder); rec.Code != http.StatusOK {
		t.Fatalf("holder: %d", rec.Code)
	}
	for i := 1; i <= 5; i++ {
		rec := e.start(waiting)
		if rec.Code != http.StatusTooManyRequests || codeOf(t, rec) != "live_full" {
			t.Fatalf("waiting #%d: %d %s, want 429 live_full", i, rec.Code, rec.Body.String())
		}
	}
	var n int
	for _, r := range e.repo.audit {
		if r.uid == waiting {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("5 rejected retries left %d audit rows for the waiting user, want 1", n)
	}
	if e.repo.auditAttempts != 6 {
		t.Fatalf("attempts = %d, want 6 (holder 1 + waiting 5): every non-reauth start still asks", e.repo.auditAttempts)
	}
}

// 去重不得改變「證據先於 grant」：寫不進證據就不建 grant（既有行為，去重後仍成立）。
func TestLiveStartStillWritesEvidenceBeforeTheGrantWithDedupe(t *testing.T) {
	e := newStartEnv(t)
	e.repo.auditErr = errTest("insert failed")
	uid := e.member(MemberJoined)
	if rec := e.start(uid); rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d", rec.Code)
	}
	if e.mr.HGet("rml:{"+liveMeetUUID+"}:grants", uid) != "" {
		t.Fatal("no grant may exist when the consent evidence could not be written")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

// SQL 結構：單一語句、去重條件完整、欄位與 meta 不變。
func TestInsertConsentAuditSQLIsOneDedupedStatement(t *testing.T) {
	if consentDedupeWindowMinutes != 15 {
		t.Fatalf("dedupe window = %d minutes, want 15 (reviewer-specified)", consentDedupeWindowMinutes)
	}
	sql := strings.Join(strings.Fields(insertConsentAuditSQL), " ")
	for _, want := range []string{
		"INSERT INTO audit_logs (user_id, action, resource, resource_id, meta, ip)",
		"SELECT $1::uuid, $2::text, $3::text, $4::uuid, $5::jsonb, $6::text",
		"WHERE NOT EXISTS ( SELECT 1 FROM audit_logs WHERE user_id = $1::uuid AND action = $2::text AND resource_id = $4::uuid " +
			"AND created_at > NOW() - INTERVAL '15 minutes')",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL lacks %q\n got: %s", want, sql)
		}
	}
	if strings.Contains(sql, ";") {
		t.Errorf("the insert must be a single statement: %s", sql)
	}
	if strings.Contains(sql, "VALUES") {
		t.Errorf("the dedupe needs INSERT … SELECT … WHERE NOT EXISTS, not INSERT … VALUES: %s", sql)
	}
}
