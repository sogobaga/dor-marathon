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

// __dorSkinPin／__dorSkinObsInstalled：契約 TRACK_HYDRATION_CONTRACT.md 修法 2「data-skin 釘選」
// 防線用的兩個 window 全域——同一把鍵名同時被這支檔案（React 資料回來後的權威路徑）與
// app/layout.tsx 的 skinOverrideBootJs（開機那一刻的搶跑路徑，純字串 JS、不能 import 這支檔案）
// 讀寫，是共用防線的單一約定，哪一邊先跑到都認得對方已經做過的事（見下方 installSkinPinObserver
// 與 applySkinOverride/restoreOriginalSkin 的註解）。
declare global {
  interface Window {
    __dorSkinPin?: OverrideSkin | null
    __dorSkinObsInstalled?: boolean
  }
}

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

// installSkinPinObserver：契約修法 2「data-skin 釘選」防線。只裝一顆 MutationObserver（全域旗標
// __dorSkinObsInstalled 防重裝——layout.tsx 的 skinOverrideBootJs 開機時可能已經裝過同語意的一顆，
// 這裡認得那顆已經裝好，不會疊裝第二顆），只要偵測到 <html data-skin> 與 window.__dorSkinPin
// 不同、且 pin 非 null（non-null＝目前「應該」是覆寫風格中），就立刻設回去並把 meta theme-color
// 也重設成同一張色表對應的顏色。MutationObserver 的 callback 是 microtask，保證在瀏覽器下一次真正
// 繪製畫面前執行完畢——不論是「未來任何一種 hydration mismatch 讓 React 從根節點清掉 <html> 所有
// 屬性」（見契約 §根因 acquireSingletonInstance）、或其他意外把 data-skin 改掉的路徑，使用者都不會
// 看到中間那一幀「變回預設」的畫面。防迴圈：只在真的不同時才寫（下面 if 判斷），觀察者自己觸發的
// 那次「寫回同樣的值」不會再次觸發不同值的分支，不會無限遞迴。
// pin 為 null（未套用覆寫／已收回）時這顆觀察者的 callback 直接 return，對沒有覆寫資格的一般使用者
// 完全是 no-op（連比對都提早短路），不影響任何人。
function installSkinPinObserver() {
  if (typeof window === 'undefined' || typeof document === 'undefined') return
  if (window.__dorSkinObsInstalled) return
  const mo = new MutationObserver(() => {
    try {
      const pin = window.__dorSkinPin
      if (pin == null) return
      const el = document.documentElement
      if (el.dataset.skin !== pin) {
        el.dataset.skin = pin
        setThemeColorMeta(THEME_COLOR[pin])
      }
    } catch {
      // 同其餘防線：觀察者本身絕不能因為任何例外而中斷（例如某次 callback 執行時 document 已被
      // 卸載），吞掉即可，下一次屬性變動還會再觸發一次 callback。
    }
  })
  try {
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-skin'] })
    window.__dorSkinObsInstalled = true
  } catch {
    // observe() 理論上不會丟例外（documentElement 恆存在），防禦性 catch 與其他函式風格一致。
  }
}

// applySkinOverride 套用覆寫：寫 <html data-skin="scifi"|"retro">、meta theme-color、並記錄
// {uid, skin} 供下次開機防閃腳本比對（見檔頭註解）。
export function applySkinOverride(uid: string, skin: OverrideSkin) {
  clearLegacyPref()
  safeSetItem(OVERRIDE_KEY, JSON.stringify({ uid, skin }))
  if (typeof document === 'undefined') return
  // 契約修法 2：pin 必須先於屬性寫入就設好——萬一屬性寫入後、下一行程式碼執行前這段 microtask
  // 之間就有東西把 data-skin 改掉（理論上不會，但釘選的意義就是不假設「中間不會有意外」），
  // pin 也已經是正確值，觀察者隨時能認出「現在該是什麼」。
  window.__dorSkinPin = skin
  document.documentElement.dataset.skin = skin
  setThemeColorMeta(THEME_COLOR[skin])
  installSkinPinObserver()
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
  // 契約修法 2：pin 先設回 null 再移除屬性——「刻意的收回」必須讓釘選防線知情，否則上面那顆
  // 觀察者會把即將被拿掉的 data-skin 當成「被意外清掉」立刻寫回去，變成怎麼收都收不掉。
  window.__dorSkinPin = null
  if (!originalSkin || originalSkin === 'default') {
    delete document.documentElement.dataset.skin
  } else {
    document.documentElement.dataset.skin = originalSkin
  }
  setThemeColorMeta(originalThemeColor)
  clearOverrideCookie()
}

// reapplyPinThemeColor：契約修法 2「防線單測」考慮到的另一種意外——MutationObserver 只看得到
// <html data-skin> 屬性本身的變動，看不到 Next.js 重渲染 <head> 時把 <meta name="theme-color">
// 整個節點換掉（換節點＝新節點的 content 又是 SSR 算出的原值，不是我們釘選的覆寫色，但這不會觸發
// attributeFilter:['data-skin'] 的觀察者，因為被換的是另一個元素）。components/SkinOverride.tsx
// 掛載後跑一次性檢查：只要 pin 非 null，就把目前的 <meta theme-color> 重設成 pin 對應的顏色；只做
// 一次（掛載時機已晚於開機腳本／bootJs 設定 pin，足以撿回被換掉的節點），不是常駐監聽——持續監聽
// <head> 子樹變動成本較高且目前沒有已知的持續性問題，之後如證實有需要可再升級成 MutationObserver。
export function reapplyPinThemeColor() {
  if (typeof window === 'undefined' || typeof document === 'undefined') return
  const pin = window.__dorSkinPin
  if (pin == null) return
  setThemeColorMeta(THEME_COLOR[pin])
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
