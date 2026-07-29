-- 106_monopoly_user_stickers.sql
-- 修正表名衝突：001_init.sql 早已建立 user_stickers（完賽貼紙 sticker_no/race_id，由 internal/reward
-- 使用中），導致 102_monopoly_draw.sql 的 CREATE TABLE IF NOT EXISTS user_stickers（含 piece_id）被
-- 跳過——大富翁公仔貼紙的擁有表從未以正確結構建立，造成 /monopoly/stickers 查詢與 drawSticker 發放
-- 皆因 piece_id 欄位不存在而失敗。改用專屬表名 monopoly_user_stickers（不動 001 的舊表）。
CREATE TABLE IF NOT EXISTS monopoly_user_stickers (
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    piece_id          UUID NOT NULL REFERENCES sticker_pieces(id) ON DELETE CASCADE,
    obtained_count    INT NOT NULL DEFAULT 1,
    first_obtained_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_obtained_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, piece_id)
);
CREATE INDEX IF NOT EXISTS idx_monopoly_user_stickers_user ON monopoly_user_stickers(user_id);
