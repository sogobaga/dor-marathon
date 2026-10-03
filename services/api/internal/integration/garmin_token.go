package integration

// Garmin token 生命週期：到期前刷新、輪替（每次都換發新 refresh token，必須先落盤再使用）、單飛鎖、保活。
//
// 並發與可靠度（審查 R-M2）：
//   - 單飛鎖＝行程內互斥＋Postgres advisory xact lock（不依賴 Redis；Redis 被清空或不可用也不會讓兩個行程同時刷新）。
//     拿不到跨副本鎖時最多輪詢 5 秒重讀 DB；仍過期就回 errGarminTransient——**絕不自己去換**（舊 refresh 可能已失效）。
//   - 新 token 落盤使用脫離請求的 context（10 秒、重試 3 次）；落盤失敗告警（只帶連線 id 前綴）。
//   - invalid_grant 時先重讀 DB：列上的 refresh 與送出的不同＝別人已輪替，改用新的重試一次，不標 reauth。
//   - invalid_client＝設定錯誤，告警但不標記使用者；429／5xx／逾時＝暫時性，不改狀態。
//   - App 世代（client id）不符：不打 token 端點，直接標記需重新授權。
//   - 保活（refresh 剩 <30 天）：最多每 24 小時一次（Redis 冷卻旗標），避免「Garmin 若不因強制早換而延長 refresh 壽命」
//     時在每一筆推送都打 token 端點（Evaluation 金鑰有速率限制）。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	garminRefreshSkew        = 10 * time.Minute // access token 到期前 10 分鐘就換（官方建議至少扣 600 秒）
	garminKeepaliveWindow    = 30 * 24 * time.Hour
	garminKeepaliveCooldown  = 24 * time.Hour
	garminRefreshWait        = 5 * time.Second
	garminDefaultAccessTTL   = 24 * time.Hour
	garminDefaultRefreshTTL  = 90 * 24 * time.Hour
	garminRefreshFailAlertAt = 3
)

// errGarminNoConnection：連線列已不存在（刷新途中被中斷／清理）。
var errGarminNoConnection = errors.New("garmin: connection no longer exists")

func (h *GarminHandler) generation() string { return garminAppGeneration(h.cfg.ClientID) }

func (h *GarminHandler) tokenFresh(c *garminConn, force bool) bool {
	return !force && c.ExpiresAt.Sub(h.now()) > garminRefreshSkew
}

// ensureFresh 回傳 access token 仍有效的連線（必要時刷新並落盤）。force=true 時不論到期與否都刷新（保活用）。
// 錯誤分類：errGarminReauth／errGarminGeneration（需使用者重新授權）、errGarminConfig、errGarminTransient、errGarminNoConnection。
func (h *GarminHandler) ensureFresh(ctx context.Context, c *garminConn, force bool) (*garminConn, error) {
	if c == nil {
		return nil, errGarminNoConnection
	}
	if c.Issuer != h.generation() {
		_ = h.store.MarkGarminReauth(ctx, c.ID)
		return nil, errGarminGeneration
	}
	if c.ReauthRequiredAt != nil {
		return nil, errGarminReauth
	}
	if h.tokenFresh(c, force) {
		return c, nil
	}
	if c.RefreshExpiresAt != nil && !c.RefreshExpiresAt.After(h.now()) {
		_ = h.store.MarkGarminReauth(ctx, c.ID)
		return nil, errGarminReauth
	}

	deadline := h.now().Add(h.refreshWait)
	for {
		var out *garminConn
		var rerr error
		unlock := h.userMu.Lock("rt:" + c.ProviderUserID)
		lerr := h.store.WithGarminUserLock(ctx, "rt:"+c.ProviderUserID, func(lctx context.Context) error {
			out, rerr = h.refreshLocked(lctx, c, force)
			return nil
		})
		unlock()
		if lerr == nil {
			return out, rerr
		}
		if !errors.Is(lerr, errGarminBusy) {
			return nil, fmt.Errorf("%w: lock: %v", errGarminTransient, lerr)
		}
		// 別的行程正在刷新：稍等後重讀 DB；已刷新就直接用，等不到就回暫時性錯誤（不自己換）。
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", errGarminTransient, ctx.Err())
		}
		cur, err := h.store.GetGarminDirectByUserID(ctx, c.ProviderUserID, true)
		if err != nil {
			return nil, fmt.Errorf("%w: reread: %v", errGarminTransient, err)
		}
		if cur == nil {
			return nil, errGarminNoConnection
		}
		if cur.ReauthRequiredAt != nil {
			return nil, errGarminReauth
		}
		if h.tokenFresh(cur, false) {
			return cur, nil
		}
		if h.now().After(deadline) {
			return nil, fmt.Errorf("%w: refresh lock busy", errGarminTransient)
		}
	}
}

