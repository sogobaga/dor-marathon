'use client'

import dynamic from 'next/dynamic'

// PhoneShell 是純 client SPA（大量 useState/useSWR + 持久化 localStorage SWR 快取）。
// 改為 ssr:false（不 SSR/不 hydrate）→ 徹底消除「持久化 SWR 快取在 client 首次 render 同步灌入舊資料、
// 而伺服器渲染的是空 loading」造成的 hydration 不一致（React #418/#423；原本 React 會 recover，但代價是把
// 整棵 SSR 結果丟掉、client 端整個重繪一次，徒增 console 錯誤與一次多餘渲染）。
// 首頁本就是登入後的 app UI、無 SEO 需求，SSR 幾乎沒帶來價值卻造成上述問題；改 client-only 後快取照常從
// 首次 client render 生效，UX 不變、反而少一次「丟棄式重繪」。track 等其他路由不受影響。
const PhoneShell = dynamic(() => import('@/components/PhoneShell'), {
  ssr: false,
  loading: () => <div style={{ minHeight: '100dvh' }} />,
})

export default function Home() {
  return (
    <main className="phone-frame">
      <PhoneShell />
    </main>
  )
}
