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
  external_data?: boolean // 是否採用 Strava 等外部數據做排名/里程統計（預設 false=只認 App GPS，合規）
  config?: RaceConfig // 後端一律回傳（非 omitempty）；此處選填僅為前端防禦
  challenge_rule?: ChallengeRule | null // 個人挑戰模式(event_mode=personal)專用規則；其餘模式為 null
  reward_config?: RewardConfig | null // 個人挑戰模式(event_mode=personal)完成觸發即時獎勵設定；其餘模式為 null，選填
  // 參賽虛擬獎勵設定（migration 140）：沿用同一 RewardConfig 結構，但觸發條件完全不同——不看任何任務/
  // 完成條件，賽事開始後由後端排程自動發給所有已報名(paid)者（見後端 race.EntryRewardConfig）。公開端點
  // （ListPublic/GetPublicDetail）一律清空，前台改走 racesApi.entryRewardPreview 取得展示用清單；選填。
  entry_reward_config?: RewardConfig | null
  created_at: string
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
let authRecovery: AuthRecovery | null = null
export function setAuthRecovery(fn: AuthRecovery | null) { authRecovery = fn }

async function request<T>(path: string, init?: RequestInit, retried = false): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })
  // 401 且尚未重試：若這次帶的是後台 token → 續期後用新 token 重試一次（避免 token 剛過期就被登出）
  if (res.status === 401 && !retried && authRecovery) {
    const h = init?.headers as Record<string, string> | undefined
    const auth = h?.Authorization // 所有呼叫都用 withAuth（大寫 Authorization），重試時原樣覆寫、不會產生重複 header
    const token = typeof auth === 'string' && auth.startsWith('Bearer ') ? auth.slice(7) : ''
    if (token) {
      const nt = await authRecovery(token)
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
}
// speed：都卜勒速度 m/s（距離防漂移的訊號分流用，見 lib/movingTime.ts）；裝置不支援時為 null。
// 上傳相容：後端以 encoding/json 解析、忽略未知欄位，多帶 speed 不影響既有 API（後端零改動）。
export interface GpsPoint { lat: number; lng: number; t: number; acc: number; speed?: number | null }
export interface GpsRunHistory {
  id: string
  distance_km: number
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
}
export const activitiesApi = {
  uploadGps: (token: string, body: { race_id?: string; started_at: string; ended_at: string; points: GpsPoint[] }) =>
    request<{ result: GpsRunResult }>('/activities/gps', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  gpsHistory: (token: string) => request<{ runs: GpsRunHistory[] }>('/activities/gps/history', { headers: withAuth(token) }),
  gpsDetail: (token: string, id: string) => request<{ run: GpsRunHistory }>(`/activities/gps/${id}`, { headers: withAuth(token) }),
  // 跑步中心跳（後台「目前在跑名單」用）；失敗可忽略
  trackPing: (token: string) => request<void>('/track/ping', { method: 'POST', headers: withAuth(token) }),
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
export const adminGpsApi = {
  list: (token: string) => request<{ runs: GpsRunSummary[] }>('/admin/gps-runs', { headers: withAuth(token) }),
  get: (token: string, id: string) => request<{ run: GpsRunSummary }>(`/admin/gps-runs/${id}`, { headers: withAuth(token) }),
  approve: (token: string, id: string) => request<void>(`/admin/gps-runs/${id}/approve`, { method: 'POST', headers: withAuth(token) }),
  reject: (token: string, id: string) => request<void>(`/admin/gps-runs/${id}/reject`, { method: 'POST', headers: withAuth(token) }),
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

  logout: (token: string, refresh_token: string) =>
    request<void>('/auth/logout', {
      method: 'DELETE',
      headers: withAuth(token),
      body: JSON.stringify({ refresh_token }),
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

export interface RegisterPayload {
  group_id?: string
  group_key?: string // 加入需鑰匙的分組時帶入
  addons?: { addon_id: string; qty: number }[]
  participant: Partial<Record<ParticipantField, string>>
  invoice?: InvoiceInfo // 電子發票資訊（選填）
  promo_code?: string
  use_coupon?: boolean // 使用 VIP 活動優惠券($100)；與 promo_code、coupon_reward_id 三擇一
  coupon_reward_id?: string // 使用活動優惠券（migration 138，user_rewards.id）；三者互斥
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
  my: { total_km: number; activities: number; ascent_m: number }
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
  preferred_data_source?: 'gps' | 'strava' // 跨來源去重偏好
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
  new_titles?: { code: string; name: string; tier: number; category: string }[] // 新解鎖稱號（前台跳彈窗用，跳完呼叫 /titles/seen）
  // 體力值 SP（跑步後依距離×強度扣、依跑步水準以時間恢復；扣到 0 凍結 6 小時）
  sp: number
  sp_max: number
  sp_recover_min: number       // 每恢復 1 點所需分鐘
  sp_next_recover_sec: number  // 距下一點恢復秒數（0=已滿）
  sp_freeze_until: string | null // 過度訓練凍結到此時間（null=無）
  fitness: number              // 跑步水準 0-100
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
  // 跨來源去重：偏好來源、首次彈窗
  setDataSource: (token: string, source: 'gps' | 'strava') =>
    request<{ ok: boolean; preferred_data_source: string }>('/profile/data-source', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ source }) }),
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
}

export interface OrderDetail extends OrderRow {
  items: OrderItemRow[]
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
