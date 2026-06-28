// 前台使用者 session 管理（localStorage）

import { authApi, type User } from './api'

const TOKEN_KEY = 'dor_user_token'
const REFRESH_KEY = 'dor_user_refresh'
const USER_KEY = 'dor_user'

export function getUserToken(): string | null {
  if (typeof window === 'undefined') return null
  return localStorage.getItem(TOKEN_KEY)
}

export function getUser(): User | null {
  if (typeof window === 'undefined') return null
  const raw = localStorage.getItem(USER_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw) as User
  } catch {
    return null
  }
}

export function setUserSession(accessToken: string, refreshToken: string, user: User) {
  localStorage.setItem(TOKEN_KEY, accessToken)
  localStorage.setItem(REFRESH_KEY, refreshToken)
  localStorage.setItem(USER_KEY, JSON.stringify(user))
}

export function clearUserSession() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(REFRESH_KEY)
  localStorage.removeItem(USER_KEY)
}

// 用 refresh token 換發新的 access token（access token 僅 15 分鐘）。
// 成功回傳新 access token 並更新 localStorage；失敗回 null。
export async function refreshUserToken(): Promise<string | null> {
  if (typeof window === 'undefined') return null
  const rt = localStorage.getItem(REFRESH_KEY)
  if (!rt) return null
  try {
    const pair = await authApi.refresh(rt)
    localStorage.setItem(TOKEN_KEY, pair.access_token)
    localStorage.setItem(REFRESH_KEY, pair.refresh_token)
    return pair.access_token
  } catch {
    return null
  }
}

// 包裝需登入的 API 呼叫：token 過期（401）時自動 refresh 後重試一次。
// refresh 也失敗則清除 session 並丟出 SessionExpiredError。
export class SessionExpiredError extends Error {
  constructor() {
    super('登入已過期，請重新登入')
    this.name = 'SessionExpiredError'
  }
}

export async function withUserAuth<T>(fn: (token: string) => Promise<T>): Promise<T> {
  const token = getUserToken()
  if (!token) throw new SessionExpiredError()
  try {
    return await fn(token)
  } catch (e: any) {
    if (e?.status === 401) {
      const fresh = await refreshUserToken()
      if (fresh) return await fn(fresh)
      clearUserSession()
      throw new SessionExpiredError()
    }
    throw e
  }
}
