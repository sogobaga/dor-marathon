package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dor/api/internal/auth"
)

// handler_test.go：HTTP 層。一律 httptest.NewRecorder 直接打 chi router，**不**開真的 TCP 監聽
// （本機沙盒會改寫 loopback HTTP 回應，見 memory local-sandbox-loopback-http）。

// withUser 模擬 RequireAuth：把 uid 放進 ctx（與 middleware.RequireAuth 同一個 key）。
func withUser(uid string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), auth.CtxKeyUserID, uid)))
		})
	}
}

// newAPI 回一個把 uid 放進 ctx 的 router（掛在 /run-meet-live，與 main.go 同路徑）。
func (e *testEnv) newAPI(uid string) http.Handler {
	r := chi.NewRouter()
	if uid != "" {
		r.Use(withUser(uid))
	}
	r.Mount("/run-meet-live", NewHandlerWithStore(e.store).Router())
	return r
}

func do(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) posJSON(p player, fix string) string {
	if fix == "" {
		return fmt.Sprintf(`{"pv":1,"sid":%q}`, p.sid)
	}
	return fmt.Sprintf(`{"pv":1,"sid":%q,"p":%s}`, p.sid, fix)
}

const fixA = `{"la":25.03321,"ln":121.56543,"ac":8,"fa":2}`
const fixB = `{"la":25.03401,"ln":121.56612,"ac":6,"fa":1}`

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON: %q", rec.Body.String())
	}
	s, _ := out["error"].(string)
	return s
}

// 契約 §9-6：非法 uuid → 404（含 `a}:g:x{` 這種想混淆 key 空間的字串）；Redis 不得被碰。
func TestInvalidUUIDIs404AndNeverTouchesRedis(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	api := e.newAPI(a.uid)
	for _, id := range []string{"not-a-uuid", "a%7D:g:x%7B", "123", "..%2F" + testMeet, testMeet + "%7D", "%20"} {
		for _, op := range []string{"pos", "leave"} {
			rec := do(api, "/run-meet-live/"+id+"/"+op, e.posJSON(a, fixA))
			if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
				t.Errorf("%s/%s: %d %s, want 404 not_found", id, op, rec.Code, rec.Body.String())
			}
		}
	}
	if keys := e.mr.Keys(); len(keys) != 0 {
		t.Fatalf("invalid ids must not create Redis keys: %v", keys)
	}
}

