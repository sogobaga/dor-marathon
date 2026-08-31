// Package runmeet 團練邀請：會員自行發起「揪人一起去跑步」的聚會，其他會員可探索/加入/留言/心情。
//
// ⚠️ 命名（規格 1.6）：資料表/API/套件一律 run_meet_*（英文語意＝「跑步聚會」），
// 禁用 team/group/group_key——「跑團分組」(race_groups.group_key) 是既有的賽事內分組功能，
// 撞名會誤觸 .claude/workflows/security-audit.js 的外洩欄位規則。
// ⚠️ 中文顯示文案一律用「團練」，不得出現「跑團」二字（避免與賽事「跑團分組」混淆）。
//
// ⚠️ 地點三層揭露（本套件最重要的資安不變式）：
//
//	公開層 region / place_label       → 所有過入口閘門的登入者
//	成員層 lat / lng / meeting_detail  → 發起人、status='joined' 的成員、後台
//
// 分層是靠**不同的回應 struct**（CardView / PublicDetailView / MemberDetailView）做到的，
// 不是靠前端隱藏、也不是回零值：未加入者拿到的 JSON **根本不含** lat/lng/meeting_detail 三個 key。
// 專案有前例教訓——review_note 與測試白名單 email 都是「前端沒顯示但 API 照吐」而外洩。
// 新增回應欄位時務必先確認它該落在哪一層，不要為了省事把 MemberDetailView 拿去餵給非成員。
package runmeet

import "time"

// --- 狀態常數 ---

const (
	StatusOpen      = "open"
	StatusClosed    = "closed"
	StatusCancelled = "cancelled"
)

const (
	MemberPending  = "pending"
	MemberJoined   = "joined"
	MemberRejected = "rejected"
	MemberKicked   = "kicked"
	MemberLeft     = "left"
)

// ReactionKinds 五種心情（與 migration 156 的 rmr_kind_chk 同步維護）。
// 顯示：👍 讚 / 🔥 熱血 / 💪 一起跑 / 🙏 加油 / ❤️ 喜歡
var ReactionKinds = map[string]bool{
	"like": true, "fire": true, "muscle": true, "pray": true, "heart": true,
}

// --- 內部資料列（repository 掃出來的原始欄位，含成員層敏感欄位；**不得**直接序列化回前台）---

type meetRow struct {
	ID               string
	OwnerID          string
	Title            string
	MeetAt           time.Time
	Region           string
	PlaceLabel       string
	Lat              *float64 // 成員層
	Lng              *float64 // 成員層
	MeetingDetail    string   // 成員層
	Capacity         int
	Description      string
	ImageURLs        []string
	ImageLimit       int
	ApprovalRequired bool
	IsPrivate        bool // 由 join_password_hash IS NOT NULL 推導；hash 本身永不離開 repository
	MemberCount      int
	PendingCount     int
	Status           string
	HiddenByAdmin    bool
	HiddenByOwner    bool // migration 158；發起人自行隱藏，可逆，與 HiddenByAdmin 分離（發起人不得自行解除後者）
	HiddenReason     string
	CommentCount     int
	ReactionCount    int
	CreatedAt        time.Time
	UpdatedAt        time.Time

	// 隨查詢帶出的「觀看者視角」欄位
	OwnerName   string
	OwnerAvatar string
	MyStatus    *string // run_meet_members.status（NULL＝從未申請）
	MyReaction  *string
	Unlocked    bool // run_meet_access 有票證

	// 附近搜尋用（僅在 near 查詢時填；**不得**輸出精確值，只轉成 distance_band）
	distanceM float64
}

// --- 對外 DTO ---

