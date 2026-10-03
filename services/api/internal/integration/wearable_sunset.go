package integration

// Terra／Strava 串接結束公告（announce）的守門共用件。狀態來源與語意見 wearablesunset 套件；這裡只放 integration
// 套件自己的部分：handler 持有的 Source、被擋下時的 409 回應、announce 期間「只更新既有列、絕不新建」的 Terra 落地。
//
// 規則（擁有者決定，2026-10-31 結束）：announce 起不得再建立任何新的 Terra／Strava 連接，也不准既有使用者重新授權
// （所有「連接」入口一律擋）；既有連線照常同步，已存在的連線列一律不動。因此：
//   - API／瀏覽器入口（Terra connect／callback、Strava connect／callback）：整個擋下。API 回 409＋code
//     （wearablesunset.RefusalStatus），瀏覽器導回前台帶 ?strava=sunset／?terra=sunset。409 不是 5xx，不會進
//     「API 5xx 激增」告警，也不會被每日資安報告算成登入失敗（401／403 會，見 RefusalStatus 的說明）。
//   - Terra webhook：一律先 ack（Terra 只看 HTTP 狀態、背景才處理，所以不會因為這裡不處理而無限重送）。
//     auth 事件（＝使用者在 Terra 完成一次連接／重新授權）在 announce 期間整個不落地、只記一行 Info，連既有列都不改寫；
//     activity 保底建列與 user_reauth 只「更新既有列」（沒有既有列就略過），讓既有連線的 Terra user id 換新後仍能自癒，
//     詳見 persistTerraConn。
//   - 其餘（Strava webhook／sync／backfill／disconnect、Terra import／disconnect／status）完全不變。

import (
	"context"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/integration/wearablesunset"
)

// defaultSunsetSource handler 沒有被明確注入 Source 時的預設：repo 帶有資料庫（正式環境）就讀 app_settings，
// 否則（單元測試的 repo=nil）恆 off。這樣 main.go 不必另外接線，也就不會有「忘了接線、守門永遠不生效」的洞；
// 守門測試（wearable_sunset_guard_test.go）會確認兩個建構子都呼叫了它。
func defaultSunsetSource(repo *Repository) wearablesunset.Source {
	if repo == nil {
		return wearablesunset.FromSettings(nil)
	}
	return wearablesunset.FromSettings(repo.db)
}

// sunsetInfoOf 讀目前狀態；src 為 nil（直接以 struct 字面值建出的 handler）視為 off。
func sunsetInfoOf(ctx context.Context, src wearablesunset.Source) wearablesunset.Info {
	if src == nil {
		return wearablesunset.Info{State: wearablesunset.StateOff}
	}
	return src.Current(ctx)
}

// refuseNewConnection announce 期間對 API 呼叫回 wearablesunset.RefusalStatus（409）＋{error,code:"wearable_sunset",
// state,date} 並回 true（呼叫端直接 return）；off 回 false、不寫任何東西。所有「發起連接」的 API 入口都必須在做任何事
// 之前呼叫它——包含檢查 enabled()（503）之前，這樣即使日後移除憑證環境變數也仍是 4xx。
// 狀態碼為什麼不是 403：見 wearablesunset.RefusalStatus 的說明（401／403 會被每日資安報告算成登入失敗）。
func refuseNewConnection(w http.ResponseWriter, info wearablesunset.Info) bool {
	if !info.Announcing() {
		return false
	}
	respondJSON(w, wearablesunset.RefusalStatus, info.ErrorBody())
	return true
}

// SetSunset 注入公告狀態來源（測試用；正式環境 NewTerraHandler 已預設讀 app_settings）。
func (h *TerraHandler) SetSunset(src wearablesunset.Source) { h.sunset = src }

// SetSunset 注入公告狀態來源（測試用；正式環境 NewStravaHandler 已預設讀 app_settings）。
func (h *StravaHandler) SetSunset(src wearablesunset.Source) { h.sunset = src }