// 子路由不經 requireEntry，RequireAuth 由上層提供；沒有使用者 ctx 一律 401（縱深防禦）。
func TestNoUserIs401(t *testing.T) {
	e := newEnv(t)
	api := e.newAPI("") // 沒有 withUser
	rec := do(api, "/run-meet-live/"+testMeet+"/pos", `{"pv":1,"sid":"abcdefghijklmnop"}`)
	if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "unauthorized" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = do(api, "/run-meet-live/"+testMeet+"/leave", `{"pv":1,"sid":"abcdefghijklmnop"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("leave: %d", rec.Code)
	}
}

func TestBodyOver512BytesIs413(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	api := e.newAPI(a.uid)

	pad := strings.Repeat("x", 600)
	rec := do(api, "/run-meet-live/"+testMeet+"/pos", fmt.Sprintf(`{"pv":1,"sid":%q,"junk":%q}`, a.sid, pad))
	if rec.Code != http.StatusRequestEntityTooLarge || errCode(t, rec) != "payload_too_large" {
		t.Fatalf("pos: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(api, "/run-meet-live/"+testMeet+"/leave", fmt.Sprintf(`{"pv":1,"sid":%q,"junk":%q}`, a.sid, pad))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("leave: %d", rec.Code)
	}

	// 邊界：剛好 512 B 通過、513 B 被擋
	base := fmt.Sprintf(`{"pv":1,"sid":%q,"j":"`, a.sid)
	tail := `"}`
	exact := base + strings.Repeat("y", 512-len(base)-len(tail)) + tail
	if len(exact) != 512 {
		t.Fatalf("test setup: len = %d", len(exact))
	}
	if rec := do(api, "/run-meet-live/"+testMeet+"/pos", exact); rec.Code != http.StatusOK {
		t.Fatalf("512 B body: %d %s", rec.Code, rec.Body.String())
	}
	e.advance(2 * time.Second)
	if rec := do(api, "/run-meet-live/"+testMeet+"/pos", exact[:len(exact)-2]+"y"+tail); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("513 B body: %d", rec.Code)
	}
}

// 契約 §3：pv 缺或 ≠ 1 → 426；未知欄位忽略；壞 JSON → 400。
func TestProtocolVersionAndJSONHandling(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	api := e.newAPI(a.uid)
	url := "/run-meet-live/" + testMeet + "/pos"
	sid := fmt.Sprintf("%q", a.sid)

	for name, body := range map[string]string{
		"pv missing": `{"sid":` + sid + `}`, "pv 0": `{"pv":0,"sid":` + sid + `}`,
		"pv 2": `{"pv":2,"sid":` + sid + `}`, "pv 99": `{"pv":99,"sid":` + sid + `}`,
		"empty object": `{}`, "pv negative": `{"pv":-1,"sid":` + sid + `}`,
	} {
		rec := do(api, url, body)
		if rec.Code != http.StatusUpgradeRequired || errCode(t, rec) != "upgrade_required" {
			t.Errorf("%s: %d %s, want 426 upgrade_required", name, rec.Code, rec.Body.String())
		}
	}
	for name, body := range map[string]string{
		"not json": `not json`, "empty body": ``, "array": `[]`, "trailing garbage": `{"pv":1} x`,
		"pv string": `{"pv":"1","sid":` + sid + `}`,
	} {
		rec := do(api, url, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, rec.Code, rec.Body.String())
		}
	}
	// 未知欄位忽略
	if rec := do(api, url, `{"pv":1,"sid":`+sid+`,"future":{"x":1},"p":null}`); rec.Code != http.StatusOK {
		t.Fatalf("unknown fields must be ignored: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBadSIDIs400(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	api := e.newAPI(a.uid)
	for _, sid := range []string{"", "short", strings.Repeat("a", 33), "has|pipe00000000000", "空白 sid 0000000000000"} {
		rec := do(api, "/run-meet-live/"+testMeet+"/pos", fmt.Sprintf(`{"pv":1,"sid":%q}`, sid))
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "bad_sid" {
			t.Errorf("sid %q: %d %s", sid, rec.Code, rec.Body.String())
		}
		rec = do(api, "/run-meet-live/"+testMeet+"/leave", fmt.Sprintf(`{"pv":1,"sid":%q}`, sid))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("leave sid %q: %d", sid, rec.Code)
		}
	}
}

// 契約 §3.2：la∈[-90,90]、ln∈[-180,180]、ac∈[0,10000]、fa∈[0,600]，不合 → 400。
func TestCoordinateValidation(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	api := e.newAPI(a.uid)
	url := "/run-meet-live/" + testMeet + "/pos"

	bad := []string{
		`{"la":90.00001,"ln":0,"ac":8,"fa":0}`, `{"la":-90.00001,"ln":0,"ac":8,"fa":0}`,
		`{"la":0,"ln":180.00001,"ac":8,"fa":0}`, `{"la":0,"ln":-180.00001,"ac":8,"fa":0}`,
		`{"la":0,"ln":0,"ac":-1,"fa":0}`, `{"la":0,"ln":0,"ac":10001,"fa":0}`,
		`{"la":0,"ln":0,"ac":8,"fa":-1}`, `{"la":0,"ln":0,"ac":8,"fa":601}`,
		`{"ln":0,"ac":8,"fa":0}`, `{"la":0,"ac":8,"fa":0}`, `{"la":0,"ln":0,"fa":0}`, `{"la":0,"ln":0,"ac":8}`,
		`{"la":null,"ln":0,"ac":8,"fa":0}`, `{"la":"25","ln":0,"ac":8,"fa":0}`, `{}`, `{"la":1e999,"ln":0,"ac":8,"fa":0}`,
	}
	for _, p := range bad {
		rec := do(api, url, e.posJSON(a, p))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("p=%s: %d %s, want 400", p, rec.Code, rec.Body.String())
		}
		if e.mr.Exists(mk("pos")) {
			t.Fatalf("p=%s: a rejected position must not be stored", p)
		}
	}
	// 邊界值合法
	good := []string{
		`{"la":90,"ln":180,"ac":0,"fa":0}`, `{"la":-90,"ln":-180,"ac":10000,"fa":600}`,
		`{"la":0,"ln":0,"ac":8.4,"fa":1.6}`,
	}
	for _, p := range good {
		e.advance(2 * time.Second)
		if rec := do(api, url, e.posJSON(a, p)); rec.Code != http.StatusOK {
			t.Errorf("p=%s: %d %s, want 200", p, rec.Code, rec.Body.String())
		}
	}
}

