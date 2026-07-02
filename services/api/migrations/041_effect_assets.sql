-- Migration 041: 效果資產覆寫（把暫代 emoji/合成音效換成正式圖片/音檔）。
-- slug 對應程式內的效果位置；url 指向已上傳的圖片/音檔（/api/v1/images/{id}）。空/無列＝用內建暫代。
CREATE TABLE IF NOT EXISTS effect_assets (
    slug       TEXT PRIMARY KEY,
    url        TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES ('041') ON CONFLICT DO NOTHING;
