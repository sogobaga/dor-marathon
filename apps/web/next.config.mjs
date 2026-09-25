// 版號：v<VERSION_BASE>.<VERSION_SERIAL>.<commit8>。進大版號改 VERSION_BASE；每次推送遞增 VERSION_SERIAL
//（= git commit 累計數 `git rev-list --count HEAD`）。兩者皆需與後端 internal/version 同步。
const VERSION_BASE = '1.2'
const VERSION_SERIAL = '850'
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
    // - script-src/connect-src www.googletagmanager.com + *.google-analytics.com：GA4 gtag.js 與 beacon（lib/analytics.ts）
    // v0.1.418 起改為正式 enforce（Content-Security-Policy）；v0.1.419 依實跑 console 補三處：
    // - style-src accounts.google.com：Google 登入按鈕載入 accounts.google.com/gsi/style（GSI 樣式）
    // - script-src/connect-src static.cloudflareinsights.com/cloudflareinsights.com：Cloudflare 自動注入的 RUM beacon
    //   （程式碼裡沒有、只有站台在 Cloudflare 後面才會出現，靜態盤點抓不到，靠實跑才發現）
    // - connect-src unpkg.com：Leaflet sourcemap（僅 DevTools 開啟時抓，不影響真實使用者，順手補齊）
    // v0.1.543 起（VIP 訂閱 Phase E 前端綁卡）新增：
    // - script-src ecpg-stage.ecpay.com.tw／ecpg.ecpay.com.tw：綠界站內付2.0 綁卡 JS SDK（依環境擇一載入，
    //   見 lib/ecpay.ts loadEcpaySdk）；code.jquery.com：SDK 必要依賴 jQuery；cdn.jsdelivr.net：SDK 必要
    //   依賴 node-forge（前端加密），三者缺一 SDK 會直接 throw Error
    // - connect-src／frame-src 同兩個 ecpg 網域：SDK 內部 API 呼叫與渲染綁卡介面的 iframe
    // ⚠️ style-src 目前刻意不加 ecpg 網域：SDK 官方文件未提及注入外部樣式表，若 stage 實測 console 報
    //   style-src CSP 錯誤才補（比照本檔既有 v0.1.419 實跑補漏慣例，見上方段落）。
    // 簡章影片欄位加 FB Reel 支援後新增：
    // - frame-src www.facebook.com：FB Reel 簡章嵌入（MediaCarousel.tsx FBReelEmbed，官方免 SDK
    //   plugins/video.php iframe）。
    // 其餘：GSI 走 accounts.google.com、地圖圖磚/商家圖靠 img-src https:、Next.js 水合靠 'unsafe-inline'。
    // ⚠️ 日後若新增外部資源，務必同步加對應 directive，否則 enforce 會直接擋下。
    // DORPG 戰鬥 BGM 修復第 2 輪（2026-09-14）新增：
    // - media-src img.dor.tw：<audio src="https://img.dor.tw/dorpg/bgm/...">（lib/dorpg/audio.ts）
    //   之前一直沒播出來——CSP 沒有獨立的 media-src 指令時，<audio>/<video> 會 fallback 吃
    //   default-src 'self'，跨網域的 R2 媒體因此被整個擋下（實跑 console：Refused to load media
    //   ... 'media-src' was not explicitly set）。這是跟 iOS 手勢限制正交的另一個獨立根因，兩個都要
    //   修才會有聲音。刻意不比照 img-src 放寬成 `https:`：img-src 需要廣域是因為後台合作商家/簡章
    //   欄位允許管理者自填任意外部圖片網址（見上方 img-src 註解），但 BGM 只會從我們自己的 R2 bucket
    //   （img.dor.tw）載入，沒有「任意外部音檔網址」這種使用情境，範圍鎖到單一網域可以在不影響功能
    //   下縮小攻擊面（例如某處 XSS 注入 <audio src="https://evil.example/x.mp3"> 會被這裡擋下）。
    // 2026-09-14：本機 `npm run dev` 整站無法水合的既有問題——Next dev 的 webpack HMR 用
    // eval-source-map 載入模組，需要 'unsafe-eval'，但這份 CSP 對所有環境無條件套用同一字串，
    // dev 下所有 'use client' 元件都掛不上（console：Evaluating a string as JavaScript violates ...
    // 'unsafe-eval' is not an allowed source of script）。只在非 production 加這一項，正式環境的
    // script-src 完全不變（不放寬任何正式站的攻擊面）。
    const devScriptSrc = process.env.NODE_ENV === 'production' ? '' : " 'unsafe-eval'"
    const csp = [
      "default-src 'self'",
      "script-src 'self' 'unsafe-inline'" + devScriptSrc + " https://unpkg.com https://accounts.google.com https://apis.google.com https://www.googletagmanager.com https://static.cloudflareinsights.com https://ecpg-stage.ecpay.com.tw https://ecpg.ecpay.com.tw https://code.jquery.com https://cdn.jsdelivr.net",
      "style-src 'self' 'unsafe-inline' https://unpkg.com https://accounts.google.com",
      "img-src 'self' data: blob: https:",
      "media-src 'self' https://img.dor.tw",
      "font-src 'self' data:",
      "connect-src 'self' wss: ws: https://accounts.google.com https://www.googletagmanager.com https://www.google-analytics.com https://*.google-analytics.com https://*.analytics.google.com https://cloudflareinsights.com https://unpkg.com https://ecpg-stage.ecpay.com.tw https://ecpg.ecpay.com.tw",
      "frame-src https://www.youtube-nocookie.com https://accounts.google.com https://ecpg-stage.ecpay.com.tw https://ecpg.ecpay.com.tw https://www.facebook.com",
      "frame-ancestors 'none'",
      "base-uri 'self'",
      "form-action 'self' https://payment.ecpay.com.tw https://payment-stage.ecpay.com.tw",
      "object-src 'none'",
    ].join('; ')

    const securityHeaders = [
      { key: 'X-Content-Type-Options', value: 'nosniff' },
      { key: 'X-Frame-Options', value: 'DENY' },
      { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
      { key: 'Strict-Transport-Security', value: 'max-age=31536050; includeSubDomains' },
      { key: 'X-Permitted-Cross-Domain-Policies', value: 'none' },
      { key: 'Content-Security-Policy', value: csp },
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
