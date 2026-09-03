package race

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/promo"
	"github.com/dor/api/internal/ttlcache"
)

// uuidRe 判斷字串是否為標準 UUID 格式，用於區分 raceID 路徑參數是 UUID 還是 slug。
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	// maxAddonQtyPerLine 加購單品項每筆數量上限。業務上不可能真的單筆買到這麼多份，
	// 主要用於擋整數溢位攻擊（見 SEC-C1：超大 qty 讓 price*qty 溢位成負數，繞過金流）。
	maxAddonQtyPerLine = 999
	// maxAddonLineCents 加購單品項金額上限（分）＝新台幣 1000 萬，超出視為異常資料一律拒絕。
	maxAddonLineCents = 1_000_000_000
)

// safeAddonLineTotal 計算加購單品項金額 price_cents*qty（分），並防整數溢位。
// qty 必須先通過 checkAddonQty 的上限檢查；price_cents 理論上來自後台設定（受信任），
// 仍加上除法防溢位檢查以防禦異常資料。回傳 ok=false 時代表溢位或超過合理上限，呼叫端應拒絕整筆請求。
func safeAddonLineTotal(priceCents, qty int) (int, bool) {
	if qty <= 0 || qty > maxAddonQtyPerLine || priceCents < 0 {
		return 0, false
	}
	if priceCents != 0 && qty > math.MaxInt64/priceCents {
		return 0, false // 相乘會溢位
	}
	total := priceCents * qty
	if total > maxAddonLineCents {
		return 0, false
	}
	return total, true
}

// discountMethodCount 統計這次報名同時指定了幾種折抵方式（VIP活動優惠券／活動優惠券(migration 138)／
// 優惠序號）。抽成純函式（不碰 DB）方便單元測試互斥矩陣，供 Register() 的前置守門與
// Repository.RegisterWithOrder 的交易層最後防線共用同一套判斷邏輯。
func discountMethodCount(useCoupon bool, promoCode, couponRewardID string) int {
	n := 0
	if useCoupon {
		n++
	}
	if strings.TrimSpace(promoCode) != "" {
		n++
	}
	if strings.TrimSpace(couponRewardID) != "" {
		n++
	}
	return n
}

// checkAddonQty 驗證加購數量在合理範圍內（Qty<=0 由呼叫端略過該筆，不經過此函式）。
func checkAddonQty(qty int) error {
	if qty > maxAddonQtyPerLine {
		return ErrAddonQtyInvalid
	}
	return nil
}

var (
	ErrRaceNotFound      = errors.New("race not found")
	ErrAlreadyRegistered = errors.New("already registered for this race")
	// ErrChallengeInProgress 個人挑戰模式(event_mode=personal)專用：使用者已有一筆進行中(pending/paid未完成)
	// 的挑戰 attempt，需完成或到期後才能再次報名同一賽事。與 ErrAlreadyRegistered 不同之處在於
	// completed/expired/cancelled 的舊 attempt 不會擋下新報名（見 Register 的 personal 分支）。
	ErrChallengeInProgress  = errors.New("你有進行中的挑戰，完成後才能再報名")
	ErrRegistrationClosed   = errors.New("registration is not open")
	ErrSoldOut              = errors.New("race is sold out")
	ErrInvalidDistance      = errors.New("invalid distance for this race")
	ErrRaceHasRegistrations = errors.New("race has registrations and cannot be deleted")
	ErrGroupNotFound        = errors.New("group not found in this race")
	ErrGroupFull            = errors.New("group is full")
	ErrGroupRequired        = errors.New("group selection is required")
	ErrMissingRequiredField = errors.New("missing required participant field")
	ErrGroupRestriction     = errors.New("participant does not meet group restriction")
	ErrNoGroups             = errors.New("race has no groups")
	ErrAddonNotFound        = errors.New("addon not found")
	ErrAddonLimit           = errors.New("addon quantity exceeds per-user limit")
	ErrAddonSoldOut         = errors.New("addon sold out")
	// ErrAddonQtyInvalid 加購單品項數量超出允許上限（防整數溢位、異常大量下單）。
	ErrAddonQtyInvalid      = errors.New("addon quantity exceeds allowed limit")
	ErrOrderNotFound        = errors.New("order not found")
	ErrRegistrationNotFound = errors.New("registration not found")
	ErrCheckinNotFound      = errors.New("checkin not found or already reviewed")
	ErrRegistrationPaused   = errors.New("此賽事目前暫停報名")
	ErrGroupKeyWrong        = errors.New("跑團鑰匙錯誤")
	ErrTeamGroupsDisabled   = errors.New("此賽事未開放跑團分組申請")
	ErrTeamGroupName        = errors.New("請輸入跑團分組名稱")
	ErrTeamGroupNotAllowed  = errors.New("您的帳號未開放建立跑團分組")
	ErrTaskModuleName       = errors.New("請輸入任務模組名稱")
	ErrTaskModuleNotFound   = errors.New("task module not found")
	ErrVIPOnly              = errors.New("此賽事僅限 VIP 會員報名")
	ErrNoCoupon             = errors.New("沒有可用的活動優惠券")
	// ErrDiscountConflict 報名折抵三擇一（VIP活動優惠券／活動優惠券／優惠序號）同時帶超過一種
	// （取代舊版只擋 VIP券+序號兩者的 ErrCouponPromoConflict，migration 138 加入活動優惠券後一般化為三擇一）。
	ErrDiscountConflict = errors.New("折抵方式僅能擇一使用（VIP活動優惠券／活動優惠券／優惠序號）")
	// ErrCouponRewardInvalid 指定的活動優惠券（user_rewards.id）不存在、非本人、已使用或已過期
	// （CAS 核銷 0 列時回這個錯誤，見 Repository.RegisterWithOrder）。
	ErrCouponRewardInvalid = errors.New("活動優惠券不存在、已使用或已過期")
	ErrInvalidInvoice      = errors.New("invoice 資料有誤")
	// ErrOrderNotPending 訂單存在，但目前狀態不是 pending 也不是已付款(paid)——例如 refunded/cancelled。
	// 用於區分「已付款的冪等重複通知」（安靜成功）與「錢已收到但訂單處於異常狀態，需要人工核對」（要告警）。
	ErrOrderNotPending = errors.New("order exists but is not pending")
	// ErrRegistrationNotPending 報名存在，但目前狀態不是 pending，不可再標記為已付款
	// （避免已取消/已退款的報名被後台「標記已付」按鈕復活）。
	ErrRegistrationNotPending = errors.New("registration exists but is not pending")

	// --- 取消報名申請 ---
	ErrCancelRequestNotFound   = errors.New("cancel request not found")
	ErrCancelRequestPending    = errors.New("已有取消申請正在審核中")
	ErrCancelRegistrationState = errors.New("此報名狀態無法申請取消")
	ErrCancelDeadlinePassed    = errors.New("已超過可取消期限")

	// ErrInvalidVipPlan CreateVipOrder 收到 monthly/annual 以外的方案代碼（見 VIP 訂閱 Phase C1）。
	ErrInvalidVipPlan = errors.New("invalid vip plan")

	// ErrCertificateDisabled 此賽事關閉完賽證明／完賽歷程顯示（config.certificate_disabled，見
	// GetMyCertificate／GetPersonalHistory）。handler 層回 403，防止繞過前端隱藏直接呼叫 API。
	ErrCertificateDisabled = errors.New("此賽事未開放完賽證明")
)

