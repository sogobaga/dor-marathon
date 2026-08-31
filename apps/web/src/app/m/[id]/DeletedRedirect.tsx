'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'

// 團練分享短網址 /m/{id}：後端 share 端點回 { available:false, deleted:true } 時使用（見 page.tsx）。
// 純 SSR 的 page.tsx 沒有 client-side 導頁能力，所以拆這個極簡子元件單獨標 'use client'——
// 3 秒倒數（顯示剩餘秒數）結束就回「團練邀請」頁（?runmeet=list，非首頁——
// 使用者是為了看某個團練才點進來的，回列表才接得上情境），與 RunMeetDetailView.tsx 詳情頁 410 時
// 的行為、文案完全一致（同一句「該團練已被刪除，N 秒後將返回團練邀請頁。」）。
export default function DeletedRedirect() {
  const router = useRouter()
  const [sec, setSec] = useState(3)

  useEffect(() => {
    if (sec <= 0) { router.push('/?runmeet=list'); return }
    const t = setTimeout(() => setSec((s) => s - 1), 1000)
    return () => clearTimeout(t)
  }, [sec, router])

  return (
    <div style={{ fontSize: 18, fontWeight: 800, lineHeight: 1.7 }}>
      該團練已被刪除，{sec} 秒後將返回團練邀請頁。
    </div>
  )
}
