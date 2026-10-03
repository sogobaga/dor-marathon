package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dor/api/internal/mailer"
)

// 後台廣播「空對象不可變成全體」的單元測試（不需 DB，預設 `go test ./...` 就會跑）。
//
// 背景：race／group 目前沒有任何成員時 resolveBroadcastTargets 回 (空清單, isAll=false)，舊的
// listSubscriptions 把空清單當成「全部」，推播頻道就把訊息送給全站訂閱者。
//
// Handler.db 是 pgExecutor 介面，這裡換成 fakeDB：依 SQL 片段回罐頭資料並記錄每次呼叫，
// 所以能直接斷言「有沒有查 push_subscriptions」「試圖送給了哪幾筆訂閱」。
// 假訂閱的 endpoint 一律不在白名單：send() 在白名單檢查就刪列並回 errInvalidPushEndpoint，
// 碰不到 DNS／webpush／網路——「這筆訂閱被試圖送出」＝「它的 id 出現在 fakeDB.deleted」。

// --- 純函式 ---

func TestCheckBroadcastAudience(t *testing.T) {
	cases := []struct {
		name    string
		userIDs []string
		isAll   bool
		wantErr bool
	}{
		// 全體廣播：依契約 userIDs 恆為空，但 isAll=true 必須放行
		{"all with nil ids", nil, true, false},
		{"all with empty ids", []string{}, true, false},
		// 指定對象：至少一位才放行
		{"single user", []string{"u1"}, false, false},
		{"race with members", []string{"u1", "u2"}, false, false},
		{"group with one member", []string{"u1"}, false, false},
		// race／group 沒有成員：非全體 + 空清單 → 擋下（原本的 bug 入口）
		{"race without members (nil)", nil, false, true},
		{"group without members (empty)", []string{}, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkBroadcastAudience(tc.userIDs, tc.isAll)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkBroadcastAudience(%v, %v) err = %v, wantErr %v", tc.userIDs, tc.isAll, err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, errEmptyAudience) {
				t.Errorf("err = %v, want errEmptyAudience", err)
			}
		})
	}
}

// 訊息會原樣顯示在後台（Broadcast 回 400 的 error 欄位，前端 catch 後 setErr(e.message)），文字是介面契約。
func TestErrEmptyAudienceMessage(t *testing.T) {
	const want = "目標對象目前沒有任何會員，未送出"
	if got := errEmptyAudience.Error(); got != want {
		t.Errorf("errEmptyAudience = %q, want %q", got, want)
	}
}

// --- 訂閱查詢：空清單絕不等於「全部」 ---

func TestListSubscriptionsForEmptyNeverQueries(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
	}{
		{"nil ids", nil},
		{"empty ids", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeDB{t: t, subs: poisonSubs("u1", 2)}
			subs, err := newTestHandler(db, nil).listSubscriptionsFor(context.Background(), tc.ids)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if subs != nil {
				t.Errorf("subs = %v, want nil (empty ids must never mean everyone)", subs)
			}
			if len(db.calls) != 0 {
				t.Errorf("listSubscriptionsFor(%v) touched the DB: %v", tc.ids, db.calls)
			}
		})
	}
}