// 200 回應：形狀、欄位白名單、p 的元素為 [n, la, ln, age_s, acc]。
func TestPosResponseShapeAndPrivacy(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("小明"), newPlayer("阿華")
	e.start(a, 50, false)
	e.start(b, 50, false)
	apiA, apiB := e.newAPI(a.uid), e.newAPI(b.uid)
	url := "/run-meet-live/" + testMeet + "/pos"

	if rec := do(apiA, url, e.posJSON(a, fixA)); rec.Code != http.StatusOK {
		t.Fatalf("a: %d %s", rec.Code, rec.Body.String())
	}
	e.advance(2 * time.Second)
	rec := do(apiB, url, fmt.Sprintf(`{"pv":1,"sid":%q,"rv":1,"p":%s}`, b.sid, fixB))
	if rec.Code != http.StatusOK {
		t.Fatalf("b: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control %q, want no-store", cc)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"pv": true, "t": true, "iv": true, "live": true, "rv": true, "own": true, "reauth_in_s": true, "roster": true, "p": true}
	for k := range raw {
		if !allowed[k] {
			t.Errorf("unexpected top-level field %q (response must be a whitelist: no ids, no history)", k)
		}
	}
	var generic map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &generic); err != nil {
		t.Fatal(err)
	}
	num := func(k string) float64 { v, _ := generic[k].(float64); return v }
	if num("pv") != 1 || num("iv") != 5000 || num("live") != 2 || num("rv") != 2 ||
		generic["own"] != "ok" || int64(num("t")) != e.nowMs() {
		t.Fatalf("envelope: %s", rec.Body.String())
	}
	if r := num("reauth_in_s"); r < 590 || r > 600 {
		t.Fatalf("reauth_in_s = %v, want ~600 (grant 900 s − 300 s)", r)
	}
	// rv=1 ≠ 伺服器 rv=2 → 附完整名冊
	roster, _ := generic["roster"].([]any)
	if len(roster) != 2 {
		t.Fatalf("roster: %v", generic["roster"])
	}
	first := roster[0].([]any)
	if first[0].(float64) != 1 || first[1].(string) != "小明" {
		t.Fatalf("roster[0] = %v", first)
	}
	// p 的元素：[n, la, ln, age_s, acc]；a 的定位年齡 = 回報的 fa(2 s) + 之後過了 2 s = 4 s
	peers, _ := generic["p"].([]any)
	if len(peers) != 1 {
		t.Fatalf("p = %v", generic["p"])
	}
	tuple := peers[0].([]any)
	if len(tuple) != 5 {
		t.Fatalf("peer tuple must be exactly [n,la,ln,age_s,acc]: %v", tuple)
	}
	if tuple[0].(float64) != 1 || tuple[1].(float64) != 25.03321 || tuple[2].(float64) != 121.56543 ||
		tuple[3].(float64) != 4 || tuple[4].(float64) != 8 {
		t.Fatalf("peer tuple = %v, want [1,25.03321,121.56543,4,8]", tuple)
	}

	// 回應裡不得出現任何 user id（兩人的 uuid）、也沒有 account／email 之類的字串
	body := rec.Body.String()
	for _, secret := range []string{a.uid, b.uid, "account", "email", "user_id", "uid"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(secret)) {
			t.Fatalf("response leaks %q: %s", secret, body)
		}
	}

	// rv 與伺服器相同 → 不附名冊
	e.advance(2 * time.Second)
	rec = do(apiB, url, fmt.Sprintf(`{"pv":1,"sid":%q,"rv":2,"p":%s}`, b.sid, fixB))
	generic = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &generic)
	if _, has := generic["roster"]; has {
		t.Fatalf("matching rv must omit roster: %s", rec.Body.String())
	}
}

