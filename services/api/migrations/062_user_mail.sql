-- 站內信（in-app mail）：後台廣播/系統事件可寫入，前台鈴鐺列表 + 未讀數。
CREATE TABLE IF NOT EXISTS user_mail (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  level TEXT NOT NULL DEFAULT 'normal' CHECK (level IN ('normal','important','urgent')),
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  url TEXT NOT NULL DEFAULT '',
  read_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_user_mail_user_created ON user_mail(user_id, created_at DESC);
