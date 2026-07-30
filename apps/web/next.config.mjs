// 版號：v<VERSION_BASE>.<VERSION_SERIAL>.<commit8>。進大版號改 VERSION_BASE；每次推送遞增 VERSION_SERIAL
//（= git commit 累計數 `git rev-list --count HEAD`）。兩者皆需與後端 internal/version 同步。
const VERSION_BASE = '0.1'
const VERSION_SERIAL = '411'
const COMMIT = (process.env.RAILWAY_GIT_COMMIT_SHA || process.env.GIT_COMMIT || 'dev').slice(0, 8)

/** @type {import('next').NextConfig} */
const nextConfig = {
  // 生產容器化：輸出 standalone（自帶最小 node server，映像更小）
  output: 'standalone',
  // 版號於 build 時內聯到前端（client 可讀）
  env: {
    NEXT_PUBLIC_APP_VERSION: `v${VERSION_BASE}.${VERSION_SERIAL}.${COMMIT}`,
  },
  // 重寫 API 請求到 Go 後端（開發環境）
  async rewrites() {
    return [
      {
        source: '/api/:path*',
        destination: (process.env.API_URL || 'http://localhost:8080') + '/api/:path*',
      },
      {
        source: '/ws/:path*',
        destination: (process.env.API_URL || 'http://localhost:8080') + '/ws/:path*',
      },
    ]
  },
  // PWA headers ＋ 全站安全標頭（SEC-H3）
  async headers() {
    // CSP 依實際 grep 到的外部資源組成：
    // - script-src/style-src unpkg.com：Leaflet CDN（src/lib/leaflet.ts 載入 leaflet@1.9.4 css+js）
    // - script-src/connect-src/frame-src accounts.google.com：GoogleAuthProvider.tsx 用 @react-oauth/google（GSI）
    // - frame-src www.youtube-nocookie.com：MediaCarousel.tsx 嵌入 YouTube（一律走 nocookie 網域）
    // - img-src https:：合作商家後台（admin/partners/page.tsx）banner_url/photo_urls 為管理者自填外部圖片網址；
    //   另有 /api/v1/images 站內圖與 data:/blob: 預覽
    // - connect-src wss:／ws:：createRaceSocket/createSiteSocket（lib/api.ts）皆為 same-origin WebSocket
    // - form-action payment.ecpay.com.tw／payment-stage.ecpay.com.tw：submitEcpayForm（lib/ecpay.ts）導轉綠界結帳
    // 目前用 Content-Security-Policy-Report-Only（只回報、不阻擋）：先觀察一段時間、確認未誤傷正常功能
    // （尤其 Next.js 水合所需的 inline script/style、上述各外部資源）後，再改成正式 enforce 的
    // Content-Security-Policy 標頭。
    const csp = [
      "default-src 'self'",
      "script-src 'self' 'unsafe-inline' https://unpkg.com https://accounts.google.com https://apis.google.com",
      "style-src 'self' 'unsafe-inline' https://unpkg.com",
      "img-src 'self' data: blob: https:",
      "font-src 'self' data:",
      "connect-src 'self' wss: ws: https://accounts.google.com",
      "frame-src https://www.youtube-nocookie.com https://accounts.google.com",
      "frame-ancestors 'none'",
      "base-uri 'self'",
      "form-action 'self' https://payment.ecpay.com.tw https://payment-stage.ecpay.com.tw",
      "object-src 'none'",
    ].join('; ')

    const securityHeaders = [
      { key: 'X-Content-Type-Options', value: 'nosniff' },
      { key: 'X-Frame-Options', value: 'DENY' },
      { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
      { key: 'Strict-Transport-Security', value: 'max-age=31536000; includeSubDomains' },
      { key: 'X-Permitted-Cross-Domain-Policies', value: 'none' },
      { key: 'Content-Security-Policy-Report-Only', value: csp },
    ]

    return [
      {
        source: '/:path*',
        headers: securityHeaders,
      },
      {
        source: '/manifest.json',
        headers: [{ key: 'Cache-Control', value: 'public, max-age=86400' }],
      },
    ]
  },
}

export default nextConfig
