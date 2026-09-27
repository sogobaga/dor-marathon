// 帳號層級「風格設定」覆寫——第 22 套（未來科技）＋第 23 套（復古 RPG），見契約
// scratchpad/retro_skin/CONTRACT.md §2（沿用 scratchpad/scifi_skin/CONTRACT.md 的原始設計）。
//
// 這是「疊在 SSR active_skin 之上」的個人化覆寫，只在同時滿足以下條件時生效：
//   1. 已登入（uid 存在）
//   2. 後端 dashboard.skin_select_entry === 'shown'（見
//      services/api/internal/profile.resolveSkinSelectEntry；預設只有 sogobaga@gmail.com 白名單
//      命中才會是 'shown'，其餘帳號恆為 'hidden'）
//   3. 後端 dashboard.ui_skin 是 'scifi' 或 'retro'（帳號自己在「會員管理→個人資料→風格設定」選的，
//      伺服器權威儲存；不是本機裝置偏好）
// 三者缺一，就必須恢復呼叫端傳入的 SSR 原值（active_skin），不得殘留 scifi/retro 外觀——這是
// 「非白名單/未選擇的使用者零影響」的最後一道防線，實際套用/移除都經過這裡的函式，不在別處直接改
// dataset。
//
// 開機防閃：layout.tsx 的內聯 boot script 是純字串 JS（不能 import 這支 TS 檔），用同一把
// localStorage key（dor_skin_override）做等效判斷，在 React 開始渲染前就把 <html data-skin> 設好，
// 避免「先看到原 skin 一瞬間、再跳成 scifi/retro 的閃爍」；本檔案的 applySkinOverride 是「之後」
// （dashboard 資料回來、選擇改變時）用來保持一致或收回的權威實作，兩邊邏輯必須同步維護（哪一邊改了
// 判斷條件，另一邊也要跟著改）。
//
// 對其他工人的介面約定（契約明訂）：getActiveSkin()、isSciFiActive()、SKIN_CHANGE_EVENT 三個匯出
// 是固定介面，GPS 地圖／專注模式等其他模組依此判斷該載入哪一套視覺。

export type OverrideSkin = 'scifi' | 'retro'

export const SKIN_CHANGE_EVENT = 'dor-skin-change'

const OVERRIDE_KEY = 'dor_skin_override'
const LEGACY_PREF_KEY = 'dor_skin_pref' // 舊版(第22套)裝置開關，已由伺服器權威 ui_skin 取代，讀到就清

const THEME_COLOR: Record<OverrideSkin, string> = { scifi: '#02040a', retro: '#000000' }

function safeGetItem(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}
function safeSetItem(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // 私密瀏覽/儲存被封鎖：覆寫這次只在記憶體內生效（不寫入），下次開機防閃腳本讀不到很正常，
    // 不因此拋錯——風格覆寫本來就是錦上添花的個人化外觀，不能因為存不了而讓頁面壞掉。
  }
}
function safeRemoveItem(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {
    // 同上
  }
}

// clearLegacyPref：舊版(第22套) dor_skin_pref 裝置開關不再讀取，讀到就清掉，避免殘留誤導。
function clearLegacyPref() {
  safeRemoveItem(LEGACY_PREF_KEY)
}

// getActiveSkin 供其他模組（例如 track 頁的地圖選擇、除錯用途）查詢目前 <html data-skin> 是否為
// 受管理的覆寫風格之一，不依賴任何 React state。非 scifi/retro（含未設定、default、warm 等 SSR
// 原值）一律回 null。
export function getActiveSkin(): OverrideSkin | null {
  if (typeof document === 'undefined') return null
  const v = document.documentElement.dataset.skin
  return v === 'scifi' || v === 'retro' ? v : null
}

// isSciFiActive 舊介面相容（＝getActiveSkin()==='scifi'）；track 頁等既有呼叫端沿用這支不必改名。
export function isSciFiActive(): boolean {
  return getActiveSkin() === 'scifi'
}

function setThemeColorMeta(color: string) {
  if (typeof document === 'undefined') return
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', color)
}

// applySkinOverride 套用覆寫：寫 <html data-skin="scifi"|"retro">、meta theme-color、並記錄
// {uid, skin} 供下次開機防閃腳本比對（見檔頭註解）。
export function applySkinOverride(uid: string, skin: OverrideSkin) {
  clearLegacyPref()
  safeSetItem(OVERRIDE_KEY, JSON.stringify({ uid, skin }))
  if (typeof document === 'undefined') return
  document.documentElement.dataset.skin = skin
  setThemeColorMeta(THEME_COLOR[skin])
}

// restoreOriginalSkin 移除覆寫並恢復呼叫端傳入的 SSR 原值（originalSkin/originalThemeColor 由
// layout.tsx 算好、經 components/SkinOverride.tsx 傳入——這支本身不知道「原本」是什麼，避免這裡與
// layout.tsx 的 skin/主題色對照表各存一份、將來新增 skin 時漏改一邊）。
export function restoreOriginalSkin(originalSkin: string, originalThemeColor: string) {
  clearLegacyPref()
  safeRemoveItem(OVERRIDE_KEY)
  if (typeof document === 'undefined') return
  if (!originalSkin || originalSkin === 'default') {
    delete document.documentElement.dataset.skin
  } else {
    document.documentElement.dataset.skin = originalSkin
  }
  setThemeColorMeta(originalThemeColor)
}

// clearSkinOverride：登出／換帳號時呼叫的語意別名（行為與 restoreOriginalSkin 完全相同，只是呼叫端
// 表達的情境不同——SkinOverride.tsx 在 uid 消失時呼叫這支，行為上就是「收回覆寫、恢復原值」）。
export const clearSkinOverride = restoreOriginalSkin

// readOverrideRecord 讀目前的覆寫記錄（除錯／單元測試用；正常運作路徑不需要呼叫，SkinOverride.tsx
// 直接以 uid + dashboard.ui_skin 現算，不依賴讀回這份記錄）。
export function readOverrideRecord(): { uid: string; skin: string } | null {
  const raw = safeGetItem(OVERRIDE_KEY)
  if (!raw) return null
  try {
    const v = JSON.parse(raw)
    return v && typeof v.uid === 'string' && typeof v.skin === 'string' ? v : null
  } catch {
    return null
  }
}
