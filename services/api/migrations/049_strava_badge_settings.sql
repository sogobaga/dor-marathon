-- 049: Strava「Powered by Strava」標章雙版本（後台可上傳）
-- 一個檔案無法同時適用深/淺色 skin：深色 skin 用白字版、淺色 skin 用深字版，前台依 active skin 顯示對應版本。
ALTER TABLE site_settings
  ADD COLUMN IF NOT EXISTS strava_powered_dark_url  TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS strava_powered_light_url TEXT NOT NULL DEFAULT '';
