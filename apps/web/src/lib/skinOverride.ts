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
//
// 'cute'（溫馨可愛，第 24 套）比照 scifi/retro 同一套機制加入，見 docs/skins/CUTE_CONTRACT.md
// §2；theme-color 第二輪改 #fff5f8（草莓牛奶，docs/skins/CUTE_CONTRACT_R2.md §2 milk 色號，
// 取代第一輪的奶油黃 #fff3d6）。

import { OVERRIDE_THEME_COLOR, type OverrideSkin } from './skinColors'

export type { OverrideSkin }

export const SKIN_CHANGE_EVENT = 'dor-skin-change'

const OVERRIDE_KEY = 'dor_skin_override'
const LEGACY_PREF_KEY = 'dor_skin_pref' // 舊版(第22套)裝置開關，已由伺服器權威 ui_skin 取代，讀到就清

// 契約 docs/skins/HOME_FLASH_CONTRACT.md 修法 3：meta theme-color 色表改從 lib/skinColors.ts
// 單一來源引入（原本這裡自己一份、app/layout.tsx 的開機腳本 skinOverrideBootJs 又自己內嵌一份，
// 兩處各存一份色號，將來改色容易漏改一邊）。
const THEME_COLOR = OVERRIDE_THEME_COLOR

// dor_skin_ov cookie：middleware.ts 到站彈跳頁用（純外觀，判斷白幕該用哪個風格的色，見
// lib/skinColors.ts OVERRIDE_VEIL_COLORS 的註解）。只存 skin 字串本身，不含 uid——cookie 會被
// 伺服器讀到，而 uid 比對本來就只在瀏覽器端（localStorage dor_skin_override／dor_user）進行，
// cookie 只是「這個瀏覽器上次生效的覆寫風格是什麼」的外觀提示，不是權威判斷來源，就算殘留過期
// 也只會讓彈跳頁白幕色猜錯（≤600ms 的過場動畫），不影響任何實際套用邏輯。
const OVERRIDE_COOKIE = 'dor_skin_ov'

function setOverrideCookie(skin: OverrideSkin) {
  try {
    const secure = typeof location !== 'undefined' && location.protocol === 'https:' ? '; Secure' : ''
    document.cookie = `${OVERRIDE_COOKIE}=${skin}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`
  } catch {
    // 同 safeSetItem：私密瀏覽/cookie 被封鎖時靜默放棄，彈跳頁退回 SSR skin 白幕色，外觀而已。
  }
}

function clearOverrideCookie() {
  try {
    document.cookie = `${OVERRIDE_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`
  } catch {
    // 同上
  }
}

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
// 受管理的覆寫風格之一，不依賴任何 React state。非 scifi/retro/cute（含未設定、default、warm 等
// SSR 原值）一律回 null。
export function getActiveSkin(): OverrideSkin | null {
  if (typeof document === 'undefined') return null
  const v = document.documentElement.dataset.skin
  return v === 'scifi' || v === 'retro' || v === 'cute' ? v : null
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
  // 契約修法 4：cookie 只在真的有 document（瀏覽器）時才種，SSR/測試環境 import 這支檔案不會
  // 意外寫入任何東西（上面的 early return 已經保證這裡以下都在瀏覽器）。
  setOverrideCookie(skin)
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
  clearOverrideCookie()
}

// clearSkinOverride：登出／換帳號時呼叫的語意別名（行為與 restoreOriginalSkin 完全相同，只是呼叫端
// 表達的情境不同——SkinOverride.tsx 在 uid 消失時呼叫這支，行為上就是「收回覆寫、恢復原值」）。
export const clearSkinOverride = restoreOriginalSkin

// subscribeSkinChange／getSkinServerSnapshot：搭配 React 18 useSyncExternalStore 給 track 頁／歷史頁
// 用（單一真相，避免兩處各寫一份 MutationObserver＋事件監聽）。舊版寫法是 useState(null)+useEffect，
// 讀取時機在 commit 之後、瀏覽器真正繪製之前那一刻都還是 null——等於保證會先畫一次「沒有風格」的畫面
// （預設 Leaflet 地圖／RaceFocusMode 預設樣式），等 effect 跑完才切回正確風格，這正是「重新進入 GPS
// 跑步追蹤頁先顯示預設畫面、才切換風格畫面」的成因。useSyncExternalStore 在 render 當下就能同步讀到
// 正確值：
//   ・純client端掛載（例如站內切頁進 /track）：第一次 render 就直接呼叫 getSnapshot()，沒有「先 null」
//     這一輪。
//   ・SSR＋hydration（整頁載入／硬重載）：hydration 那一輪用 getServerSnapshot()（固定回 null，與伺服器
//     端 document 不存在時 getActiveSkin() 本來就會回傳的值一致，不會有 hydration mismatch）；hydration
//     完成後，React 對 useSyncExternalStore 有内建保證——若這時 client 端 getSnapshot() 讀到不同的值
//     （例如 layout.tsx 的開機腳本已經在繪製前把 <html data-skin> 設成 scifi/retro/cute），會在瀏覽器
//     真正把畫面畫出來之前強制同步重新渲染一次，使用者不會看到「先預設、才跳成風格」那一幀。
export function subscribeSkinChange(onChange: () => void): () => void {
  if (typeof document === 'undefined') return () => {}
  const mo = new MutationObserver(onChange)
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-skin'] })
  window.addEventListener(SKIN_CHANGE_EVENT, onChange)
  return () => { mo.disconnect(); window.removeEventListener(SKIN_CHANGE_EVENT, onChange) }
}
export function getSkinServerSnapshot(): OverrideSkin | null {
  return null
}

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
