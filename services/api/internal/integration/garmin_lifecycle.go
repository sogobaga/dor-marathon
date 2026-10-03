package integration

// 撤銷、權限異動、清除（PurgeUser）。
//
// 破壞性事件一律「先用該連線自己的 token 打 Garmin 實測，再動手」（比照 strava.go handleDeauthorizeEvent）：
// webhook 只有路徑 token 保護、沒有簽章，偽造或過期的 deregistration 事件不可直接刪資料。
// 這同時是 Evaluation→Production 換 App 的安全閥：舊 App 的撤銷通知到達時，連線若已用新 App 重新授權，
// 實測會回 200 → 不刪。
//
// 撤銷實測矩陣（審查 R-M1）：
//   401／403／404／410（user 端點）、refresh 被拒 invalid_grant、列已標記需重新授權 → 確認撤銷 → PurgeUser；
//   200（token 仍有效）、其他 4xx → 「未決」：依 +10 分／+1 小時／+6 小時重驗，三次都仍有效才判定偽造或過期並告警；
//   429／5xx／逾時／解密失敗 → 暫時性：一般退避重試，不刪；
//   連線屬舊 App 世代（無法實測）→ 不動資料。
// 權限異動（userPermissionsChange）：不信推送內容，一律以 API 取「當下權限」更新連線；暫停只影響卡片橫幅，
// 絕不丟棄活動（R-C2）。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

var garminRecheckDelays = []time.Duration{10 * time.Minute, time.Hour, 6 * time.Hour}

// garminRecheckError：dereg 實測「token 仍有效」→ 稍後重驗（計入事件的 attempts，但不會走到 dead）。
type garminRecheckError struct{ Delay time.Duration }

func (e *garminRecheckError) Error() string {
	return "garmin: deregistration not confirmed, will recheck"
}

// 站內信文案（⚠️ 含 Garmin 字樣：依 License §8.4／計畫 E-8，正式對外前須先取得 Garmin 書面核准；白名單內測期間只會寄給測試帳號）。
const (
	garminMailDeregTitle = "Garmin 連線已中斷"
	garminMailDeregBody  = "你已在 Garmin Connect 移除 DOR 的授權，DOR 已停止接收你的 Garmin 紀錄，並刪除已匯入的 Garmin 活動紀錄（賽事成績會重算；已發放的獎勵不會收回）。如需重新連接，請到「會員管理 → 運動數據」。"
	garminMailPauseTitle = "Garmin 資料分享已關閉"
	garminMailPauseBody  = "你已在 Garmin Connect 關閉資料分享，DOR 不會收到新的 Garmin 紀錄。如需恢復，請在 Garmin Connect 重新開啟分享。"
)

func (h *GarminHandler) mailUser(ctx context.Context, userID, title, body string) {
	if h.mailer == nil || userID == "" {
		return
	}
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := h.mailer.InsertForUsers(mctx, []string{userID}, "normal", title, body, ""); err != nil {
		log.Warn().Err(err).Msg("garmin: send in-app mail failed")
	}
}

// GarminPurgeResult PurgeUser 的結果。
type GarminPurgeResult struct {
	HadConnection     bool
	DeletedActivities int64
	DeletedEvents     int64
	AnonymizedEvents  int64
}

