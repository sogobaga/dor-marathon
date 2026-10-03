package integration

// Garmin 推送接收端：路徑 token 驗證 → 讀取並串流解析（白名單欄位）→ 濾掉未知使用者 → 批次落地事件 →
// 回精確 200 → 背景就地處理。
//
// 回應規則（Garmin 把非 200 一律視為失敗並退避重送；回 200 後不再重送）：
//   - 成功：精確 200、空 body（不可用 201／202／204）。
//   - 路徑 token 不符：404（不洩漏端點存在）；token 未設定：503。
//   - 入口 hidden（緊急關閉）：活動端點 503（讓 Garmin 排隊）；撤銷／權限端點照常處理。
//   - body 超過上限：413。
//   - body 讀取中斷、Content-Length 與實讀不符、JSON 提前結束（疑似被代理截斷）：400——必須讓 Garmin 重送。
//   - 完整讀完但內容不是合法 JSON：200＋log（毒訊息不要被無限重送）。
//   - 落地（INSERT）或使用者查詢失敗：503（讓 Garmin 排隊重送；事件尚未落地所以不會遺失）。
//   - 不做 IP 限流（被 429 會觸發 Garmin 的退避重送）。
// 日誌紀律：不印 payload、token、路徑、完整 userId。

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/dbwake"
	"github.com/dor/api/internal/reqip"
)

// WebhookActivities POST /webhook/{token}/activities
func (h *GarminHandler) WebhookActivities(w http.ResponseWriter, r *http.Request) {
	h.webhook(w, r, garminPushActivities)
}

// WebhookDeregistrations POST /webhook/{token}/deregistrations
func (h *GarminHandler) WebhookDeregistrations(w http.ResponseWriter, r *http.Request) {
	h.webhook(w, r, garminPushDeregs)
}

// WebhookPermissions POST /webhook/{token}/permissions
func (h *GarminHandler) WebhookPermissions(w http.ResponseWriter, r *http.Request) {
	h.webhook(w, r, garminPushPerms)
}

const garminReadWindow = 25 * time.Second

// tokenOK 常數時間比對路徑 token（先雜湊成固定長度再比，兩把 token 都比、不短路）。
func (h *GarminHandler) tokenOK(got string) bool {
	d := sha256.Sum256([]byte(got))
	cur := subtle.ConstantTimeCompare(d[:], h.tokDigest[:])
	prev := 0
	if h.hasPrev {
		prev = subtle.ConstantTimeCompare(d[:], h.tokPrevDigest[:])
	}
	return (cur | prev) == 1
}

// noteBadToken 記錄一次錯誤 token（Redis 10 分鐘桶；≥20 次告警一次）。不記錄 token 內容與路徑。
func (h *GarminHandler) noteBadToken(ctx context.Context) {
	n := h.count(ctx, "garmin:cnt:badtoken:"+h.tenMinBucket(), 1, time.Hour)
	if n == 20 {
		h.alert("garmin_bad_token", "Garmin webhook 收到大量錯誤 token", "10 分鐘內 ≥20 次路徑 token 不符（可能是掃描或舊 token 仍在使用）")
	}
}

