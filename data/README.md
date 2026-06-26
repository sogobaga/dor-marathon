# DOR 雲端馬拉松 — 資料庫設計文件 v1.0

## 目錄結構

```
data/
├── schema.sql          ← DDL：所有 CREATE TABLE（請先執行此檔）
├── README.md           ← 本文件
└── seeds/              ← 種子資料（依數字前綴順序匯入）
    ├── 01_users.json
    ├── 02_user_profiles.json
    ├── 03_user_levels.json
    ├── 04_races.json
    ├── 05_race_distance_groups.json
    ├── 06_race_factions.json
    ├── 07_race_faction_settings.json
    ├── 08_race_clubs.json
    ├── 09_race_mileage_rules.json
    ├── 10_guilds.json
    ├── 11_guild_members.json
    ├── 12_registrations.json
    ├── 13_orders.json
    ├── 14_user_group_assignments.json
    ├── 15_mission_templates.json
    ├── 16_user_mission_progress.json
    ├── 17_stores.json
    ├── 18_race_stores.json
    ├── 19_activity_logs.json
    ├── 20_race_mileage_accumulation.json
    ├── 21_faction_daily_standings.json
    ├── 22_data_sync_connections.json
    ├── 23_checkins.json
    ├── 24_wheel_pools.json
    ├── 25_wheel_spins.json
    ├── 26_sticker_templates.json
    ├── 27_user_stickers.json
    ├── 28_prize_redemptions.json
    ├── 29_race_results.json
    ├── 30_notification_templates.json
    ├── 31_notification_logs.json
    └── 32_reward_transactions.json
```

---

## 資料表一覽（共 33 張）

| # | 資料表 | 說明 | 相依 |
|---|--------|------|------|
| 1 | `users` | 帳號與認證 | — |
| 2 | `user_profiles` | 個人資料 | users |
| 3 | `user_levels` | 等級/EXP/統計快取 | users |
| 4 | `auth_sessions` | 登入 Session | users |
| 5 | `races` | 賽事主表 | users |
| 6 | `race_distance_groups` | 各賽事距離/費用 | races |
| 7 | `race_factions` | 陣營定義 | races |
| 8 | `race_faction_settings` | 陣營玩法參數 | races |
| 9 | `race_clubs` | 公會/跑團定義 | races |
| 10 | `race_mileage_rules` | 里程銀行規則 | races |
| 11 | `guilds` | 跑團（跨賽事） | users |
| 12 | `guild_members` | 跑團成員 | guilds, users |
| 13 | `registrations` | 報名紀錄 | users, races, race_distance_groups |
| 14 | `orders` | 付款訂單 | registrations, users |
| 15 | `user_group_assignments` | 陣營/公會指派 | users, races, race_factions, race_clubs, guilds |
| 16 | `mission_templates` | 每日任務定義 | races |
| 17 | `user_mission_progress` | 任務完成進度 | users, mission_templates |
| 18 | `activity_logs` | 個人跑步紀錄 | users, races |
| 19 | `race_mileage_accumulation` | 每人每賽事里程總計 | users, races |
| 20 | `faction_daily_standings` | 每日陣營戰況快照 | races, race_factions |
| 21 | `data_sync_connections` | 健康平台授權 | users |
| 22 | `stores` | 門市資訊 | — |
| 23 | `race_stores` | 賽事×門市關聯 | races, stores |
| 24 | `checkins` | 使用者打卡紀錄 | users, races, stores |
| 25 | `wheel_pools` | 轉盤獎項設定 | races |
| 26 | `wheel_spins` | 轉盤記錄 | users, races, wheel_pools |
| 27 | `sticker_templates` | 九宮格集點定義 | races |
| 28 | `user_stickers` | 已收集貼紙 | users, sticker_templates |
| 29 | `prize_redemptions` | 公仔/獎品出貨 | users, races |
| 30 | `race_results` | 完賽紀錄 | users, races, registrations |
| 31 | `notification_templates` | 推播模板 | races |
| 32 | `notification_logs` | 推播歷史紀錄 | notification_templates, users, races |
| 33 | `reward_transactions` | 獎勵進出帳本 | users, races |

---

## ER 關係概覽

