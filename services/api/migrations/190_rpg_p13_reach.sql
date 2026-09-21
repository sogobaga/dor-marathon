-- 190_rpg_p13_reach.sql
-- DORPG P13（docs/dorpg/P13_CONTRACT.md）：前排阻擋——前排還有活著的怪時，melee 武器只能打
-- 前排；ranged 不受限；前排清空後全部解鎖。P5～P12 契約仍有效，本檔只加不改。
--
-- 阻擋規則本身（誰能打誰）是 ENGINE 的事（engine/dispatch.ts、combat.ts），這裡只負責標記
-- 「這個武器類型的攻擊算 melee 還是 ranged」——掛在既有的 rpg_weapon_types.traits（P7
-- migration 183 已建欄位，JSONB NOT NULL DEFAULT '{}'），沿用 P12（188）「traits || 合併、
-- @> 判斷已套用就跳過」的既有寫法：只新增 reach 鍵，不動 P7 產生器留下的既有描述鍵或 P12 的
-- 排位加成鍵。冪等：重複執行結果相同。
--
-- CONTRACT §2 分類理由：ranged＝弓（射程本就遠）與法系（法杖／書，魔法系依契約本就不受阻擋，
-- 但明確標 ranged 讓 reach 欄位本身保持「跟武器手感一致」，未來若法系也開放物理技能不會忘記
-- 補上）；melee＝其餘近戰武器（劍系、槍矛巨劍斧、鈍器、鍊）。缺省（未列出的既有或未來武器類型、
-- traits 遺漏 reach 鍵）一律當 melee——阻擋是預設規則、遠程才是需要主動標記的例外，這樣新增武器
-- 類型忘了設定 reach 時，行為是「保守受阻擋」而不是「意外繞過阻擋機制」的漏洞。
--
-- ⚠️ 使用者手動套用到 Neon，不得自行套到正式庫。

-- ---------------------------------------------------------------------------
-- 1) ranged（不受前排阻擋）：弓類三種 + 法系（魔法師／聖職者）杖書共五種。
-- ---------------------------------------------------------------------------
UPDATE rpg_weapon_types
SET traits = traits || '{"reach": "ranged"}'::jsonb, updated_at = NOW()
WHERE id IN (
    'ar_longbow', 'ar_shortbow', 'ar_crossbow',
    'mg_staff', 'mg_rod', 'mg_book',
    'cl_staff', 'cl_book'
  )
  AND NOT (traits @> '{"reach": "ranged"}'::jsonb);

-- ---------------------------------------------------------------------------
-- 2) melee（受前排阻擋）：劍系、重騎士系、商人鈍器系、聖職者鍊，共十種。明確寫入（而非只靠
--    Go 端缺省）讓後台武器類型頁一開就看得到欄位與值，跟 P12 row_bonus 系列的既有慣例一致。
-- ---------------------------------------------------------------------------
UPDATE rpg_weapon_types
SET traits = traits || '{"reach": "melee"}'::jsonb, updated_at = NOW()
WHERE id IN (
    'lk_sword', 'lk_dual', 'lk_rapier',
    'hk_greatsword', 'hk_spear', 'hk_axe',
    'mc_hammer', 'mc_mallet', 'mc_club',
    'cl_chain'
  )
  AND NOT (traits @> '{"reach": "melee"}'::jsonb);

INSERT INTO schema_migrations (version) VALUES ('190') ON CONFLICT DO NOTHING;
