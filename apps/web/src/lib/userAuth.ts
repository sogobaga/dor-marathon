// 前台使用者 session 管理（localStorage）

import { useEffect, useState } from 'react'
import { authApi, type User } from './api'

const TOKEN_KEY = 'dor_user_token'
const REFRESH_KEY = 'dor_user_refresh'
const USER_KEY = 'dor_user'
const AUTH_EVENT = 'dor-auth-changed'

// 通知所有訂閱者登入狀態變了（登入/登出/session 過期清除）
function emitAuthChange() {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(AUTH_EVENT))
}

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
  emitAuthChange()
}

export function clearUserSession() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(REFRESH_KEY)
  localStorage.removeItem(USER_KEY)
  emitAuthChange()
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

// 開啟 app 時主動驗證 session：有 token 就打 /auth/me（401 會自動 refresh 重試），
// 成功 = 維持登入（並順便更新使用者資料）；失敗 = session 已被清除（一開始就顯示未登入）。
export async function validateSession(): Promise<boolean> {
  if (!getUserToken()) return false
  try {
    const user = await withUserAuth((t) => authApi.me(t))
    // 更新快取的使用者資料（名稱/頭像可能有變）
    if (typeof window !== 'undefined') {
      localStorage.setItem(USER_KEY, JSON.stringify(user))
      emitAuthChange()
    }
    return true
  } catch {
    return false // withUserAuth 在 refresh 失敗時已清除 session
  }
}

// 反應式讀取登入使用者：登入/登出/過期時自動重新渲染，讓 header 與各頁狀態一致。
export function useUser(): User | null {
  const [user, setUser] = useState<User | null>(null)
  useEffect(() => {
    setUser(getUser()) // 掛載後才讀 localStorage（避免 SSR 不一致）
    const handler = () => setUser(getUser())
    window.addEventListener(AUTH_EVENT, handler)
    return () => window.removeEventListener(AUTH_EVENT, handler)
  }, [])
  return user
}
