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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })
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
}
export const activitiesApi = {
  uploadGps: (token: string, body: { race_id?: string; started_at: string; ended_at: string; points: GpsPoint[] }) =>
    request<{ result: GpsRunResult }>('/activities/gps', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  gpsHistory: (token: string) => request<{ runs: GpsRunHistory[] }>('/activities/gps/history', { headers: withAuth(token) }),
  gpsDetail: (token: string, id: string) => request<{ run: GpsRunHistory }>(`/activities/gps/${id}`, { headers: withAuth(token) }),
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
export interface EventDef {
  id?: string
  name: string
  description?: string
  enabled: boolean
  weight: number
  cooldown_sec: number
  trigger_type: string
  trigger_params: Record<string, number>
  completion_type: string
  completion_params: Record<string, number>
  message: string
  reward_exp: number
  reward_dp: number
}
export const eventApi = {
  active: (token: string) => request<{ defs: EventDef[] }>('/events/active', { headers: withAuth(token) }),
  createOccurrence: (token: string, body: { def_id: string; trigger_dist_m: number; trigger_elapsed_s: number }) =>
    request<{ id: string; reward_exp: number; reward_dp: number }>('/events/occurrences', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  complete: (token: string, id: string, body: { moved_m: number; window_s: number }) =>
    request<{ completed: boolean; reward_exp?: number; reward_dp?: number; message?: string }>(`/events/occurrences/${id}/complete`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  fail: (token: string, id: string) => request<void>(`/events/occurrences/${id}/fail`, { method: 'POST', headers: withAuth(token) }),
}
export const adminEventsApi = {
  list: (token: string) => request<{ defs: EventDef[]; trigger_catalog: EventTypeSpec[]; completion_catalog: EventTypeSpec[] }>('/admin/events', { headers: withAuth(token) }),
  create: (token: string, body: EventDef) => request<{ def: EventDef }>('/admin/events', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: EventDef) => request<{ def: EventDef }>(`/admin/events/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) => request<void>(`/admin/events/${id}`, { method: 'DELETE', headers: withAuth(token) }),
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
  reward_exp: number
  reward_dp: number
  per_user_daily_cap: number
}

// WS 邀請 payload
export interface RaceEventInvite {
  instance_id: string
  target_user_ids: string[]
  initiator_name: string
  name: string
  message: string
  completion_type: string
  completion_params: Record<string, number>
  join_window_s: number
  reward_exp: number
  reward_dp: number
  join_deadline: number // epoch ms
}

export const eventRaceApi = {
  context: (token: string) => request<{ races: { id: string; title: string }[] }>('/events/race/context', { headers: withAuth(token) }),
  trigger: (token: string, body: { race_id: string; moved_m: number; elapsed_s: number }) =>
    request<{ triggered: boolean; instance_id?: string; targets?: number }>('/events/race/trigger', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  join: (token: string, instID: string) =>
    request<{ joined: boolean; message?: string; name?: string; completion_type?: string; completion_params?: Record<string, number>; reward_exp?: number; reward_dp?: number; deadline?: number }>(`/events/race/instances/${instID}/join`, { method: 'POST', headers: withAuth(token) }),
  complete: (token: string, instID: string, body: { moved_m: number; window_s: number }) =>
    request<{ completed: boolean; reward_exp?: number; reward_dp?: number; message?: string; capped?: boolean }>(`/events/race/instances/${instID}/complete`, { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  fail: (token: string, instID: string) => request<void>(`/events/race/instances/${instID}/fail`, { method: 'POST', headers: withAuth(token) }),
}

export const adminEventRacesApi = {
  list: (token: string) => request<{ defs: RaceEventDef[]; completion_catalog: EventTypeSpec[]; group_rel_options: RelOption[]; follow_rel_options: RelOption[]; gender_rel_options: RelOption[] }>('/admin/event-races', { headers: withAuth(token) }),
  create: (token: string, body: RaceEventDef) => request<{ def: RaceEventDef }>('/admin/event-races', { method: 'POST', headers: withAuth(token), body: JSON.stringify(body) }),
  update: (token: string, id: string, body: RaceEventDef) => request<{ def: RaceEventDef }>(`/admin/event-races/${id}`, { method: 'PUT', headers: withAuth(token), body: JSON.stringify(body) }),
  remove: (token: string, id: string) => request<void>(`/admin/event-races/${id}`, { method: 'DELETE', headers: withAuth(token) }),
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
  // 賽事進度（任務達成度 + 個人統計；帶 token 則含個人）
  progress: (raceID: string, token?: string) =>
    request<{ progress: RaceProgress }>(`/races/${raceID}/progress`, token ? { headers: withAuth(token) } : undefined),
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
}

export interface DashboardInfo {
  name: string
  nickname: string
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
  total_km: number
  race_count: number
  ongoing_count: number
  completed_count: number
  following_count: number
  follower_count: number
}

export interface FollowRow {
  user_id: string
  nickname: string
  account_code: string
  avatar_url: string
}

export interface LeaderboardRow {
  rank: number
  user_id: string
  nickname: string
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
  account_code: string
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

// --- 金流（綠界 ECPay）---

export interface EcpayCheckout {
  action_url: string
  params: Record<string, string>
}

export const paymentsApi = {
  // 取得綠界結帳表單參數（前端據此 POST 表單導去綠界）
  ecpayCheckout: (token: string, orderID: string) =>
    request<EcpayCheckout>('/payments/ecpay/checkout', {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify({ order_id: orderID }),
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
}

export interface MemberDetail extends MemberSummary {
  nickname: string
  address: string
  birthday: string
  race_count: number
  exp: number
  level: number
  level_title: string
  is_vip: boolean
  vip_expires_at?: string
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

// --- WebSocket helper ---

export function createRaceSocket(raceID: string, accessToken: string): WebSocket {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const host = window.location.host
  const url = `${protocol}//${host}/ws/race/${raceID}?token=${accessToken}`
  return new WebSocket(url)
}