// OwnerView 發起人公開身分。名字一律 COALESCE(NULLIF(u.name,”), u.handle)
// （memory display-name-convention：玩家可見名字禁讀 user_profiles.nickname）。
// 絕不含 account_code / email / is_vip（memory account-code-privacy；VIP 狀態外洩會讓 VIP 成為詐騙目標）。
type OwnerView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// CardView 列表卡片＋詳情共用的公開層。**地點只有 region / place_label 兩個公開欄位。**
type CardView struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	MeetAt           time.Time `json:"meet_at"`
	Region           string    `json:"region"`
	PlaceLabel       string    `json:"place_label"`
	Capacity         int       `json:"capacity"`
	MemberCount      int       `json:"member_count"`
	IsPrivate        bool      `json:"is_private"`
	ApprovalRequired bool      `json:"approval_required"`
	Excerpt          string    `json:"excerpt"`   // 60 字摘要（swrCache 單筆 100KB 上限，列表不給完整 description）
	CoverURL         *string   `json:"cover_url"` // 私密團且未解鎖時為 null
	Status           string    `json:"status"`    // open|closed|cancelled
	IsEnded          bool      `json:"is_ended"`  // meet_at <= NOW()
	Owner            OwnerView `json:"owner"`
	MyState          string    `json:"my_state"` // none|pending|joined|rejected|kicked|left|owner
	ReactionCount    int       `json:"reaction_count"`
	CommentCount     int       `json:"comment_count"`
	MyReaction       *string   `json:"my_reaction"`
	HasAccess        bool      `json:"has_access"`
	// DistanceBand 只在「附近搜尋」時出現，且**只有分級字串**（lt1|1to3|3to5|5to10|gt10）。
	// ⚠️ 絕不回精確距離：回 0.23 km 這種值可讓攻擊者換多組座標查詢、三角定位反推出精確地點，
	// 等於繞過整套地點分層設計。排序用精確距離（後端記憶體內），輸出只給 band。
	DistanceBand string `json:"distance_band,omitempty"`
	// HiddenByOwner 發起人管理面板用：目前是否被自己隱藏。只有 isOwner || isAdmin 視角會填真值
	// （比照下面 PendingCount 的既有模式：非 owner 恆 false），因為這欄位的唯一用途是「發起人
	// 要知道自己按過隱藏沒」，不是給一般訪客判斷用的公開狀態——一般訪客本來就看不到被隱藏的團
	// （見 repository.GetMeet），沒有機會走到這個欄位有意義的分支。
	HiddenByOwner bool `json:"hidden_by_owner"`
}

// detailBase 詳情的公開部分（完整說明與圖片；需通過私密團解鎖才拿得到）。
type detailBase struct {
	CardView
	Description  string   `json:"description"`
	ImageURLs    []string `json:"image_urls"`
	ImageLimit   int      `json:"image_limit"`
	PendingCount int      `json:"pending_count"` // 只有 owner 視角有意義；非 owner 恆 0
	CanComment   bool     `json:"can_comment"`   // joined/owner 且未超過「結束 7 天」唯讀期
}

// PublicDetailView 未加入者（含已解鎖但尚未加入、申請中 pending）看到的詳情。
// ⚠️ 結構上就沒有 Lat/Lng/MeetingDetail 三個欄位——序列化出來的 JSON 不會有這些 key。
type PublicDetailView struct {
	detailBase
	// LocationLocked 固定 true，前端據此顯示「成功加入後才會顯示完整詳細地點」並且不載入地圖。
	LocationLocked bool   `json:"location_locked"`
	LocationNote   string `json:"location_note"`
}

// MemberDetailView 發起人／已加入成員／後台看到的詳情：多出成員層三個欄位。
type MemberDetailView struct {
	detailBase
	LocationLocked bool     `json:"location_locked"` // 固定 false
	Lat            *float64 `json:"lat"`
	Lng            *float64 `json:"lng"`
	MeetingDetail  string   `json:"meeting_detail"`
}

// MemberView 成員項（欄位白名單，多一個都不行）。
// ⚠️ 絕對禁止（含發起人看申請者的視角）：account_code、email、phone、real_name、nickname、
// birthday、address、emergency_contact、vip_expires_at／is_vip、password_hash。
type MemberView struct {
	UserID    string  `json:"user_id"`
	Name      string  `json:"name"`
	AvatarURL string  `json:"avatar_url"`
	IsOwner   bool    `json:"is_owner"`
	Status    string  `json:"status"`     // joined|pending
	ApplyNote string  `json:"apply_note"` // 只有 pending 清單才有值
	JoinedAt  *string `json:"joined_at"`
	AppliedAt string  `json:"applied_at"`
}

