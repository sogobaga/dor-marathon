package integration

// 假 store 的連線列／匯入部分，以及假 Garmin（以 host 分流的 in-process RoundTripper；不開本機 TCP）。
// 全部是合成資料；token／userId 都是假值。

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type fakeTail struct {
	NA  *NormalizedActivity
	Res ImportResult
	Opt TailOptions
}

type fakePurge struct{ UserID, GarminUID string }

type fakeTokenUpdate struct {
	ID, Access, Refresh string
	Exp, RefreshExp     time.Time
}

type fakeConnState struct {
	conns        map[string]*garminConn // DOR user id → 連線列
	blockedUsers map[string]bool
	blockedGIDs  map[string]bool
	blockRows    map[string]string // DOR user id → 封鎖當時記下的 Garmin userId

	imports  []*NormalizedActivity
	importFn func(a *NormalizedActivity) (ImportResult, error)
	tails    []fakeTail
	twins    map[string]bool
	touched  []string

	purges           []fakePurge
	purgeActivities  int64
	purgeErr         error
	deviceName       string
	saveErr          error
	saves            []garminSaveInput
	updates          []fakeTokenUpdate
	updateFailTimes  int
	scopeSets        []string
	reauthMarks      []string
	callLog          []string
	seq              int
	deletedEventsFor []string
}

func newFakeConnState() fakeConnState {
	return fakeConnState{
		conns:        map[string]*garminConn{},
		blockedUsers: map[string]bool{},
		blockedGIDs:  map[string]bool{},
		blockRows:    map[string]string{},
		twins:        map[string]bool{},
	}
}

func (s *fakeGarminStore) logCall(c string) { s.cs.callLog = append(s.cs.callLog, c) }

func (s *fakeGarminStore) putConn(c garminConn) *garminConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := c
	if cp.ID == "" {
		s.cs.seq++
		cp.ID = fmt.Sprintf("conn-%04d", s.cs.seq)
	}
	s.cs.conns[cp.UserID] = &cp
	out := cp // 回傳副本：測試手上的物件不能與 store 內的列共用記憶體
	return &out
}

func (s *fakeGarminStore) conn(userID string) *garminConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.cs.conns[userID]; c != nil {
		cp := *c
		return &cp
	}
	return nil
}

func copyConn(c *garminConn, withTokens bool) *garminConn {
	if c == nil {
		return nil
	}
	cp := *c
	if !withTokens {
		cp.AccessToken, cp.RefreshToken = "", ""
	}
	return &cp
}

func (s *fakeGarminStore) GetGarminByUser(ctx context.Context, userID string, withTokens bool) (*garminConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyConn(s.cs.conns[userID], withTokens), nil
}

func (s *fakeGarminStore) GetGarminDirectByUserID(ctx context.Context, gid string, withTokens bool) (*garminConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cs.conns {
		if c.Via == garminViaDirect && c.ProviderUserID == gid {
			return copyConn(c, withTokens), nil
		}
	}
	return nil, nil
}

func (s *fakeGarminStore) GarminBlocked(ctx context.Context, userID, gid string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return (userID != "" && s.cs.blockedUsers[userID]) || (gid != "" && s.cs.blockedGIDs[gid]), nil
}

func (s *fakeGarminStore) SaveGarmin(ctx context.Context, in garminSaveInput) (garminSaveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res garminSaveResult
	if s.cs.saveErr != nil {
		return res, s.cs.saveErr
	}
	if !TokenKeyConfigured() {
		return res, ErrTokenKeyMissing
	}
	cur := s.cs.conns[in.UserID]
	if cur != nil && cur.Via == garminViaDirect && cur.ProviderUserID != "" && cur.ProviderUserID != in.GarminUserID {
		return res, ErrGarminAccountChanged
	}
	for uid, c := range s.cs.conns {
		if uid != in.UserID && c.ProviderUserID == in.GarminUserID && c.ProviderUserID != "" {
			return res, ErrGarminAlreadyLinked
		}
	}
	s.cs.saves = append(s.cs.saves, in)
	now := time.Now()
	if cur == nil {
		s.cs.seq++
		cur = &garminConn{ID: fmt.Sprintf("conn-%04d", s.cs.seq), UserID: in.UserID, ConnectedAt: now}
		s.cs.conns[in.UserID] = cur
		res.Inserted = true
	} else {
		res.PrevVia = cur.Via
	}
	cur.ProviderUserID, cur.AccessToken, cur.RefreshToken = in.GarminUserID, in.AccessToken, in.RefreshToken
	cur.ExpiresAt, cur.Scope, cur.Via, cur.Issuer = in.ExpiresAt, in.Scope, garminViaDirect, in.Issuer
	rexp := in.RefreshExpiresAt
	cur.RefreshExpiresAt, cur.ReauthRequiredAt = &rexp, nil
	cat := in.ConsentAt
	cur.ConsentAt, cur.ConsentVersion = &cat, in.ConsentVersion
	cur.AuthorizedAt = &now
	res.ID, res.ConnectedAt = cur.ID, cur.ConnectedAt
	return res, nil
}

