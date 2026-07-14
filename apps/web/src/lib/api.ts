// API client — 封裝所有對 Go API 的呼叫

const BASE = '/api/v1'

export interface TokenPair {
  access_token: string
  refresh_token: string
  expires_in: number
}

export interface User {
  id: string
  email: string
  handle: string
  name: string
  avatar_url: string
  total_km: number
}

export type EventMode = 'general' | 'competition' | 'faction_battle'
export type GoalType = 'cumulative' | 'distance'

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
  entry_fee: number
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
  review_status: string
  certificate_bg_url?: string
  show_distance_rank?: boolean
  show_time_rank?: boolean
  vip_only?: boolean // VIP 限定賽事（只提供給 VIP 帳號）
  created_at: string
}

export type ControlStatus = 'active' | 'paused' | 'suspended' | 'closed' | 'hidden' | 'testing'
export type DisplayStatus =
  | 'upcoming_reg' | 'registering' | 'reg_closed'
  | 'starting_soon' | 'racing' | 'ended'
  | 'paused' | 'suspended'

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
}
export interface GpsPoint { lat: number; lng: number; t: number; acc: number }
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

export const authApi = {
  register: (body: { email: string; handle: string; name: string; password: string }) =>
    request<{ user: User; tokens: TokenPair }>('/auth/register', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  login: (body: { email: string; password: string }) =>
    request<{ user: User; tokens: TokenPair }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  // Google 登入（GIS ID-token）
  google: (id_token: string) =>
    request<{ user: User; tokens: TokenPair }>('/auth/google', {
      method: 'POST',
      body: JSON.stringify({ id_token }),
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
}

export interface RegistrationState {
  id: string
  group_id?: string
  group_revealed: boolean
  group_name?: string
  status: string
  amount: number
}

export interface RegisterPayload {
  group_id?: string
  group_key?: string // 加入需鑰匙的分組時帶入
  addons?: { addon_id: string; qty: number }[]
  participant: Partial<Record<ParticipantField, string>>
  promo_code?: string
  use_coupon?: boolean // 使用 VIP 活動優惠券($100)；與 promo_code 擇一
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
  promoCheck: (raceID: string, token: string, body: { code: string; addons?: { addon_id: string; qty: number }[] }) =>
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

// --- Admin: 數據總覽 ---
export interface OverviewRace {
  id: string
  title: string
  display_status: string
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
  level: number
  level_title: string
  level_floor: number
  next_level_exp: number | null
  is_vip: boolean
  vip_expires_at?: string
  vip_plan: '' | 'trial' | 'monthly' | 'annual' // 訂閱方案（''=無）
  activity_coupon_balance: number               // 活動優惠券($100)剩餘張數
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

export const achievementApi = {
  stats: (token: string) => request<AchievementStats>('/profile/achievements', { headers: withAuth(token) }),
  calendar: (token: string, month: string) =>
    request<AchievementCalendar>(`/profile/achievements/calendar?month=${encodeURIComponent(month)}`, { headers: withAuth(token) }),
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
  order: (token: string, orderID: string) =>
    request<{ order: MyOrder }>(`/profile/orders/${orderID}`, { headers: withAuth(token) }),
  follows: (token: string) =>
    request<{ following: FollowRow[]; following_count: number; follower_count: number }>('/profile/follows', { headers: withAuth(token) }),
  recommendations: (token: string, raceID: string) =>
    request<{ recommendations: RecommendRow[] }>(`/profile/recommendations/${raceID}`, { headers: withAuth(token) }),
}

export const followApi = {
  follow: (token: string, userId: string) =>
    request<{ following: boolean }>('/profile/follow', { method: 'POST', headers: withAuth(token), body: JSON.stringify({ user_id: userId }) }),
  unfollow: (token: string, userId: string) =>
    request<null>(`/profile/follow/${userId}`, { method: 'DELETE', headers: withAuth(token) }),
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

export const adminPersonalTasksApi = {
  list: (token: string) =>
    request<{ plans: PersonalPlan[]; tasks: PersonalTask[] }>('/admin/personal-tasks', { headers: withAuth(token) }),
  import: (token: string, body: { plans: unknown[]; tasks: unknown[] }) =>
    request<{ plans: number; tasks: number }>('/admin/personal-tasks/import', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify(body),
    }),
}

// 城市探索：打卡點關主
export interface ExploreBoss {
  id: string; code: string; name: string; title: string; region: string; place: string
  gender: string; age: number; workout_label: string; difficulty_stars: number
  quote: string; skill_name: string; skill_desc: string; dialogue_intro: string; dialogue_start: string
  scene_image_url: string; card_image_url: string
  lat: number; lng: number; radius_m: number
  reward_exp: number; reward_dp: number; retry_dp_cost: number
  workout_kind: string; segments: WorkoutSegment[] | null; data_source: string
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

// 城市探索（前台）：啟用中的關主 + 我的進度
export const exploreApi = {
  list: (token: string) => request<{ bosses: ExploreBoss[] }>('/explore', { headers: withAuth(token) }),
  // 到打卡點打卡 → 揭露關主（回完整關主資料）；純打卡點則不揭露，僅回 checkin_only/place/already
  checkin: (token: string, id: string, body: { lat: number; lng: number; acc: number }) =>
    request<{ ok: boolean; status: string; distance_m?: number; message?: string; boss?: ExploreBoss; checkin_only?: boolean; place?: string; already?: boolean }>(
      `/explore/${id}/checkin`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  // 接受挑戰（扣 DP=難度×10）→ 帶到課表挑戰
  accept: (token: string, id: string) =>
    request<{ ok: boolean; tier: number; charged_dp: number }>(`/explore/${id}/accept`, { method: 'POST', headers: withAuth(token) }),
  // 完成挑戰（由 /track 分段引擎回報）→ 得星、3★ 取得卡片、回傳本趟完成時間(秒)
  complete: (token: string, id: string, body: { finished: boolean; work_in_band: number; work_total: number }) =>
    request<{ completed: boolean; stars: number; card_obtained: boolean; reward_exp: number; reward_dp: number; time_s: number }>(
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
  best_time_s: number // 最短一次完成挑戰的秒數（時間榜排序值）
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
}

export interface MemberDetail extends MemberSummary {
  nickname: string
  address: string
  birthday: string
  race_count: number
  exp: number
  level: number
  level_title: string
  athlete: AthleteStats
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
  list: (token: string, params?: { q?: string; limit?: number; offset?: number }) => {
    const qs = new URLSearchParams()
    if (params?.q) qs.set('q', params.q)
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.offset) qs.set('offset', String(params.offset))
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
  // 模擬加里程（測試用）：推一筆活動 → worker 寫入並發日常里程 EXP
  addMileage: (token: string, userID: string, distanceKm: number) =>
    request<void>('/admin/activities/add-mileage', {
      method: 'POST', headers: withAuth(token), body: JSON.stringify({ user_id: userID, distance_km: distanceKm }),
    }),
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
}

export interface OrderDetail extends OrderRow {
  items: OrderItemRow[]
}

export const adminSignupsApi = {
  list: (token: string, params: { race_id: string; q?: string }) => {
    const qs = new URLSearchParams({ race_id: params.race_id })
    if (params.q) qs.set('q', params.q)
    return request<{ signups: SignupRow[]; count: number; groups: RaceGroup[] }>(`/admin/signups?${qs.toString()}`, {
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
  list: (token: string, params?: { race_id?: string; status?: string }) => {
    const qs = new URLSearchParams()
    if (params?.race_id) qs.set('race_id', params.race_id)
    if (params?.status) qs.set('status', params.status)
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
