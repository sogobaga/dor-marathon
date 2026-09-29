import useSWR, { mutate } from 'swr'
import { profileApi, type DashboardInfo } from './api'
import { getUserToken, useUser, withUserAuth } from './userAuth'

// 共用會員儀表板快取：首頁會員卡與會員資訊頁共用同一份（key=['dashboard', uid]），
// 只抓一次、切頁時直接用快取（不再各自 loading）。未登入 → key=null 不抓。
export function useDashboard() {
  const user = useUser()
  const uid = user?.id ?? null
  const key = uid && getUserToken() ? (['dashboard', uid] as const) : null
  const { data, error, isLoading, mutate: revalidate } = useSWR(
    key,
    () => withUserAuth((t) => profileApi.dashboard(t)).then((r) => r.dashboard),
  )
  // error 額外暴露給 components/SkinOverride.tsx：它需要分辨「還在抓資料中」跟「這次請求失敗」，
  // 兩者都不能拿來當「確定沒有風格覆寫」的定論（見該檔案 sync() 的 gate 註解）。既有呼叫端都只解構
  // 自己要的欄位，多這個欄位不影響任何人。
  return { dash: (data ?? null) as DashboardInfo | null, loading: isLoading, error, revalidate, user }
}

// 資料異動後呼叫（完成任務 / 獲得里程 / 得到獎勵 / 改個資 / 追蹤…）→ 讓所有用到儀表板的畫面重抓。
export function refreshDashboard() {
  return mutate((key) => Array.isArray(key) && key[0] === 'dashboard')
}
