-- Migration 172: GPS 上傳 outbox 缺口補強 + 活動佇列死信落地
-- 依賴：025_gps_runs.sql（gps_runs 建表）
--
-- 背景（2026-09-08 第二次稽核 finding 2/3）：
--   finding 2：SaveGPSRun／AdminApproveGPS 都是「先寫 gps_runs（或標成 approved）、後 XAdd 推入
--   活動佇列」兩步式流程；若程序在兩步之間被殺掉（部署重啟/OOM/崩潰），gps_runs 這筆已經落地，
--   但活動事件永遠不會被送出，且 uq_gps_runs_user_start 冪等索引會讓使用者重傳同一趟被當重複
--   擋下、永遠補不回來——這是一個沒有兜底的 outbox 缺口。本次新增 gps_runs.enqueued_at：
--   SaveGPSRun/AdminApproveGPS 在 XAdd 成功後才寫入這個時間戳；internal/ops 每小時排程呼叫
--   activity.Service.RequeueUnenqueued 掃描 enqueued_at IS NULL 且已過一段時間的孤兒列，重建
--   ActivityEvent 補送。既有資料（本 migration 套用前的所有歷史列）都是在舊版「無 outbox 追蹤」
--   路徑下處理過的，一律當作「已處理過」回填 created_at，避免上線後把整表歷史資料誤判成孤兒重新
--   入隊、造成大量重複活動。
--
--   finding 3：活動佇列（services/worker Redis Stream "activity_queue"）的死信處理過去只告警就
--   直接 ACK——訊息內容從此在 Redis PEL 消失、事後無從查起或重放；加上既有的 XTRIM MAXLEN 定期
--   裁剪不看 PEL，還可能把尚未處理完的訊息直接砍掉。本次新增 activity_dead_letters 落地死信內容
--   （payload/error/delivery_count），worker 改成「先落地才 ACK」，並提供 ReplayDeadLetter 重放。

-- (1) gps_runs.enqueued_at：見上方 finding 2 說明。
ALTER TABLE gps_runs ADD COLUMN IF NOT EXISTS enqueued_at TIMESTAMPTZ NULL;
COMMENT ON COLUMN gps_runs.enqueued_at IS
  '成功推入活動佇列（XAdd）的時間；NULL 代表尚未入隊（見 activity.Service.RequeueUnenqueued 孤兒列補送掃描）';

-- 只索引 enqueued_at IS NULL 的列（partial index）：正常運作下這個集合應該近乎空，供
-- ListUnenqueuedGPS 的背景掃描快速命中，不拖累一般查詢。
CREATE INDEX IF NOT EXISTS idx_gps_runs_unenqueued ON gps_runs (created_at) WHERE enqueued_at IS NULL;

-- 回填既有資料：本 migration 套用前的所有歷史列都是在舊版路徑下處理過的（要嘛已經正常入隊、
-- 要嘛是本來就不該入隊的 flagged 待審/駁回列），一律視為「已處理完畢」，不可回填成 NULL 讓
-- RequeueUnenqueued 誤判成孤兒重新入隊。
UPDATE gps_runs SET enqueued_at = created_at WHERE enqueued_at IS NULL;

-- (2) activity_dead_letters：見上方 finding 3 說明。msg_id 是 Redis Stream 的 entry id（文字，
-- 非 uuid，格式如 "1694160000000-0"），payload 是原始 JSON 事件內容（供 ReplayDeadLetter 直接
-- 重新 XAdd）；error 記錄最後一次處理失敗的錯誤訊息；delivery_count 是死信化當下的投遞次數
-- （Redis PEL 官方計數）；replayed_at 非 NULL 代表已被重放過一次，不可再重放（見
-- services/worker/main.go ReplayDeadLetter 的防重複重放判斷）。
CREATE TABLE IF NOT EXISTS activity_dead_letters (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    msg_id          TEXT NOT NULL,
    payload         TEXT NOT NULL,
    error           TEXT NOT NULL DEFAULT '',
    delivery_count  INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    replayed_at     TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS idx_activity_dead_letters_unreplayed ON activity_dead_letters (created_at) WHERE replayed_at IS NULL;

INSERT INTO schema_migrations (version) VALUES ('172') ON CONFLICT DO NOTHING;
