# 跑者充電站 多品項（variants）契約 v1 — 2026-09-21

單一真相。既有 partner_shops 行為（migration 091/112/117/121/122、v0.1.412 CTA gate、v0.1.413 頁籤）全部維持，本檔只加不改。

## 0. 使用者需求（原話濃縮）
商家設定目前只能對單一品項設連結。要擴充：單一商家資訊最上面仍是商品說明介紹，**最底部可設定多個細項商品**（圖片／名稱／描述／連結，例如巧克力／蔓越莓／蜂蜜檸檬口味），可排序。商家設定加「單一品項／多品項」：單一品項＝入口按鈕照舊；多品項＝入口只保留「詳細」、隱藏「前往」，「詳細」適度變寬不留空位。

## 1. 決策（編排者定案）
- **資料放 JSONB**：`partner_shops.item_mode TEXT NOT NULL DEFAULT 'single' CHECK (single|multi)`＋`partner_shops.variants JSONB NOT NULL DEFAULT '[]'`（比照 photo_urls／content_images 的 marshal/unmarshal 慣例，不另開表）。陣列順序＝顯示順序（排序＝後台調整陣列順序後整包 PUT，一次交易、無競態）。
- **品項結構**：`{ "id": "<uuid，伺服器產生，前端可回傳既有 id>", "name": "巧克力口味", "description": "純文字，可含換行", "image_url": "https://…或 /api/v1/images/…", "cta_url": "https://…" }`。
  - 驗證（service 層）：`item_mode` ∈ single|multi（空＝single）；variants ≤ 50 筆；`name` 必填 1–60 字（trim 後）；`description` ≤ 300 字（純文字，不進 bluemonday、不當 HTML 渲染，前端用 React 文字節點＋`whiteSpace:'pre-line'`）；`image_url` 選填、非空時須過既有 `validImageURL`；`cta_url` 選填、非空時須過既有 `validHTTPURL`（http/https）；`id` 缺或非 UUID 格式→伺服器補新 uuid。錯誤回 400，`error` 訊息指出第幾筆哪個欄位（例：`variants[2].name 必填`）。
  - 多品項模式時 variants 可為 0 筆（後台儲存不擋，前台詳細頁該區塊不顯示）。
- **CTA gate 擴充**：`applyCtaGate` 現有行為不變（vip_featured 且不合格→`cta_locked=true`＋清空 `cta_url`）；**Detail 另外對每個 variant 清空 `cta_url`**（同一把鎖，伺服器端真 gate）。前端品項按鈕 `showCta = !!v.cta_url || shop.cta_locked`；locked 時顯示「🔒 前往」並開既有 `VipLockedModal`。
- **列表（入口卡片）**：List DTO 新增 `item_mode`（ListEnabled SELECT 加欄位；**不回 variants**，列表保持精簡）。`ShopCard`：`showCta = shop.item_mode !== 'multi' && (!!shop.cta_url || !!shop.cta_locked)`；多品項時 grid 只剩一欄、「詳細」自然占滿整列（既有 `gridTemplateColumns` 依 showCta 切換，不留空位）。
- **詳細頁**：Detail DTO 新增 `item_mode`＋`variants`。多品項模式：**隱藏底部固定 CTA**（商家層級 cta_url 不用），在 `video_urls` 區塊之後（捲動內容最底部）渲染「品項」區塊：標題「品項」＋每個品項一張卡：左圖（正方形縮圖 72px、`objectFit:cover`、無圖時顯示占位）＋右側名稱（粗體）／描述（`pre-line`、小字、次要色）／「前往」按鈕（primaryFullBtn 風格、右對齊或整列，開新分頁 `noopener,noreferrer`；無 cta_url 且未鎖定時不顯示按鈕）。單一品項模式：**完全維持現狀**（不渲染 variants 區塊，即使資料有）。
- **後台**（`admin/partners/page.tsx`）：在 audience 選單旁加「商品模式」`<select>`：單一品項／多品項（照抄 audience 樣板）。多品項時顯示「品項列表」編輯區（照抄 content_images 的增刪／上傳／排序寫法）：每筆一張卡＝縮圖（單張，`adminImagesApi.upload` 上傳／更換／移除）＋名稱 input＋描述 textarea＋連結 input＋「↑ ↓ 刪除」；底部「＋ 新增品項」。單一品項時**隱藏**品項編輯區（資料保留，切回多品項會再出現）；多品項時單一 cta_url／cta_label 欄位仍顯示但加註「多品項模式下入口與詳細頁不使用此連結」。`edit()` 載入 `item_mode ?? 'single'`／`variants ?? []`；`save()` 送兩欄。伺服器 400 訊息原樣顯示。
- **不做**：WS `data_updated` 廣播（partner 套件原本就沒接，維持既有 SWR 重驗證行為）；點擊統計；品項層級 gate（沿用商家層級）。
- **相容**：舊快取／舊 bundle 收到多出的欄位無害；`item_mode` 缺省 single 讓既有商家行為零改動。

