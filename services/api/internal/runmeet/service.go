package runmeet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
)

// --- 錯誤集中檔首（規格 6.2 的文案表逐條對應；4xx 一律中文，404/500 回英文短句不外洩內部錯誤）---

// apiErr 帶 HTTP status 的錯誤。handler 用 respondAPIErr 一次轉譯，
// 避免每個端點各寫一串 errors.Is 分支（partner 那套在端點少時可行，這裡有 25 個端點會失控）。
type apiErr struct {
	Status int
	Msg    string
}

func (e *apiErr) Error() string { return e.Msg }

func newErr(status int, msg string) *apiErr { return &apiErr{Status: status, Msg: msg} }

var (
	// 入口／權限
	errEntryClosed = newErr(http.StatusForbidden, "團練邀請尚未開放。")
	errNotOwner    = newErr(http.StatusForbidden, "只有發起人可以做這件事。")
	errNotMember   = newErr(http.StatusForbidden, "加入團練後才能留言。")
	errNotFound    = newErr(http.StatusNotFound, "not found")

	// 配額
	errRequiresVIP = newErr(http.StatusForbidden, "發起團練是 VIP 專屬功能。")

	// 私密團密碼
	errLocked          = newErr(http.StatusForbidden, "這是私密團練，請輸入發起人提供的密碼。")
	errPasswordWrong   = newErr(http.StatusForbidden, "團練密碼錯誤，請再確認一次。")
	errPasswordTooMany = newErr(http.StatusTooManyRequests, "密碼錯誤次數過多，請 10 分鐘後再試。")
	errPasswordLen     = newErr(http.StatusBadRequest, "密碼請填 4 到 32 個字元。")

	// 名額／成員
	errMeetFull        = newErr(http.StatusConflict, "這個團練已經滿了。")
	errApproveFull     = newErr(http.StatusConflict, "名額已滿，無法再同意新成員。請先調高人數上限或剔除成員。")
	errApplicationDone = newErr(http.StatusConflict, "這筆申請已經處理過了。")
	errKicked          = newErr(http.StatusForbidden, "你已被移出這個團練，無法再次加入。")
	errPendingFull     = newErr(http.StatusConflict, "待審核的申請已達上限，請稍後再試。")
	errAlreadyJoined   = newErr(http.StatusConflict, "你已經在這個團練裡了。")
	errAlreadyPending  = newErr(http.StatusConflict, "你已經送出過申請，請等發起人回覆。")
	errEnded           = newErr(http.StatusConflict, "這個團練的時間已經過了，無法加入。")
	errMeetClosed      = newErr(http.StatusConflict, "發起人已關閉這個團練。")
	errEditEnded       = newErr(http.StatusConflict, "這個團練的時間已經過了，無法再編輯。請重新發起一個新的團練。")
	// errMeetCancelled 規格用字是「中止」不是「取消」：中止＝停止加入的任何動作（可恢復），
	// 與舊版「取消＝單向終局」語意不同，文案跟著改，避免使用者誤以為不可逆。
	errMeetCancelled  = newErr(http.StatusConflict, "該團練已中止。")
	errOwnerCantLeave = newErr(http.StatusBadRequest, "你是發起人，無法退出自己的團練（請改用關閉或中止）。")
	errNoSuchMember   = newErr(http.StatusConflict, "這位跑者目前不在團練成員名單中。")
	// errBadTransition SetStatus 收到不合法的狀態轉換（見 model.go CanTransition）。
	// 不細分「為什麼不行」（例如 cancelled→closed vs 同狀態互轉）：呼叫端本來就是自己指定
	// 要轉去哪個狀態，這裡只需告知「不行」，不需要額外資訊。
	errBadTransition = newErr(http.StatusConflict, "目前狀態無法這樣切換。")

	// 內容驗證
	errTooLong        = newErr(http.StatusBadRequest, "內容超過長度上限。")
	errTitleLen       = newErr(http.StatusBadRequest, "團練名稱請填 2 到 40 個字。")
	errRegionLen      = newErr(http.StatusBadRequest, "縣市・行政區請填 2 到 30 個字。")
	errPlaceLabelLen  = newErr(http.StatusBadRequest, "地點名稱請填 2 到 60 個字。")
	errMeetingDetail  = newErr(http.StatusBadRequest, "集合細節最多 200 字。")
	errDescriptionLen = newErr(http.StatusBadRequest, "說明最多 500 字。")
	errMeetAtPast     = newErr(http.StatusBadRequest, "預計時間必須晚於現在。")
	errMeetAtFar      = newErr(http.StatusBadRequest, "預計時間最多只能設定到 90 天後。")
	errBadCoord       = newErr(http.StatusBadRequest, "座標格式不正確。")
	errImageSource    = newErr(http.StatusBadRequest, "圖片來源不正確，請重新上傳。")
	errCommentEmpty   = newErr(http.StatusBadRequest, "留言不能空白。")
	errCommentLen     = newErr(http.StatusBadRequest, "留言最多 200 字。")
	errCommentDup     = newErr(http.StatusBadRequest, "請勿重複張貼相同內容。")
	errCommentFast    = newErr(http.StatusTooManyRequests, "留言太快了，休息一下再發吧。")
	errCommentCap     = newErr(http.StatusTooManyRequests, "今天的留言則數已達上限，明天再來吧。")
	errBadReaction    = newErr(http.StatusBadRequest, "不支援這個心情。")
	errBadJSON        = newErr(http.StatusBadRequest, "invalid json")
	errBadID          = newErr(http.StatusBadRequest, "invalid id")
	errServer         = newErr(http.StatusInternalServerError, "failed")
)

