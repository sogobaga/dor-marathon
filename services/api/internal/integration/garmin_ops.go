package integration

// 營運：日報「直連手錶」段的統計（純 DB＋Redis 計數，只含人數／筆數，**不呼叫 Garmin**、不含任何使用者識別）與每日維護。
//
// MaintainDaily 掛在既有的每日報告窗口（ops.DirectWearableProvider；不新增排程）：
//   1. 事件保存期限清理（done 14 天、dead／error 與久未處理的 pending 30 天）；
//   2. 補掃到期事件（第三個掃描時機；沒有 ticker）；
//   3. token 保活：refresh token 將在 14 天內到期者（最多 50 位）強制刷新一次，只打 token 端點。
// 任何一步失敗只回報錯誤、不影響其他步驟，也不影響日報本身（呼叫端只記 log）。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/dor/api/internal/ops"
)

const (
	garminMaintainKeepaliveLimit  = 50
	garminMaintainKeepaliveWindow = 14 * 24 * time.Hour
)

var _ ops.DirectWearableProvider = (*GarminHandler)(nil)

// DirectStats 實作 ops.DirectWearableProvider：日報「直連手錶」段的統計。
func (h *GarminHandler) DirectStats(ctx context.Context) (ops.DirectWearableStats, error) {
	st := ops.DirectWearableStats{Provider: garminProvider}
	cs, err := h.store.GarminConnStats(ctx, h.generation())
	if err != nil {
		return st, err
	}
	es, err := h.store.GarminEventStats(ctx)
	if err != nil {
		return st, err
	}
	st.Connected, st.Active24h, st.NeedsReauth, st.Paused, st.Stale3d = cs.Connected, cs.Active24h, cs.NeedsReauth, cs.Paused, cs.Stale3d
	st.Events24h, st.Imported24h, st.PendingStale, st.Dead = es.Received24h, es.Imported24h, es.PendingStale, es.Dead
	st.UnknownUser24h = h.sumCounter(ctx, "garmin:cnt:unknown:", 24)
	if cs.PausedButActive > 0 {
		st.Notes = append(st.Notes, "已暫停但 24 小時內仍有推送："+strconv.Itoa(cs.PausedButActive)+" 人")
	}
	if es.Skipped24h > 0 {
		st.Notes = append(st.Notes, "24 小時內略過的活動事件："+strconv.Itoa(es.Skipped24h)+" 筆")
	}
	if n := h.sumCounter(ctx, "garmin:cnt:skiptype:", 24); n > 0 {
		st.Notes = append(st.Notes, "24 小時內非跑走健行類型（未落地）："+strconv.Itoa(n)+" 筆")
	}
	if n := h.sumDaily(ctx, "garmin:cnt:truncated:", 2); n > 0 {
		st.Notes = append(st.Notes, "推送內容被截斷而拒收（要求 Garmin 重送）：近 2 日 "+strconv.Itoa(n)+" 次")
	}
	return st, nil
}

// sumCounter 加總最近 hours 個小時桶（key 前綴＋yyyymmddHH）的計數器；Redis 不可用回目前已加總的值。
func (h *GarminHandler) sumCounter(ctx context.Context, prefix string, hours int) int {
	now := h.now().UTC()
	keys := make([]string, 0, hours)
	for i := 0; i < hours; i++ {
		keys = append(keys, prefix+now.Add(-time.Duration(i)*time.Hour).Format("2006010215"))
	}
	return h.sumKeys(ctx, keys)
}

// sumDaily 加總最近 days 個日桶（key 前綴＋yyyymmdd）的計數器。
func (h *GarminHandler) sumDaily(ctx context.Context, prefix string, days int) int {
	now := h.now().UTC()
	keys := make([]string, 0, days)
	for i := 0; i < days; i++ {
		keys = append(keys, prefix+now.AddDate(0, 0, -i).Format("20060102"))
	}
	return h.sumKeys(ctx, keys)
}

func (h *GarminHandler) sumKeys(ctx context.Context, keys []string) int {
	total := 0
	for _, key := range keys {
		v, ok, err := h.kv.Get(ctx, key)
		if err != nil {
			return total
		}
		if ok {
			if n, perr := strconv.Atoi(v); perr == nil {
				total += n
			}
		}
	}
	return total
}

// MaintainDaily 每日維護；回傳一行只含數字的摘要。
func (h *GarminHandler) MaintainDaily(ctx context.Context) (string, error) {
	var errs []error
	purged, err := h.store.PurgeGarminEvents(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("purge events: %w", err))
	}
	swept := h.SweepPending(ctx)

	kept, keptFail := 0, 0
	cands, cerr := h.store.GarminKeepaliveCandidates(ctx, h.generation(), garminMaintainKeepaliveWindow, garminMaintainKeepaliveLimit)
	if cerr != nil {
		errs = append(errs, fmt.Errorf("keepalive candidates: %w", cerr))
	}
	for _, c := range cands {
		if ctx.Err() != nil {
			break
		}
		// 與事件驅動保活共用 24 小時冷卻旗標：今天已保活過就略過
		if ok, kerr := h.kv.SetNX(ctx, "garmin:ka:"+c.ID, "1", garminKeepaliveCooldown); kerr != nil || !ok {
			continue
		}
		if _, ferr := h.ensureFresh(ctx, c, true); ferr != nil {
			keptFail++
		} else {
			kept++
		}
	}
	summary := fmt.Sprintf("Garmin 維護：清理過期事件 %d 筆、補掃到期事件 %d 筆、token 保活成功 %d／失敗 %d 位", purged, swept, kept, keptFail)
	return summary, errors.Join(errs...)
}