## 2. 介面（WIRE）
- Go model：`PartnerVariant{ID string json:"id"; Name string json:"name"; Description string json:"description"; ImageURL string json:"image_url"; CTAURL string json:"cta_url"}`；`PartnerShop` 加 `ItemMode string json:"item_mode"`；`PartnerShopDetail`／`AdminPartnerShop`／`AdminPartnerShopRequest` 加 `Variants []PartnerVariant json:"variants"`（JSON 一律回陣列，nil→`[]`）。
- repo：INSERT 新增 `item_mode=$15, variants=$16`；UPDATE 同序、`WHERE id=$17`；`adminSelectCols`／`GetDetail` 加 `item_mode, variants`；`ListEnabled` 加 `item_mode`。
- TS（`apps/web/src/lib/api.ts`）：`PartnerVariant` 型別；`PartnerShop.item_mode: 'single'|'multi'`；`PartnerShopDetail.variants: PartnerVariant[]`；`AdminPartnerShop`／`PartnerShopWriteBody` 加 `item_mode`／`variants`。
- migration `services/api/migrations/191_partner_shop_variants.sql`：`ADD COLUMN IF NOT EXISTS item_mode TEXT NOT NULL DEFAULT 'single'`＋CHECK（用 DO $$ 判斷約束不存在才加，可重複執行）＋`ADD COLUMN IF NOT EXISTS variants JSONB NOT NULL DEFAULT '[]'::jsonb`＋`INSERT INTO schema_migrations (version) VALUES ('191') ON CONFLICT DO NOTHING`。

## 3. 驗證
- Go：`go build ./... && go vet ./internal/partner/ && go test ./internal/partner/`（新增 `variants_test.go`：驗證規則各情境、id 補發、gate 清空 variants cta_url、item_mode 缺省 single、JSON nil→[]）。
- 前端：`tsc --noEmit` 0 錯誤、`next build` 54 頁。
- Neon 臨時分支：191 套用＋再套一次冪等；後台 POST/PUT 帶 variants 往返（順序保持、id 補發、非法 → 400 訊息含索引）；公開 list 有 item_mode 無 variants；detail 有 variants；vip_featured 不合格帳號 detail 的 shop 與每個 variant cta_url 皆空且 cta_locked=true；既有商家（未動）item_mode=single、variants=[]。
- E2E（Playwright）：後台建多品項商家（3 筆＋排序）→ 前台入口卡只有「詳細」且寬度＝卡片內容寬（無第二欄）；單一品項商家卡仍兩顆；詳細頁多品項底部區塊順序與每筆「前往」開新分頁（攔 window.open）；鎖定情境開 VipLockedModal；四視窗（390/430/768/1280）無橫向溢出；0 新 console error。
- 審查（唯讀）。