// CommentView 留言（同樣只給顯示名與頭像）。migration 159：升級成討論串——
// ParentID/ReplyCount/Replies 三個欄位只有「頂層留言」有意義（parent_id IS NULL 時）；
// 回覆（parent_id 非 NULL）的 ReplyCount 恆 0、Replies 恆 []（規格只允許一層，回覆沒有子回覆）。
//
// ⚠️ 軟刪遮蔽（見 thread.go maskDeleted）：deleted_at 非空的留言，Body 恆 ""、CanDelete 恆
// false、Reactions 恆 []、MyReaction 恆 nil——但列表仍要回傳這則留言本身（Deleted=true 的
// 佔位），否則討論串中間被挖掉，後面的回覆會看不懂（migration 159 檔頭的既有教訓）。
type CommentView struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatar_url"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	CanDelete bool      `json:"can_delete"`

	ParentID   *string             `json:"parent_id"`
	ReplyCount int                 `json:"reply_count"`
	Reactions  []ReactionCountView `json:"reactions"`
	MyReaction *string             `json:"my_reaction"`
	Deleted    bool                `json:"deleted"`
	Replies    []CommentView       `json:"replies"`
}

// ReactionCountView 單一留言的表情反應統計（依 count desc、kind asc 排序，見 thread.go
// sortReactions；無反應時上層一律回 []，不是 null）。
type ReactionCountView struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// QuotaView GET /run-meets/quota。
//
// ⚠️ VIPCap / VIPImageLimit 是給前端「升級 VIP 可以做什麼」文案用的，必須由後端帶下去：
// 這兩個值是後台可調設定（runmeet_quota_vip / runmeet_images_vip），前端寫死 10 / 4 的話，
// 營運一改設定，確認彈窗與圖片欄提示就會對非 VIP 承諾拿不到的權益。
// 它們是全站共用的方案參數、不是任何人的個資，回傳給所有登入者沒有隱私問題。
type QuotaView struct {
	Month         string    `json:"month"` // 台北月 YYYY-MM
	Cap           int       `json:"cap"`
	Used          int       `json:"used"`
	Remaining     int       `json:"remaining"`
	IsVIP         bool      `json:"is_vip"`
	RequiresVIP   bool      `json:"requires_vip"`
	ImageLimit    int       `json:"image_limit"`
	VIPCap        int       `json:"vip_cap"`
	VIPImageLimit int       `json:"vip_image_limit"`
	CapacityMax   int       `json:"capacity_max"`
	ResetsAt      time.Time `json:"resets_at"`
}