// refreshLocked 在持有刷新鎖時執行：重讀列、確認仍需刷新、呼叫 token 端點、先落盤再回傳。
func (h *GarminHandler) refreshLocked(ctx context.Context, c *garminConn, force bool) (*garminConn, error) {
	cur, err := h.store.GetGarminDirectByUserID(ctx, c.ProviderUserID, true)
	if err != nil {
		return nil, fmt.Errorf("%w: reread: %v", errGarminTransient, err)
	}
	if cur == nil {
		return nil, errGarminNoConnection
	}
	if cur.Issuer != h.generation() {
		_ = h.store.MarkGarminReauth(ctx, cur.ID)
		return nil, errGarminGeneration
	}
	if cur.ReauthRequiredAt != nil {
		return nil, errGarminReauth
	}
	// 別人剛刷新過（列上的到期時間比我們手上的新）：直接用。force（保活）則照樣刷新。
	if !force && h.tokenFresh(cur, false) {
		return cur, nil
	}
	if cur.RefreshExpiresAt != nil && !cur.RefreshExpiresAt.After(h.now()) {
		_ = h.store.MarkGarminReauth(ctx, cur.ID)
		return nil, errGarminReauth
	}
	if cur.RefreshToken == "" {
		_ = h.store.MarkGarminReauth(ctx, cur.ID)
		return nil, errGarminReauth
	}

	sent := cur.RefreshToken
	for attempt := 0; attempt < 2; attempt++ {
		apiCtx, cancel := garminAPITimeout(ctx)
		tr, err := h.refreshToken(apiCtx, sent)
		cancel()
		if err == nil {
			return h.persistRefreshed(ctx, cur, tr)
		}
		var ae *garminAPIError
		if !errors.As(err, &ae) {
			return nil, fmt.Errorf("%w: %v", errGarminTransient, err)
		}
		switch {
		case ae.Code == "invalid_client":
			h.alert("garmin_token_config", "Garmin token 憑證設定錯誤", "token 端點回 invalid_client：請檢查 GARMIN_CLIENT_ID／GARMIN_CLIENT_SECRET／GARMIN_TOKEN_AUTH（不會標記使用者為需重新授權）")
			return nil, fmt.Errorf("%w: invalid_client", errGarminConfig)
		case ae.Code == "invalid_grant" && (ae.Status == http.StatusBadRequest || ae.Status == http.StatusUnauthorized):
			// 先重讀 DB：列上的 refresh 與送出的不同＝別的行程已輪替，改用新的重試一次，不標 reauth。
			again, rerr := h.store.GetGarminDirectByUserID(ctx, cur.ProviderUserID, true)
			if rerr == nil && again != nil && again.RefreshToken != "" && again.RefreshToken != sent && attempt == 0 {
				sent, cur = again.RefreshToken, again
				continue
			}
			_ = h.store.MarkGarminReauth(ctx, cur.ID)
			return nil, errGarminReauth
		case garminIsTransientStatus(ae.Status):
			h.noteRefreshFailure(ctx)
			return nil, fmt.Errorf("%w: token endpoint %d", errGarminTransient, ae.Status)
		default:
			// 其他 4xx（沒有可辨識的 OAuth 錯誤碼）：不確定是誰的問題，不標記使用者；計入失敗告警。
			h.noteRefreshFailure(ctx)
			return nil, fmt.Errorf("%w: token endpoint %d", errGarminTransient, ae.Status)
		}
	}
	return nil, errGarminReauth
}