func (h *GarminHandler) acquireParse(ctx context.Context) bool {
	t := time.NewTimer(garminParseWait)
	defer t.Stop()
	select {
	case h.parseSem <- struct{}{}:
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (h *GarminHandler) releaseParse() { <-h.parseSem }

func (h *GarminHandler) webhook(w http.ResponseWriter, r *http.Request, kind string) {
	if !h.webhookConfigured() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if !h.tokenOK(chi.URLParam(r, "token")) {
		h.noteBadToken(r.Context())
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ctx := r.Context()
	if kind == garminPushActivities && h.emergency != nil && h.emergency(ctx) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// 觀察用（只記有／無／相符與來源 IP 樣本，不記值）：在 handler 返回前先取出，之後不再碰 request。
	obs := garminObservation{
		clientID: strings.TrimSpace(r.Header.Get("garmin-client-id")),
		ip:       reqip.ClientIP(r),
	}

	if !h.acquireParse(ctx) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	res, size, perr := h.readPush(w, r, kind)
	h.releaseParse()
	if perr != nil {
		var mbe *http.MaxBytesError
		switch {
		case errors.As(perr, &mbe) || errors.Is(perr, errGarminTooLarge):
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		case errors.Is(perr, errGarminBadJSON):
			// 完整讀完但不是合法 JSON：回 200（避免毒訊息無限重送），記 log 與計數。
			log.Warn().Str("kind", kind).Int64("bytes", size).Msg("garmin webhook: body is not valid json, acked and dropped")
			h.count(ctx, "garmin:cnt:badjson:"+h.dayBucket(), 1, 2*24*time.Hour)
			w.WriteHeader(http.StatusOK)
		default:
			// 讀取中斷／提前結束：必須回非 200，讓 Garmin 重送（回 200 會讓被截斷的資料永久遺失）。
			log.Warn().Str("kind", kind).Int64("bytes", size).Str("reason", "truncated_or_io").Msg("garmin webhook: body incomplete, rejecting so garmin retries")
			h.count(ctx, "garmin:cnt:truncated:"+h.dayBucket(), 1, 2*24*time.Hour)
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}

	var st garminBuildStats
	var cands []garminEventIn
	switch kind {
	case garminPushActivities:
		cands = buildGarminActivityEvents(res.Activities, &st)
	case garminPushDeregs:
		cands = buildGarminDeregEvents(res.Deregs, &st)
	case garminPushPerms:
		cands = buildGarminPermissionEvents(res.Perms, &st)
	}
	st.Malformed = res.Malformed
	st.Total = len(res.Activities) + len(res.Deregs) + len(res.Perms)

	// 一次查詢濾掉「對不到直連連線（或已封鎖）」的 userId：只計數、不落地。
	known, err := h.store.KnownGarminUsers(ctx, garminUniqueUserIDs(cands))
	if err != nil {
		log.Error().Err(err).Str("kind", kind).Msg("garmin webhook: known-user lookup failed")
		h.alert("garmin_webhook_err", "Garmin Webhook 落地失敗", "查詢使用者連線失敗（回 503，Garmin 會重送）；若持續發生請確認 migration 199 已套用")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	keep := cands[:0:0]
	unknown := 0
	for _, c := range cands {
		if _, ok := known[c.ProviderUserID]; ok {
			keep = append(keep, c)
		} else {
			unknown++
		}
	}

	var inserted []garminEvent
	if len(keep) > 0 {
		inserted, err = h.store.InsertGarminEvents(ctx, keep)
		if err != nil {
			log.Error().Err(err).Str("kind", kind).Msg("garmin webhook: persist events failed")
			h.alert("garmin_webhook_err", "Garmin Webhook 落地失敗", "寫入 integration_events 失敗（回 503，Garmin 會重送）；若持續發生請確認 migration 199 已套用")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}

	// 先落地、後回應：到這裡事件已 commit，才回精確 200（空 body），並立刻送出（不等背景處理）。
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	log.Info().Str("kind", kind).Int("total", st.Total).Int("persisted", len(inserted)).Int("duplicate", len(keep)-len(inserted)).
		Int("unknown_user", unknown).Int("skipped_type", st.SkippedType).Int("bad_payload", st.BadPayload).
		Int("malformed", st.Malformed).Int("callback_ignored", st.CallbackIgnored).Int64("bytes", size).
		Msg("garmin webhook: accepted")

	h.afterAck(dbwake.Detach(ctx), kind, st, unknown, obs, inserted)
}

// garminObservation：Evaluation 階段的觀察資料（只記有／無／相符與來源 IP 樣本）。
type garminObservation struct {
	clientID string
	ip       string
}

// afterAck 回應送出後的工作：計數、觀察、順手掃描、背景處理新落地的事件。全部使用脫離請求的 context。
func (h *GarminHandler) afterAck(ctx context.Context, kind string, st garminBuildStats, unknown int, obs garminObservation, inserted []garminEvent) {
	if h.draining.Load() || !h.workBegin() {
		// 關閉中：不再啟動新的處理；事件已落地，立刻釋放租約，讓新行程的啟動掃描能接手。
		h.releaseLeases(inserted)
		return
	}
	ids := garminEventIDs(inserted)
	h.track(ids)
	go func() {
		defer h.workEnd()
		defer h.untrack(ids)
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().Interface("panic", rec).Msg("garmin afterAck panic")
			}
		}()
		pctx, cancel := context.WithTimeout(ctx, garminProcessBudget)
		defer cancel()

		// 先處理事件（資料不等 Redis 計數）；計數／觀察／順手掃描都是盡力而為，Redis 卡住也不會拖慢處理。
		if len(inserted) > 0 {
			h.processEvents(pctx, inserted)
		}
		bctx, bcancel := context.WithTimeout(ctx, 10*time.Second)
		defer bcancel()
		hour := h.hourBucket()
		if n := len(inserted); n > 0 {
			h.count(bctx, "garmin:cnt:recv:"+hour, int64(n), 2*24*time.Hour)
		}
		if st.SkippedType > 0 {
			h.count(bctx, "garmin:cnt:skiptype:"+hour, int64(st.SkippedType), 2*24*time.Hour)
		}
		if st.CallbackIgnored > 0 {
			h.count(bctx, "garmin:cnt:callback:"+h.dayBucket(), int64(st.CallbackIgnored), 2*24*time.Hour)
		}
		if unknown > 0 {
			if v := h.count(bctx, "garmin:cnt:unknown:"+hour, int64(unknown), 2*24*time.Hour); v > 50 && v-int64(unknown) <= 50 {
				h.alert("garmin_unknown_user_burst", "Garmin 未知使用者推送暴增", "1 小時內未對應到連線的推送 >50 筆（可能有人已在 DOR 中斷但 Garmin 仍在推送，或有人試探）")
			}
		}
		h.observe(bctx, obs)
		// 下一筆推送順手掃一次到期事件（5 分鐘節流；沒有 ticker）。先處理自己的事件，再掃別人遺留的。
		h.maybeSweep(pctx)
	}()
}

// observe 記錄 garmin-client-id header 有／無／相符計數，以及來源 IP 樣本（最多 20 個、7 天）。不記錄任何值（IP 除外，且只存 Redis 集合）。
func (h *GarminHandler) observe(ctx context.Context, o garminObservation) {
	state := "none"
	if o.clientID != "" {
		state = "present"
		if h.cfg.ClientID != "" {
			if subtle.ConstantTimeCompare([]byte(o.clientID), []byte(h.cfg.ClientID)) == 1 {
				state = "match"
			} else {
				state = "mismatch"
			}
		}
	}
	h.count(ctx, "garmin:cnt:cid:"+h.dayBucket()+":"+state, 1, 8*24*time.Hour)
	if o.ip != "" {
		_, _ = h.kv.SAddCap(ctx, "garmin:ips", o.ip, 20, 7*24*time.Hour)
	}
}

// --- 讀取與解析 ---

type garminPushResult struct {
	Activities []garminActivityIn
	Deregs     []garminDeregIn
	Perms      []garminPermIn
	Malformed  int // 單筆型別不符而丟棄的筆數
}

// garminCountReader 計算實際讀取的位元組數（用來核對 Content-Length）。
type garminCountReader struct {
	r io.Reader
	n int64
}

func (c *garminCountReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// garminMaxWSRun：連續空白位元組的上限。encoding/json 的 Decoder 在跳過空白時每次補讀都會從頭重掃，
// 超長空白串會變成 O(n²) 的 CPU 消耗（16MB 空白約 10 秒以上）；正常 JSON（含排版縮排）遠低於此值。
const garminMaxWSRun = 64 << 10

// garminWSGuard 擋掉超長空白串（回 errGarminBadJSON：完整但不合法的內容，回 200 並丟棄）。
type garminWSGuard struct {
	r   io.Reader
	run int
}

func (g *garminWSGuard) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	for i := 0; i < n; i++ {
		switch p[i] {
		case ' ', '\t', '\r', '\n':
			g.run++
			if g.run > garminMaxWSRun {
				return i, errGarminBadJSON
			}
		default:
			g.run = 0
		}
	}
	return n, err
}

// garminInflateReader 限制解壓後的大小（超過回 errGarminTooLarge）。
type garminInflateReader struct {
	r   io.Reader
	max int64
	n   int64
}

func (g *garminInflateReader) Read(p []byte) (int, error) {
	if g.n >= g.max {
		// 再讀 1 byte 確認是否還有資料：有就是超限，沒有就是正常結尾
		var one [1]byte
		n, err := g.r.Read(one[:])
		if n > 0 {
			return 0, errGarminTooLarge
		}
		return 0, err
	}
	if rem := g.max - g.n; int64(len(p)) > rem {
		p = p[:rem]
	}
	n, err := g.r.Read(p)
	g.n += int64(n)
	return n, err
}

// readPush 讀取並解析推送 body。回傳解析結果、實際讀取（壓縮前）位元組數與錯誤；錯誤種類見檔頭的回應規則。
func (h *GarminHandler) readPush(w http.ResponseWriter, r *http.Request, kind string) (garminPushResult, int64, error) {
	// 全域 ReadTimeout（15 秒）對大 body 太短：放寬到 25 秒；不支援（測試 recorder 等）就略過。
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(garminReadWindow)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Debug().Err(err).Msg("garmin webhook: SetReadDeadline unsupported, using server default")
	}
	cr := &garminCountReader{r: http.MaxBytesReader(w, r.Body, h.cfg.WebhookMaxBytes)}
	var src io.Reader = cr
	if enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc != "" && enc != "identity" {
		if enc != "gzip" {
			return garminPushResult{}, 0, errGarminBadJSON // 不支援的編碼：視為內容無法解析
		}
		gz, err := gzip.NewReader(cr)
		if err != nil {
			return garminPushResult{}, cr.n, wrapTruncated(err)
		}
		defer gz.Close()
		src = &garminInflateReader{r: gz, max: garminInflateMax}
	}

	res, err := parseGarminPush(&garminWSGuard{r: src}, kind)
	if err != nil {
		if errors.Is(err, io.EOF) && cr.n == 0 {
			return res, cr.n, errGarminBadJSON // 完整的空 body
		}
		return res, cr.n, err
	}
	// 讀完剩餘位元組（尾端空白等），並核對 Content-Length：不符＝被截斷或多出資料，一律當讀取不完整。
	if _, err := io.Copy(io.Discard, src); err != nil {
		return res, cr.n, classifyReadErr(err)
	}
	if r.ContentLength >= 0 && cr.n != r.ContentLength {
		return res, cr.n, wrapTruncated(io.ErrUnexpectedEOF)
	}
	return res, cr.n, nil
}

func wrapTruncated(err error) error {
	return errors.Join(errGarminTruncated, err)
}

// classifyReadErr：讀取層錯誤分類。MaxBytes／解壓超限原樣回傳（→413），其餘一律視為讀取不完整。
func classifyReadErr(err error) error {
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe), errors.Is(err, errGarminTooLarge), errors.Is(err, errGarminBadJSON):
		return err
	}
	return wrapTruncated(err)
}

// classifyJSONErr：解析層錯誤分類。語法錯誤（非提前結束）＝完整但不合法；EOF／讀取錯誤＝讀取不完整。
func classifyJSONErr(err error) error {
	var se *json.SyntaxError
	var ute *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se), errors.As(err, &ute):
		return errGarminBadJSON
	}
	return classifyReadErr(err)
}