// MailInserter 站內信最小介面（參賽虛擬獎勵發放通知用，migration 140）：由 mail.Handler 實作。用小介面
// 而非直接 import internal/mail，比照 push/payment 套件既有的 MailInserter 介面同一慣例（見
// internal/push/push.go、internal/payment/ecpay_bind_handler.go），讓依賴方向單純、好測試。
type MailInserter interface {
	InsertForUsers(ctx context.Context, userIDs []string, level, title, body, url string) (int, error)
}

type Service struct {
	repo  *Repository
	rdb   *redis.Client
	promo *promo.Service
	// refundCreator：取消申請核准時複用綠界退款核心邏輯（不重寫打綠界那段）。
	// 注入自 payment.Handler.CreateRefund（見 main.go 的 raceHandler.SetRefundCreator），晚於本
	// Service 建構——payment.NewHandler 需要 raceSvc 當 OrderMarker，兩者互相依賴，只能用 setter
	// 晚繫結解開。未設定時 ApproveCancelRequest 會略過自動建立退款，留言請人工處理。
	refundCreator RefundCreatorFunc
	// mail：參賽虛擬獎勵發放通知用（migration 140，見 entry_reward_schedule.go grantEntryRewardSafe）。
	// 注入自 mail.Handler（見 main.go 的 raceSvc.SetMailInserter），晚於本 Service 建構——mailHandler
	// 建構需要 wsManager，兩者初始化順序無法互換，只能用 setter 晚繫結。未設定時通知直接跳過，不影響
	// 發獎本身（比照 payment.BindHandler.sendRenewalMail 的取捨）。
	mail MailInserter
	// raceMetaCache 見 meta_cache.go：GET /races/{slug}/meta 用的「所有已上線賽事精簡欄位」快取
	// （10 分鐘 TTL）。NewService 建構時就綁好 load closure，不用 sync.Once 延遲初始化——本 Service
	// 只會在 main.go 建構一次，建構當下還是單一 goroutine（HTTP server 尚未開始收 request），沒有
	// 資料競賽疑慮。
	raceMetaCache *ttlcache.Cache[map[string]RaceMeta]
}

func NewService(repo *Repository, rdb *redis.Client, promoSvc *promo.Service) *Service {
	s := &Service{repo: repo, rdb: rdb, promo: promoSvc}
	s.raceMetaCache = newRaceMetaCache(s.repo.ListPublicRaceMeta)
	return s
}

// SetRefundCreator 見上方欄位註解。
func (s *Service) SetRefundCreator(fn RefundCreatorFunc) {
	s.refundCreator = fn
}

// SetMailInserter 見上方欄位註解。
func (s *Service) SetMailInserter(m MailInserter) {
	s.mail = m
}

// List 回傳賽事列表（admin 用，含全部 control_status，填入 display_status）
func (s *Service) List(ctx context.Context, status string) ([]*Race, error) {
	races, err := s.repo.List(ctx, status)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, r := range races {
		r.FillDisplay(now)
	}
	return races, nil
}

// ListPublic 前台賽事列表：排除 closed/hidden，testing 只給白名單 email，並填入 display_status。
func (s *Service) ListPublic(ctx context.Context, userID string) ([]*Race, error) {
	races, err := s.repo.List(ctx, "") // 不依舊 status 欄位過濾
	if err != nil {
		return nil, err
	}
	email, _ := s.repo.GetUserEmail(ctx, userID)
	now := time.Now()
	out := []*Race{}
	for _, r := range races {
		switch r.ControlStatus {
		case "closed", "hidden":
			continue
		case "testing":
			if email == "" {
				continue
			}
			ok, err := s.repo.IsEmailWhitelisted(ctx, r.ID, email)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		r.FillDisplay(now)
		// SEC：reward_config 內含機率(prob_bp)/面額權重(weight)/金額區間(min/max) 等機敏設定，公開列表
		// 一律不得帶出（玩家能看到就等於看到中獎率），前台改走 GetRewardPreview 專用端點取代。
		r.RewardConfig = nil
		// SEC：entry_reward_config（migration 140）同樣機敏，理由相同；前台改走 GetEntryRewardPreview
		// 專用端點取代（見 reward_preview.go）。
		r.EntryRewardConfig = nil
		// SEC（2026-08-28 資安盤點）：review_note 是後台審核合作方投稿賽事的內部備註（reject 時的
		// 質疑/業務判斷用語），公開列表不得帶出——與 test_whitelist 同款「漏清機敏欄位」。
		r.ReviewNote = ""
		out = append(out, r)
	}
	return out, nil
}

// GetUserRegistrations 取得使用者所有報名的精簡狀態（賽事列表用）
func (s *Service) GetUserRegistrations(ctx context.Context, userID string) (map[string]MyRegLite, error) {
	return s.repo.GetUserRegistrations(ctx, userID)
}

// --- 全域預設測試白名單 ---

func (s *Service) ListDefaultWhitelist(ctx context.Context) ([]string, error) {
	return s.repo.ListDefaultWhitelist(ctx)
}
func (s *Service) AddDefaultWhitelist(ctx context.Context, email string) error {
	return s.repo.AddDefaultWhitelist(ctx, email)
}
func (s *Service) RemoveDefaultWhitelist(ctx context.Context, email string) error {
	return s.repo.RemoveDefaultWhitelist(ctx, email)
}

// GetDetail 回傳賽事詳情 + 使用者的報名狀態
func (s *Service) GetDetail(ctx context.Context, raceID, userID string) (*Race, *Registration, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, nil, err
	}
	if race == nil {
		return nil, nil, ErrRaceNotFound
	}

	var reg *Registration
	if userID != "" {
		reg, err = s.repo.GetRegistration(ctx, userID, raceID)
		if err != nil {
			return nil, nil, err
		}
	}

	return race, reg, nil
}

