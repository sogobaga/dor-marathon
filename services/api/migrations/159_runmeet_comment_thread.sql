-- Migration 159: 團練留言升級為討論串（回覆 + 表情反應 + 分頁）
--
-- 需求（使用者原話摘要）：
--   ・留言可以像 Threads 那樣「針對某一句話回覆」，討論串太多要能展開／收起。
--   ・可以對「單一留言」設定 emoji——不見得想留言，但想表達心情。
--   ・詳情頁只顯示最初 10 筆；點展開後先載 20 筆，往下滑再一次載 10 筆（避免瞬間載入過多、降流量壓力）。
--
-- ⚠️ 只允許一層回覆（parent 必須是頂層留言），由應用層強制：
--    巢狀無限展開在手機上會縮排到沒有寬度可用，Threads/IG 也都是「頂層 + 回覆」兩層。
--    對回覆再按回覆時，前端會把它掛回同一個頂層串（並在內文帶 @對象），不建立第三層。
--
-- ⚠️ 父留言被軟刪時，子回覆刻意保留（不 CASCADE、不連帶刪）：
--    討論串中間被挖掉會讓後面的回覆變得無法理解。前端對 deleted_at 非空的父留言
--    顯示「這則留言已刪除」佔位，子回覆照常顯示。
--    外鍵仍設 ON DELETE CASCADE 是為了「硬刪」情境（後台清資料／使用者刪帳號），
--    正常流程走軟刪不會觸發。

ALTER TABLE run_meet_comments
    -- NULL = 頂層留言；非 NULL = 對某則頂層留言的回覆
    ADD COLUMN IF NOT EXISTS parent_id      UUID REFERENCES run_meet_comments(id) ON DELETE CASCADE,
    -- 反正規化計數，供「查看 N 則回覆」與列表分頁時不必逐筆 COUNT
    ADD COLUMN IF NOT EXISTS reply_count    INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reaction_count INT NOT NULL DEFAULT 0;

-- 討論串查詢：某團練的頂層留言（parent_id IS NULL）依時間分頁，或某頂層留言的回覆。
-- (meet_id, parent_id, created_at, id) 同時服務兩種查詢，並讓 cursor 分頁
-- （created_at, id 為游標，避免 OFFSET 在新留言插入時跳號重複）走索引。
CREATE INDEX IF NOT EXISTS idx_rmc_thread
    ON run_meet_comments (meet_id, parent_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- 單則留言的表情反應（PK 保證一人一則一種，換表情＝UPDATE，天然防洗榜；與 run_meet_reactions 同款設計）
CREATE TABLE IF NOT EXISTS run_meet_comment_reactions (
    comment_id UUID NOT NULL REFERENCES run_meet_comments(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)             ON DELETE CASCADE,
    -- meet_id 冗餘存一份：權限判定（是否為該團成員）與後台稽核都要從留言反查團練，
    -- 少一次 JOIN；由應用層在寫入時填入，不設觸發器。
    meet_id    UUID NOT NULL REFERENCES run_meets(id)         ON DELETE CASCADE,
    kind       VARCHAR(10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (comment_id, user_id),
    CONSTRAINT rmcr_kind_chk CHECK (kind IN ('like','fire','muscle','pray','heart'))
);
CREATE INDEX IF NOT EXISTS idx_rmcr_comment ON run_meet_comment_reactions (comment_id);
-- 「我對這串留言各按了什麼」一次撈回：載入討論串時用 (meet_id, user_id) 一句查完，
-- 不必對每則留言各查一次。
CREATE INDEX IF NOT EXISTS idx_rmcr_meet_user ON run_meet_comment_reactions (meet_id, user_id);

INSERT INTO schema_migrations (version) VALUES ('159') ON CONFLICT DO NOTHING;