// PurgeUser 清除一位使用者的全部 Garmin 資料：單一交易刪除連線列、**全部** source='garmin' 活動（含舊 Terra 列）、
// 該 Garmin userId 的所有事件列（任何狀態），並把屬於這些活動的 mileage_exp_events 匿名化（去掉距離明細、開始時間與
// 活動連結，保留 EXP／DP／公里帳務），解除因這些列而標記的良性重複，重設偏好來源。
// 不動 external_award_ledger（只存雜湊，防「中斷再重連」重複請領）；不追回已發放的 EXP／DP／total_km。
// 這是中斷連線、deregistration、刪帳號 SOP 共用的唯一入口；冪等。
// 取得與事件處理相同的使用者鎖（行程內互斥＋Postgres advisory lock），清理與處理中的 goroutine 不會競態。
func (h *GarminHandler) PurgeUser(ctx context.Context, userID string) (GarminPurgeResult, error) {
	var zero GarminPurgeResult
	c, err := h.store.GetGarminByUser(ctx, userID, false)
	if err != nil {
		return zero, err
	}
	garminUID := ""
	if c != nil && c.Via == garminViaDirect {
		garminUID = c.ProviderUserID
	}
	if garminUID == "" {
		return h.purgeLocked(ctx, userID, "")
	}
	deadline := h.now().Add(h.purgeWait)
	for {
		var res GarminPurgeResult
		var perr error
		unlock := h.userMu.Lock(garminUID)
		lerr := h.store.WithGarminUserLock(ctx, garminUID, func(lctx context.Context) error {
			res, perr = h.purgeLocked(lctx, userID, garminUID)
			return nil
		})
		unlock()
		if lerr == nil {
			return res, perr
		}
		if !errors.Is(lerr, errGarminBusy) || h.now().After(deadline) {
			return zero, lerr
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
}

// purgeLocked：呼叫端已持有該 Garmin 使用者的鎖（事件處理器內部用這個，避免自己鎖自己）。
func (h *GarminHandler) purgeLocked(ctx context.Context, userID, garminUID string) (GarminPurgeResult, error) {
	res, err := h.store.PurgeGarminUser(ctx, userID, garminUID)
	if err != nil {
		return res, err
	}
	if res.HadConnection || res.DeletedActivities > 0 {
		recomputeStandingsAsync(userID) // 刪了活動：競賽分組成績重算（debounce）
	}
	return res, nil
}

// GarminBlockResult BlockUser 的結果。
type GarminBlockResult struct {
	RegistrationDeleted bool // 已通知 Garmin 撤銷註冊（204／401／403／404）
	Purge               GarminPurgeResult
}

// BlockUser 後台封鎖（License §5.3：須能立即限制違規使用者）：①先寫封鎖清單（DOR user_id＋目前連線的 Garmin userId）——
// 之後 /connect 回 403、該 Garmin userId 的推送一律丟棄；②best-effort 呼叫 DELETE registration（仍會呼叫）；③PurgeUser。
// 給後台管理介面（WP-G6/G12，超管限定）呼叫的單一入口；冪等。adminID 可為空字串。
func (h *GarminHandler) BlockUser(ctx context.Context, userID, reason, adminID string) (GarminBlockResult, error) {
	var out GarminBlockResult
	c, err := h.store.GetGarminByUser(ctx, userID, true)
	if err != nil {
		c, err = h.store.GetGarminByUser(ctx, userID, false) // 解密失敗仍要能封鎖：略過向 Garmin 撤銷
		if err != nil {
			return out, err
		}
	}
	garminUID := ""
	if c != nil && c.Via == garminViaDirect {
		garminUID = c.ProviderUserID
	}
	if err := h.store.AddGarminBlock(ctx, userID, garminUID, reason, adminID); err != nil {
		return out, err
	}
	if c != nil && c.Via == garminViaDirect {
		_ = h.kv.Set(ctx, "garmin:selfdereg:"+userID, "1", 5*time.Minute) // 不對被封鎖者發「已中斷」站內信
		if c.AccessToken != "" || c.RefreshToken != "" {
			if cur, ferr := h.ensureFresh(ctx, c, false); ferr == nil && cur.AccessToken != "" {
				dctx, cancel := garminAPITimeout(ctx)
				out.RegistrationDeleted = h.deleteRegistration(dctx, cur.AccessToken) == nil
				cancel()
			}
		}
	}
	out.Purge, err = h.PurgeUser(ctx, userID)
	return out, err
}

// UnblockUser 解除封鎖（之後使用者可重新連接）。回傳是否真的移除了封鎖列。
func (h *GarminHandler) UnblockUser(ctx context.Context, userID string) (bool, error) {
	n, err := h.store.RemoveGarminBlock(ctx, userID)
	return n > 0, err
}

func (h *GarminHandler) isSelfDereg(ctx context.Context, userID string) bool {
	_, ok, _ := h.kv.Get(ctx, "garmin:selfdereg:"+userID)
	return ok
}

// verifyRevoked 以 API 實測連線是否真的已被撤銷。回傳 (confirmed, skip, err)：
// skip＝無法實測且不該動資料（舊 App 世代）；err＝暫時性，呼叫端走一般退避。
func (h *GarminHandler) verifyRevoked(ctx context.Context, conn *garminConn) (confirmed, skip bool, err error) {
	cur, ferr := h.ensureFresh(ctx, conn, false)
	switch {
	case ferr == nil:
	case errors.Is(ferr, errGarminReauth):
		return true, false, nil // refresh 被拒（invalid_grant）或授權早已標記失效：確認撤銷
	case errors.Is(ferr, errGarminGeneration):
		return false, true, nil
	default:
		return false, false, ferr
	}
	actx, cancel := garminAPITimeout(ctx)
	defer cancel()
	perms, perr := h.getPermissions(actx, cur.AccessToken)
	if perr == nil {
		// token 仍有效：順手把權限同步成當下真實狀態（只更新旗標，不動資料）
		_ = h.store.SetGarminScope(ctx, conn.ID, strings.Join(perms, ","))
		return false, false, nil
	}
	if garminIsRevokedErr(perr) {
		return true, false, nil
	}
	var ae *garminAPIError
	if errors.As(perr, &ae) && !garminIsTransientStatus(ae.Status) {
		return false, false, nil // 其他 4xx：未決
	}
	return false, false, perr
}

// processDeregEvent 處理 deregistration 事件（呼叫端已持有使用者鎖）。
func (h *GarminHandler) processDeregEvent(ctx context.Context, ev garminEvent) (string, error) {
	conn, err := h.store.GetGarminDirectByUserID(ctx, ev.ProviderUserID, true)
	if err != nil {
		return "", fmt.Errorf("lookup connection: %w", err)
	}
	if conn == nil {
		return garminResUnknown, nil // 連線已不存在：例如自己中斷後 Garmin 回送的最後一則通知（冪等忽略）
	}
	confirmed, skip, err := h.verifyRevoked(ctx, conn)
	switch {
	case err != nil:
		return "", err
	case skip:
		log.Info().Str("user", prefix8(conn.UserID)).Msg("garmin dereg: connection belongs to a previous app generation, not verifiable, data untouched")
		return "skipped_generation", nil
	case !confirmed:
		if ev.Attempts >= len(garminRecheckDelays) {
			// 三次重驗 token 都仍有效：判定為偽造或過期事件，不刪；告警內容帶小時計數，不只靠 per-kind 節流。
			n := h.count(ctx, "garmin:cnt:deregunconfirmed:"+h.hourBucket(), 1, 2*time.Hour)
			h.alert("garmin_deauth_unconfirmed", "Garmin 撤銷通知無法確認", "撤銷通知經三次 API 重驗 token 仍有效，未刪除任何資料（本小時第 "+strconv.FormatInt(n, 10)+" 次）")
			return "unconfirmed", nil
		}
		return "", &garminRecheckError{Delay: garminRecheckDelays[ev.Attempts]}
	}
	self := h.isSelfDereg(ctx, conn.UserID)
	if _, err := h.purgeLocked(ctx, conn.UserID, conn.ProviderUserID); err != nil {
		return "", fmt.Errorf("purge: %w", err)
	}
	if !self {
		h.mailUser(ctx, conn.UserID, garminMailDeregTitle, garminMailDeregBody)
	}
	return garminResPurged, nil
}

// processPermissionEvent 處理 userPermissionsChange 事件（呼叫端已持有使用者鎖）：不信推送內容，以 API 取當下權限。
func (h *GarminHandler) processPermissionEvent(ctx context.Context, ev garminEvent) (string, error) {
	conn, err := h.store.GetGarminDirectByUserID(ctx, ev.ProviderUserID, true)
	if err != nil {
		return "", fmt.Errorf("lookup connection: %w", err)
	}
	if conn == nil {
		return garminResUnknown, nil
	}
	cur, ferr := h.ensureFresh(ctx, conn, false)
	switch {
	case ferr == nil:
	case errors.Is(ferr, errGarminReauth), errors.Is(ferr, errGarminGeneration):
		return "skipped_unverifiable", nil
	case errors.Is(ferr, errGarminNoConnection):
		return garminResUnknown, nil
	default:
		return "", ferr
	}
	actx, cancel := garminAPITimeout(ctx)
	perms, perr := h.getPermissions(actx, cur.AccessToken)
	cancel()
	if perr != nil {
		if garminIsRevokedErr(perr) {
			_ = h.store.MarkGarminReauth(ctx, conn.ID) // token 已不可用；真正的撤銷由 deregistration 事件處理
			return "skipped_unauthorized", nil
		}
		return "", perr
	}
	wasPaused := conn.paused()
	if err := h.store.SetGarminScope(ctx, conn.ID, strings.Join(perms, ",")); err != nil {
		return "", fmt.Errorf("set scope: %w", err)
	}
	nowPaused := true
	for _, p := range perms {
		if p == garminPermActivityExport {
			nowPaused = false
		}
	}
	switch {
	case nowPaused && !wasPaused:
		h.mailUser(ctx, conn.UserID, garminMailPauseTitle, garminMailPauseBody)
		return "paused", nil
	case !nowPaused && wasPaused:
		return "resumed", nil
	}
	return "unchanged", nil
}
