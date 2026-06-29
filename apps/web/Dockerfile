# 生產環境 Dockerfile — Next.js standalone 多階段建構
FROM node:24-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:24-alpine AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# next.config rewrites 在 build 時就把 API_URL 寫死，故需在 build 階段提供
# （Railway 會把服務變數當 build arg 傳入）
ARG API_URL
ENV API_URL=$API_URL
# NEXT_PUBLIC_* 於 build 時內聯，故 Google Client ID 也需在 build 階段提供
ARG NEXT_PUBLIC_GOOGLE_CLIENT_ID
ENV NEXT_PUBLIC_GOOGLE_CLIENT_ID=$NEXT_PUBLIC_GOOGLE_CLIENT_ID
# 版號 commit（Railway 提供 RAILWAY_GIT_COMMIT_SHA；build 時內聯成前端版號）
ARG RAILWAY_GIT_COMMIT_SHA
ENV RAILWAY_GIT_COMMIT_SHA=$RAILWAY_GIT_COMMIT_SHA
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:24-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1

# standalone server 自帶必要的 node_modules
COPY --from=builder /app/public ./public
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static

EXPOSE 3000
ENV PORT=3000
CMD ["node", "server.js"]
