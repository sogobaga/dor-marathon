import useSWR, { mutate } from 'swr'
import { profileApi, type DashboardInfo } from './api'
import { getUserToken, useUser, withUserAuth } from './userAuth'

// 共用會員儀表板快取：首頁會員卡與會員資訊頁共用同一份（key=['dashboard', uid]），
// 只抓一次、切頁時直接用快取（不再各自 loading）。未登入 → key=null 不抓。
export function useDashboard() {
  const user = useUser()
  const uid = user?.id ?? null
  const key = uid && getUserToken() ? (['dashboard', uid] as const) : null
  const { data, isLoading, mutate: revalidate } = useSWR(
    key,
    () => withUserAuth((t) => profileApi.dashboard(t)).then((r) => r.dashboard),
  )
  return { dash: (data ?? null) as DashboardInfo | null, loading: isLoading, revalidate, user }
}

// 資料異動後呼叫（完成任務 / 獲得里程 / 得到獎勵 / 改個資 / 追蹤…）→ 讓所有用到儀表板的畫面重抓。
export function refreshDashboard() {
  return mutate((key) => Array.isArray(key) && key[0] === 'dashboard')
}
