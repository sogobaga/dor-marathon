// 版號：v<VERSION_BASE>.<commit8>。正式發布進大版號時改 VERSION_BASE（後端 internal/version 同步）。
const VERSION_BASE = '0.0'
const COMMIT = (process.env.RAILWAY_GIT_COMMIT_SHA || process.env.GIT_COMMIT || 'dev').slice(0, 8)

/** @type {import('next').NextConfig} */
const nextConfig = {
  // 生產容器化：輸出 standalone（自帶最小 node server，映像更小）
  output: 'standalone',
  // 版號於 build 時內聯到前端（client 可讀）
  env: {
    NEXT_PUBLIC_APP_VERSION: `v${VERSION_BASE}.${COMMIT}`,
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
  // PWA headers
  async headers() {
    return [
      {
        source: '/manifest.json',
        headers: [{ key: 'Cache-Control', value: 'public, max-age=86400' }],
      },
    ]
  },
}

export default nextConfig
