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
  review_status: string
  created_at: string
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

export interface RaceDetail extends Race {
  groups: RaceGroup[]
  addons: RaceAddon[]
  supplies: RaceSupply[]
}

// 建立賽事的巢狀 payload（Race 基本欄位 + 子陣列）
export type CreateRacePayload = Partial<Race> & {
  groups: RaceGroup[]
  addons: RaceAddon[]
  supplies: RaceSupply[]
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

export interface RegistrationState {
  id: string
  group_id?: string
  group_revealed: boolean
  status: string
  amount: number
}

export interface RegisterPayload {
  group_id?: string
  addons?: { addon_id: string; qty: number }[]
  participant: Partial<Record<ParticipantField, string>>
}

export interface RegisterResult {
  registration: RegistrationState
  order: { id: string; total_cents: number; status: string }
  assigned_group: string
  group_revealed: boolean
}

export interface MyRegLite {
  status: string // pending|paid|cancelled
  group_revealed: boolean
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
    request<{ race: RaceDetail; registration: RegistrationState | null }>(
      `/races/${raceID}`,
      token ? { headers: withAuth(token) } : undefined
    ),
  register: (raceID: string, token: string, payload: RegisterPayload) =>
    request<RegisterResult>(`/races/${raceID}/register`, {
      method: 'POST',
      headers: withAuth(token),
      body: JSON.stringify(payload),
    }),
  // 競賽排行榜（公開；帶 token 則附自己分組名次）
  standings: (raceID: string, token?: string) =>
    request<CompetitionRanking>(`/races/${raceID}/standings`, token ? { headers: withAuth(token) } : undefined),
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
}

// --- 個人資訊 (Profile) ---

export interface Profile {
  user_id: string
  email: string
  real_name: string
  nickname: string
  phone: string
  address: string
  birthday: string // YYYY-MM-DD
  gender: '' | 'male' | 'female' | 'other'
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
  created_at: string
}

export interface MemberDetail extends MemberSummary {
  nickname: string
  address: string
  birthday: string
  race_count: number
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