// parseGarminPush 串流解析：只取 kind 對應的陣列元素（逐筆解碼，記憶體只放一筆），其餘鍵略過。
func parseGarminPush(r io.Reader, kind string) (garminPushResult, error) {
	var res garminPushResult
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return res, classifyJSONErr(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return res, errGarminBadJSON
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return res, classifyJSONErr(err)
		}
		key, ok := kt.(string)
		if !ok {
			return res, errGarminBadJSON
		}
		first, err := dec.Token()
		if err != nil {
			return res, classifyJSONErr(err)
		}
		d, isDelim := first.(json.Delim)
		if key != kind || !isDelim || d != '[' {
			if isDelim {
				if err := garminSkipRest(dec, d); err != nil {
					return res, classifyJSONErr(err)
				}
			}
			continue
		}
		for dec.More() {
			var derr error
			switch kind {
			case garminPushActivities:
				derr = garminDecodeElem(dec, &res.Activities, &res.Malformed)
			case garminPushDeregs:
				derr = garminDecodeElem(dec, &res.Deregs, &res.Malformed)
			case garminPushPerms:
				derr = garminDecodeElem(dec, &res.Perms, &res.Malformed)
			default:
				derr = errGarminBadJSON
			}
			if derr != nil {
				return res, derr
			}
		}
		if _, err := dec.Token(); err != nil { // ']'
			return res, classifyJSONErr(err)
		}
	}
	if _, err := dec.Token(); err != nil { // '}'
		return res, classifyJSONErr(err)
	}
	return res, nil
}

