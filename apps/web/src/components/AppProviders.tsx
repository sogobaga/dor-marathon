'use client'

import { SWRConfig } from 'swr'
import { swrLocalStorageProvider } from '@/lib/swrCache'

// 全站 SWR 設定（快取地基）。掛在 root layout，所有 useSWR 共用。
// stale-while-revalidate：切頁面立刻顯示暫存、背景默默重抓 → 感覺不到 loading；
// revalidateOnFocus：回到分頁時默默更新 → 後台改的資料跟得上、不會卡舊。
export default function AppProviders({ children }: { children: React.ReactNode }) {
  return (
    <SWRConfig
      value={{
        provider: swrLocalStorageProvider,
        keepPreviousData: true,       // 換 key 時先顯示舊資料，不閃 loading
        revalidateOnFocus: true,      // 回到分頁 → 背景重抓（後台異動跟得上）
        revalidateIfStale: true,
        dedupingInterval: 5000,       // 5 秒內同 key 不重打（快速切頁不狂抓）
        focusThrottleInterval: 10000, // focus 重抓最多每 10 秒一次
      }}
    >
      {children}
    </SWRConfig>
  )
}
