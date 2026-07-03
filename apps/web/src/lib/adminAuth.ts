// 後台 admin session 管理。
// 「保持登入」勾選 → access + refresh 存 localStorage（跨重啟、可自動續期，30 天內免再登入）。
// 不勾選 → access 只存 sessionStorage（關閉分頁即失效），不存 refresh。
// 存的是「token」而非密碼：不是明碼、且可在伺服器端撤銷（refresh denylist）；access TTL 60 分、refresh 30 天滑動。
import { authApi, setAuthRecovery } from './api'

const ACCESS_KEY = 'dor_admin_token'    // keep 模式在 localStorage；session 模式在 sessionStorage
const REFRESH_KEY = 'dor_admin_refresh' // 只有「保持登入」才存（localStorage）

// 近期後台 access token（現行＋剛輪替掉的）。用來讓 401 自動回復「只作用在後台 token」，不誤動會員請求。
const recent = new Set<string>()
function remember(access: string) {
  if (!access) return
  recent.add(access)
  if (recent.size > 4) recent.delete(recent.values().next().value as string)
}

export function getToken(): string | null {
  if (typeof window === 'undefined') return null
  return localStorage.getItem(ACCESS_KEY) ?? sessionStorage.getItem(ACCESS_KEY)
}
export function getRefresh(): string | null {
  if (typeof window === 'undefined') return null
  return localStorage.getItem(REFRESH_KEY)
}

// 登入成功後呼叫：依「保持登入」決定存放位置
export function setSession(access: string, refresh: string, keep: boolean) {
  if (keep) {
    localStorage.setItem(ACCESS_KEY, access)
    if (refresh) localStorage.setItem(REFRESH_KEY, refresh)
    sessionStorage.removeItem(ACCESS_KEY)
  } else {
    sessionStorage.setItem(ACCESS_KEY, access)
    localStorage.removeItem(ACCESS_KEY)
    localStorage.removeItem(REFRESH_KEY)
  }
  remember(access)
}
// 相容舊呼叫：把新 access 寫回「目前所在」的儲存
export function setToken(access: string) {
  if (localStorage.getItem(REFRESH_KEY)) localStorage.setItem(ACCESS_KEY, access)
  else sessionStorage.setItem(ACCESS_KEY, access)
  remember(access)
}
export function clearToken() {
  if (typeof window === 'undefined') return
  localStorage.removeItem(ACCESS_KEY)
  localStorage.removeItem(REFRESH_KEY)
  sessionStorage.removeItem(ACCESS_KEY)
}

// 解出 JWT 的 exp（base64url）；無法判讀回 null
function decodeExp(token: string): number | null {
  try {
    let b = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
    b += '='.repeat((4 - (b.length % 4)) % 4)
    const payload = JSON.parse(atob(b))
    return typeof payload.exp === 'number' ? payload.exp : null
  } catch { return null }
}
function isExpiringSoon(token: string, withinSec = 300): boolean {
  const exp = decodeExp(token)
  if (exp == null) return true // 判讀不了 → 當作要續期
  return exp * 1000 - Date.now() < withinSec * 1000
}

// 用 refresh token 換新 access（含 rotation）。並行守門避免重複刷新。失敗 → 清空 session。
let refreshing: Promise<string | null> | null = null
export async function refreshSession(): Promise<string | null> {
  const rt = getRefresh()
  if (!rt) return null
  if (refreshing) return refreshing
  refreshing = (async () => {
    try {
      const tp = await authApi.refresh(rt)
      setSession(tp.access_token, tp.refresh_token || rt, true)
      return tp.access_token
    } catch (e: any) {
      // 只有明確的無效 token（401/400）才視為 session 已死；暫時性錯誤（網路/5xx）保留 token 不誤登出
      if (e?.status === 401 || e?.status === 400) clearToken()
      return null
    } finally {
      refreshing = null
    }
  })()
  return refreshing
}

// 進後台時確保 access token 新鮮：過期/快過期且有 refresh → 先續期，避免頁面用過期 token 打 API 而被登出。
export async function ensureValidToken(): Promise<void> {
  const t = getToken()
  if (t && !isExpiringSoon(t)) return
  if (getRefresh()) await refreshSession()
}

// 註冊給 api.ts 的 request：任一後台請求收到 401 時自動續期並用新 token 重試一次。
// 只作用在「近期後台 token」→ 不會誤動會員請求；並行 401 也能用現行 token 重試。
setAuthRecovery(async (failedToken: string) => {
  // 只作用在後台 token：近期用過的（recent）或「目前儲存的後台 token」（重整/新分頁時 recent 為空也能回復）。
  // 會員 token 一定不等於 getToken()（未登入後台時為 null），故不會誤動會員請求。
  if (!recent.has(failedToken) && failedToken !== getToken()) return null
  const cur = getToken()
  if (cur && cur !== failedToken) return cur   // 已被並行請求刷新 → 直接用現行 token 重試
  return await refreshSession()                 // 這就是現行 token → 續期後重試（失敗回 null → 照常 401）
})