func garminDecodeElem[T any](dec *json.Decoder, out *[]T, malformed *int) error {
	var el T
	if err := dec.Decode(&el); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			*malformed++ // 這一筆型別不符（例如不是物件）：丟棄，繼續下一筆
			return nil
		}
		return classifyJSONErr(err)
	}
	*out = append(*out, el)
	return nil
}

// garminSkipRest 略過一個已讀到開頭 Delim 的巢狀值。
func garminSkipRest(dec *json.Decoder, first json.Delim) error {
	if first != '{' && first != '[' {
		return nil
	}
	depth := 1
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// --- 過濾與事件建構 ---

type garminBuildStats struct {
	Total           int
	SkippedType     int // 非白名單活動類型
	BadPayload      int // 缺 userId／id／開始時間等
	CallbackIgnored int // 含 callbackURL（Ping 式通知）：忽略
	Malformed       int
}

func garminUniqueUserIDs(evs []garminEventIn) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, e := range evs {
		if _, ok := seen[e.ProviderUserID]; !ok {
			seen[e.ProviderUserID] = struct{}{}
			out = append(out, e.ProviderUserID)
		}
	}
	sort.Strings(out)
	return out
}

func garminEventIDs(evs []garminEvent) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.ID)
	}
	return out
}

// garminShortHash：sha256 前 n 個十六進位字元。
func garminShortHash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:])
	if n > len(h) {
		n = len(h)
	}
	return h[:n]
}