func TestListSubscriptionsForOnlyReturnsGivenUsers(t *testing.T) {
	db := &fakeDB{t: t, subs: slices.Concat(poisonSubs("u1", 2), poisonSubs("u2", 1))}
	subs, err := newTestHandler(db, nil).listSubscriptionsFor(context.Background(), []string{"u2"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got, want := idsOf(subs), idsOf(poisonSubs("u2", 1)); !slices.Equal(got, want) {
		t.Errorf("subscriptions = %v, want %v", got, want)
	}
}

func TestListAllSubscriptionsReturnsEverything(t *testing.T) {
	all := slices.Concat(poisonSubs("u1", 2), poisonSubs("u2", 1))
	db := &fakeDB{t: t, subs: all}
	subs, err := newTestHandler(db, nil).listAllSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got, want := sorted(idsOf(subs)), sorted(idsOf(all)); !slices.Equal(got, want) {
		t.Errorf("subscriptions = %v, want %v", got, want)
	}
}

// --- (a) race／group 沒有任何成員：400，且任何頻道都不送 ---

func TestBroadcastEmptyAudienceRespondsBadRequestAndSendsNothing(t *testing.T) {
	// u1、u2 是「旁觀者」：不在這個 race／group 裡，但手上有推播訂閱——舊行為會把推播送給他們。
	bystanders := slices.Concat(poisonSubs("u1", 2), poisonSubs("u2", 1))

	cases := []struct {
		name     string
		payload  map[string]any
		channels []string
	}{
		{"race 沒有任何報名 / 推播+Email+站內信", map[string]any{"target_type": "race", "race_id": "race-1"}, []string{"push", "email", "mail"}},
		{"race 沒有任何報名 / 只推播", map[string]any{"target_type": "race", "race_id": "race-1"}, []string{"push"}},
		{"group 沒有任何成員 / 推播+Email+站內信", map[string]any{"target_type": "group", "group_id": "group-1"}, []string{"push", "email", "mail"}},
		{"group 沒有任何成員 / 只推播", map[string]any{"target_type": "group", "group_id": "group-1"}, []string{"push"}},
		// 依規格「對象為空一律 400」，不分頻道（只勾站內信本來不會誤送全體，行為與其他頻道一致）
		{"group 沒有任何成員 / 只站內信", map[string]any{"target_type": "group", "group_id": "group-1"}, []string{"mail"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// raceMembers／groupMembers 留空＝這個 race／group 沒有任何有效成員
			db := &fakeDB{t: t, subs: bystanders, allUserIDs: []string{"u1", "u2", "u3"}}
			mail := &fakeMail{}
			h := newTestHandler(db, mail)

			tc.payload["channels"] = tc.channels
			status, out := postBroadcast(t, h, tc.payload)

			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %v)", status, out)
			}
			if out["error"] != errEmptyAudience.Error() {
				t.Errorf("error = %v, want %q", out["error"], errEmptyAudience.Error())
			}
			if _, sent := out["push_sent"]; sent {
				t.Errorf("response carries send stats although nothing should be sent: %v", out)
			}
			// 解析完對象後不得再碰任何 DB：只准有「查 race 報名／群組成員」那一句，
			// 不查訂閱、不查 users（Email／站內信的收件人）、也沒有 Exec（刪訂閱＝試圖送出）。
			for _, c := range db.calls {
				if !strings.Contains(c.sql, "FROM registrations") && !strings.Contains(c.sql, "FROM account_group_members") {
					t.Errorf("unexpected DB call after resolving an empty audience: %s", c.sql)
				}
			}
			if len(db.subQueries()) != 0 {
				t.Errorf("push_subscriptions was queried: %v", db.subQueries())
			}
			if len(db.deleted) != 0 {
				t.Errorf("push send was attempted for subscriptions %v", db.deleted)
			}
			if mail.calls != 0 {
				t.Errorf("in-app mail inserted %d time(s), want 0", mail.calls)
			}
		})
	}
}

// --- (b) target_type=all 仍查全部訂閱 ---

func TestBroadcastAllStillListsEverySubscription(t *testing.T) {
	subs := slices.Concat(poisonSubs("u1", 2), poisonSubs("u2", 1), poisonSubs("u3", 1))
	users := []string{"u1", "u2", "u3", "u4"} // u4 沒有訂閱
	db := &fakeDB{t: t, subs: subs, allUserIDs: users}
	mail := &fakeMail{}
	h := newTestHandler(db, mail)

	status, out := postBroadcast(t, h, map[string]any{"target_type": "all", "channels": []string{"push", "mail"}})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, out)
	}
	if got := num(t, out, "recipients"); got != len(users) {
		t.Errorf("recipients = %d, want %d", got, len(users))
	}
	if got := num(t, out, "push_failed"); got != len(subs) { // 假端點 → 每筆都 errInvalidPushEndpoint
		t.Errorf("push_failed = %d, want %d (every subscription attempted)", got, len(subs))
	}
	if got := num(t, out, "push_sent"); got != 0 {
		t.Errorf("push_sent = %d, want 0", got)
	}
	if got, want := sorted(db.deleted), sorted(idsOf(subs)); !slices.Equal(got, want) {
		t.Errorf("attempted subscriptions = %v, want all of %v", got, want)
	}
	// 訂閱查詢恰好一次，且是「全部」版本（沒有 user_id 篩選）
	q := db.subQueries()
	if len(q) != 1 || strings.Contains(q[0].sql, "ANY") {
		t.Errorf("push_subscriptions queries = %v, want exactly one unfiltered query", q)
	}
	// 站內信頻道不受空對象保護影響：一次、收件人是全部使用者
	if mail.calls != 1 {
		t.Errorf("in-app mail calls = %d, want 1", mail.calls)
	}
	if !slices.Equal(mail.lastIDs, users) {
		t.Errorf("in-app mail recipients = %v, want %v", mail.lastIDs, users)
	}
	if got := num(t, out, "mail_sent"); got != len(users) {
		t.Errorf("mail_sent = %d, want %d", got, len(users))
	}
}

