-- 放寬 reward_serials.code 為 TEXT：支援 LINE POINT 等「純貼領取連結」批次匯入
-- （以連結本身作為唯一序號去重，連結長度不定，原 VARCHAR(200) 可能不夠）。
-- 既有唯一索引 uq_reward_serials_code 會隨欄位型別變更自動重建；URL 長度遠低於 btree 限制，安全。
ALTER TABLE reward_serials ALTER COLUMN code TYPE TEXT;

INSERT INTO schema_migrations (version) VALUES ('129') ON CONFLICT DO NOTHING;