// PlaceSuggestion GET /run-meets/place-suggest 的單筆建議（來源：explore_bosses 的地點欄位）。
// ⚠️ 只回四個地點欄位。探索系統有「未揭露關主要遮蔽身分/圖/課表」的既有規則（explore.maskBoss），
// 這裡若順手多回 name/code/scene_image_url 等於開了一條繞過該規則的旁路。
type PlaceSuggestion struct {
	Region string  `json:"region"`
	Place  string  `json:"place"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
}

// --- 權限判定（純函式，可單元測試）---

// MyState 由「觀看者是不是發起人」與 run_meet_members.status 推導出前端 CTA 用的身分字串。
func MyState(isOwner bool, memberStatus *string) string {
	if isOwner {
		return "owner"
	}
	if memberStatus == nil || *memberStatus == "" {
		return "none"
	}
	return *memberStatus
}

// CanSeePreciseLocation 決定要不要吐 lat/lng/meeting_detail（成員層）。
//
// ⚠️ 身分即時判定：被剔除（kicked）或自行退出（left）後立刻失去精確地點；審核制下「申請中」
// （pending）**不算成員**，看不到；私密團的密碼只是進入詳情頁的入場券，**通過密碼 ≠ 成為成員**
// （unlocked 不在這個判斷式裡），精確地點仍需正式加入。
func CanSeePreciseLocation(isOwner bool, memberStatus *string, isAdmin bool) bool {
	if isOwner || isAdmin {
		return true
	}
	return memberStatus != nil && *memberStatus == MemberJoined
}

// HasDetailAccess 私密團的「詳情頁入場券」判定（規格 1.4 的統一判斷式）。
// 公開團恆 true；私密團需為發起人／成員或申請中／已解鎖／後台。
// 注意：這條只管「能不能看完整說明與圖片」，與 CanSeePreciseLocation 是**兩道獨立閘門**。
func HasDetailAccess(isPrivate, isOwner bool, memberStatus *string, unlocked, isAdmin bool) bool {
	if !isPrivate || isOwner || isAdmin || unlocked {
		return true
	}
	if memberStatus == nil {
		return false
	}
	return *memberStatus == MemberJoined || *memberStatus == MemberPending
}

// --- 狀態機（純函式，可單元測試；handler/repository 一律呼叫這裡，不要各自散落 if）---

// runmeetTransitions 合法的狀態轉換白名單（使用者定案）：
//
//	open      → closed | cancelled
//	closed    → open   | cancelled   （關閉可逆——這是本次重整的主因：使用者原話「關閉的團練
//	                                   為什麼無法再開啟，發起人應該可以再次開啟才對」）
//	cancelled → open                 （中止可恢復；但不可直接 cancelled → closed，必須先回到
//	                                   open 才能再關閉——中止當下已把所有待審一併婉拒，跳過 open
//	                                   直接變 closed 會讓「重開後繼續處理待審」這個語意變得含糊）
//
// 同狀態互轉（open→open 等）刻意不在白名單內：那不是一次有意義的轉換，呼叫端該用「不做任何事」
// 表達，不該讓 API 靜默把它當成功。
var runmeetTransitions = map[string]map[string]bool{
	StatusOpen:      {StatusClosed: true, StatusCancelled: true},
	StatusClosed:    {StatusOpen: true, StatusCancelled: true},
	StatusCancelled: {StatusOpen: true},
}

// CanTransition 這個狀態轉換是否合法。
func CanTransition(from, to string) bool {
	return runmeetTransitions[from][to]
}

// CanEdit 只有 open 狀態能編輯基本資料（title/meet_at/地點/人數上限…）。
// closed／cancelled 都不行——「不再收人」或「已中止」的當下不該還能把時間地點整個換掉。
// 已過期（meet_at <= now）是另一條獨立的擋法（errEditEnded，查詢條件判定，不歸這支管）。
// CanEdit 這個狀態下發起人能不能編輯團練內容（名稱/時間/地點/說明/圖片/人數上限）。
//
// open、closed 都可以編輯；cancelled 不行。
// ⚠️ closed 必須可編輯——使用者對「關閉」的定義是「不再收新人，其他功能都照舊」（見
// migration 158 檔頭），發起人常見的動作正是「先關閉停止招募、再把時間或集合細節改一改、
// 然後重新開啟」。把 closed 也擋掉會讓「可逆的關閉」失去意義。
// 「同意待審申請」則相反，仍只在 open 允許（見 members.go Approve）——同意＝收新人，
// 與關閉的語意直接衝突；待審申請在 closed 期間保留不動，重開後才處理。
func CanEdit(status string) bool { return status == StatusOpen || status == StatusClosed }

// CanSeeWhenHidden 團練被發起人隱藏（hidden_by_owner）時，這個身分是否仍看得到／能互動。
//
// 與 repository.GetMeet 的 SQL 條件（hidden_by_owner=FALSE OR owner_id=viewer OR
// mm.status='joined'）語意刻意保持一致——那條規則因為要在 SQL 層擋掉非當事人，沒辦法直接
// 單元測試，這裡把同一條規則抽成 Go 純函式：一來給 members.go 的 Join() 用（呼叫者在那個
// 時間點一定不是 owner，見呼叫端註解），二來讓這條「隱藏可見性」規則本身可以脫離 DB 被測試，
// 未來 SQL 那邊改規則時，這裡的測試會提醒維護者同步改。
func CanSeeWhenHidden(isOwner bool, memberStatus string) bool {
	return isOwner || memberStatus == MemberJoined
}
