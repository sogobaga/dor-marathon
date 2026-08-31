-- Migration 158: 團練狀態模型重整——發起人可自行隱藏，且「關閉」改為可逆
--
-- 使用者定案的四個動作（正交三軸，可並存）：
--   ① 關閉／重新開啟  status: 'open' ⇄ 'closed'
--      關閉＝「不再收新人，其他功能照舊」，**可逆**（原本 closed 是單向終局，這是本次修正的主因）。
--      因為可以再開啟，關閉時待審申請一律「保留」——重開後發起人可以繼續處理。
--      （原實作在關閉時會把所有 pending 婉拒，那是為了「不可逆」而設計的收尾，現在改由「中止」承擔。）
--   ② 中止           status: 'cancelled'（可恢復為 open）
--      ＝「停止加入的任何動作」。與關閉的差別：中止會把所有待審申請一併婉拒，
--      且任何人嘗試加入時明確顯示「該團練已中止」。
--   ③ 隱藏／取消隱藏  hidden_by_owner（本次新增欄位，可逆）
--      ＝從團練探索移除、連結也不給非相關人看；發起人／已加入成員／管理者仍可正常查看與互動
--      （若連成員都看不到，已成團的人會突然失去集合資訊，所以成員必須保留可見）。
--   ④ 刪除           deleted_at（軟刪，不可逆）
--      連結進入時前端顯示「該團練已被刪除，3 秒後將會切換至首頁。」並自動導回首頁。
--
-- ⚠️ hidden_by_owner 與既有 hidden_by_admin 刻意分成兩欄，不共用：
--    前者是發起人自己的選擇（自己可以取消），後者是後台強制下架（發起人不得自行解除）。
--    合併成單一欄位會讓發起人有機會把管理員的下架處分關掉。
--
-- ⚠️ 既有資料不受影響：新欄位有 DEFAULT FALSE，既有列自動補值（PostgreSQL 11+ 對有預設值的
--    ADD COLUMN 不重寫資料表）。

ALTER TABLE run_meets
    ADD COLUMN IF NOT EXISTS hidden_by_owner BOOLEAN NOT NULL DEFAULT FALSE;

-- 探索列表的主要瀏覽索引要一併排除發起人自行隱藏的團練，否則列表查詢會多掃一堆隱藏列。
DROP INDEX IF EXISTS idx_run_meets_browse;
CREATE INDEX IF NOT EXISTS idx_run_meets_browse ON run_meets (meet_at)
    WHERE deleted_at IS NULL AND hidden_by_admin = FALSE AND hidden_by_owner = FALSE AND status = 'open';

INSERT INTO schema_migrations (version) VALUES ('158') ON CONFLICT DO NOTHING;
