package integration

// 使用者端點：POST /connect、GET /callback、GET /status、POST /disconnect。
//
//   - /connect：登入＋入口閘＋限流（router 掛）。body {consent:true, consent_v:"<版本>"}；種 nonce cookie、PKCE verifier 存 Redis；
//     回 {url}。同意時間與版本在 callback 成功時寫進連線列（後端紀錄，不只靠前端勾選）。
//   - /callback：公開（驗 state＋nonce），一律 302 導回 /?garmin=connected 或 /?garmin=error&reason=<固定詞彙>。
//     reason 詞彙：invalid_state state_mismatch denied missing_code token_exchange_failed api_failed no_activity_permission
//     already_linked account_changed entry_closed server_config save_failed disabled。
//     授權中途失敗留下的「孤兒註冊」：只有在新取得的 Garmin userId **沒有綁定任何 DOR 帳號**時才 best-effort 撤銷；
//     已綁定（他人或同一人）一律不撤銷（那個註冊屬於合法的連線）。
//   - /status、/disconnect：不受入口閘限制（只要有連線列就能看、能中斷）。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/integration/entrygate"
)

var consentVersionRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,24}$`)

// configured：連接所需的設定都在（缺任一回 false，/connect 回 503 garmin_disabled）。
func (h *GarminHandler) configured() bool {
	return h.cfg.ClientID != "" && h.cfg.ClientSecret != "" && h.webhookConfigured() &&
		h.deps.JWTSecret != "" && h.disabledReason == ""
}

// entryState 入口狀態（"shown"／"hidden"），與 Dashboard 共用同一個 entrygate.Resolve。
func (h *GarminHandler) entryState(ctx context.Context, userID string) (string, error) {
	if h.entryFor != nil {
		return h.entryFor(ctx, userID)
	}
	if h.db == nil {
		return entrygate.Hidden, nil
	}
	return entrygate.Load(ctx, h.db, garminEntryStateKey, garminWhitelistKey, "", userID)
}

func (h *GarminHandler) redirectFront(w http.ResponseWriter, r *http.Request, status, reason string) {
	base := h.deps.FrontendURL
	if base == "" {
		base = "/"
	}
	target := appendQuery(base, "garmin", status)
	if reason != "" {
		target = appendQuery(target, "reason", reason)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// Connect POST /connect
func (h *GarminHandler) Connect(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		Consent  bool   `json:"consent"`
		ConsentV string `json:"consent_v"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || !req.Consent {
		respondErr(w, http.StatusBadRequest, "consent_required")
		return
	}
	if !consentVersionRe.MatchString(req.ConsentV) || !h.consentVersionOK(req.ConsentV) {
		respondErr(w, http.StatusBadRequest, "invalid_consent_version")
		return
	}
	if !h.configured() || !TokenKeyConfigured() {
		respondErr(w, http.StatusServiceUnavailable, "garmin_disabled")
		return
	}
	ctx := r.Context()
	entry, err := h.entryState(ctx, userID)
	if err != nil {
		log.Error().Err(err).Msg("garmin connect: entry lookup failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if entry != entrygate.Shown {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if blocked, err := h.store.GarminBlocked(ctx, userID, ""); err != nil {
		log.Error().Err(err).Msg("garmin connect: blocklist lookup failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	} else if blocked {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	verifier, err1 := garminNewVerifier()
	sid, err2 := garminRandB64(16)
	nonce, err3 := garminRandB64(16)
	if err := errors.Join(err1, err2, err3); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if err := h.kv.Set(ctx, garminPKCEKeyPrefix+sid, verifier, garminPKCETTL); err != nil {
		log.Error().Err(err).Msg("garmin connect: store pkce verifier failed")
		respondErr(w, http.StatusServiceUnavailable, "try_again")
		return
	}
	now := h.now()
	state := garminSignState(h.deps.JWTSecret, garminState{UserID: userID, Nonce: nonce, SID: sid, ConsentV: req.ConsentV,
		IssuedAt: now, ExpiresAt: now.Add(garminStateTTL)})
	http.SetCookie(w, garminNonceCookieFor(nonce, int(garminStateTTL.Seconds())))
	respondJSON(w, http.StatusOK, map[string]string{"url": h.garminAuthURL(garminChallenge(verifier), state)})
}

func (h *GarminHandler) consentVersionOK(v string) bool {
	for _, ok := range h.cfg.ConsentVersions {
		if ok == v {
			return true
		}
	}
	return false
}

// orphanCleanup 授權中途失敗時，best-effort 撤銷「孤兒註冊」——只有這個 Garmin userId 沒有綁定任何 DOR 帳號才撤銷。
func (h *GarminHandler) orphanCleanup(ctx context.Context, access, garminUserID string) {
	if access == "" || garminUserID == "" {
		return // 不知道 userId＝不知道是否綁定了別人：保守不撤銷
	}
	bound, err := h.store.GetGarminDirectByUserID(ctx, garminUserID, false)
	if err != nil || bound != nil {
		return
	}
	dctx, cancel := garminAPITimeout(context.WithoutCancel(ctx))
	defer cancel()
	if err := h.deleteRegistration(dctx, access); err != nil {
		log.Warn().Err(err).Msg("garmin callback: orphan registration cleanup failed (best effort)")
	}
}

// Callback GET /callback
func (h *GarminHandler) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cookieVal := ""
	if c, err := r.Cookie(garminNonceCookie); err == nil {
		cookieVal = c.Value
	}
	http.SetCookie(w, garminNonceCookieFor("", -1)) // 不論結果先清 cookie
	if !h.configured() {
		h.redirectFront(w, r, "error", "disabled")
		return
	}
	q := r.URL.Query()
	st, ok := garminVerifyState(h.deps.JWTSecret, q.Get("state"), h.now())
	if !ok {
		h.redirectFront(w, r, "error", "invalid_state")
		return
	}
	if !garminNonceMatches(cookieVal, st.Nonce) {
		h.redirectFront(w, r, "error", "state_mismatch")
		return
	}
	verifier, found, err := h.kv.GetDel(ctx, garminPKCEKeyPrefix+st.SID) // 一次性
	if err != nil {
		log.Error().Err(err).Msg("garmin callback: pkce verifier lookup failed")
		h.redirectFront(w, r, "error", "server_config")
		return
	}
	if !found {
		h.redirectFront(w, r, "error", "invalid_state")
		return
	}
	if q.Get("error") != "" {
		h.redirectFront(w, r, "error", "denied")
		return
	}
	code := strings.TrimSpace(q.Get("code"))
	if code == "" {
		h.redirectFront(w, r, "error", "missing_code")
		return
	}
	userID := st.UserID
	if entry, err := h.entryState(ctx, userID); err != nil || entry != entrygate.Shown {
		h.redirectFront(w, r, "error", "entry_closed")
		return
	}
	if !TokenKeyConfigured() {
		h.alert("garmin_server_config", "Garmin 連接失敗（缺 token 金鑰）", "STRAVA_TOKEN_KEY 缺失或無效：Garmin 直連拒絕保存 token（fail-closed）")
		h.redirectFront(w, r, "error", "server_config")
		return
	}

	apiCtx, cancel := garminAPITimeout(ctx)
	defer cancel()
	tr, err := h.exchangeCode(apiCtx, code, verifier)
	if err != nil || tr.RefreshToken == "" {
		log.Warn().Err(err).Msg("garmin callback: token exchange failed")
		h.redirectFront(w, r, "error", "token_exchange_failed")
		return
	}
	// 從這裡起 Garmin 端已有註冊：任何失敗都要考慮孤兒註冊（見 orphanCleanup）。
	garminUID, err := h.getUserID(apiCtx, tr.AccessToken)
	if err != nil {
		log.Warn().Err(err).Msg("garmin callback: get user id failed")
		h.redirectFront(w, r, "error", "api_failed") // 不知道 userId：不撤銷（可能綁了別人）
		return
	}
	perms, err := h.getPermissions(apiCtx, tr.AccessToken)
	if err != nil {
		log.Warn().Err(err).Msg("garmin callback: get permissions failed")
		h.orphanCleanup(ctx, tr.AccessToken, garminUID)
		h.redirectFront(w, r, "error", "api_failed")
		return
	}
	if blocked, berr := h.store.GarminBlocked(ctx, userID, garminUID); berr != nil || blocked {
		h.orphanCleanup(ctx, tr.AccessToken, garminUID)
		h.redirectFront(w, r, "error", "entry_closed")
		return
	}
	hasExport := false
	for _, p := range perms {
		if p == garminPermActivityExport {
			hasExport = true
		}
	}
	if !hasExport {
		// 缺 ACTIVITY_EXPORT：不得顯示已連接、不存連線。若是已連線者重新授權時縮減權限，順手更新權限旗標（卡片顯示暫停）。
		if bound, _ := h.store.GetGarminDirectByUserID(ctx, garminUID, false); bound != nil && bound.UserID == userID {
			_ = h.store.SetGarminScope(ctx, bound.ID, strings.Join(perms, ","))
		} else {
			h.orphanCleanup(ctx, tr.AccessToken, garminUID)
		}
		h.redirectFront(w, r, "error", "no_activity_permission")
		return
	}

	now := h.now()
	exp := now.Add(garminDefaultAccessTTL)
	if tr.ExpiresIn > 0 {
		exp = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	rexp := now.Add(garminDefaultRefreshTTL)
	if tr.RefreshExpiresIn > 0 {
		rexp = now.Add(time.Duration(tr.RefreshExpiresIn) * time.Second)
	}
	// 儲存使用脫離請求的 context：連線已在 Garmin 端建立，不要因為使用者關掉頁面就半途而廢。
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer scancel()
	res, err := h.store.SaveGarmin(sctx, garminSaveInput{
		UserID: userID, GarminUserID: garminUID,
		AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken,
		ExpiresAt: exp, RefreshExpiresAt: rexp,
		Scope: strings.Join(perms, ","), Issuer: h.generation(),
		ConsentAt: st.IssuedAt, ConsentVersion: st.ConsentV,
	})
	switch {
	case err == nil:
	case errors.Is(err, ErrGarminAlreadyLinked):
		// 這個 Garmin 帳號已綁在另一個 DOR 帳號：絕不撤銷（那個註冊屬於合法的連線）。
		h.redirectFront(w, r, "error", "already_linked")
		return
	case errors.Is(err, ErrGarminAccountChanged):
		h.orphanCleanup(ctx, tr.AccessToken, garminUID) // 新帳號若未綁任何 DOR 帳號＝孤兒；已綁定則不動
		h.redirectFront(w, r, "error", "account_changed")
		return
	case errors.Is(err, ErrTokenKeyMissing):
		h.alert("garmin_server_config", "Garmin 連接失敗（缺 token 金鑰）", "STRAVA_TOKEN_KEY 缺失或無效：Garmin 直連拒絕保存 token（fail-closed）")
		h.orphanCleanup(ctx, tr.AccessToken, garminUID)
		h.redirectFront(w, r, "error", "server_config")
		return
	default:
		log.Error().Err(err).Msg("garmin callback: save connection failed")
		h.orphanCleanup(ctx, tr.AccessToken, garminUID)
		h.redirectFront(w, r, "error", "save_failed")
		return
	}
	h.kv.Del(ctx, "garmin:selfdereg:"+userID) // 剛重新連接：清掉先前自己中斷留下的標記
	log.Info().Str("user", prefix8(userID)).Bool("reauth", !res.Inserted).Str("prev_via", res.PrevVia).Msg("garmin connected")
	h.redirectFront(w, r, "connected", "")
}

// Status GET /status（不受入口閘限制；不呼叫 Garmin）。
func (h *GarminHandler) Status(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()
	c, err := h.store.GetGarminByUser(ctx, userID, false)
	if err != nil {
		log.Error().Err(err).Msg("garmin status: load connection failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	entry, eerr := h.entryState(ctx, userID)
	if eerr != nil {
		entry = entrygate.Hidden
	}
	out := map[string]any{
		"connected": false, "connected_at": nil, "import_from": nil, "last_data_at": nil, "device_name": nil,
		"needs_reauth": false, "paused": false, "legacy_terra": false, "entry": entry,
	}
	if c != nil && c.Via == garminViaTerra {
		out["legacy_terra"] = true
	}
	if c != nil && c.Via == garminViaDirect {
		connectedAt := c.ConnectedAt
		if c.AuthorizedAt != nil {
			connectedAt = *c.AuthorizedAt
		}
		out["connected"] = true
		out["connected_at"] = connectedAt.UTC().Format(time.RFC3339)
		out["import_from"] = c.ConnectedAt.UTC().Format(time.RFC3339) // 匯入起算點（floor）
		if c.LastSyncedAt != nil {
			out["last_data_at"] = c.LastSyncedAt.UTC().Format(time.RFC3339)
		}
		out["needs_reauth"] = c.ReauthRequiredAt != nil || c.Issuer != h.generation() ||
			(c.RefreshExpiresAt != nil && !c.RefreshExpiresAt.After(h.now()))
		out["paused"] = c.paused()
		if name, derr := h.store.LatestGarminDeviceName(ctx, userID); derr == nil && name != "" {
			out["device_name"] = name
		}
		h.goKeepAlive(ctx, c) // 使用者開卡片時順手保活（背景、24 小時冷卻）
	}
	respondJSON(w, http.StatusOK, out)
}

// Disconnect POST /disconnect（不受入口閘限制；冪等）。
func (h *GarminHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()
	c, err := h.store.GetGarminByUser(ctx, userID, true)
	if err != nil {
		// 常見原因：token 解密失敗（金鑰缺失）。仍要能中斷：改讀不含 token 的列，略過向 Garmin 撤銷。
		log.Warn().Err(err).Msg("garmin disconnect: load with tokens failed, continuing without registration delete")
		c, err = h.store.GetGarminByUser(ctx, userID, false)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	if c == nil || c.Via != garminViaDirect {
		respondJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_activities": 0, "garmin_notified": false, "was_connected": false})
		return
	}

	// 先設自己中斷的標記（Garmin 回送的最後一則 deregistration 到達時據此不發站內信），再撤銷註冊。
	_ = h.kv.Set(ctx, "garmin:selfdereg:"+userID, "1", 5*time.Minute)
	notified := false
	if c.AccessToken != "" || c.RefreshToken != "" {
		if cur, ferr := h.ensureFresh(ctx, c, false); ferr == nil && cur.AccessToken != "" {
			dctx, cancel := garminAPITimeout(ctx)
			derr := h.deleteRegistration(dctx, cur.AccessToken)
			cancel()
			notified = derr == nil
			if derr != nil {
				log.Warn().Err(derr).Msg("garmin disconnect: registration delete failed, cleaning up locally anyway")
			}
		} else if ferr != nil {
			log.Info().Err(ferr).Msg("garmin disconnect: no usable token, skipping registration delete")
		}
	}
	// 一律本地清理（連線、活動、事件、偏好來源）。
	res, perr := h.PurgeUser(ctx, userID)
	if perr != nil {
		log.Error().Err(perr).Msg("garmin disconnect: local purge failed")
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_activities": res.DeletedActivities, "garmin_notified": notified, "was_connected": true})
}