// persistRefreshed 先把輪替後的新 token 落盤（脫離請求 context、重試 3 次），成功才回傳更新後的連線。
func (h *GarminHandler) persistRefreshed(ctx context.Context, cur *garminConn, tr *garminTokenResponse) (*garminConn, error) {
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = cur.RefreshToken // Garmin 一律輪替；萬一沒回就沿用舊的，不把連線弄壞
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
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var err error
	for i, wait := range []time.Duration{0, 200 * time.Millisecond, 500 * time.Millisecond} {
		if wait > 0 {
			time.Sleep(wait)
		}
		if err = h.store.UpdateGarminTokens(pctx, cur.ID, tr.AccessToken, refresh, exp, rexp); err == nil {
			break
		}
		log.Warn().Err(err).Int("try", i+1).Str("conn", prefix8(cur.ID)).Msg("garmin: persist refreshed tokens failed")
	}
	if err != nil {
		// 新 refresh token 已發出但沒存成：舊的可能已失效，下次刷新可能需要使用者重新授權。
		h.alert("garmin_token_persist", "Garmin 新 token 落盤失敗", "conn="+prefix8(cur.ID)+"（新 refresh token 已發出但寫入失敗）")
		return nil, fmt.Errorf("%w: persist: %v", errGarminTransient, err)
	}
	out := *cur
	out.AccessToken, out.RefreshToken = tr.AccessToken, refresh
	out.ExpiresAt, out.RefreshExpiresAt, out.ReauthRequiredAt = exp, &rexp, nil
	return &out, nil
}

// noteRefreshFailure：同一小時 ≥3 次暫時性／不明刷新失敗就告警一次（不含使用者資料）。
func (h *GarminHandler) noteRefreshFailure(ctx context.Context) {
	if n := h.count(ctx, "garmin:cnt:refreshfail:"+h.hourBucket(), 1, 2*time.Hour); n == garminRefreshFailAlertAt {
		h.alert("garmin_token_refresh", "Garmin token 刷新連續失敗", "同一小時內 ≥3 次 token 刷新失敗（429／5xx／逾時或不明 4xx）；請檢查 Garmin 服務狀態與速率限制")
	}
}

// maybeKeepAlive 事件驅動的保活：refresh 剩 <30 天、且 24 小時內沒保活過 → 強制刷新一次（換出新的 refresh token）。
// 只打 token 端點，不打資料端點。呼叫端應在背景呼叫（goKeepAlive）。
func (h *GarminHandler) maybeKeepAlive(ctx context.Context, c *garminConn) {
	if c == nil || c.Via != garminViaDirect || c.ReauthRequiredAt != nil || c.RefreshExpiresAt == nil {
		return
	}
	if c.RefreshExpiresAt.Sub(h.now()) >= garminKeepaliveWindow {
		return
	}
	ok, err := h.kv.SetNX(ctx, "garmin:ka:"+c.ID, "1", garminKeepaliveCooldown)
	if err != nil || !ok {
		return
	}
	if _, err := h.ensureFresh(ctx, c, true); err != nil {
		log.Info().Err(err).Str("conn", prefix8(c.ID)).Msg("garmin keepalive refresh did not complete")
	}
}

// goKeepAlive 背景保活（不阻塞呼叫端；Drain 會等）。
func (h *GarminHandler) goKeepAlive(ctx context.Context, c *garminConn) {
	if c == nil || h.draining.Load() || !h.workBegin() {
		return
	}
	dctx := context.WithoutCancel(ctx)
	go func() {
		defer h.workEnd()
		defer func() { _ = recover() }()
		kctx, cancel := context.WithTimeout(dctx, 30*time.Second)
		defer cancel()
		h.maybeKeepAlive(kctx, c)
	}()
}
