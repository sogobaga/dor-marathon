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

// OwnerView 發起人公開身分。名字一律 COALESCE(NULLIF(u.name,''), u.handle)
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
	Excerpt          string    `json:"excerpt"`    // 60 字摘要（swrCache 單筆 100KB 上限，列表不給完整 description）
	CoverURL         *string   `json:"cover_url"`  // 私密團且未解鎖時為 null
	Status           string    `json:"status"`     // open|closed|cancelled
	IsEnded          bool      `json:"is_ended"`   // meet_at <= NOW()
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
}

// detailBase 詳情的公開部分（完整說明與圖片；需通過私密團解鎖才拿得到）。
type detailBase struct {
	CardView
	Description string   `json:"description"`
	ImageURLs   []string `json:"image_urls"`
	ImageLimit  int      `json:"image_limit"`
	PendingCount int     `json:"pending_count"` // 只有 owner 視角有意義；非 owner 恆 0
	CanComment  bool     `json:"can_comment"`   // joined/owner 且未超過「結束 7 天」唯讀期
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

// CommentView 留言（同樣只給顯示名與頭像）。
type CommentView struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatar_url"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	CanDelete bool      `json:"can_delete"`
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