// garminDedupeKey 組去重鍵並限制長度（VARCHAR(160)）：prefix:<uid>:<id>；過長的 id 改用雜湊。
// 鍵內含 userId：防止攻擊者用自己的 userId 搶先送出「別人將來的 summaryId」而讓真實事件被去重吞掉。
func garminDedupeKey(prefix, uid, id string) string {
	k := prefix + ":" + uid + ":" + id
	if len(k) <= garminDedupeKeyMax {
		return k
	}
	return prefix + ":" + uid + ":h" + garminShortHash(id, 40)
}

// buildGarminActivityEvents 過濾並最小化 activities 推送：只留白名單類型、必要欄位齊全的筆；
// 座標、活動名稱、熱量等一律不進 payload。
func buildGarminActivityEvents(items []garminActivityIn, st *garminBuildStats) []garminEventIn {
	var out []garminEventIn
	for _, a := range items {
		uid := string(a.UserID)
		if uid == "" || len(uid) > garminUserIDMax {
			st.BadPayload++
			continue
		}
		if a.CallbackURL != "" {
			st.CallbackIgnored++ // DG-16：不做 Ping，不碰 callbackURL
			continue
		}
		if garminActivityKind(string(a.ActivityType)) == "" {
			st.SkippedType++
			continue
		}
		id := string(a.SummaryID)
		if id == "" {
			id = string(a.ActivityID)
		}
		if id == "" || !a.StartTime.OK || a.StartTime.V <= 0 || a.StartTime.V > 4e9 {
			st.BadPayload++
			continue
		}
		sa := garminStoredActivity{
			SummaryID:       string(a.SummaryID),
			ActivityID:      string(a.ActivityID),
			ActivityType:    strings.ToUpper(strings.TrimSpace(string(a.ActivityType))),
			StartTime:       int64(a.StartTime.V),
			DeviceName:      truncateRunes(string(a.DeviceName), garminDeviceMax),
			Manual:          bool(a.Manual),
			IsWebUpload:     bool(a.IsWebUpload),
			IsParent:        bool(a.IsParent),
			ParentSummaryID: truncateRunes(string(a.ParentSummaryID), 64),
		}
		if a.Duration.OK {
			sa.Duration = a.Duration.V
		}
		if a.Distance.OK {
			sa.Distance = a.Distance.V
		}
		if a.ElevationGain.OK {
			v := a.ElevationGain.V
			sa.ElevationGain = &v
		}
		if garminStoreAvgHR && a.AvgHeartRate.OK {
			v := a.AvgHeartRate.V
			sa.AvgHeartRate = &v
		}
		payload, err := json.Marshal(sa)
		if err != nil {
			st.BadPayload++
			continue
		}
		key := garminDedupeKey("act", uid, id)
		out = append(out, garminEventIn{EventType: garminEvActivity, ProviderUserID: uid, DedupeKey: &key, Payload: payload})
	}
	return out
}