// 互惠規則的 JSON 形狀：need_own_fix 的 p 必須是空陣列（存在且為 []）；presence_only 則整個 p 欄位不存在。
func TestPJSONShapeNeedOwnFixVsPresenceOnly(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("A"), newPlayer("B")
	e.start(a, 50, false)
	e.start(b, 50, false)
	api := e.newAPI(b.uid)
	url := "/run-meet-live/" + testMeet + "/pos"

	rec := do(api, url, e.posJSON(b, "")) // 沒有自己的位置
	var generic map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &generic)
	if string(generic["own"]) != `"need_own_fix"` || string(generic["p"]) != `[]` {
		t.Fatalf("need_own_fix must carry p:[]: %s", rec.Body.String())
	}

	// 不限地點團
	e2 := newEnv(t)
	c := newPlayer("C")
	e2.start(c, 50, true)
	rec = do(e2.newAPI(c.uid), url, e2.posJSON(c, fixA))
	generic = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &generic)
	if string(generic["own"]) != `"presence_only"` {
		t.Fatalf("presence-only: %s", rec.Body.String())
	}
	if _, has := generic["p"]; has {
		t.Fatalf("presence-only must never include p: %s", rec.Body.String())
	}
	if e2.mr.Exists(mk("pos")) {
		t.Fatal("presence-only must not store the position")
	}
}