// errCapacityBelowMembers 動態訊息（要帶目前人數），不是常數。
func errCapacityBelowMembers(n int) *apiErr {
	return newErr(http.StatusBadRequest, fmt.Sprintf("目前已有 %d 位成員，人數上限不可低於 %d 人。", n, n))
}

// errCapacityRange 動態訊息（上限來自系統設定 runmeet_capacity_max）。
func errCapacityRange(max int) *apiErr {
	return newErr(http.StatusBadRequest, fmt.Sprintf("人數上限請填 2 到 %d 人。", max))
}

// errQuotaUsedUp 規格 6.2：非 VIP 版多一句升級引導。
//
// ⚠️ vipCap 必須由呼叫端從 app_settings（runmeet_quota_vip，後台可調 1..50）帶進來，
// 不得寫死 10——營運把它調成 5 之後，寫死的文案會向非 VIP 承諾拿不到的權益，直接引發客訴。
func errQuotaUsedUp(cap int, isVIP bool, resetMonth time.Month, vipCap int) *apiErr {
	if isVIP {
		return newErr(http.StatusConflict,
			fmt.Sprintf("本月發起次數已用完（%d/%d），下個月 1 日重置。", cap, cap))
	}
	if vipCap <= cap {
		// VIP 上限沒比較高（營運把兩者調成一樣）→ 不做假的升級引導
		return newErr(http.StatusConflict,
			fmt.Sprintf("本月發起次數已用完（%d/%d），將於 %d 月 1 日重置。", cap, cap, int(resetMonth)))
	}
	return newErr(http.StatusConflict,
		fmt.Sprintf("本月發起次數已用完（%d/%d），將於 %d 月 1 日重置。升級 VIP 每月可發起 %d 次。",
			cap, cap, int(resetMonth), vipCap))
}

// errImageOverLimit 動態訊息（張數來自建立當下的 image_limit 快照）。
//
// ⚠️ vipLimit 由呼叫端從 app_settings（runmeet_images_vip，後台可調 1..4）帶進來，不得寫死 4。
// vipLimit <= limit（含 repository 層拿不到設定時傳 0）→ 只講這個團的上限，不做假的升級引導。
func errImageOverLimit(limit, vipLimit int) *apiErr {
	if vipLimit <= limit {
		return newErr(http.StatusBadRequest, fmt.Sprintf("這個團練最多可放 %d 張圖片。", limit))
	}
	return newErr(http.StatusBadRequest,
		fmt.Sprintf("這個團練最多可放 %d 張圖片。VIP 每個團練可上傳 %d 張。", limit, vipLimit))
}

// --- 系統設定 key 與預設值（規格 1.7；migration 156 已種子）---

const (
	EntryStateKey     = "runmeet_entry_state"
	EntryWhitelistKey = "runmeet_entry_whitelist"

	keyRequiresVIP         = "runmeet_create_requires_vip"
	keyQuotaNormal         = "runmeet_quota_normal"
	keyQuotaVIP            = "runmeet_quota_vip"
	keyImagesNormal        = "runmeet_images_normal"
	keyImagesVIP           = "runmeet_images_vip"
	keyCapacityMax         = "runmeet_capacity_max"
	keyPendingMax          = "runmeet_pending_max"
	keyCommentDailyCap     = "runmeet_comment_daily_cap"
	keyRejectCooldownHours = "runmeet_reject_cooldown_hours"
	keyEndedVisibleDays    = "runmeet_ended_visible_days"
)