// buildGarminDeregEvents：deregistrations 推送（只有 userId）。dedupe_key 為 NULL：每則都處理（處理本身冪等）。
func buildGarminDeregEvents(items []garminDeregIn, st *garminBuildStats) []garminEventIn {
	var out []garminEventIn
	for _, d := range items {
		uid := string(d.UserID)
		if uid == "" || len(uid) > garminUserIDMax {
			st.BadPayload++
			continue
		}
		out = append(out, garminEventIn{EventType: garminEvDeregister, ProviderUserID: uid, Payload: []byte(`{}`)})
	}
	return out
}

// buildGarminPermissionEvents：userPermissionsChange 推送。去重鍵＝perm:<uid>:<changeTimeInSeconds>
// （R-C2：不可只用可能重複的 summaryId，否則「關→開」的第二則可能被去重吞掉而永久停在暫停）。
// permissions 內容只留存供對帳，處理時一律以 API 取得當下權限，不信推送內容。
func buildGarminPermissionEvents(items []garminPermIn, st *garminBuildStats) []garminEventIn {
	var out []garminEventIn
	for _, p := range items {
		uid := string(p.UserID)
		if uid == "" || len(uid) > garminUserIDMax {
			st.BadPayload++
			continue
		}
		sp := garminStoredPermission{SummaryID: truncateRunes(string(p.SummaryID), 64), Permissions: garminParsePermissions(p.Permissions)}
		var suffix string
		switch {
		case p.ChangeTime.OK && p.ChangeTime.V > 0 && p.ChangeTime.V < 4e9:
			sp.ChangeTime = int64(p.ChangeTime.V)
			suffix = strconv.FormatInt(sp.ChangeTime, 10)
		case sp.SummaryID != "":
			suffix = "s" + sp.SummaryID
		default:
			b, _ := json.Marshal(sp)
			suffix = "h" + garminShortHash(string(b), 16)
		}
		payload, err := json.Marshal(sp)
		if err != nil {
			st.BadPayload++
			continue
		}
		key := garminDedupeKey("perm", uid, suffix)
		out = append(out, garminEventIn{EventType: garminEvPermission, ProviderUserID: uid, DedupeKey: &key, Payload: payload})
	}
	return out
}