func (s *fakeGarminStore) UpdateGarminTokens(ctx context.Context, id, access, refresh string, exp, rexp time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cs.updateFailTimes > 0 {
		s.cs.updateFailTimes--
		return errors.New("simulated persist failure")
	}
	for _, c := range s.cs.conns {
		if c.ID == id {
			c.AccessToken, c.RefreshToken, c.ExpiresAt = access, refresh, exp
			r := rexp
			c.RefreshExpiresAt, c.ReauthRequiredAt = &r, nil
			s.cs.updates = append(s.cs.updates, fakeTokenUpdate{ID: id, Access: access, Refresh: refresh, Exp: exp, RefreshExp: rexp})
			s.logCall("update_tokens")
			return nil
		}
	}
	return errors.New("connection not found")
}

func (s *fakeGarminStore) MarkGarminReauth(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cs.conns {
		if c.ID == id {
			if c.ReauthRequiredAt == nil {
				now := time.Now()
				c.ReauthRequiredAt = &now
			}
			s.cs.reauthMarks = append(s.cs.reauthMarks, id)
		}
	}
	return nil
}

func (s *fakeGarminStore) SetGarminScope(ctx context.Context, id, scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cs.conns {
		if c.ID == id {
			c.Scope = scope
			s.cs.scopeSets = append(s.cs.scopeSets, scope)
		}
	}
	return nil
}

func (s *fakeGarminStore) TouchGarminSynced(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cs.conns {
		if c.ID == id {
			now := time.Now()
			c.LastSyncedAt = &now
		}
	}
	s.cs.touched = append(s.cs.touched, id)
	return nil
}

func (s *fakeGarminStore) LatestGarminDeviceName(ctx context.Context, userID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cs.deviceName, nil
}

func (s *fakeGarminStore) PurgeGarminUser(ctx context.Context, userID, gid string) (GarminPurgeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cs.purgeErr != nil {
		return GarminPurgeResult{}, s.cs.purgeErr
	}
	s.cs.purges = append(s.cs.purges, fakePurge{userID, gid})
	s.logCall("purge")
	res := GarminPurgeResult{DeletedActivities: s.cs.purgeActivities}
	if _, ok := s.cs.conns[userID]; ok {
		res.HadConnection = true
		delete(s.cs.conns, userID)
	}
	for id, r := range s.events {
		if gid != "" && r.ProviderUserID == gid {
			delete(s.events, id)
			res.DeletedEvents++
		}
	}
	kept := s.order[:0]
	for _, id := range s.order {
		if _, ok := s.events[id]; ok {
			kept = append(kept, id)
		}
	}
	s.order = kept
	return res, nil
}

func (s *fakeGarminStore) GarminConnStats(ctx context.Context, generation string) (garminConnStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st garminConnStats
	now := time.Now()
	for _, c := range s.cs.conns {
		if c.Via != garminViaDirect {
			continue
		}
		st.Connected++
		if c.LastSyncedAt != nil && now.Sub(*c.LastSyncedAt) < 24*time.Hour {
			st.Active24h++
		}
		if c.ReauthRequiredAt != nil || c.Issuer != generation {
			st.NeedsReauth++
		}
		if c.paused() {
			st.Paused++
			if c.LastSyncedAt != nil && now.Sub(*c.LastSyncedAt) < 24*time.Hour {
				st.PausedButActive++
			}
		}
	}
	return st, nil
}

func (s *fakeGarminStore) GarminEventStats(ctx context.Context) (garminEventStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st garminEventStats
	for _, r := range s.events {
		st.Received24h++
		switch {
		case r.Status == garminStDead:
			st.Dead++
		case r.Status == garminStDone && r.Result == garminResInserted:
			st.Imported24h++
		case r.Status == garminStDone && strings.HasPrefix(r.Result, "skipped_"):
			st.Skipped24h++
		}
	}
	return st, nil
}