// GetPublicDetail 取得公開賽事詳情（含分組/加購/物資）+ 使用者報名狀態。
// raceID 可為 UUID 或 slug（廣告落地頁 /event/{slug} 深連結用）：非 UUID 格式時先以 slug 查出真正的 ID。
func (s *Service) GetPublicDetail(ctx context.Context, raceID, userID string) (*RaceDetail, *Registration, error) {
	if !uuidRe.MatchString(raceID) {
		bySlug, err := s.repo.GetBySlug(ctx, raceID)
		if err != nil {
			return nil, nil, err
		}
		if bySlug == nil {
			return nil, nil, ErrRaceNotFound
		}
		raceID = bySlug.ID
	}
	detail, err := s.repo.GetDetail(ctx, raceID)
	if err != nil {
		return nil, nil, err
	}
	if detail == nil || detail.ReviewStatus != "approved" {
		return nil, nil, ErrRaceNotFound
	}
	// 可見性：closed 全擋；testing 僅白名單 email（hidden 有連結可進）
	if detail.ControlStatus == "closed" {
		return nil, nil, ErrRaceNotFound
	}
	if detail.ControlStatus == "testing" {
		email, _ := s.repo.GetUserEmail(ctx, userID)
		if email == "" {
			return nil, nil, ErrRaceNotFound
		}
		ok, err := s.repo.IsEmailWhitelisted(ctx, raceID, email)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, ErrRaceNotFound
		}
	}
	detail.FillDisplay(time.Now())

	// 賽事已結束 → 背景自動結算 EXP（idempotent；已結算會便宜跳過、失敗則下次讀取重試）
	if detail.DisplayStatus == "ended" {
		raceID := detail.ID
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = s.SettleRaceEXP(bg, raceID, false)
		}()
	}

	// 安全：公開回傳一律不洩漏跑團鑰匙明碼（前台只需 requires_key 旗標）
	for i := range detail.Groups {
		detail.Groups[i].GroupKey = ""
	}
	// 安全：reward_config 內含機率(prob_bp)/面額權重(weight)/金額區間(min/max) 等機敏設定，公開詳情
	// 一律不得帶出（玩家能看到就等於看到中獎率），前台改走 GetRewardPreview 專用端點取代。
	detail.RewardConfig = nil
	// 安全：entry_reward_config（migration 140）同樣機敏，理由相同；前台改走 GetEntryRewardPreview
	// 專用端點取代（見 reward_preview.go）。
	detail.EntryRewardConfig = nil
	// 安全（SEC，2026-08-28 測試標籤任務盤點時發現）：test_whitelist 是白名單成員 email 明碼，
	// 只有後台編輯表單需要（走 AdminGetRace→GetRaceDetail 另一條路），公開詳情一律清空——
	// 否則任何看得到詳情頁的訪客都能拿到測試名單的 email（PII 洩漏）。
	detail.TestWhitelist = nil
	// 安全（SEC，2026-08-28 資安盤點再發現同款）：review_note 是後台審核內部備註，公開詳情不得帶出。
	detail.ReviewNote = ""

	// 取消退費規則（簡章頁尾表格用）：解析好最終生效政策一併回傳，跟 CreateCancelRequest 實際退費
	// 計算共用同一顆 ResolveCancellationPolicy，避免前端顯示跟真正退費金額兜不起來。
	// 不退費賽事（config.refund_disabled）不回傳解析後政策——前台簡章的「取消退費規則」區塊
	// 已有 !policy 防禦（BrochureScreen RefundPolicyBody），會自動整塊不顯示。
	if detail.Config.RefundDisabled {
		detail.ResolvedCancellationPolicy = nil
	} else {
		sysDefaultPolicy := appsettings.GetString(ctx, s.repo.db, "cancellation_policy", "")
		resolvedPolicy := ResolveCancellationPolicy(detail.Config.CancellationPolicy, sysDefaultPolicy)
		detail.ResolvedCancellationPolicy = &resolvedPolicy
	}

	var reg *Registration
	if userID != "" {
		reg, err = s.repo.GetRegistration(ctx, userID, raceID)
		if err != nil {
			return nil, nil, err
		}
	}
	return detail, reg, nil
}

// CreateTeamGroup 前台跑團成員自建分組（限 competition 且已開放 allow_team_groups、報名期間內）。
func (s *Service) CreateTeamGroup(ctx context.Context, req *CreateTeamGroupRequest) (*RaceGroup, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.GroupKey = strings.TrimSpace(req.GroupKey)
	if req.Name == "" {
		return nil, ErrTeamGroupName
	}
	race, err := s.repo.GetByID(ctx, req.RaceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	if race.EventMode != "competition" || !race.AllowTeamGroups {
		return nil, ErrTeamGroupsDisabled
	}
	// 權限：僅開放的會員可建立
	allowed, err := s.repo.UserCanCreateTeamGroup(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrTeamGroupNotAllowed
	}
	// testing 模式僅白名單可建立；closed 全擋
	switch race.ControlStatus {
	case "closed":
		return nil, ErrRaceNotFound
	case "testing":
		email, _ := s.repo.GetUserEmail(ctx, req.UserID)
		if email == "" {
			return nil, ErrRaceNotFound
		}
		ok, err := s.repo.IsEmailWhitelisted(ctx, req.RaceID, email)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrRaceNotFound
		}
	}
	// 僅報名期間可建立
	if _, canReg := race.ComputeDisplay(time.Now()); !canReg {
		return nil, ErrRegistrationClosed
	}
	return s.repo.CreateTeamGroup(ctx, *req)
}

// CanUserCreateTeamGroup 該使用者於此賽事是否可建立跑團分組（前台顯示按鈕用）
func (s *Service) CanUserCreateTeamGroup(ctx context.Context, userID string, race *Race) bool {
	if userID == "" || race == nil || race.EventMode != "competition" || !race.AllowTeamGroups {
		return false
	}
	ok, _ := s.repo.UserCanCreateTeamGroup(ctx, userID)
	return ok
}

