-- Migration 135: 獎勵管理一般化——賽後抽獎從個人挑戰模式(personal)擴大到所有賽事模式。
-- personal 模式的既有獎勵管理（registrations.reward_status/reward_note/reward_fulfilled_at，見 migration 125）
-- 完全不動、平行共存；本檔案是給「其餘賽事模式」用的全新一組表，不共用欄位也不互相影響。
--
-- 抽獎資格底線＝完賽：完成該賽事「基本目標」（各分組 target_distance_km，即 race.computeFinishers／
-- GetLeaderboard 完成榜判定的同一條線）的報名者。全體/分組/個人額外挑戰(race_tasks)都是疊加的額外目標，
-- 這裡的資格線只看完賽。
--
-- 兩種抽籤範圍(scope)：all_finishers（全體完賽者）｜winning_group（獲勝分組內的完賽者）。
-- winning_group 的獲勝組判定(win_rule)：highest_metric（分組指標最高，平手並列）｜
-- first_to_target（最先達到分組目標，逐時間序累加判定）——實作見 internal/race/reward_draw.go。
--
-- 抽獎為後台手動觸發，可對同一賽事建立多次批次（如頭獎抽 1 名、普獎抽 10 名）；同一批次內以 user 為
-- 單位去重（不重複中獎），跨批次是否排除先前批次中獎者由 exclude_prior 控制（前端預設勾選）。

CREATE TABLE IF NOT EXISTS race_reward_draws (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    race_id       UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    title         VARCHAR(100) NOT NULL DEFAULT '',          -- 批次名稱（如「頭獎」）
    scope         VARCHAR(20) NOT NULL,                      -- all_finishers | winning_group
    win_rule      VARCHAR(20) NOT NULL DEFAULT '',           -- winning_group 用：highest_metric | first_to_target
    win_task_id   UUID REFERENCES race_tasks(id),            -- winning_group 用：判定所依據的分組(group_team)任務；
                                                               -- 建立抽獎時未明確指定則由後端自動取該賽事第一個
                                                               -- 非打卡點的 group_team 任務，並把實際採用的任務 ID
                                                               -- 寫回本欄位（稽核用，NULL 僅見於 scope=all_finishers）
    winner_count  INT NOT NULL,
    exclude_prior BOOLEAN NOT NULL DEFAULT TRUE,             -- 排除本賽事先前批次中獎者
    winning_group_ids UUID[] DEFAULT NULL,                   -- 抽獎當下判定的獲勝組（稽核；平手可多筆）
    pool_size     INT NOT NULL DEFAULT 0,                    -- 抽獎當下資格池人數（稽核）
    drawn_by      UUID REFERENCES users(id),
    drawn_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rrd_race ON race_reward_draws(race_id);

CREATE TABLE IF NOT EXISTS race_reward_winners (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    draw_id             UUID NOT NULL REFERENCES race_reward_draws(id) ON DELETE CASCADE,
    race_id             UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id            UUID,
    reward_status       VARCHAR(20) NOT NULL DEFAULT '',     -- ''=中獎待發, 'fulfilled'=已發放
                                                               -- （中獎本身已定案，沒有 personal 舊制那個
                                                               -- ''=未中選/待抽 的中間態，比照語意但少一態）
    reward_note         TEXT NOT NULL DEFAULT '',
    reward_fulfilled_at TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (draw_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_rrw_race_user ON race_reward_winners(race_id, user_id);

INSERT INTO schema_migrations (version) VALUES ('135') ON CONFLICT DO NOTHING;
