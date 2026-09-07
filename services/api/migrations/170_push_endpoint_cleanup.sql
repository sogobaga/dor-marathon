-- Migration 170: 清掉 push_subscriptions 裡不在白名單網域內的舊訂閱端點（M6 資安修補）
--
-- 背景：internal/push.Subscribe 原本完全不驗證前端送來的 endpoint（瀏覽器
-- PushManager.subscribe() 回傳的 URL），直接存進 push_subscriptions；internal/push.send()
-- 之後會對這個 URL 直接發 HTTP 請求（帶 VAPID 簽章 body）——任何登入使用者只要換一個
-- 假造的 endpoint 就能讓後端變成一台可控 SSRF 代理（打內網位址／雲端 metadata 服務／
-- 任意第三方站台）。程式端已補上白名單驗證（Subscribe 拒收、send 送出前再驗一次，見
-- internal/push/push.go isAllowedPushHost／validatePushEndpoint），但正式環境上線前
-- 已經寫進 DB 的舊資料不受新驗證約束，必須一次性清掉。
--
-- 只放行各大瀏覽器/OS 廠商已公開文件的推播閘道網域（與程式端 allowedPushHostSuffixes
-- 完全對應）：
--   Chrome/Edge/Android (FCM)   fcm.googleapis.com、android.googleapis.com（限完全相符，
--                               規格沒有 "*." 前綴，不放行子網域）
--   Firefox                     updates.push.services.mozilla.com 及其任一層子網域
--                               （*.push.services.mozilla.com）
--   Windows (WNS)                *.notify.windows.com
--   Safari/iOS (APNs Web Push)   *.push.apple.com（涵蓋 web.push.apple.com）
--
-- 用正規表示式直接比對 endpoint 欄位：必須是 https，且 host 落在上述白名單——host 邊界
-- 用 "(/|$)" 卡住（比對到白名單網域後，下一個字元只能是路徑分隔符或字串結尾），避免
-- "fcm.googleapis.com.attacker.com" 這種尾碼混淆字串被誤判為合法。
--
-- ⚠️ 本 migration 只寫檔，未套用到任何資料庫。部署順序：先套 migration 再推程式（owner 手動套用）。
-- 執行前若想先確認會刪掉哪些列，可自行把下面的 DELETE 改成 SELECT id, endpoint 手動核對。

DELETE FROM push_subscriptions
WHERE endpoint !~* '^https://(fcm\.googleapis\.com|android\.googleapis\.com|updates\.push\.services\.mozilla\.com|([a-z0-9-]+\.)*(push\.services\.mozilla\.com|notify\.windows\.com|push\.apple\.com))(/|$)';

INSERT INTO schema_migrations (version) VALUES ('170') ON CONFLICT DO NOTHING;
