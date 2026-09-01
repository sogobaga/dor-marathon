import { NextResponse } from 'next/server'

// 回報「目前部署中的前端版本」。UpdateNotice 用它和 build 時內聯的 NEXT_PUBLIC_APP_VERSION 比對，
// 不一致＝伺服器已換新版、客戶端是舊的 → 通知並自動重新整理。
// 刻意放在 /app-version 而非 /api/*：/api/:path* 會被 next.config rewrites 轉去 Go 後端，
// 而這個值必須由「Next 伺服器自己」回答才能反映前端部署狀態（web/api 是不同服務，可能有部署時差）。
// no-store：不能讓瀏覽器或 Cloudflare 快取，否則比對的是舊答案，通知永遠不會出現。
export const dynamic = 'force-dynamic'

export function GET() {
  return NextResponse.json(
    { version: process.env.NEXT_PUBLIC_APP_VERSION || 'dev' },
    { headers: { 'Cache-Control': 'no-store' } },
  )
}