// Register 處理前台報名（分組 + 加購 + 訂單 + 個資回填）。
func (s *Service) Register(ctx context.Context, req *RegisterRequest) (*RegisterResult, error) {
	race, err := s.repo.GetByID(ctx, req.RaceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	// control_status 守門
	switch race.ControlStatus {
	case "closed":
		return nil, ErrRaceNotFound
	case "paused":
		return nil, ErrRegistrationPaused
	case "suspended":
		return nil, ErrRegistrationClosed
	case "testing":
		email, _ := s.repo.GetUserEmail(ctx, req.UserID)
		if email == "" {
			return nil, ErrRaceNotFound
		}
		ok, err := s.repo.IsEmailWhitelisted(ctx, req.RaceID, email)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrRaceNotFound
		}
	}
	// VIP 限定：非 VIP 不可報名
	if race.VipOnly && !s.repo.IsUserVIP(ctx, req.UserID) {
		return nil, ErrVIPOnly
	}
	// 報名折抵三擇一：VIP活動優惠券／活動優惠券(migration 138)／優惠序號，同時帶超過一種一律擋
	// （比照原「優惠券與序號擇一」，一般化成三方互斥；只擋「多帶」，三者都不帶＝不折抵，合法）。
	if discountMethodCount(req.UseCoupon, req.PromoCode, req.CouponRewardID) > 1 {
		return nil, ErrDiscountConflict
	}
	// 加購數量上限（防整數溢位攻擊，見 SEC-C1）：在進交易前先擋，避免無謂鎖表
	for _, a := range req.Addons {
		if err := checkAddonQty(a.Qty); err != nil {
			return nil, err
		}
	}
	// 時間規則：非「報名中」不可報名
	if _, canReg := race.ComputeDisplay(time.Now()); !canReg {
		return nil, ErrRegistrationClosed
	}

	// 重複報名 / 再挑戰閘門（交易內的 partial unique index uq_registrations_active_user_race 仍是最終保證，
	// 見 RegisterWithOrder 對 23505 衝突的 mapping）。依賽事模式行為不同：
	attemptNo := 1
	if race.EventMode == "personal" {
		// 個人挑戰模式：可重複報名再挑戰，只有「進行中」(pending/paid未完成) 的 attempt 才擋下新報名；
		// completed/expired/cancelled 的舊 attempt 都不算數（見 migration 124 唯一索引已放寬）。
		active, err := s.repo.HasActiveRegistration(ctx, req.UserID, req.RaceID)
		if err != nil {
			return nil, err
		}
		if active {
			return nil, ErrChallengeInProgress
		}
		attemptNo, err = s.repo.NextAttemptNo(ctx, req.UserID, req.RaceID)
		if err != nil {
			return nil, err
		}
	} else {
		// 非 personal：維持原邏輯不變——已取消（含退款後取消）的舊報名不算數，允許重新報名同一賽事
		// （見 migration 093）；其餘（pending/paid）狀態一律擋下。
		if existing, err := s.repo.GetRegistration(ctx, req.UserID, req.RaceID); err != nil {
			return nil, err
		} else if existing != nil && existing.Status != "cancelled" {
			return nil, ErrAlreadyRegistered
		}
	}

	groups, err := s.repo.GetGroups(ctx, req.RaceID)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, ErrNoGroups
	}

	// 決定分組
	var chosen *RaceGroup
	revealed := true
	switch race.EventMode {
	case "faction_battle":
		chosen = pickBalancedGroup(groups) // 隨機/平衡指派
		revealed = false                   // 賽事當天才公布
	case "personal":
		// 個人挑戰模式沒有「選分組」的概念：P1 建賽事(ensurePersonalDefaultGroup)時已自動補一筆
		// 隱藏預設分組（唯一分組），所有挑戰者實質上共用它，前端不顯示分組選擇 UI，這裡直接取第一筆。
		chosen = &groups[0]
	default:
		if req.GroupID == "" {
			return nil, ErrGroupRequired
		}
		for i := range groups {
			if groups[i].ID == req.GroupID {
				chosen = &groups[i]
				break
			}
		}
		if chosen == nil {
			return nil, ErrGroupNotFound
		}
	}

	// 必填欄位驗證
	if err := validateRequiredFields(race.RequiredFields, req.Participant); err != nil {
		return nil, err
	}
	// 分組性別/年齡限制
	if err := validateGroupRestriction(chosen, req.Participant); err != nil {
		return nil, err
	}
	// 發票資訊：未帶 invoice 物件時正規化為 personal 且全空（雲端發票存證），不影響報名成功；
	// 有帶則驗證三種買受人類型互斥、統編檢查碼、手機條碼載具格式、愛心碼格式（見 invoice.go）。
	invoice, err := ValidateInvoice(req.Invoice)
	if err != nil {
		return nil, err
	}

	distance := 0
	if chosen.TargetDistanceKm != nil {
		distance = int(*chosen.TargetDistanceKm)
	}

	return s.repo.RegisterWithOrder(ctx, RegisterTxInput{
		UserID:         req.UserID,
		RaceID:         req.RaceID,
		EventMode:      race.EventMode,
		AttemptNo:      attemptNo,
		GroupID:        chosen.ID,
		GroupKey:       strings.TrimSpace(req.GroupKey),
		EntryFee:       race.EntryFee, // 預設報名費；有效組價由 RegisterWithOrder 在鎖定分組列的同一刻重算（見該函式）
		FeeMode:        race.FeeMode,
		GroupRevealed:  revealed,
		Distance:       distance,
		Addons:         req.Addons,
		Participant:    req.Participant,
		PromoCode:      strings.TrimSpace(req.PromoCode),
		UseCoupon:      req.UseCoupon,
		CouponRewardID: strings.TrimSpace(req.CouponRewardID),
		Invoice:        invoice,
		// 只有本次報名真的帶了 invoice 物件才覆寫 user_profiles 的發票預填欄位；完全沒帶（例如舊版
		// 前端）時維持既有預填不變，避免被正規化出來的空白值誤蓋掉使用者之前填過的統編/載具。
		SaveInvoiceToProfile: req.Invoice != nil,
	})
}

// QuotePromo 報名前試算優惠序號折抵（不寫入）。序號無效時回 Valid=false + Reason。
// groupID 選填：per_group 計價模式下需要它才能算出正確的有效組價（見 EffectiveGroupFee）；
// 分組對抗/個人挑戰模式報名前沒有「選分組」概念，帶空字串即可，會回退賽事預設報名費。
func (s *Service) QuotePromo(ctx context.Context, raceID, userID, groupID, code string, addons []AddonSelection) (*PromoQuote, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}

	var group *RaceGroup
	if groupID != "" {
		groups, err := s.repo.GetGroups(ctx, raceID)
		if err != nil {
			return nil, err
		}
		for i := range groups {
			if groups[i].ID == groupID {
				group = &groups[i]
				break
			}
		}
	}
	entryFee := EffectiveGroupFee(race, group)

	p, err := s.promo.ValidateForRace(ctx, code, raceID, userID)
	if err != nil {
		return &PromoQuote{Valid: false, Reason: err.Error()}, nil
	}

	discount := promo.DiscountCents(p, entryFee)
	addonsTotal, err := s.addonsTotal(ctx, raceID, addons)
	if err != nil {
		return nil, err
	}
	payable := entryFee - discount
	if payable < 0 {
		payable = 0
	}
	payable += addonsTotal
	if payable < 0 { // 防禦性夾限：理論上 addonsTotal 已不可能為負，仍與 repository.go 的實際扣款邏輯保持一致
		payable = 0
	}
	return &PromoQuote{
		Valid:         true,
		Code:          p.Code,
		DiscountCents: discount,
		PayableCents:  payable,
		Free:          payable < 50,
	}, nil
}