// 有成員的 race／group、單一帳號：仍只送給該對象，其他人的訂閱一筆都不碰。
func TestBroadcastScopedTargetsOnlyReachTheirMembers(t *testing.T) {
	subs := slices.Concat(poisonSubs("u1", 2), poisonSubs("u2", 1), poisonSubs("u3", 1))

	cases := []struct {
		name       string
		db         *fakeDB
		payload    map[string]any
		wantUsers  []string // 預期被試圖送出的使用者
		wantSubs   []storedSubscription
		wantFilter []string // 訂閱查詢的 user_id 篩選（單一帳號／race／group 都該是這份清單）
	}{
		{
			name:       "group 有一位成員",
			db:         &fakeDB{groupMembers: []string{"u1"}},
			payload:    map[string]any{"target_type": "group", "group_id": "group-1"},
			wantUsers:  []string{"u1"},
			wantSubs:   poisonSubs("u1", 2),
			wantFilter: []string{"u1"},
		},
		{
			name:       "race 有兩位報名者",
			db:         &fakeDB{raceMembers: []string{"u2", "u3"}},
			payload:    map[string]any{"target_type": "race", "race_id": "race-1"},
			wantUsers:  []string{"u2", "u3"},
			wantSubs:   slices.Concat(poisonSubs("u2", 1), poisonSubs("u3", 1)),
			wantFilter: []string{"u2", "u3"},
		},
		{
			name:       "單一帳號（以 email 指定）",
			db:         &fakeDB{usersByEmail: map[string]string{"someone@example.invalid": "u2"}},
			payload:    map[string]any{"target_type": "user", "identifier": "someone@example.invalid"},
			wantUsers:  []string{"u2"},
			wantSubs:   poisonSubs("u2", 1),
			wantFilter: []string{"u2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.db.t = t
			tc.db.subs = subs
			h := newTestHandler(tc.db, &fakeMail{})

			tc.payload["channels"] = []string{"push"}
			status, out := postBroadcast(t, h, tc.payload)

			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %v)", status, out)
			}
			if got := num(t, out, "recipients"); got != len(tc.wantUsers) {
				t.Errorf("recipients = %d, want %d", got, len(tc.wantUsers))
			}
			if got := num(t, out, "push_failed"); got != len(tc.wantSubs) {
				t.Errorf("push_failed = %d, want %d (only these users' subscriptions)", got, len(tc.wantSubs))
			}
			if got, want := sorted(tc.db.deleted), sorted(idsOf(tc.wantSubs)); !slices.Equal(got, want) {
				t.Errorf("attempted subscriptions = %v, want %v", got, want)
			}
			q := tc.db.subQueries()
			if len(q) != 1 {
				t.Fatalf("push_subscriptions queries = %v, want exactly one", q)
			}
			if got, _ := q[0].args[0].([]string); !slices.Equal(sorted(got), sorted(tc.wantFilter)) || !strings.Contains(q[0].sql, "ANY") {
				t.Errorf("subscription query = %q args %v, want a user_id filter of %v", q[0].sql, q[0].args, tc.wantFilter)
			}
		})
	}
}

// --- (c) SendToUser 只送給指定的那一位 ---

func TestSendToUserTargetsExactlyOneUser(t *testing.T) {
	db := &fakeDB{t: t, subs: slices.Concat(poisonSubs("u1", 2), poisonSubs("u2", 1))}
	h := newTestHandler(db, nil)
	msg := PushMessage{Title: "t", Body: "b"}
	ctx := context.Background()

	// u1：兩筆訂閱都被試圖送出（假端點 → errInvalidPushEndpoint，SendToUser 回第一個錯誤），u2 的不動。
	if err := h.SendToUser(ctx, "u1", msg); !errors.Is(err, errInvalidPushEndpoint) {
		t.Fatalf("SendToUser(u1) err = %v, want errInvalidPushEndpoint", err)
	}
	if got, want := sorted(db.deleted), sorted(idsOf(poisonSubs("u1", 2))); !slices.Equal(got, want) {
		t.Errorf("attempted subscriptions = %v, want u1's %v", got, want)
	}
	assertSingleUserQuery(t, db, "u1")

	// 沒有任何訂閱的 u3：沒東西可送，更不能因為「空」連帶送給別人。
	db.deleted, db.calls = nil, nil
	if err := h.SendToUser(ctx, "u3", msg); err != nil {
		t.Fatalf("SendToUser(u3, no subscriptions) err = %v, want nil", err)
	}
	if len(db.deleted) != 0 {
		t.Errorf("SendToUser(u3) attempted subscriptions %v, want none", db.deleted)
	}
	assertSingleUserQuery(t, db, "u3")
}