func (s *fakeGarminStore) GarminKeepaliveCandidates(ctx context.Context, generation string, within time.Duration, limit int) ([]*garminConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*garminConn
	for _, c := range s.cs.conns {
		if c.Via == garminViaDirect && c.ReauthRequiredAt == nil && c.Issuer == generation && c.RefreshExpiresAt != nil &&
			c.RefreshExpiresAt.After(time.Now()) && time.Until(*c.RefreshExpiresAt) < within {
			out = append(out, copyConn(c, false))
		}
	}
	return out, nil
}

func (s *fakeGarminStore) LegacyTwinExists(ctx context.Context, userID, bare string, start int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cs.twins[fmt.Sprintf("%s|%s|%d", userID, bare, start)] || s.cs.twins[fmt.Sprintf("%s|garmin:%d", userID, start)] ||
		(bare != "" && s.cs.twins[userID+"|bare|"+bare]), nil
}

func (s *fakeGarminStore) ImportActivity(ctx context.Context, a *NormalizedActivity) (ImportResult, error) {
	s.mu.Lock()
	cp := *a
	s.cs.imports = append(s.cs.imports, &cp)
	fn := s.cs.importFn
	n := len(s.cs.imports)
	s.mu.Unlock()
	if fn != nil {
		return fn(&cp)
	}
	return ImportResult{Status: "inserted", ID: fmt.Sprintf("act-%03d", n)}, nil
}

func (s *fakeGarminStore) AfterImport(ctx context.Context, na *NormalizedActivity, res ImportResult, opt TailOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cs.tails = append(s.cs.tails, fakeTail{NA: na, Res: res, Opt: opt})
}

func (s *fakeGarminStore) importCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cs.imports)
}

// --- 假 Garmin ---

type fakeGarmin struct {
	mu sync.Mutex

	userID string
	perms  []string

	accessSeq       int
	refreshSeq      int
	validAccess     map[string]bool
	validRefresh    map[string]bool // 目前有效的 refresh token（輪替後舊的失效）
	revoked         bool            // 使用者已在 Garmin 端撤銷：user 端點 401、refresh invalid_grant
	challengeByCode map[string]string
	codeSeq         int

	expiresIn        int64
	refreshExpiresIn int64
	noRefreshInResp  bool

	// 行為覆寫
	tokenStatus  int // 非 0＝token 端點一律回這個狀態
	tokenErrCode string
	userIDStatus int
	permsStatus  int
	deleteStatus int
	userIDBody   string
	permsBody    string
	netErr       error // 非 nil＝所有請求網路層失敗
	onToken      func()

	// 紀錄
	tokenCalls   int
	userIDCalls  int
	permCalls    int
	deleteCalls  int
	tokenAuth    []string
	tokenForms   []url.Values
	deleteTokens []string
	hostsSeen    map[string]bool
}

func newFakeGarmin() *fakeGarmin {
	return &fakeGarmin{
		userID: "g-" + strings.Repeat("a", 30), perms: []string{"ACTIVITY_EXPORT", "HISTORICAL_DATA_EXPORT"},
		validAccess: map[string]bool{}, validRefresh: map[string]bool{}, challengeByCode: map[string]string{}, expiresIn: 86400, refreshExpiresIn: 7775998,
		hostsSeen: map[string]bool{},
	}
}

func (f *fakeGarmin) issueTokens() (string, string) {
	f.accessSeq++
	f.refreshSeq++
	a := fmt.Sprintf("acc-%d", f.accessSeq)
	r := fmt.Sprintf("ref-%d", f.refreshSeq)
	f.validAccess[a] = true
	f.validRefresh[r] = true
	return a, r
}

func (f *fakeGarmin) newCode(challenge string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codeSeq++
	c := fmt.Sprintf("code-%d", f.codeSeq)
	f.challengeByCode[c] = challenge
	return c
}

func (f *fakeGarmin) calls() (token, userID, perms, del int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls, f.userIDCalls, f.permCalls, f.deleteCalls
}

func (f *fakeGarmin) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.hostsSeen[r.URL.Host] = true
	if f.netErr != nil {
		err := f.netErr
		f.mu.Unlock()
		return nil, err
	}
	f.mu.Unlock()
	switch {
	case r.URL.Host == "diauth.garmin.com" && r.URL.Path == "/di-oauth2-service/oauth/token" && r.Method == http.MethodPost:
		return f.handleToken(r)
	case r.URL.Host == "apis.garmin.com":
		return f.handleAPI(r)
	}
	return jsonResp(404, `{"error":"unexpected host in test"}`), nil
}