// 契約 §9-6：1.5 s 地板 → 429（Retry-After 秒）。
func TestFloorIs429WithRetryAfter(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	api := e.newAPI(a.uid)
	url := "/run-meet-live/" + testMeet + "/pos"
	if rec := do(api, url, e.posJSON(a, fixA)); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := do(api, url, e.posJSON(a, fixA))
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "too_fast" {
		t.Fatalf("second: %d %s", rec.Code, rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "2" && ra != "1" {
		t.Fatalf("Retry-After = %q, want 1 or 2 (ceil of the remaining floor)", ra)
	}
	e.advance(1500 * time.Millisecond)
	if rec := do(api, url, e.posJSON(a, fixA)); rec.Code != http.StatusOK {
		t.Fatalf("after the floor: %d %s", rec.Code, rec.Body.String())
	}
}

// 契約 §3.2 錯誤表。
func TestPosErrorTable(t *testing.T) {
	e := newEnv(t)
	a, stranger := newPlayer("A"), newPlayer("S")
	url := "/run-meet-live/" + testMeet + "/pos"

	// 無 grant → 409 grant_missing
	if rec := do(e.newAPI(stranger.uid), url, e.posJSON(stranger, fixA)); rec.Code != http.StatusConflict || errCode(t, rec) != "grant_missing" {
		t.Fatalf("no grant: %d %s", rec.Code, rec.Body.String())
	}
	e.start(a, 50, false)
	api := e.newAPI(a.uid)

	// 另一個 sid → 409 sid_mismatch
	other := a
	other.sid = "ANOTHERSID000000001"
	if rec := do(api, url, e.posJSON(other, "")); rec.Code != http.StatusConflict || errCode(t, rec) != "sid_mismatch" {
		t.Fatalf("sid mismatch: %d %s", rec.Code, rec.Body.String())
	}

	// kill → 410 killed
	e.mr.Set(KillKey, "1")
	if rec := do(api, url, e.posJSON(a, "")); rec.Code != http.StatusGone || errCode(t, rec) != "killed" {
		t.Fatalf("killed: %d %s", rec.Code, rec.Body.String())
	}
	e.mr.Del(KillKey)

	// dead → 410 meet_over
	_ = e.store.MarkDead(context.Background(), testMeet)
	if rec := do(api, url, e.posJSON(a, "")); rec.Code != http.StatusGone || errCode(t, rec) != "meet_over" {
		t.Fatalf("dead: %d %s", rec.Code, rec.Body.String())
	}
	_ = e.store.ClearDead(context.Background(), testMeet)
	e.advance(2 * time.Second)

	// revoked → 403 revoked
	_ = e.store.Revoke(context.Background(), testMeet, a.uid)
	if rec := do(api, url, e.posJSON(a, "")); rec.Code != http.StatusForbidden || errCode(t, rec) != "revoked" {
		t.Fatalf("revoked: %d %s", rec.Code, rec.Body.String())
	}
}

// Redis 不可用 → 503 redis_unavailable（fail-closed）；rdb 為 nil 也一樣。
func TestRedisUnavailableIs503(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	api := e.newAPI(a.uid)
	e.mr.Close()
	for _, op := range []string{"pos", "leave"} {
		rec := do(api, "/run-meet-live/"+testMeet+"/"+op, e.posJSON(a, ""))
		if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "redis_unavailable" {
			t.Errorf("%s with Redis down: %d %s", op, rec.Code, rec.Body.String())
		}
	}

	r := chi.NewRouter()
	r.Use(withUser(a.uid))
	r.Mount("/run-meet-live", NewHandler(nil).Router())
	rec := do(r, "/run-meet-live/"+testMeet+"/pos", e.posJSON(a, ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil rdb: %d", rec.Code)
	}
}

// 契約 §3.3：leave → 204；僅 sid 相符才刪；不寫墓碑。
func TestLeaveEndpoint(t *testing.T) {
	e := newEnv(t)
	a := newPlayer("A")
	e.start(a, 50, false)
	api := e.newAPI(a.uid)
	leaveURL := "/run-meet-live/" + testMeet + "/leave"
	posURL := "/run-meet-live/" + testMeet + "/pos"

	rec := do(api, leaveURL, `{"pv":1,"sid":"WRONGSID0000000001"}`)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("leave with a wrong sid: %d %q (still 204, nothing leaked)", rec.Code, rec.Body.String())
	}
	if rec := do(api, posURL, e.posJSON(a, fixA)); rec.Code != http.StatusOK {
		t.Fatalf("grant must survive a wrong-sid leave: %d %s", rec.Code, rec.Body.String())
	}
	e.advance(2 * time.Second)

	if rec := do(api, leaveURL, e.posJSON(a, "")); rec.Code != http.StatusNoContent {
		t.Fatalf("leave: %d", rec.Code)
	}
	if e.mr.Exists(mk("rv:" + a.uid)) {
		t.Fatal("leave must not write a tombstone")
	}
	rec = do(api, posURL, e.posJSON(a, fixA))
	if rec.Code != http.StatusConflict || errCode(t, rec) != "grant_missing" {
		t.Fatalf("pos after leave: %d %s, want 409 grant_missing (not 403 revoked)", rec.Code, rec.Body.String())
	}
	// pv 錯 → 426
	if rec := do(api, leaveURL, `{"pv":3,"sid":"abcdefghijklmnop"}`); rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("leave pv: %d", rec.Code)
	}
}

// 兩個使用者只透過各自的 ctx 使用者區分；同一個 router 上互不干擾。
func TestTwoUsersThroughHTTPSeeEachOther(t *testing.T) {
	e := newEnv(t)
	a, b := newPlayer("小明"), newPlayer("阿華")
	e.start(a, 50, false)
	e.start(b, 50, false)
	url := "/run-meet-live/" + testMeet + "/pos"
	do(e.newAPI(a.uid), url, e.posJSON(a, fixA))
	e.advance(2 * time.Second)
	rec := do(e.newAPI(b.uid), url, e.posJSON(b, fixB))
	var out struct {
		P [][]float64 `json:"p"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.P) != 1 || out.P[0][0] != 1 {
		t.Fatalf("b's snapshot: %s", rec.Body.String())
	}
	e.advance(2 * time.Second)
	rec = do(e.newAPI(a.uid), url, e.posJSON(a, fixA))
	out.P = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.P) != 1 || out.P[0][0] != 2 {
		t.Fatalf("a's snapshot: %s", rec.Body.String())
	}
}