func (h *TerraHandler) sunsetInfo(ctx context.Context) wearablesunset.Info {
	return sunsetInfoOf(ctx, h.sunset)
}

// newConnectionsPaused announce 期間為 true：不再開放新的 Terra 連接。
func (h *TerraHandler) newConnectionsPaused(ctx context.Context) bool {
	return h.sunsetInfo(ctx).Announcing()
}

func (h *StravaHandler) sunsetInfo(ctx context.Context) wearablesunset.Info {
	return sunsetInfoOf(ctx, h.sunset)
}

// newConnectionsPaused announce 期間為 true：不再開放新的 Strava 連接。
func (h *StravaHandler) newConnectionsPaused(ctx context.Context) bool {
	return h.sunsetInfo(ctx).Announcing()
}

// persistTerraConn 是 Terra 連線列「落地」的唯一入口：Callback、auth 事件、activity 保底建列、user_reauth 四條路徑
// 都必須經過這裡，go/ast 守門測試要求 SaveTerraUnlessDirect 只能在這個函式內被呼叫——日後新增任何建列路徑
// 只要沿用這個入口就自動受 announce 管轄。
//   - off：SaveTerraUnlessDirect（upsert，與改版前完全相同）。
//   - announce：UpdateTerraConnIfExists——只更新既有的 via='terra' 列，沒有既有列就什麼都不做（saved=false），
//     由資料庫的單一 UPDATE 保證絕不會 INSERT，沒有「先查後寫」的競態。既有連線的 Terra user id 換新
//     （user_reauth、activity 保底自癒）因此仍然有效，活動不會因為這個公告而掉資料。
//     （announce 期間 auth 事件在 handleAuthEvent 開頭就被略過；瀏覽器 Callback 在開頭就被擋下，只有「開頭檢查通過、
//     落地前剛好切到 announce」的極小競態才會走到這裡，此時同樣只更新既有列。）
func (h *TerraHandler) persistTerraConn(ctx context.Context, c *Connection) (saved bool, err error) {
	store := h.connStore()
	if h.newConnectionsPaused(ctx) {
		updated, err := store.UpdateTerraConnIfExists(ctx, c)
		if err == nil && !updated {
			log.Info().Str("provider", c.Provider).Str("user", prefix8(c.UserID)).
				Msg("terra: new connection not created (wearable sunset announce); no existing terra connection to update")
		}
		return updated, err
	}
	return store.SaveTerraUnlessDirect(ctx, c)
}

// terraConnStore persistTerraConn 用到的兩個寫入動作；*Repository 就是正式實作。
type terraConnStore interface {
	SaveTerraUnlessDirect(ctx context.Context, c *Connection) (saved bool, err error)
	UpdateTerraConnIfExists(ctx context.Context, c *Connection) (updated bool, err error)
}

func (h *TerraHandler) connStore() terraConnStore {
	if h.store != nil {
		return h.store
	}
	return h.repo
}

// UpdateTerraConnIfExists 只更新「既有」的 Terra 連線列（via='terra'），絕不 INSERT：回 updated=true 代表有列被更新；
// 沒有這個 (user,provider) 的列、或該列是直連（via='direct'）都回 false。欄位語意與 SaveTerraUnlessDirect 的更新分支相同
// （token 恆空、via 維持 terra、不動 created_at＝匯入 floor）。
func (r *Repository) UpdateTerraConnIfExists(ctx context.Context, c *Connection) (updated bool, err error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE user_integrations SET
			provider_user_id = $3,
			access_token     = '',
			refresh_token    = '',
			expires_at       = $4,
			scope            = $5,
			via              = 'terra',
			updated_at       = NOW()
		WHERE user_id = $1 AND provider = $2 AND via = 'terra'`,
		c.UserID, c.Provider, c.ProviderUserID, c.ExpiresAt, c.Scope)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