func (f *fakeGarmin) handleToken(r *http.Request) (*http.Response, error) {
	body := ""
	if r.Body != nil {
		b := new(strings.Builder)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		body = b.String()
	}
	form, _ := url.ParseQuery(body)
	f.mu.Lock()
	f.tokenCalls++
	f.tokenAuth = append(f.tokenAuth, r.Header.Get("Authorization"))
	f.tokenForms = append(f.tokenForms, form)
	onToken, override, code := f.onToken, f.tokenStatus, f.tokenErrCode
	f.mu.Unlock()
	if onToken != nil {
		onToken()
	}
	if override != 0 {
		return jsonResp(override, fmt.Sprintf(`{"error":%q}`, code)), nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch form.Get("grant_type") {
	case "authorization_code":
		ch, ok := f.challengeByCode[form.Get("code")]
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != ch {
			return jsonResp(400, `{"error":"invalid_grant"}`), nil
		}
		delete(f.challengeByCode, form.Get("code"))
	case "refresh_token":
		if f.revoked || !f.validRefresh[form.Get("refresh_token")] {
			return jsonResp(400, `{"error":"invalid_grant"}`), nil
		}
		delete(f.validRefresh, form.Get("refresh_token")) // 輪替：舊的立即失效
	default:
		return jsonResp(400, `{"error":"unsupported_grant_type"}`), nil
	}
	a, rt := f.issueTokens()
	resp := fmt.Sprintf(`{"access_token":%q,"token_type":"bearer","expires_in":%d,"refresh_token_expires_in":%d,"scope":"PARTNER_WRITE PARTNER_READ","jti":"x"`, a, f.expiresIn, f.refreshExpiresIn)
	if !f.noRefreshInResp {
		resp += fmt.Sprintf(`,"refresh_token":%q`, rt)
	}
	return jsonResp(200, resp+"}"), nil
}

func (f *fakeGarmin) handleAPI(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	authorized := !f.revoked && f.validAccess[tok]
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/partner-gateway/rest/user/id":
		f.userIDCalls++
		if f.userIDStatus != 0 {
			return jsonResp(f.userIDStatus, `{"errorMessage":"x"}`), nil
		}
		if !authorized {
			return jsonResp(401, `{"errorMessage":"bad token"}`), nil
		}
		if f.userIDBody != "" {
			return jsonResp(200, f.userIDBody), nil
		}
		return jsonResp(200, fmt.Sprintf(`{"userId":%q}`, f.userID)), nil
	case r.Method == http.MethodGet && r.URL.Path == "/partner-gateway/rest/user/permissions":
		f.permCalls++
		if f.permsStatus != 0 {
			return jsonResp(f.permsStatus, `{"errorMessage":"x"}`), nil
		}
		if !authorized {
			return jsonResp(401, `{"errorMessage":"bad token"}`), nil
		}
		if f.permsBody != "" {
			return jsonResp(200, f.permsBody), nil
		}
		quoted := make([]string, len(f.perms))
		for i, p := range f.perms {
			quoted[i] = fmt.Sprintf("%q", p)
		}
		return jsonResp(200, "["+strings.Join(quoted, ",")+"]"), nil
	case r.Method == http.MethodDelete && r.URL.Path == "/partner-gateway/rest/user/registration":
		f.deleteCalls++
		f.deleteTokens = append(f.deleteTokens, tok)
		if f.deleteStatus != 0 {
			return &http.Response{StatusCode: f.deleteStatus, Header: http.Header{}, Body: http.NoBody}, nil
		}
		if !authorized {
			return &http.Response{StatusCode: 401, Header: http.Header{}, Body: http.NoBody}, nil
		}
		f.revoked = true // 註冊刪除後，之後所有 user 端點與 refresh 都失效
		return &http.Response{StatusCode: 204, Header: http.Header{}, Body: http.NoBody}, nil
	}
	return jsonResp(404, `{"error":"unexpected path in test"}`), nil
}

func (s *fakeGarminStore) AddGarminBlock(ctx context.Context, userID, gid, reason, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID == "" && gid == "" {
		return errors.New("target required")
	}
	if userID != "" {
		s.cs.blockedUsers[userID] = true
		s.cs.blockRows[userID] = gid
	}
	if gid != "" {
		s.cs.blockedGIDs[gid] = true
	}
	s.logCall("block")
	return nil
}

func (s *fakeGarminStore) RemoveGarminBlock(ctx context.Context, userID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cs.blockedUsers[userID] {
		return 0, nil
	}
	delete(s.cs.blockedUsers, userID)
	if gid := s.cs.blockRows[userID]; gid != "" {
		delete(s.cs.blockedGIDs, gid)
	}
	delete(s.cs.blockRows, userID)
	return 1, nil
}
