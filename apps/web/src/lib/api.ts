// API client — 封裝所有對 Go API 的呼叫

const BASE = '/api/v1'

export interface TokenPair {
  access_token: string
  refresh_token: string
  expires_in: number
  session_epoch: number // 單一登入：這次登入/refresh 的 session 版本號，後端每次登入會遞增並用 WS session_revoked 通知舊 session
}

export interface User {
  id: string
  email: string
  handle: string
  name: string
  avatar_url: string
  total_km: number
}

export type EventMode = 'general' | 'competition' | 'faction_battle' | 'personal'
export type GoalType = 'cumulative' | 'distance'

// --- 個人挑戰模式（event_mode=personal）規則（見後端 race.ChallengeRule） ---
export type CompletionType = 'streak_days' | 'window_cumulative' | 'single_distance'
export interface ChallengeRule {
  completion_type: CompletionType
  days?: number           // streak_days：連續天數
  min_km_per_day?: number // streak_days：每天最低里程 (km)
  daily_mode?: string     // streak_days：'cumulative'(當日累積,預設)|'single'(當日至少一趟達標)
  window_days?: number    // window_cumulative：視窗天數
  cum_km?: number         // window_cumulative：視窗內累積里程 (km)
  single_km?: number      // window_cumulative：至少一趟里程(km，選填)／single_distance：單趟里程(km，必填)
}

// formatChallengeRule 把個人挑戰規則組成人看得懂的一句話（報名頁/賽事詳情頁共用）。
export function formatChallengeRule(rule?: ChallengeRule | null): string {
  if (!rule) return ''
  switch (rule.completion_type) {
    case 'streak_days':
      return `連續 ${rule.days ?? 0} 天，每天${rule.daily_mode === 'single' ? '至少一趟' : '累積里程'} ≥ ${rule.min_km_per_day ?? 0} km`
    case 'window_cumulative': {
      const base = `${rule.window_days ?? 0} 天內累積跑步里程 ≥ ${rule.cum_km ?? 0} km`
      return rule.single_km && rule.single_km > 0 ? `${base}，且其中至少一趟單次里程 ≥ ${rule.single_km} km` : base
    }
    case 'single_distance':
      return `單次跑步里程 ≥ ${rule.single_km ?? 0} km`
    default:
      return ''
  }
}

// --- 活動獎勵系統 P2：即時獎勵設定（全域模板 + 每場挑戰 config）（見後端 activityreward.RewardConfig） ---
export type RewardItemType = 'exp' | 'dp' | 'gp' | 'vip' | 'serial' | 'coupon'
// RewardDenom serial 類：商家旗下一個序號組（面額）在兩層抽獎第二層的加權設定（見後端 activityreward.RewardDenom）。
export interface RewardDenom {
  group_id: string
  weight: number
}
// 組合型序號組子項（migration 150）：組合型序號組（reward_serial_groups.is_bundle=true）不自己存
// 序號，而是定義成「子面額組 × 數量」的固定組合（如「LINE POINTS 3000」= LINE POINTS 1000 × 3）。
// 發放此組合型序號組時，系統從各子面額組原子搶出對應數量的序號、綁同一 bundle_id 給玩家（錢包併成
// 一張卡，展開看零散序號）。組合定義在序號管理頁，不在賽事即時獎勵設定——賽事只是把它當一個普通面額選。
export interface RewardGroupBundleItem {
  child_group_id: string // 子面額組（reward_serial_groups.id，須為非組合型）
  count: number          // 這個子面額發幾張（≥1）
}
export interface RewardItem {
  type: RewardItemType
  min?: number            // exp/dp/gp：均勻隨機區間下界（含）
  max?: number             // exp/dp/gp：均勻隨機區間上界（含）
  days?: number            // vip：固定天數
  prob_bp: number          // 中獎機率，萬分位（10000=100%）；serial：該商家「給不給獎」的機率
  serial_group_id?: string // 【已過時，僅供向後相容】serial 舊格式單一序號組；新設定請用 merchant_id + denominations
  merchant_id?: string      // serial：指定商家（兩層抽獎第一層）
  denominations?: RewardDenom[] // serial：該商家旗下序號組與抽獎權重（兩層抽獎第二層，加權隨機抽一個）；某面額組若為組合型序號組，抽中時自動拆解全發
  coupon_def_id?: string    // coupon（活動優惠券，migration 138）：指定券種
  hidden?: boolean          // 前台「活動獎勵」預覽頁籤是否隱藏此項目（true=隱藏，發獎不受影響）
}
export interface RewardConfig {
  items: RewardItem[]
}

// --- 個人挑戰模式 P3：完成判定引擎（見後端 race.GetPersonalProgress） ---
export interface ChallengeProgress {
  completion_type: CompletionType
  streak_days?: number      // streak_days：目前最長連續達標天數
  target_days?: number
  cum_km?: number            // window_cumulative：累積里程
  target_cum_km?: number
  best_single_km?: number    // window_cumulative/single_distance：目前最長單趟
  target_single_km?: number
  window_ends_at?: string
}
export interface PersonalProgress {
  has_attempt: boolean
  status?: string // pending|paid|completed|expired（has_attempt=true 才有意義）
  rule?: ChallengeRule
  progress?: ChallengeProgress
  completed_count: number
  newly_granted?: GrantedReward[] // 本次呼叫剛觸發完成時發放的即時獎勵（活動獎勵系統 P2；P3 前端彈窗才用）
}

// GPS 跑步追蹤頁「進行中活動/賽事」面板：目前登入者「這筆 GPS 跑步、現在跑會被計入」的賽事/挑戰
// 清單 + 各自進度（見後端 race.GetMyActiveRaces / GET /races/my-active）。
export interface MyActiveRace {
  id: string
  slug: string
  title: string
  event_mode: EventMode
  start_date: string
  end_date: string
  my_total_km: number
  my_activities: number
  tasks_done: number  // 非 personal 適用；personal 恆為 0
  tasks_total: number // 非 personal 適用；personal 恆為 0
  challenge_rule?: ChallengeRule         // personal 專用
  challenge_progress?: ChallengeProgress // personal 專用
  attempt_no?: number                    // personal 專用（reg.attempt_no）
}

// GrantedReward 一次完成觸發 roll 中「實際中獎並成功發放」的單筆結果（見後端 activityreward.GrantedReward）。
export interface GrantedReward {
  type: RewardItemType
  amount?: number     // exp/dp/gp 數量；coupon 為面額（分）
  days?: number        // vip
  item_label?: string  // serial 品項名稱；coupon 為券種名稱
  code?: string        // serial
}

// --- 活動獎勵系統 P3：玩家活動獎勵錢包（序號類 + 活動優惠券類，migration 138；見後端 profile.UserReward） ---
export interface UserReward {
  id: string
  source_type: string
  source_race_id?: string
  source_reg_id?: string
  kind: 'serial' | 'coupon' // 活動優惠券擴充（migration 138）；既有序號類舊資料一律 'serial'
  code: string
  link?: string
  item_label: string
  merchant_name?: string
  usage_note?: string
  icon_url?: string
  description?: string
  coupon_def_id?: string // kind='coupon' 專用
  amount_cents?: number  // kind='coupon' 專用：面額（分）
  // 組合包（migration 149）：同一次組合包發放的多張序號共用同一 bundle_id，前台錢包 group by
  // bundle_id 併成一張卡（bundle_label 顯示名如「LINE POINTS 3500」、bundle_total 總面額）。
  // 非組合包（單張序號獎勵）bundle_id 為空，維持一列一卡。
  bundle_id?: string
  bundle_label?: string
  bundle_total?: number
  valid_from?: string
  valid_until?: string
  used: boolean
  used_at?: string
  obtained_at: string
}

export const rewardsApi = {
  // 只回登入者自己的序號類/活動優惠券類活動獎勵；排序（近到期置頂+外框、其餘新到舊）由前端依全量資料自行分組。
  list: (token: string) => request<{ rewards: UserReward[] }>('/profile/rewards', { headers: withAuth(token) }),
  // 標記某筆已使用（冪等，只能改自己的）；coupon 類不提供此按鈕（券由報名系統自動核銷，見 RewardsWalletScreen）。
  markUsed: (token: string, id: string) =>
    request<{ ok: boolean; used: boolean; used_at?: string }>(`/profile/rewards/${id}/use`, { method: 'POST', headers: withAuth(token) }),
}

// --- 個人挑戰模式 P4：排行榜（依完成次數 desc、最早完成時間 asc；見後端 race.GetPersonalLeaderboard） ---
export interface PersonalLeaderRow {
  rank: number
  user_id: string
  name: string
  avatar: string
  completed_count: number
  first_completed_at: string
  is_following: boolean
  is_me: boolean
}
export interface PersonalLeaderboard {
  leaderboard: PersonalLeaderRow[]
  my_rank: number  // 登入者在榜上的名次；未完成過或未登入 = 0
  my_count: number
}

// --- 個人挑戰模式「完賽歷程」（取代一般模式完賽證明；見後端 race.GetPersonalHistory） ---
export interface PersonalHistory {
  total_attempts: number  // 總報名次數（不論結果）
  completed_count: number // 已完成挑戰次數
  best_metric?: 'duration' | 'distance' | '' // 空＝尚無完成紀錄；duration=最短用時、distance=最佳距離
  best_duration_s?: number  // best_metric=duration 時有效
  best_distance_km?: number // best_metric=distance 時有效
  last_completed_at?: string // 最近一次完成時間
}

// formatChallengeProgress 把個人挑戰進行中的即時進度組成人看得懂的一句話（賽事詳情頁用）。
export function formatChallengeProgress(p?: ChallengeProgress | null): string {
  if (!p) return ''
  switch (p.completion_type) {
    case 'streak_days':
      return `連續 ${p.streak_days ?? 0} / ${p.target_days ?? 0} 天`
    case 'window_cumulative': {
      const base = `累積 ${(p.cum_km ?? 0).toFixed(1)} / ${(p.target_cum_km ?? 0).toFixed(1)} km`
      return p.target_single_km && p.target_single_km > 0
        ? `${base}・最長單趟 ${(p.best_single_km ?? 0).toFixed(1)} / ${p.target_single_km.toFixed(1)} km`
        : base
    }
    case 'single_distance':
      return `目前最佳單趟 ${(p.best_single_km ?? 0).toFixed(1)} / ${(p.target_single_km ?? 0).toFixed(1)} km`
    default:
      return ''
  }
}

export interface Race {
  id: string
  slug: string
  title: string
  subtitle: string
  world: string
  blurb: string
  hero_image_url: string
  status: 'live' | 'open' | 'soon' | 'done'
  event_mode: EventMode
  goal_type: GoalType
  distances: number[]
  group_type: string
  group_mode: string
  slots_total: number
  entry_fee: number // 分；fee_mode=per_group 時語意為「預設報名費」（未獨立設定的組別、前台新增組別適用）
  fee_mode: 'uniform' | 'per_group' // uniform(預設，全場統一用 entry_fee) | per_group(各分組可獨立定價，見 RaceGroup.entry_fee_cents)
  // 計算欄位（後端讀取時算好填入）：「列表/摘要情境」（尚未選定分組）該顯示的報名費。uniform＝entry_fee；
  // per_group＝各組有效報名費（COALESCE(組獨立價, entry_fee)）的最小值，沒有任何分組則回退 entry_fee。
  // 列表卡／後台列表一律改用本欄位（per_group 顯示「NT$ {此值} 起」），不要再用 entry_fee 近似。
  // 報名頁/詳情頁「已選組別」情境仍走 effectiveGroupFee，不受影響。
  display_fee_cents: number
  registration_start?: string | null
  registration_end?: string | null
  start_date: string
  end_date: string
  required_fields: string[]
  brochure_title?: string
  control_status: ControlStatus
  starting_soon_days: number
  allow_team_groups?: boolean
  display_status: DisplayStatus
  can_register: boolean
  // 計算欄位（等同 control_status==='testing'）：只有「本來就看得到這場測試賽事的人」（白名單）才會收到
  // true——後端 ListPublic/GetPublicDetail 已先過濾掉看不到的人，不是獨立的資訊外洩面。前台用來顯示
  // 「🧪 測試中」識別標籤（RacesScreen 卡片／RaceDetailScreen 狀態列）。
  is_testing?: boolean
  review_status: string
  certificate_bg_url?: string
  show_distance_rank?: boolean
  show_time_rank?: boolean
  vip_only?: boolean // VIP 限定賽事（只提供給 VIP 帳號）
  external_data?: boolean // 是否採用手錶外部數據（garmin/coros/polar/suunto/wahoo）做排名/里程統計；Strava 一律排除（gate v2）；後台新賽事表單預設 true
  config?: RaceConfig // 後端一律回傳（非 omitempty）；此處選填僅為前端防禦
  challenge_rule?: ChallengeRule | null // 個人挑戰模式(event_mode=personal)專用規則；其餘模式為 null
  reward_config?: RewardConfig | null // 個人挑戰模式(event_mode=personal)完成觸發即時獎勵設定；其餘模式為 null，選填
  // 參賽虛擬獎勵設定（migration 140）：沿用同一 RewardConfig 結構，但觸發條件完全不同——不看任何任務/
  // 完成條件，賽事開始後由後端排程自動發給所有已報名(paid)者（見後端 race.EntryRewardConfig）。公開端點
  // （ListPublic/GetPublicDetail）一律清空，前台改走 racesApi.entryRewardPreview 取得展示用清單；選填。
  entry_reward_config?: RewardConfig | null
  created_at: string
  // 寵物雲端馬拉松（2026-09-08）：''=一般賽事（不影響既有行為）、'dog'/'cat'=寵物賽事。
  // pet_max_per_reg 含基本名額（1..20 上限，UI 於等於 20 時鎖住加購 stepper）；pet_base_slots 目前恆為 1，仍照後端回傳值使用不寫死。
  pet_kind: '' | 'dog' | 'cat'
  pet_max_per_reg: number
  pet_base_slots: number
  // 寵物成績規則（僅 pet_kind!=='' 時有意義；非寵物賽事恆為 ''=不影響既有行為）：
  // ''=依飼主里程（預設，等同無寵物功能前）／'pet'=狗狗累積里程／'owner_pet_sum'=飼主＋狗狗里程加總。
  // 實際生效規則仍受分組 for_owner/for_pet 限制（for_pet=false→視同'pet'不可能→退化 owner；
  // for_owner=false 時 owner_pet_sum 退化為 pet），前端顯示以後端算好的 LeaderboardRow/RaceProgress
  // 各欄位為準，這裡的欄位只用於表單編輯與「賽事設定」原樣顯示。
  pet_score_mode: '' | 'pet' | 'owner_pet_sum'
}

// --- 取消退費政策（見後端 race.CancellationPolicy／race.ResolveCancellationPolicy）---
export interface CancellationTier {
  days_before: number // 距賽事開始 >= 此天數
  ratio: number        // 退費百分比 0–100
}
export interface CancellationPolicy {
  deadline_days: number // 賽事開始前幾天截止申請取消
  tiers: CancellationTier[] // 依 days_before 由大到小比對，取第一個符合的 ratio
}
// 賽事 JSONB config：目前前端僅提供 cancellation_policy 的編輯 UI；factions/clubs/missions
// 尚無編輯介面，用索引簽章原樣保留這些既有欄位，往返送出時不誤刪（見後端 configToBytes/bytesToConfig
// 是整個 struct marshal，任何未帶到的欄位都會被清空）。
export interface RaceConfig {
  cancellation_policy?: CancellationPolicy | null // null／不覆寫＝繼承系統預設
  refund_disabled?: boolean // true＝此活動不提供退費（玩家仍可申請取消釋出名額，但退費為 0；簡章不顯示退費規則）
  // certificate_disabled：true＝此賽事不顯示完賽證明／完賽歷程區塊（一般模式的「完賽證明」按鈕與
  // personal 模式取代它的「完賽歷程」按鈕皆隱藏）。後端 certificate／personal-history 端點同步擋
  // （403），防止繞過前端隱藏直接呼叫 API。
  certificate_disabled?: boolean
  // cert_layout：完賽證明可視化排版覆寫（後台 RaceForm 拖曳編輯器產出）。key 為元素識別碼（見
  // '@/lib/certificate' 的 CERT_DEFAULT_LAYOUT），只存「被改過（≠預設）」的 key，未設定一律 fallback
  // 模板預設值。
  cert_layout?: Record<string, CertElementLayout>
  [key: string]: unknown
}

// CertElementLayout 完賽證明單一元素的座標/字級（見後端 race.CertElementLayout）。X/Y 為畫布寬高比例
// （0–1，元素中心/基準點），Size 為該元素主文字字級（px）。
export interface CertElementLayout {
  x: number
  y: number
  size: number
}

export type ControlStatus = 'active' | 'paused' | 'suspended' | 'closed' | 'hidden' | 'testing'
export type DisplayStatus =
  | 'upcoming_reg' | 'registering' | 'reg_closed'
  | 'starting_soon' | 'racing' | 'ended'
  | 'paused' | 'suspended'

// raceStatusFlags：把「報名中」「進行中」拆成兩個獨立、可同時成立的布林（而非單一 display_status 互斥），
// 給活動列表(RacesScreen)/賽事詳情(RaceDetailScreen)/後台數據總覽(admin/overview)共用，讓「活動期間也開放
// 報名」的賽事（目前是個人挑戰模式，未來可能更多）能同時顯示/篩選到「進行中」＋「報名中」。
// - ended：活動期間已結束（依 end_date，與 display_status==='ended' 等價，但不受 control_status 影響）
// - ongoing：活動期間內（依 start_date～end_date）；後端 control_status=paused/suspended 會不分日期強制覆寫
//   display_status（見 race.ComputeDisplay），故排除這兩種狀態，避免「賽事中止/暫停」在事件日期範圍內
//   仍被誤判成「進行中」（例如比賽中途因故中止，仍會落在 start_date~end_date 之間）。
// - regOpen：後端已算好「現在可否報名」（can_register，含報名窗/控制狀態；個人挑戰活動中為 true）
// 一般賽事活動中 can_register 恆為 false，故只會顯示 ongoing，與改版前呈現等價；
// 個人挑戰活動中 ongoing && regOpen 同時成立，兩者並存顯示。
// 參數只取結構子集（非完整 Race），讓 admin/overview 的輕量 OverviewRace（同樣含這 4 欄）也能重用同一套判定。
export interface RaceStatusLike {
  display_status: string
  start_date: string
  end_date: string
  can_register: boolean
}
export function raceStatusFlags(race: RaceStatusLike): { ended: boolean; ongoing: boolean; regOpen: boolean } {
  const now = Date.now()
  const ended = now >= new Date(race.end_date).getTime()
  const overridden = race.display_status === 'paused' || race.display_status === 'suspended'
  const ongoing = !ended && !overridden && now >= new Date(race.start_date).getTime()
  return { ended, ongoing, regOpen: race.can_register }
}

// RACE_FILTER_CATEGORY：搜尋標籤分類 fallback，把 display_status 歸到 報名中/進行中/已結束。
// 與 raceStatusFlags 搭配使用（`flags.xxx || RACE_FILTER_CATEGORY[display_status] === 'xxx'`），
// 確保一般賽事在每個 display_status 下都落入正確分類，不因改採日期判斷而漏篩。
// 前台(RacesScreen)/後台(admin/overview)共用同一份，避免兩邊各自維護的判定跑掉。
export type RaceFilterCategory = 'reg' | 'racing' | 'ended'
export const RACE_FILTER_CATEGORY: Record<string, RaceFilterCategory> = {
  upcoming_reg: 'reg', registering: 'reg', paused: 'reg',
  reg_closed: 'racing', starting_soon: 'racing', racing: 'racing',
  ended: 'ended', suspended: 'ended',
}

export type ParticipantField = 'real_name' | 'nickname' | 'phone' | 'address' | 'birthday' | 'gender'

export interface RaceGroup {
  id?: string
  name: string
  description?: string
  display_order: number
  slot_limit?: number | null
  slots_taken?: number
  gender_limit: 'any' | 'male' | 'female'
  age_min?: number | null
  age_max?: number | null
  target_distance_km?: number | null
  requires_key?: boolean
  group_key?: string // 後台編輯時可帶；公開回傳一律為空
  created_by?: string
  is_user_created?: boolean
  exp_reward?: number // 完成此分組可獲得的 EXP
  dp_reward?: number // 完成此分組可獲得的 DP
  // 該組獨立報名費（分）；僅 race.fee_mode='per_group' 時生效，null/undefined=沿用 race.entry_fee（預設報名費）。
  // 前台跑團成員自建分組一律不會帶這個欄位（無定價權，永遠沿用預設）；僅後台官方分組可設定。
  entry_fee_cents?: number | null
  // 寵物雲端馬拉松（2026-09-08）：此分組是否計入飼主／寵物成績（供未來計分邏輯使用，目前僅後台可勾選、
  // 前台不消費）；後端預設兩者皆 true，前台跑團成員自建分組不帶這兩欄，一律沿用後端預設。
  for_owner?: boolean
  for_pet?: boolean
}

// effectiveGroupFee 是「有效組價」的單一事實來源（分），與後端 race.EffectiveGroupFee 對應一致：
// fee_mode='per_group' 且該組設有獨立報名費時用該組獨立價；其餘情況（uniform 模式，或 per_group 下
// 該組未設定/尚未選定分組）一律回退 race.entry_fee 作為預設報名費。所有前台計價/顯示點都應呼叫本函式，
// 不得直接讀 race.entry_fee。
export function effectiveGroupFee(race: Race, group?: RaceGroup | null): number {
  if (race.fee_mode === 'per_group' && group && group.entry_fee_cents != null) {
    return group.entry_fee_cents
  }
  return race.entry_fee
}

export interface RaceAddon {
  id?: string
  name: string
  description?: string
  image_url?: string
  price_cents: number
  per_user_limit?: number | null
  total_stock?: number | null
  display_order: number
  active: boolean
  // 寵物雲端馬拉松（2026-09-08）：'pet_slot'＝加購寵物參賽名額（每份 qty +1 隻），每場賽事至多一個；
  // 其餘一律 'item'（一般加購品項，既有行為）。舊資料/未帶＝視同 'item'。
  kind?: 'item' | 'pet_slot'
}

export interface RaceSupply {
  id?: string
  group_id?: string // 回傳時的實際 UUID（空=共用）
  group_index?: number | null // 建立時對應 groups 陣列索引（null=共用）
  kind: 'race_pack' | 'finisher'
  name: string
  description?: string
  image_url?: string
  display_order: number
}

export interface BrochureBlock {
  id?: string
  block_type: 'text' | 'image' | 'video'
  content: string
  caption?: string
  display_order: number
}

// 簡章「圖片」區塊 content 陣列（或單一字串）裡，一張圖片的結構。
// 每張圖可選填 caption（顯示在圖片下方一行說明）與 link（點擊導向，站內/站外皆可）。
export interface BrochureImageItem {
  url: string
  caption?: string
  link?: string
}

// 正規化單一圖片項目：相容舊格式（陣列元素是純字串網址＝url）與新格式（物件）。
// 讀取端（前台渲染／後台編輯）一律先經此函式，未來格式演進只需改這一處。
export function normalizeBrochureImage(raw: string | BrochureImageItem | null | undefined): BrochureImageItem {
  if (typeof raw === 'string') return { url: raw }
  if (raw && typeof raw === 'object' && typeof raw.url === 'string') {
    return { url: raw.url, caption: raw.caption || undefined, link: raw.link || undefined }
  }
  return { url: '' }
}

// --- 賽事任務系統 ---
export type MetricType =
  | 'cumulative_distance' | 'single_distance' | 'daily_distance' | 'streak_days'
  | 'weekly_distance' | 'avg_pace_range' | 'checkpoint' | 'cumulative_ascent' | 'single_ascent' | 'avg_hr_range'
export type TaskScope = 'race_collective' | 'group_team' | 'group_individual'

export interface MetricSpec {
  key: MetricType
  label: string
  unit: string
  kind: 'threshold' | 'range' | 'checkpoint'
  has_data: boolean
}

// 前端鏡像後端 MetricCatalog（順序、文案一致）
export const METRIC_CATALOG: MetricSpec[] = [
  { key: 'cumulative_distance', label: '累計總里程', unit: 'km', kind: 'threshold', has_data: true },
  { key: 'single_distance', label: '單次里程', unit: 'km', kind: 'threshold', has_data: true },
  { key: 'daily_distance', label: '每日里程', unit: 'km', kind: 'threshold', has_data: true },
  { key: 'streak_days', label: '連續進行任務天數', unit: '天', kind: 'threshold', has_data: true },
  { key: 'weekly_distance', label: '每週總里程', unit: 'km', kind: 'threshold', has_data: true },
  { key: 'avg_pace_range', label: '平均配速區間', unit: '秒/km', kind: 'range', has_data: true },
  { key: 'checkpoint', label: '指定地點打卡', unit: '點', kind: 'checkpoint', has_data: true },
  { key: 'cumulative_ascent', label: '累積爬升海拔', unit: 'm', kind: 'threshold', has_data: false },
  { key: 'single_ascent', label: '單次爬升海拔', unit: 'm', kind: 'threshold', has_data: false },
  { key: 'avg_hr_range', label: '平均心率區間', unit: 'bpm', kind: 'range', has_data: false },
]
export const METRIC_BY_KEY: Record<string, MetricSpec> = Object.fromEntries(METRIC_CATALOG.map((m) => [m.key, m]))

export interface Checkpoint {
  id?: string
  lat: number
  lng: number
  radius_m: number
  title?: string
  display_order: number
  collected?: boolean // 進度用：已通過審核打卡
  pending?: boolean   // 進度用：已打卡待審
}

export interface RaceTask {
  id?: string
  scope: TaskScope
  group_id?: string
  group_index?: number | null // 建立時對應 groups 陣列索引（race_collective 為 null）
  metric_type: MetricType
  target_value?: number | null
  range_lo?: number | null
  range_hi?: number | null
  title: string
  description?: string
  display_order: number
  checkpoints?: Checkpoint[] // metric_type=checkpoint 時的打卡點清單
}

export interface TaskModuleItem {
  id?: string
  metric_type: MetricType
  target_value?: number | null
  range_lo?: number | null
  range_hi?: number | null
  title: string
  description?: string
  display_order: number
}

export interface TaskModule {
  id: string
  name: string
  description?: string
  is_system: boolean
  items: TaskModuleItem[]
}

export interface RaceDetail extends Race {
  groups: RaceGroup[]
  addons: RaceAddon[]
  supplies: RaceSupply[]
  test_whitelist: string[]
  brochure: BrochureBlock[]
  tasks: RaceTask[]
  // 後端 GetPublicDetail 解析好的最終生效取消退費政策（賽事覆寫→系統預設→內建預設），簡章頁尾
  // 「取消退費規則」表格用；跟實際退費計算共用同一顆後端函式，顯示不會兜不起來。後台編輯用的
  // GetDetail 不填此欄位。
  resolved_cancellation_policy?: CancellationPolicy | null
}

// 建立賽事的巢狀 payload（Race 基本欄位 + 子陣列）
export type CreateRacePayload = Partial<Race> & {
  groups: RaceGroup[]
  addons: RaceAddon[]
  supplies: RaceSupply[]
  test_whitelist?: string[]
  brochure?: BrochureBlock[]
  tasks?: RaceTask[]
}

export interface GroupPreset {
  id: string
  name: string
  default_distance_km?: number | null
  is_system: boolean
}

class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

// 401 自動回復：由 adminAuth 註冊（用 refresh 換新 token 後重試一次），以回呼註冊避免 api.ts↔adminAuth 循環依賴。
// 回傳新 token 才重試；回 null（非後台 token / 續期失敗）則照常拋 401，交給呼叫端處理。
type AuthRecovery = (failedToken: string) => Promise<string | null>
// 401 續期掛勾可以有多個（後台 token 由 adminAuth 註冊、會員 token 由 userAuth 註冊）：request() 依序詢問，
// 誰認得這把 token 就由誰續期。2026-09-05 之前只有後台註冊——會員端直接帶 token 的 SWR 抓取一遇 401
// 不會刷新、也不會登出，被 SWR 無限重試＋30 秒輪詢，一台被單一登入踢掉的裝置一天打出上千次 401
// （日報「登入失敗異常 IP 1172 次」＝ 846/846 全是 401 的那台）。
const authRecoveries: AuthRecovery[] = []
export function setAuthRecovery(fn: AuthRecovery | null) { if (fn) authRecoveries.push(fn) }
async function runAuthRecovery(failedToken: string): Promise<string | null> {
  for (const fn of authRecoveries) {
    try {
      const nt = await fn(failedToken)
      if (nt) return nt
    } catch { /* 單一掛勾失敗不影響其他掛勾 */ }
  }
  return null
}

async function request<T>(path: string, init?: RequestInit, retried = false): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })
  // 401 且尚未重試：若這次帶的是後台 token → 續期後用新 token 重試一次（避免 token 剛過期就被登出）
  if (res.status === 401 && !retried && authRecoveries.length > 0) {
    const h = init?.headers as Record<string, string> | undefined
    const auth = h?.Authorization // 所有呼叫都用 withAuth（大寫 Authorization），重試時原樣覆寫、不會產生重複 header
    const token = typeof auth === 'string' && auth.startsWith('Bearer ') ? auth.slice(7) : ''
    if (token) {
      const nt = await runAuthRecovery(token)
      if (nt) return request<T>(path, { ...init, headers: { ...init?.headers, Authorization: `Bearer ${nt}` } }, true)
    }
  }
  // 204 No Content 或空 body（如 DELETE / logout）不解析 JSON，避免 "Unexpected end of JSON input"
  const text = await res.text()
  let data: any = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }
  if (!res.ok) throw new ApiError(res.status, data?.error ?? 'request failed')
  return data as T
}

function withAuth(token: string): HeadersInit {
  return { Authorization: `Bearer ${token}` }
}

// --- 全站外觀設定 ---

export interface SiteSettings {
  member_panel_bg_url: string
  strava_powered_dark_url: string  // 深色 skin 用（白字版）
  strava_powered_light_url: string // 淺色 skin 用（深字版）
}

export const settingsApi = {
  get: () => request<{ settings: SiteSettings }>('/settings'),
}

export interface GpsRunResult {
  distance_km: number
  duration_s: number
  avg_pace_s: number
  flagged: boolean
  flag_reason?: string
  anomaly_segments: number
  exp_awarded: boolean
  too_short?: boolean
  km_paces?: number[] // 每公里分段配速（秒/km）；上傳後由後端回傳，結束畫面以此覆寫本地分段保持同源
  // GPS 距離校正（見 internal/gpscalib，2026-08-30）：raw_distance_km 為校正前原始距離、calib_factor 為
  // 上傳當下生效的係數（未套用恆 1.0）。distance_km/avg_pace_s/km_paces 皆已是「校正後」的值，前端不必
  // 再自己乘係數；這兩欄只用於顯示「原始 vs 校正後」對照。
  raw_distance_km: number
  calib_factor: number
  // 已排除的異常段（超速／訊號中斷跳點，見前端 apps/web/src/app/track/page.tsx 的 GAP_MAX_S/GAP_MAX_M）：
  // 後端對同一批上傳點套用相同規則重算，這趟一律回傳（無異常時為 0），前端結束畫面據此顯示提示。
  excluded_km: number
  excluded_segments: number
}
// speed：都卜勒速度 m/s（距離防漂移的訊號分流用，見 lib/movingTime.ts）；裝置不支援時為 null。
// 上傳相容：後端以 encoding/json 解析、忽略未知欄位，多帶 speed 不影響既有 API（後端零改動）。
export interface GpsPoint { lat: number; lng: number; t: number; acc: number; speed?: number | null }
export interface GpsRunHistory {
  id: string
  distance_km: number // 原始距離（未套 GPS 距離校正，見 internal/gpscalib）；一律以 calib_distance_km 優先顯示
  duration_s: number
  avg_pace_s: number
  point_count: number
  flagged: boolean
  flag_reason?: string
  review_action?: string
  started_at: string
  ended_at: string
  polyline?: string
  km_paces?: number[] // 每公里分段配速(秒/km)；僅詳情回傳、v0.1.205 後的新跑步才有
  // GPS 距離校正（見 internal/gpscalib，2026-08-30）：校正後距離，與「已同步活動」列表/總里程
  // 同一套數字；未套校正時等於 distance_km。calib_factor<1 才代表這筆真的套用過校正。
  calib_distance_km?: number
  calib_factor?: number
  // calib_avg_pace_s：校正後平均配速（duration_s / calib_distance_km）。對抗式審查修正：距離已改
  // 顯示校正後但配速若仍用 avg_pace_s（原始），同畫面「距離×配速≠時間」且跟已同步活動的同一趟
  // 配速對不上；有值時優先顯示這個，沒有（舊資料/未套校正）才 fallback 回 avg_pace_s。
  calib_avg_pace_s?: number
  // 已排除的異常段（同上，見 GpsRunResult）：optional——舊資料（此欄位上線前的紀錄）沒有這兩欄。
  excluded_km?: number
  excluded_segments?: number
}
export const activitiesApi = {
  // client_version：App/前端版號，供 GPS 距離校正量測用（見 internal/gpscalib），可不送。
  // pet_ids：D5 狗狗歸屬——上傳當下勾選「這趟狗狗有一起跑」的 registration_pet id（見 track/page.tsx
  // 的「這趟狗狗有一起跑嗎？」卡片），非寵物賽事/沒勾就不帶。
  // signal：呼叫端可帶 AbortSignal.timeout(...)——request() 的 fetch 本身沒有逾時，iOS 上網路停滯時 fetch 可能
  // 幾分鐘不 reject，跑完的上傳若懸著、結束畫面就沒有任何出口（2026-09-13 對抗式審查）。
  uploadGps: (token: string, body: { race_id?: string; started_at: string; ended_at: string; points: GpsPoint[]; client_version?: string; pet_ids?: string[] }, signal?: AbortSignal) =>
    request<{ result: GpsRunResult }>('/activities/gps', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body), signal }),
  gpsHistory: (token: string) => request<{ runs: GpsRunHistory[] }>('/activities/gps/history', { headers: withAuth(token) }),
  gpsDetail: (token: string, id: string) => request<{ run: GpsRunHistory }>(`/activities/gps/${id}`, { headers: withAuth(token) }),
  // 跑步中心跳（後台「目前在跑名單」用）；失敗可忽略
  trackPing: (token: string) => request<void>('/track/ping', { method: 'POST', headers: withAuth(token) }),
  // D5：狗狗歸屬——查詢／覆寫某筆活動（任何來源：App GPS／Strava／Terra／歷史紀錄）目前歸屬的狗狗
  // registration_pet id 名單。POST 為「整組取代」（見後端註解：insert missing, delete removed），
  // 不是增量勾選。⚠️ 2026-09-09 review 修正：這兩支後端 handler（services/api/internal/activity/
  // handler.go GetActivityPets/SetActivityPets）實際回傳的是 {"pet_ids": string[]}——只有目前歸戶中
  // 的 id 名單，不含寵物名字/賽事名稱（那些沒有活動 id 可比對，本來就不知道「哪些是候選、哪些已
  // 勾選」）。舊版這裡宣告成 {pets: ActivityPetOption[]} 一個前端自己想像出來、後端從未實作過的形狀，
  // 導致 ProfileScreen 解構出來的 pets 恆為 undefined，checkbox 清單永遠不會顯示（見
  // ProfileScreen.tsx PetAttributionToggle 同步修正：候選寵物名冊改由已載入的 profileApi.registrations()
  // 在前端組出，這裡只負責「這筆活動目前勾了哪些」）。
  getActivityPets: (token: string, activityID: string) =>
    request<{ pet_ids: string[] }>(`/activities/${activityID}/pets`, { headers: withAuth(token) }),
  setActivityPets: (token: string, activityID: string, petIDs: string[]) =>
    request<{ pet_ids: string[] }>(`/activities/${activityID}/pets`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ pet_ids: petIDs }) }),
}

// --- GPS 距離校正（見 internal/gpscalib，2026-08-30）：以穿戴裝置(Strava/Garmin/COROS)匯入的活動為
// 參考，估計 App GPS 的系統性偏差，只准向下修正(factor∈[0.92,1.00])、只向前生效。Dashboard 的
// gps_calib_factor 與 GPS 上傳當下套用的係數同一函式（gpscalib.EffectiveFactor）算出，保證「看到的＝
// 入帳的」；本區塊為使用者自助端點（/me/gps-calib），需入口白名單 shown（見 DashboardInfo.gps_calib_entry）
// 才會 200，否則 401/403。---
export type GpsCalibState = 'warming' | 'active' | 'unstable' | 'stale' | 'frozen'
export interface GpsCalibPair {
  activity_at: string
  ext_source: string
  gps_km: number // App GPS 原始距離（永不套校正）
  ext_km: number // 手錶／外部來源距離
  // 「這趟當時真正入帳」的距離與係數，逐趟凍結在 gps_runs.calib_distance_km / calib_factor。
  // 係數隨時間演進（±2% 遲滯步幅、warming 期間恆為 1.0），所以舊配對多半不等於今天的係數——
  // 要講「實際入帳」只能用這兩個欄位，不能拿現在的係數回推。
  credited_km: number
  credited_factor: number
  // 回推（back-test）：gps_km × 目前生效係數（effective_factor）。用來評估「現在這組係數好不好」，
  // **不是**實際入帳的距離。前端一律直接顯示後端算好的值，不要自己乘（四捨五入口徑會不一致）。
  calib_km: number
  ratio: number // = ext_km / gps_km
  accepted: boolean // 通過 G1-G7 閘門；不含視窗條件，會多於實際採用的組數
  in_window: boolean // 真的進了估計視窗（accepted + 有 inlier_w + 仍在 120 天內）
  reject_reason?: string
  inlier_w?: number
  gps_run_id: string // 供後台追查原始跑步紀錄
  ext_activity_id: string // 供後台追查外部活動
}
export interface GpsCalibLogEntry {
  version: number
  factor_before?: number
  factor_after?: number
  status?: string
  reason: string // recompute|enable|disable|reset|admin_freeze|admin_unfreeze
  actor: string  // system|user|admin:<id>
  created_at: string
}
// not_apply_reason：校正沒生效的原因（生效時不回傳）。
//   entry=影子模式（入口非 shown，Recompute 照算但不套）／no_data=從未有過配對／
//   disabled=使用者自己關掉／status=warming 或 unstable／stale=太久沒新配對
export type GpsCalibNotApplyReason = 'entry' | 'no_data' | 'disabled' | 'status' | 'stale'
export interface GpsCalibInfo {
  entry: 'hidden' | 'locked' | 'shown'
  // apply_entry 不含 super_admin 旁路，是「校正對他是否真的生效」那一道；entry 是「卡片可不可見」。
  apply_entry: 'hidden' | 'locked' | 'shown'
  // factor 是 user_gps_calib 的估計係數，**不等於**實際入帳的係數（Recompute 是影子模式，對全體
  // 無條件執行）。effective_factor 才是這一刻真的乘上去的那個數字，與 GPS 上傳的 EffectiveFactor
  // 同一份判定。任何要講「校正後」的畫面一律用 effective_factor。
  effective_factor: number
  applied: boolean
  not_apply_reason?: GpsCalibNotApplyReason
  enabled: boolean
  factor: number
  status: GpsCalibState
  ref_source: string
  n_pairs: number
  n_eff: number
  sigma: number
  last_pair_at?: string
  computed_at?: string
  version: number
  pairs: GpsCalibPair[] // 最近 20 筆（後台查詢最近 200 筆）
  log: GpsCalibLogEntry[] // 最近 30 筆（後台查詢最近 200 筆）
}
export const gpsCalibApi = {
  get: (token: string) => request<GpsCalibInfo>('/me/gps-calib', { headers: withAuth(token) }),
  setEnabled: (token: string, enabled: boolean) =>
    request<GpsCalibInfo>('/me/gps-calib', { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ enabled }) }),
  // 每人 60 秒限流，超限拋 429（ApiError，e?.status 可判斷）
  recompute: (token: string) => request<GpsCalibInfo>('/me/gps-calib/recompute', { method: 'POST', headers: withAuth(token) }),
}
// GpsCalibRow GET /admin/gps-calib 列表的一列（後台「GPS 校正紀錄」頁）。factor/status 已在後端
// SQL 套用懶判 stale、effective_factor 另外含入口與使用者開關兩道閘門，與點進詳情
// GET /admin/gps-calib/{user_id} 同一口徑，兩處數字保證一致。
// account_code 僅後台可見（面向玩家的 API 一律不得回傳他人帳號編碼，見 memory account-code-privacy）。
export interface GpsCalibRow {
  user_id: string
  name: string // COALESCE(NULLIF(users.name,''), users.handle)，顯示名稱統一口徑
  email: string
  account_code: string
  // 語意同 GpsCalibInfo 的同名欄位：factor 只是估計值，effective_factor 才是實際入帳的係數。
  apply_entry: 'hidden' | 'locked' | 'shown'
  effective_factor: number
  applied: boolean
  not_apply_reason?: GpsCalibNotApplyReason
  factor: number
  status: GpsCalibState
  enabled: boolean
  ref_source: string // strava|garmin|coros，可能為空字串
  n_pairs: number
  n_eff: number
  sigma: number // DB 為 NULL 時後端回 0
  last_pair_at?: string
  computed_at?: string
  version: number
}
// 後台管理（需 admin + members 權限）：會員詳情頁凍結/解凍/重設用，見 memory event-schema-round1 GPS 校正段落。
export const adminGpsCalibApi = {
  // 全站校正概況；total 為套用 status 篩選後的總筆數（供分頁）。status 只接受 GpsCalibState，其餘回 400。
  list: (token: string, params?: { limit?: number; offset?: number; status?: string }) => {
    const qs = new URLSearchParams()
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset != null) qs.set('offset', String(params.offset))
    if (params?.status) qs.set('status', params.status)
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ items: GpsCalibRow[]; total: number }>(`/admin/gps-calib${suffix}`, { headers: withAuth(token) })
  },
  get: (token: string, userID: string) => request<GpsCalibInfo>(`/admin/gps-calib/${userID}`, { headers: withAuth(token) }),
  freeze: (token: string, userID: string, factor: number) =>
    request<void>(`/admin/gps-calib/${userID}/freeze`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ factor }) }),
  unfreeze: (token: string, userID: string) =>
    request<void>(`/admin/gps-calib/${userID}/unfreeze`, { method: 'POST', headers: withAuth(token) }),
  reset: (token: string, userID: string) =>
    request<void>(`/admin/gps-calib/${userID}/reset`, { method: 'POST', headers: withAuth(token) }),
}

// --- 打卡點任務（geofence check-in）---
export interface ActiveCheckpoint {
  id: string
  lat: number
  lng: number
  radius_m: number
  title?: string
  task_id: string
  task_title?: string
  race_id: string
  race_title?: string
  checked: boolean
  pending: boolean
}
export interface CheckinResult {
  ok: boolean
  status: 'verified' | 'pending' | 'already' | 'out_of_range' | 'low_accuracy' | 'not_open'
  distance_m: number
  message: string
  collected: number
  required: number
  task_done: boolean
}
export const checkpointApi = {
  active: (token: string) =>
    request<{ checkpoints: ActiveCheckpoint[] }>('/checkpoints', { headers: withAuth(token) }),
  checkin: (token: string, id: string, body: { lat: number; lng: number; acc: number; points?: { lat: number; lng: number; t: number; acc: number }[] }) =>
    request<{ result: CheckinResult }>(`/checkpoints/${id}/checkin`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
}

// 跑步路線建議（後端 ORS foot-walking 代理）：目前位置 → 打卡點的跑者友善建議路線。
// coords 為 [lat,lng] 序列，直接給 Leaflet polyline。
export const routeApi = {
  plan: (token: string, fromLat: number, fromLng: number, toLat: number, toLng: number) =>
    request<{ distance_m: number; duration_s: number; coords: [number, number][] }>(
      `/route?from=${fromLat},${fromLng}&to=${toLat},${toLng}`,
      { headers: withAuth(token) },
    ),
}

// --- 事件任務（日常隨機事件）---
export interface EventParamSpec { key: string; label: string; unit: string }
export interface EventTypeSpec { key: string; label: string; params: EventParamSpec[] }
// 完成事件的佐證：基本移動 + 配速類額外指標（伺服器重驗）
export interface CompleteEvidence {
  moved_m: number
  window_s: number
  min_seg_m?: number
  max_seg_m?: number
  first_half_m?: number
  second_half_m?: number
  taps?: number // tap_burst：點擊次數
  held_ms?: number // hold_press：累積按住毫秒
  swipe_px?: number // swipe_charge：累積滑動距離
  swipes?: number // dodge_swipe：滑動段數
  shape_pts?: [number, number][] // draw_shape：實際筆跡點（伺服器重算辨識）
  shape?: number // draw_shape：本次抽到的圖形（3/4/5）
  baseline_spk?: number // pace_shift：觸發時平均配速（秒/公里）。Phase A 伺服器會以快照覆寫；Phase B 用此值
}

export interface EventDef {
  id?: string
  name: string
  description?: string
  enabled: boolean
  weight: number
  trigger_type: string
  trigger_params: Record<string, number>
  completion_type: string
  completion_params: Record<string, number>
  message: string
  goal_text?: string // 自訂任務目標說明（留空＝用系統依完成條件自動產生）
  image_url?: string // 預設圖（時段未設定時回退）
  image_day_url?: string // 白天 06:00–17:00
  image_dusk_url?: string // 黃昏 17:00–19:00
  image_night_url?: string // 晚上 19:00–06:00
  reward_exp: number
  reward_dp: number
}
export const eventApi = {
  active: (token: string) => request<{ defs: EventDef[]; wait_min_sec?: number; wait_max_sec?: number; first_event_wait_sec?: number }>('/events/active', { headers: withAuth(token) }),
  createOccurrence: (token: string, body: { def_id: string; trigger_dist_m: number; trigger_elapsed_s: number; first_of_run?: boolean }) =>
    request<{ id: string; reward_exp: number; reward_dp: number }>('/events/occurrences', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  complete: (token: string, id: string, body: CompleteEvidence) =>
    request<{ completed: boolean; reward_exp?: number; reward_dp?: number; stars?: number; bonus_exp?: number; bonus_dp?: number; message?: string }>(`/events/occurrences/${id}/complete`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  fail: (token: string, id: string) => request<void>(`/events/occurrences/${id}/fail`, { method: 'POST', headers: withAuth(token) }),
  claimManual: (token: string) => request<{ armed: boolean; def?: EventDef; occ_id?: string }>('/events/manual/claim', { method: 'POST', headers: withAuth(token) }),
}
export const adminEventsApi = {
  list: (token: string) => request<{ defs: EventDef[]; trigger_catalog: EventTypeSpec[]; completion_catalog: EventTypeSpec[] }>('/admin/events', { headers: withAuth(token) }),
  create: (token: string, body: EventDef) => request<{ def: EventDef }>('/admin/events', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: EventDef) => request<{ def: EventDef }>(`/admin/events/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) => request<void>(`/admin/events/${id}`, { method: 'DELETE', headers: withAuth(token) }),
  push: (token: string, id: string, email: string) => request<{ ok: boolean; target: string }>(`/admin/events/${id}/push`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ email }) }),
  // 每個管理者專屬的「測試觸發」常用名單
  testTargets: (token: string) => request<{ targets: TestTarget[] }>('/admin/events/test-targets', { headers: withAuth(token) }),
  addTestTarget: (token: string, email: string, makeDefault = false) => request<{ targets: TestTarget[] }>('/admin/events/test-targets', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ email, make_default: makeDefault }) }),
  removeTestTarget: (token: string, email: string) => request<{ targets: TestTarget[] }>(`/admin/events/test-targets?email=${encodeURIComponent(email)}`, { method: 'DELETE', headers: withAuth(token) }),
  setDefaultTestTarget: (token: string, email: string) => request<{ targets: TestTarget[] }>('/admin/events/test-targets/default', { method: 'PATCH', headers: withAuth(token), body: JSON.stringify({ email }) }),
}
export interface TestTarget { email: string; is_default: boolean }

// 效果資產覆寫（把暫代 emoji/合成音效換成正式圖片/音檔）
export const effectsApi = {
  get: (token: string) => request<{ assets: Record<string, string> }>('/effect-assets', { headers: withAuth(token) }),
}
export const adminEffectsApi = {
  list: (token: string) => request<{ assets: Record<string, string> }>('/admin/effect-assets', { headers: withAuth(token) }),
  set: (token: string, slug: string, url: string) => request<{ assets: Record<string, string> }>(`/admin/effect-assets/${slug}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ url }) }),
  clear: (token: string, slug: string) => request<{ assets: Record<string, string> }>(`/admin/effect-assets/${slug}`, { method: 'DELETE', headers: withAuth(token) }),
}

// 通用系統設定（key-value）
export const adminAppSettingsApi = {
  list: (token: string) => request<{ settings: Record<string, string> }>('/admin/app-settings', { headers: withAuth(token) }),
  set: (token: string, key: string, value: string) => request<{ settings: Record<string, string> }>(`/admin/app-settings/${key}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ value }) }),
}
// 公開系統設定（前台外觀，如 active_skin；免登入）
export const publicSettingsApi = {
  get: () => request<{ settings: Record<string, string> }>('/app-settings/public'),
}

// 蓋板廣告（拍立得卡片堆疊）
export interface InterstitialAd {
  id?: string
  enabled: boolean
  sort_order: number
  image_url: string
  headline: string
  description: string
  cta_label: string
  cta_url: string
}
export const interstitialApi = {
  get: () => request<{ ads: InterstitialAd[] }>('/interstitial'), // 公開，前台開啟時讀取
}
export const adminInterstitialApi = {
  list: (token: string) => request<{ ads: InterstitialAd[] }>('/admin/interstitial', { headers: withAuth(token) }),
  create: (token: string, body: InterstitialAd) => request<{ id: string }>('/admin/interstitial', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: InterstitialAd) => request<{ ok: boolean }>(`/admin/interstitial/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) => request<{ ok: boolean }>(`/admin/interstitial/${id}`, { method: 'DELETE', headers: withAuth(token) }),
}

// --- 賽事多人連動事件（Phase B）---
export interface RelOption { key: string; label: string }
export interface RaceEventDef {
  id?: string
  name: string
  description?: string
  enabled: boolean
  race_id?: string // '' = 適用所有賽事
  weight: number
  trigger_min_m: number
  initiator_cooldown_sec: number
  target_count: number
  group_rel: string
  follow_rel: string
  gender_rel: string
  join_window_s: number
  completion_type: string
  completion_params: Record<string, number>
  message: string
  image_url?: string
  image_day_url?: string
  image_dusk_url?: string
  image_night_url?: string
  reward_exp: number
  reward_dp: number
  per_user_daily_cap: number
  mode?: 'individual' | 'collective' // 省略/'individual' 視為個人賽（既有行為）
  goal_metric?: string // collective 用；B1 僅實作 distance_m
  goal_target?: number // collective 用；共享目標總量（公尺）
  goal_window_s?: number // collective 用；達標時限秒數
}

// WS 邀請 payload
export interface RaceEventInvite {
  instance_id: string
  target_user_ids: string[]
  initiator_name: string
  name: string
  message: string
  mode?: 'individual' | 'collective' // Phase B2：省略/'individual' 視為個人賽（既有行為）
  goal_target?: number // collective 專用：共享累積目標（公尺）
  completion_type: string
  completion_params: Record<string, number>
  join_window_s: number
  reward_exp: number
  reward_dp: number
  image_url?: string
  image_day_url?: string
  image_dusk_url?: string
  image_night_url?: string
  join_deadline: number // epoch ms
}

// Phase B2：WS 廣播的共享進度／達標訊息（collective 模式）
export interface GroupGoalProgressMsg {
  instance_id: string
  current: number
  target: number
  participants: number
  reached: boolean
}
export interface GroupGoalReachedMsg {
  instance_id: string
  reward_exp: number
  reward_dp: number
}

export const eventRaceApi = {
  context: (token: string) => request<{ races: { id: string; title: string }[] }>('/events/race/context', { headers: withAuth(token) }),
  trigger: (token: string, body: { race_id: string; moved_m: number; elapsed_s: number }) =>
    request<{ triggered: boolean; instance_id?: string; targets?: number }>('/events/race/trigger', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  join: (token: string, instID: string) =>
    request<{ joined: boolean; message?: string; name?: string; completion_type?: string; completion_params?: Record<string, number>; reward_exp?: number; reward_dp?: number; deadline?: number; mode?: 'individual' | 'collective'; instance_id?: string; goal_target?: number; current?: number }>(`/events/race/instances/${instID}/join`, { method: 'POST', headers: withAuth(token) }),
  complete: (token: string, instID: string, body: CompleteEvidence) =>
    request<{ completed: boolean; reward_exp?: number; reward_dp?: number; stars?: number; bonus_exp?: number; bonus_dp?: number; message?: string; capped?: boolean }>(`/events/race/instances/${instID}/complete`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  fail: (token: string, instID: string) => request<void>(`/events/race/instances/${instID}/fail`, { method: 'POST', headers: withAuth(token) }),
  // Phase B2：collective 模式回報移動量貢獻共享目標
  contribute: (token: string, instID: string, deltaM: number) =>
    request<{ current: number; target: number; reached: boolean; participants: number }>(`/events/race/instances/${instID}/contribute`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ delta_m: deltaM }) }),
}

export const adminEventRacesApi = {
  list: (token: string) => request<{ defs: RaceEventDef[]; completion_catalog: EventTypeSpec[]; group_rel_options: RelOption[]; follow_rel_options: RelOption[]; gender_rel_options: RelOption[] }>('/admin/event-races', { headers: withAuth(token) }),
  create: (token: string, body: RaceEventDef) => request<{ def: RaceEventDef }>('/admin/event-races', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: RaceEventDef) => request<{ def: RaceEventDef }>(`/admin/event-races/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) => request<void>(`/admin/event-races/${id}`, { method: 'DELETE', headers: withAuth(token) }),
  // Phase B3：管理員立即發起一次 collective 事件（測試/人工介入）
  fire: (token: string, defID: string, raceID: string) =>
    request<{ instance_id?: string; invited: number; message?: string }>(`/admin/event-races/${defID}/fire`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ race_id: raceID }) }),
}

export interface GpsRunSummary {
  id: string
  user_id: string
  user_name: string
  distance_km: number
  duration_s: number
  avg_pace_s: number
  point_count: number
  flag_reason: string
  started_at: string
  ended_at: string
  polyline?: string
}
// 2026-09-03「回收異常數據」：已入帳（早於偵測或漏網）的 GPS 跑步，後台可事後標異常＋反沖已發的 EXP/DP/里程。
export interface AdminRecentGpsRun {
  id: string; user_id: string; user_name: string; user_email: string
  started_at: string; ended_at: string
  distance_km: number; calib_distance_km: number | null; duration_s: number; avg_pace_s: number
  flagged: boolean; flag_reason: string; review_action: string
  excluded_km: number; excluded_segments: number
  activity_id: string | null; exp_awarded: boolean | null
}
export interface GpsRecallResult {
  run_id: string; activity_id: string | null; already_recalled: boolean
  reversed: { total_km: number; exp: number; dp: number; km_added: number }
  reason: string; followups: string[]
}
export const adminGpsApi = {
  list: (token: string) => request<{ runs: GpsRunSummary[] }>('/admin/gps-runs', { headers: withAuth(token) }),
  get: (token: string, id: string) => request<{ run: GpsRunSummary }>(`/admin/gps-runs/${id}`, { headers: withAuth(token) }),
  approve: (token: string, id: string) => request<void>(`/admin/gps-runs/${id}/approve`, { method: 'POST', headers: withAuth(token) }),
  reject: (token: string, id: string) => request<void>(`/admin/gps-runs/${id}/reject`, { method: 'POST', headers: withAuth(token) }),
  // 近期已入帳跑步（含未被旗標者），供人工事後回收；q 為選填的使用者 ID／Email／名稱模糊搜尋
  recent: (token: string, params: { days?: number; limit?: number; q?: string }) => {
    const qs = new URLSearchParams()
    if (params.days != null) qs.set('days', String(params.days))
    if (params.limit != null) qs.set('limit', String(params.limit))
    if (params.q) qs.set('q', params.q)
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ runs: AdminRecentGpsRun[] }>(`/admin/gps-runs/recent${suffix}`, { headers: withAuth(token) })
  },
  // 標為異常並反沖已發出的 EXP/DP/里程（保留紀錄不刪，僅排除計算）
  recall: (token: string, id: string, body: { reason?: string; valid_distance_km?: number | null; activity_id?: string | null }) =>
    request<GpsRecallResult>(`/admin/gps-runs/${id}/recall`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
}

export const mileageExpApi = {
  get: (token: string) => request<{ breakdown: ExpBreakdown }>('/profile/mileage-exp', { headers: withAuth(token) }),
  config: (token: string) => request<MileageConfig>('/profile/mileage-config', { headers: withAuth(token) }),
  markSeen: (token: string) => request<void>('/profile/mileage-exp/seen', { method: 'POST', headers: withAuth(token) }),
}

export const adminSettingsApi = {
  set: (token: string, settings: SiteSettings) =>
    request<{ settings: SiteSettings }>('/admin/settings', {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify(settings),
    }),
}

// --- Auth ---

// 註冊來源歸因（migration 147_signup_attribution）：landing_url/referrer_url 選填，後端只在「新建用戶」路徑
// 才會 classify+寫入，既有帳號一律忽略。由呼叫端帶入 buildAcqPayload()（見 lib/acquisition.ts）。
export interface AcqPayload { landing_url: string; referrer_url: string }

export const authApi = {
  // refCode：推廣連結帶入的推薦碼（optional，僅新帳號註冊時由後端綁定）
  // acq：first-touch 擷取的 landing/referrer（optional，僅新帳號註冊時由後端 classify 來源）
  register: (body: { email: string; handle: string; name: string; password: string }, refCode?: string, acq?: AcqPayload) =>
    request<{ user: User; tokens: TokenPair }>('/auth/register', {
      method: 'POST',
      body: JSON.stringify({ ...body, ...(refCode ? { ref_code: refCode } : {}), ...(acq ? { acq } : {}) }),
    }),

  login: (body: { email: string; password: string }) =>
    request<{ user: User; tokens: TokenPair }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  // Google 登入（GIS ID-token）；refCode：推廣連結帶入的推薦碼；acq：first-touch 來源擷取
  // （皆 optional，僅「全新會員」分支由後端使用，既有帳號登入一律忽略）
  google: (id_token: string, refCode?: string, acq?: AcqPayload) =>
    request<{ user: User; tokens: TokenPair }>('/auth/google', {
      method: 'POST',
      body: JSON.stringify({ id_token, ...(refCode ? { ref_code: refCode } : {}), ...(acq ? { acq } : {}) }),
    }),

  refresh: (refresh_token: string) =>
    request<TokenPair>('/auth/refresh', {
      method: 'POST',
      body: JSON.stringify({ refresh_token }),
    }),

  // 2026-09-08 第二次稽核修法：後端現在收到空 refresh_token 也會照樣撤銷這顆 access token
  // （之前呼叫端「沒有 refresh token 就整個跳過撤銷請求」，等於 session-only 模式登出時 access
  // token 完全沒被撤銷，只是本機清掉——理論上還能在到期前繼續使用）。refresh_token 改成 optional，
  // 呼叫端只要有 access token 就該打這支，沒有 refresh 就傳空字串。
  logout: (token: string, refresh_token?: string) =>
    request<void>('/auth/logout', {
      method: 'DELETE',
      headers: withAuth(token),
      body: JSON.stringify({ refresh_token: refresh_token || '' }),
    }),

  me: (token: string) =>
    request<User>('/auth/me', { headers: withAuth(token) }),
}

// --- Races ---

export interface GroupStanding {
  group_id: string
  group_name: string
  total_km: number
  member_count: number
  avg_km: number
  avg_pace_s: number
  finish_total_s: number
}

export interface StandingRank extends GroupStanding {
  rank: number
}

export interface MyGroupRank {
  group_id: string
  group_name: string
  cumulative_rank: number
  finish_rank: number
  total_km: number
}

export interface CompetitionRanking {
  race_id: string
  event_mode: EventMode
  goal_type: GoalType
  by_cumulative: StandingRank[]
  by_finish_time: StandingRank[]
  my_group?: MyGroupRank | null
}

export interface ExpBreakdownItem {
  label: string
  amount: number
  dp?: number // 同來源同時獲得的 DP
  kind: string // completion | mileage | task
}
export interface ExpLevelRow {
  level: number
  title: string
  exp_required: number
}
export interface ExpBreakdown {
  gained: number
  exp_before: number
  exp_after: number
  dp_gained?: number
  dp_after?: number
  completion_pct?: number
  items: ExpBreakdownItem[]
  levels: ExpLevelRow[]
}

export interface Certificate {
  completed: boolean
  race_title: string
  name: string
  group_name?: string
  target_km: number
  completed_km: number
  completion_at?: string
  total_time_s: number
  finish_rank: number
  finished_count: number
  race_end?: string
  race_ended: boolean
  bg_url?: string
  // layout：完賽證明可視化排版覆寫（見 RaceConfig.cert_layout）；此賽事未設定任何覆寫時為 undefined，
  // renderCertificate 收到 undefined 時等同用內建預設。
  layout?: Record<string, CertElementLayout>
}

export interface RegistrationState {
  id: string
  group_id?: string
  group_revealed: boolean
  group_name?: string
  status: string
  amount: number
}

export type InvoiceBuyerType = 'personal' | 'company' | 'donation'

export interface InvoiceInfo {
  buyer_type: InvoiceBuyerType
  tax_id: string               // company 專用，8 碼統編
  title: string                // company 專用，發票抬頭
  carrier_type: '' | 'mobile'  // personal 專用
  carrier_id: string           // personal 專用，手機條碼載具
  love_code: string            // donation 專用，愛心碼
}

// --- 電子發票輸入時查驗（見 services/api/internal/einvoice/verify.go，2026-09-08）---

export interface InvoiceVerifyResult {
  format_ok: boolean
  exists: boolean | null // null＝未查驗（格式不合法，或 ECPay/財政部暫時無法查）
  checked: boolean
  message: string
  env: string // stage｜prod：env !== 'prod' 時前端顯示「（測試環境）」
}

export const invoiceApi = {
  verify: (token: string, body: { type: 'mobile' | 'love_code'; value: string }) =>
    request<InvoiceVerifyResult>('/invoice/verify', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
}

// 寵物雲端馬拉松（2026-09-08，migration 173）：報名請求送出的單隻寵物資料；chip_id 選填、預留未來
// 第三方寵物資料平台串接，格式比照後端寬鬆驗證（^[A-Za-z0-9-]{0,32}$）。
export interface PetEntryPayload {
  name: string
  chip_id?: string
}
// 報名/訂單讀回的寵物資料（含 seq 供匯出/明細顯示排序）；chip_id 後端一律回傳空字串而非省略。
// id＝registration_pets.id，供 GPS 上傳的 pet_ids 勾選與 /activities/{id}/pets 歸屬切換使用。
export interface RegistrationPet {
  id: string
  seq: number
  name: string
  chip_id: string
}

export interface RegisterPayload {
  group_id?: string
  group_key?: string // 加入需鑰匙的分組時帶入
  addons?: { addon_id: string; qty: number }[]
  participant: Partial<Record<ParticipantField, string>>
  invoice?: InvoiceInfo // 電子發票資訊（選填）
  promo_code?: string
  use_coupon?: boolean // 使用 VIP 活動優惠券($100)；與 promo_code、coupon_reward_id 三擇一
  coupon_reward_id?: string // 使用活動優惠券（migration 138，user_rewards.id）；三者互斥
  // 寵物雲端馬拉松：race.pet_kind !== '' 時必填（數量＝1 + 加購「寵物參賽名額」qty，見 D5/D8）；
  // 一般賽事（pet_kind===''）不帶。
  pets?: PetEntryPayload[]
}

export interface CreateTeamGroupPayload {
  name: string
  description?: string
  target_distance_km?: number | null
  requires_key: boolean
  group_key?: string
}

export interface RegisterResult {
  registration: RegistrationState
  order: { id: string; total_cents: number; status: string }
  assigned_group: string
  group_revealed: boolean
  discount_cents: number
  payable_cents: number
  paid: boolean
}

export interface PromoQuote {
  valid: boolean
  code?: string
  discount_cents: number
  payable_cents: number
  free: boolean
  reason?: string
}

export interface MyRegLite {
  status: string // pending|paid|cancelled
  group_revealed: boolean
}

export interface StravaStatus {
  connected: boolean
  enabled: boolean
  athlete_name?: string
}

// Terra（Garmin/COROS/Polar/Suunto/Wahoo 等手錶直連聚合器，Phase 1；見 memory terra-wearable-integration）
export interface TerraConnection {
  provider: string // 小寫品牌代碼，如 garmin/coros
  connected_at: string
  via: 'terra'
}
export interface TerraStatus {
  enabled: boolean // 後端未設定 Terra 憑證時為 false，卡片維持「即將開放」
  providers: string[] // 目前開放連接的品牌（小寫）
  connections: TerraConnection[] // 使用者已連接的品牌，可有多筆（不同手錶）
}

// 手動補匯（見 integrationsApi.terraImport）的回應：webhook 可能不會送 activity 事件，故留一個補救管道
export interface TerraImportResult {
  provider: string
  days: number
  fetched: number
  imported: number
  duplicate: number
  skipped_before_connect: number
  skipped_non_running: number
  skipped_invalid: number
  errors: number
  async: boolean
}

export interface SyncedActivity {
  id: string
  source: string
  distance_km: number
  duration_s: number
  avg_pace_s: number
  ascent_m?: number
  avg_hr?: number
  recorded_at: string
  started_at: string
  race_title?: string
  flagged: boolean
  flag_reason?: string
  external_id?: string // provider 活動 id（Strava→「View on Strava」回連）
  // GPS 距離校正（見 internal/gpscalib，2026-08-30）：raw_distance_km 已 COALESCE（非 App GPS 或未套校正
  // 時等於 distance_km）；calib_factor 為 null 代表尚未評估/非 App GPS 來源，App GPS 來源即使 k=1.0 也會
  // 是 1.0 而非 null——用 calib_factor != null && calib_factor < 1 判斷是否顯示「校正後/原始」對照。
  raw_distance_km: number
  calib_factor: number | null
  // 跨來源去重（見 internal/profile/dedup.go）：flagged 為多裝置/跨來源重複時，dup_of_id 指向被保留的
  // 那筆活動，dup_of_source 是它的來源（null 一律 fallback 成 'gps'，與 source 的 'manual' fallback
  // 對使用者顯示上等價，都是「App GPS」）。沒有對應保留活動時兩欄皆缺席（後端 omitempty）。
  dup_of_id?: string
  dup_of_source?: string
}

export interface SyncResult {
  imported: number
  duplicates: number
  existing: number
  total: number
}

export const metaApi = {
  version: () => request<{ version: string; base: string; commit: string }>('/version'),
}

export const integrationsApi = {
  stravaStatus: (token: string) =>
    request<StravaStatus>('/integrations/strava/status', { headers: withAuth(token) }),
  stravaConnectUrl: (token: string, returnUrl?: string) =>
    request<{ url: string }>(
      `/integrations/strava/connect${returnUrl ? `?return=${encodeURIComponent(returnUrl)}` : ''}`,
      { headers: withAuth(token) }
    ),
  stravaDisconnect: (token: string) =>
    request<null>('/integrations/strava/disconnect', { method: 'DELETE', headers: withAuth(token) }),
  stravaSync: (token: string) =>
    request<SyncResult>('/integrations/strava/sync', { method: 'POST', headers: withAuth(token) }),
  stravaActivities: (token: string) =>
    request<{ activities: SyncedActivity[] }>('/integrations/strava/activities', { headers: withAuth(token) }),
  // Terra 手錶直連（Phase 1）：狀態未設定憑證時 enabled=false；connect 未開通時回 503 { error: 'terra_disabled' }
  terraStatus: (token: string) =>
    request<TerraStatus>('/integrations/terra/status', { headers: withAuth(token) }),
  terraConnectUrl: (token: string, returnUrl?: string) =>
    request<{ url: string }>(
      `/integrations/terra/connect${returnUrl ? `?return=${encodeURIComponent(returnUrl)}` : ''}`,
      { headers: withAuth(token) }
    ),
  terraDisconnect: (token: string, provider: string) =>
    request<{ ok: true }>(`/integrations/terra/disconnect?provider=${encodeURIComponent(provider)}`, {
      method: 'POST',
      headers: withAuth(token),
    }),
  // 手動補匯：webhook 可能沒送活動事件（如 COROS 真機只送 daily 沒送 activity），提供使用者主動向 Terra 要近期紀錄
  terraImport: (token: string, provider: string, days = 30) =>
    request<TerraImportResult>(`/integrations/terra/import?provider=${encodeURIComponent(provider)}&days=${days}`, {
      method: 'POST',
      headers: withAuth(token),
    }),
}

export const racesApi = {
  // 公開列表；帶 token 則附 registrations（race_id → 報名狀態）
  list: (token?: string) =>
    request<{ races: Race[]; registrations?: Record<string, MyRegLite> }>(
      '/races',
      token ? { headers: withAuth(token) } : undefined
    ),
  // 公開賽事詳情（含分組/加購/物資）+ 報名狀態（帶 token）
  detail: (raceID: string, token?: string) =>
    request<{ race: RaceDetail; registration: RegistrationState | null; can_create_team_group?: boolean }>(
      `/races/${raceID}`,
      token ? { headers: withAuth(token) } : undefined
    ),
  register: (raceID: string, token: string, payload: RegisterPayload) =>
    request<RegisterResult>(`/races/${raceID}/register`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(payload),
    }),
  // 前台跑團成員自建分組（competition + allow_team_groups）
  createTeamGroup: (raceID: string, token: string, payload: CreateTeamGroupPayload) =>
    request<RaceGroup>(`/races/${raceID}/groups`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(payload),
    }),
  promoCheck: (raceID: string, token: string, body: { code: string; group_id?: string; addons?: { addon_id: string; qty: number }[] }) =>
    request<PromoQuote>(`/races/${raceID}/promo/check`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(body),
    }),
  // 競賽排行榜（公開；帶 token 則附自己分組名次）
  standings: (raceID: string, token?: string) =>
    request<CompetitionRanking>(`/races/${raceID}/standings`, token ? { headers: withAuth(token) } : undefined),
  // 某分組的成員排名（依累積里程；帶 token 則含自己/追蹤旗標）
  groupMembers: (raceID: string, groupID: string, token?: string) =>
    request<{ members: Contributor[] }>(`/races/${raceID}/groups/${groupID}/members`, token ? { headers: withAuth(token) } : undefined),
  // 賽事進度（任務達成度 + 個人統計；帶 token 則含個人）
  progress: (raceID: string, token?: string) =>
    request<{ progress: RaceProgress }>(`/races/${raceID}/progress`, token ? { headers: withAuth(token) } : undefined),
  // 某任務的里程貢獻榜（前 20 名 + 自己；帶 token 則含自己排名）
  taskContributors: (raceID: string, taskID: string, token?: string) =>
    request<{ contributors: TaskContributors }>(`/races/${raceID}/tasks/${taskID}/contributors`, token ? { headers: withAuth(token) } : undefined),
  // 區間任務（平均配速/心率區間）的個人達標明細（哪幾公里達標；需登入）
  taskRangeDetail: (raceID: string, taskID: string, token?: string) =>
    request<{ detail: TaskRangeDetail }>(`/races/${raceID}/tasks/${taskID}/range-detail`, token ? { headers: withAuth(token) } : undefined),
  // 一般模式個人完成排名（帶 token 則含追蹤狀態）
  leaderboard: (raceID: string, token?: string) =>
    request<{ leaderboard: Leaderboard }>(`/races/${raceID}/leaderboard`, token ? { headers: withAuth(token) } : undefined),

  certificate: (raceID: string, token: string) =>
    request<{ certificate: Certificate }>(`/races/${raceID}/certificate`, { headers: withAuth(token) }),

  expBreakdown: (raceID: string, token: string) =>
    request<{ breakdown: ExpBreakdown }>(`/races/${raceID}/exp-breakdown`, { headers: withAuth(token) }),

  // 個人挑戰模式(personal)完成判定引擎觸發點：開個人賽事詳情頁即打，即時評估規則＋CAS 標記完成/逾期（需登入）
  personalProgress: (raceID: string, token: string) =>
    request<PersonalProgress>(`/races/${raceID}/personal-progress`, { headers: withAuth(token) }),

  // 個人挑戰模式(personal)排行榜：依完成次數 desc、最早完成時間 asc（公開；帶 token 則含 is_me/is_following/my_rank）
  personalLeaderboard: (raceID: string, token?: string) =>
    request<PersonalLeaderboard>(`/races/${raceID}/personal-leaderboard`, token ? { headers: withAuth(token) } : undefined),

  // 個人挑戰模式(personal)完賽歷程：取代一般模式完賽證明（需登入，只回呼叫者自己的統計）
  personalHistory: (raceID: string, token: string) =>
    request<{ history: PersonalHistory }>(`/races/${raceID}/personal-history`, { headers: withAuth(token) }),

  // 活動獎勵預覽：完成活動有機會獲得的獎勵（公開，不需登入；不含機率/數量/權重，見 memory activity-reward-system）
  rewardPreview: (raceID: string) =>
    request<{ rewards: RewardPreviewItem[] }>(`/races/${raceID}/reward-preview`),

  // 參賽虛擬獎勵預覽（migration 140）：賽事開始後自動發放給所有已報名者的項目（公開，不需登入；不含
  // 機率/數量/權重，見後端 race.GetEntryRewardPreview）。
  entryRewardPreview: (raceID: string) =>
    request<{ rewards: RewardPreviewItem[] }>(`/races/${raceID}/entry-reward-preview`),

  // 進度頁每日歷程記錄：每一天跑了幾筆、各筆距離/時長/配速（需登入；里程窗與「我的里程」完全一致）
  myDailyActivities: (raceID: string, token?: string) =>
    request<{ days: DailyStat[] }>(`/races/${raceID}/my-daily-activities`, token ? { headers: withAuth(token) } : undefined),

  // GPS 跑步追蹤頁「進行中活動/賽事」面板：目前登入者「現在跑步會被計入」的賽事清單 + 各自進度（需登入）
  myActive: (token: string) => request<{ races: MyActiveRace[] }>('/races/my-active', { headers: withAuth(token) }),
}

export interface TaskProgress extends RaceTask {
  group_name?: string
  scope_label: string // 賽事集體 / 本組團體 / 本組個人
  current: number
  done: boolean
  qualify_count: number
}
export interface RaceProgress {
  // owner_km/pet_km/score/pet_score_mode：同 LeaderboardRow 註解，僅寵物賽事有意義；進度條/完賽判定
  // 一律改用 score（非寵物賽事 score===total_km）。
  my: { total_km: number; activities: number; ascent_m: number; owner_km?: number; pet_km?: number; score?: number; pet_score_mode?: '' | 'pet' | 'owner_pet_sum' }
  has_group: boolean
  group_name?: string
  started: boolean
  registered?: boolean
  tasks: TaskProgress[]
  newly_granted?: GrantedReward[] // 本次呼叫剛觸發「個人額外挑戰」完成時發放的即時獎勵（活動獎勵系統一般化；比照 PersonalProgress.newly_granted）
}
export interface Contributor {
  rank: number
  user_id: string
  name: string
  title?: string // 展示中稱號名稱
  group_name?: string
  distance_km: number
  activities: number
  is_me: boolean
  is_following: boolean // 目前使用者是否已追蹤此人（自己恆 false）
}
export interface TaskContributors {
  task_id: string
  task_title: string
  scope: string
  pool_label: string // 全體參賽者 / 本組：XXX
  total: number
  contributed: number
  top: Contributor[]
  me?: Contributor | null
}
export interface RangeActivity {
  recorded_at: string
  distance_km: number
  avg_pace_s: number
  avg_hr: number
  km_paces: number[]
  qualify_kms: number[] // 1-based：落在配速區間的公里
  qualified: boolean
}
export interface TaskRangeDetail {
  task_id: string
  task_title: string
  metric: string // avg_pace_range | avg_hr_range
  range_lo: number
  range_hi: number
  activities: RangeActivity[]
}
// 活動獎勵預覽單筆卡片（見後端 race.RewardPreviewItem）：可讀展示欄位＋中獎機率，不含權重/庫存等抽獎
// 引擎內部設定。prob_bp 為選填（後端 omitempty，理論上有效設定必為 >0，缺欄位視同未知/不顯示）。
export interface RewardPreviewItem {
  kind: string // economy|serial|coupon
  name: string
  amount: string // economy 類的數量/區間（如 100~500 / 7 天）；serial 類為空
  icon_url: string
  description: string
  prob_bp?: number // 中獎機率，萬分位（10000=100% 必得）；serial 類已是「該面額實際機率」，非商家層機率
}
// 進度頁每日歷程：單筆活動（見後端 race.DailyActivity）
export interface DailyActivity {
  recorded_at: string
  distance_km: number
  duration_s: number
  avg_pace_s: number
  source: string // '' = App GPS；其餘 strava/garmin/coros
  external_id: string // provider 活動 id（Strava→「View on Strava」回連；App GPS 為空字串）
}
// 進度頁每日歷程：某一天的統計 + 當天各筆活動（見後端 race.DailyStat）
export interface DailyStat {
  date: string // 台北日期 YYYY-MM-DD
  total_km: number
  count: number
  activities: DailyActivity[]
}

// --- Admin: 數據總覽 ---
export interface OverviewRace {
  id: string
  title: string
  display_status: string
  can_register: boolean // 供 raceStatusFlags 判定用，與前台 Race.can_register 同義
  start_date: string
  end_date: string
  registrations: number
  tracking_count: number
  tracking_names: string[]
}
export interface AdminOverview {
  races: OverviewRace[]
  tracking_total: number
  generated_at: string
}
export const adminOverviewApi = {
  get: (token: string) => request<AdminOverview>('/admin/overview', { headers: withAuth(token) }),
}

// --- Admin: Races ---

export const adminRacesApi = {
  list: (token: string) =>
    request<{ races: Race[] }>('/admin/races', { headers: withAuth(token) }),
  get: (token: string, id: string) =>
    request<{ race: RaceDetail }>(`/admin/races/${id}`, { headers: withAuth(token) }),
  create: (token: string, payload: CreateRacePayload) =>
    request<{ race: RaceDetail }>('/admin/races', {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(payload),
    }),
  update: (token: string, id: string, race: Race) =>
    request<{ race: Race }>(`/admin/races/${id}`, {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify(race),
    }),
  updateFull: (token: string, id: string, payload: CreateRacePayload) =>
    request<{ race: RaceDetail }>(`/admin/races/${id}`, {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify(payload),
    }),
  remove: (token: string, id: string) =>
    request<void>(`/admin/races/${id}`, {
      method: 'DELETE',
      headers: withAuth(token),
    }),
  setCertificateBg: (token: string, id: string, url: string) =>
    request<void>(`/admin/races/${id}/certificate-bg`, {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify({ url }),
    }),
  setRankDisplay: (token: string, id: string, body: { show_distance_rank: boolean; show_time_rank: boolean }) =>
    request<void>(`/admin/races/${id}/rank-display`, {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify(body),
    }),
  settleExp: (token: string, id: string, force = false) =>
    request<{ result: { race_id: string; participants: number; awarded_users: number; total_exp: number; already_settled: boolean } }>(
      `/admin/races/${id}/settle-exp${force ? '?force=1' : ''}`,
      { method: 'POST', headers: withAuth(token) },
    ),
}

// --- Admin: 個人挑戰模式（event_mode=personal）P5 獎勵管理 ---
// 獎勵＝「完成者中抽獎/限額」，LINE Point 由後台人工發放；系統只管「資格＋發放狀態」。
// 以「每一筆完成」為單位（同一人完成多次＝多筆完成＝多個抽獎資格，對應每次挑戰皆付費 299 的經濟）。

export interface RewardCompletionRow {
  registration_id: string
  user_id: string
  user_name: string
  user_email: string
  completed_at: string
  attempt_no: number
  reward_status: '' | 'won' | 'fulfilled'
  reward_note?: string
  reward_fulfilled_at?: string | null
}
export interface RewardCompletionSummary {
  total: number
  pending: number
  won: number
  fulfilled: number
}
export interface RewardCompletionsResponse {
  completions: RewardCompletionRow[]
  count: number
  summary: RewardCompletionSummary
}

export const adminRewardsApi = {
  list: (token: string, raceId: string, params?: { reward_status?: string; limit?: number; offset?: number }) => {
    const qs = new URLSearchParams()
    if (params?.reward_status !== undefined) qs.set('reward_status', params.reward_status) // ''（待處理）需明確帶出，與「未帶=all」區分
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<RewardCompletionsResponse>(`/admin/races/${raceId}/reward-completions${suffix}`, { headers: withAuth(token) })
  },
  update: (token: string, regId: string, body: { reward_status: '' | 'won' | 'fulfilled'; reward_note: string }) =>
    request<void>(`/admin/reward-completions/${regId}`, {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify(body),
    }),
  draw: (token: string, raceId: string, n: number) =>
    request<{ winners: RewardCompletionRow[]; count: number }>(`/admin/races/${raceId}/reward-draw`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify({ n }),
    }),
}

// --- Admin: 獎勵管理一般化（migration 135）——非 personal 模式賽事的賽後抽獎 ---
// 抽獎資格底線＝完賽（各分組 target_distance_km，與前台排行榜/完賽證明同一條線）；全體/分組/個人額外
// 挑戰(race_tasks)都是疊加的額外目標，不影響資格線。personal 模式仍走上面 adminRewardsApi 舊制。

export type RewardDrawScope = 'all_finishers' | 'winning_group'
export type RewardDrawWinRule = '' | 'highest_metric' | 'first_to_target'

export interface RewardWinnerRow {
  id: string
  draw_id: string
  user_id: string
  user_name: string
  user_email: string
  group_id?: string
  group_name?: string
  reward_status: '' | 'fulfilled'
  reward_note?: string
  reward_fulfilled_at?: string | null
  created_at: string
}

export interface RewardDraw {
  id: string
  race_id: string
  title: string
  scope: RewardDrawScope
  win_rule?: RewardDrawWinRule
  win_task_id?: string
  winner_count: number
  exclude_prior: boolean
  winning_group_ids?: string[]
  pool_size: number
  drawn_by?: string
  drawn_at: string
  winners: RewardWinnerRow[]
}

export interface CreateRewardDrawPayload {
  title: string
  scope: RewardDrawScope
  win_rule?: RewardDrawWinRule
  win_task_id?: string
  winner_count: number
  exclude_prior: boolean
}

export const adminRewardDrawsApi = {
  list: (token: string, raceId: string) =>
    request<{ draws: RewardDraw[]; count: number }>(`/admin/races/${raceId}/reward-draws`, { headers: withAuth(token) }),
  create: (token: string, raceId: string, payload: CreateRewardDrawPayload) =>
    request<{ draw: RewardDraw }>(`/admin/races/${raceId}/reward-draws`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(payload),
    }),
  updateWinner: (token: string, winnerId: string, body: { reward_status: '' | 'fulfilled'; reward_note: string }) =>
    request<void>(`/admin/reward-winners/${winnerId}`, {
      method: 'PATCH',
      headers: withAuth(token),
      body: JSON.stringify(body),
    }),
}

// --- 個人資訊 (Profile) ---

// 里程優先來源：App GPS、Strava、或任一 Terra 手錶品牌（小寫）。見 memory terra-wearable-integration。
export type DataSource = 'gps' | 'strava' | 'garmin' | 'coros' | 'polar' | 'suunto' | 'wahoo'
const DATA_SOURCE_LABEL: Record<DataSource, string> = {
  gps: 'GPS 跑步追蹤', strava: 'Strava', garmin: 'Garmin', coros: 'COROS', polar: 'Polar', suunto: 'Suunto', wahoo: 'Wahoo',
}
// 里程優先來源顯示名稱（ProfileScreen 的「里程優先來源」按鈕、track 頁的三選一彈窗共用，避免各自維護一份）
export function sourceLabel(src: string): string {
  return DATA_SOURCE_LABEL[src as DataSource] ?? src
}

export interface Profile {
  user_id: string
  email: string
  name: string         // 顯示名稱
  avatar_url: string
  real_name: string
  nickname: string
  phone: string
  address: string
  birthday: string // YYYY-MM-DD
  gender: '' | 'male' | 'female' | 'other'
  preferred_data_source?: DataSource // 跨來源去重偏好
  invoice: InvoiceInfo // 發票資訊（上次填的，供報名表單預填）
}

export interface DedupSide { source: 'gps' | 'strava'; distance_km: number; duration_s: number; recorded_at: string }
export interface DedupNotice { gps: DedupSide; strava: DedupSide; current_preference: 'gps' | 'strava' }

export interface DashboardInfo {
  name: string
  nickname: string
  displayed_title: string // 展示中稱號名稱（空=未設定，面板顯示於顯示名稱下方）
  handle: string
  avatar_url: string
  account_code: string
  exp: number
  dp: number
  gp: number // GP 幣餘額（環台大富翁）
  level: number
  level_title: string
  level_floor: number
  next_level_exp: number | null
  is_vip: boolean
  vip_expires_at?: string
  vip_plan: '' | 'trial' | 'monthly' | 'annual' // 訂閱方案（''=無）
  activity_coupon_balance: number               // 活動優惠券($100)剩餘張數
  activity_coupon_value_cents: number // 活動優惠券面額（分）；後台系統設定 vip_coupon_value_cents 可調
  show_trial_expiry_notice: boolean             // 試用到期 + 尚未提示過 → 前台跳一次升級彈窗
  total_km: number
  race_count: number
  ongoing_count: number
  completed_count: number
  following_count: number
  follower_count: number
  personal_entry: 'hidden' | 'locked' | 'shown' // 個人任務入口可見性（後端解析）
  explore_entry: 'hidden' | 'locked' | 'shown'  // 城市探索入口可見性
  gallery_entry: 'hidden' | 'locked' | 'shown'  // 卡片圖鑑入口可見性
  title_entry: 'hidden' | 'locked' | 'shown'       // 稱號系統(PB探索)入口可見性
  achievement_entry: 'hidden' | 'locked' | 'shown' // 成就統計(成就探索)入口可見性
  training_entry: 'hidden' | 'locked' | 'shown'    // 自主訓練入口可見性
  strategy_entry: 'hidden' | 'locked' | 'shown'    // 賽事策略入口可見性（自主訓練第三分頁）
  cheer_test_entry: 'hidden' | 'locked' | 'shown' // 每公里應援「測試觸發」按鈕入口（cheer_test_entry_state/whitelist，2026-08-29）
  cheer_display_ms: number // 應援表演（泡泡框+啦啦隊）顯示毫秒數，系統設定 cheer_display_ms，預設 3000
  cheer_edit_entry: 'hidden' | 'locked' | 'shown' // 啦啦隊位置校正模式入口（cheer_edit_entry_state/whitelist，2026-08-29）
  cheer_char_layout: string // 啦啦隊三張角色的位置校正 JSON 字串（CheerCharLayout；系統設定 cheer_char_layout），前端 try/catch 解析
  monopoly_entry: 'hidden' | 'locked' | 'shown'    // 環台大富翁入口可見性
  knowledge_entry: 'hidden' | 'locked' | 'shown'   // 知識探索(知識卡圖鑑)入口可見性
  gov500_entry: 'hidden' | 'locked' | 'shown'      // 500.gov.tw「揮汗有禮」截圖模式入口可見性（純前端 UI 開關，見 lib/gov500.ts）
  // 遊戲化角色數值（RO 素質系統，見 internal/rpg）：只有 VVIP／白名單管理者看得到，故只有 hidden|shown 兩態
  // （無 locked——不對一般會員揭露「有這個功能但鎖住」）。後端以 rpg_entry_state/whitelist + is_vvip 解析。
  rpg_entry: 'hidden' | 'shown'
  // 團練邀請（見 internal/runmeet）：入口三態 + 本月剩餘發起次數（只用在「＋ 發起團練」按鈕文案，
  // 不做成入口徽章——會被誤讀成「還能加入 N 個團練」，見 lib/runMeet.ts createBtnText 註解）。
  // entry 非 shown 時 runmeet_remaining 恆 0（後端不查 DB，dashboard 熱路徑零額外成本）。
  runmeet_entry: 'hidden' | 'locked' | 'shown'
  runmeet_remaining: number
  // 團練開跑前 Email 提醒開關（users.runmeet_reminder_email，migration 163；預設 true）。
  // 站內信不受此開關影響（一律發）；切換走 profileApi.setNotifyPrefs。
  runmeet_reminder_email: boolean
  new_titles?: { code: string; name: string; tier: number; category: string }[] // 新解鎖稱號（前台跳彈窗用，跳完呼叫 /titles/seen）
  // 體力值 SP（跑步後依距離×強度扣、依跑步水準以時間恢復；扣到 0 凍結 6 小時）
  sp: number
  sp_max: number
  sp_recover_min: number       // 每恢復 1 點所需分鐘
  sp_next_recover_sec: number  // 距下一點恢復秒數（0=已滿）
  sp_freeze_until: string | null // 過度訓練凍結到此時間（null=無）
  fitness: number              // 跑步水準 0-100
  // GPS 距離校正（見 internal/gpscalib，2026-08-30）：gps_calib_factor 與 GPS 上傳當下套用的係數同一函式
  // （gpscalib.EffectiveFactor）算出，保證「看到的＝入帳的」；entry 非 shown 時 factor 恆 1.0。
  gps_calib_entry: 'hidden' | 'locked' | 'shown'
  gps_calib_factor: number
  gps_calib_status: GpsCalibState
  gps_calib_pairs: number // 視窗內配對數
  gps_calib_enabled: boolean
}

// --- 稱號系統 (PB探索) ---

export interface TitleCat { key: string; label: string }
export interface TitleItem {
  code: string
  category: string
  name: string // 未解鎖時已被伺服器遮成「？？？？？？？？」
  tier: number // 1~6，越高越華麗
  threshold: number
  unit: string
  earned: boolean
  earned_at?: string
}

export const titleApi = {
  list: (token: string) =>
    request<{ categories: TitleCat[]; titles: TitleItem[]; displayed: string }>('/profile/titles', { headers: withAuth(token) }),
  // code='' 取消展示
  display: (token: string, code: string) =>
    request<{ ok: boolean }>('/profile/titles/display', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ code }) }),
  seen: (token: string, codes: string[]) =>
    request<{ ok: boolean }>('/profile/titles/seen', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ codes }) }),
}

// --- 成就統計 (成就探索) ---

export interface AchievementStats {
  single_max_km: number
  cum_km: number
  single_max_sec: number
  cum_sec: number
  activity_count: number
  streak_days: number
  checkin_count: number
  boss_count: number
  boss_s1: number
  boss_s2: number
  boss_s3: number
  personal_count: number
  level: number
  level_title: string
  card_count: number
  following: number
  followers: number
  dp: number
  race_count: number
}

export interface AchievementCalendarDay { date: string; km: number }
export interface AchievementCalendar { month: string; total_km: number; days: AchievementCalendarDay[] }
export interface AchievementDayActivity { id: string; source: string; distance_km: number; duration_s: number; avg_pace_s: number; flagged: boolean; flag_reason?: string; recorded_at: string; external_id?: string }

export const achievementApi = {
  stats: (token: string) => request<AchievementStats>('/profile/achievements', { headers: withAuth(token) }),
  calendar: (token: string, month: string) =>
    request<AchievementCalendar>(`/profile/achievements/calendar?month=${encodeURIComponent(month)}`, { headers: withAuth(token) }),
  day: (token: string, date: string) =>
    request<{ date: string; activities: AchievementDayActivity[] }>(`/profile/achievements/day?date=${date}`, { headers: withAuth(token) }),
}

// VIP 訂閱優惠檔期（後台管理）。pay_pct=實付%（70=付七成、即打七折）
export interface VipPromo {
  id: string
  name: string
  plan: 'monthly' | 'annual' | 'both'
  pay_pct: number
  starts_at?: string | null
  ends_at?: string | null
  active: boolean
  created_at?: string
}

export interface DataSourceMetrics {
  need_direct_watch: number
  watch_users: number
  garmin_users: number
  coros_users: number
  strava_users: number
  gps_users: number
}

export interface VipAnalytics {
  total: number
  vip: number
  general: number
  vip_by_plan: { trial: number; monthly: number; annual: number }
  last_month_non_renewers: { user_id: string; name: string; email: string; plan: string; expired_at: string }[]
  growth: { month: string; count: number }[]
  churn: { month: string; count: number }[]
}

export const adminMetricsApi = {
  dataSource: (token: string) =>
    request<DataSourceMetrics>('/admin/data-source-metrics', { headers: withAuth(token) }),
  vipAnalytics: (token: string) =>
    request<VipAnalytics>('/admin/vip-analytics', { headers: withAuth(token) }),
}

export const adminVipPromosApi = {
  list: (token: string) =>
    request<{ promos: VipPromo[] }>('/admin/vip-promos', { headers: withAuth(token) }),
  save: (token: string, p: Partial<VipPromo>) =>
    request<{ id: string }>('/admin/vip-promos', { method: 'POST', headers: withAuth(token), body: JSON.stringify(p) }),
  del: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/vip-promos/${id}/delete`, { method: 'POST', headers: withAuth(token) }),
}

// VIP 方案定價（元）。price=折後、save=現省、promo=是否套用折扣
export interface VipPlanPrice { original: number; price: number; save: number; promo: boolean }
export interface VipPricing {
  monthly: VipPlanPrice
  annual: VipPlanPrice
  in_promo_window: boolean
  promo_ends_at?: string
  trial_days: number
  is_vip: boolean
  vip_plan: '' | 'trial' | 'monthly' | 'annual'
  vip_expires_at?: string
  coupon_value_ntd: number
  coupon_per_month: number
}

export interface FollowRow {
  user_id: string
  nickname: string
  avatar_url: string
}

export interface LeaderboardRow {
  rank: number
  user_id: string
  nickname: string
  title: string // 目前展示中的稱號（無則空字串）
  group_name?: string
  completion_at?: string
  total_time_s: number
  distance_km: number
  is_following: boolean
  is_me: boolean
  // 寵物雲端馬拉松（僅該場 race.pet_kind!==''才有意義；非寵物賽事恆 owner_km===distance_km、
  // pet_km===0、pet_score_mode===''）：owner_km=飼主里程、pet_km=狗狗累積里程、
  // score=依 pet_score_mode 算出的成績（排名/完賽判定實際用的數字，distance_km 保留供舊版相容顯示）。
  owner_km?: number
  pet_km?: number
  score?: number
  pet_score_mode?: '' | 'pet' | 'owner_pet_sum'
}
export interface Leaderboard {
  finished_count: number
  total_count: number
  by_completion: LeaderboardRow[]
  by_total_time: LeaderboardRow[]
}

export interface LevelConfig {
  level: number
  title: string
  exp_required: number
}
export interface ExpRules {
  per_collective_task: number
  per_group_task: number
  per_individual_task: number
  per_km: number
  dp_per_collective_task: number
  dp_per_group_task: number
  dp_per_individual_task: number
  dp_per_km: number
  mileage_cap_km: number     // 單趟里程獎勵上限（整公里）
  mileage_min_pace_s: number // 防造假：最快合理配速（秒/公里）
  // VIP 天數平行費率（取得來源同 EXP／DP，達標時延長玩家 VIP 會員資格；0＝不發）
  vip_days_collective_task: number
  vip_days_group_task: number
  vip_days_individual_task: number
  // GP（環台大富翁貨幣）平行費率，範圍同 VIP 天數：僅任務完成三種 scope，里程無 GP；0＝不發
  gp_per_collective_task: number
  gp_per_group_task: number
  gp_per_individual_task: number
}

export interface MileageConfig {
  per_km: number
  dp_per_km: number
  cap_km: number
}

export interface AthleteStats {
  volume_km: number
  activities: number
  pace_s: number
  avg_dist_km: number
  longest_km: number
  monthly_freq: number
  score: number
  level: string
}
export interface AthleteMetricConfig {
  metric_key: string
  weight: number
  ref_lo: number
  ref_hi: number
  display_order: number
}
export interface AthleteLevel {
  min_score: number
  name: string
}
export interface RecommendRow {
  user_id: string
  nickname: string
  avatar_url: string
}

export interface MyRegistration {
  registration_id: string
  race_id: string
  race_title: string
  race_slug: string
  group_name: string
  group_revealed: boolean
  status: string
  created_at: string
  order_id?: string
  order_total_cents: number
  order_status?: string
  // 取消報名 / 分級退費
  can_cancel: boolean
  cancel_blocked_reason: string
  refund_ratio: number
  estimated_refund_cents: number
  cancel_request_status: string // ''|'pending'|'processing'|'approved'|'rejected'
  refund_disabled: boolean // 該賽事 config.refund_disabled；true 時報名紀錄頁不顯示「申請取消報名」，改顯示「本活動不適用七天鑑賞期」
  // 電子發票（見 services/api/internal/einvoice）；無資料列（舊資料／尚未觸發開立）＝undefined
  invoice_number?: string
  invoice_status?: string // pending|issuing|issued|void|failed|skipped
  issued_at?: string | null
  // 寵物雲端馬拉松：僅寵物賽事（該場 race.pet_kind !== ''）有值；一般賽事省略。
  pets?: RegistrationPet[]
}

export interface MyOrderItem {
  item_type: string
  addon_name?: string
  qty: number
  subtotal_cents: number
}

export interface MyOrder {
  id: string
  race_title: string
  total_cents: number
  status: string
  payment_ref?: string
  created_at: string
  items: MyOrderItem[]
}

// VIP 訂閱 Phase E：綁卡 SDK 串接（見 lib/ecpay.ts loadEcpaySdk／components/BindCardModal）
export interface VipSubscribeResponse {
  token: string             // 綠界綁卡 Token（前端 ECPay.addBindingCard 用）
  token_expire_date: string
  order_id: string
  merchant_trade_no: string
  amount_cents: number
  server_type: 'Stage' | 'Prod' // ECPay.initialize(ServerType,...) 用，後端依 ECPayBindEnv 決定，前端不用自己猜
}
export interface VipBindCompleteResponse {
  status: 'paid' | '3d_required'
  card_last4?: string   // status=paid
  three_d_url?: string  // status=3d_required：整頁導轉（勿用 iframe，綠界官方明示）
}
export interface VipCardInfo {
  bound: boolean
  card_last4?: string
  card_expiry_mm?: string
  card_expiry_yy?: string
}

export const profileApi = {
  getMe: (token: string) =>
    request<{ profile: Profile }>('/profile', { headers: withAuth(token) }),
  updateMe: (token: string, body: Partial<Profile>) =>
    request<{ profile: Profile }>('/profile', {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify(body),
    }),
  dashboard: (token: string) =>
    request<{ dashboard: DashboardInfo }>('/profile/dashboard', { headers: withAuth(token) }),
  // VIP 訂閱：方案定價（依此帳號促銷資格）、標記試用到期彈窗已顯示
  vipPricing: (token: string) =>
    request<VipPricing>('/profile/vip/pricing', { headers: withAuth(token) }),
  vipCancel: (token: string) =>
    request<{ ok: boolean; vip_expires_at?: string }>('/profile/vip/cancel', { method: 'POST', headers: withAuth(token) }),
  // VIP 訂閱 Phase E：發起訂閱（取得綁卡 Token）／綁卡完成／卡片查詢/解除
  vipSubscribe: (token: string, plan: 'monthly' | 'annual') =>
    request<VipSubscribeResponse>('/profile/vip/subscribe', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ plan }) }),
  vipBindComplete: (token: string, bindCardPayToken: string, orderId: string) =>
    request<VipBindCompleteResponse>('/profile/vip/bind-card/complete', {
      method: 'POST', headers: withAuth(token),
      body: JSON.stringify({ bind_card_pay_token: bindCardPayToken, order_id: orderId }),
    }),
  vipCard: (token: string) =>
    request<VipCardInfo>('/profile/vip/card', { headers: withAuth(token) }),
  vipCardDelete: (token: string) =>
    request<{ ok: boolean }>('/profile/vip/card', { method: 'DELETE', headers: withAuth(token) }),
  markTrialNoticeShown: (token: string) =>
    request<{ ok: boolean }>('/profile/trial-notice-shown', { method: 'POST', headers: withAuth(token) }),
  // 跨來源去重：偏好來源、首次彈窗（來源尚未連接時後端回 400 { error: 'not_connected' }）
  setDataSource: (token: string, source: DataSource) =>
    request<{ ok: boolean; preferred_data_source: string }>('/profile/data-source', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ source }) }),
  // 通知偏好（目前：團練開跑前 Email 提醒）。比照 setDataSource 同一慣例：小 body、只改一個欄位。
  setNotifyPrefs: (token: string, body: { runmeet_reminder_email: boolean }) =>
    request<{ ok: boolean; runmeet_reminder_email: boolean }>('/profile/notify-prefs', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  dedupNotice: (token: string) =>
    request<{ notice: DedupNotice | null }>('/profile/dedup-notice', { headers: withAuth(token) }),
  dedupResolve: (token: string, choice: 'gps' | 'strava', remember: boolean) =>
    request<{ ok: boolean }>('/profile/dedup-resolve', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ choice, remember }) }),
  uploadAvatar: async (token: string, file: File): Promise<{ id: string; url: string }> => {
    const fd = new FormData()
    fd.append('file', file)
    const res = await fetch(`${BASE}/profile/avatar`, { method: 'POST', headers: { Authorization: `Bearer ${token}` }, body: fd })
    const text = await res.text()
    const data = text ? JSON.parse(text) : null
    if (!res.ok) throw new ApiError(res.status, data?.error ?? '頭像上傳失敗')
    return data as { id: string; url: string }
  },
  registrations: (token: string) =>
    request<{ registrations: MyRegistration[]; count: number }>('/profile/registrations', { headers: withAuth(token) }),
  // 取消報名申請：建立 / 撤回（僅本人待審中的申請可撤回）
  cancelRequest: (token: string, registrationID: string, reason: string) =>
    request<{ ok: boolean }>(`/profile/registrations/${registrationID}/cancel-request`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify({ reason }),
    }),
  withdrawCancelRequest: (token: string, registrationID: string) =>
    request<{ ok: boolean }>(`/profile/registrations/${registrationID}/cancel-request`, {
      method: 'DELETE',
      headers: withAuth(token),
    }),
  order: (token: string, orderID: string) =>
    request<{ order: MyOrder }>(`/profile/orders/${orderID}`, { headers: withAuth(token) }),
  follows: (token: string) =>
    request<{ following: FollowRow[]; following_count: number; follower_count: number }>('/profile/follows', { headers: withAuth(token) }),
  recommendations: (token: string, raceID: string) =>
    request<{ recommendations: RecommendRow[] }>(`/profile/recommendations/${raceID}`, { headers: withAuth(token) }),
}

// --- 推廣連結（累積 10km 才可產生；朋友註冊+達標雙方各得 VIP 天數）---

export interface ReferralInfo {
  referral_code: string
  referred_count: number
  rewarded_count: number
  reward_days_referrer: number
  reward_days_referred: number
}

export const referralApi = {
  // 產生（或取得既有）本人的推廣碼；累積里程未達 10km 會回 403
  generate: (token: string) =>
    request<ReferralInfo>('/profile/referral', { method: 'POST', headers: withAuth(token) }),
  // 只查現況、不產生；沒產生過會回空 referral_code（供頁面 mount 時回顯既有連結，不會誤觸發 403）
  get: (token: string) =>
    request<ReferralInfo>('/profile/referral', { headers: withAuth(token) }),
}

export const followApi = {
  follow: (token: string, userId: string) =>
    request<{ following: boolean }>('/profile/follow', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ user_id: userId }) }),
  unfollow: (token: string, userId: string) =>
    request<null>(`/profile/follow/${userId}`, { method: 'DELETE', headers: withAuth(token) }),
}

// --- 百里英雄榜（累積里程 >= 100km 前 100 名；公開，登入後附帶 is_following/is_self）---

export interface HundredHero {
  user_id: string
  name: string
  avatar_url: string
  total_km: number
  title: string          // 目前掛載展示的稱號名稱；未掛載為空字串
  is_following: boolean // 登入時才有意義；未登入恆 false
  is_self: boolean       // 登入時才有意義；未登入恆 false
}

export const heroesApi = {
  hundred: (token?: string) =>
    request<{ heroes: HundredHero[]; count: number }>('/heroes/hundred', token ? { headers: withAuth(token) } : undefined),
}

// --- 個人任務（跑者生命週期 10 計畫 × 每 100 天鏈式任務）---

export interface PersonalPlan {
  id: string
  code: string
  name: string
  lifecycle: string
  stage_order: number
  target_km: number
  target_time: string
  entry_note: string
  data_source: string // gps | strava
  banner_url: string
  enabled: boolean
  total: number     // 任務總數
  completed: number // 我完成數
}

export interface PersonalTask {
  id: string
  plan_id: string
  plan_code: string
  day: number
  week: number
  title: string
  story: string
  workout: string
  workout_type: string
  target_km: number
  target_min: number
  intensity: string
  complete_cond: string
  completion_type: string
  reward_exp: number
  reward_dp: number
  icon_url: string
  data_source: string
  safety_note: string
  enabled: boolean
  done: boolean               // 已完成至少 1★
  stars: number               // 最高星數 0..3
  attempts: number            // 已挑戰次數（>0 → 下次挑戰要付 DP）
  active: boolean             // 有進行中的挑戰
  challenge_tier: number      // 進行中挑戰的星級
  challenge_target_km: number // 進行中挑戰的縮放目標
  retry_dp_cost: number       // 重挑 DP 花費
  workout_kind: string        // 非空＝結構化課表（帶到 GPS 追蹤跑）
  segments: WorkoutSegment[]  // 分段課表
}

// 結構化課表的一個分段
export interface WorkoutSegment {
  kind: 'warmup' | 'work' | 'rest' | 'recovery' | 'cooldown' | 'steady'
  label?: string
  target_type: 'distance' | 'time'
  target: number        // 距離(公尺) 或 時間(秒)
  pace_fast_s?: number  // 較快界（秒/公里，較小）
  pace_slow_s?: number  // 較慢界
  reps?: number         // 組數（如 400m×6）
  rest_s?: number       // 組間休息秒數
}

// 自主訓練（P1）：課表庫的一個分段——以「效度 effort」表達強度，前端選定配速等級後解析成 WorkoutSegment。
export interface TemplateSegment {
  kind: 'warmup' | 'work' | 'rest' | 'recovery' | 'cooldown'
  label?: string
  effort?: 'easy' | 'marathon' | 'threshold' | 'interval' | 'rep'
  target_type: 'distance' | 'time'
  target: number
  reps?: number
  rest_s?: number
}

// 自主訓練：課表庫的一份課表（system seed，非玩家挑戰制——跑步照常走 GPS 上傳自動發里程 EXP）。
export interface WorkoutTemplate {
  code: string
  name: string
  category: string
  description: string
  segments: TemplateSegment[]
  sort_order: number
  // P3：false＝距離變體（產生器排課用，如 lsd_20/easy_8），不進「課表庫」清單／選課表 modal；
  // 但仍可用 template_code 解析分段（開跑用）。缺省視為 true（相容舊回應）。
  library_visible?: boolean
  // 課表可微調（migration 085）：distance(調總距離)/reps(調趟數)/pyramid(調峰值±400m)/none。
  // 決定「開始訓練」卡片與選課表 modal 是否顯示 −/＋ 微調列及其步階單位，見 lib/workout.ts adjustMeta。
  adjust_type?: string
}

// 自主訓練：配速等級（玩家自選，決定 TemplateSegment.effort 對應的實際配速秒/公里）。
export interface PaceLevel {
  id: number
  label: string
  paces: {
    easy: { fast: number; slow: number }
    marathon: { fast: number; slow: number }
    threshold: { fast: number; slow: number }
    interval: { fast: number; slow: number }
    rep: { fast: number; slow: number }
  }
}

// 挑戰制：進行中挑戰的即時狀態
export interface PersonalChallenge {
  task_id: string; plan_code: string; day: number; title: string
  kind: 'mileage' | 'rest' | 'manual' | 'workout'
  tier: number
  target_km: number; acc_km: number; data_source: string // gps | strava
  rest_window_s: number; elapsed_s: number
  met: boolean; failed: boolean
  workout_kind: string
  segments: WorkoutSegment[] | null // workout：分段課表（給 /track 驅動）
}

// /track 任務面板卡：某計畫「目前可挑戰的結構化課表任務」
export interface PanelCard {
  plan_code: string; plan_name: string; stage_order: number
  task_id: string; day: number; title: string; workout_kind: string
  segments: WorkoutSegment[] | null
  stars: number; attempts: number; retry_dp_cost: number; active: boolean
  vip_locked: boolean // 階段 4+ 且非 VIP → 鎖住
}

export const personalTasksApi = {
  listPlans: (token: string) =>
    request<{ plans: PersonalPlan[] }>('/personal-tasks', { headers: withAuth(token) }),
  // /track 面板：各計畫前沿 workout 卡 + 進行中挑戰卡（可左右滑動切換階段）
  trackPanel: (token: string) =>
    request<{ cards: PanelCard[]; active_card: PanelCard | null }>('/personal-tasks/track-panel', { method: 'POST', headers: withAuth(token) }),
  planDetail: (token: string, code: string) =>
    request<{ plan: PersonalPlan; tasks: PersonalTask[] }>(`/personal-tasks/plans/${code}`, { headers: withAuth(token) }),
  // 進行中挑戰的即時狀態（開頁/輪詢/跑步後呼叫）
  status: (token: string) =>
    request<{ challenge: PersonalChallenge | null }>('/personal-tasks/status', { method: 'POST', headers: withAuth(token) }),
  // 開始挑戰（第一次免費、之後扣 DP）
  challenge: (token: string, taskId: string) =>
    request<{ challenging?: boolean; already?: boolean; tier: number; kind?: string; target_km?: number; charged_dp?: number; rest_window_s?: number }>(
      `/personal-tasks/tasks/${taskId}/challenge`, { method: 'POST', headers: withAuth(token) }),
  // 放棄（判失敗、可重挑）
  abandon: (token: string, taskId: string) =>
    request<{ ok: boolean }>(`/personal-tasks/tasks/${taskId}/abandon`, { method: 'POST', headers: withAuth(token) }),
  // 完成（僅達標可完成；發星 + 獎勵）。workout 課表由 /track 送 finished/work_in_band/work_total。
  complete: (token: string, taskId: string, body?: { pain?: number; rpe?: number; finished?: boolean; work_in_band?: number; work_total?: number; evidence?: unknown }) =>
    request<{ completed: boolean; stars: number; tier: number; reward_exp: number; reward_dp: number }>(
      `/personal-tasks/tasks/${taskId}/complete`,
      { method: 'POST', headers: withAuth(token), body: JSON.stringify(body || {}) },
    ),
}

// 自主訓練：某日已排的課表（快照：template_code/name/category/pace_level + 算好的 planned_km/planned_min）；
// P3 一天可多份——id 供單筆刪除/操作定位；plan_id 非 null＝來自某訓練計畫（plan_name 為該計畫名稱快照），
// null＝手動排定。
export interface ScheduledWorkout {
  id: string
  plan_id: string | null
  plan_name?: string
  template_code: string
  name: string
  category: string
  pace_level: number
  planned_km: number
  planned_min: number
  adjust: number // 微調量（migration 085；0＝課表預設，距離型±km/間歇型±趟/金字塔±400m峰值階）
}
// 自主訓練：月曆單日——已排課表（P3 改陣列，一天可多份） + 當日實際跑量（GPS/Strava 等未被 flag 的活動，依台北時區分桶）
export interface TrainingDay {
  date: string // YYYY-MM-DD
  scheduled: ScheduledWorkout[]
  actual_km: number
  has_activity: boolean
}
// 自主訓練：月曆整月資料——當月排定/實際的天數、里程、時間總計 + 每日明細
// planned.days＝有排課的 distinct 日數；planned.km/min＝當月所有 scheduled 加總（同日多份會加總）
export interface TrainingCalendar {
  month: string // YYYY-MM
  planned: { days: number; km: number; min: number }
  actual: { days: number; km: number; min: number }
  days: TrainingDay[]
}

// 自主訓練（P3）：一鍵產生的訓練計畫——依跑齡/最佳配速/最長跑量 + 目標賽事(或週數)自動排一組課表，
// 每帳號最多 3 個（後端把關，見 trainingApi.plans 的 limit）。
export interface TrainingPlan {
  id: string
  name: string
  race_name: string // 使用者自填的賽事名稱，可能為空字串 → 顯示時一律 race_name || name
  race_date: string | null
  race_distance: string // '5k' | '10k' | 'half' | 'full' | ''
  weeks: number
  days_per_week: number
  pace_level: number
  start_date: string
  end_date: string
  workout_count: number
  monthly_km: number   // 使用者填寫的目前月跑量(km)；0=未填
  goal_time_s: number  // 目標完賽秒數；0=未設定
  goal_pace_s: number  // 目標配速(秒/km)；0=無（需 goal_time_s 與賽事距離皆有值才會算）
  plan_mode: string    // 課表強度：'conservative'（保守）｜'aggressive'（積極）
  // 該計畫的進度與執行狀況（取代舊版「本月總覽」，改成以計畫為單位呈現）
  stats: {
    planned: { days: number; km: number; min: number }
    actual: { days: number; km: number; min: number }
    total_days: number
    elapsed_days: number
    remaining_days: number
  }
}
// 自主訓練（P3）：POST /training/auto-plan 請求體——「一鍵安排課表」表單送出的內容。
// has_race=true 時帶 race_date/race_distance（依賽事日回推排課），false 時帶 weeks（依週數排課）。
export interface AutoPlanRequest {
  running_age: 'new' | 'novice' | 'experienced' | 'veteran' // 跑齡：不到1年／1-3年／3-5年／5年以上
  best_1km_s: number  // 1km 最快（秒）
  longest_km: number  // 最長距離（km）
  longest_min: number // 最長時間（分）
  has_race: boolean
  race_name?: string // 使用者自填賽事名稱，選填
  race_date?: string
  race_distance?: '5k' | '10k' | 'half' | 'full'
  weeks?: number       // has_race=false：1|4|8|12|16
  rest_days: number[]  // 預定休息的星期索引，0=週一..6=週日（前端 checkbox 一..日）；其餘星期皆為訓練日，
                        // 全 7 天皆休（無訓練日）後端回 400 {error:"need_training_day"}
  monthly_km?: number  // 目前月跑量(km)，選填；0/未填=不套用跑量模型(沿用舊行為)
  goal_time_s?: number // 目標完賽秒數，選填（全馬 4:30:00 = 16200）；0/未填=未設定
  plan_mode?: 'conservative' | 'aggressive' // 課表強度：不填/非法值後端一律當 'conservative'
  start_long_km?: number // 期望起始長距離(km)，選填；0/未填=依近三週實際最長跑步/自報最長距離自動判斷
}

// 自主訓練（P1+P2+P3）：課表庫 + 配速等級表、月曆排程 CRUD、一鍵訓練計畫。VIP 限定——非 VIP 呼叫回 403
// {error:"vip_only"}（呼叫端請用 catch (e:any) { if (e?.status === 403 && e?.message === 'vip_only') ... } 辨識）。
export const trainingApi = {
  templates: (token: string) =>
    request<{ templates: WorkoutTemplate[]; pace_levels: PaceLevel[] }>('/training/templates', { headers: withAuth(token) }),
  // 月曆：指定月份的排程（每日可多份） + 實際跑量彙總
  calendar: (token: string, month: string) =>
    request<TrainingCalendar>(`/training/calendar?month=${encodeURIComponent(month)}`, { headers: withAuth(token) }),
  // 排課（手動，plan_id 固定 NULL）：P3 改 INSERT 一筆（不再 upsert-by-date，一天可多份），回含 id 的新列
  schedule: (token: string, body: { date: string; template_code: string; pace_level: number; planned_km: number; planned_min: number; adjust?: number }) =>
    request<ScheduledWorkout & { date: string }>('/training/schedule', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  // 移除單筆排課（依 id；來自計畫的課表也可單筆移除，不影響同計畫其餘課表／不刪計畫本身）
  unschedule: (token: string, id: string) =>
    request<{ ok: boolean }>(`/training/schedule/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),
  // 搬移單筆排課到另一天（月曆長按拖曳）：同來源(plan_id 相同或皆 NULL)若目標日已有課表，後端會自動
  // 往後推擠找空日；完全找不到空日回 409 {error:"no_free_day"}（呼叫端用 e.status===409 && e.message==='no_free_day' 辨識）
  moveSchedule: (token: string, id: string, date: string) =>
    request<{ ok: boolean; moved: number }>(`/training/schedule/${encodeURIComponent(id)}/move`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ date }) }),
  // 我的訓練計畫（≤3）
  plans: (token: string) =>
    request<{ plans: TrainingPlan[]; limit: number }>('/training/plans', { headers: withAuth(token) }),
  // 一鍵產生訓練計畫；已有 3 個回 409 {error:"plan_limit"}（呼叫端用 e.status===409 && e.message==='plan_limit' 辨識）
  // goal_note/volume_note：目標偏積極/月跑量偏低的可行性提示，可能為空字串；提示不代表失敗，計畫仍會產生
  autoPlan: (token: string, body: AutoPlanRequest) =>
    request<{ plan: TrainingPlan; goal_note: string; volume_note: string }>('/training/auto-plan', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  // 刪除訓練計畫（其排程 CASCADE 一併刪除）
  deletePlan: (token: string, id: string) =>
    request<{ ok: boolean }>(`/training/plans/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),
}

// 賽事策略（自主訓練新分頁）：配速計劃（分段目標配速）＋補給計劃（時間/距離觸發提醒），
// 開跑時帶 /track?strategy=<id> 進入「比賽專注模式」（半透明黑底大字資訊+配速/補給提醒）。
// 比照自主訓練 v0.1.565 慣例：清單/單筆為唯讀瀏覽（登入即可），建立/修改/刪除為 VIP 動作
// （非 VIP 回 403 {error:"vip_only"}）；每帳號最多 5 份（後端把關，超過回 409 {error:"strategy_limit"}）。
export type FuelKind = 'gel' | 'salt' | 'electrolyte' | 'caffeine'
export const FUEL_KIND_LABEL: Record<FuelKind, string> = { gel: '能量膠', salt: '鹽錠', electrolyte: '電解質', caffeine: '咖啡因錠' }
// 配速段：from_km 由前一段 to_km 銜接（首段固定 0），pace_s=目標配速（秒/公里）
export interface StrategySegment { from_km: number; to_km: number; pace_s: number }
// 補給點：mode='time' 時 at=開跑後秒數；mode='distance' 時 at=移動距離公尺
export interface FuelPoint { kind: FuelKind; mode: 'time' | 'distance'; at: number }
export interface RaceStrategy {
  id: string
  name: string
  total_km: number // 冗餘欄位＝segments 最後一段 to_km，供列表顯示與 ETA 計算
  segments: StrategySegment[]
  fuel: FuelPoint[]
  created_at: string
  updated_at: string
}
export const strategiesApi = {
  list: (token: string) =>
    request<{ strategies: RaceStrategy[]; limit: number }>('/training/strategies', { headers: withAuth(token) }),
  get: (token: string, id: string) =>
    request<{ strategy: RaceStrategy }>(`/training/strategies/${encodeURIComponent(id)}`, { headers: withAuth(token) }),
  create: (token: string, body: { name: string; segments: StrategySegment[]; fuel: FuelPoint[] }) =>
    request<{ strategy: RaceStrategy }>('/training/strategies', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: { name: string; segments: StrategySegment[]; fuel: FuelPoint[] }) =>
    request<{ strategy: RaceStrategy }>(`/training/strategies/${encodeURIComponent(id)}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) =>
    request<{ ok: boolean }>(`/training/strategies/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),
}

export const adminPersonalTasksApi = {
  list: (token: string) =>
    request<{ plans: PersonalPlan[]; tasks: PersonalTask[] }>('/admin/personal-tasks', { headers: withAuth(token) }),
  import: (token: string, body: { plans: unknown[]; tasks: unknown[] }) =>
    request<{ plans: number; tasks: number }>('/admin/personal-tasks/import', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
}

// 城市探索：打卡點關主。
// P1.5 縮小 List payload：GET /explore（列表）不再回傳挑戰面板專用的重欄位（quote/skill_name/skill_desc/
// dialogue_intro/dialogue_start/segments/card_image_url/master_image_url，見後端 explore.go listCols）——
// 這些欄位在此型別改為 optional，列表來源(ExploreScreen/track 的 exList)不會有值；點開挑戰面板時改用
// exploreApi.detail() 或 Checkin 回應（皆回完整資料，此型別同時也代表那兩者的回應形狀）。
// scene_image_url 例外：清單(ExploreScreen RevealCard)直接顯示此圖，仍是必填、清單就有值。
export interface ExploreBoss {
  id: string; code: string; name: string; title: string; region: string; place: string
  gender: string; age: number; workout_label: string; difficulty_stars: number
  quote?: string; skill_name?: string; skill_desc?: string; dialogue_intro?: string; dialogue_start?: string
  scene_image_url: string; card_image_url?: string; master_image_url?: string
  lat: number; lng: number; radius_m: number
  reward_exp: number; reward_dp: number; retry_dp_cost: number
  // 打卡（每次成功打卡皆可能觸發，含重複打卡）DP/GP 隨機發放區間；完成挑戰依機率額外發放的 GP 區間，
  // 皆後台每點可設、預設 0（不發）——見 migration 098。
  checkin_reward_dp_min?: number; checkin_reward_dp_max?: number
  checkin_reward_gp_min?: number; checkin_reward_gp_max?: number
  complete_reward_gp_min?: number; complete_reward_gp_max?: number; complete_reward_gp_chance?: number
  workout_kind: string; segments?: WorkoutSegment[] | null; data_source: string
  display_order: number; enabled: boolean
  access_note: string
  checkin_only?: boolean // 純打卡點：無關主內容，其餘關主欄位留空
  // 玩家進度（前台列表）
  stars?: number; card_obtained?: boolean; active?: boolean; attempts?: number; best_time_s?: number
  discovered?: boolean // 已打卡揭露關主（未揭露則 name/scene/難度等欄位被伺服器遮蔽）
}

export const adminExploreApi = {
  list: (token: string) => request<{ bosses: ExploreBoss[] }>('/admin/explore', { headers: withAuth(token) }),
  save: (token: string, boss: Partial<ExploreBoss>) =>
    request<{ id: string }>('/admin/explore', { method: 'POST', headers: withAuth(token), body: JSON.stringify(boss) }),
  del: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/explore/${id}/delete`, { method: 'POST', headers: withAuth(token) }),
}

// 卡片圖鑑輕量卡片（GET /explore/gallery）：只含 CardGalleryScreen 實際用到的欄位。已排除純打卡點；
// 未取得卡 card_image_url 一律為空字串（前端本就只在 card_obtained 時渲染圖片）。
export interface ExploreGalleryCard {
  id: string
  name: string
  place: string
  difficulty_stars: number
  card_image_url: string
  card_obtained: boolean
}

// 城市探索（前台）：啟用中的關主 + 我的進度 + 今日打卡剩餘次數（跨所有點加總）
export const exploreApi = {
  list: (token: string) => request<{ bosses: ExploreBoss[]; checkin_daily_cap: number; checkin_daily_remaining: number }>('/explore', { headers: withAuth(token) }),
  // 卡片圖鑑專用輕量端點：只回圖鑑用到的 7 欄位、已排除純打卡點，取代原本打 /explore 全量列表（1.2MB→數十KB）。
  gallery: (token: string) => request<{ bosses: ExploreGalleryCard[] }>('/explore/gallery', { headers: withAuth(token) }),
  // 單一關主完整 detail（含 List 拿掉的重欄位：segments/對話/金句/技能/card_image_url/master_image_url）。
  // 點開挑戰面板（非剛打卡揭露、Checkin 回應已內含完整資料的情況）時呼叫，見 track/page.tsx openBossPanel。
  detail: (token: string, id: string) => request<{ boss: ExploreBoss }>(`/explore/${id}`, { headers: withAuth(token) }),
  // 到打卡點打卡（可重複，同點 24h 冷卻、每日全站上限一般3/VIP5次）→ 通過才隨機發 DP/GP。
  // 揭露關主：一般點(checkin_only=false)回完整關主資料；純打卡點(checkin_only=true)不揭露、只回地點。
  // already=true 代表此點先前已揭露過（前端不應再自動彈出挑戰面板）；can_challenge 供「打卡/挑戰」二選一 UI；
  // 冷卻中/達每日上限時 ok=false、不發獎，訊息與剩餘秒數/次數見 message/cooldown_remaining_s/daily_remaining。
  checkin: (token: string, id: string, body: { lat: number; lng: number; acc: number }) =>
    request<{
      ok: boolean; status: string; distance_m?: number; message?: string; boss?: ExploreBoss
      checkin_only?: boolean; place?: string; already?: boolean; active?: boolean; can_challenge?: boolean
      dp_awarded?: number; gp_awarded?: number; daily_remaining?: number; cooldown_remaining_s?: number
    }>(`/explore/${id}/checkin`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  // 接受挑戰（扣 DP=難度×10）→ 帶到課表挑戰
  accept: (token: string, id: string) =>
    request<{ ok: boolean; tier: number; charged_dp: number }>(`/explore/${id}/accept`, { method: 'POST', headers: withAuth(token) }),
  // 完成挑戰（由 /track 分段引擎回報）→ 得星、3★ 取得卡片、回傳本趟完成時間(秒)；bonus_gp=依機率額外發放的 GP
  complete: (token: string, id: string, body: { finished: boolean; work_in_band: number; work_total: number }) =>
    request<{ completed: boolean; stars: number; card_obtained: boolean; reward_exp: number; reward_dp: number; bonus_gp?: number; time_s: number }>(
      `/explore/${id}/complete`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  // 挑戰者時間榜（最短完成時間，前 100）+ 我是否追蹤 + 我的名次
  ranking: (token: string, id: string) =>
    request<{ ranking: ExploreRankRow[]; my_rank: number }>(`/explore/${id}/ranking`, { headers: withAuth(token) }),
}

// 城市探索：某關主的挑戰者成績排行列
export interface ExploreRankRow {
  rank: number
  user_id: string
  nickname: string
  title: string // 目前展示中的稱號（無則空字串）
  avatar_url: string
  stars: number
  best_time_s: number | null // 最短一次完成挑戰的秒數（時間榜排序值）；室內跑等無 GPS 時間者為 null
  completed_at?: string
  is_following: boolean
  is_me: boolean
}

export const adminLevelsApi = {
  levelConfig: (token: string) =>
    request<{ levels: LevelConfig[] }>('/admin/membership/level-config', { headers: withAuth(token) }),
  setLevelConfig: (token: string, levels: LevelConfig[]) =>
    request<{ levels: LevelConfig[] }>('/admin/membership/level-config', {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify({ levels }),
    }),
  expRules: (token: string) =>
    request<{ exp_rules: ExpRules }>('/admin/membership/exp-rules', { headers: withAuth(token) }),
  setExpRules: (token: string, body: ExpRules) =>
    request<{ exp_rules: ExpRules }>('/admin/membership/exp-rules', {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  athleteConfig: (token: string) =>
    request<{ metrics: AthleteMetricConfig[]; levels: AthleteLevel[] }>('/admin/membership/athlete-config', { headers: withAuth(token) }),
  setAthleteConfig: (token: string, body: { metrics: AthleteMetricConfig[]; levels: AthleteLevel[] }) =>
    request<{ metrics: AthleteMetricConfig[]; levels: AthleteLevel[] }>('/admin/membership/athlete-config', {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
}

// --- 稱號管理（title_defs；9 個固定 category，checkAndAwardTitles 依此計算解鎖） ---
export type TitleCategory =
  | 'single_dist' | 'cum_dist' | 'cum_time' | 'checkin' | 'boss' | 'personal' | 'level' | 'card' | 'streak'

export interface AdminTitle {
  code: string
  category: TitleCategory
  threshold: number
  unit: string
  name: string
  tier: number // 1-6
  sort_order: number
  enabled: boolean
  earned_count: number // 已有多少玩家取得此稱號
}
export interface TitleCategoryMeta { key: string; label: string }

export const adminTitlesApi = {
  list: (token: string) =>
    request<{ titles: AdminTitle[]; categories: TitleCategoryMeta[] }>('/admin/titles', { headers: withAuth(token) }),
  create: (token: string, body: Omit<AdminTitle, 'earned_count'>) =>
    request<{ title: AdminTitle }>('/admin/titles', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, code: string, body: Omit<AdminTitle, 'code' | 'earned_count'>) =>
    request<{ title: AdminTitle }>(`/admin/titles/${encodeURIComponent(code)}`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  remove: (token: string, code: string) =>
    request<{ deleted: boolean; revoked_from: number }>(`/admin/titles/${encodeURIComponent(code)}`, {
      method: 'DELETE', headers: withAuth(token),
    }),
}

// --- 自主訓練後台管理（workout_templates 課表庫 + pace_levels 配速等級表）---
// segments/paces 為任意形狀 jsonb，後台以 JSON textarea 直接編輯字串再 JSON.parse，故型別留 unknown。

export interface AdminWorkoutTemplate {
  code: string
  name: string
  category: string
  description: string
  segments: unknown // 陣列；每段 {kind,label,effort?,target_type,target,reps?,rest_s?}
  sort_order: number
  enabled: boolean
  library_visible: boolean
  adjust_type: 'distance' | 'reps' | 'pyramid' | 'none'
}

export interface AdminPaceLevel {
  id: number
  label: string
  paces: unknown // {easy,marathon,threshold,interval,rep} 各 {fast,slow}（秒/公里）
  enabled: boolean
}

export const adminTrainingApi = {
  data: (token: string) =>
    request<{ templates: AdminWorkoutTemplate[]; pace_levels: AdminPaceLevel[] }>('/admin/training/data', {
      headers: withAuth(token),
    }),
  createTemplate: (token: string, body: AdminWorkoutTemplate) =>
    request<{ template: AdminWorkoutTemplate }>('/admin/training/templates', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
  updateTemplate: (token: string, code: string, body: Omit<AdminWorkoutTemplate, 'code'>) =>
    request<{ template: AdminWorkoutTemplate }>(`/admin/training/templates/${encodeURIComponent(code)}`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  deleteTemplate: (token: string, code: string) =>
    request<{ deleted: boolean }>(`/admin/training/templates/${encodeURIComponent(code)}`, {
      method: 'DELETE', headers: withAuth(token),
    }),
  createPaceLevel: (token: string, body: AdminPaceLevel) =>
    request<{ pace_level: AdminPaceLevel }>('/admin/training/pace-levels', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
  updatePaceLevel: (token: string, id: number, body: Omit<AdminPaceLevel, 'id'>) =>
    request<{ pace_level: AdminPaceLevel }>(`/admin/training/pace-levels/${id}`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  deletePaceLevel: (token: string, id: number) =>
    request<{ deleted: boolean }>(`/admin/training/pace-levels/${id}`, {
      method: 'DELETE', headers: withAuth(token),
    }),
}

// --- 虛擬選手（virtual_runners；is_virtual=true 的人頭帳號，無 user_identities 天然無法登入，
// 用來補賽事熱度/陪跑）。migration 146：users.is_virtual + vr_level_presets(8 級能力模板) + virtual_runners。
// 能力值（avg_km 單次km/monthly_km 月里程/pace_fast_s-pace_slow_s 配速秒每公里）建立時由 preset 帶入±5%抖動；
// PUT 更新若換 level 且未明給能力值 → 後端重新從新 preset 帶入抖動，故編輯表單用 overrideAbility 開關控制
// 是否要把能力值欄位一併送出（見 virtual-runners/page.tsx RForm）。

export type VirtualCity = 'taipei' | 'new_taipei' | 'taoyuan' | 'hsinchu' | 'taichung' | 'tainan' | 'kaohsiung'
export type VirtualLevel =
  | 'beginner' | 'citizen' | 'advanced' | 'half_challenger' | 'half_finisher' | 'full_challenger' | 'full_finisher' | 'elite'

export interface VirtualRunnerLevelPreset {
  level: VirtualLevel
  label: string
  sort_order: number
  avg_km: number
  monthly_km: number
  pace_fast_s: number
  pace_slow_s: number
}

export interface VirtualRunner {
  user_id: string
  name: string
  avatar_url: string // 空＝無頭像（顯示字首圓）
  gender: 'male' | 'female'
  city: VirtualCity
  level: VirtualLevel
  diligence: number // 1-5，預設 3
  window_hour: number // 4/5/6/19/20/21/22
  avg_km: number
  monthly_km: number
  pace_fast_s: number
  pace_slow_s: number
  enabled: boolean
  last_generated_at: string | null
  race_count: number
}

export interface VirtualRunnerCreatePayload {
  name?: string // 空 → 後端從姓名池自動取名
  gender: 'male' | 'female'
  city: VirtualCity
  level: VirtualLevel
  diligence: number
  window_hour: number
}

export interface VirtualRunnerBatchPayload {
  count: number // 1-200
  level?: VirtualLevel // 空 → 逐位隨機
  city?: VirtualCity
  gender?: 'male' | 'female'
}

export interface VirtualRunnerUpdatePayload {
  name?: string // 改名（省略＝不改）
  avatar_url?: string // 頭像站內 URL；''＝清除；省略＝不改
  gender?: 'male' | 'female'
  city?: VirtualCity
  level?: VirtualLevel
  diligence?: number
  window_hour?: number
  avg_km?: number
  monthly_km?: number
  pace_fast_s?: number
  pace_slow_s?: number
  enabled?: boolean
}

export interface VirtualRunnerRaceAssignedRow {
  user_id: string
  name: string
  gender: 'male' | 'female'
  level: VirtualLevel
  group_id: string
  group_name: string
  reg_status: string
}

export interface VirtualRunnerRaceGroupRow {
  id: string
  name: string
  slot_limit: number | null
  slots_taken: number
}

export type VirtualRunnerAssignSkipReason = 'duplicate' | 'group_full' | 'disabled' | 'not_found'
export interface VirtualRunnerAssignSkip {
  user_id: string
  reason: VirtualRunnerAssignSkipReason | string
}

export const adminVirtualRunnersApi = {
  list: (token: string) =>
    request<{ runners: VirtualRunner[]; presets: VirtualRunnerLevelPreset[] }>('/admin/virtual-runners', {
      headers: withAuth(token),
    }),
  create: (token: string, body: VirtualRunnerCreatePayload) =>
    request<{ runner: VirtualRunner }>('/admin/virtual-runners', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
  batchCreate: (token: string, body: VirtualRunnerBatchPayload) =>
    request<{ created: number }>('/admin/virtual-runners/batch', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
  regenerateNames: (token: string) =>
    request<{ renamed: number }>('/admin/virtual-runners/regenerate-names', {
      method: 'POST', headers: withAuth(token),
    }),
  // 分批同步（一批預設 20）：全量一次會超過閘道逾時，前端以 offset 逐批串完
  syncTitles: (token: string, offset: number, limit = 20) =>
    request<{ synced: number; changed: number; total: number; done: boolean }>(
      `/admin/virtual-runners/sync-titles?offset=${offset}&limit=${limit}`, {
      method: 'POST', headers: withAuth(token),
    }),
  // 管理員手動重抽：無視「每 N 趟」規則，對 run 數 ≥ min_runs 的啟用選手立即從已解鎖稱號隨機重抽展示稱號。
  // 同樣分批（一批預設 20，後端上限 50）避免全量單發超過閘道逾時；以回傳的 next_offset 串下一批（null＝完成）。
  rerollTitles: (token: string, minRuns: number, offset: number, limit = 20) =>
    request<{ rerolled: number; skipped: number; total: number; next_offset: number | null }>(
      `/admin/virtual-runners/reroll-titles?min_runs=${minRuns}&offset=${offset}&limit=${limit}`, {
      method: 'POST', headers: withAuth(token),
    }),
  update: (token: string, userID: string, body: VirtualRunnerUpdatePayload) =>
    request<{ runner: VirtualRunner }>(`/admin/virtual-runners/${userID}`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  remove: (token: string, userID: string) =>
    request<{ ok: boolean }>(`/admin/virtual-runners/${userID}`, {
      method: 'DELETE', headers: withAuth(token),
    }),
  updatePreset: (token: string, level: VirtualLevel, body: { avg_km: number; monthly_km: number; pace_fast_s: number; pace_slow_s: number }) =>
    request<{ preset: VirtualRunnerLevelPreset }>(`/admin/virtual-runners/presets/${level}`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  race: (token: string, raceID: string) =>
    request<{ assigned: VirtualRunnerRaceAssignedRow[]; groups: VirtualRunnerRaceGroupRow[]; candidates_count: number }>(
      `/admin/virtual-runners/race/${raceID}`,
      { headers: withAuth(token) },
    ),
  assign: (token: string, raceID: string, body: { user_ids?: string[]; random_count?: number; group_id?: string }) =>
    request<{ added: number; skipped: VirtualRunnerAssignSkip[] }>(`/admin/virtual-runners/race/${raceID}/assign`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
  unassign: (token: string, raceID: string, userID: string) =>
    request<{ ok: boolean }>(`/admin/virtual-runners/race/${raceID}/${userID}`, {
      method: 'DELETE', headers: withAuth(token),
    }),
}

// --- 金流（綠界 ECPay）---

export interface EcpayCheckout {
  action_url: string
  params: Record<string, string>
}

export const paymentsApi = {
  // 取得綠界結帳表單參數（前端據此 POST 表單導去綠界）。
  // 帶自身 origin → 付款後回到「原本所在網域」（支援 www.dor.tw / dor.hero-mi.com 雙網域）。
  ecpayCheckout: (token: string, orderID: string) =>
    request<EcpayCheckout>('/payments/ecpay/checkout', {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify({
        order_id: orderID,
        client_back_url: typeof window !== 'undefined' ? window.location.origin : '',
      }),
    }),
}

// --- Admin: 會員管理 ---

// 註冊來源歸因分類（migration 147_signup_attribution，見後端 classify 純函式）
export type SignupSource = 'referral' | 'facebook' | 'instagram' | 'line' | 'google' | 'threads' | 'tiktok' | 'x' | 'youtube' | 'dcard' | 'ptt' | 'other' | 'direct'

export interface MemberSummary {
  id: string
  email: string
  handle: string
  name: string
  role: string
  real_name: string
  phone: string
  gender: string
  total_km: number
  can_create_team_group: boolean
  created_at: string
  is_vip: boolean
  vip_expires_at?: string
  vip_plan: string
  last_login_at?: string
  signup_source?: SignupSource | null // 歷史會員（migration 147 上線前註冊）無資料 → null/undefined
  signup_ref_name?: string | null // 推薦人暱稱，僅 source=referral 有值
  signup_utm_source: string // utm_source 原值（未經正規化）；後端不 omitempty，無則空字串
  is_virtual: boolean // 虛擬選手（users.is_virtual，見 migrations/146_virtual_runner.sql）
}

// 完整歸因資料（會員詳情頁顯示用）
export interface SignupAttribution {
  source: SignupSource
  ref_name?: string | null // 推薦人暱稱，僅 source=referral 有值
  utm?: { source?: string; medium?: string; campaign?: string } | null
  landing_url?: string | null
  referrer_url?: string | null
  created_at?: string
}

export interface MemberDetail extends MemberSummary {
  nickname: string
  address: string
  birthday: string
  race_count: number
  exp: number
  gp: number // GP 幣餘額（環台大富翁）
  level: number
  level_title: string
  athlete: AthleteStats
  attribution?: SignupAttribution | null // 歷史會員無資料 → null/undefined
}

// --- 後台管理者帳號 + 權限 ---
export interface AdminScope { key: string; label: string }
export interface AdminAccount {
  id: string
  login: string
  name: string
  is_super: boolean
  permissions: string[]
  created_at: string
}
export interface AdminMe { admin: AdminAccount; scopes: AdminScope[] }

export const adminMeApi = {
  get: (token: string) => request<AdminMe>('/admin/me', { headers: withAuth(token) }),
}
export interface AuditLog {
  id: string
  actor_id: string
  actor_login: string
  actor_name: string
  method: string
  path: string
  resource: string
  action: string
  status: number
  ip: string
  created_at: string
}
export const auditApi = {
  list: (token: string, params?: { limit?: number; offset?: number; resource?: string }) => {
    const qs = new URLSearchParams()
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    if (params?.resource) qs.set('resource', params.resource)
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ logs: AuditLog[]; count: number }>(`/admin/audit${suffix}`, { headers: withAuth(token) })
  },
}

// --- Admin: 用戶登入紀錄（user_login_logs；與 auditApi 的後台操作紀錄分開） ---
export interface LoginLog {
  created_at: string
  user_id: string
  email: string
  method: string // password | google | register
  ip: string
}
export const adminLoginLogsApi = {
  list: (token: string, params?: { q?: string; limit?: number; offset?: number }) => {
    const qs = new URLSearchParams()
    if (params?.q) qs.set('q', params.q)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ logs: LoginLog[]; count: number }>(`/admin/login-logs${suffix}`, { headers: withAuth(token) })
  },
}

export const adminAccountsApi = {
  list: (token: string) => request<{ admins: AdminAccount[] }>('/admin/admins', { headers: withAuth(token) }),
  create: (token: string, body: { login: string; password: string; name: string; is_super: boolean; permissions: string[] }) =>
    request<{ admin: AdminAccount }>('/admin/admins', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: { name?: string; password?: string; is_super: boolean; permissions: string[] }) =>
    request<{ admin: AdminAccount }>(`/admin/admins/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) =>
    request<void>(`/admin/admins/${id}`, { method: 'DELETE', headers: withAuth(token) }),
}

export const adminMembersApi = {
  list: (token: string, params?: { q?: string; limit?: number; offset?: number; source?: SignupSource; hideVirtual?: boolean }) => {
    const qs = new URLSearchParams()
    if (params?.q) qs.set('q', params.q)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    if (params?.source) qs.set('source', params.source)
    if (params?.hideVirtual) qs.set('hide_virtual', '1')
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ members: MemberSummary[]; count: number }>(`/admin/members${suffix}`, {
      headers: withAuth(token),
    })
  },
  get: (token: string, id: string) =>
    request<{ member: MemberDetail }>(`/admin/members/${id}`, { headers: withAuth(token) }),
  setTeamGroupPermission: (token: string, id: string, allowed: boolean) =>
    request<{ can_create_team_group: boolean }>(`/admin/members/${id}/team-group-permission`, {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify({ allowed }),
    }),
  setVip: (token: string, id: string, vipExpiresAt: string) =>
    request<{ vip_expires_at: string }>(`/admin/members/${id}/vip`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify({ vip_expires_at: vipExpiresAt }),
    }),
  setExp: (token: string, id: string, body: { set?: number; delta?: number }) =>
    request<{ exp: number }>(`/admin/members/${id}/exp`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  setGp: (token: string, id: string, body: { set?: number; delta?: number }) =>
    request<{ gp: number }>(`/admin/members/${id}/gp`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  // 模擬加里程（測試用）：推一筆活動 → worker 寫入並發日常里程 EXP
  addMileage: (token: string, userID: string, distanceKm: number) =>
    request<void>('/admin/activities/add-mileage', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ user_id: userID, distance_km: distanceKm }),
    }),
}

// 推廣連結頁「成效統計」：各通路近 12 週註冊數趨勢 + 彙總（見後端 internal/profile/signup_stats.go）
export interface SignupStatsWeek {
  week_start: string // 台灣時區週一起算的當週起始日 YYYY-MM-DD
  counts: Record<string, number> // key=SignupSource，只含當週有註冊的來源
}
export interface SignupStatsTotal {
  source: SignupSource
  utm_source: string // 僅 source='other' 時可能有值（原始 utm_source）；其餘固定空字串
  c7: number
  c30: number
  total: number
}
export interface SignupStats {
  weekly: SignupStatsWeek[]
  totals: SignupStatsTotal[]
}
export const adminSignupStatsApi = {
  get: (token: string) => request<SignupStats>('/admin/signup-stats', { headers: withAuth(token) }),
}

export type TaskModuleInput = { name: string; description?: string; items: TaskModuleItem[] }

export const adminTaskModulesApi = {
  list: (token: string) =>
    request<{ modules: TaskModule[]; metrics: MetricSpec[] }>('/admin/task-modules', { headers: withAuth(token) }),
  get: (token: string, id: string) =>
    request<{ module: TaskModule }>(`/admin/task-modules/${id}`, { headers: withAuth(token) }),
  create: (token: string, body: TaskModuleInput) =>
    request<{ module: TaskModule }>('/admin/task-modules', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
  update: (token: string, id: string, body: TaskModuleInput) =>
    request<{ module: TaskModule }>(`/admin/task-modules/${id}`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  remove: (token: string, id: string) =>
    request<null>(`/admin/task-modules/${id}`, { method: 'DELETE', headers: withAuth(token) }),
}

// --- Admin: 報名管理 / 訂單管理 ---

export interface SignupRow {
  id: string
  user_name: string
  user_email: string
  group_id?: string
  group_name: string
  status: string
  group_revealed: boolean
  snap_real_name: string
  snap_phone: string
  created_at: string
  order_id?: string
  order_total_cents: number
  order_status?: string
  race_title?: string // 僅「全部賽事」模式（race_id 留空）有值，後端多回傳供前端顯示賽事名稱欄
  is_virtual: boolean // 虛擬選手（users.is_virtual），供🤖標記
  // 寵物雲端馬拉松：僅寵物賽事報名有值。
  pets?: RegistrationPet[]
}

export interface OrderItemRow {
  item_type: string
  addon_name?: string
  qty: number
  unit_price_cents: number
  subtotal_cents: number
}

export interface OrderRow {
  id: string
  user_name: string
  user_email: string
  race_title: string
  total_cents: number
  status: string
  payment_ref?: string
  paid_at?: string | null
  created_at: string
  registration_id?: string
  invoice: InvoiceInfo | null // 發票資訊（過渡期人工開立用）；舊訂單沒有資料則為 null
  is_virtual: boolean // 虛擬選手（users.is_virtual），供🤖標記
  // 電子發票實際開立狀態（見 adminInvoiceApi）；與上面 invoice（買受人快照）不同，這是綠界開立結果摘要
  invoice_number?: string
  invoice_status?: string // pending|issuing|issued|void|failed|skipped（無資料列＝undefined）
  // 2026-09-08 owner request：後台訂單管理需看得到報名者真實個資，LEFT JOIN registrations/user_profiles
  // 取得（VIP 訂閱訂單無 registration，以下皆為零值/空字串）
  real_name: string // 真實姓名：優先報名快照，快照空退回會員個資
  phone: string
  address: string
  distance_km: number // 報名組別對應距離（km）；VIP 訂單為 0
  faction?: string // 陣營（分組對抗模式）
  group_name?: string // 報名分組名稱
  user_handle: string // 會員帳號 handle
  // 寵物雲端馬拉松：僅寵物賽事訂單有值（後台訂單明細顯示用）。
  pets?: RegistrationPet[]
}

export interface OrderDetail extends OrderRow {
  items: OrderItemRow[]
}

// --- 匯出賽事訂單（含加購）：GET /admin/orders/export，見 services/api/internal/race ExportOrderRow ---

export interface ExportOrderItem {
  name: string
  qty: number
  unit_price_cents: number
  subtotal_cents: number
}

export interface ExportOrderRow {
  id: string
  created_at: string
  paid_at?: string | null
  status: string
  user_email: string
  user_handle: string
  user_name: string
  real_name: string
  phone: string
  address: string
  distance_km: number
  faction: string
  group_name: string
  entry_cents: number
  addons: ExportOrderItem[]
  addon_cents: number
  discount_cents: number // 負數＝有折抵，0＝無
  total_cents: number
  invoice_number?: string
  invoice_status?: string
  buyer_type?: string
  tax_id?: string
  title?: string // 三聯式發票抬頭
  carrier_id?: string
  love_code?: string
  // 寵物雲端馬拉松：與 registration/order DTO 同形狀的完整寵物清單（僅寵物賽事有值）。
  pets?: RegistrationPet[]
  // 後端已用「；」join 好的名稱清單，訂單 sheet 「寵物」欄直接顯示用（僅寵物賽事有值，一般賽事省略/空字串）。
  pets_text?: string
}

// 寵物明細（訂單匯出第三張 sheet）：每隻寵物一列，含晶片號碼；訂單 sheet 的「寵物」欄只顯示 pets_text 名稱清單。
export interface ExportOrdersResponse {
  race: { id: string; title: string }
  orders: ExportOrderRow[] // 寵物在 orders[].pets／pets_text，後端不另給頂層陣列
}

export interface RefundRow {
  id: string
  transaction_id: string
  order_id: string
  amount_cents: number
  status: string // pending|success|failed|manual_required|manual_done
  method: string // api|manual
  reason?: string
  operator_admin_id?: string
  ecpay_rtn_code?: string
  ecpay_rtn_msg?: string
  created_at: string
  updated_at: string
}

export const adminSignupsApi = {
  // race_id 選填：留空＝跨賽事「全部賽事」模式（後端依報名時間 DESC，僅取最新 200 筆）
  // statuses 選填：報名狀態過濾清單（如 ['paid','pending']），空／未帶＝不過濾（後端白名單驗證非法值忽略）
  list: (token: string, params?: { race_id?: string; q?: string; hideVirtual?: boolean; statuses?: string[] }) => {
    const qs = new URLSearchParams()
    if (params?.race_id) qs.set('race_id', params.race_id)
    if (params?.q) qs.set('q', params.q)
    if (params?.hideVirtual) qs.set('hide_virtual', '1')
    if (params?.statuses && params.statuses.length > 0) qs.set('statuses', params.statuses.join(','))
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ signups: SignupRow[]; count: number; groups: RaceGroup[] }>(`/admin/signups${suffix}`, {
      headers: withAuth(token),
    })
  },
  markPaid: (token: string, regID: string) =>
    request<void>(`/admin/signups/${regID}/pay`, { method: 'PATCH', headers: withAuth(token) }),
  changeGroup: (token: string, regID: string, groupID: string) =>
    request<void>(`/admin/signups/${regID}/group`, { method: 'PATCH', headers: withAuth(token), body: JSON.stringify({ group_id: groupID }) }),
}

export interface PendingCheckin {
  id: string
  user_name: string
  user_email: string
  checkpoint_id: string
  checkpoint_name: string
  task_title: string
  lat: number
  lng: number
  cp_lat: number
  cp_lng: number
  radius_m: number
  accuracy: number
  distance_m: number
  flag_reason: string
  checked_at: string
}

export const adminCheckinReviewApi = {
  list: (token: string, raceID: string) =>
    request<{ checkins: PendingCheckin[]; count: number }>(`/admin/checkin-review?race_id=${encodeURIComponent(raceID)}`, { headers: withAuth(token) }),
  approve: (token: string, checkinID: string) =>
    request<void>(`/admin/checkin-review/${checkinID}/approve`, { method: 'PATCH', headers: withAuth(token) }),
  reject: (token: string, checkinID: string) =>
    request<void>(`/admin/checkin-review/${checkinID}/reject`, { method: 'PATCH', headers: withAuth(token) }),
}

export const adminOrdersApi = {
  list: (token: string, params?: { race_id?: string; status?: string; limit?: number; offset?: number; hideVirtual?: boolean }) => {
    const qs = new URLSearchParams()
    if (params?.race_id) qs.set('race_id', params.race_id)
    if (params?.status) qs.set('status', params.status)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    if (params?.hideVirtual) qs.set('hide_virtual', '1')
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ orders: OrderRow[]; count: number }>(`/admin/orders${suffix}`, { headers: withAuth(token) })
  },
  get: (token: string, id: string) =>
    request<{ order: OrderDetail }>(`/admin/orders/${id}`, { headers: withAuth(token) }),
  markPaid: (token: string, id: string, payment_ref?: string) =>
    request<void>(`/admin/orders/${id}/pay`, {
      method: 'PATCH',
      headers: withAuth(token),
      body: JSON.stringify({ payment_ref: payment_ref ?? '' }),
    }),
  // race_id 必填；status 預設 all（不篩）；hideVirtual 預設 true（後端亦預設 true，帶 false 才會顯式關閉）。
  // 2026-09-08 第二次稽核修法：後端改成 POST + JSON body（原 GET + query string 不會進後端的操作
  // 稽核記錄，匯出訂單這種含個資/金流資料的動作要留稽核軌跡，比照其他會異動/匯出敏感資料的端點
  // 一律改用有 body 的方法）；回應格式不變。
  // status 合法值：'all'|'paid'|'pending'|'cancelled'|'refunded'（比照 list() 的 status 用寬鬆 string，
  // 呼叫端目前用同一顆 <select> 驅動 list 與 export，型別一致才不必為了這裡另外轉型）。
  export: (token: string, params: { race_id: string; status?: string; hideVirtual?: boolean }) =>
    request<ExportOrdersResponse>('/admin/orders/export', {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify({
        race_id: params.race_id,
        ...(params.status ? { status: params.status } : {}),
        ...(params.hideVirtual === false ? { hide_virtual: false } : {}),
      }),
    }),
}

// --- Admin: 電子發票（見 services/api/internal/einvoice，migration 169）---

export interface EInvoiceDetail {
  order_id: string
  buyer_type: string
  tax_id?: string
  title?: string
  carrier_type?: string
  carrier_id?: string
  love_code?: string
  invoice_status: string // pending|issuing|issued|void|failed|skipped
  invoice_number?: string
  invoice_date?: string
  random_number?: string
  relate_number?: string
  ecpay_env?: string // stage|prod
  sales_amount_ntd: number
  attempts: number
  last_error?: string
  skip_reason?: string
  issued_at?: string | null
  voided_at?: string | null
  void_reason?: string
  remain_allowance_ntd?: number | null
}

export interface EInvoiceAllowance {
  id: string
  refund_id?: string | null
  amount_ntd: number
  reason?: string
  status: string // pending|success|failed
  allowance_no?: string
  allowance_date?: string | null
  last_error?: string
  created_at: string
}

export interface AdminInvoiceRow {
  order_id: string
  user_name: string
  amount_ntd: number
  invoice_status: string
  invoice_number?: string
  invoice_date?: string
  last_error?: string
  skip_reason?: string
  updated_at: string
  buyer_type: string
}

export const adminInvoiceApi = {
  get: (token: string, orderID: string) =>
    request<{ invoice: EInvoiceDetail | null; allowances: EInvoiceAllowance[] }>(`/admin/orders/${orderID}/invoice`, {
      headers: withAuth(token),
    }),
  issue: (token: string, orderID: string) =>
    request<{ ok: boolean; invoice: EInvoiceDetail }>(`/admin/orders/${orderID}/invoice/issue`, {
      method: 'POST', headers: withAuth(token),
    }),
  void: (token: string, orderID: string, reason: string) =>
    request<{ ok: boolean; invoice: EInvoiceDetail }>(`/admin/orders/${orderID}/invoice/void`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ reason }),
    }),
  sync: (token: string, orderID: string) =>
    request<{ ok: boolean; invoice: EInvoiceDetail }>(`/admin/orders/${orderID}/invoice/sync`, {
      method: 'POST', headers: withAuth(token),
    }),
  allowance: (token: string, orderID: string, amount_ntd: number, reason: string) =>
    request<{ ok: boolean; allowance: EInvoiceAllowance }>(`/admin/orders/${orderID}/invoice/allowance`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ amount_ntd, reason }),
    }),
  list: (token: string, params?: { status?: string; limit?: number }) => {
    const qs = new URLSearchParams()
    if (params?.status) qs.set('status', params.status)
    if (params?.limit) qs.set('limit', String(params.limit))
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ invoices: AdminInvoiceRow[] }>(`/admin/invoices${suffix}`, { headers: withAuth(token) })
  },
  // 對這筆訂單目前存的手機條碼載具／愛心碼重新查驗一次（見 einvoice/verify.go VerifyCarrier）
  verifyCarrier: (token: string, orderID: string) =>
    request<InvoiceVerifyResult>(`/admin/orders/${orderID}/invoice/verify-carrier`, {
      method: 'POST', headers: withAuth(token),
    }),
}

// --- 取消報名審核（後台） ---

export interface AdminCancelRequest {
  id: string
  registration_id: string
  order_id?: string
  user_id: string
  user_name?: string
  user_email?: string
  race_title?: string
  status: string // pending|processing|approved|rejected
  reason: string
  days_before_race: number
  refund_ratio: number
  refund_amount_cents: number
  order_total_cents: number
  reviewed_by?: string
  reviewed_at?: string
  review_note?: string
  refund_id?: string
  created_at: string
}

export interface CancelApproveResult {
  order_status: string // refunded|cancelled|""
  refund_id?: string
  refund_note?: string
}

export const adminCancelRequestsApi = {
  list: (token: string, status?: string) => {
    const qs = status ? `?status=${encodeURIComponent(status)}` : ''
    return request<{ cancel_requests: AdminCancelRequest[]; count: number }>(`/admin/cancel-requests${qs}`, {
      headers: withAuth(token),
    })
  },
  approve: (token: string, id: string) =>
    request<CancelApproveResult>(`/admin/cancel-requests/${id}/approve`, { method: 'PATCH', headers: withAuth(token) }),
  reject: (token: string, id: string, note: string) =>
    request<void>(`/admin/cancel-requests/${id}/reject`, {
      method: 'PATCH',
      headers: withAuth(token),
      body: JSON.stringify({ note }),
    }),
}

export interface EcpayEnvCheck {
  global_ecpay_env: string
  prod_origins: string[]
  received_origin: string
  resolve_ok: boolean
  // 除錯參考用，已不再用於決定要用哪組特店（前台是 Next.js 伺服器端代理，這兩個 header 反映不出
  // 瀏覽器真實網域）——實際解析一律以 received_origin/resolve_ok 為準。
  legacy_host_headers: { host: string; x_forwarded_host: string }
  resolved_env: string
  resolved_merchant_id: string
  resolved_action_url: string
  would_charge_real_money: boolean
  prod_credentials_configured: { merchant_id: boolean; hash_key: boolean; hash_iv: boolean }
}

export const adminPaymentsApi = {
  // 帶自身 origin → 與結帳（paymentsApi.ecpayCheckout 的 client_back_url）用同一個值，
  // 診斷結果才能反映「這個網域真的結帳會發生什麼事」。
  envCheck: (token: string, origin: string) =>
    request<EcpayEnvCheck>(`/admin/payments/env-check?origin=${encodeURIComponent(origin)}`, { headers: withAuth(token) }),
  listRefunds: (token: string, orderID: string) =>
    request<{ refunds: RefundRow[]; count: number }>(`/admin/payments/refunds?order_id=${encodeURIComponent(orderID)}`, {
      headers: withAuth(token),
    }),
  createRefund: (token: string, params: { order_id: string; amount_cents?: number; reason: string }) =>
    request<{ refund_id: string; status: string; method?: string; note?: string; message?: string }>(`/admin/payments/refunds`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(params),
    }),
  markRefundManualDone: (token: string, refundID: string) =>
    request<void>(`/admin/payments/refunds/${refundID}/manual-done`, {
      method: 'PATCH',
      headers: withAuth(token),
    }),
}

// --- Admin: 優惠序號 ---

export interface PromoCode {
  id: string
  code: string
  discount_type: 'amount' | 'percent'
  discount_value: number
  max_uses?: number | null
  used_count: number
  per_user_once: boolean
  race_id?: string | null
  target_user_id?: string | null
  valid_from?: string | null
  valid_until?: string | null
  batch_id?: string | null
  note?: string
  active: boolean
  created_at: string
  target_email?: string
}

export interface PromoUsage {
  id: string
  user_name: string
  user_email: string
  race_title: string
  discount_cents: number
  used_at: string
}

export interface PromoCreateInput {
  code?: string
  discount_type: 'amount' | 'percent'
  discount_value: number
  max_uses?: number | null
  per_user_once: boolean
  race_id?: string | null
  target_email?: string
  valid_from?: string | null
  valid_until?: string | null
  note?: string
  quantity: number
}

export const adminPromoApi = {
  list: (token: string, params?: { race_id?: string; q?: string }) => {
    const qs = new URLSearchParams()
    if (params?.race_id) qs.set('race_id', params.race_id)
    if (params?.q) qs.set('q', params.q)
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ codes: PromoCode[]; count: number }>(`/admin/promo-codes${suffix}`, { headers: withAuth(token) })
  },
  create: (token: string, body: PromoCreateInput) =>
    request<{ codes: PromoCode[]; count: number }>('/admin/promo-codes', {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(body),
    }),
  setActive: (token: string, id: string, active: boolean) =>
    request<void>(`/admin/promo-codes/${id}`, {
      method: 'PATCH',
      headers: withAuth(token),
      body: JSON.stringify({ active }),
    }),
  update: (token: string, id: string, body: PromoCreateInput & { active: boolean }) =>
    request<void>(`/admin/promo-codes/${id}`, {
      method: 'PUT',
      headers: withAuth(token),
      body: JSON.stringify(body),
    }),
  usages: (token: string, id: string) =>
    request<{ usages: PromoUsage[]; count: number }>(`/admin/promo-codes/${id}/usages`, { headers: withAuth(token) }),
}

// --- Admin: 圖片上傳 ---

export const adminImagesApi = {
  // 上傳圖片檔（multipart）→ { id, url }；不可手動設 Content-Type（讓瀏覽器帶 boundary）
  upload: async (token: string, file: File): Promise<{ id: string; url: string }> => {
    const fd = new FormData()
    fd.append('file', file)
    const res = await fetch(`${BASE}/admin/images`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}` },
      body: fd,
    })
    const text = await res.text()
    const data = text ? JSON.parse(text) : null
    if (!res.ok) throw new ApiError(res.status, data?.error ?? '上傳失敗')
    return data as { id: string; url: string }
  },
}

// --- Admin: 全域預設測試白名單 ---

export const adminTestWhitelistApi = {
  list: (token: string) =>
    request<{ emails: string[] }>('/admin/test-whitelist', { headers: withAuth(token) }),
  add: (token: string, email: string) =>
    request<void>('/admin/test-whitelist', {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify({ email }),
    }),
  remove: (token: string, email: string) =>
    request<void>(`/admin/test-whitelist?email=${encodeURIComponent(email)}`, {
      method: 'DELETE',
      headers: withAuth(token),
    }),
}

// --- Admin: 分組預設選單 ---

export const adminPresetsApi = {
  list: (token: string) =>
    request<{ presets: GroupPreset[] }>('/admin/group-presets', { headers: withAuth(token) }),
  create: (token: string, body: { name: string; default_distance_km?: number | null }) =>
    request<{ preset: GroupPreset }>('/admin/group-presets', {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(body),
    }),
}

// --- Web Push（背景推播） ---

export interface PushVapid {
  public_key: string
  enabled: boolean
}
export interface PushSubscribeBody {
  endpoint: string
  keys: { p256dh: string; auth: string }
}

export const pushApi = {
  vapidKey: (token: string) => request<PushVapid>('/push/vapid', { headers: withAuth(token) }),
  subscribe: (token: string, sub: PushSubscribeBody) =>
    request<{ ok: boolean }>('/push/subscribe', { method: 'POST', headers: withAuth(token), body: JSON.stringify(sub) }),
  unsubscribe: (token: string, endpoint: string) =>
    request<{ ok: boolean }>('/push/unsubscribe', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ endpoint }) }),
}

// --- 站內信（遊戲內訊息） ---

export interface MailItem {
  id: string
  level: 'normal' | 'important' | 'urgent'
  title: string
  body: string
  url: string
  read: boolean
  created_at: string
}

export const mailApi = {
  list: (token: string) => request<{ mail: MailItem[]; unread_count: number }>('/mail', { headers: withAuth(token) }),
  unreadCount: (token: string) => request<{ unread_count: number }>('/mail/unread-count', { headers: withAuth(token) }),
  markRead: (token: string, body: { ids?: string[]; all?: boolean }) =>
    request<{ ok: boolean; marked: number }>('/mail/read', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
}

export interface AdminPushBroadcastBody {
  title: string
  body: string
  url?: string
  channels: ('push' | 'email' | 'mail')[]
  level?: 'normal' | 'important' | 'urgent' // 站內信重要程度（勾選 mail 頻道時適用）
  target_type: 'all' | 'user' | 'race' | 'group'
  identifier?: string
  race_id?: string
  group_id?: string
}
export interface AdminPushBroadcastResult {
  recipients: number
  push_sent: number
  push_failed: number
  email_sent: number
  email_failed: number
  mail_sent: number
}

export const adminPushApi = {
  broadcast: (token: string, body: AdminPushBroadcastBody) =>
    request<AdminPushBroadcastResult>('/admin/push/broadcast', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
}

// --- Admin: Push Groups（帳號群組管理） ---

export interface PushGroup {
  id: string
  name: string
  member_count: number
}
export interface PushGroupMember {
  user_id: string
  account_code: string
  name: string
  email: string
}
export interface PushGroupDetail {
  id: string
  name: string
  members: PushGroupMember[]
}
export interface GroupAddResult {
  added: number
  not_found: string[]
}

export const adminPushGroupsApi = {
  list: (token: string) =>
    request<{ groups: PushGroup[] }>('/admin/push-groups', { headers: withAuth(token) }),
  create: (token: string, name: string) =>
    request<{ id: string }>('/admin/push-groups', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ name }) }),
  rename: (token: string, id: string, name: string) =>
    request<void>(`/admin/push-groups/${id}/rename`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ name }) }),
  del: (token: string, id: string) =>
    request<void>(`/admin/push-groups/${id}/delete`, { method: 'POST', headers: withAuth(token) }),
  get: (token: string, id: string) =>
    request<PushGroupDetail>(`/admin/push-groups/${id}`, { headers: withAuth(token) }),
  addMembers: (token: string, id: string, identifiers: string[]) =>
    request<GroupAddResult>(`/admin/push-groups/${id}/members/add`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ identifiers }) }),
  removeMember: (token: string, id: string, user_id: string) =>
    request<void>(`/admin/push-groups/${id}/members/remove`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ user_id }) }),
}

// --- Admin: Email 廣播（Resend 全玩家批次寄送，migration 141）---

export interface EmailBroadcastItem {
  id: string
  subject: string
  status: 'sending' | 'done' | 'failed' | 'partial'
  audience: string // 'all' 全部玩家 | 'custom:N人' 指定 N 位會員（migration 142）
  total_count: number
  sent_count: number
  fail_count: number
  error_note: string
  created_at: string
  finished_at: string | null
}
export interface EmailBroadcastCreateResult {
  id?: string             // dry_run=true 時不建立紀錄，不會回傳 id
  total: number
  audience: string
  not_found: string[]              // 指定對象模式：格式不合法或查無此會員的 email
  unsubscribed_excluded: number    // 指定對象模式：比對到會員但已退訂而被排除的筆數
}

export const adminEmailBroadcastApi = {
  recipientCount: (token: string) =>
    request<{ count: number }>('/admin/email-broadcasts/recipient-count', { headers: withAuth(token) }),
  list: (token: string) =>
    request<{ broadcasts: EmailBroadcastItem[] }>('/admin/email-broadcasts', { headers: withAuth(token) }),
  create: (token: string, body: { subject: string; body_html: string; recipients?: string[]; dry_run?: boolean }) =>
    request<EmailBroadcastCreateResult>('/admin/email-broadcasts', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
}

// --- 跑者充電站 / 特約商店 (Partner Shops) ---

export interface PartnerShop {          // 列表用
  id: string
  slug: string             // 自訂連結代碼（選填，空字串＝未設定）；有值時 /shop/{slug} 可取代 /shop/{id}
  name: string
  summary: string
  banner_url: string
  cta_url: string
  cta_label: string
  display_order: number
  audience: 'all' | 'vip_featured' // all=全體會員；vip_featured=VIP精選（全體玩家皆可見，gate 改放在「前往」CTA）
  is_favorited: boolean
  cta_locked?: boolean       // true＝audience='vip_featured' 且該使用者不合格；此時 cta_url 已被後端清空
  cta_lock_reason?: string   // cta_locked=true 時的原因文案；false 時為空字串
}

// 列表隨附的 VIP 精選資格資訊；後端刻意不在不合格時多回傳 vip_featured 商家內容，只給數量。
export interface PartnerListMeta {
  is_vip: boolean
  user_km: number
  min_km: number
  qualifies: boolean
  vip_featured_count: number // 全站 enabled 的 vip_featured 商家總數（不論本次是否回傳其內容）
}

export interface PartnerShopDetail extends PartnerShop {   // 詳細用
  detail_html: string      // 已由後端消毒過的安全 HTML
  photo_urls: string[]     // 多圖（輪播相簿）
  video_url: string        // 舊：單支 YouTube 原始連結（保留相容，不再是主來源）
  video_urls: string[]     // 新：多支 YouTube 原始連結（前端用 ytId() 逐支解析成 embed）
  content_images: string[] // 滿版長圖（產品 DM／長圖）；詳細頁滿版直列顯示，與 photo_urls 輪播分開
}

// 後台清單/回應用：PartnerShop 欄位（不含 is_favorited）+ 詳細欄位 + enabled（含下架）
export type AdminPartnerShop = Omit<PartnerShop, 'is_favorited'> & {
  detail_html: string
  photo_urls: string[]
  video_url: string
  video_urls: string[]
  content_images: string[] // 滿版長圖（產品 DM／長圖）；詳細頁滿版直列顯示，與 photo_urls 輪播分開
  enabled: boolean
}

// 後台新增/更新送出的 body
export interface PartnerShopWriteBody {
  slug: string             // 自訂連結代碼（選填）；空字串＝不設定
  name: string
  summary: string
  banner_url: string
  detail_html: string
  photo_urls: string[]
  video_url: string
  video_urls: string[]
  content_images: string[] // 滿版長圖（產品 DM／長圖）；詳細頁滿版直列顯示，與 photo_urls 輪播分開
  cta_url: string
  cta_label: string
  display_order: number
  enabled: boolean
  audience?: 'all' | 'vip_featured' // 空預設 all
}

// 前台（OptionalAuth：未登入也能看，登入才有 is_favorited）
export const partnersApi = {
  list: (token?: string) =>
    request<{ shops: PartnerShop[]; meta: PartnerListMeta }>('/partner-shops', token ? { headers: withAuth(token) } : undefined),
  get: (token: string | undefined, id: string) =>
    request<{ shop: PartnerShopDetail }>(`/partner-shops/${id}`, token ? { headers: withAuth(token) } : undefined),
  favorite: (token: string, shopId: string) =>
    request<{ ok: boolean }>('/profile/partner-favorites', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ shop_id: shopId }) }),
  unfavorite: (token: string, shopId: string) =>
    request<{ ok: boolean }>(`/profile/partner-favorites/${shopId}`, { method: 'DELETE', headers: withAuth(token) }),
}

// 後台（RequireAuth + RequireAdmin + RequirePerm("partners")）
export const adminPartnersApi = {
  list: (token: string) =>
    request<{ shops: AdminPartnerShop[] }>('/admin/partner-shops', { headers: withAuth(token) }),
  create: (token: string, body: PartnerShopWriteBody) =>
    request<{ shop: AdminPartnerShop }>('/admin/partner-shops', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: PartnerShopWriteBody) =>
    request<{ shop: AdminPartnerShop }>(`/admin/partner-shops/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/partner-shops/${id}`, { method: 'DELETE', headers: withAuth(token) }),
  getVipFeaturedMinKm: (token: string) =>
    request<{ min_km: number }>('/admin/partner-shops/vip-featured-min-km', { headers: withAuth(token) }),
  setVipFeaturedMinKm: (token: string, minKm: number) =>
    request<{ min_km: number }>('/admin/partner-shops/vip-featured-min-km', { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ min_km: minKm }) }),
}

// --- 活動獎勵系統 P1：序號庫存管理（合作商家/序號組/序號匯入/清單狀態）---
// 設計見 memory activity-reward-system；P1 只做序號庫存，即時獎勵 roll(P2)/玩家錢包(P3) 待後續上線。

export interface RewardMerchant {
  id: string
  name: string
  note: string
  created_at: string
}

export type RewardUseLimitType = 'single' | 'repeat' | 'unlimited'

export interface RewardSerialGroup {
  id: string
  merchant_id: string | null
  merchant_name?: string
  name: string
  item_label: string
  is_line_point: boolean
  face_value: number   // 結構化面額（migration 149）：如 1000/500，取代靠名稱字串解析；0=未設。組合型序號組回傳「組合總額」Σ(子面額×數量)
  is_bundle: boolean   // 組合型序號組（migration 150）：true=不自己存序號，由 bundle_items 定義成子面額組×數量的固定組合
  bundle_items: RewardGroupBundleItem[] // is_bundle=true 時的組合定義（子面額組×數量）；一般序號組為空陣列
  valid_from: string | null
  valid_until: string | null
  use_limit_type: RewardUseLimitType
  use_limit_count: number | null
  grant_count: number
  applies_all_races: boolean
  race_ids: string[]
  usage_note: string   // 獎勵詳情：使用說明（活動獎勵系統 P2）
  icon_url: string     // 獎勵詳情：獎勵圖示
  description: string  // 獎勵詳情：活動/獎勵說明
  created_at: string
  available_count: number   // 一般序號組=庫存序號數；組合型=可發包數 min(子面額組available / count)
  issued_count: number
  void_count: number
  total_count: number
  // 共用序號組容量（migration 178＋GroupCapacity，後端尚未上線前可能缺值）：
  // use_limit_type='repeat'→ remaining_issues=Σ剩餘可發人數；'unlimited'→ unlimited=true（remaining_issues 無意義）；
  // 'single' 不使用這兩欄，沿用 available_count 既有語意。缺值時前端一律 fallback 回舊版 available_count 顯示。
  remaining_issues?: number
  unlimited?: boolean
}

export interface RewardSerialGroupWriteBody {
  merchant_id: string | null
  name: string
  item_label: string
  is_line_point: boolean
  face_value: number // 結構化面額（migration 149）：如 1000；0=未設。組合型序號組此欄由後端依 bundle_items 算，前端傳 0 即可
  is_bundle: boolean // 組合型序號組（migration 150）
  bundle_items: RewardGroupBundleItem[] // is_bundle=true 時必填（≥1 子項）；一般序號組傳空陣列
  valid_from: string | null // RFC3339；null=即刻可用
  valid_until: string | null // RFC3339；null=無期限
  use_limit_type: RewardUseLimitType
  use_limit_count: number | null
  grant_count: number
  applies_all_races: boolean
  race_ids: string[]
  usage_note: string
  icon_url: string
  description: string
}

// --- 活動獎勵系統 P2：全域即時獎勵模板 ---

export interface RewardTemplate {
  id: string
  name: string
  items: RewardItem[]
  created_at: string
}

export const adminRewardTemplatesApi = {
  list: (token: string) =>
    request<{ templates: RewardTemplate[] }>('/admin/reward-templates', { headers: withAuth(token) }),
  create: (token: string, body: { name: string; items: RewardItem[] }) =>
    request<{ template: RewardTemplate }>('/admin/reward-templates', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: { name: string; items: RewardItem[] }) =>
    request<{ template: RewardTemplate }>(`/admin/reward-templates/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/reward-templates/${id}`, { method: 'DELETE', headers: withAuth(token) }),
}

// --- 活動優惠券券種管理（migration 138）---

export type CouponExpiryMode = 'fixed' | 'days'

export interface EventCouponDef {
  id: string
  name: string
  amount_cents: number
  expiry_mode: CouponExpiryMode
  expires_at: string | null // fixed 用（RFC3339）
  valid_days: number | null // days 用
  enabled: boolean
  created_at: string
  updated_at: string
  issued_count: number // 統計：已發放張數
  used_count: number   // 統計：已使用張數
}

export interface EventCouponDefWriteBody {
  name: string
  amount_cents: number
  expiry_mode: CouponExpiryMode
  expires_at: string | null
  valid_days: number | null
  enabled: boolean
}

export const adminEventCouponsApi = {
  list: (token: string) =>
    request<{ defs: EventCouponDef[] }>('/admin/event-coupons', { headers: withAuth(token) }),
  create: (token: string, body: EventCouponDefWriteBody) =>
    request<{ def: EventCouponDef }>('/admin/event-coupons', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: EventCouponDefWriteBody) =>
    request<{ def: EventCouponDef }>(`/admin/event-coupons/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/event-coupons/${id}`, { method: 'DELETE', headers: withAuth(token) }),
}

export interface RewardSerial {
  id: string
  group_id: string
  code: string
  link: string
  status: 'available' | 'issued' | 'void'
  used: boolean
  used_at: string | null
  issued_to: string | null
  issued_at: string | null
  created_at: string
  // 共用序號（migration 178）：此列已發給幾位得主／上限（unlimited 組無上限，issue_limit 為 null）。
  // single 型組別此欄恆為 0／null，畫面沿用舊版狀態顯示。後端尚未上線前可能缺值（undefined）。
  issue_count?: number
  issue_limit?: number | null
}

export interface RewardSerialImportResult {
  imported: number
  revived: number // 復活搬移（2026-08-29）：撞碼但原列已註銷且從未發送過玩家，改配到本次匯入目標組並重新變為可用
  skipped: number
  duplicates: string[]
}

// 批次刪除／批次註銷序號（2026-08-29 新增：序號清單複選批次操作）
export interface RewardSerialDeleteResult {
  deleted: number
  skipped: number
  reasons: string[] // 人類可讀的跳過原因彙總，如「已發送的序號不可刪除（2 筆）」
}
export interface RewardSerialVoidBatchResult {
  voided: number
  skipped: number
  reasons: string[]
}

export const adminRewardMerchantsApi = {
  list: (token: string) =>
    request<{ merchants: RewardMerchant[] }>('/admin/reward-merchants', { headers: withAuth(token) }),
  create: (token: string, body: { name: string; note: string }) =>
    request<{ merchant: RewardMerchant }>('/admin/reward-merchants', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: { name: string; note: string }) =>
    request<{ merchant: RewardMerchant }>(`/admin/reward-merchants/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/reward-merchants/${id}`, { method: 'DELETE', headers: withAuth(token) }),
}

export const adminRewardGroupsApi = {
  list: (token: string) =>
    request<{ groups: RewardSerialGroup[] }>('/admin/reward-groups', { headers: withAuth(token) }),
  create: (token: string, body: RewardSerialGroupWriteBody) =>
    request<{ group: RewardSerialGroup }>('/admin/reward-groups', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: RewardSerialGroupWriteBody) =>
    request<{ group: RewardSerialGroup }>(`/admin/reward-groups/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/reward-groups/${id}`, { method: 'DELETE', headers: withAuth(token) }),
  serials: (token: string, groupId: string, params?: { status?: string; limit?: number; offset?: number }) => {
    const qs = new URLSearchParams()
    if (params?.status) qs.set('status', params.status)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return request<{ serials: RewardSerial[]; count: number }>(`/admin/reward-groups/${groupId}/serials${suffix}`, { headers: withAuth(token) })
  },
  importSerials: (token: string, groupId: string, serials: { code: string; link: string }[]) =>
    request<RewardSerialImportResult>(`/admin/reward-groups/${groupId}/serials/import`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify({ serials }),
    }),
  voidSerial: (token: string, groupId: string, serialId: string) =>
    request<{ ok: boolean }>(`/admin/reward-groups/${groupId}/serials/${serialId}/void`, { method: 'PUT', headers: withAuth(token) }),
  voidSerialsBatch: (token: string, groupId: string, ids: string[]) =>
    request<RewardSerialVoidBatchResult>(`/admin/reward-groups/${groupId}/serials/void-batch`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ ids }),
    }),
  deleteSerials: (token: string, groupId: string, ids: string[]) =>
    request<RewardSerialDeleteResult>(`/admin/reward-groups/${groupId}/serials/delete`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ ids }),
    }),
}

// 環台大富翁（Phase 1：盤面遊戲）
export interface MonopolyState {
  position: number
  laps_completed: number
  gp_balance: number
  dice_gp_cost: number
}
export interface MonopolyRollResult {
  roll: number           // 伺服器決定的點數 1..6（前端動畫必須停在這個值，前端無法自行決定）
  from: number
  to: number
  laps_gained: number
  landed_on: 'normal' | 'chance' | 'destiny'
  lap_reward_gp: number
  gp_balance: number
  draw_pending: boolean  // true=停在機會/命運格，抽卡功能 Phase 3 才開放
  draw_result?: DrawResult // 本次實際抽到的獎勵（draw_pending=true 時才有）
}

// 機會/命運抽卡結果（A2）。只有 type 保證存在，其餘欄位依 type 條件性出現，一律視為 optional。
export interface DrawResult {
  type: 'gp' | 'dp' | 'vip_days' | 'knowledge_card' | 'sticker' | 'redemption_code'
  title?: string
  body?: string           // 知識卡正文 / 兌換碼說明
  image_url?: string
  rarity?: 'common' | 'rare'
  amount?: number         // gp/dp/vip_days 的數量
  is_duplicate?: boolean
  converted_gp?: number   // 重複卡/兌換碼耗盡 fallback 轉發的 GP
  code?: string           // 兌換碼
  kind?: 'line_point' | 'coupon' // 兌換碼種類
  is_fallback?: boolean   // true=兌換碼庫存耗盡，已改發 converted_gp
  main_category?: string
  subtopic?: string
  player_action?: string
  risk_note?: string
  source_org?: string
  source_url?: string
}

// 知識卡圖鑑（GET /monopoly/knowledge）。防劇透：未擁有(owned=false)時只有前 5 個欄位，展示用欄位全省略。
export interface KnowledgeCard {
  id: string
  theme: 'training' | 'care'
  main_category: string
  rarity: 'common' | 'rare'
  owned: boolean
  obtained_count: number
  code?: string
  subtopic?: string
  title?: string
  body?: string
  player_action?: string
  timing?: string
  importance?: string
  handling_level?: string
  game_effect_hint?: string
  risk_note?: string
  source_org?: string
  source_doc?: string
  source_url?: string
  image_url?: string
}
export interface KnowledgeGallery {
  counts: {
    training_total: number
    training_owned: number
    care_total: number
    care_owned: number
  }
  cards: KnowledgeCard[]
}

// 完賽公仔九宮格貼紙（GET /monopoly/stickers）。與知識卡不同，貼紙不做防劇透——灰階片本來就要讓
// 玩家看到缺哪片，所以每片一律回 gray_url，不因 owned=false 而省略欄位。
export interface StickerPiece {
  position: number // 1..9，3×3 row-major：1=左上 2=上中 3=右上 4=中左 5=中央 6=中右 7=左下 8=下中 9=右下
  title: string
  rarity: 'common' | 'rare'
  gray_url: string
  owned: boolean
  obtained_count: number
}
export interface StickerGallery {
  title: string      // 公仔名稱（例如「完賽跑者公仔」）
  figure_url: string // 完整彩圖 1254×1254，已收集的格子用此圖裁 1/9 顯示
  total: number       // 恆為 9
  owned: number        // 已收集片數
  pieces: StickerPiece[] // 依 position 排序
  line_oa: string    // 官方 LINE OA id（如 '@855xfwqe'），兌換彈窗用來組 LINE 加好友連結
  landing_url: string // 完賽公仔 Landing Page URL
  redemption_status: '' | 'pending' | 'fulfilled' | 'rejected' // 該使用者兌換申請狀態；''=未申請
}

export const monopolyApi = {
  state: (token: string) => request<MonopolyState>('/monopoly/state', { headers: withAuth(token) }),
  // GP 不足回 409 {error:"GP 不足"}（呼叫端用 e.status===409 辨識，不必比對訊息文字）
  roll: (token: string) => request<MonopolyRollResult>('/monopoly/roll', { method: 'POST', headers: withAuth(token) }),
  knowledge: (token: string) => request<KnowledgeGallery>('/monopoly/knowledge', { headers: withAuth(token) }),
  stickers: (token: string) => request<StickerGallery>('/monopoly/stickers', { headers: withAuth(token) }),
  // 完賽公仔兌換申請：伺服器驗證已集滿九宮格才受理、冪等（已申請過就回現況）；未集滿回 400
  redeemFigure: (token: string) => request<{ status: string }>('/monopoly/figure/redeem', { method: 'POST', headers: withAuth(token) }),
}

// --- Admin: 環台大富翁（C1 後端 /admin/monopoly；見 internal/monopoly/admin.go・admin_repo.go） ---

export type MonopolyPool = 'chance' | 'destiny'
export type MonopolyRewardType = 'gp' | 'dp' | 'vip_days' | 'knowledge_card' | 'sticker' | 'redemption_code'

// POST/PATCH /admin/monopoly/pool 的請求 body 形狀（PATCH 為全量更新，非欄位級部分更新）。
// 後端驗證：gp/dp/vip_days → amount 必須 >0；redemption_code → redemption_batch_key 必填；
// knowledge_card/sticker → amount 與 redemption_batch_key 一律被後端清空（即使送了也無效）。
export interface PoolEntryInput {
  pool: MonopolyPool
  reward_type: MonopolyRewardType
  weight: number
  amount: number
  redemption_batch_key: string
  note: string
  is_active: boolean
  sort_order: number
}
export interface PoolEntry extends PoolEntryInput {
  id: string
}

// GET /admin/monopoly/redeem/batches 依 batch_key 匯總列。
export interface RedeemBatch {
  batch_key: string
  kind: 'line_point' | 'coupon'
  label: string
  total: number
  used: number
  remaining: number
}
// GET /admin/monopoly/redeem/batch/{key} 單筆碼（不含 used_by）。
export interface RedeemCode {
  code: string
  is_used: boolean
  used_at?: string | null
}

// GET /admin/monopoly/cards 單張知識卡（後台管理用，不做前台防劇透）。
export interface AdminKnowledgeCard {
  id: string
  code: string
  theme: 'training' | 'care'
  main_category: string
  subtopic: string
  title: string
  rarity: 'common' | 'rare'
  image_url: string
  is_active: boolean
}

// GET/PUT /admin/monopoly/settings；dup_gp 目前固定 {common, rare} 兩把，用索引簽章保留彈性。
export interface MonopolySettings {
  dup_gp: Record<string, number>
  redeem_fallback_gp: number
}

// GET /admin/monopoly/stickers 單片（set_key 固定 'finisher'，後台不需要顯示）。
export interface AdminStickerPiece {
  id: string
  position: number
  title: string
  image_url: string
  rarity: 'common' | 'rare'
  is_active: boolean
}
// GET /admin/monopoly/stickers 回應：9 片灰階拼圖 ＋ 彩圖 URL／標題／LINE OA／完賽公仔 Landing URL。
export interface AdminStickerGallery {
  figure_color_url: string
  figure_title: string
  line_oa: string
  landing_url: string
  pieces: AdminStickerPiece[]
}

// GET /admin/monopoly/redemptions 單列／PATCH 回應：完賽公仔兌換申請（JOIN users/user_profiles）。
export type FigureRedemptionStatus = 'pending' | 'fulfilled' | 'rejected'
export interface FigureRedemption {
  id: string
  user_id: string
  account_code: string
  nickname: string
  email: string
  status: FigureRedemptionStatus
  note: string
  created_at: string
}

export const adminMonopolyApi = {
  // 獎勵池
  poolList: (token: string) => request<{ entries: PoolEntry[] }>('/admin/monopoly/pool', { headers: withAuth(token) }),
  poolCreate: (token: string, body: PoolEntryInput) =>
    request<PoolEntry>('/admin/monopoly/pool', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  poolUpdate: (token: string, id: string, body: PoolEntryInput) =>
    request<PoolEntry>(`/admin/monopoly/pool/${id}`, { method: 'PATCH', headers: withAuth(token), body: JSON.stringify(body) }),
  poolDelete: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/monopoly/pool/${id}`, { method: 'DELETE', headers: withAuth(token) }),

  // 兌換碼批次
  redeemBatches: (token: string) => request<{ batches: RedeemBatch[] }>('/admin/monopoly/redeem/batches', { headers: withAuth(token) }),
  redeemCreateBatch: (token: string, body: { batch_key: string; kind: 'line_point' | 'coupon'; label: string; codes_text: string }) =>
    request<{ inserted: number; skipped: number }>('/admin/monopoly/redeem/batch', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  redeemBatchCodes: (token: string, key: string) =>
    request<{ codes: RedeemCode[] }>(`/admin/monopoly/redeem/batch/${encodeURIComponent(key)}`, { headers: withAuth(token) }),

  // 知識卡（主要用途＝補圖／調稀有度／上下架）
  cards: (token: string, theme?: 'training' | 'care') =>
    request<{ cards: AdminKnowledgeCard[] }>(`/admin/monopoly/cards${theme ? `?theme=${theme}` : ''}`, { headers: withAuth(token) }),
  updateCard: (token: string, id: string, body: { image_url?: string; rarity?: 'common' | 'rare'; is_active?: boolean; title?: string; body?: string }) =>
    request<AdminKnowledgeCard>(`/admin/monopoly/cards/${id}`, { method: 'PATCH', headers: withAuth(token), body: JSON.stringify(body) }),

  // 公仔貼紙（主要用途＝補圖／換彩圖／上下架）
  stickers: (token: string) => request<AdminStickerGallery>('/admin/monopoly/stickers', { headers: withAuth(token) }),
  updateSticker: (token: string, id: string, body: { image_url?: string; title?: string; rarity?: 'common' | 'rare'; is_active?: boolean }) =>
    request<AdminStickerPiece>(`/admin/monopoly/stickers/${id}`, { method: 'PATCH', headers: withAuth(token), body: JSON.stringify(body) }),
  setFigureSettings: (token: string, body: { figure_color_url: string; figure_title: string; line_oa?: string; landing_url?: string }) =>
    request<{ figure_color_url: string; figure_title: string; line_oa: string; landing_url: string }>('/admin/monopoly/figure-settings', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),

  // 完賽公仔兌換申請（審核）；PATCH 為全量覆寫（非欄位級部分更新），status 必填限三種，note 未帶視同空字串。
  redemptions: (token: string) => request<{ redemptions: FigureRedemption[] }>('/admin/monopoly/redemptions', { headers: withAuth(token) }),
  updateRedemption: (token: string, id: string, patch: { status: FigureRedemptionStatus; note?: string }) =>
    request<FigureRedemption>(`/admin/monopoly/redemptions/${id}`, { method: 'PATCH', headers: withAuth(token), body: JSON.stringify(patch) }),
  // 重置為新一輪：清空該玩家公仔碎片＋刪除此兌換紀錄，玩家可重新收集
  resetRedemption: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/monopoly/redemptions/${id}/reset`, { method: 'POST', headers: withAuth(token) }),

  // 抽卡設定
  settings: (token: string) => request<MonopolySettings>('/admin/monopoly/settings', { headers: withAuth(token) }),
  setSettings: (token: string, body: MonopolySettings) =>
    request<MonopolySettings>('/admin/monopoly/settings', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
}

// --- Admin: 會員活躍度分析（member_analytics_reports，migration 148）---
// 契約鍵名與後端 internal/analytics（日報 JSONB）一致，勿自行改名；統計皆已排除 users.is_virtual、
// 活動皆已排除 flagged，時區台灣日（見後端 internal/ops/dailyreport.go 同口徑）。

export interface AnalyticsDatePoint { date: string; count: number }
export interface AnalyticsKmPoint { date: string; km: number }
export interface AnalyticsBucket { bucket: string; count: number }
export interface AnalyticsSourceCount { source: string; count: number }
export interface AnalyticsGroupAvg { group: string; avg_km: number; users: number }
export interface AnalyticsTopCount { title?: string; name?: string; count: number }
export interface AnalyticsSystemUsage { system: string; label: string; users_30d: number; users_total: number }
export interface AnalyticsRunner {
  name: string
  handle: string
  is_virtual: boolean
  total_km: number
  total_duration_s: number
  avg_pace_s: number
  runs: number
  avg_days_per_week: number
  // level/dp/gp：目前等級（後端已依 exp 換算好，比照會員面板 Lv.X）／DP／GP 現況快照。舊日報
  // （本三欄上線前算出的 JSONB，即使 runners 陣列本身已存在）沒有這三個鍵 → optional，容忍 undefined
  // 顯示為 —（與 runners 整體 undefined 的舊版提示機制並存，見 RunnersSection）。
  level?: number
  dp?: number
  gp?: number
  // rank_delta／is_new：與「上週或更早最近一份」報告比較的真人榜名次升降（見後端 model.go
  // RunnerStat 型別註解）。只在真人列（is_virtual=false）有值；虛擬列兩者永遠缺省。舊日報（本兩欄
  // 上線前算出的）也沒有這兩個鍵 → optional，容忍 undefined；rank_delta 缺且 is_new 非 true 時，
  // 前端一律顯示「—」（無法判斷是否有變化，見 RunnersSection）。
  rank_delta?: number
  is_new?: boolean
}

// AnalyticsRunnersSummary 第七區塊表格上方的總覽統計列（見後端 model.go RunnersSummary 型別註解）。
export interface AnalyticsRunnersSummary {
  ran_yesterday_real: number
  ran_yesterday_virtual: number
  ran_7d_real: number
  ran_7d_virtual: number
  runners_total_real: number
  runners_total_virtual: number
  members_real: number
  members_virtual: number
}

export interface MemberAnalyticsReport {
  day: string // YYYY-MM-DD，統計基準日
  generated_at: string
  registrations: {
    total_members: number
    new_30d: AnalyticsDatePoint[]
    by_hour: number[] // 24 個 int，index=小時
    by_source: AnalyticsSourceCount[]
  }
  logins: {
    dau_30d: AnalyticsDatePoint[]
    active_7d: number
    active_30d: number
    freq_dist_30d: AnalyticsBucket[]
    by_hour: number[]
  }
  mileage: {
    daily_km_30d: AnalyticsKmPoint[]
    pace_dist: AnalyticsBucket[]
    monthly_volume_dist: AnalyticsBucket[]
    by_gender: AnalyticsGroupAvg[]
    by_age: AnalyticsGroupAvg[]
  }
  participation: {
    reg_30d: AnalyticsDatePoint[]
    ever_registered_pct: number
    top_races: AnalyticsTopCount[]
    repeat_dist: AnalyticsBucket[]
  }
  cards: {
    collectors: number
    total_collected: number
    collection_dist: AnalyticsBucket[]
    top_cards: AnalyticsTopCount[]
  }
  systems: {
    usage: AnalyticsSystemUsage[]
  }
  // runners 第七區塊「跑步數據分析排行」：舊日報（本欄位上線前算出的）沒有這個鍵，故為 optional，
  // 前端顯示「按『立即重算』後出現」提示（見 admin/analytics/page.tsx）。
  runners?: AnalyticsRunner[]
  // runners_summary 第七區塊表格上方的總覽統計列：舊日報（本欄位上線前算出的）沒有這個鍵，故為
  // optional，前端不顯示這一列統計（比照 runners 欄位的既有慣例，見 admin/analytics/page.tsx）。
  runners_summary?: AnalyticsRunnersSummary
}

export interface MemberAnalyticsResponse {
  report: MemberAnalyticsReport
  stale: boolean // 最新一筆已超過 48h 未重算
}

export const adminAnalyticsApi = {
  // 讀最新一筆已存檔的日報
  get: (token: string) => request<MemberAnalyticsResponse>('/admin/analytics/report', { headers: withAuth(token) }),
  // 立即重算並存檔（後端 20s timeout 內完成），回同形狀 report
  recompute: (token: string) => request<MemberAnalyticsResponse>('/admin/analytics/recompute', { method: 'POST', headers: withAuth(token) }),
}

// --- WebSocket helper ---

export function createRaceSocket(raceID: string, accessToken: string): WebSocket {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const host = window.location.host
  const url = `${protocol}//${host}/ws/race/${raceID}?token=${accessToken}`
  return new WebSocket(url)
}

// 全站資料異動推播（data_updated）：登入後於全站掛載一條連線（見 SiteRealtime.tsx）
export function createSiteSocket(accessToken: string): WebSocket {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const host = window.location.host
  const url = `${protocol}//${host}/ws/site?token=${accessToken}`
  return new WebSocket(url)
}

// ── 跑步鼓勵語（每公里彈出；專注模式進度條配套，2026-08-29）──────────────────────────
// phase：'before'＝完成目標 50% 前（累積式文案，含佔位符 {done}，例「加油!!你已經完成{done}囉!!」→ 3 km）；
//        'after' ＝超過 50% 後（剩餘式文案，含 {remain}，例「努力撐住，還剩下{remain}。」→ 7 km／38 分鐘）。
// 無目標跑步一律用 before 池。佔位符由前台代入，後台文案不需管單位。
export type RunCheerPhase = 'before' | 'after'
export interface RunCheerMessage {
  id: string
  phase: RunCheerPhase
  text: string
  enabled: boolean
  sort_order: number
  created_at: string
}
export interface RunCheerInput {
  phase: RunCheerPhase
  text: string
  enabled: boolean
  sort_order: number
}
// 後台 CRUD（perm scope: run_cheers）
export const adminRunCheersApi = {
  list: (token: string) =>
    request<{ items: RunCheerMessage[] }>('/admin/run-cheers', { headers: withAuth(token) }),
  create: (token: string, body: RunCheerInput) =>
    request<{ item: RunCheerMessage }>('/admin/run-cheers', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: RunCheerInput) =>
    request<{ item: RunCheerMessage }>(`/admin/run-cheers/${encodeURIComponent(id)}`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(body),
    }),
  remove: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/admin/run-cheers/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),
}
// 前台公開讀取（免登入）：只回 enabled 的文案，依 sort_order, created_at 排序
export const runCheersApi = {
  get: () => request<{ before: string[]; after: string[] }>('/run-cheers'),
}

// ── 啦啦隊位置校正（2026-08-29）────────────────────────────────────────────────
// dx/dy：相對角色容器「自身寬/高」的百分比位移（跨裝置一致；正 dx 往右、正 dy 往下），scale：縮放倍率（transform-origin 上中）。
export interface CheerCharLayoutItem { dx: number; dy: number; scale: number }
// 角色清單：新增角色時把 webp 放到 public/ui/cheer/cheerleader-NN.webp，並在這裡（與後端 profile/cheer_layout.go 的
// cheerLayoutKeys）加上編號即可；其餘（預設校正值、解析、CheerShow 隨機/校正 UI）皆由此陣列驅動。
export const CHEER_CHAR_IDS = ['01', '02', '03', '04', '05', '06', '07', '08'] as const
export type CheerCharId = (typeof CHEER_CHAR_IDS)[number]
export type CheerCharLayout = Record<CheerCharId, CheerCharLayoutItem>
export const DEFAULT_CHEER_CHAR_LAYOUT: CheerCharLayout = Object.fromEntries(
  CHEER_CHAR_IDS.map((id) => [id, { dx: 0, dy: 0, scale: 1 }]),
) as CheerCharLayout
// 解析 Dashboard.cheer_char_layout；缺欄位/壞 JSON 一律補預設，永不 throw
export function parseCheerCharLayout(raw: string | null | undefined): CheerCharLayout {
  const out = Object.fromEntries(CHEER_CHAR_IDS.map((id) => [id, { ...DEFAULT_CHEER_CHAR_LAYOUT[id] }])) as CheerCharLayout
  if (!raw) return out
  try {
    const j = JSON.parse(raw) as Partial<Record<CheerCharId, Partial<CheerCharLayoutItem>>>
    for (const id of CHEER_CHAR_IDS) {
      const v = j?.[id]
      if (!v) continue
      if (typeof v.dx === 'number' && Number.isFinite(v.dx)) out[id].dx = v.dx
      if (typeof v.dy === 'number' && Number.isFinite(v.dy)) out[id].dy = v.dy
      if (typeof v.scale === 'number' && Number.isFinite(v.scale) && v.scale > 0) out[id].scale = v.scale
    }
  } catch { /* 壞 JSON → 預設 */ }
  return out
}
// 儲存校正值：後端掛 requireEntry(cheer_edit_entry_state/whitelist)，非白名單 403
export const cheerLayoutApi = {
  save: (token: string, layout: CheerCharLayout) =>
    request<{ ok: boolean; layout: CheerCharLayout }>('/me/cheer-layout', {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify({ layout }),
    }),
}

// ── 團練邀請（run meets，見 services/api/internal/runmeet；migration 156）─────────────────
// ⚠️ 中文顯示文案一律用「團練」，不得出現「跑團」二字（賽事已有「跑團分組」，撞名會混淆）。
//    英文命名維持 run_meets / run-meets / runmeet（語意＝跑步聚會），與 race group 不衝突。
//
// ⚠️ 地點三層揭露（本功能最重要的資安不變式，型別即契約）：
//    公開層 region / place_label      → 所有人（列表與詳情皆可見）
//    成員層 lat / lng / meeting_detail → 發起人、已加入成員、後台
//    後端用**兩種不同的 DTO** 分層：未加入者拿到的 JSON **根本沒有** lat/lng/meeting_detail 三個 key
//    （不是 null、不是 0）。前端型別因此用 discriminated union（location_locked），
//    不要為了省事把兩者併成一個「欄位都 optional」的介面——那會讓「忘了判斷」重新變成可能。

export type RunMeetOwner = { id: string; name: string; avatar_url: string }
export type RunMeetMyState = 'none' | 'pending' | 'joined' | 'rejected' | 'kicked' | 'left' | 'owner'
// open⇄closed（closed＝暫停收新成員，其他功能照舊）、open/closed→cancelled（中止＝停止一切加入動作，
// 待審申請一併婉拒）、cancelled→open（重新開啟）；cancelled→closed 不合法（後端回 409）。
export type RunMeetStatus = 'open' | 'closed' | 'cancelled'
// POST /run-meets/{id}/status 的 body.action（見 runMeetApi.setStatus）。
export type RunMeetStatusAction = 'open' | 'close' | 'cancel'
// ⚠️ 後端刻意只回「距離分級」不回精確距離：回 0.23 km 這種值可讓攻擊者換多組座標查詢、
//    三角定位反推出精確地點，等於繞過整套地點分層設計。只在帶 near_lat/near_lng 查詢時出現。
export type RunMeetDistanceBand = 'lt1' | '1to3' | '3to5' | '5to10' | 'gt10'
export type RunMeetReactionKind = 'like' | 'fire' | 'muscle' | 'pray' | 'heart'
// 團練生命週期三態（phase-2，anchor＝effectiveEnd=COALESCE(ends_at, meet_at)）：
// upcoming（尚未到 meet_at）／ongoing（meet_at ≤ 現在 < effectiveEnd）／ended（現在 ≥ effectiveEnd）。
// is_ended 是這裡的 boolean 化簡版（is_ended === (phase === 'ended')）——兩者恆一致，元件挑一個用即可，
// 不需要每處都改成 phase；3 態文字（即將開始／進行中／已結束）才是非用 phase 不可的地方
// （見 lib/runMeet.ts meetPhase／MEET_PHASE_LABEL）。
export type RunMeetPhase = 'upcoming' | 'ongoing' | 'ended'

export interface RunMeetCard {
  id: string
  title: string
  meet_at: string            // RFC3339；顯示一律用 Asia/Taipei 格式化（見 lib/runMeet.ts）
  // 結束時間（migration 168）。RFC3339，恆與 meet_at 同一台北日曆日且晚於 meet_at（後端已驗證）。
  // 為 null 僅發生在 migration 168 上線前建立的舊資料——顯示一律走 lib/runMeet.ts fmtMeetRange，
  // 它會在 ends_at 為 null 時自動退回只顯示開始時間，不要在元件裡各自寫判斷。
  ends_at: string | null
  region: string             // 公開層：縣市・行政區
  place_label: string        // 公開層：地標名
  // 「不限地點」（migration 161）：true 時 region/place_label 固定是「不限」佔位文字——
  // 前端顯示邏輯必須先判斷這個旗標，不可直接拼接兩欄，否則會顯示成「不限・不限」
  // （見 lib/runMeet.ts runMeetLocationText）。詳情頁另據此決定要不要載入地圖與 Leaflet。
  no_location: boolean
  capacity: number
  member_count: number
  is_private: boolean
  approval_required: boolean
  excerpt: string            // 60 字摘要（列表不給完整 description，swrCache 單筆 100KB 上限）
  cover_url: string | null   // 私密團未解鎖、或 show_cover=false 時為 null（見下方 show_cover）
  // 「顯示封面圖片」偏好（migration 162），預設 true。這是原始偏好值本身，不是「這次算出來
  // 要不要顯示」的結果（cover_url 才是那個結果）——編輯表單用這個欄位決定 checkbox 初始勾選狀態，
  // 不要用 cover_url==null 反推（那也可能單純是沒圖，或私密團未解鎖）。
  show_cover: boolean
  status: RunMeetStatus
  // is_ended 現在的定義是 now >= COALESCE(ends_at, meet_at)（migration 168 phase-2），
  // 不再只看 meet_at——舊資料 ends_at=null 時等同舊行為不變。
  is_ended: boolean
  // 三態顯示用（見上方 RunMeetPhase 說明）；is_ended 與這個欄位的 'ended' 恆一致。
  phase: RunMeetPhase
  owner: RunMeetOwner
  my_state: RunMeetMyState
  reaction_count: number
  comment_count: number
  my_reaction: RunMeetReactionKind | null
  has_access: boolean
  distance_band?: RunMeetDistanceBand
  // 發起人自己把團練從探索/連結隱藏（≠ status、≠ 後台強制下架 hidden_by_admin）。
  // 恆存在；非發起人/後台視角一律 false（不外洩「這團被隱藏了」給其他人）。
  hidden_by_owner: boolean
}

interface RunMeetDetailBase extends RunMeetCard {
  description: string
  image_urls: string[]  // 恆為陣列；私密團未解鎖時為 []
  image_limit: number   // 建立當下的 VIP 快照（1 或 4）；編輯表單以此為上限，不可用即時 VIP 判定
  pending_count: number // 非 owner/admin 恆 0
  can_comment: boolean  // joined/owner 且未過「結束後 7 天」唯讀線
}

/** 未加入者（含已解鎖但尚未加入、申請中）看到的詳情：結構上就沒有成員層三欄位。 */
export interface RunMeetPublicDetail extends RunMeetDetailBase {
  location_locked: true
  location_note: string // 固定「成功加入後才會顯示完整詳細地點」
}

/** 發起人／已加入成員／後台看到的詳情。 */
export interface RunMeetMemberDetail extends RunMeetDetailBase {
  location_locked: false
  lat: number | null // 與 lng 成對出現（同時 null 或同時有值）
  lng: number | null
  meeting_detail: string
}

// 前端判斷：if (!d.location_locked) { 載入 Leaflet 地圖 + 標記 } else { 只顯示公開層 + location_note }
export type RunMeetDetail = RunMeetPublicDetail | RunMeetMemberDetail

export interface RunMeetMember {
  user_id: string
  name: string
  avatar_url: string
  is_owner: boolean
  status: 'joined' | 'pending'
  apply_note: string
  joined_at: string | null
  applied_at: string
}

// migration 159：留言升級成討論串——ParentID/ReplyCount/Replies 只有頂層留言（parent_id===null）
// 才有意義；回覆的 ReplyCount 恆 0、Replies 恆 []（規格只允許一層，回覆沒有子回覆）。
// ⚠️ 軟刪遮蔽：deleted===true 時 body 恆 ''、can_delete 恆 false、reactions 恆 []、
// my_reaction 恆 null——但列表仍會回傳這則留言本身（佔位），底下的回覆照常顯示。
export interface RunMeetComment {
  id: string
  user_id: string
  name: string
  avatar_url: string
  body: string
  created_at: string
  can_delete: boolean
  parent_id: string | null
  reply_count: number
  reactions: { kind: RunMeetReactionKind; count: number }[]
  my_reaction: RunMeetReactionKind | null
  deleted: boolean
  replies: RunMeetComment[]
}

export interface RunMeetQuota {
  month: string      // 台北月 YYYY-MM
  cap: number
  used: number
  remaining: number
  is_vip: boolean
  requires_vip: boolean // runmeet_create_requires_vip=1 時，非 VIP 按發起要跳 VIP 引導（不扣次數）
  image_limit: number
  // ⚠️ VIP 權益數字一律用後端帶下來的這兩個值做文案，前端不得寫死 10 / 4——
  //    它們是後台可調設定（runmeet_quota_vip / runmeet_images_vip），營運一改，
  //    寫死的文案就會對非 VIP 承諾拿不到的權益。
  vip_cap: number
  vip_image_limit: number
  capacity_max: number
  resets_at: string
}

/** 熱門跑點快選建議（來源 explore_bosses；後端只回四個地點欄位，不含關主身分/圖/課表）。 */
export interface RunMeetPlaceSuggestion { region: string; place: string; lat: number; lng: number }

export interface RunMeetInput {
  title: string
  meet_at: string        // ISO（表單以台北時間解讀後轉出，見 lib/runMeet.ts taipeiLocalToISO）
  // 結束時間（migration 168）：建立／編輯皆必填，須與 meet_at 同一台北日曆日且晚於 meet_at
  // （後端 400「請填寫結束時間」／「結束時間須晚於開始時間」／「結束時間須在當天」）。
  // 刻意宣告成必填（不是 `ends_at?:`），理由同 show_cover 正下方那則註解——逼所有建構
  // RunMeetInput 的呼叫端都要明確帶值。
  ends_at: string
  region: string
  place_label: string
  // 「不限地點」：true 時後端會強制清空 lat/lng、region/place_label 空白時補「不限」
  // （見 services/api/internal/runmeet/service.go normalizeNoLocation）。
  no_location: boolean
  lat?: number | null
  lng?: number | null
  meeting_detail?: string
  capacity: number
  description?: string
  image_urls?: string[]
  approval_required: boolean
  // 「顯示封面圖片」開關（migration 162），預設 true。⚠️ 後端用 *bool 解析、把「省略此欄位」
  // 當成「維持預設/原值」——前端這裡刻意宣告成必填（不是 `show_cover?:`），逼所有建構
  // RunMeetInput 的呼叫端都要明確帶值，不要不小心漏帶而觸發後端那條「未帶欄位」語意。
  show_cover: boolean
  // 建立：非空＝私密團；編輯：欄位省略=不動、''=移除密碼、其他=重設
  password?: string | null
  client_token?: string  // crypto.randomUUID()，防連點/網路重試重複扣配額
}

export interface RunMeetAdminRow {
  id: string
  title: string
  meet_at: string
  ends_at: string | null // migration 168；舊資料為 null，見 RunMeetCard.ends_at 的說明
  region: string
  place_label: string
  capacity: number
  member_count: number
  pending_count: number
  is_private: boolean
  status: RunMeetStatus
  hidden_by_admin: boolean
  hidden_reason: string
  deleted: boolean
  comment_count: number
  reaction_count: number
  quota_month: string
  created_at: string
  owner: RunMeetOwner
}

export interface RunMeetAdminReport {
  id: string
  meet_id: string
  meet_title: string
  comment_id: string | null
  comment_body: string
  reporter_id: string
  reporter_name: string
  reason: string
  status: 'pending' | 'handled' | 'dismissed'
  review_note: string
  created_at: string
}

export interface RunMeetListParams {
  q?: string
  region?: string
  privacy?: 'public' | 'private'
  approval?: 'free' | 'review'
  has_slot?: '1'
  ended?: '1'
  sort?: 'soon' | 'new' | 'hot'
  limit?: number
  offset?: number
  // 附近搜尋：使用者位置只當查詢參數，後端不寫 DB、不進 log；回應只給 distance_band。
  near_lat?: number
  near_lng?: number
  radius_km?: number
}

function runMeetQuery(params: object): string {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue
    sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

// 前台（RequireAuth → requireEntry：入口未開放一律 403「團練邀請尚未開放。」）
export const runMeetApi = {
  list: (token: string, params: RunMeetListParams = {}) =>
    request<{ items: RunMeetCard[]; total: number }>(`/run-meets${runMeetQuery(params)}`, { headers: withAuth(token) }),
  mine: (token: string) =>
    request<{ owned: RunMeetCard[]; joined: RunMeetCard[]; pending: RunMeetCard[] }>('/run-meets/mine', { headers: withAuth(token) }),
  quota: (token: string) =>
    request<RunMeetQuota>('/run-meets/quota', { headers: withAuth(token) }),
  placeSuggest: (token: string, q?: string, lat?: number, lng?: number) =>
    request<{ items: RunMeetPlaceSuggestion[] }>(`/run-meets/place-suggest${runMeetQuery({ q, lat, lng })}`, { headers: withAuth(token) }),
  create: (token: string, input: RunMeetInput) =>
    request<{ meet: RunMeetDetail; used: number; remaining: number }>('/run-meets', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(input),
    }),
  // 上傳團練圖片（multipart，欄位名 file）；不可手動設 Content-Type（讓瀏覽器帶 boundary）。
  // 後端只收 JPG/PNG（嗅探 + DecodeConfig 交叉比對）並無條件重新編碼（去 EXIF/GPS 與夾帶內容）。
  uploadImage: async (token: string, file: File): Promise<{ id: string; url: string }> => {
    const fd = new FormData()
    fd.append('file', file)
    const res = await fetch(`${BASE}/run-meets/images`, { method: 'POST', headers: { Authorization: `Bearer ${token}` }, body: fd })
    const text = await res.text()
    const data = text ? JSON.parse(text) : null
    if (!res.ok) throw new ApiError(res.status, data?.error ?? '圖片上傳失敗')
    return data as { id: string; url: string }
  },
  // 私密團未解鎖 → 403 { error, locked:true, card }（見 RunMeetScreen 的解鎖流程；
  // request() 只會把 error 字串包成 ApiError，卡片摘要由呼叫端改打 list 或直接顯示已知卡片）。
  // 已被發起人刪除 → 410 { error: 'deleted' }（其餘不可見仍是 404）；呼叫端用 ApiError.status 判斷，
  // 見 RunMeetDetailView 的「該團練已被刪除，3 秒後將返回團練邀請頁」倒數導頁。
  detail: (token: string, id: string) =>
    request<{ meet: RunMeetDetail }>(`/run-meets/${id}`, { headers: withAuth(token) }),
  update: (token: string, id: string, input: RunMeetInput) =>
    request<{ meet: RunMeetDetail; pending_kept: number }>(`/run-meets/${id}`, {
      method: 'PUT', headers: withAuth(token), body: JSON.stringify(input),
    }),
  // 狀態機（見 RunMeetStatus 註解）：action 'open'|'close'|'cancel'。
  // rejected＝中止時被一併婉拒的待審申請數（後端在同一交易內處理）；關閉時待審申請會保留、
  // rejected 恆 0（重新開啟後可繼續處理）。非法轉換（例如 cancelled→closed）回 409。
  setStatus: (token: string, id: string, action: RunMeetStatusAction) =>
    request<{ ok: boolean; status: RunMeetStatus; rejected: number }>(`/run-meets/${id}/status`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ action }),
    }),
  // 發起人自行隱藏/取消隱藏（限發起人）；與 status 無關，closed/cancelled 期間也能隱藏。
  setVisibility: (token: string, id: string, hidden: boolean) =>
    request<{ ok: boolean; hidden: boolean }>(`/run-meets/${id}/visibility`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ hidden }),
    }),
  remove: (token: string, id: string) =>
    request<{ ok: boolean }>(`/run-meets/${id}`, { method: 'DELETE', headers: withAuth(token) }),
  unlock: (token: string, id: string, password: string) =>
    request<{ ok: boolean }>(`/run-meets/${id}/unlock`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ password }) }),
  join: (token: string, id: string, note?: string) =>
    request<{ state: 'joined' | 'pending' }>(`/run-meets/${id}/join`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ note: note ?? '' }),
    }),
  leave: (token: string, id: string) =>
    request<{ state: 'withdrawn' | 'left' }>(`/run-meets/${id}/join`, { method: 'DELETE', headers: withAuth(token) }),
  members: (token: string, id: string, status: 'joined' | 'pending') =>
    request<{ items: RunMeetMember[]; total: number }>(`/run-meets/${id}/members?status=${status}`, { headers: withAuth(token) }),
  approve: (token: string, id: string, uid: string) =>
    request<{ ok: boolean }>(`/run-meets/${id}/members/${uid}/approve`, { method: 'POST', headers: withAuth(token) }),
  reject: (token: string, id: string, uid: string) =>
    request<{ ok: boolean }>(`/run-meets/${id}/members/${uid}/reject`, { method: 'POST', headers: withAuth(token) }),
  kick: (token: string, id: string, uid: string) =>
    request<{ ok: boolean }>(`/run-meets/${id}/members/${uid}/kick`, { method: 'POST', headers: withAuth(token) }),
  unban: (token: string, id: string, uid: string) =>
    request<{ ok: boolean }>(`/run-meets/${id}/members/${uid}/ban`, { method: 'DELETE', headers: withAuth(token) }),
  // 批次同意：後端一人一交易，回 per-item 結果（名額只剩 3 個卻同意 5 人 → 成功 3 筆、2 筆 409）
  approveBatch: (token: string, id: string, userIDs: string[]) =>
    request<{ approved: number; failed: number; results: { user_id: string; ok: boolean; error?: string }[] }>(
      `/run-meets/${id}/members/approve-batch`,
      { method: 'POST', headers: withAuth(token), body: JSON.stringify({ user_ids: userIDs }) },
    ),
  // 頂層留言，游標分頁（新的在前）；total＝未刪頂層留言數，供「查看全部留言(N)」。
  comments: (token: string, id: string, limit?: number, cursor?: string | null) =>
    request<{ items: RunMeetComment[]; next_cursor: string | null; total: number }>(
      `/run-meets/${id}/comments${runMeetQuery({ limit, cursor: cursor || undefined })}`, { headers: withAuth(token) },
    ),
  // 某頂層留言的回覆，游標分頁（舊的在前，對話由舊到新才讀得順）。
  replies: (token: string, id: string, commentId: string, limit?: number, cursor?: string | null) =>
    request<{ items: RunMeetComment[]; next_cursor: string | null }>(
      `/run-meets/${id}/comments/${commentId}/replies${runMeetQuery({ limit, cursor: cursor || undefined })}`, { headers: withAuth(token) },
    ),
  // parentId 非空＝回覆；只允許一層，parentId 必須指向同團練的頂層留言（後端擋第三層，前端不該送出違規請求，
  // 見 lib/runMeet.ts replyTargetId：對「回覆」再按回覆時，要把 parentId 換成該回覆的 parent_id）。
  addComment: (token: string, id: string, body: string, parentId?: string | null) =>
    request<{ comment: RunMeetComment }>(`/run-meets/${id}/comments`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ body, parent_id: parentId ?? null }),
    }),
  deleteComment: (token: string, id: string, cid: string) =>
    request<{ ok: boolean }>(`/run-meets/${id}/comments/${cid}`, { method: 'DELETE', headers: withAuth(token) }),
  setReaction: (token: string, id: string, kind: RunMeetReactionKind) =>
    request<{ ok: boolean }>(`/run-meets/${id}/reaction`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ kind }) }),
  clearReaction: (token: string, id: string) =>
    request<{ ok: boolean }>(`/run-meets/${id}/reaction`, { method: 'DELETE', headers: withAuth(token) }),
  // 單則留言的表情反應（migration 159；與上面團練層級的 setReaction/clearReaction 是兩張表、兩套端點）。
  // 團練結束後仍可按（只有留言輸入框停用），回傳該留言更新後的完整狀態，直接覆蓋本地即可。
  setCommentReaction: (token: string, id: string, commentId: string, kind: RunMeetReactionKind) =>
    request<{ reactions: { kind: RunMeetReactionKind; count: number }[]; my_reaction: RunMeetReactionKind | null }>(
      `/run-meets/${id}/comments/${commentId}/reaction`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ kind }) },
    ),
  clearCommentReaction: (token: string, id: string, commentId: string) =>
    request<{ reactions: { kind: RunMeetReactionKind; count: number }[]; my_reaction: RunMeetReactionKind | null }>(
      `/run-meets/${id}/comments/${commentId}/reaction`, { method: 'DELETE', headers: withAuth(token) },
    ),
  report: (token: string, id: string, body: { comment_id?: string; reason: string }) =>
    request<{ ok: boolean }>(`/run-meets/${id}/report`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
}

// 後台（RequireAuth → RequireAdmin → Audit → perm('run_meets')）
export const adminRunMeetsApi = {
  list: (token: string, params: { q?: string; status?: RunMeetStatus | ''; owner?: string; hidden?: '1'; include_deleted?: '1'; limit?: number; offset?: number } = {}) =>
    request<{ items: RunMeetAdminRow[]; total: number }>(`/admin/run-meets${runMeetQuery(params)}`, { headers: withAuth(token) }),
  // 後台視角看得到成員層地點（處理檢舉/糾紛需要完整資訊）→ 恆為 RunMeetMemberDetail
  detail: (token: string, id: string) =>
    request<{
      meet: RunMeetMemberDetail
      members: RunMeetMember[]
      pending: RunMeetMember[]
      comments: RunMeetComment[]
      hidden_by_admin: boolean
      hidden_reason: string
    }>(`/admin/run-meets/${id}`, { headers: withAuth(token) }),
  takedown: (token: string, id: string, reason: string) =>
    request<{ ok: boolean }>(`/admin/run-meets/${id}/takedown`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ reason }) }),
  restore: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/run-meets/${id}/restore`, { method: 'POST', headers: withAuth(token) }),
  deleteComment: (token: string, id: string, cid: string) =>
    request<{ ok: boolean }>(`/admin/run-meets/${id}/comments/${cid}`, { method: 'DELETE', headers: withAuth(token) }),
  reports: (token: string, params: { status?: 'pending' | 'handled' | 'dismissed' | ''; limit?: number; offset?: number } = {}) =>
    request<{ items: RunMeetAdminReport[]; total: number }>(`/admin/run-meets/reports${runMeetQuery(params)}`, { headers: withAuth(token) }),
  reviewReport: (token: string, rid: string, body: { status: 'handled' | 'dismissed'; review_note: string }) =>
    request<{ ok: boolean }>(`/admin/run-meets/reports/${rid}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  quota: (token: string, userID: string) =>
    request<{ user_id: string; month: string; cap: number; used: number; remaining: number; is_vip: boolean }>(
      `/admin/run-meets/quota/${userID}`, { headers: withAuth(token) }),
  // ⚠️ 唯一的配額返還管道（close/cancel/delete 一律不回補）；delta −50..50 非零，負=返還。
  adjustQuota: (token: string, userID: string, body: { delta: number; reason: string }) =>
    request<{ ok: boolean; month: string; used: number }>(`/admin/run-meets/quota/${userID}/adjust`, {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
  imageGC: (token: string) =>
    request<{ ok: boolean; deleted: number }>('/admin/run-meets/images/gc', { method: 'POST', headers: withAuth(token) }),
}

// --- 遊戲化角色數值（RO 素質系統，見 internal/rpg・migration 175）---
// 只有 VVIP（users.is_vvip）與白名單管理者看得到；後端同步以 rpg_entry_state/rpg_entry_whitelist
// （一般 appsettings key-value，透過 adminAppSettingsApi 讀寫，見 admin/rpg 頁「入口與 VVIP」分頁）
// 控管，前端一律以 dashboard 的 rpg_entry 為準（'hidden'|'shown'，無 locked 態）。

export type RpgStatKey = 'str' | 'agi' | 'vit' | 'dex' | 'int' | 'luk'
export type RpgStats = Record<RpgStatKey, number>

// RpgConfig 欄位名稱與後端 internal/rpg.Config 完全一致（勿自行改名）；後台「參數設定」分頁逐欄可調，
// 每項係數對應 owner 提供的 RO 素質對照表（見 lib/rpgMeta.ts 的中文標籤/說明）。
export interface RpgConfig {
  initial_stat: number
  initial_free_points: number
  max_stat: number
  cost_base: number
  cost_step_every: number
  default_weapon_type: 'melee' | 'ranged'
  base_hp: number
  hp_per_base_level: number
  base_mp: number
  mp_per_base_level: number
  aspd_base: number
  aspd_per_agi: number
  aspd_per_dex: number
  aspd_cap: number
  weight_base: number
  weight_per_str: number
  str_melee_atk: number
  str_equip_atk_pct: number
  str_ranged_atk_per: number
  agi_flee: number
  agi_def_per: number
  vit_hp_pct: number
  vit_item_hp_pct: number
  vit_def_per: number
  vit_mdef_per: number
  vit_hp_regen_per: number
  hp_regen_per_max_hp: number
  dex_ranged_atk: number
  dex_equip_atk_pct: number
  dex_hit: number
  dex_melee_atk_per: number
  dex_matk_per: number
  dex_mdef_per: number
  int_matk: number
  int_mdef: number
  int_mp_pct: number
  int_item_mp_pct: number
  int_mp_regen_per: number
  int_mp_regen_at_120: number
  int_mp_regen_per_after_120: number
  mp_regen_per_max_mp: number
  luk_crit: number
  luk_atk_per: number
  luk_matk_per: number
  luk_hit_per: number
  luk_flee_per: number
  luk_crit_shield_per: number
  luk_perfect_dodge_per: number
  lv_hit: number
  lv_flee: number
  lv_def_per: number
  lv_atk_per: number
  lv_matk_per: number
  lv_mdef_per: number
  cast_unit_dex: number
  cast_unit_int: number
  cast_pct_per_unit: number
  cast_cap_pct: number
  resist_pct_per_point: number
  flee_cap_pct: number

  // --- DORPG P2：戰鬥內容/縮放/手感參數（契約 dorpg_p2 §3.1，後端 internal/rpg/config.go 同名
  // json tag）。獨立一群方便後台開「戰鬥」分頁，見 lib/rpgMeta.ts CONFIG_GROUPS。---
  battle_scale_mode: 'power' | 'level' | 'fixed'
  battle_mob_hits: number
  battle_mob_def_ratio: number
  battle_enemy_dps_ratio: number
  battle_player_min_atk: number
  battle_player_min_hp: number
  battle_attack_cooldown_ms: number
  battle_charge_min_ms: number
  battle_charge_full_ms: number
  battle_charge_max_multiplier: number
  battle_guard_multiplier: number
  battle_recovery_ms: number
  battle_default_cast_ms: number
  battle_escape_judge_ms: number
  battle_resolve_delay_ms: number
  battle_enemy_act_min_ms: number
  battle_enemy_act_max_ms: number
  battle_ally_act_min_ms: number
  battle_ally_act_max_ms: number
  battle_hit_rate: number
  battle_crit_rate: number
  battle_crit_multiplier: number
  battle_exp_preview_per_level: number

  // --- P2（暴擊／Miss／無效攻擊）新增：全部走這份既有 rpg_config JSON，不新增 migration。
  // json tag 逐字對齊後端 internal/rpg/config.go 與前端 engine/types.ts BattleConfig 的同名欄位
  // （camelCase↔snake_case 轉換規則跟其餘 battle_* 欄位一致）。---
  battle_base_miss_pct: number
  battle_hit_flee_scale: number
  battle_miss_min_pct: number
  battle_miss_max_pct: number
  battle_monster_hit_base: number
  battle_monster_flee_base: number
  battle_monster_crit_pct: number
  battle_monster_crit_shield_base: number
  /** 怪物 attribute（中文）→ 技能 element（英文）→ 倍率；0＝無效攻擊，未列出＝1.0。 */
  battle_element_chart: Record<string, Record<string, number>>

  // --- P3（AGI 攻速／DEX 詠唱縮減，審查 dorpg_p3 r5）新增：全部走這份既有 rpg_config JSON，
  // 不新增 migration。json tag 逐字對齊後端 internal/rpg/config.go 與前端 engine/types.ts
  // BattleConfig 的同名欄位（camelCase↔snake_case 轉換規則同其餘 battle_* 欄位）。
  // battle_monster_hit_base/flee_base 的語意本輪已改（見 rpgMeta.ts 對應說明文字改寫）：
  // 從「絕對基準值」改成「相對玩家等級基線的偏移量」，這兩個新欄位是同一條公式的「每級」係數。---
  battle_monster_hit_per_level: number
  /** 怪物命中的絕對上限：必須小於 flee_cap_pct，否則高等級玩家的 AGI 迴避永遠卡在下限（見 rpgMeta 說明）。 */
  battle_monster_hit_max: number
  battle_monster_flee_per_level: number
  battle_aspd_reference: number
  battle_attack_cooldown_min_ms: number
  battle_cast_min_ms: number

  // --- DORPG P5（職業／測試等級／配點技能規則調整，見契約 dorpg_p5 CONTRACT.md §3/§4/§6）新增：
  // 全部走這份既有 rpg_config JSON，不新增 migration。
  // ⚠️ 這批欄位名稱是 FRONTEND 依 CONTRACT.md 文字敘述擬定（後端尚未落地時無法逐字核對 internal/
  // rpg/config.go 的 json tag）——整合時請以 BACKEND 實際欄位名為準，若不同只需在這裡＋
  // lib/rpgMeta.ts CONFIG_GROUPS 同步改名，ConfigTab 是完全依 CONFIG_GROUPS 驅動的通用表單，
  // 不必再改 admin/rpg/page.tsx。---
  /** 配點公式初始值 TotalStatPoints(1)（契約預設 48）。 */
  stat_points_initial: number
  /** 配點公式：floor((k−1)/此值) 每幾級遞增一階（契約預設 5）。 */
  stat_points_step_levels: number
  /** 配點公式：每級基礎點數（契約預設 3）。 */
  stat_points_per_level_base: number
  /** 技能點公式初始值 TotalSkillPoints(1)（契約預設 0）。 */
  skill_points_initial: number
  /** 技能點公式：每級技能點（契約預設 1）。 */
  skill_points_per_level: number
  /** 暴擊傷害浮動倍率下界，取代固定的 battle_crit_multiplier（契約預設 1.75）。
   *  ⚠️ INTEGRATOR 對齊：json tag 依 internal/rpg/config.go 實際欄位為準，不加 battle_ 前綴。 */
  crit_mult_min: number
  /** 暴擊傷害浮動倍率上界（契約預設 2.25）。同上，對齊 config.go 的 crit_mult_max。 */
  crit_mult_max: number
  /** 技能屬性命中怪物 weak_elements 時的傷害加成 %（契約預設 25）。 */
  battle_weakness_bonus_pct: number
  /** STR 整十階梯：atk += floor(STR/10)² × 此值（契約預設 1，全職業統一，弓箭手也吃 STR）。 */
  str_tier_coef: number
  /** INT 整十階梯：matk += floor(INT/10)² × 此值（契約預設 1）。 */
  int_tier_coef: number
  /** 等級→防禦二段線性的轉折等級（契約預設 50，取代現行 floor(L/2)）。 */
  lv_def_breakpoint: number
  /** 轉折等級（含）以下，每級防禦加成（契約預設 0.5）。 */
  lv_def_per_low: number
  /** 轉折等級以上，每級防禦加成（契約預設 0.35）。 */
  lv_def_per_high: number
  /**
   * 審查#2 修正新增：測試等級功能的獨立總開關（正式上線前關閉）。過去 test_level 只靠
   * requireEntry 白名單擋，白名單放寬後任何在白名單內的人都能把自己等級設成 99，沒有第二道
   * 閘門。關閉後 PUT /rpg/test-level 一律 403（error:"test_level_disabled"），且既有 test_level
   * 也會被後端忽略（不是只擋新的寫入請求）。預設 true。對齊後端 internal/rpg/config.go 同名
   * json tag（test_level_enabled）。
   */
  test_level_enabled: boolean

  // --- DORPG P6（怪物等級制，見契約 dorpg_p6 CONTRACT.md §2）新增：battle_scale_mode 從本輪起
  // 預設改 "level"；怪物 HP/ATK/DEF/MDEF 改用「參考玩家 RefPlayer(N)」乘這四個比例算出，取代 P2
  // power 模式的「依玩家戰力動態縮放」。全部走既有 rpg_config JSON，不新增 migration 欄位以外的表。
  // ⚠️ 同 P5 的整合警語：json tag 依 internal/rpg/config.go 實際欄位為準，這裡是依契約文字擬定。---
  /** 怪物 HP 相對 RefPlayer(N).HPMax 的比例（契約預設 1.0）。 */
  battle_lvl_hp_ratio: number
  /** 怪物 ATK 相對 RefPlayer(N).ATK 的比例（契約預設 1.0）。 */
  battle_lvl_atk_ratio: number
  /** 怪物 DEF 相對 RefPlayer(N).DEF 的比例（契約預設 1.0）。 */
  battle_lvl_def_ratio: number
  /** 怪物 MDEF 相對 RefPlayer(N).MDEF 的比例（契約預設 1.0）。 */
  battle_lvl_mdef_ratio: number

  // --- DORPG P7（武器系統，見契約 dorpg_p7 CONTRACT.md §1、WIRE §REST）新增：屬性相剋倍率的
  // 三個全域係數（管理者 battle_element_chart 個別覆寫仍最優先，見該欄位說明）。全部走既有
  // rpg_config JSON，不新增 migration。⚠️ 同 P5/P6 的整合警語：json tag 依 internal/rpg/config.go
  // 實際欄位為準，這裡是依契約文字擬定。---
  /** 攻擊屬性剋制怪物屬性時的倍率加成 %（契約預設 25，即 ×1.25）。 */
  battle_element_advantage_pct: number
  /** 攻擊屬性被怪物屬性剋制時的倍率減損 %（契約預設 −25，即 ×0.75）。 */
  battle_element_disadvantage_pct: number
  /** 攻擊屬性與怪物屬性相同時的倍率減損 %（契約預設 −25，即 ×0.75）。 */
  battle_element_same_pct: number
}

export interface RpgDerived {
  atk: number
  def: number
  matk: number
  mdef: number
  hit: number
  flee: number
  perfect_dodge: number
  crit_pct: number
  crit_shield: number
  aspd: number
  weight: number
  hp_regen: number
  mp_regen: number
  cast_reduction_pct: number
  resists: Record<string, number> // key 名稱由後端決定（狀態異常代碼），前端以 lib/rpgMeta.ts RESIST_LABEL 轉中文、未對到的 key 原樣顯示
}

// --- DORPG P5：六職業（見契約 dorpg_p5 CONTRACT.md §1，id 固定不隨語系/顯示名變動）---
export type RpgJobId = 'light_knight' | 'archer' | 'heavy_knight' | 'cleric' | 'merchant' | 'mage'
export interface JobPathDTO {
  id: string
  name: string
  desc: string
}
// DORPG P10（CONTRACT §1/§4、WIRE §REST）：依 key 排序的路線陣列——只有 heavy_knight 會有第三個
// 元素（key='c'，「守護」路線）。CharacterScreen/TavernScreen 的技能路線畫面改用這個陣列通用渲染
// （見任務決策：路線 UI 一律「有 path_c 才多畫一欄」），不再各自手寫 path_a/path_b 兩份。
export interface JobPathKeyedDTO {
  id: string
  key: 'a' | 'b' | 'c'
  name: string
  desc: string
}
export interface JobDTO {
  id: string
  name: string
  tagline: string
  description: string
  path_a: JobPathDTO
  path_b: JobPathDTO
  // DORPG P10：後端用 *JobPathRow（omitempty）——只有 heavy_knight 有值，其餘職業這個鍵在 JSON
  // 裡直接不存在（不是 null），故這裡型別是「選填」而非「可為 null」。CharacterScreen/TavernScreen
  // 一律改讀下面的 paths 陣列，不直接用這個欄位（留著只為型別對後端誠實）。
  path_c?: JobPathDTO
  // DORPG P10 新增，見上方 JobPathKeyedDTO 型別註解；既有 path_a/path_b 兩個巢狀欄位維持不變
  // （相容既有呼叫端，WIRE：「既有欄位保留」）。
  paths: JobPathKeyedDTO[]
  // DORPG P10（CONTRACT §1/§3）：職業天生特性，目前只有 heavy_knight 有 `damage_taken_pct: -15`，
  // 其餘職業缺省 {}。已經由 bootstrap 合併進戰鬥 equipmentEffects.damageTakenPct（見下方
  // RpgBootstrapPartyMemberRaw.jobTraits 型別註解），這裡純供角色頁顯示「職業特性：...」一行，
  // 不參與任何前端計算。
  traits: { damage_taken_pct?: number }
  weapon: 'sword' | 'staff' | 'bow' | 'greatsword'
  atk_branch: 'melee' | 'ranged'
  recommended_stats: string
  sort_order: number
}

export interface RpgCharacter {
  base_level: number
  job_level: number
  job_exp: number
  stats: RpgStats
  free_points: number // P5：現為推導值，等同 stat_points_free（契約 §3：free_points 欄位不再是真相，僅為相容舊碼保留）
  next_cost: Partial<RpgStats> // 缺該項鍵＝已達上限（stat_cap），前端據此關閉該項 +1/Max
  max_hp: number
  max_mp: number
  derived: RpgDerived
  // --- P5 新增（見契約 §1-§4、WIRE §會員端 REST GET /rpg/me） ---
  job: JobDTO | null
  test_level: number | null // 1–99；null＝使用真實等級（測試用，不影響真實資料）
  effective_level: number // test_level ?? base_level；配點/技能點/上限一律用這個
  stat_points_total: number
  stat_points_free: number
  stat_cap: number // = min(max_stat, effective_level)
  skill_points_total: number
  skill_points_free: number
  // --- DORPG P7（見契約 §2/§3、WIRE §REST「/rpg/me 新增 weapon」）：目前裝備的武器（未裝備＝
  // null）。衍生值（上面的 derived/max_hp/max_mp）已經套用武器 WeaponProfile 的效果，這個欄位
  // 只是給畫面顯示「目前武器是哪一件」用，不需要前端自己再疊加一次數值。WeaponDTO 定義在本檔
  // 下方 P7 專節（TS interface 宣告順序不影響型別檢查，故可以先在這裡引用）。 ---
  weapon: WeaponDTO | null
  // --- DORPG P8（見契約 §4、WIRE §REST「/rpg/me 新增 equipment 與 equip_bonus」）：八格裝備各自
  // 只送「item 名稱或 null」（不是完整 DTO——完整資料要看 EquipmentScreen 才需要，角色頁只顯示
  // 摘要），衍生值已含全部裝備（含 P7 武器＋本輪防具/飾品）。equip_bonus 是彙總後的加成明細，供
  // 角色頁顯示「DEF+n、VIT+n…」摘要（見 rpgMeta.ts formatEquipBonus()）。ArmorDTO/EquipBonusDTO
  // 定義在本檔下方 P8 專節（TS interface 宣告順序不影響型別檢查）。 ---
  equipment: RpgEquipmentNamesDTO
  equip_bonus: EquipBonusDTO
  // --- DORPG P9（見契約 §1/§4、WIRE §REST「/rpg/me 新增 auto_battle」）：玩家自動戰鬥開關＋
  // 目前套用的 AI 策略，開關與策略持久化在玩家角色列，戰鬥 HUD 可隨時切換（PUT /rpg/auto-battle）。
  auto_battle: AutoBattleDTO
}

export interface RpgMe {
  enabled: boolean
  character?: RpgCharacter // enabled=false 時省略（未達入口資格）
}

// --- DORPG P5：技能效果詞彙（見契約 §5、WIRE §引擎（TS）要吃的新欄位）---
// DORPG P10（CONTRACT §3/§5）新增 'taunt'（重騎士守護系列：挑釁／守護姿態）。
export type SkillKind = 'damage' | 'heal' | 'shield' | 'buff' | 'debuff' | 'passive' | 'special' | 'taunt'
export type SkillDmgType = 'physical' | 'magic'
export type SkillTarget = 'enemy' | 'allEnemies' | 'self' | 'ally' | 'allAllies'
// 怪物屬性桶（DOR 8 桶英文代碼，同 apps/web/src/lib/dorpg/types.ts ElementKind，這裡不 import
// 該檔避免跨角色檔案耦合——兩邊字面量集合須保持一致，改動時兩處都要同步）。
export type RpgElement = 'metal' | 'wood' | 'water' | 'fire' | 'earth' | 'light' | 'dark' | 'neutral'

// DORPG P11（契約 dorpg_p11 CONTRACT.md §1/§2、WIRE.md）：怪物強度九級，由弱到強固定 9 個字面值，
// 對照新表 rpg_monster_ranks 的主鍵（既有 rpg_monsters.rank 的 A–E 值皆是這九值的子集，本輪加 FK 後
// 收斂成同一份詞彙表）。SA/SS＝特A／特S（後端/DB 用純 ASCII 代碼，中文顯示交給 rpgMeta.ts RANK_LABEL）。
export type RpgRank = 'F' | 'E' | 'D' | 'C' | 'B' | 'A' | 'SA' | 'S' | 'SS'

/** 已依技能等級展開成即時數值的效果快照（WIRE：base+per_level×(lv−1)）。各 kind 只會用到其中一部分欄位。 */
export interface EffectAtLevel {
  kind: SkillKind
  stat?: string // buff/debuff/passive 用；集合見契約 §5（atk_pct/matk_pct/def_pct/...）
  value?: number // buff/debuff/passive 用
  duration_ms?: number // buff/debuff 用
  coef?: number // damage/heal 用
  flat?: number // damage/heal/shield 用
  hits?: number // damage 用，>1 表示多段
  target: SkillTarget
  mp_cost: number
  // DORPG P10（WIRE §REST：「passive 展開新增 guard_taunt: boolean」）：kind=passive 專用，只有
  // 「守護本能」（hk_c2）這個被動有 true——學到 ≥1 級時，玩家按防禦（GUARD）期間即進入守護狀態
  // （見契約 §3 inGuardianState）。其餘 passive 缺省 false／undefined，不影響既有顯示。
  guard_taunt?: boolean
  // DORPG P10：kind=taunt 專用，後端同一份展開結果也會鏡射在這兩個扁平欄位上（跟 SkillDTO.taunt
  // 是同一份資料的兩種殼，見後端 skills.go EffectAtLevel 註解）；本檔的顯示邏輯一律讀 SkillDTO.taunt
  // （見 formatTauntEffect），這裡純粹補齊型別讓 EffectAtLevel 對後端誠實，不是另一份資料來源。
  retarget?: boolean
  damage_taken_pct?: number
}

/** GET /rpg/skills 單一技能列（含目前職業已配等級與可升條件）。 */
export interface SkillDTO {
  id: string
  name: string
  path: 'a' | 'b' | 'c' // DORPG P10：'c' 目前只有 heavy_knight（守護路線）會出現
  tier: number
  kind: SkillKind
  dmg_type?: SkillDmgType // damage 才有意義
  element?: RpgElement // damage 才有意義
  target: SkillTarget
  max_level: number
  level: number // 目前已配等級，0＝尚未學習
  prereq_skill_id?: string | null
  prereq_level?: number | null
  prereq_ok: boolean
  can_level_up: boolean
  mp_cost: number
  mp_cost_per_level: number
  cooldown_ms: number
  cast_ms: number
  display_text: string
  implemented: boolean // false＝本輪引擎未實裝（special 類），可配點但戰鬥中不可用
  effect_at_level: EffectAtLevel // level=0 時用 lv=1 的數值預覽
  effect_next_level: EffectAtLevel | null // 已達 max_level 時為 null
  lv_preview: { '1': string; '5': string; '10': string }
  // DORPG P10（CONTRACT §3/§4、WIRE §REST）：kind='taunt' 專用的展開值——WIRE 明講是額外一個
  // `taunt` 物件（不是塞進上面 effect_at_level 既有欄位），CharacterScreen/TavernScreen 的技能列
  // 在 kind==='taunt' 時改讀這裡顯示持續時間／減傷／是否拉怪（見 rpgMeta.ts formatTauntEffect）。
  // 非 taunt 技能沒有這個欄位。
  taunt?: { duration_ms: number; damage_taken_pct: number; retarget: boolean }
}

export interface RpgSkillsResponse {
  job: JobDTO | null
  skills: SkillDTO[] // 未選職業＝空陣列
  skill_points_total: number
  skill_points_free: number
}

/** DORPG P11（WIRE §REST：「GET /rpg/ranks → { ranks: RankDTO[] }」）：玩家端可查詢的九級強度說明
 *  （不含 summon／level_curve 這種後台調參用的內部欄位）——目前 FRONTEND 這輪主要靠 EncounterPicker
 *  卡片自帶的 rank_label/badge_color 顯示，這支端點留給未來「強度說明」畫面或角色頁引用。 */
export interface RpgRankDTO {
  rank: RpgRank
  label: string
  sort_order: number
  hp_mult: number
  atk_mult: number
  def_mult: number
  mdef_mult: number
  description: string
  badge_color: string
}

export const rpgApi = {
  me: (token: string) => request<RpgMe>('/rpg/me', { headers: withAuth(token) }),
  // P5：六職業清單（含路線 A/B 說明、武器、物攻分支）；EncounterPicker 簡潔切換列與角色頁完整
  // 卡片共用同一份資料，各自只取用需要的欄位。
  jobs: (token: string) => request<{ jobs: JobDTO[] }>('/rpg/jobs', { headers: withAuth(token) }),
  // job_id: null＝清除職業選擇（回到未選職業狀態，沿用全域預設武器、無職業技能）。
  setJob: (token: string, jobId: string | null) =>
    request<RpgMe>('/rpg/job', { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ job_id: jobId }) }),
  // level: null＝使用真實等級（清除測試等級）。
  setTestLevel: (token: string, level: number | null) =>
    request<RpgMe>('/rpg/test-level', { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ level }) }),
  // P5：points 與 mode 二選一——mode='max' 由伺服器反覆 +1 直到點數不夠或達上限（契約 §3）。
  allocate: (token: string, body: { stat: RpgStatKey; points?: number; mode?: 'max' }) =>
    request<RpgMe>('/rpg/allocate', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  // 六圍全回 initial，寫 6 筆負值 player_stat_log（契約 §3）。
  resetStats: (token: string) => request<RpgMe>('/rpg/stats/reset', { method: 'POST', headers: withAuth(token) }),
  // P5：技能（契約 §4）。未選職業時 skills 為空陣列。
  skills: (token: string) => request<RpgSkillsResponse>('/rpg/skills', { headers: withAuth(token) }),
  allocateSkill: (token: string, body: { skill_id: string; delta: 1 | -1 | 'max' }) =>
    request<RpgSkillsResponse>('/rpg/skills/allocate', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  resetSkills: (token: string) => request<RpgSkillsResponse>('/rpg/skills/reset', { method: 'POST', headers: withAuth(token) }),
  // DORPG P9（契約 §1/§4、WIRE §REST）：玩家自動戰鬥開關＋策略，持久化在玩家角色列；戰鬥 HUD
  // 的 AutoBattleBar 切換時打這支（本地先靠引擎 SET_AUTO_BATTLE 即時生效，這支只負責存檔，
  // 失敗只 toast、不回滾本地——見任務 §3）。錯誤碼 unknown_strategy（呼叫端自行組中文文案）。
  setAutoBattle: (token: string, body: AutoBattleDTO) =>
    request<AutoBattleDTO>('/rpg/auto-battle', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  // DORPG P11（WIRE §REST）：九級強度說明表（見 RpgRankDTO 型別註解）。
  ranks: (token: string) => request<{ ranks: RpgRankDTO[] }>('/rpg/ranks', { headers: withAuth(token) }),
}

/** DORPG P9（WIRE §REST）：PUT /rpg/auto-battle body／回應，與 /rpg/me 的 auto_battle 欄位同形。 */
export interface AutoBattleDTO {
  enabled: boolean
  strategy_id: string
}

// --- DORPG P6：酒館／隊伍／傭兵腳本（見契約 dorpg_p6 CONTRACT.md §3、WIRE.md §REST）---
// PresetDTO.stats 沿用既有 RpgStats（{str,agi,vit,dex,int,luk}）；skill_levels 是 {skill_id: level}
// 的純資料 map（跟玩家 /rpg/skills 回傳的完整 SkillDTO[] 不同形狀——腳本要整包存進
// rpg_companion_presets.skill_levels JSONB，草稿用最精簡表示法即可，顯示用的完整 SkillDTO[]
// 交給 validate 端點另外算給你，見下面 PresetValidateResponse.skills）。

/** WIRE §REST DerivedDTO：腳本套用該傭兵倍率後的最終衍生值（整數，含 crit_pct）。 */
export interface PresetDerivedDTO {
  hp_max: number
  mp_max: number
  atk: number
  matk: number
  def: number
  mdef: number
  hit: number
  flee: number
  aspd: number
  crit_pct: number
}

/** WIRE §REST PresetDTO：一組傭兵腳本（系統預設或玩家自訂）。 */
export interface CompanionPresetDTO {
  id: string
  companion_id: string
  name: string
  level: number
  /** true＝系統預設，唯讀（契約 §3.2：只能「另存新腳本」，不能覆蓋／刪除）。 */
  is_system: boolean
  stats: RpgStats
  skill_levels: Record<string, number>
  derived: PresetDerivedDTO
  stat_points_total: number
  stat_points_free: number
  stat_cap: number
  skill_points_total: number
  skill_points_free: number
  // --- DORPG P9（契約 §2、WIRE §REST）：傭兵裝備比照玩家，掛在腳本上 ---
  /** 八格裝備（同 P8 GET /rpg/equipment 的 equipped 形狀：各格 WeaponDTO|ArmorDTO|null）。
   *  EquippedGearDTO/WeaponDTO/ArmorDTO 定義在本檔下方 P7/P8 專節（TS interface 宣告順序不影響型別檢查）。 */
  equipment: EquippedGearDTO
  /** 彙總後（含武器）的裝備加成，供腳本編輯器顯示「DEF+n、VIT+n…」摘要（同 CharacterScreen 的 formatEquipBonus）。 */
  equip_bonus: EquipBonusDTO
  /** 這份腳本套用的 AI 策略 id（見上方 StrategyDTO），預設 'balanced'。 */
  strategy_id: string
}

/** WIRE §REST MercenaryDTO：酒館下段「傭兵」四張卡；presets 系統預設在前、使用者自訂在後。 */
export interface MercenaryDTO {
  id: string
  name: string
  portrait_id: string
  role: string
  job: JobDTO
  in_party: boolean
  presets: CompanionPresetDTO[]
}

/** WIRE §REST PartySlotDTO：隊伍 4 格其中一格；companion_id=null＝空格（顯示「空」）。 */
export interface PartySlotDTO {
  slot: number // 1..4
  companion_id: string | null
  preset_id: string | null
  preset_name: string | null
  level: number | null
}

/** WIRE §REST GET /rpg/tavern 的 leader 子物件——玩家本人（隊長），不是傭兵。 */
export interface TavernLeaderDTO {
  job: JobDTO | null
  effective_level: number
  name: string
}

// --- DORPG P9：AI 戰鬥策略（見契約 dorpg_p9 CONTRACT.md §4、WIRE.md §REST）---
// id 集合固定六種（balanced／mp_conserve／skill_aggressive／protect_allies／focus_fire／
// element_advantage，引擎 registry 白名單），資料庫只管顯示名稱/說明/參數覆寫/啟用/排序，
// 故 StrategyDTO 沒有任何「行為」欄位——實際決策邏輯全在 ENGINE 的 engine/strategies.ts。
export interface StrategyDTO {
  id: string
  name: string
  description: string
  params: Record<string, unknown>
  sort_order: number
}

export interface TavernResponse {
  leader: TavernLeaderDTO
  party: PartySlotDTO[] // 固定 4 格（沒有任何列＝預設隊伍＝小咪＋其系統預設腳本，契約 §3.1）
  mercenaries: MercenaryDTO[] // 固定 4 位（小咪／小優／阿光／阿深）
  /** DORPG P9（WIRE §REST）：只含 is_active，依 sort_order——腳本編輯器「AI 策略」下拉的資料來源。 */
  strategies: StrategyDTO[]
}

/** PUT /rpg/party body 的單格；只送「有人」的格子即可——伺服器整批覆蓋 4 格（契約 §3.3）。 */
export interface PartySlotInput {
  slot: number
  companion_id: string
  preset_id: string | null
}

/**
 * DORPG P9（WIRE §REST）：腳本的八格裝備輸入——各格 item_id 或 null（卸下），缺鍵＝維持空。
 * 型別上用 EquipmentSlot 索引（八個 key 恰好對齊 weapon/helmet/gloves/armor/legs/boots/
 * accessory1/accessory2，同 P8 EquippedGearDTO 的既有慣例），全部 optional——草稿可以只送
 * 有變動的格子，未提到的格子後端維持原樣（比照 PUT /rpg/party「只送有人的格子」的既有慣例）。
 */
export type PresetEquipmentInput = { [K in EquipmentSlot]?: string | null }

/** POST /rpg/presets、PUT /rpg/presets/{id} 共用的 body 形狀。 */
export interface PresetSaveBody {
  companion_id: string
  name: string
  level: number
  stats: RpgStats
  skill_levels: Record<string, number>
  /** DORPG P9（契約 §2、WIRE §REST）：八格裝備。 */
  equipment: PresetEquipmentInput
  /** DORPG P9：這份腳本套用的 AI 策略 id。 */
  strategy_id: string
}

/** POST /rpg/presets/validate body——沒有 name（草稿試算不需要，見 WIRE 逐字列出的欄位）。 */
export interface PresetValidateBody {
  companion_id: string
  level: number
  stats: RpgStats
  skill_levels: Record<string, number>
  equipment: PresetEquipmentInput
  strategy_id: string
}

/** WIRE §REST 驗證錯誤項；message 是後端組好的備援文案，UI 找不到 code 對照表時可以直接顯示。 */
export interface PresetValidateError {
  field: string
  code: string
  message: string
}

export interface PresetValidateResponse {
  ok: boolean
  // 審查【CRITICAL】：型別誠實反映後端可能的原始回應——Go 的 nil slice 經 encoding/json 編碼會是
  // null（若後端該處的初始化又被改回 var errs []PresetError，這裡的型別能在編譯期提醒要處理 null，
  // 不會靠巧合矇混過關）；rpgTavernApi.validatePreset() 已在下方統一正規化成陣列，一般呼叫端
  // 仍應維持安全存取（result?.errors ?? []）以防禦這個型別本身允許的 null。
  errors: PresetValidateError[] | null
  stat_points_total: number
  stat_points_free: number
  stat_cap: number
  skill_points_total: number
  skill_points_free: number
  derived: PresetDerivedDTO
  /** 該傭兵職業的 10 個技能，已依草稿 skill_levels 展開（與 /rpg/skills 的 SkillDTO 同形）。 */
  skills: SkillDTO[]
  /** DORPG P9（WIRE §REST）：這次草稿套用裝備後的加成彙總（含武器），derived 已經套用，這欄只供顯示摘要。 */
  equip_bonus: EquipBonusDTO
}

/**
 * DORPG P9（WIRE §REST）：GET /rpg/tavern/gear?job_id=&level= 回應——該職業全部武器＋該職業
 * 防具＋通用飾品，can_equip 依傳入的 level 計算；⚠️ 契約明講「equipped／equipped_in 恆
 * false／null——腳本編輯器自己比對」，呼叫端要拿目前草稿的 equipment 跟這份清單的 id 自行比對
 * 標記已裝備／已裝於哪格（見 TavernScreen PresetEditor 的 gear 相關 useMemo）。
 */
export interface TavernGearResponse {
  weapon_types: WeaponTypeDTO[]
  weapons: WeaponDTO[]
  armor_items: ArmorDTO[]
}

export const rpgTavernApi = {
  get: (token: string) => request<TavernResponse>('/rpg/tavern', { headers: withAuth(token) }),
  setParty: (token: string, slots: PartySlotInput[]) =>
    request<TavernResponse>('/rpg/party', { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ slots }) }),
  createPreset: (token: string, body: PresetSaveBody) =>
    request<CompanionPresetDTO>('/rpg/presets', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  updatePreset: (token: string, id: string, body: PresetSaveBody) =>
    request<CompanionPresetDTO>(`/rpg/presets/${encodeURIComponent(id)}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  deletePreset: (token: string, id: string) =>
    request<{ ok: boolean }>(`/rpg/presets/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),
  // 草稿試算——不存檔，debounce 呼叫用（契約 §3.4：即時衍生值預覽經 validate 端點）。
  // 審查【CRITICAL】：後端合法草稿一律回 errors:[]（見 presets.go ValidatePreset），但這裡仍在
  // client 端正規化成陣列，防禦後端行為將來又漂移回 null、也讓呼叫端不必每處都寫 ?? []。
  validatePreset: (token: string, body: PresetValidateBody) =>
    request<PresetValidateResponse>('/rpg/presets/validate', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) })
      .then((r) => ({ ...r, errors: r.errors ?? [] })),
  // DORPG P9：腳本編輯器「裝備」區的清單來源，依職業＋草稿等級查詢。
  gear: (token: string, jobId: string, level: number) =>
    request<TavernGearResponse>(`/rpg/tavern/gear?job_id=${encodeURIComponent(jobId)}&level=${encodeURIComponent(String(level))}`, { headers: withAuth(token) }),
}

// --- DORPG P7：武器裝備（見契約 dorpg_p7 CONTRACT.md §2/§3、WIRE.md §REST）---
// WeaponProfile 是契約 §3 的「引擎詞彙」JSON，缺省欄位＝中性值（後端 omitempty，前端一律用 ?? 0／
// ?? 'neutral' 讀取，不假設每個欄位都存在）。這份 snake_case 形狀同時是 GET /rpg/equipment、
// GET /rpg/me 的 weapon.profile，也是後台 rpg_weapons.profile 編輯器（adminRpgApi）要吃的形狀，
// 兩處共用同一個型別，欄位改名只需要改這裡一處。
export interface WeaponSizeBonus {
  small?: number
  medium?: number
  large?: number
}
export interface WeaponProfile {
  atk?: number
  matk?: number
  int_bonus?: number
  mp_pct?: number
  atk_pct?: number
  matk_pct?: number
  def_pct?: number
  mdef_pct?: number
  hits?: number
  hit_mul?: number
  extra_hit_chance_pct?: number
  interval_pct?: number
  charge_time_mul?: number
  charge_dmg_mul?: number
  splash_pct?: number
  size_bonus?: WeaponSizeBonus
  crit_pct?: number
  crit_dmg_pct?: number
  flee_bonus?: number
  element_resist_pct?: number
  magic_skill_pct?: number
  element?: RpgElement
}

export type WeaponRarity = 'common' | 'rare' | 'epic' | 'legendary'

/** WIRE §REST WeaponTypeDTO：該職業其中一種武器類型（如「單手劍」），traits 是型別層固定特性
 *  （多數鍵純顯示／設計依據，engine 不直接讀這個物件——實際效果數值全部在每件武器各自的 profile；
 *  例外是 DORPG P12 新增的 row_bonus_front_pct／row_bonus_rear_pct／pierce_chance_pct／
 *  pierce_dmg_pct 四鍵，後端 buildPlayerWeaponWire 會把它們合併進武器 profile 的同名四欄
 *  （見 RpgBootstrapWeaponProfileRaw），這四鍵因此「真的」影響戰鬥計算，見契約 dorpg_p12
 *  CONTRACT.md §1/§3、rpgMeta.ts formatWeaponTypeRowBonus）。 */
export interface WeaponTypeDTO {
  id: string
  job_id: string
  name: string
  visual: 'sword' | 'staff' | 'bow' | 'greatsword'
  elemental_capable: boolean
  description: string
  traits: Record<string, unknown>
  sort_order: number
}

/** WIRE §REST WeaponDTO：該類型其中一級武器；can_equip／equipped 是後端依「目前職業＋有效等級」
 *  算好的旗標，前端不必自己重算門檻判斷（見契約 §2「唯一條件＝有效等級 ≥ level_req」）。 */
export interface WeaponDTO {
  id: string
  type_id: string
  tier: number
  name: string
  rarity: WeaponRarity
  level_req: number
  element: RpgElement
  profile: WeaponProfile
  description: string
  can_equip: boolean
  equipped: boolean
}

// --- DORPG P8：防具／飾品裝備（見契約 dorpg_p8 CONTRACT.md §1/§2、WIRE.md §REST）---
// ArmorProfile 是契約 §2 的「引擎詞彙」JSON，缺省欄位＝中性值（同 WeaponProfile 的既有慣例，
// 見上方檔頭說明）。防具（helmet/gloves/armor/legs/boots）只用得到 def 與少數副屬性；飾品
// （accessory）恆不用 def，且契約設計成「每件飾品恰好只有一個非零效果欄位」——這個特性被
// rpgMeta.ts accessoryEffectKey() 拿來把 90 件飾品分成 18 組，不必依賴 id 命名慣例（seed 由
// 另一個 workflow 產生，id/name 措辭不受這裡控制，只有 profile 的欄位語意受契約保證）。
export interface ArmorProfile {
  def?: number
  str?: number
  agi?: number
  vit?: number
  dex?: number
  int?: number
  luk?: number
  hp_pct?: number
  mp_pct?: number
  atk_pct?: number
  matk_pct?: number
  interval_pct?: number // 負＝攻擊間隔縮短（與武器 interval_pct 相加）
  crit_pct?: number
  crit_dmg_pct?: number
  mp_cost_reduce_pct?: number
  hp_regen_pct_per_5s?: number
  mp_regen_pct_per_5s?: number
  damage_taken_pct?: number // 負＝少受傷（與 buff 的 damage_taken_pct 相加）
  element_resist_pct?: number // 與武器（鍊）相加
}

/** 防具品項本身固定占用的格子（helmet/gloves/armor/legs/boots 各自唯一對應一個裝備格；
 *  accessory 則可以裝到 accessory1 或 accessory2 兩格其中之一，見 EquipmentSlot）。 */
export type ArmorItemSlot = 'helmet' | 'gloves' | 'armor' | 'legs' | 'boots' | 'accessory'
/** PUT /rpg/equipment/{slot} 可接受的防具格 slot（不含 'weapon'——武器走既有 setWeapon）。 */
export type ArmorEquipSlot = 'helmet' | 'gloves' | 'armor' | 'legs' | 'boots' | 'accessory1' | 'accessory2'
/** 八格裝備欄的格子名（EquipmentScreen 頂部裝備欄、/rpg/me equipment 摘要共用）。 */
export type EquipmentSlot = 'weapon' | ArmorEquipSlot

/** WIRE §REST ArmorDTO：某個部位／飾品其中一級防具；can_equip 同 WeaponDTO 慣例——後端依「目前
 *  職業＋有效等級」算好的旗標，前端不必自己重算門檻判斷。equipped_in＝實際佔用的格子名（飾品
 *  可能裝在 accessory1 或 accessory2 其中之一；未裝備＝null），跟 WeaponDTO 的單純布林 equipped
 *  不同，因為同一件飾品理論上可以出現在兩個不同格子的清單裡（accessory1/2 清單其實是同一份
 *  armor_items，只是目標格子不同）。 */
export interface ArmorDTO {
  id: string
  job_id: string | null // null＝通用（僅飾品）
  slot: ArmorItemSlot
  tier: number
  name: string
  rarity: WeaponRarity // 稀有度沿用武器同一組 common/rare/epic/legendary 詞彙（契約 §1）
  level_req: number
  profile: ArmorProfile
  description: string
  can_equip: boolean
  equipped_in: EquipmentSlot | null
}

/** WIRE §REST EquipBonusDTO：彙總後（含武器）的裝備加成，供 /rpg/equipment 與 /rpg/me 共用。 */
export interface EquipBonusDTO {
  str: number
  agi: number
  vit: number
  dex: number
  int: number
  luk: number
  def: number
  hp_pct: number
  mp_pct: number
  atk_pct: number
  matk_pct: number
  interval_pct: number
  crit_pct: number
  crit_dmg_pct: number
  mp_cost_reduce_pct: number
  hp_regen_pct_per_5s: number
  mp_regen_pct_per_5s: number
  damage_taken_pct: number
  element_resist_pct: number
}

/** GET /rpg/equipment 的 equipped 八格（契約 §4）。 */
export interface EquippedGearDTO {
  weapon: WeaponDTO | null
  helmet: ArmorDTO | null
  gloves: ArmorDTO | null
  armor: ArmorDTO | null
  legs: ArmorDTO | null
  boots: ArmorDTO | null
  accessory1: ArmorDTO | null
  accessory2: ArmorDTO | null
}

/** /rpg/me 的 equipment 摘要（WIRE：「各格 item 名稱或 null」——跟 EquippedGearDTO 不同形狀，
 *  角色頁只需要顯示名字，不需要完整 DTO）。 */
export interface RpgEquipmentNamesDTO {
  weapon: string | null
  helmet: string | null
  gloves: string | null
  armor: string | null
  legs: string | null
  boots: string | null
  accessory1: string | null
  accessory2: string | null
}

/** GET /rpg/equipment、PUT /rpg/equipment/{slot} 共用回應形狀（WIRE §REST）。weapons／weapon_types
 *  只反映「目前職業」；未選職業時兩者皆為空陣列（契約 §2）。armor_items＝目前職業的 50 件防具
 *  ＋90 件通用飾品（未選職業→只有飾品，契約 §4）。 */
export interface RpgEquipmentResponse {
  job: JobDTO | null
  effective_level: number
  equipped: EquippedGearDTO
  weapon_types: WeaponTypeDTO[]
  weapons: WeaponDTO[]
  armor_items: ArmorDTO[]
  equip_bonus: EquipBonusDTO
}

// PUT /rpg/equipment/weapon、PUT /rpg/equipment/{防具格} 400 錯誤碼（WIRE §REST）：
// not_found／wrong_job／wrong_slot／level_too_low／duplicate_accessory——呼叫端（EquipmentScreen）
// 比照其餘 rpg* API 的 friendlyErr() 慣例自行對照中文文案。
export const rpgEquipmentApi = {
  get: (token: string) => request<RpgEquipmentResponse>('/rpg/equipment', { headers: withAuth(token) }),
  setWeapon: (token: string, itemId: string | null) =>
    request<RpgEquipmentResponse>('/rpg/equipment/weapon', { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ item_id: itemId }) }),
  // P8（契約 §4）：五部位防具＋兩飾品格共用同一個端點形狀，slot 由呼叫端傳入（不含 'weapon'，
  // 武器一律走上面的 setWeapon）。
  setSlot: (token: string, slot: ArmorEquipSlot, itemId: string | null) =>
    request<RpgEquipmentResponse>(`/rpg/equipment/${slot}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ item_id: itemId }) }),
}

// --- Admin: 遊戲化角色數值（perm scope 'rpg'；見 internal/rpg admin.go） ---

export interface AdminRpgUser {
  id: string
  name: string
  email: string
  is_vvip: boolean
  has_character: boolean
  free_points: number
  job_level: number
  stats?: RpgStats
}

// --- DORPG P2：戰鬥內容 CRUD 型別（怪物/技能/道具/場景/隊友/遭遇/戰鬥數據）。json tag 逐欄對照
// 後端 internal/rpg/{scaling.go,content.go,content_repo.go} 的同名 Row struct（2026-09-14 已對照
// 原始碼核對，非憑空照契約寫），供 /admin/rpg 七個內容分頁使用。這批型別只給 adminRpgApi 用，
// 跟上面 FE_MEMBER 的 RpgBootstrap* 系列（camelCase、玩家端 bootstrap wire 格式）刻意分開。

/** rpg_monsters 一列。對照 internal/rpg/scaling.go MonsterRow。
 *
 * DORPG P11（契約 §2、WIRE §後台）：`rank` 收斂為九值字面聯集（migration 189 加 FK 參照
 * rpg_monster_ranks，既有 A–E 值皆合法子集）；新增 `weak_elements`——P5 引擎/bootstrap 早就支援
 * 怪物弱點屬性桶（見 dorpg/types.ts Enemy.weakElements、RpgBootstrapEnemyRaw.weakElements），但
 * P2 建表當時漏開這個後台可編輯欄位，只能靠 SQL migration 塞值，本輪順手補齊（既有落差，非本輪
 * 新規則）。
 */
export interface RpgMonster {
  id: string
  name: string
  rank: RpgRank
  attribute: string
  size: string
  race: string
  sprite_id: string
  poster_url: string
  hp_mult: number
  atk_mult: number
  def_mult: number
  speed_mult: number
  threat: number
  is_boss: boolean
  is_active: boolean
  sort_order: number
  /** 這隻怪的弱點屬性桶（DOR 8 桶，同 RpgBootstrapEnemyRaw.weakElements）；技能屬性命中其中之一時
   *  傷害加成（見 rpgMeta.ts CONFIG_GROUPS 的 battle_weakness_bonus_pct 說明）。缺省 []（無弱點）。 */
  weak_elements: RpgElement[]
}

/**
 * rpg_skills 一列。對照 internal/rpg/content.go SkillRow。
 *
 * DORPG P10（CONTRACT §7 決定更新）：P5 建表時 job_id/path/tier/max_level/effect/dmg_type/
 * prereq_skill_id/prereq_level/mp_cost_per_level/display_text/implemented 這批職業技能樹欄位
 * 「本輪沒有後台 CRUD」，只能靠 SQL migration 編輯——這裡補齊成完整的 SkillRow 鏡射（原本只有前
 * 5 個通用技能在用的 kind/target 兩個欄位型別也太窄，改成完整 SkillKind／SkillTarget），P10 起
 * SkillsTab 開放編輯，讓後台能設定 kind='taunt'（重騎士守護系列）與 path='c'。
 */
export interface RpgSkill {
  id: string
  name: string
  icon_id: string
  kind: SkillKind
  target: SkillTarget
  weapon: 'sword' | 'staff' | 'bow' | 'greatsword'
  element: string
  mp_cost: number
  cooldown_ms: number
  coefficient: number
  flat: number
  cast_ms: number
  is_default: boolean
  is_active: boolean
  sort_order: number
  /** null／''＝通用技能（既有 5 個），非空＝職業技能樹所屬職業。 */
  job_id: string | null
  /** ''＝通用技能無路線；職業技能一律 'a'|'b'（重騎士另有 'c'，見契約 dorpg_p10 §1）。 */
  path: '' | 'a' | 'b' | 'c'
  /** 路線內順位（1 起算，決定技能欄與角色頁排序）；通用技能為 0。 */
  tier: number
  /** 技能等級上限；通用技能固定 1（沒有等級概念）。 */
  max_level: number
  /** SkillEffect JSON（見後端 content.go SkillEffect），後台以 JSON textarea 編輯——各 kind 只
   *  取用其中一部分欄位（damage/heal/shield 用 coef_base/flat_base/hits，buff/debuff/passive 用
   *  stat/value_base/value_per_level，taunt 用 duration_base_ms/damage_taken_pct_base 等，見契約
   *  dorpg_p10 CONTRACT §2 hk_c1~c4 範例）。通用技能（既有 5 個）留 {} 即可。 */
  effect: Record<string, unknown>
  /** 僅 kind='damage' 有意義：physical 扣 DEF、magic 扣 MDEF；其餘 kind 仍會有值但沒有實際效果。 */
  dmg_type: '' | 'physical' | 'magic'
  /** 前置技能 id；null＝無前置。 */
  prereq_skill_id: string | null
  /** 前置技能需要達到的等級；無前置時忽略。 */
  prereq_level: number
  /** 每級遞增的 MP 消耗（疊加在 mp_cost 之上）；通用技能固定 0。 */
  mp_cost_per_level: number
  /** 角色頁/腳本編輯器技能列直接顯示的效果說明文字，前端不解析。 */
  display_text: string
  /** false＝本輪引擎未實裝（special 類），可配點但戰鬥中不可用；通用技能恆為 true。 */
  implemented: boolean
}

/**
 * rpg_jobs 一列——GET /admin/rpg/jobs 回傳與 JobDTO 是同一個後端 struct（JobRow 的 *Full 版本），
 * 形狀因此完全一致（巢狀 path_a/path_b/path_c，見 JobDTO 型別註解），不像本檔其餘 admin Row 型別
 * 各自鏡射扁平 DB 欄位——這裡刻意跟著後端「同一個 DTO 兩處共用」的設計，不要自行拍平。
 *
 * ⚠️ PUT 語意跟 GET 不對稱：GET 的 path_c 是「有才出現」（undefined＝沒有第三路線）；PUT body 的
 * path_c 後端固定要求一個完整物件，用 `id` 空字串代表「清除這條路線」（見 adminJobPutRequest 註解，
 * 目前只有 heavy_knight 會填非空值）——JobsTab 存檔時必須自己組出 `{id,name,desc}`，不能直接把
 * 讀到的 `path_c?: JobPathDTO`（可能 undefined）原樣送回去。
 *
 * DORPG P10（CONTRACT §7 決定更新、WIRE §後台）：六職業固定資料，P5 決定「本輪沒有後台 CRUD」；
 * P10 追加後台 CRUD（PUT /admin/rpg/jobs），但六職業本身仍是固定 6 筆，只開放編輯既有列，不提供
 * 新增／刪除。FRONTEND 這輪只在 JobsTab 開放編輯 path_c／traits 兩組欄位，其餘欄位唯讀顯示但仍
 * 要在 PUT body 完整帶回（後端 UPDATE 會覆寫這些欄位，見同一份型別的 PUT 語意註解）。
 */
export interface RpgJob {
  id: string
  name: string
  tagline: string
  description: string
  path_a: JobPathDTO
  path_b: JobPathDTO
  /** GET：undefined＝此職業沒有第三條路線（目前只有 heavy_knight 有值）。 */
  path_c?: JobPathDTO
  /** 依 key 排序的路線陣列（唯讀，後端從 path_a/b/c 組出）；本輪 JobsTab 不需要用到，僅供型別對齊。 */
  paths: JobPathKeyedDTO[]
  /** 職業天生特性，目前只有 heavy_knight 的 {"damage_taken_pct":-15}；其餘職業缺省 {}。 */
  traits: { damage_taken_pct?: number }
  weapon: 'sword' | 'staff' | 'bow' | 'greatsword'
  atk_branch: 'melee' | 'ranged'
  recommended_stats: string
  sort_order: number
}

/** rpg_items 一列。對照 internal/rpg/content.go ItemRow。 */
export interface RpgItem {
  id: string
  name: string
  icon_id: string
  kind: 'hp' | 'mp' | 'revive'
  amount: number
  default_quantity: number
  is_active: boolean
  sort_order: number
}

/** rpg_scenes.slots 陣列元素。對照 internal/rpg/content.go SceneSlotRow。 */
export interface RpgSceneSlot {
  id: string
  x: number
  y: number
  scale: number
  row: 'rear' | 'front'
}

/** rpg_scenes 一列。對照 internal/rpg/content.go SceneRow。 */
export interface RpgScene {
  id: string
  name: string
  image_url: string
  slots: RpgSceneSlot[]
  location_note: string
  is_active: boolean
  sort_order: number
}

/** rpg_companions 一列。對照 internal/rpg/scaling.go CompanionRow。 */
export interface RpgCompanion {
  id: string
  name: string
  portrait_id: string
  role: string
  weapon: 'sword' | 'staff' | 'bow' | 'greatsword'
  level_offset: number
  hp_mult: number
  mp_mult: number
  atk_mult: number
  matk_mult: number
  def_mult: number
  mdef_mult: number
  act_interval_mult: number
  skill_ids: string[]
  is_player_portrait: boolean
  is_active: boolean
  sort_order: number
}

/** rpg_encounter_monsters 一列（編組其中一個槽位）。對照 internal/rpg/content.go EncounterMonsterRow。 */
export interface RpgEncounterMonster {
  slot: 'rear_left' | 'rear_right' | 'front_left' | 'front_center' | 'front_right'
  monster_id: string
  power_scale: number
}

/** rpg_encounters 一列（含編組）。對照 internal/rpg/content.go EncounterRow——id 是 DB 內部 UUID，
 *  刻意不對外匯出，對外一律用 code 識別（PUT/DELETE 皆用 code）。 */
export interface RpgEncounter {
  code: string
  title: string
  subtitle: string
  scene_id: string
  scene_kind: 'normal' | 'boss'
  difficulty: number
  power_scale: number
  escape_chance: number
  can_escape: boolean
  is_active: boolean
  sort_order: number
  monsters: RpgEncounterMonster[]
  /** DORPG P6（契約 §2）：這一場怪物的等級 N（1–99），驅動 battle_scale_mode="level" 時的
   *  RefPlayer(N) 縮放；六場預設 10/20/30/40/50/60。 */
  monster_level: number
  /** DORPG P11（契約 §1/§2、WIRE §後台）：'legacy'＝既有六場公式（手感零改動），'rank'＝吃
   *  rpg_monster_ranks 九級倍率（見下面 rank／monster_count）。 */
  scaling_mode: 'legacy' | 'rank'
  /** 'fixed'＝monster_level 欄位本身（既有語意），'player'＝怪物等級跟隨玩家有效等級（強度挑戰
   *  20 場用這個，monster_level 欄位在這裡只是後台編輯用的預設值，不影響實際對戰）。 */
  level_mode: 'fixed' | 'player'
  /** scaling_mode='rank' 才有意義；null＝未設定（legacy 場次）。 */
  rank: RpgRank | null
  /** 這場出現的怪物隻數（1/3/5，強度挑戰用）；null＝未設定（legacy 場次沿用 monsters 編組決定）。 */
  monster_count: number | null
}

// --- DORPG P11：怪物強度九級（見契約 dorpg_p11 CONTRACT.md §1/§2、WIRE §後台）。固定九列
// （rank 為主鍵，PUT 語意是 upsert 但後端只認九個既有 rank 值，不接受新增/刪除——比照
// RpgWeaponType／RpgJob「只開放 GET/PUT」的既有慣例，adminRpgApi 不提供 deleteMonsterRank）。

/** rpg_monster_ranks.summon 一波召喚（契約 §1：A 以上會在 HP% 跨門檻時召喚低階怪）。
 *  monster_ids 空陣列＝該波召喚該 rank 的分級怪（見契約 §1 九隻分級怪物）。 */
export interface RpgMonsterRankSummonWave {
  at_hp_pct: number
  rank: RpgRank
  count: number
  /** 空＝召喚該 rank 的分級怪；後端 `monster_ids,omitempty`，空陣列時這個鍵可能整個不出現。 */
  monster_ids?: string[]
  /** 召喚怪整體折減（P11 sim2：完整 rank 向量太強），缺省 1.0、值域 0.05–2.0 */
  power_scale?: number;
}
export interface RpgMonsterRankSummon {
  /** 後端 `waves,omitempty`——大多數等級（F～B）沒有召喚設定，GET 回應可能整個是 `{}`
   *  （鍵不存在），不是 `{ waves: [] }`；讀取端一律要用 `summon?.waves ?? []` 這種寫法。 */
  waves?: RpgMonsterRankSummonWave[]
}

/** rpg_monster_ranks 一列。對照 internal/rpg（migration 189 新表）——九級（F/E/D/C/B/A/SA/S/SS）
 *  相對「同級參考玩家 Ref(N)」的絕對倍率，`scaling_mode='rank'` 的遭遇才會用到；level_curve／summon
 *  是 JSON，比照 WeaponsTab profile／AiStrategiesTab params 的既有慣例存成排版過的字串編輯。 */
export interface RpgMonsterRank {
  rank: RpgRank
  label: string
  sort_order: number
  hp_mult: number
  atk_mult: number
  def_mult: number
  mdef_mult: number
  /** 每級修正係數（契約 §1：等級漂移根治留給 P13，本輪先留 JSONB 掛勾，預設 {}）。 */
  level_curve: Record<string, unknown>
  summon: RpgMonsterRankSummon
  badge_color: string
  description: string
  /** 校準備註（RANK_TABLE.md 的錨點與已知缺口，唯讀顯示用途，PUT body 未列這欄不代表後端會拒絕，
   *  只是本輪表單不開放編輯——調整請改 description）。 */
  calibrated_note?: string
}

// --- DORPG P7：武器內容 CRUD 型別（見契約 dorpg_p7 CONTRACT.md §2、WIRE §後台）。與上面 FE_MEMBER
// 的 WeaponTypeDTO/WeaponDTO 刻意分開宣告（跟其餘七個內容分頁的既有慣例一致：admin 用 snake_case
// row 直接對照 DB 欄位，會員端 wire 另外挑選欄位/加旗標），profile 共用同一個 WeaponProfile 型別。

/** rpg_weapon_types 一列。18 筆固定資料（每職業 3 種），後台只能編輯既有列（WIRE：後端只開放
 *  GET/PUT，沒有 DELETE 路由），故 WeaponTypesTab 不提供刪除／新增按鈕。沒有 is_active 欄位——
 *  契約沒有把「停用整個武器類型」設計進資料模型，要下架請改動該類型底下個別武器的 is_active。 */
export interface RpgWeaponType {
  id: string
  job_id: string
  name: string
  visual: 'sword' | 'staff' | 'bow' | 'greatsword'
  elemental_capable: boolean
  description: string
  traits: Record<string, unknown>
  sort_order: number
}

/** rpg_weapons 一列。level_req 對照 tier→1/10/20/30/40/50/60/70/80/90（契約 §2），後台直接顯示/
 *  編輯後端存的值，不重算。profile 是契約 §3 WeaponProfile 的 JSON，後台以 JSON textarea 編輯
 *  （比照 RpgConfig.battle_element_chart 的既有慣例），存檔前另外做 JSON.parse 驗證。 */
export interface RpgWeapon {
  id: string
  type_id: string
  tier: number
  name: string
  rarity: WeaponRarity
  level_req: number
  element: RpgElement
  profile: WeaponProfile
  description: string
  is_active: boolean
  sort_order: number
}

/** rpg_armor_items 一列（見契約 dorpg_p8 CONTRACT.md §1、WIRE §後台）。job_id＝null 只會出現在
 *  飾品（accessory）；防具（helmet/gloves/armor/legs/boots）恆有 job_id。profile 是契約 §2
 *  ArmorProfile 的 JSON，比照 WeaponsTab 的既有慣例存成排版過的字串編輯。 */
export interface RpgArmorItem {
  id: string
  job_id: string | null
  slot: ArmorItemSlot
  tier: number
  name: string
  rarity: WeaponRarity
  level_req: number
  profile: ArmorProfile
  description: string
  is_active: boolean
  sort_order: number
}

/**
 * DORPG P9（契約 §2、WIRE §後台）：rpg_ai_strategies 一列。id 集合固定六種（引擎 registry 白名單，
 * 後端持有同一份 id 清單常數）——後台只能編輯既有列的顯示/參數/啟用/排序，不能發明引擎沒有的行為
 * （新增行為＝改 ENGINE 的 registry＋seed 一列，不是後台這裡能做的事）。
 */
export interface RpgAiStrategy {
  id: string
  name: string
  description: string
  /** 覆寫引擎預設門檻的參數（見契約 §4 各策略 params，例如 heal_pct/mp_reserve_pct），JSON 編輯。 */
  params: Record<string, unknown>
  is_active: boolean
  sort_order: number
}

/** GET /admin/rpg/battle-logs 明細列（不含 email/account_code——隱私規則，見後端 SQL 只
 *  SELECT COALESCE(name,handle)）。對照 internal/rpg/content_repo.go battleLogListRow。 */
export interface RpgBattleLogRow {
  created_at: string
  display_name: string
  encounter_code: string
  outcome: RpgBattleOutcomeAdmin
  duration_ms: number
  damage_dealt: number
  damage_taken: number
}
export type RpgBattleOutcomeAdmin = 'victory' | 'defeat' | 'draw' | 'escaped' | 'abandoned'

/** GET /admin/rpg/battle-logs 每遭遇彙總列。對照 internal/rpg/content_repo.go battleLogSummaryRow。 */
export interface RpgBattleLogSummary {
  encounter_code: string
  plays: number
  wins: number
  win_rate: number
  avg_duration_ms: number
  p50_duration_ms: number
  avg_damage_taken: number
  defeat_rate: number
}

export const adminRpgApi = {
  config: (token: string) => request<{ config: RpgConfig; defaults: RpgConfig }>('/admin/rpg/config', { headers: withAuth(token) }),
  setConfig: (token: string, config: RpgConfig) =>
    request<{ config: RpgConfig; defaults: RpgConfig }>('/admin/rpg/config', { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ config }) }),
  // 入口狀態＋白名單：走專屬端點（perm 'rpg'），不透過 /admin/app-settings（那條路徑掛
  // perm 'settings'，只勾「遊戲化」權限的管理者會被擋——見 code review finding，已修）。
  getEntry: (token: string) => request<{ state: string; whitelist: string }>('/admin/rpg/entry', { headers: withAuth(token) }),
  setEntry: (token: string, state: string, whitelist: string) =>
    request<{ state: string; whitelist: string }>('/admin/rpg/entry', { method: 'PUT', headers: withAuth(token), body: JSON.stringify({ state, whitelist }) }),
  users: (token: string, q?: string) => {
    const qs = q ? `?q=${encodeURIComponent(q)}` : ''
    return request<{ users: AdminRpgUser[] }>(`/admin/rpg/users${qs}`, { headers: withAuth(token) })
  },
  setVvip: (token: string, id: string, on: boolean) =>
    request<{ ok: boolean }>(`/admin/rpg/users/${id}/vvip`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ on }) }),
  reset: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/users/${id}/reset`, { method: 'POST', headers: withAuth(token) }),
  setJobLevel: (token: string, id: string, jobLevel: number) =>
    request<{ ok: boolean }>(`/admin/rpg/users/${id}/job-level`, { method: 'POST', headers: withAuth(token), body: JSON.stringify({ job_level: jobLevel }) }),
  preview: (token: string, params: { base_level: number; job_level: number } & RpgStats) => {
    const qs = new URLSearchParams()
    qs.set('base_level', String(params.base_level))
    qs.set('job_level', String(params.job_level))
    ;(['str', 'agi', 'vit', 'dex', 'int', 'luk'] as RpgStatKey[]).forEach((k) => qs.set(k, String(params[k])))
    return request<{ derived: RpgDerived; max_hp: number; max_mp: number; next_cost: Partial<RpgStats> }>(`/admin/rpg/preview?${qs.toString()}`, { headers: withAuth(token) })
  },

  // --- DORPG P2：內容 CRUD（怪物/技能/道具/場景/隊友/遭遇）＋戰鬥數據，對照
  // internal/rpg/battle_admin.go 實際掛載路徑（2026-09-14 已對照原始碼，未照契約憑空寫）。---
  monsters: (token: string) => request<{ monsters: RpgMonster[] }>('/admin/rpg/monsters', { headers: withAuth(token) }),
  putMonster: (token: string, row: RpgMonster) =>
    request<RpgMonster>('/admin/rpg/monsters', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteMonster: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/monsters/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),

  skills: (token: string) => request<{ skills: RpgSkill[] }>('/admin/rpg/skills', { headers: withAuth(token) }),
  putSkill: (token: string, row: RpgSkill) =>
    request<RpgSkill>('/admin/rpg/skills', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteSkill: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/skills/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),

  // DORPG P10（契約 §7 決定更新、WIRE §後台）：職業表——六職業固定列，沒有 DELETE／新增路由
  // （比照 weapon-types 既有慣例），只能編輯既有列（本輪窄用途：path_c／traits，見 RpgJob 型別註解）。
  jobs: (token: string) => request<{ jobs: RpgJob[] }>('/admin/rpg/jobs', { headers: withAuth(token) }),
  putJob: (token: string, row: RpgJob) =>
    request<RpgJob>('/admin/rpg/jobs', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),

  items: (token: string) => request<{ items: RpgItem[] }>('/admin/rpg/items', { headers: withAuth(token) }),
  putItem: (token: string, row: RpgItem) =>
    request<RpgItem>('/admin/rpg/items', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteItem: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/items/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),

  scenes: (token: string) => request<{ scenes: RpgScene[] }>('/admin/rpg/scenes', { headers: withAuth(token) }),
  putScene: (token: string, row: RpgScene) =>
    request<RpgScene>('/admin/rpg/scenes', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteScene: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/scenes/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),

  companions: (token: string) => request<{ companions: RpgCompanion[] }>('/admin/rpg/companions', { headers: withAuth(token) }),
  putCompanion: (token: string, row: RpgCompanion) =>
    request<RpgCompanion>('/admin/rpg/companions', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteCompanion: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/companions/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),

  encounters: (token: string) => request<{ encounters: RpgEncounter[] }>('/admin/rpg/encounters', { headers: withAuth(token) }),
  putEncounter: (token: string, row: RpgEncounter) =>
    request<RpgEncounter>('/admin/rpg/encounters', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteEncounter: (token: string, code: string) =>
    request<{ ok: boolean }>(`/admin/rpg/encounters/${encodeURIComponent(code)}`, { method: 'DELETE', headers: withAuth(token) }),

  // DORPG P11（契約 §2/§3、WIRE §後台）：怪物強度九級——固定九列，只有 GET/PUT（比照
  // weapon-types／jobs 既有慣例，不提供刪除路由，rank 固定九值不可新增刪除）。
  monsterRanks: (token: string) => request<{ ranks: RpgMonsterRank[] }>('/admin/rpg/monster-ranks', { headers: withAuth(token) }),
  putMonsterRank: (token: string, row: RpgMonsterRank) =>
    request<RpgMonsterRank>('/admin/rpg/monster-ranks', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),

  // --- DORPG P7：武器類型／武器 CRUD（見契約 dorpg_p7 CONTRACT.md §2、WIRE §後台）。
  // weapon-types 只有 GET/PUT（沒有 DELETE 路由，見 RpgWeaponType 型別註解）。---
  weaponTypes: (token: string) => request<{ weapon_types: RpgWeaponType[] }>('/admin/rpg/weapon-types', { headers: withAuth(token) }),
  putWeaponType: (token: string, row: RpgWeaponType) =>
    request<RpgWeaponType>('/admin/rpg/weapon-types', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),

  weapons: (token: string) => request<{ weapons: RpgWeapon[] }>('/admin/rpg/weapons', { headers: withAuth(token) }),
  putWeapon: (token: string, row: RpgWeapon) =>
    request<RpgWeapon>('/admin/rpg/weapons', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteWeapon: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/weapons/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),

  // --- DORPG P8：防具／飾品 CRUD（見契約 dorpg_p8 CONTRACT.md §1、WIRE §後台）。後端支援
  // ?job_id=／?slot= 篩選，但比照 WeaponsTab 既有慣例——一次抓全部 390 筆、前端自行篩選，不必
  // 為此多打帶查詢參數的請求（資料量固定，前端篩選即可）。---
  armorItems: (token: string) => request<{ armor_items: RpgArmorItem[] }>('/admin/rpg/armor-items', { headers: withAuth(token) }),
  putArmorItem: (token: string, row: RpgArmorItem) =>
    request<RpgArmorItem>('/admin/rpg/armor-items', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteArmorItem: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/armor-items/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),

  // --- DORPG P9：AI 戰鬥策略 CRUD（見契約 dorpg_p9 CONTRACT.md §2、WIRE §後台）。DELETE 只允許
  // 非 balanced 且無腳本／玩家引用，否則後端回 409 in_use（呼叫端自行組中文文案）。---
  aiStrategies: (token: string) => request<{ ai_strategies: RpgAiStrategy[] }>('/admin/rpg/ai-strategies', { headers: withAuth(token) }),
  putAiStrategy: (token: string, row: RpgAiStrategy) =>
    request<RpgAiStrategy>('/admin/rpg/ai-strategies', { method: 'PUT', headers: withAuth(token), body: JSON.stringify(row) }),
  deleteAiStrategy: (token: string, id: string) =>
    request<{ ok: boolean }>(`/admin/rpg/ai-strategies/${encodeURIComponent(id)}`, { method: 'DELETE', headers: withAuth(token) }),

  // 戰鬥數據：期間 days（7/30，後端夾在 1..90）＋可選 code 篩選單一遭遇；limit 明細筆數上限。
  battleLogs: (token: string, params: { code?: string; days: 7 | 30; limit?: number }) => {
    const qs = new URLSearchParams()
    if (params.code) qs.set('code', params.code)
    qs.set('days', String(params.days))
    qs.set('limit', String(params.limit ?? 200))
    return request<{ rows: RpgBattleLogRow[]; summary: RpgBattleLogSummary[] }>(`/admin/rpg/battle-logs?${qs.toString()}`, { headers: withAuth(token) })
  },
}

// --- DORPG P2：戰鬥內容（怪物/技能/道具/場景/隊友/遭遇）＋玩家真實角色接入的會員端 API ---
// 見後端 rpg/battle.go（契約 §3.4）。本區塊 + 上面的 rpgApi 屬於 FE_MEMBER；adminRpgApi 區塊為
// FE_ADMIN 所有，兩邊只各自動自己的區塊、不重排檔案（契約 §0）。

/** 遭遇卡片列表其中一隻怪的簡要（挑選畫面用縮圖，不含完整戰鬥數值）。對照 services/api/internal/rpg/battle.go wireEncounterMonsterInfo。 */
export interface RpgEncounterMonsterBrief {
  slot: string
  name: string
  poster_url: string
  is_boss: boolean
}

/** 本人在這個遭遇的累計戰績（GET /rpg/battle/encounters 依 rpg_battle_logs 對本人聚合）。對照 wireEncounterStats。 */
export interface RpgEncounterStats {
  plays: number
  wins: number
  best_ms: number | null // 後端存 *int：尚未有任何一場勝利可算「最快」時是 null，不是 0
  last_outcome: string // ''＝尚未挑戰過；否則 victory|defeat|draw|escaped|abandoned
}

/** bootstrap 回應的「encounter」子物件形狀（不含 monsters/stats，那兩個只有列表端點才有）——
 *  對照 wireEncounterInfo。 */
export interface RpgBootstrapEncounterInfo {
  code: string
  title: string
  subtitle: string
  scene_id: string
  scene_image_url: string
  scene_kind: 'normal' | 'boss'
  difficulty: number // 1~5，前端畫星
  can_escape: boolean
  /** DORPG P6（契約 §2／WIRE）：這一場怪物等級 N（EncounterPicker 卡片顯示「怪物 Lv.N」）；
   *  DORPG P11：level_mode='player' 時這裡回的是伺服器算好的玩家有效等級（見下方 level_mode）。 */
  monster_level: number
  // DORPG P11（契約 §1/§2、WIRE §REST：「GET /rpg/battle/encounters 每場新增…」）：選填——這批欄位
  // 只有走過 migration 189 的後端會送；舊版後端／app/dev/dorpg 的離線 fixture（Preview.tsx）沒有
  // 這幾欄時，EncounterPicker 一律退回「無分級資料」的單一清單相容模式（見契約 §1 決策段），
  // BattleStage 的敵人徽章也不渲染，不是本檔案的相容性漏洞。
  /** 'legacy'＝既有六場公式，'rank'＝吃 rpg_monster_ranks 九級倍率。 */
  scaling_mode?: 'legacy' | 'rank'
  /** 'fixed'＝monster_level 是固定值，'player'＝monster_level 已經是玩家有效等級。 */
  level_mode?: 'fixed' | 'player'
  rank?: RpgRank | null
  rank_label?: string | null
  badge_color?: string | null
  /** 這場的怪物隻數（1/3/5，強度挑戰卡片顯示「單挑／三隻／五隻」，見 rpgMeta.ts rankCountLabel）。 */
  monster_count?: number | null
  /** EncounterPicker 分組用：'story'＝既有六場劇情場景卡片外觀不變，'rank'＝強度挑戰卡片（徽章＋
   *  隻數＋「Lv.＝你的等級」）。缺省時 EncounterPicker 視為 'story'（相容模式，見上方檔頭說明）。 */
  group?: 'story' | 'rank'
}

/** 對照 wireEncounterSummary（= wireEncounterInfo 內嵌 + monsters + stats，Go struct embedding 展平）。 */
export interface RpgEncounterSummary extends RpgBootstrapEncounterInfo {
  monsters: RpgEncounterMonsterBrief[]
  stats: RpgEncounterStats
}

/** 選單頁角色摘要（只給挑選遭遇時看戰力用；完整配點介面仍在角色頁）。對照 BattleEncounters 回應的 "character"。
 *  ⚠️ unspent_hint 是後端算好的布林（ch.FreePoints>0），不是文案字串——文案（「你還有 N 點未配置」）
 *  要靠 free_points 自己組，這裡命名沿用後端 JSON key，容易誤讀成字串，故特別註記。 */
export interface RpgBattleCharacterBrief {
  base_level: number
  free_points: number
  power: number
  max_hp: number
  max_mp: number
  unspent_hint: boolean
}

export interface RpgBattleEncounters {
  encounters: RpgEncounterSummary[]
  character: RpgBattleCharacterBrief
}

// bootstrap 的「sample」／「config」子物件依 services/api/internal/rpg/battle.go 檔頭明講的命名慣例
// 直接對齊 apps/web/src/lib/dorpg/types.ts／engine/types.ts 的 camelCase 欄位名（不是全站 snake_case
// 慣例），而且 portraitUrl／imageUrl／iconUrl 後端已經算好完整路徑——這裡不需要再呼叫
// charPortrait()/kitAsset() 轉換一次（那是 sampleBattle.ts 组「id → 路徑」用的，wire 格式已經是路徑）。
// 型別逐欄對照 wirePartyMember/wireEnemy/wireScene/wireSkill/wireItem/wireBattleSample（2026-09-14
// 已對照 battle.go 原始碼核對過，非憑空假設）。
export interface RpgBootstrapActorStatsRaw {
  hpMax: number
  mpMax: number
  atk: number
  matk: number
  def: number
  mdef: number
}
// P2（暴擊／Miss／無效攻擊）新增：命中/暴擊評級，逐欄對齊 apps/web/src/lib/dorpg/types.ts 的
// CombatRating（camelCase，同一份「wire 格式已經是引擎形狀」慣例，見本檔上方對照表說明）。
// 玩家/隊友取自 internal/rpg Compute 的 Hit/Flee/CritPct/CritShield；怪物由後端依
// battle_monster_* 係數推導。可選——後端尚未送這欄，或未來欄位缺漏時，fromApi.ts 整包丟棄、
// 交給 engine 自己用 config 推導後備值，不強行塞半殘資料。
export interface RpgBootstrapRatingRaw {
  hit: number
  flee: number
  critPct: number
  critShield: number
  // P3（AGI 攻速／DEX 詠唱縮減）新增：可選——舊版後端（本輪部署前）可能還沒送這兩個欄位，
  // fromApi.ts asRating() 缺欄位時給中性預設值（不是整包丟棄 rating，見該函式註解）。
  aspd?: number
  castReductionPct?: number
  // 審查#5【低・PLAUSIBLE】新增：被動技能 crit_dmg_pct 加成的總和（%），疊在浮動暴擊倍率之上
  // （見 engine/effects.ts rollCritMultiplier 呼叫端）。可選、缺欄位視為 0（無加成），理由同
  // aspd/castReductionPct——舊版後端可能還沒送這個欄位。
  critDmgPct?: number
}
/**
 * WIRE §戰鬥 bootstrap「玩家 party member 新增 equipmentEffects」（契約 dorpg_p8 CONTRACT.md §2）：
 * 防具＋飾品彙總後、引擎需要另外套用的六個欄位（其餘防具彙總——六素質 flat／def／hp_pct／
 * mp_pct／atk_pct／matk_pct／crit_pct／crit_dmg_pct——已經在 Compute 階段吃進 stats/derived，
 * 引擎不需要也不應該再套一次）。camelCase，同 RpgBootstrapWeaponProfileRaw 的既有慣例。
 */
export interface RpgBootstrapEquipmentEffectsRaw {
  intervalPct: number
  mpCostReducePct: number
  hpRegenPctPer5s: number
  mpRegenPctPer5s: number
  damageTakenPct: number
  elementResistPct: number
}
export interface RpgBootstrapPartyMemberRaw {
  id: string
  name: string
  level: number
  hp: number
  hpMax: number
  mp: number
  mpMax: number
  portraitUrl: string | null
  stats?: RpgBootstrapActorStatsRaw
  /**
   * DORPG P7（WIRE §戰鬥 bootstrap）破壞性變更：INTEGRATOR 對照 services/api/internal/rpg/
   * battle.go 確認——`wirePartyMember.Weapon` 是 `*wireWeapon`（`json:"weapon"`，無 omitempty），
   * 正式後端這個鍵一律送完整物件或 JSON null，P1～P6 時期的純視覺字串已被完全取代，wire 上
   * 不會再有 `weapon: "sword"` 這種寫法。這裡型別放寬成三選一（物件｜字串｜null）只是配合
   * fromApi.ts mapPartyMember() 的雙軌防禦式讀取（asWeapon() 只在收到合法字串時成功、
   * asEquippedWeapon() 只在收到合法物件時成功，兩者互斥）——不代表正式後端真的還會送字串，純粹
   * 讓型別誠實涵蓋「舊格式測試 fixture／理論上的降級回應」這個邊界，也讓這裡不必再靠
   * unknown-cast 繞過型別檢查。未裝備＝物件（INTEGRATOR 已在 battle.go resolvePlayerWeaponWire
   * 補回職業預設視覺，玩家這個鍵幾乎不會是 null；查無職業這種理論邊界才會是 null）；隊友固定
   * 送視覺物件（Profile 中性值，本輪不裝備，見契約 §2）。
   */
  weapon?: RpgBootstrapEquippedWeaponRaw | string | null
  // DORPG P8（WIRE §戰鬥 bootstrap）新增：防具＋飾品彙總（不含武器，武器仍走上面 weapon.profile），
  // 玩家與隊友皆會帶（契約：「傭兵一律零值物件」——本輪隊友不裝備防具，但欄位仍是完整物件而非
  // undefined，數值全 0）。選填只是配合本檔一貫的「舊版後端可能還沒送」防禦慣例（同 rating？／
  // weakElements？），並非契約允許省略。ENGINE 的 fromApi.ts 負責在缺欄位時填零值預設（不在本輪
  // FRONTEND 寫入範圍內，見任務回報）。
  equipmentEffects?: RpgBootstrapEquipmentEffectsRaw
  rating?: RpgBootstrapRatingRaw
  // P5：玩家目前選擇的職業（見契約 §1／WIRE）——武器已由後端決定填在上面的 weapon，
  // 這個 id 只給前端顯示/除錯用，選填（AI 隊友恆為 undefined）。
  jobId?: string | null
  // --- DORPG P6（契約 §3.2／WIRE）新增：隊友（非玩家本人）才會帶這兩個欄位 ---
  /** 隊友 AI 可用技能（已展開、不含 passive、只含 implemented=true），供引擎的五段優先序判斷。
   *  玩家本人這欄不會有值（玩家技能欄走既有的 sample.skills，不重複塞在這裡）。 */
  skills?: RpgBootstrapSkillRaw[]
  /** 這位隊友目前套用的腳本名稱（酒館頁選的），純顯示用；玩家本人恆為 undefined。 */
  presetName?: string
  /** DORPG P9（契約 §4、WIRE §戰鬥 bootstrap）：這位角色目前套用的 AI 策略 id——玩家＝
   *  auto_strategy_id，隊友＝preset.strategy_id。ENGINE 的 decideAction 共用同一個決策入口，
   *  隊友與「玩家自動戰鬥」都靠這個欄位決定要用哪種策略（未知 id 引擎端一律退回 balanced）。 */
  strategyId?: string
  /** DORPG P10（CONTRACT §3/§4、WIRE §戰鬥 bootstrap）：學過「守護本能」（hk_c2）≥1 級——玩家
   *  按防禦（GUARD）期間即進入守護狀態（見契約 §3 inGuardianState）。後端一律送值（非
   *  omitempty，見 battle.go wirePartyMember.GuardTaunt），非重騎士／未學到一律 false，不是
   *  undefined；選填只是配合本檔一貫的「舊版後端可能還沒送」防禦慣例（同 rating？／weakElements？）。 */
  guardTaunt?: boolean
  /** DORPG P10（CONTRACT §1/§3、WIRE §戰鬥 bootstrap）：這位角色所屬職業的天生特性，目前只有
   *  heavy_knight 的 damageTakenPct: -15（其餘一律送 0，非 undefined，見 battle.go wireJobTraits
   *  註解）；已經合併進上面 equipmentEffects.damageTakenPct（夾限 ≥ -60 仍適用），這裡另外送一份
   *  只給 FRONTEND 顯示/除錯用，ENGINE 的戰鬥計算不重複套用。選填同上，防禦舊版後端。 */
  jobTraits?: { damageTakenPct: number }
}
/**
 * WIRE §戰鬥 bootstrap WeaponProfileWire（camelCase，「已展開成引擎詞彙」版本）：Compute 已經吃掉
 * 的 int_bonus/mp_pct/atk_pct/matk_pct/def_pct/mdef_pct/flee_bonus/crit_pct 不再送（那些只影響
 * Compute 算好的 stats，戰鬥引擎不需要再套一次），critPct 仍送供除錯顯示（同 RpgBootstrapRatingRaw
 * 的既有慣例）。⚠️ 逐欄對齊 ENGINE lib/dorpg/types.ts 的 WeaponProfileWire——那邊每個欄位都是必填
 * number（非 optional），這裡跟著用必填欄位，避免 fromApi.ts 映射時還要處理「wire 缺欄位」的
 * ?? 預設值分支（後端 toWireWeaponProfile 應該逐欄都給值，缺省語意在 rpg_weapons.profile 那層
 * 的 snake_case WeaponProfile 才需要 optional）。
 */
export interface RpgBootstrapWeaponProfileRaw {
  atk: number
  matk: number
  hits: number
  hitMul: number
  extraHitChancePct: number
  intervalPct: number
  chargeTimeMul: number
  chargeDmgMul: number
  splashPct: number
  sizeBonus: { small: number; medium: number; large: number }
  critPct: number
  critDmgPct: number
  elementResistPct: number
  magicSkillPct: number
  element: string
  // DORPG P12（契約 dorpg_p12 CONTRACT.md §3）：前後排加成／貫穿——由後端 buildPlayerWeaponWire
  // 合併 rpg_weapon_types.traits 的四個新鍵算出，非武器職業/類型一律送 0（同其餘欄位「後端逐欄
  // 都給值」慣例，不設 optional）。
  rowBonusFrontPct: number
  rowBonusRearPct: number
  pierceChancePct: number
  pierceDmgPct: number
}
/** 對齊 ENGINE lib/dorpg/types.ts 的 EquippedWeaponWire。 */
export interface RpgBootstrapEquippedWeaponRaw {
  id: string
  name: string
  typeId: string
  visual: string
  profile: RpgBootstrapWeaponProfileRaw
}
export interface RpgBootstrapEnemyRaw {
  id: string
  name: string
  level: number
  hp: number
  hpMax: number
  slot: string
  // P12（CONTRACT §1/§3、WIRE「戰鬥 bootstrap」：battle.go wireEnemy.Row 恆送 'front'|'rear'，
  // 由 Slot 推導）：選填只是防禦——舊版後端（本輪部署前）可能還沒送這個欄位，fromApi.ts 的
  // mapEnemy() 對缺欄位／非法值一律退回 undefined，讓 engine 自己用 rowOfSlot(slot) 後備推導
  // （見 dorpg/engine/formulas.ts rowOfSlot），不會因為這裡沒收到值而漏掉排位加成/貫穿機制。
  row?: string
  imageUrl: string
  rank?: string
  attribute?: string
  size?: string
  race?: string
  stats?: RpgBootstrapActorStatsRaw
  threatPriority?: number
  rating?: RpgBootstrapRatingRaw
  /**
   * 2026-09-14 P2 修正第1輪 審查5（CONFIRMED）：這裡原本的註解描述一個「omitempty 吞掉 false」的
   * 已知 bug，但已對照 services/api/internal/rpg/battle.go 目前程式碼確認該檔的 wireEnemy.CanEscape
   * 是 `json:"canEscape"`（刻意沒有 omitempty，該檔自己也有註解說明原因）——bug 已不存在，舊註解
   * 誤導後續維護者去調查一個不存在的問題，或誤以為這欄位在正式 API 回應裡真的可能是 undefined
   * 而多寫防禦邏輯。正式回應裡這個欄位恆為 true/false，故型別收斂為必有的 boolean。
   */
  canEscape: boolean
  // P5：怪物弱點屬性桶（見契約 §6／WIRE elementMultiplier）；技能 element 命中其中之一 →
  // 傷害 ×(1+battle_weakness_bonus_pct/100)。選填——舊版後端（本輪部署前）可能還沒送。
  weakElements?: string[]
  // DORPG P11（WIRE §戰鬥 bootstrap：「enemy 新增 rankLabel/badgeColor/isSummoned」）：rank 本身
  // P6 已存在（見上方 rank 欄位）；這三個是新加的顯示用欄位（中文標籤／徽章底色／是否為召喚物）。
  // 選填＝舊版後端尚未送這三個欄位時，BattleStage 的徽章直接不渲染，不影響既有無分級戰鬥的畫面。
  rankLabel?: string
  badgeColor?: string
  isSummoned?: boolean
}
export interface RpgBootstrapSceneSlotRaw {
  id: string
  x: number
  y: number
  scale: number
  row: 'rear' | 'front'
}
export interface RpgBootstrapSceneRaw {
  id: string
  name: string
  imageUrl: string
  slots: RpgBootstrapSceneSlotRaw[]
}
/**
 * wireSkill.effect 的裸資料形狀（WIRE.md 引擎章節）。
 * ⚠️ 審查#1(b) 修正：這裡曾經誤宣告成 `import('@/lib/dorpg/types').EffectAtLevel`（camelCase
 * durationMs/mpCost），註解還寫「bootstrap 沿用戰鬥引擎自己的 camelCase 慣例」——但實際上後端
 * services/api/internal/rpg/skills.go 的 EffectAtLevel struct／WIRE.md 逐字列出的都是
 * snake_case（duration_ms/mp_cost，跟本檔會員端 REST 的 EffectAtLevel 用同一套命名，兩者其實是
 * 同一份命名慣例，不是「不同」），前一版註解對現實的描述反了。沿用舊型別會讓 fromApi.ts 讀
 * `e.durationMs`/`e.mpCost` 永遠讀到 undefined（真正的 key 是 duration_ms/mp_cost），buff/debuff
 * 的持續時間因此恆為 0——這是純執行期的資料遺失，型別檢查完全抓不到。獨立宣告一份 snake_case
 * 的裸資料型別，fromApi.ts 的 asEffect() 負責轉成 engine 內部要的 camelCase EffectAtLevel。
 */
export interface RpgBootstrapEffectRaw {
  kind?: string
  stat?: string
  value?: number
  duration_ms?: number
  coef?: number
  flat?: number
  hits?: number
  target?: string
  mp_cost?: number
  // DORPG P10：kind=passive 專用，見 api.ts 會員端 EffectAtLevel.guard_taunt 型別註解——這裡是
  // 同一份資料的 bootstrap（snake_case，逐欄對照後端 Go EffectAtLevel）鏡射版本。
  guard_taunt?: boolean
  // DORPG P10：kind=taunt 專用，同一份資料也會鏡射在 RpgBootstrapSkillRaw.taunt（camelCase）；
  // ENGINE 的 fromApi.ts 讀哪一份是 ENGINE 的選擇，這裡純粹補齊型別對後端誠實。
  retarget?: boolean
  damage_taken_pct?: number
}

export interface RpgBootstrapSkillRaw {
  id: string
  name: string
  iconUrl: string
  cooldownMs: number
  kind: string
  target: string
  mpCost: number
  coefficient: number
  flat: number
  element?: string
  weapon: string
  castMs?: number
  // --- P5 新增（見契約 §4/§6、WIRE §戰鬥 bootstrap）：職業技能才會帶這些欄位；未選職業維持
  // 現行 is_default 技能填法（這批欄位缺省）。effect 已依 level 展開成即時數值，前端不再自己算。---
  // 審查#1【高・CONFIRMED】新增：一次施放命中次數的頂層欄位（後端 battle.go toWireSkillLeveled/
  // toWireSkillLegacy 新增，既有 5 個一般技能固定送 1，缺欄位＝舊版後端，維持 undefined→引擎
  // 預設 1 的既有語意，見 dorpg/types.ts Skill.hits 型別註解）。
  hits?: number
  level?: number
  maxLevel?: number
  displayText?: string
  dmgType?: string
  implemented?: boolean
  effect?: RpgBootstrapEffectRaw
  // DORPG P6（INTEGRATOR 補上，2026-09-18）：ENGINE 向 BACKEND 提的需求——ai.ts 隊友 AI 五段
  // 優先序的「damage 技能挑 tier 最高的」需要真正的 tier 值，不再只靠陣列位置代理。只有職業
  // 技能（toWireSkillLeveled）會填；既有 5 個無職業技能維持 undefined（後端 omitempty 送 0）。
  tier?: number
  // DORPG P10（CONTRACT §3/§4、WIRE §引擎）：kind='taunt' 專用展開值（camelCase，同本結構其餘
  // BACKEND 計算好的欄位慣例，例如 iconUrl/cooldownMs/mpCost）；只有職業技能會填，非 taunt 技能
  // 缺省 undefined。durationMs/damageTakenPct 已依等級展開（base+per_level×(lv-1)），engine 的
  // dispatch.ts resolveTaunt() 直接讀這裡，不重新推導等級公式。
  taunt?: { durationMs: number; damageTakenPct: number; retarget: boolean }
}
export interface RpgBootstrapItemRaw {
  id: string
  name: string
  iconUrl: string
  quantity: number
  kind: string
  amount: number
}
export interface RpgBootstrapSampleRaw {
  party: RpgBootstrapPartyMemberRaw[]
  enemies: RpgBootstrapEnemyRaw[]
  scene: RpgBootstrapSceneRaw
  // P5 起：未選職業維持現行長度 8（Go [8]*wireSkill）；已選職業改回傳最多 10 格（兩排各 5，依
  // path→tier 排序，見契約 §6／WIRE），未裝備／未達等級的槽位是 null。前端一律用陣列實際長度，
  // 不要寫死 8（SkillTray 改用 ENGINE 匯出的 SKILL_SLOTS 常數）。
  skills: (RpgBootstrapSkillRaw | null)[]
  items: RpgBootstrapItemRaw[]
  initialTargetId: string
  sceneKind?: 'normal' | 'boss'
  escapeChance?: number
}
// config 子物件對齊 engine 的 BattleConfig（camelCase）——用 inline import type 借用該型別，避免在這個
// 沒有任何 import 的檔案頂端另開一行 import 造成跟 FE_ADMIN 同時編輯本檔時的不必要衝突面。
// DORPG P6（WIRE：「config 新增 scaleMode: "level"|"power"（純顯示／除錯）」）：只加一個純展示欄位，
// 不需要 ENGINE 在 BattleConfig 裡也加這個 key（引擎完全不讀它），故用交集型別在 api.ts 這層自己補上，
// 避免為了一個顯示用欄位去動 engine/types.ts（不在本輪 FRONTEND 寫入範圍內）。
// DORPG P7（WIRE §引擎：「config 新增 elementAdvantagePct、elementDisadvantagePct、elementSamePct」）：
// 這三個是 elementMultiplier() 真正要讀的係數，不是純展示欄位，但仍屬於「BACKEND 送、ENGINE 讀」的
// BattleConfig 擴充——ENGINE 尚未在 engine/types.ts BattleConfig 加這三個欄位時，用同一招交集型別
// 先把 wire 形狀鋪好，不硬改 engine/types.ts（不在本輪 FRONTEND 寫入範圍內，見任務回報對 ENGINE 的需求）。
// DORPG P9（WIRE §戰鬥 bootstrap：「config 新增 aiStrategies」）：引擎預設 ⊕ DB 覆寫、只含
// is_active 的策略參數表——ENGINE 的 resolveStrategy(id, cfg) 用這份表覆寫 registry 的
// defaultParams（未知 id／找不到覆寫＝直接用引擎預設值）。
export type RpgBootstrapAiStrategiesRaw = Record<string, { params: Record<string, unknown> }>
export type RpgBootstrapConfigRaw = Partial<import('@/lib/dorpg/engine').BattleConfig> & {
  scaleMode?: 'level' | 'power'
  elementAdvantagePct?: number
  elementDisadvantagePct?: number
  elementSamePct?: number
  aiStrategies?: RpgBootstrapAiStrategiesRaw
}
/** DORPG P11（WIRE §戰鬥 bootstrap：「新增 summonPool」）：每個召喚波次預先算好的完整敵人資料
 *  （含數值/sprite），引擎在召喚者 HP% 跨門檻時把 `enemies` 塞進空槽（見契約 §1 召喚門檻表）。
 *  `enemies[].id` 唯一（例如 `<summonerEnemyId>_w1_1`），形狀與 sample.enemies 的元素相同。 */
export interface RpgBattleSummonWaveRaw {
  summonerEnemyId: string
  atHpPct: number
  enemies: RpgBootstrapEnemyRaw[]
}
export interface RpgBattleBootstrap {
  encounter: RpgBootstrapEncounterInfo
  sample: RpgBootstrapSampleRaw
  config: RpgBootstrapConfigRaw
  hints: { free_points: number }
  /** DORPG P9（WIRE §戰鬥 bootstrap：「頂層新增 autoBattle」）：玩家目前是否開啟自動戰鬥
   *  （auto_battle.enabled）——ENGINE 的 createBattle() 用這個初始化 BattleState.autoBattle，
   *  策略 id 走 sample.party[0]（玩家）的 strategyId，不重複送一次。 */
  autoBattle: boolean
  // DORPG P11（契約 §1/§3、WIRE §戰鬥 bootstrap：「頂層新增 scalingMode/levelMode/monsterLevel」）：
  // 選填——舊版後端／沒有 migration 189 時整包缺席，ENGINE 的 createBattle() 缺省視為
  // scalingMode='legacy'／levelMode='fixed'（即既有行為零改動）。monsterLevel＝這場實際使用的 N
  // （level_mode='player' 時等於玩家有效等級，由伺服器算好，前端不重算）。
  scalingMode?: 'legacy' | 'rank'
  levelMode?: 'fixed' | 'player'
  monsterLevel?: number
  /** 契約 §1：A 以上會召喚——每波召喚已經預先算好完整敵人資料，ENGINE 的 advanceSummons() 在
   *  召喚者 HP% 跨門檻時取用。選填＝舊版後端／非 rank 場次沒有召喚，缺省視為 []。 */
  summonPool?: RpgBattleSummonWaveRaw[]
}

// POST /rpg/battle/report body（純遙測，見 §2 rpg_battle_logs／§3.4 健全性檢查；不影響任何帳本/獎勵）。
export type RpgBattleOutcome = 'victory' | 'defeat' | 'draw' | 'escaped' | 'abandoned'
export interface RpgBattleReportBody {
  encounter_code: string
  outcome: RpgBattleOutcome
  duration_ms: number
  damage_dealt: number
  damage_taken: number
  enemies_defeated: number
  attacks: number
  charged_attacks: number
  skills_used: number
  items_used: number
  guard_ms: number
  player_level: number
  player_power: number
  client_version: string
}

export const rpgBattleApi = {
  encounters: (token: string) => request<RpgBattleEncounters>('/rpg/battle/encounters', { headers: withAuth(token) }),
  bootstrap: (token: string, code: string) =>
    request<RpgBattleBootstrap>(`/rpg/battle/bootstrap?code=${encodeURIComponent(code)}`, { headers: withAuth(token) }),
  // 204 No Content；呼叫端（PhoneShell）失敗只 console.warn，不擋 UI（契約 §4）。
  report: (token: string, body: RpgBattleReportBody) =>
    request<void>('/rpg/battle/report', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
}