// SendToUser 沒設齊 VAPID 時整個 no-op：連 DB 都不碰。
func TestSendToUserDisabledIsNoop(t *testing.T) {
	db := &fakeDB{t: t, subs: poisonSubs("u1", 1)}
	h := newTestHandler(db, nil)
	h.cfg = Config{}
	if err := h.SendToUser(context.Background(), "u1", PushMessage{Title: "t"}); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(db.calls) != 0 {
		t.Errorf("disabled SendToUser touched the DB: %v", db.calls)
	}
}

// assertSingleUserQuery 斷言恰好查了一次訂閱，且是只篩這一位 user_id 的版本。
func assertSingleUserQuery(t *testing.T, db *fakeDB, userID string) {
	t.Helper()
	q := db.subQueries()
	if len(q) != 1 {
		t.Fatalf("push_subscriptions queries = %v, want exactly one", q)
	}
	got, _ := q[0].args[0].([]string)
	if !strings.Contains(q[0].sql, "ANY") || !slices.Equal(got, []string{userID}) {
		t.Errorf("subscription query = %q args %v, want a filter of exactly [%s]", q[0].sql, q[0].args, userID)
	}
}

// --- 測試替身 ---

type dbCall struct {
	sql  string // 空白已壓縮
	args []any
}

// fakeDB 只實作 Broadcast／SendToUser 會用到的幾條查詢，依 SQL 片段回罐頭資料並記錄每次呼叫；
// 碰到不認得的 SQL 直接 Fatalf（避免新增了查詢而測試悄悄漏掉）。
type fakeDB struct {
	t *testing.T

	// 罐頭資料
	raceMembers  []string             // FROM registrations 的結果
	groupMembers []string             // FROM account_group_members 的結果
	usersByEmail map[string]string    // resolveIdentifier(email) 的結果：email -> user_id
	allUserIDs   []string             // users WHERE NOT is_virtual
	subs         []storedSubscription // push_subscriptions 全部內容

	// 記錄
	calls   []dbCall
	deleted []string // DELETE FROM push_subscriptions 刪掉的訂閱 id（＝被試圖送出且端點無效者）
}

func (d *fakeDB) record(sql string, args []any) string {
	q := strings.Join(strings.Fields(sql), " ")
	d.calls = append(d.calls, dbCall{sql: q, args: args})
	return q
}

// subQueries 所有碰 push_subscriptions 的 SELECT（不含 DELETE）。
func (d *fakeDB) subQueries() []dbCall {
	var out []dbCall
	for _, c := range d.calls {
		if strings.HasPrefix(c.sql, "SELECT") && strings.Contains(c.sql, "FROM push_subscriptions") {
			out = append(out, c)
		}
	}
	return out
}

func (d *fakeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q := d.record(sql, args)
	switch {
	case strings.Contains(q, "FROM registrations"):
		return idRows(d.raceMembers), nil
	case strings.Contains(q, "FROM account_group_members"):
		return idRows(d.groupMembers), nil
	case strings.Contains(q, "FROM push_subscriptions"):
		// 有 ANY($1) 才依第一個參數（user_id 清單）篩；沒有 WHERE＝全部（舊 bug 的出口）
		filtered := strings.Contains(q, "ANY($1)")
		var ids []string
		if filtered {
			ids, _ = args[0].([]string)
		}
		var rows [][]string
		for _, s := range d.subs {
			if !filtered || slices.Contains(ids, s.UserID) {
				rows = append(rows, []string{s.ID, s.UserID, s.Endpoint, s.P256dh, s.Auth})
			}
		}
		return &fakeRows{rows: rows}, nil
	case q == "SELECT id::text FROM users WHERE NOT is_virtual":
		return idRows(d.allUserIDs), nil
	}
	d.t.Fatalf("fakeDB: unexpected Query: %s", q)
	return nil, nil
}

func (d *fakeDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q := d.record(sql, args)
	switch {
	case q == "SELECT COUNT(*) FROM users WHERE NOT is_virtual":
		return fakeRow{val: len(d.allUserIDs)}
	case strings.Contains(q, "FROM users WHERE lower(email) = lower($1)"):
		if id, ok := d.usersByEmail[args[0].(string)]; ok {
			return fakeRow{val: id}
		}
		return fakeRow{err: pgx.ErrNoRows}
	}
	d.t.Fatalf("fakeDB: unexpected QueryRow: %s", q)
	return nil
}

