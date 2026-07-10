// 版號：v<VERSION_BASE>.<VERSION_SERIAL>.<commit8>。進大版號改 VERSION_BASE；每次推送遞增 VERSION_SERIAL
//（= git commit 累計數 `git rev-list --count HEAD`）。兩者皆需與後端 internal/version 同步。
const VERSION_BASE = '0.1'
const VERSION_SERIAL = '282'
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
