-- Seed 初始資料 — 僅用於開發/測試環境
-- 執行前請確保 migration 已跑完

-- 1. 建立平台 admin 帳號
-- 密碼：password  (測試用，正式環境須更換)
INSERT INTO users (email, handle, name, password_hash, role)
VALUES (
    'admin@dor.tw',
    'dor_admin',
    'DOR 管理員',
    '$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi', -- password: password
    'admin'
) ON CONFLICT (email) DO NOTHING;

-- 2. 建立測試參賽者帳號（密碼同上）
INSERT INTO users (email, handle, name, password_hash, role)
VALUES (
    'runner@dor.tw',
    'test_runner',
    '測試跑者',
    '$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi',
    'user'
) ON CONFLICT (email) DO NOTHING;

-- 3. 建立測試賽事（status=open，可報名）
INSERT INTO races (
    slug, title, subtitle, world, blurb,
    status, distances, group_type, group_mode,
    slots_total, entry_fee, start_date, end_date,
    config, review_status
) VALUES (
    'hunt2026',
    '獵人之夜',
    'HUNTERS NIGHT',
    '獵人 vs 逃亡者',
    '一半的人追，一半的人逃。對抗只在虛擬戰局，不在真實世界。',
    'open',
    ARRAY[10, 21, 42],
    'faction',
    'random',
    1000,
    69000,
    CURRENT_DATE + INTERVAL '3 days',
    CURRENT_DATE + INTERVAL '10 days',
    '{
        "factions": [
            {"id": "fugitive", "name": "逃亡者", "color": "fug"},
            {"id": "hunter",   "name": "獵人",   "color": "hunt"}
        ],
        "missions": [
            {"day":1,"title":"身份啟動","tag":"IDENTITY","type":"base","base_km":2.0,"desc":"完成 2K 啟動跑，啟用今日身份卡。"},
            {"day":2,"title":"低速潛伏","tag":"STEALTH","type":"pace","base_km":3.0,"pace_lo":"9:30","pace_hi":"10:30","desc":"以 9:30–10:30 配速完成 3K。"},
            {"day":3,"title":"高速追擊","tag":"CHASE","type":"pace","base_km":3.0,"pace_lo":"4:30","pace_hi":"5:30","desc":"以 4:30–5:30 配速完成 3K 追擊。"},
            {"day":4,"title":"誘餌與搜索","tag":"DECOY","type":"base","base_km":4.0,"desc":"完成 4K 佈設誘餌。"},
            {"day":5,"title":"救援日","tag":"RESCUE","type":"rescue","base_km":3.0,"desc":"完成 3K 後，每多 1K 釋放 1 位夥伴（今日加倍）。"},
            {"day":6,"title":"壓縮戰局","tag":"SQUEEZE","type":"pace","base_km":5.0,"pace_lo":"5:30","pace_hi":"6:30","desc":"以 5:30–6:30 配速完成 5K。"},
            {"day":7,"title":"最終追逐","tag":"FINALE","type":"base","base_km":6.0,"desc":"最終結算日，完成 6K 決定翻盤。"}
        ]
    }'::jsonb,
    'approved'
) ON CONFLICT (slug) DO NOTHING;

-- 4. 建立第二個測試賽事（status=soon）
INSERT INTO races (
    slug, title, subtitle, world, blurb,
    status, distances, group_type, group_mode,
    slots_total, entry_fee, start_date, end_date,
    config, review_status
) VALUES (
    'signal2026',
    '穩定訊號',
    'STEADY SIGNAL',
    '配速控制挑戰',
    '慢不是弱，是潛伏。把配速波動控制在區間內，維持訊號不被獵人偵測。',
    'soon',
    ARRAY[10, 21],
    'distance',
    'self',
    500,
    59000,
    CURRENT_DATE + INTERVAL '25 days',
    CURRENT_DATE + INTERVAL '30 days',
    '{}'::jsonb,
    'approved'
) ON CONFLICT (slug) DO NOTHING;