// addonsTotal 依選購計算加購總額（分）
func (s *Service) addonsTotal(ctx context.Context, raceID string, sel []AddonSelection) (int, error) {
	if len(sel) == 0 {
		return 0, nil
	}
	addons, err := s.repo.GetAddons(ctx, raceID)
	if err != nil {
		return 0, err
	}
	priceByID := map[string]int{}
	for _, a := range addons {
		priceByID[a.ID] = a.PriceCents
	}
	total := 0
	for _, a := range sel {
		if a.Qty <= 0 {
			continue
		}
		if err := checkAddonQty(a.Qty); err != nil {
			return 0, err
		}
		lineTotal, ok := safeAddonLineTotal(priceByID[a.AddonID], a.Qty)
		if !ok {
			return 0, ErrAddonQtyInvalid
		}
		total += lineTotal
	}
	return total, nil
}

// pickBalancedGroup 從分組中挑人數最少者（同最少則取第一個），用於分組對抗隨機指派
func pickBalancedGroup(groups []RaceGroup) *RaceGroup {
	idx := 0
	for i := range groups {
		if groups[i].SlotsTaken < groups[idx].SlotsTaken {
			idx = i
		}
	}
	return &groups[idx]
}

func validateRequiredFields(required []string, p ParticipantInfo) error {
	get := map[string]string{
		"real_name": p.RealName, "nickname": p.Nickname, "phone": p.Phone,
		"address": p.Address, "birthday": p.Birthday, "gender": p.Gender,
	}
	for _, f := range required {
		if strings.TrimSpace(get[f]) == "" {
			return fmt.Errorf("%w: %s", ErrMissingRequiredField, f)
		}
	}
	return nil
}

func validateGroupRestriction(g *RaceGroup, p ParticipantInfo) error {
	if g.GenderLimit != "" && g.GenderLimit != "any" {
		if p.Gender != g.GenderLimit {
			return fmt.Errorf("%w: gender", ErrGroupRestriction)
		}
	}
	if g.AgeMin != nil || g.AgeMax != nil {
		age, ok := ageFromBirthday(p.Birthday)
		if !ok {
			return fmt.Errorf("%w: birthday required", ErrGroupRestriction)
		}
		if g.AgeMin != nil && age < *g.AgeMin {
			return fmt.Errorf("%w: age below min", ErrGroupRestriction)
		}
		if g.AgeMax != nil && age > *g.AgeMax {
			return fmt.Errorf("%w: age above max", ErrGroupRestriction)
		}
	}
	return nil
}

// ageFromBirthday 由 YYYY-MM-DD 算現在年齡
func ageFromBirthday(birthday string) (int, bool) {
	t, err := time.Parse("2006-01-02", birthday)
	if err != nil {
		return 0, false
	}
	now := time.Now()
	age := now.Year() - t.Year()
	if now.YearDay() < t.YearDay() {
		age--
	}
	return age, true
}

// GetLiveStatus 取得即時陣營分數
func (s *Service) GetLiveStatus(ctx context.Context, raceID string) (*LiveStatus, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil {
		return nil, ErrRaceNotFound
	}

	status := &LiveStatus{
		RaceID: raceID,
		Status: race.Status,
		DayNow: s.computeDayNow(race),
	}

	// 陣營分數（從 Redis）
	if race.GroupType == "faction" && len(race.Config.Factions) > 0 {
		factionKmKey := "race:" + raceID + ":faction_km"
		kmMap := s.rdb.HGetAll(ctx, factionKmKey).Val()

		// 計算總 km
		totalKm := 0.0
		factionKms := make(map[string]float64, len(race.Config.Factions))
		for _, f := range race.Config.Factions {
			if v, ok := kmMap[f.ID]; ok {
				km, _ := strconv.ParseFloat(v, 64)
				factionKms[f.ID] = km
				totalKm += km
			}
		}

		for _, f := range race.Config.Factions {
			km := factionKms[f.ID]
			pct := 0.0
			if totalKm > 0 {
				pct = km / totalKm * 100
			}
			status.Factions = append(status.Factions, FactionStatus{
				ID:       f.ID,
				Name:     f.Name,
				Color:    f.Color,
				TotalKm:  km,
				ScorePct: pct,
			})
		}
	}

	return status, nil
}

// GetRanking 取得排行榜（Redis ZSET，Top N）
func (s *Service) GetRanking(ctx context.Context, raceID string, limit int64) ([]*RankEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	rankKey := "race:" + raceID + ":ranking"
	// ZREVRANGE: 高分優先
	zs, err := s.rdb.ZRevRangeWithScores(ctx, rankKey, 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("get ranking from redis: %w", err)
	}

	if len(zs) == 0 {
		return []*RankEntry{}, nil
	}

	// 批次取使用者資訊
	userIDs := make([]string, len(zs))
	for i, z := range zs {
		userIDs[i] = fmt.Sprint(z.Member)
	}
	handles, err := s.repo.GetUserHandles(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	entries := make([]*RankEntry, len(zs))
	for i, z := range zs {
		uid := fmt.Sprint(z.Member)
		info := handles[uid]
		entries[i] = &RankEntry{
			Rank:       i + 1,
			UserID:     uid,
			Handle:     info[0],
			Name:       info[1],
			DistanceKm: z.Score / 1000, // 儲存時乘以 1000，還原
		}
	}

	return entries, nil
}

// UpdateRanking 更新 Redis 排行榜分數（activity upload 後呼叫）
func (s *Service) UpdateRanking(ctx context.Context, raceID, userID string, addKm float64) error {
	rankKey := "race:" + raceID + ":ranking"
	// ZINCRBY 原子增加分數
	return s.rdb.ZIncrBy(ctx, rankKey, addKm*1000, userID).Err()
}

// UpdateRaceStatus 更新賽事狀態（admin 用）
func (s *Service) UpdateRaceStatus(ctx context.Context, raceID, status string) error {
	if err := s.repo.UpdateStatus(ctx, raceID, status); err != nil {
		return err
	}
	s.InvalidateRaceMetaCache() // status 是 meta 表欄位之一，改了要讓 SSR 快取重查
	return nil
}

// SetCertificateBg 設定完賽證明底圖（admin 用）
func (s *Service) SetCertificateBg(ctx context.Context, raceID, url string) error {
	return s.repo.SetCertificateBg(ctx, raceID, url)
}

// SetRankDisplay 設定排行榜顯示（admin 用）
func (s *Service) SetRankDisplay(ctx context.Context, raceID string, dist, time bool) error {
	return s.repo.SetRankDisplay(ctx, raceID, dist, time)
}

// UpdateRace 更新賽事可編輯欄位（admin 用）。status 留空則沿用原值。
func (s *Service) UpdateRace(ctx context.Context, raceID string, race *Race) (*Race, error) {
	existing, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrRaceNotFound
	}
	race.ID = raceID
	if race.Status == "" {
		race.Status = existing.Status
	}
	updated, err := s.repo.Update(ctx, race)
	if err == nil {
		s.InvalidateRaceMetaCache()
	}
	return updated, err
}

