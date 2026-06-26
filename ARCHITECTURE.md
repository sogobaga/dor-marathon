# DOR 雲端馬拉松 — 系統架構設計

> 版本 v1.0 · 2026-06-25  
> 目標：單場賽事 10,000 人並發、可橫向擴展、前後台資料連動、即時推播

---

## 目錄

1. [瓶頸分析（先於技術選型）](#1-瓶頸分析)
2. [技術選型與理由](#2-技術選型)
3. [整體架構圖](#3-整體架構圖)
4. [服務模組設計](#4-服務模組設計)
5. [資料庫 Schema 設計](#5-資料庫-schema)
6. [快取與 Redis 策略](#6-快取與-redis-策略)
7. [即時推播架構](#7-即時推播架構)
8. [部署架構（AWS）](#8-部署架構)
9. [開發路線圖](#9-開發路線圖)

---

## 1. 瓶頸分析

> 先找痛點，再選工具。以下按影響程度排序。

### 1.1 WebSocket 連線（最高優先）

**情境**：10,000 人同時觀看即時戰況，每人一條長連線。

**傳統方案的問題**：
- Java/Python/Node.js：每條 WS 連線映射一個 OS thread（或 async loop），  
  10,000 連線 × ~2MB = 20GB RAM → 不可行。
- PHP/Rails 傳統同步模型：完全不適合。

**選擇 Go 的原因**：
- Go goroutine 初始 stack ≈ 2–8 KB → 10,000 goroutines ≈ **20–80 MB RAM**。
- Go runtime 自動調度，不依賴 OS thread 數量。
- 橫向擴展時，多個 WS 實例需要用 **Redis Pub/Sub** 做跨實例廣播。

**解法**：Go WebSocket Hub + Redis Pub/Sub 跨實例廣播。

---

### 1.2 排行榜讀寫（高頻讀、低延遲要求）

**情境**：選手上傳跑步資料後，排行榜需在 1–2 秒內更新。  
10,000 人 × 每 30 秒上傳一次 = **333 req/s 的排名更新請求**。

**問題**：直接 `SELECT ... ORDER BY` 每次都掃全表 → 高延遲、DB 超載。

**解法**：
- **Redis Sorted Set (ZSET)**：`ZADD race:{raceId}:ranking {score} {userId}`
  - ZADD：O(log N)，毫秒級更新。
  - ZRANGE / ZREVRANGE：O(log N + M)，直接取前 100 名。
- 排行榜快照每 5 秒更新一次廣播給所有 WS 客戶端。
- 資料庫只負責持久化存儲，排名計算全在 Redis。

---

### 1.3 跑步資料上傳（寫入高峰）

**情境**：活動結束前 30 分鐘，大量選手同時上傳完賽資料。  
10,000 人同時寫入 = 瞬間 DB 過載。

**問題**：直接寫 PostgreSQL → 連線池耗盡（預設 max_connections=100）。

**解法**：
```
Client → API → Redis Streams（寫入緩衝）→ Worker 批次消費 → PostgreSQL
```
- 用 Redis Streams Consumer Group 做非同步批次寫入。
- API 立即回應 202 Accepted，背景處理。
- 若 Worker 崩潰，Redis Streams 保留未確認消息，自動重試。

---

### 1.4 報名爆量（瞬間搶位）

**情境**：熱門賽事報名開放時，數千人同時搶有限名額。  
問題：兩個請求同時讀到「還有 1 個名額」，各自寫入 → oversell。

**解法**：
```go
// 用 Redis 原子操作做名額扣減，再非同步寫 DB
remaining := redis.DECR("race:{id}:slots")
if remaining < 0 {
    redis.INCR("race:{id}:slots") // 歸還
    return ErrSoldOut
}
// 非同步寫 DB 確認報名
```
- 不用資料庫鎖，Redis 單執行緒保證原子性。
- DB 只做最終確認寫入（eventual consistency 可接受）。

---

### 1.5 資料庫連線池耗盡

**問題**：PostgreSQL 預設 max_connections = 100，  
多個 API 實例同時連接 → 超過上限直接報錯。

**解法**：在 PostgreSQL 前加 **PgBouncer**（連線池代理）：
- 模式：Transaction Pooling（每次 query 才借用連線）。
- 1,000 個 API 連線 → 實際只佔 PostgreSQL 30 個連線。

---

## 2. 技術選型

### 後端：Go（Golang）

| 指標 | Go | Node.js | Python |
|------|-----|---------|--------|
| 10K WS 並發 RAM | ~80 MB | ~500 MB | ~2 GB |
| 啟動時間 | <100ms | ~500ms | ~1s |
| JSON 序列化 | 極快 | 快 | 慢 |
| 靜態二進制 | ✅ | ❌ | ❌ |
| WebSocket 生態 | 成熟 | 成熟 | 弱 |
| 學習曲線 | 中 | 低 | 低 |

**結論**：Go 在並發效能與記憶體效率上明顯勝出，適合即時系統。  
框架：`Chi`（輕量路由）+ `pgx`（PostgreSQL 原生驅動）+ `gorilla/websocket`。

### 前端：Next.js 14（React + TypeScript）

- **SSR / RSC**：首屏渲染快，SEO 友好（報名頁、賽事介紹頁）。
- **PWA**：手機用戶可「加入主畫面」，接近 App 體驗。
- **API Routes**：BFF (Backend for Frontend) 層，避免前端直連 Go 服務。
- **Tailwind CSS**：快速實現設計系統 token。

### 資料庫：PostgreSQL 16

- ACID 保證（報名、付款不能有不一致）。
- JSONB 欄位：儲存任務定義、賽事規則等半結構化設定。
- 原生支援 `uuid_generate_v4()`，ID 安全不可猜。
- 部署：**Amazon Aurora PostgreSQL Serverless v2**（閒置期縮容至 0.5 ACU）。

### 快取：Redis 7

- Sorted Set → 排行榜。
- Hash → 賽事即時狀態（陣營分數、捕獲數）。
- String + TTL → JWT 黑名單、Rate Limit 計數器。
- Streams → 活動上傳非同步佇列。
- Pub/Sub → WebSocket 跨實例廣播。
- 部署：**Amazon ElastiCache Redis**（單節點開發，Cluster 模式賽事日）。

### 訊息佇列：Redis Streams

- 不引入額外組件（Kafka/RabbitMQ），用 Redis Streams 即可滿足需求。
- Consumer Group 支援：多個 Worker 並行消費，自動重試。
- 若未來規模再擴大（>10 萬人），可無縫遷移至 NATS JetStream。

---

## 3. 整體架構圖

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client Layer                             │
│  Browser / PWA (Next.js)          Admin Console (Next.js)      │
│  手機模擬框 + 即時戰況               管理後台                     │
└────────────┬──────────────────────────────┬────────────────────┘
             │ HTTPS/WSS                    │ HTTPS
             ▼                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    AWS Application Load Balancer                │
│              Sticky Session（WS）/ Round Robin（REST）           │
└────────────┬──────────────────────────────┬────────────────────┘
             │                              │
    ┌────────▼────────┐           ┌─────────▼─────────┐
    │   Go API Server │           │  Go API Server    │  ← 橫向擴展
    │   (ECS Fargate) │           │  (ECS Fargate)    │
    │                 │           │                   │
    │  ┌───────────┐  │           │  ┌─────────────┐  │
    │  │ REST API  │  │           │  │  WebSocket  │  │
    │  │ /api/v1/  │  │           │  │  Hub        │  │
    │  └───────────┘  │           │  └──────┬──────┘  │
    └────────┬────────┘           └─────────│─────────┘
             │                              │
             ▼                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Redis 7 Cluster                            │
│  ZSET: 排行榜    Hash: 賽事狀態   Streams: 上傳佇列              │
│  String: 限流    Pub/Sub: WS 廣播  TTL: Session/Token           │
└──────────────┬──────────────────────────────────────────────────┘
               │                   ▲
               │ 批次寫入           │ Pub/Sub 廣播
               ▼                   │
┌─────────────────────────────────────────────────────────────────┐
│                   Activity Worker (Go)                          │
│              Consumer Group 消費 Redis Streams                  │
│              批次寫入 PostgreSQL（每 5 秒或 100 筆）             │
└──────────────┬──────────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────────┐
│          PgBouncer (Transaction Pooling)                        │
└──────────────┬──────────────────────────────────────────────────┘
               │
               ▼
┌─────────────────────────────────────────────────────────────────┐
│        Amazon Aurora PostgreSQL Serverless v2                   │
│                                                                 │
│  users │ races │ registrations │ activities │ missions          │
│  checkins │ wheel_spins │ stickers │ rewards │ audit_logs       │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                    支援服務                                      │
│  S3: 圖片/媒體    CloudFront: CDN    SES: 報名確認信             │
│  CloudWatch: 監控  Secrets Manager: 密鑰管理                    │
└─────────────────────────────────────────────────────────────────┘
```

---

## 4. 服務模組設計

採用**模組化單體（Modular Monolith）**架構：

- 模組間透過 Go interface 溝通，不共享 package-level 變數。
- 單一 Docker 映像，部署簡單。
- 未來若單一模組成為瓶頸，可獨立拆出成 microservice。

```
dor-server/
├── cmd/
│   ├── api/          # REST API 主程序
│   └── worker/       # Activity Worker 主程序
├── internal/
│   ├── auth/         # 註冊、登入、JWT、OAuth
│   ├── race/         # 賽事 CRUD、陣營管理
│   ├── registration/ # 報名、付款整合（綠界/藍新）
│   ├── activity/     # 跑步資料上傳、配速驗證
│   ├── ranking/      # 排行榜（Redis ZSET）
│   ├── mission/      # 每日任務追蹤
│   ├── checkin/      # 門市 QR 打卡
│   ├── reward/       # 轉盤、集點卡
│   ├── realtime/     # WebSocket Hub + 推播
│   └── admin/        # 後台管理 API
├── pkg/
│   ├── db/           # pgx 連線池 + migration
│   ├── cache/        # Redis 封裝
│   ├── queue/        # Redis Streams 封裝
│   └── middleware/   # Auth、Rate Limit、CORS
└── migrations/       # SQL migration 檔案
```

### 核心 API 端點（REST）

```
POST   /api/v1/auth/register          # 會員註冊
POST   /api/v1/auth/login             # 登入（JWT）
POST   /api/v1/auth/refresh           # Refresh Token
DELETE /api/v1/auth/logout            # 登出（JWT 加黑名單）

GET    /api/v1/races                  # 賽事列表
GET    /api/v1/races/:id              # 賽事詳情
POST   /api/v1/races/:id/register    # 報名
GET    /api/v1/races/:id/ranking     # 排行榜快照
GET    /api/v1/races/:id/status      # 陣營即時分數

POST   /api/v1/activities            # 上傳跑步資料
GET    /api/v1/activities/me         # 我的活動記錄

GET    /api/v1/missions              # 今日任務
POST   /api/v1/missions/:day/complete # 完成任務（需附活動資料）

POST   /api/v1/checkins              # 門市 QR 打卡
GET    /api/v1/checkins/stores       # 附近門市

POST   /api/v1/rewards/spin          # 轉盤抽獎
GET    /api/v1/rewards/stickers      # 我的集點卡

# Admin
GET    /api/v1/admin/races           # 賽事管理
POST   /api/v1/admin/races           # 新增賽事
PATCH  /api/v1/admin/races/:id       # 更新賽事
GET    /api/v1/admin/signups         # 報名管理
POST   /api/v1/admin/broadcast       # 手動推播訊息

# WebSocket
WS     /ws/race/:raceId              # 加入即時戰況頻道
```

---

## 5. 資料庫 Schema

```sql
-- 使用者
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email       VARCHAR(255) UNIQUE NOT NULL,
    handle      VARCHAR(50) UNIQUE NOT NULL,
    name        VARCHAR(100) NOT NULL,
    password_hash TEXT NOT NULL,
    avatar_url  TEXT,
    total_km    DECIMAL(8,2) DEFAULT 0,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- 賽事
CREATE TABLE races (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        VARCHAR(50) UNIQUE NOT NULL,   -- 'hunt2026'
    title       VARCHAR(100) NOT NULL,
    subtitle    VARCHAR(100),
    world       VARCHAR(100),
    status      VARCHAR(10) NOT NULL DEFAULT 'soon', -- soon|open|live|done
    distances   INT[] NOT NULL,                -- {10,21,42}
    group_type  VARCHAR(10) NOT NULL,          -- faction|club|distance
    group_mode  VARCHAR(10) NOT NULL,          -- random|self
    slots_total INT NOT NULL DEFAULT 0,
    start_date  DATE NOT NULL,
    end_date    DATE NOT NULL,
    config      JSONB NOT NULL DEFAULT '{}',   -- factions, clubs, rules
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- 報名
CREATE TABLE registrations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    race_id     UUID NOT NULL REFERENCES races(id),
    distance    INT NOT NULL,
    faction     VARCHAR(20),                    -- 分配陣營
    team_id     UUID,
    status      VARCHAR(20) DEFAULT 'pending',  -- pending|paid|cancelled
    paid_at     TIMESTAMPTZ,
    amount      INT NOT NULL DEFAULT 0,         -- 分（NT$ 690 = 69000）
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, race_id)
);

-- 跑步活動（原始資料）
CREATE TABLE activities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    race_id     UUID REFERENCES races(id),
    mission_day INT,                            -- 對應任務日
    distance_km DECIMAL(6,3) NOT NULL,
    duration_s  INT NOT NULL,
    avg_pace_s  INT NOT NULL,                   -- 秒/公里
    gps_data    JSONB,                          -- 可選：[{lat,lng,ts}]
    recorded_at TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);
-- 分區表（按 recorded_at 月份分區，避免全表掃描）
-- 建立索引
CREATE INDEX idx_activities_user_race ON activities(user_id, race_id);
CREATE INDEX idx_activities_race_recorded ON activities(race_id, recorded_at);

-- 每日任務完成記錄
CREATE TABLE mission_completions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    race_id     UUID NOT NULL REFERENCES races(id),
    day         INT NOT NULL,
    activity_id UUID REFERENCES activities(id),
    rescue_count INT DEFAULT 0,
    completed_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(user_id, race_id, day)
);

-- 門市打卡
CREATE TABLE checkins (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    store_id    UUID NOT NULL,
    race_id     UUID REFERENCES races(id),
    stamp_earned BOOLEAN DEFAULT FALSE,
    checked_at  TIMESTAMPTZ DEFAULT NOW()
);

-- 轉盤抽獎記錄
CREATE TABLE wheel_spins (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    race_id     UUID NOT NULL REFERENCES races(id),
    result_id   VARCHAR(20) NOT NULL,           -- 'lp50','card','again'等
    result_kind VARCHAR(20) NOT NULL,
    result_amount INT DEFAULT 0,
    spun_at     TIMESTAMPTZ DEFAULT NOW()
);

-- 集點卡
CREATE TABLE user_stickers (
    user_id     UUID NOT NULL REFERENCES users(id),
    sticker_id  INT NOT NULL,                   -- 1–9
    race_id     UUID NOT NULL REFERENCES races(id),
    earned_at   TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, sticker_id, race_id)
);

-- 審計日誌（重要操作追蹤）
CREATE TABLE audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID,
    action      VARCHAR(100) NOT NULL,
    resource    VARCHAR(50),
    resource_id UUID,
    meta        JSONB,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);
```

---

## 6. 快取與 Redis 策略

### 6.1 Key 命名規範

```
race:{raceId}:ranking          ZSET  → 分數排行（score = 累積公里 × 1000）
race:{raceId}:faction_score    HASH  → {fugitive: 58, hunter: 42}
race:{raceId}:slots            STRING → 剩餘名額（原子 DECR）
race:{raceId}:status           STRING → 賽事狀態快照（JSON，5s TTL）
user:{userId}:session          STRING → JWT Refresh Token（7d TTL）
user:{userId}:spin_quota       STRING → 今日轉盤次數（00:00 重置）
ratelimit:{userId}:{action}    STRING → 限流計數（1min TTL）
activity_queue                 STREAM → 活動上傳佇列
pubsub:race:{raceId}           CHANNEL → WS 廣播頻道
```

### 6.2 排行榜更新流程

```
選手上傳跑步資料
    → API 推入 Redis Streams: activity_queue
    → 立即更新 Redis ZSET: ZADD race:{raceId}:ranking {km×1000} {userId}
    → 立即更新 Redis Hash: HINCRBYFLOAT race:{raceId}:faction_score fugitive 0.5
    → 廣播至 Redis Pub/Sub: pubsub:race:{raceId}
    → Worker 背景批次寫入 PostgreSQL（最終持久化）
```

### 6.3 快取失效策略

- 排行榜：不用 TTL，直接以 ZADD 更新（Write-Through）。
- 賽事詳情：5 分鐘 TTL，賽事狀態變更時主動 DEL。
- 使用者 Profile：10 分鐘 TTL，Profile 更新時主動 DEL。

---

## 7. 即時推播架構

### 7.1 WebSocket Hub 設計（Go）

```go
// 每場賽事一個 Hub
type RaceHub struct {
    raceID    string
    clients   map[*Client]bool     // 所有連線客戶端
    broadcast chan []byte           // 廣播訊息 channel
    join      chan *Client
    leave     chan *Client
}

// 訂閱 Redis Pub/Sub，跨實例廣播
func (h *RaceHub) subscribeRedis(rdb *redis.Client) {
    sub := rdb.Subscribe(ctx, "pubsub:race:"+h.raceID)
    for msg := range sub.Channel() {
        h.broadcast <- []byte(msg.Payload)
    }
}
```

### 7.2 推播訊息類型

```json
// 排行榜更新（每 5 秒）
{ "type": "ranking_update", "top10": [...], "my_rank": 47 }

// 陣營分數更新
{ "type": "faction_score", "fugitive": 58, "hunter": 42, "captured": 531 }

// 任務完成廣播
{ "type": "mission_complete", "user": "陳逸帆", "day": 3, "rescue": 1 }

// 管理員公告
{ "type": "announcement", "message": "..." }
```

### 7.3 客戶端設定（前端）

```typescript
// 使用者可自訂更新頻率
type RealtimeConfig = {
  enabled: boolean;          // 是否接收推播
  rankingIntervalSec: number; // 排行榜更新頻率（5|15|30|60）
  factionAlert: boolean;      // 陣營分數翻盤時通知
};

// 傳送設定給伺服器
ws.send(JSON.stringify({ type: 'config', ...userConfig }));
```

---

## 8. 部署架構（AWS）

### 8.1 服務部署

```
┌─────────────────────────────────────────────────────────┐
│                  AWS Region ap-northeast-1               │
│                                                         │
│  Route 53 (DNS)                                         │
│      ↓                                                  │
│  CloudFront (CDN) → S3 (靜態資源 / Next.js build)       │
│      ↓                                                  │
│  Application Load Balancer                              │
│      ↓                    ↓                             │
│  ECS Fargate Cluster     ECS Fargate Cluster           │
│  [Go API Server ×2]     [Go Worker ×2]                 │
│  0.5 vCPU / 1GB RAM     0.25 vCPU / 0.5GB RAM         │
│  Auto-scaling: 2–10 tasks  Auto-scaling: 2–5 tasks    │
│                                                         │
│  ElastiCache Redis (r7g.large)                          │
│  Aurora PostgreSQL Serverless v2 (0.5–8 ACU)            │
│  PgBouncer (ECS, 1 task)                                │
│                                                         │
│  Secrets Manager (DB/Redis 密鑰)                        │
│  CloudWatch Logs + Container Insights                   │
│  SES (報名確認信)                                        │
└─────────────────────────────────────────────────────────┘
```

### 8.2 Auto Scaling 策略

| 服務 | 觸發條件 | 最小 | 最大 |
|------|---------|------|------|
| Go API Server | CPU > 60% 或 WS 連線 > 2000/task | 2 | 10 |
| Go Worker | Redis Streams 積壓 > 500 | 2 | 5 |
| Aurora | 自動 (Serverless v2) | 0.5 ACU | 8 ACU |

### 8.3 費用估算（賽事日 8 小時）

| 項目 | 規格 | 月費估算 |
|------|------|---------|
| ECS Fargate API ×4 tasks | 0.5 vCPU / 1GB | ~$30 |
| ECS Fargate Worker ×2 | 0.25 vCPU / 0.5GB | ~$8 |
| Aurora Serverless v2 | avg 2 ACU | ~$60 |
| ElastiCache Redis r7g.large | 1節點 | ~$120 |
| ALB | 10K 連線 | ~$20 |
| CloudFront + S3 | 100GB | ~$10 |
| **總計** | | **~$250/月** |

> 非賽事期間（Aurora 縮容至 0.5 ACU，Fargate 縮至 2 tasks）：約 $80/月。

---

## 9. 開發路線圖

### Phase 1：基礎建設（第 1–2 週）

- [ ] Go 專案骨架（Chi router、pgx、Redis、結構化日誌）
- [ ] PostgreSQL migrations（core schema）
- [ ] JWT Auth（register / login / refresh / logout）
- [ ] Next.js 前端骨架（PWA manifest、手機模擬框）
- [ ] Docker Compose 本地開發環境（API + Worker + PostgreSQL + Redis）

### Phase 2：核心功能（第 3–5 週）

- [ ] 賽事 API（CRUD + 狀態管理）
- [ ] 報名流程（Redis 原子名額、付款 webhook 模擬）
- [ ] 跑步資料上傳（Redis Streams → Worker → PostgreSQL）
- [ ] 排行榜（Redis ZSET）
- [ ] WebSocket Hub（即時排行榜推播）

### Phase 3：遊戲功能（第 6–8 週）

- [ ] 每日任務（任務定義 JSONB + 完成驗證）
- [ ] 陣營分數（Redis Hash + 廣播）
- [ ] 門市打卡（QR code 生成 + 驗證）
- [ ] 轉盤抽獎（加權隨機 + 防重複 Redis）
- [ ] 集點卡系統

### Phase 4：管理後台（第 9–10 週）

- [ ] Admin API（賽事、報名、任務、門市管理）
- [ ] Admin Next.js 前台（對接原型設計）
- [ ] 管理員手動廣播（公告推播）
- [ ] 報表與統計（完賽率、陣營分析）

### Phase 5：上線準備（第 11–12 週）

- [ ] AWS 部署（ECS Fargate + Aurora + ElastiCache）
- [ ] CI/CD（GitHub Actions → ECR → ECS deploy）
- [ ] 壓力測試（k6 模擬 10,000 並發 WS 連線）
- [ ] 監控告警（CloudWatch Dashboard + PagerDuty）
- [ ] 付款整合（綠界 ECPay 或藍新 NewebPay）

---

## 決策摘要

| 決策 | 選擇 | 理由 |
|------|------|------|
| 後端語言 | **Go** | 10K WS goroutine ≈ 80MB，效能/記憶體最佳 |
| 後端框架 | **Chi** | 輕量、符合標準庫風格、不過度封裝 |
| 前端 | **Next.js 14** | SSR + PWA + TypeScript 型別安全 |
| 資料庫 | **PostgreSQL** | ACID + JSONB 靈活性 |
| 排行榜 | **Redis ZSET** | O(log N) 讀寫，毫秒級回應 |
| 非同步寫入 | **Redis Streams** | 不引入 Kafka，功能夠用 |
| WS 跨實例 | **Redis Pub/Sub** | 簡單可靠 |
| 部署 | **AWS ECS Fargate** | Serverless 容器，按需付費 |
| DB Serverless | **Aurora PostgreSQL v2** | 閒置縮容，節省成本 |
| 架構模式 | **模組化單體** | 起步簡單，瓶頸時可拆出 |