func (d *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	q := d.record(sql, args)
	if strings.HasPrefix(q, "DELETE FROM push_subscriptions WHERE id = $1") {
		d.deleted = append(d.deleted, args[0].(string))
		return pgconn.NewCommandTag("DELETE 1"), nil
	}
	d.t.Fatalf("fakeDB: unexpected Exec: %s", q)
	return pgconn.CommandTag{}, nil
}

// fakeRows 逐列回字串欄位；Scan 只支援 *string 目標（本套件的查詢都是這樣）。
type fakeRows struct {
	rows [][]string
	pos  int // Next 之後指向 rows[pos-1]
}

func idRows(ids []string) *fakeRows {
	rows := make([][]string, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []string{id})
	}
	return &fakeRows{rows: rows}
}

func (r *fakeRows) Next() bool { r.pos++; return r.pos <= len(r.rows) }
func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.pos-1]
	if len(dest) != len(row) {
		return fmt.Errorf("fakeRows: %d scan targets for %d columns", len(dest), len(row))
	}
	for i, d := range dest {
		p, ok := d.(*string)
		if !ok {
			return fmt.Errorf("fakeRows: unsupported scan target %T", d)
		}
		*p = row[i]
	}
	return nil
}
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error) {
	return nil, errors.New("fakeRows: Values not implemented")
}
func (r *fakeRows) RawValues() [][]byte { return nil }
func (r *fakeRows) Conn() *pgx.Conn     { return nil }

// fakeRow 單一欄位的 QueryRow 結果（*string 或 *int），或指定的錯誤（例如 pgx.ErrNoRows）。
type fakeRow struct {
	val any
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch p := dest[0].(type) {
	case *string:
		*p = r.val.(string)
	case *int:
		*p = r.val.(int)
	default:
		return fmt.Errorf("fakeRow: unsupported scan target %T", dest[0])
	}
	return nil
}

// fakeMail 假的 MailInserter：只記錄呼叫次數與收到的 user_id（不寫 DB）。
type fakeMail struct {
	calls   int
	lastIDs []string
}

func (m *fakeMail) InsertForUsers(_ context.Context, userIDs []string, _, _, _, _ string) (int, error) {
	m.calls++
	m.lastIDs = slices.Clone(userIDs)
	return len(userIDs), nil
}

// newTestHandler 組一個 VAPID 設齊的 Handler（send() 才會真的跑）；金鑰是假的沒關係——假訂閱的端點不在
// 白名單，send() 在白名單檢查就返回，碰不到 webpush。mailer 不設 SMTP＝email 頻道 no-op。
func newTestHandler(db *fakeDB, mail MailInserter) *Handler {
	return &Handler{
		db:     db,
		cfg:    Config{PublicKey: "pub", PrivateKey: "priv", Subject: "mailto:test@example.invalid"},
		mailer: mailer.NewMailer(mailer.Config{}),
		mail:   mail,
	}
}

// poisonSubs 產生 n 筆屬於 userID 的假訂閱（id 形如 "u1-sub2"）；endpoint 不在白名單。
func poisonSubs(userID string, n int) []storedSubscription {
	out := make([]storedSubscription, 0, n)
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("%s-sub%d", userID, i)
		out = append(out, storedSubscription{
			ID: id, UserID: userID, P256dh: "x", Auth: "x",
			Endpoint: "https://not-allowed.example.invalid/" + id,
		})
	}
	return out
}

func idsOf(subs []storedSubscription) []string {
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.ID)
	}
	return out
}

func sorted(s []string) []string {
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}

// postBroadcast 以 AdminRouter 的 POST /broadcast 打一次（in-process，不經網路），回狀態碼與 JSON body。
func postBroadcast(t *testing.T, h *Handler, payload map[string]any) (int, map[string]any) {
	t.Helper()
	if _, ok := payload["title"]; !ok {
		payload["title"] = "t"
	}
	if _, ok := payload["body"]; !ok {
		payload["body"] = "b"
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rec := httptest.NewRecorder()
	h.AdminRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/broadcast", bytes.NewReader(raw)))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON (status %d): %v", rec.Code, err)
	}
	return rec.Code, out
}

func num(t *testing.T, out map[string]any, key string) int {
	t.Helper()
	v, ok := out[key].(float64)
	if !ok {
		t.Fatalf("response has no numeric %q: %v", key, out)
	}
	return int(v)
}
