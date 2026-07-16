-- Migration 088: 統一名稱「法特雷克」→「法特萊克」（Fartlek 正確譯名）
--
-- 為什麼需要這支 migration：錯字是由 migration 063（explore_bosses seed）與 cmd/seedworkouts
-- 寫進資料庫的，兩者都**早已在正式環境執行過**。修正那些來源檔只影響「未來全新安裝」，
-- 已上線的資料列不會因此改變（migrate 會 skip 已套用的版本），故必須用 UPDATE 補正名。
--
-- 落點（已逐一比對來源確認）：
--   explore_bosses.segments      JSONB 內的段落標籤「法特雷克衝刺（60 秒）」×27（063）
--   personal_tasks.title/.workout  「法特萊克變速 ×N」——seedworkouts 把 wo.Title 同時餵給兩欄
--   personal_plans.entry_note     P06「半馬長征：法特雷克變速、長跑推進。」
-- workout_templates（082 自主訓練課表庫）本來就是「法特萊克變速跑」，無需處理；
-- user_training_schedule.name 快照自 workout_templates，故同樣不受影響。
--
-- 用 REPLACE 而非整列覆寫：只換這個詞，不動使用者/管理員後來編輯過的其他內容。
-- WHERE ... LIKE 讓沒有錯字的列完全不被觸碰（避免無謂的列改寫）。

UPDATE explore_bosses
   SET segments = REPLACE(segments::text, '法特雷克', '法特萊克')::jsonb
 WHERE segments::text LIKE '%法特雷克%';

UPDATE personal_tasks
   SET title   = REPLACE(title,   '法特雷克', '法特萊克'),
       workout = REPLACE(workout, '法特雷克', '法特萊克')
 WHERE title LIKE '%法特雷克%' OR workout LIKE '%法特雷克%';

UPDATE personal_tasks
   SET segments = REPLACE(segments::text, '法特雷克', '法特萊克')::jsonb
 WHERE segments::text LIKE '%法特雷克%';

UPDATE personal_plans
   SET entry_note = REPLACE(entry_note, '法特雷克', '法特萊克')
 WHERE entry_note LIKE '%法特雷克%';

-- 後置檢查：正名若沒吃乾淨就整支 rollback，不要默默留下半套資料。
DO $$
DECLARE n BIGINT;
BEGIN
  SELECT count(*) INTO n FROM (
    SELECT 1 FROM explore_bosses  WHERE segments::text LIKE '%法特雷克%'
    UNION ALL
    SELECT 1 FROM personal_tasks  WHERE title LIKE '%法特雷克%'
                                     OR workout LIKE '%法特雷克%'
                                     OR segments::text LIKE '%法特雷克%'
    UNION ALL
    SELECT 1 FROM personal_plans  WHERE entry_note LIKE '%法特雷克%'
  ) t;
  IF n > 0 THEN
    RAISE EXCEPTION '正名未完成：仍有 % 列殘留「法特雷克」', n;
  END IF;
END $$;

INSERT INTO schema_migrations (version) VALUES ('088') ON CONFLICT DO NOTHING;