// CreateRace 建立新賽事（admin 用，直接 approved）
func (s *Service) CreateRace(ctx context.Context, race *Race) (*Race, error) {
	race.ReviewStatus = "approved"
	created, err := s.repo.Create(ctx, race)
	if err == nil {
		s.InvalidateRaceMetaCache()
	}
	return created, err
}

var (
	validEventModes  = map[string]bool{"general": true, "competition": true, "faction_battle": true, "personal": true}
	validGoalTypes   = map[string]bool{"cumulative": true, "distance": true}
	validGenderLimit = map[string]bool{"any": true, "male": true, "female": true}
	validSupplyKinds = map[string]bool{"race_pack": true, "finisher": true}
	validTaskScope   = map[string]bool{
		ScopeRaceCollective: true, ScopeGroupTeam: true, ScopeGroupIndividual: true,
	}
)

// validateTaskMetric 驗證任務指標與其數值（threshold 需 target>0；range 需 lo/hi 且 lo<=hi）。
func validateTaskMetric(metric string, target, lo, hi *float64) error {
	spec, ok := MetricCatalog[metric]
	if !ok {
		return fmt.Errorf("invalid metric_type: %s", metric)
	}
	switch spec.Kind {
	case MetricThreshold:
		if target == nil || *target <= 0 {
			return fmt.Errorf("metric %s requires positive target_value", metric)
		}
	case MetricRange:
		if lo == nil || hi == nil {
			return fmt.Errorf("metric %s requires range_lo and range_hi", metric)
		}
		if *lo > *hi {
			return fmt.Errorf("metric %s: range_lo must be <= range_hi", metric)
		}
	}
	return nil
}

// normalizeRequest 套用預設值並驗證巢狀 payload（建立與更新共用）。
func normalizeRequest(req *CreateRaceRequest) error {
	if req.EventMode == "" {
		req.EventMode = "general"
	}
	if !validEventModes[req.EventMode] {
		return fmt.Errorf("invalid event_mode: %s", req.EventMode)
	}
	// 分組對抗 = 隨機分配；其餘 = 選手自選
	if req.EventMode == "faction_battle" {
		req.GroupMode = "random"
	} else if req.GroupMode == "" {
		req.GroupMode = "self"
	}
	if req.GroupType == "" {
		req.GroupType = "distance"
	}
	// goal_type 只在競賽模式有意義，其餘固定 distance
	if req.EventMode == "competition" {
		if req.GoalType == "" {
			req.GoalType = "distance"
		}
		if !validGoalTypes[req.GoalType] {
			return fmt.Errorf("invalid goal_type: %s", req.GoalType)
		}
	} else {
		req.GoalType = "distance"
	}
	// 個人挑戰規則：僅 personal 模式使用且必填（沒有規則的個人挑戰賽不合理）；其餘模式一律清空，
	// 避免殘留舊資料或非預期輸入（比照上面 goal_type 的做法）。
	if req.EventMode == "personal" {
		if err := req.ChallengeRule.Validate(); err != nil {
			return fmt.Errorf("challenge_rule: %w", err)
		}
	} else {
		req.ChallengeRule = nil
	}
	// 即時獎勵設定（migration 134 起一般化到所有模式，不再限 personal）：personal 完成一次挑戰觸發、
	// 其餘模式完成任一「個人額外挑戰」(race_tasks scope=group_individual) 觸發（見 progress.go
	// MarkRaceTaskCompletedAndGrant）。選填（可不設定），但有填就要合法（機率/區間/序號組皆需通過驗證）。
	if err := req.RewardConfig.Validate(); err != nil {
		return fmt.Errorf("reward_config: %w", err)
	}
	// 參賽虛擬獎勵設定（migration 140）：與上面 reward_config 結構共用同一驗證，但語意完全不同——不看
	// 任何任務/完成條件，賽事開始後由背景排程自動發給所有已報名(paid)者（見 entry_reward_schedule.go）。
	// 選填，不限模式。
	if err := req.EntryRewardConfig.Validate(); err != nil {
		return fmt.Errorf("entry_reward_config: %w", err)
	}

	for i := range req.Groups {
		g := &req.Groups[i]
		if g.Name == "" {
			return fmt.Errorf("group %d: name is required", i)
		}
		if g.GenderLimit == "" {
			g.GenderLimit = "any"
		}
		if !validGenderLimit[g.GenderLimit] {
			return fmt.Errorf("group %d: invalid gender_limit", i)
		}
	}
	for i := range req.Supplies {
		su := &req.Supplies[i]
		if su.Name == "" {
			return fmt.Errorf("supply %d: name is required", i)
		}
		if !validSupplyKinds[su.Kind] {
			return fmt.Errorf("supply %d: invalid kind", i)
		}
	}
	for i := range req.Tasks {
		t := &req.Tasks[i]
		if !validTaskScope[t.Scope] {
			return fmt.Errorf("task %d: invalid scope", i)
		}
		if err := validateTaskMetric(t.MetricType, t.TargetValue, t.RangeLo, t.RangeHi); err != nil {
			return fmt.Errorf("task %d: %w", i, err)
		}
		if t.Title == "" {
			t.Title = MetricCatalog[t.MetricType].Label
		}
	}

	if req.Status == "" {
		req.Status = "soon"
	}
	// 同步 distances（沿用既有欄位；用各分組目標里程推導，至少給一筆避免 NOT NULL 空陣列）
	if len(req.Distances) == 0 {
		seen := map[int]bool{}
		for _, g := range req.Groups {
			if g.TargetDistanceKm != nil {
				d := int(*g.TargetDistanceKm)
				if d > 0 && !seen[d] {
					seen[d] = true
					req.Distances = append(req.Distances, d)
				}
			}
		}
		if len(req.Distances) == 0 {
			req.Distances = []int{0}
		}
	}
	return nil
}