const (
	defQuotaNormal         = 1
	defQuotaVIP            = 10
	defImagesNormal        = 1
	defImagesVIP           = 4
	defCapacityMax         = 50
	defPendingMax          = 50
	defCommentDailyCap     = 100
	defRejectCooldownHours = 24
	defEndedVisibleDays    = 90

	// maxMeetAtDays 預計時間最遠 90 天（規格 6.2「預計時間最多只能設定到 90 天後」）。
	maxMeetAtDays = 90
	// commentMinIntervalSec 同一人連續兩則留言的最小間隔。
	commentMinIntervalSec = 3
	// endedCommentDays 已結束後仍可留言/心情的天數（規格 5.6），超過即唯讀。
	endedCommentDays = 7
	// excerptRunes 列表卡片摘要長度。
	excerptRunes = 60
)

// Settings 一次讀齊本套件用到的可調參數（每個端點只查一輪，避免逐筆重查 app_settings）。
type Settings struct {
	RequiresVIP         bool
	QuotaNormal         int
	QuotaVIP            int
	ImagesNormal        int
	ImagesVIP           int
	CapacityMax         int
	PendingMax          int
	CommentDailyCap     int
	RejectCooldownHours int
	EndedVisibleDays    int
}

func loadSettings(ctx context.Context, db *pgxpool.Pool) Settings {
	return Settings{
		RequiresVIP:         appsettings.GetInt(ctx, db, keyRequiresVIP, 0) == 1,
		QuotaNormal:         appsettings.GetInt(ctx, db, keyQuotaNormal, defQuotaNormal),
		QuotaVIP:            appsettings.GetInt(ctx, db, keyQuotaVIP, defQuotaVIP),
		ImagesNormal:        appsettings.GetInt(ctx, db, keyImagesNormal, defImagesNormal),
		ImagesVIP:           appsettings.GetInt(ctx, db, keyImagesVIP, defImagesVIP),
		CapacityMax:         appsettings.GetInt(ctx, db, keyCapacityMax, defCapacityMax),
		PendingMax:          appsettings.GetInt(ctx, db, keyPendingMax, defPendingMax),
		CommentDailyCap:     appsettings.GetInt(ctx, db, keyCommentDailyCap, defCommentDailyCap),
		RejectCooldownHours: appsettings.GetInt(ctx, db, keyRejectCooldownHours, defRejectCooldownHours),
		EndedVisibleDays:    appsettings.GetInt(ctx, db, keyEndedVisibleDays, defEndedVisibleDays),
	}
}

// --- 圖片引用驗證（規格 1.3 的「第二條 XSS/追蹤路徑」）---

