// 前台使用者 session 管理（localStorage）

import { useEffect, useState } from 'react'
import { authApi, type User } from './api'
import { clearSwrCache } from './swrCache'

const TOKEN_KEY = 'dor_user_token'
const REFRESH_KEY = 'dor_user_refresh'
const USER_KEY = 'dor_user'
const SEV_KEY = 'dor_user_sev' // session_epoch：單一登入判定用，每次登入/refresh 後端都會遞增/帶回
export const AUTH_EVENT = 'dor-auth-changed' // 登入/登出/過期清除時廣播；供外部（track 個人任務面板）監聽重載

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

export function setUserSession(accessToken: string, refreshToken: string, user: User, sessionEpoch: number) {
  localStorage.setItem(TOKEN_KEY, accessToken)
  localStorage.setItem(REFRESH_KEY, refreshToken)
  localStorage.setItem(USER_KEY, JSON.stringify(user))
  localStorage.setItem(SEV_KEY, String(sessionEpoch)) // 單一登入：記下這次登入的 session_epoch
  emitAuthChange()
}

export function clearUserSession() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(REFRESH_KEY)
  localStorage.removeItem(USER_KEY)
  localStorage.removeItem(SEV_KEY)
  clearSwrCache() // 清持久化快取：避免同裝置下一位使用者刷新後看到上一位的資料
  emitAuthChange()
}

// 讀取目前這台裝置記錄的 session_epoch（單一登入判定用；未登入或讀不到 = 0）
export function getSessionEpoch(): number {
  if (typeof window === 'undefined') return 0
  const raw = localStorage.getItem(SEV_KEY)
  const n = raw ? Number(raw) : 0
  return Number.isFinite(n) ? n : 0
}

// 用 refresh token 換發新 access token。後端 refresh 是「一次性輪替」，
// 因此多個並發呼叫必須共用同一次 refresh（否則第一個用掉舊 token、其餘失敗 → 誤登出）。
let refreshInFlight: Promise<string | null> | null = null

export function refreshUserToken(): Promise<string | null> {
  if (typeof window === 'undefined') return Promise.resolve(null)
  if (refreshInFlight) return refreshInFlight // 並發去重：共用同一次 refresh
  refreshInFlight = (async () => {
    const rt = localStorage.getItem(REFRESH_KEY)
    if (!rt) return null // 沒有 refresh token = session 已死
    try {
      const pair = await authApi.refresh(rt)
      localStorage.setItem(TOKEN_KEY, pair.access_token)
      localStorage.setItem(REFRESH_KEY, pair.refresh_token)
      localStorage.setItem(SEV_KEY, String(pair.session_epoch)) // refresh 回應也帶 session_epoch，一併更新
      return pair.access_token
    } catch (e: any) {
      // 只有「明確的無效 token」(401/400) 才視為 session 已死（回傳 null → 登出）。
      // 暫時性錯誤（網路中斷、API 重新部署中的 5xx）必須往外丟，避免誤登出。
      if (e?.status === 401 || e?.status === 400) return null
      throw e
    }
  })()
  refreshInFlight.finally(() => {
    refreshInFlight = null
  })
  return refreshInFlight
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
      let fresh: string | null = null
      try {
        fresh = await refreshUserToken()
      } catch {
        // refresh 遇到暫時性錯誤（網路/5xx）→ 保留 session、丟回原錯誤，不要登出
        throw e
      }
      if (fresh) return await fn(fresh)
      clearUserSession() // refresh 明確失敗（無/無效 refresh token）才登出
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
