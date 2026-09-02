'use client'

import { useEffect } from 'react'

// 到站彈跳頁（src/middleware.ts）在有 URL hash 時，為了讓 location.replace() 產生「新文件」（純 hash 差異
// 在 HTML 規範裡是 fragment navigation、不會換文件），會在 query string 尾巴多塞一個 _b=1。app 這邊拿到
// 網址後把這個內部記號清掉，避免使用者看到／分享一個帶著 _b=1 的網址。
// history.replaceState（不 push）：不能多留一筆瀏覽紀錄，否則使用者按上一頁會卡在同一頁兩次。
export default function BounceCleanup() {
  useEffect(() => {
    try {
      if (window.location.search.indexOf('_b=1') === -1) return
      const params = new URLSearchParams(window.location.search)
      params.delete('_b')
      const qs = params.toString()
      const newUrl = window.location.pathname + (qs ? `?${qs}` : '') + window.location.hash
      window.history.replaceState(window.history.state, '', newUrl)
    } catch {
      /* noop */
    }
  }, [])

  return null
}