// imgURLRE image_urls 每個元素必須長成這樣。
//
// ⚠️ 比既有 partner_shops.photo_urls（允許任意 URL）嚴格是刻意的：那邊是後台管理員填的，
// 這邊是 UGC。next.config.mjs 的 CSP 是 img-src 'self' data: blob: https:，不擋等於允許
// 發起人埋 tracking pixel 蒐集所有瀏覽者 IP；也等於 avatar_url 現況那個零驗證缺口的放大版。
// 不合格一律**拒收整個請求**，不要靜默清空（靜默清空會讓使用者以為圖片存好了）。
var imgURLRE = regexp.MustCompile(`^/api/v1/images/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidImageURL 純函式版（供單元測試）。格式檢查通過後，呼叫端仍須確認該 image id
// 屬於當前使用者且 purpose='runmeet'（見 Repository.VerifyImageOwnership）——防盜連他人上傳或後台圖。
func ValidImageURL(u string) bool { return imgURLRE.MatchString(u) }

// imageIDFromURL 從合法的 image URL 取出 uuid（呼叫前請先過 ValidImageURL）。
func imageIDFromURL(u string) string {
	const prefix = "/api/v1/images/"
	if len(u) <= len(prefix) {
		return ""
	}
	return u[len(prefix):]
}

// --- 建立／編輯輸入 ---

// MeetInput 建立與編輯共用的輸入（已正規化後的值）。
//
// ⚠️ 硬規則：本套件所有請求 body/query 的解析結構中**不得出現 user_id 欄位**——
// uid 一律取自 r.Context().Value(auth.CtxKeyUserID)。路徑 {uid} 只用於「指定操作對象」，
// 且每次先驗 run_meets.owner_id = ctxUID。
type MeetInput struct {
	Title            string    `json:"title"`
	MeetAt           time.Time `json:"meet_at"`
	Region           string    `json:"region"`
	PlaceLabel       string    `json:"place_label"`
	Lat              *float64  `json:"lat"`
	Lng              *float64  `json:"lng"`
	MeetingDetail    string    `json:"meeting_detail"`
	Capacity         int       `json:"capacity"`
	Description      string    `json:"description"`
	ImageURLs        []string  `json:"image_urls"`
	ApprovalRequired bool      `json:"approval_required"`
	Password         *string   `json:"password"` // 建立：非空＝私密團；編輯：nil=不動、""=移除密碼、其他=重設
	ClientToken      string    `json:"client_token"`
}

// validateMeetInput 正規化 + 驗證（不碰 DB 的部分）。imageLimit 是「建立當下的快照」或
// 既有 run_meets.image_limit（編輯時），由呼叫端決定要傳哪一個；vipImages 只用於錯誤文案
// （runmeet_images_vip，後台可調），傳 0 表示不做升級引導。
func validateMeetInput(in *MeetInput, now time.Time, capacityMax, imageLimit, vipImages int) error {
	var err error

	// ⚠️ image_urls 在 DB 是 TEXT[] NOT NULL DEFAULT '{}'，但 pgx v5 把 Go 的 nil slice 編成
	// SQL NULL（不是空陣列）→ 省略欄位或送 "image_urls": null 會撞 23502 變成 500。
	// 在這裡補成空陣列，Create/Update 兩條路徑都吃得到。
	if in.ImageURLs == nil {
		in.ImageURLs = []string{}
	}

	if in.Title, err = normalizeText(in.Title, MaxTitleRunes, false); err != nil || runeLen(in.Title) < MinTitleRunes {
		return errTitleLen
	}
	if in.Region, err = normalizeText(in.Region, MaxRegionRunes, false); err != nil || runeLen(in.Region) < MinRegionRunes {
		return errRegionLen
	}
	if in.PlaceLabel, err = normalizeText(in.PlaceLabel, MaxPlaceLabelRunes, false); err != nil || runeLen(in.PlaceLabel) < MinPlaceLabelRunes {
		return errPlaceLabelLen
	}
	if in.MeetingDetail, err = normalizeText(in.MeetingDetail, MaxMeetingDetailRunes, true); err != nil {
		return errMeetingDetail
	}
	if in.Description, err = normalizeText(in.Description, MaxDescriptionRunes, true); err != nil {
		return errDescriptionLen
	}

	if in.MeetAt.IsZero() || !in.MeetAt.After(now) {
		return errMeetAtPast
	}
	if in.MeetAt.After(now.Add(maxMeetAtDays * 24 * time.Hour)) {
		return errMeetAtFar
	}

	if capacityMax < 2 {
		capacityMax = defCapacityMax
	}
	if in.Capacity < 2 || in.Capacity > capacityMax {
		return errCapacityRange(capacityMax)
	}

	// lat/lng 必須同時有或同時無（與 migration 的 run_meets_latlng_chk 一致）
	if (in.Lat == nil) != (in.Lng == nil) {
		return errBadCoord
	}
	if in.Lat != nil && !validCoord(*in.Lat, *in.Lng) {
		return errBadCoord
	}

	if len(in.ImageURLs) > imageLimit {
		return errImageOverLimit(imageLimit, vipImages)
	}
	for _, u := range in.ImageURLs {
		if !ValidImageURL(u) {
			return errImageSource
		}
	}

	if in.Password != nil && *in.Password != "" {
		n := runeLen(*in.Password)
		if n < 4 || n > 32 {
			return errPasswordLen
		}
	}
	return nil
}

func runeLen(s string) int { return len([]rune(s)) }

// joinStatusBlocks status → 該狀態擋加入時要用哪個既有錯誤物件（套件層級單例指標）。
//
// ⚠️ 這裡刻意用一個共用的 map，而不是讓 joinBlockReason 和 meetStatusError 各自寫一份
// switch：兩邊都要回同一個 *apiErr 單例（errors.Is 靠指標相等比對，newErr 現造一個新指標
// 會讓既有的 errors.Is(err, errMeetClosed) 全部失效），單一資料來源才不會兩邊漂移。
var joinStatusBlocks = map[string]*apiErr{
	StatusClosed:    errMeetClosed,
	StatusCancelled: errMeetCancelled,
}

// joinBlockReason 依 status 本身回「不能加入」的文案（""＝這個狀態本身不擋，是否已過期
// 由呼叫端另外用 isEnded 判斷，見 meetStatusError）。純函式，可單元測試。
func joinBlockReason(status string) string {
	if e, ok := joinStatusBlocks[status]; ok {
		return e.Msg
	}
	return ""
}

// meetStatusError 把「這個團練現在不能被加入/留言/編輯」的原因轉成對應錯誤物件——
// handler/repository 一律呼叫這裡，不要各自 if status == "closed" 散落判斷。
// isEnded 由 meet_at <= now 判定（過期是查詢條件，不是狀態欄位——刻意不開排程改 status）。
func meetStatusError(status string, isEnded bool) error {
	if e, ok := joinStatusBlocks[status]; ok {
		return e
	}
	if isEnded {
		return errEnded
	}
	return nil
}

// errIsNotFound 供 handler 判斷是否該回 404（下架/軟刪/不存在一律 404，不外洩差異）。
func errIsNotFound(err error) bool {
	var e *apiErr
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}
