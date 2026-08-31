-- Migration 160: 修正討論串分頁索引——局部索引改為完整索引
--
-- 159 建的 idx_rmc_thread 帶了 `WHERE deleted_at IS NULL`（比照既有 idx_rmc_meet 的寫法），
-- 但討論串的規格是「**已刪除的留言仍要以佔位留在串裡**」（否則中間被挖掉，後面的回覆讀不懂），
-- 所以查詢無法套用那個謂詞，於是被迫拆成 UNION ALL 兩支：未刪那支走索引，已刪那支**無索引可用**。
--
-- 在 16 萬筆留言的環境用 EXPLAIN (ANALYZE) 實測（2026-08-31）：
--   ・未刪分支     0.18 ms（走索引）
--   ・已刪分支    11.37 ms（Seq Scan + Sort，甚至啟動平行 worker）← 佔整條查詢 98% 時間
--   ・整條 UNION  11.56 ms，且成本隨**全站留言總數**成長，不是隨單一團練
-- 改用不帶謂詞的完整索引後，查詢不必再 UNION，同一環境實測：
--   ・單一查詢    3.26 ms（JOIN 在外）→ 1.95 ms（先取 N 筆再併使用者名稱）
--   ・成本改為隨「該團練的留言數」成長，正常規模（數百則）在 2 ms 以內
--
-- 代價：索引多涵蓋已軟刪的列（實務上是少數），換掉「已刪分支全表掃描」是明顯划算的交換。
--
-- ⚠️ 既有的 idx_rmc_meet（meet_id, created_at）WHERE deleted_at IS NULL 保留不動：
--    那是給「只看未刪留言」的既有簡單列表用的，與討論串分頁是不同查詢。

DROP INDEX IF EXISTS idx_rmc_thread;

-- 完整索引（不帶 WHERE）：討論串分頁一句查完，不必為了「已刪佔位」而 UNION。
-- 欄位順序＝查詢條件順序：meet_id 等值 → parent_id 等值（IS NULL 取頂層 / = ? 取某串回覆）
-- → (created_at, id) 游標範圍與排序。
CREATE INDEX IF NOT EXISTS idx_rmc_thread_all
    ON run_meet_comments (meet_id, parent_id, created_at DESC, id DESC);

INSERT INTO schema_migrations (version) VALUES ('160') ON CONFLICT DO NOTHING;