// personalDefaultGroupName 個人挑戰模式自動補的隱藏預設分組名稱（管理員不需自己設分組）。
const personalDefaultGroupName = "個人挑戰"

// ensurePersonalDefaultGroup 個人挑戰模式若未帶任何分組，補一筆隱藏預設分組，
// 繞過 Register()（P2 用）的 ErrNoGroups——個人挑戰模式沒有「選分組」的概念，
// 所有挑戰者實質上共用同一個內部分組。僅在完全沒有分組時補；已有分組（例如舊資料）則不動。
func ensurePersonalDefaultGroup(req *CreateRaceRequest) {
	if req.EventMode == "personal" && len(req.Groups) == 0 {
		req.Groups = append(req.Groups, RaceGroup{Name: personalDefaultGroupName, GenderLimit: "any"})
	}
}

// CreateRaceFull 建立含巢狀分組/加購/物資的賽事（後台新增賽事用）。
func (s *Service) CreateRaceFull(ctx context.Context, req *CreateRaceRequest) (*RaceDetail, error) {
	if err := normalizeRequest(req); err != nil {
		return nil, err
	}
	ensurePersonalDefaultGroup(req)
	req.ReviewStatus = "approved"
	detail, err := s.repo.CreateWithChildren(ctx, req)
	if err == nil {
		s.InvalidateRaceMetaCache() // 新賽事直接 approved 上線，可能立刻被 SSR 抓到
	}
	return detail, err
}

// UpdateRaceFull 更新含巢狀分組/加購/物資的賽事（後台編輯賽事用）。
func (s *Service) UpdateRaceFull(ctx context.Context, raceID string, req *CreateRaceRequest) (*RaceDetail, error) {
	existing, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrRaceNotFound
	}
	// 編輯時若未指定 status，沿用原值（須在 normalize 預設成 soon 之前判斷）
	statusSpecified := req.Status != ""
	if err := normalizeRequest(req); err != nil {
		return nil, err
	}
	if !statusSpecified {
		req.Status = existing.Status
	}
	detail, err := s.repo.UpdateWithChildren(ctx, raceID, req)
	if err == nil {
		s.InvalidateRaceMetaCache()
	}
	return detail, err
}

// GetRaceDetail 取得賽事 + 巢狀子資料（後台編輯載入用）
func (s *Service) GetRaceDetail(ctx context.Context, raceID string) (*RaceDetail, error) {
	detail, err := s.repo.GetDetail(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, ErrRaceNotFound
	}
	return detail, nil
}

// GetCompetitionRanking 取得競賽分組排行榜（兩個榜 + 使用者所屬分組名次）。
// 累積榜：總里程 DESC；完成時間榜：finish_total_s ASC（0=尚無紀錄，排最後）。
func (s *Service) GetCompetitionRanking(ctx context.Context, raceID, userID string) (*CompetitionRanking, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil {
		return nil, ErrRaceNotFound
	}

	standings, err := s.repo.GetStandings(ctx, raceID)
	if err != nil {
		return nil, err
	}

	// 累積里程榜：里程多者在前
	cumulative := make([]GroupStanding, len(standings))
	copy(cumulative, standings)
	sort.SliceStable(cumulative, func(i, j int) bool {
		return cumulative[i].TotalKm > cumulative[j].TotalKm
	})

	// 完成時間榜：時間少者在前，但 0（尚無紀錄）排到最後
	finish := make([]GroupStanding, len(standings))
	copy(finish, standings)
	sort.SliceStable(finish, func(i, j int) bool {
		a, b := finish[i].FinishTotalS, finish[j].FinishTotalS
		if a == 0 {
			return false
		}
		if b == 0 {
			return true
		}
		return a < b
	})

	// 記下每個分組在兩榜的名次（給 my_group 用）
	cumRankByGroup := make(map[string]int, len(cumulative))
	for i, g := range cumulative {
		cumRankByGroup[g.GroupID] = i + 1
	}
	finRankByGroup := make(map[string]int, len(finish))
	for i, g := range finish {
		finRankByGroup[g.GroupID] = i + 1
	}

	result := &CompetitionRanking{
		RaceID:       raceID,
		EventMode:    race.EventMode,
		GoalType:     race.GoalType,
		ByCumulative: toRanked(cumulative, 20),
		ByFinishTime: toRanked(finish, 20),
	}

	// 使用者所屬分組名次
	if userID != "" {
		gid, err := s.repo.GetUserGroupID(ctx, userID, raceID)
		if err != nil {
			return nil, err
		}
		if gid != "" {
			for _, g := range standings {
				if g.GroupID == gid {
					result.MyGroup = &MyGroupRank{
						GroupID:        g.GroupID,
						GroupName:      g.GroupName,
						CumulativeRank: cumRankByGroup[gid],
						FinishRank:     finRankByGroup[gid],
						TotalKm:        g.TotalKm,
					}
					break
				}
			}
		}
	}

	return result, nil
}

// toRanked 將排序後的成績轉成含名次、最多 limit 筆的榜單
func toRanked(sorted []GroupStanding, limit int) []StandingRank {
	if limit > len(sorted) {
		limit = len(sorted)
	}
	out := make([]StandingRank, limit)
	for i := 0; i < limit; i++ {
		out[i] = StandingRank{Rank: i + 1, GroupStanding: sorted[i]}
	}
	return out
}

// ListPresets 取得分組預設選單
func (s *Service) ListPresets(ctx context.Context) ([]GroupPreset, error) {
	return s.repo.ListPresets(ctx)
}

// CreatePreset 新增分組預設（後台擴充選單）
func (s *Service) CreatePreset(ctx context.Context, name string, distanceKm *float64) (*GroupPreset, error) {
	return s.repo.CreatePreset(ctx, name, distanceKm)
}

// --- 任務模組（全站共用範本）---

func validateModule(m *TaskModule) error {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return ErrTaskModuleName
	}
	for i := range m.Items {
		it := &m.Items[i]
		if err := validateTaskMetric(it.MetricType, it.TargetValue, it.RangeLo, it.RangeHi); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
		if it.Title == "" {
			it.Title = MetricCatalog[it.MetricType].Label
		}
	}
	return nil
}

func (s *Service) ListTaskModules(ctx context.Context) ([]TaskModule, error) {
	return s.repo.ListTaskModules(ctx)
}

func (s *Service) GetTaskModule(ctx context.Context, id string) (*TaskModule, error) {
	return s.repo.GetTaskModule(ctx, id)
}

