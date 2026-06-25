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
  const data = await res.json()
  if (!res.ok) throw new ApiError(res.status, data.error ?? 'request failed')
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

// --- WebSocket helper ---

export function createRaceSocket(raceID: string, accessToken: string): WebSocket {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const host = window.location.host
  const url = `${protocol}//${host}/ws/race/${raceID}?token=${accessToken}`
  return new WebSocket(url)
}
