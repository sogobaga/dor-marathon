/** @type {import('next').NextConfig} */
const nextConfig = {
  // 生產容器化：輸出 standalone（自帶最小 node server，映像更小）
  output: 'standalone',
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