func (s *Service) CreateTaskModule(ctx context.Context, m *TaskModule) (*TaskModule, error) {
	if err := validateModule(m); err != nil {
		return nil, err
	}
	return s.repo.CreateTaskModule(ctx, m)
}

func (s *Service) UpdateTaskModule(ctx context.Context, id string, m *TaskModule) (*TaskModule, error) {
	if err := validateModule(m); err != nil {
		return nil, err
	}
	updated, err := s.repo.UpdateTaskModule(ctx, id, m)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrTaskModuleNotFound
	}
	return updated, nil
}

func (s *Service) DeleteTaskModule(ctx context.Context, id string) error {
	ok, err := s.repo.DeleteTaskModule(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrTaskModuleNotFound
	}
	return nil
}

// CreateRaceWithReview 合作方提交賽事，指定審核狀態（pending）
func (s *Service) CreateRaceWithReview(ctx context.Context, race *Race, reviewStatus string) (*Race, error) {
	race.ReviewStatus = reviewStatus
	created, err := s.repo.Create(ctx, race)
	if err == nil {
		// 通常是 pending（合作方提交，尚未上線），meta 表不會收錄；仍一併清快取——萬一呼叫端
		// 直接帶 approved 進來（例如未來擴充），不會漏了這一筆。
		s.InvalidateRaceMetaCache()
	}
	return created, err
}

// DeleteRace 刪除賽事（admin 用）。有報名的賽事不可刪，其餘連同子資料一併移除。
func (s *Service) DeleteRace(ctx context.Context, raceID string) error {
	existing, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrRaceNotFound
	}
	n, err := s.repo.CountRegistrations(ctx, raceID)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrRaceHasRegistrations
	}
	if err := s.repo.Delete(ctx, raceID); err != nil {
		return err
	}
	s.InvalidateRaceMetaCache()
	return nil
}

// UpdateFactionKm 更新陣營累積里程（activity upload 後呼叫）
func (s *Service) UpdateFactionKm(ctx context.Context, raceID, faction string, addKm float64) error {
	if faction == "" {
		return nil
	}
	factionKmKey := "race:" + raceID + ":faction_km"
	return s.rdb.HIncrByFloat(ctx, factionKmKey, faction, addKm).Err()
}

// --- helpers ---

// assignFactionBalanced 隨機分配但儘量維持各陣營人數平衡
func (s *Service) assignFactionBalanced(ctx context.Context, raceID string, factions []FactionDef) string {
	if len(factions) == 0 {
		return ""
	}
	// 取各陣營人數（Redis 記錄）
	countKey := "race:" + raceID + ":faction_count"
	counts := s.rdb.HGetAll(ctx, countKey).Val()

	minCount := int64(^uint64(0) >> 1) // MaxInt64
	minFaction := factions[0].ID

	for _, f := range factions {
		c := int64(0)
		if v, ok := counts[f.ID]; ok {
			c, _ = strconv.ParseInt(v, 10, 64)
		}
		if c < minCount {
			minCount = c
			minFaction = f.ID
		}
	}

	// 若各陣營人數相同，純隨機
	allEqual := true
	for _, f := range factions {
		c := int64(0)
		if v, ok := counts[f.ID]; ok {
			c, _ = strconv.ParseInt(v, 10, 64)
		}
		if c != minCount {
			allEqual = false
			break
		}
	}
	if allEqual {
		minFaction = factions[rand.Intn(len(factions))].ID
	}

	// 更新計數
	s.rdb.HIncrBy(ctx, countKey, minFaction, 1)
	return minFaction
}

// GetRegistrationForUser 取得使用者在某賽事的報名記錄（供其他模組呼叫）
func (s *Service) GetRegistrationForUser(ctx context.Context, userID, raceID string) (*Registration, error) {
	return s.repo.GetRegistration(ctx, userID, raceID)
}

// AdminListSignups 列出某賽事報名（admin 用，舊版相容）
func (s *Service) AdminListSignups(ctx context.Context, raceID string) ([]*Registration, error) {
	return s.repo.ListRegistrations(ctx, raceID)
}

// --- 後台報名 / 訂單管理 ---

func (s *Service) ListSignups(ctx context.Context, raceID, q string, hideVirtual bool, statuses []string) ([]SignupRow, error) {
	return s.repo.ListSignups(ctx, raceID, q, hideVirtual, statuses)
}

// ListRaceGroups 後台報名管理用：取分組（含名額上限/已用），並清掉鑰匙明碼。
func (s *Service) ListRaceGroups(ctx context.Context, raceID string) ([]RaceGroup, error) {
	gs, err := s.repo.GetGroups(ctx, raceID)
	if err != nil {
		return nil, err
	}
	for i := range gs {
		gs[i].GroupKey = ""
	}
	return gs, nil
}

// ChangeSignupGroup 後台調整某報名的分組（額滿擋下）。
func (s *Service) ChangeSignupGroup(ctx context.Context, regID, groupID string) error {
	return s.repo.ChangeSignupGroup(ctx, regID, groupID)
}

func (s *Service) ListOrders(ctx context.Context, raceID, status string, limit, offset int, hideVirtual bool) ([]OrderRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListOrders(ctx, raceID, status, limit, offset, hideVirtual)
}

func (s *Service) GetOrderDetail(ctx context.Context, orderID string) (*OrderDetail, error) {
	d, err := s.repo.GetOrderDetail(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrOrderNotFound
	}
	return d, nil
}

func (s *Service) MarkOrderPaid(ctx context.Context, orderID, paymentRef string) error {
	return s.repo.MarkOrderPaid(ctx, orderID, paymentRef)
}

// MarkOrderRefunded 標記訂單已退款（供 payment.OrderMarker 介面使用）
func (s *Service) MarkOrderRefunded(ctx context.Context, orderID string) error {
	return s.repo.MarkOrderRefunded(ctx, orderID)
}

func (s *Service) MarkRegistrationPaid(ctx context.Context, regID string) error {
	return s.repo.MarkRegistrationPaid(ctx, regID)
}

// computeDayNow 計算目前是賽事第幾天（1-indexed，賽前為 0）
func (s *Service) computeDayNow(race *Race) int {
	if race.Status == "soon" || race.Status == "open" {
		return 0
	}
	if race.Status == "done" {
		return int(race.EndDate.Sub(race.StartDate).Hours()/24) + 1
	}
	day := int(time.Since(race.StartDate).Hours()/24) + 1
	maxDay := int(race.EndDate.Sub(race.StartDate).Hours()/24) + 1
	if day > maxDay {
		return maxDay
	}
	return day
}