```
users ──┬── user_profiles          (1:1)
        ├── user_levels             (1:1)
        ├── auth_sessions           (1:N)
        ├── registrations           (1:N) ──── orders (1:1)
        ├── user_group_assignments  (1:N, per race)
        ├── user_mission_progress   (1:N)
        ├── activity_logs           (1:N)
        ├── race_mileage_accumulation (1:N, per race)
        ├── data_sync_connections   (1:N)
        ├── checkins                (1:N)
        ├── wheel_spins             (1:N)
        ├── user_stickers           (1:N)
        ├── prize_redemptions       (1:N)
        ├── race_results            (1:N, per race)
        └── reward_transactions     (1:N)

races ──┬── race_distance_groups    (1:N)
        ├── race_factions           (1:N, 若 group_type='faction')
        ├── race_faction_settings   (1:1, 若 group_type='faction')
        ├── race_clubs              (1:N, 若 group_type='club')
        ├── race_mileage_rules      (1:N)
        ├── mission_templates       (1:N, 每天一筆)
        ├── faction_daily_standings (1:N, 每天×每陣營)
        ├── race_stores             (M:N 透過 junction)
        └── wheel_pools             (1:N)

guilds ─┬── guild_members           (M:N 透過 junction)
        └── user_group_assignments  (optional FK)
```

---

## 正規化設計說明

### 為何將 user_profiles 與 users 分開（2NF 應用）

`users` 只存認證資料（email、password_hash）；個人資料（姓名、地址、身分證）放 `user_profiles`。理由：

- **安全性**：若認證表洩漏，不包含個人識別資訊（PII）
- **擴充性**：未來可加入企業帳號類型，不需改認證表

### registrations 內的 ship_* 欄位（Snapshot Pattern）

報名時將寄件資料快照存入 `registrations`，而非只存 FK 指向 `user_profiles`。理由：

- 會員事後修改住址，不應影響已成立的訂單
- 電子發票、物流資訊需與當時填寫的一致

### race_id 使用字串而非 UUID（例外處理）

`races.race_id` 使用 `hunt2026`、`relay2026` 等人類可讀字串，其他表用 VARCHAR(50) 儲存。理由：

- 賽事 ID 會出現在 URL、Log、前端程式碼中，可讀性更重要
- 賽事總數少，不需要 UUID 的碰撞防護

### 快取表設計

`user_levels`、`race_mileage_accumulation`、`faction_daily_standings` 都是快取表，理論上可從明細表加總推算，但實際更新頻率極高，以快取表減少即時計算壓力：

- `user_levels.total_km` ← SUM(activity_logs.distance_km)
- `race_mileage_accumulation.total_km` ← SUM(activity_logs WHERE race_id)
- `faction_daily_standings` ← 每日 20:00 批次更新

---

## Seed 資料清單

| 資料集 | 資料量 | 說明 |
|--------|--------|------|
| 使用者 | 7 人 | 6 位跑者 + 1 位管理員 |
| 賽事 | 7 場 | 4 場現有 + 3 場歷史 |
| 距離組別 | 15 筆 | 各賽事距離選項 |
| 陣營 | 8 筆 | 4 場陣營賽各 2 陣營 |
| 跑團 | 4 個 | 台北夜行者等 |
| 報名 | 10 筆 | 6 筆現役 + 4 筆歷史 |
| 訂單 | 10 筆 | 對應報名，全數已付款 |
| 分組指派 | 9 筆 | 含歷史賽事的陣營指派 |
| 每日任務 | 7 筆 | hunt2026 完整 7 天 |
| 任務進度 | 10 筆 | 前 3 天各人進度 |
| 活動紀錄 | 9 筆 | hunt2026 前 3 天跑步 |
| 里程累積 | 8 筆 | 現役 + 陳逸帆歷史 |
| 陣營戰況 | 6 筆 | hunt2026 前 3 天快照 |
| 門市 | 4 家 | 台北信義/大安/中山/板橋 |
| 打卡紀錄 | 3 筆 | |
| 轉盤獎項 | 7 筆 | hunt2026 完整獎項池 |
| 轉盤記錄 | 6 筆 | |
| 集點貼紙定義 | 9 筆 | hunt2026 九宮格 |
| 已收集貼紙 | 5 筆 | 陳逸帆已集 1/2/3/4/6 格 |
| 公仔兌換 | 3 筆 | 對應後台出貨佇列 |
| 完賽紀錄 | 4 筆 | 陳逸帆歷史完賽 |
| 推播模板 | 3 筆 | |
| 推播歷史 | 3 筆 | 對應後台通知記錄 |
| 獎勵帳本 | 10 筆 | |

---

## 如何擴充

### 新增賽事類型
在 `races.group_type` ENUM 中加入新值，並建立對應的子定義表（參考 `race_factions` / `race_clubs` 模式）。

### 新增健身平台
在 `data_sync_connections.provider` VARCHAR 欄位中直接存入新平台名稱，無需改 schema。

### 新增獎勵類型
在 `wheel_pools` 中新增一列（`prize_kind` 若需要新值則修改 ENUM），在 `reward_transactions.tx_type` 同理。

### 多國語言
若未來需要 i18n，建議新增 `race_translations(race_id, lang, title, blurb)` 表，主表保留預設語言（繁體中文）。
