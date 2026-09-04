import type { Metadata } from 'next'
import GoogleCompleteClient from './GoogleCompleteClient'

// 純過場頁，不該被索引；實際邏輯是純客端（讀 URL fragment），所以拆成 Server Component 這層只負責
// metadata（'use client' 檔案不能 export metadata），真正的畫面與流程在 GoogleCompleteClient.tsx。
export const metadata: Metadata = {
  title: '登入中…｜DOR',
  robots: { index: false, follow: false },
}

export default function GoogleCompletePage() {
  return <GoogleCompleteClient />
}
