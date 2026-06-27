'use client'

import { GoogleOAuthProvider } from '@react-oauth/google'

const CLIENT_ID = process.env.NEXT_PUBLIC_GOOGLE_CLIENT_ID || ''

// build 時是否已設定 Google Client ID（NEXT_PUBLIC_* 於 build 內聯）
export const googleConfigured = CLIENT_ID.length > 0

export default function GoogleAuthProvider({ children }: { children: React.ReactNode }) {
  // 未設定 Client ID 時不啟用 provider，頁面照常運作
  if (!googleConfigured) return <>{children}</>
  return <GoogleOAuthProvider clientId={CLIENT_ID}>{children}</GoogleOAuthProvider>
}
