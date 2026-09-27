// 未來科幻世界風格（scifi skin）覆寫——第 22 套，見契約 scratchpad/scifi_skin/CONTRACT.md §2。
//
// 這是「疊在 SSR active_skin 之上」的個人化覆寫，只在同時滿足以下條件時生效：
//   1. 已登入（uid 存在）
//   2. 後端 dashboard.scifi_entry === 'shown'（見 services/api/internal/profile.resolveScifiEntry；
//      預設只有 sogobaga@gmail.com 白名單命中才會是 'shown'，其餘帳號恆為 'hidden'）
//   3. 使用者自己的裝置偏好（dor_skin_pref）不是 'off'（預設 'on'）
// 三者缺一，就必須恢復呼叫端傳入的 SSR 原值（active_skin），不得殘留 scifi 外觀——這是「非白名單/
// 關閉偏好使用者零影響」的最後一道防線，實際套用/移除都經過這裡的函式，不在別處直接改 dataset。
//
// 開機防閃：layout.tsx 的內聯 boot script 是純字串 JS（不能 import 這支 TS 檔），用同一把
// localStorage key（dor_skin_override）做等效判斷，在 React 開始渲染前就把 <html data-skin> 設好，
// 避免「先看到原 skin 一瞬間、再跳成 scifi」的閃爍；本檔案的 applyScifiSkin 是「之後」（dashboard 資料
// 回來、偏好改變時）用來保持一致或收回的權威實作，兩邊邏輯必須同步維護（哪一邊改了判斷條件，另一邊
// 也要跟著改）。

export const SKIN_CHANGE_EVENT = 'dor-skin-change'

const OVERRIDE_KEY = 'dor_skin_override'
const PREF_KEY = 'dor_skin_pref'
const SCIFI_THEME_COLOR = '#02040a'

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
    // 不因此拋錯——scifi 風格本來就是錦上添花的個人化外觀，不能因為存不了而讓頁面壞掉。
  }
}
function safeRemoveItem(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {
    // 同上
  }
}

// getSkinPref 讀使用者對 scifi 風格的裝置偏好（ProfileScreen 開關列）。未設定視為 'on'（契約 §2：
// 「偏好存 localStorage dor_skin_pref，預設 ON」）。
export function getSkinPref(): 'on' | 'off' {
  return safeGetItem(PREF_KEY) === 'off' ? 'off' : 'on'
}

// setSkinPref 只負責寫入偏好並廣播事件；實際套用/收回畫面由監聽 SKIN_CHANGE_EVENT 的
// components/SkinOverride.tsx 統一處理（單一真相：只有它會呼叫 applyScifiSkin/restoreOriginalSkin）。
export function setSkinPref(pref: 'on' | 'off') {
  safeSetItem(PREF_KEY, pref)
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(SKIN_CHANGE_EVENT))
}

// isSciFiActive 供其他模組（例如 track 頁的 MutationObserver 判斷前的初始值、或除錯用途）查詢目前
// <html data-skin> 是否為 'scifi'，不依賴任何 React state。
export function isSciFiActive(): boolean {
  if (typeof document === 'undefined') return false
  return document.documentElement.dataset.skin === 'scifi'
}

function setThemeColorMeta(color: string) {
  if (typeof document === 'undefined') return
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', color)
}

// applyScifiSkin 套用 scifi 覆寫：寫 <html data-skin="scifi">、meta theme-color、並記錄
// {uid, skin:'scifi'} 供下次開機防閃腳本比對（見檔頭註解）。
export function applyScifiSkin(uid: string) {
  safeSetItem(OVERRIDE_KEY, JSON.stringify({ uid, skin: 'scifi' }))
  if (typeof document === 'undefined') return
  document.documentElement.dataset.skin = 'scifi'
  setThemeColorMeta(SCIFI_THEME_COLOR)
}

// restoreOriginalSkin 移除覆寫並恢復呼叫端傳入的 SSR 原值（originalSkin/originalThemeColor 由
// layout.tsx 算好、經 components/SkinOverride.tsx 傳入——這支本身不知道「原本」是什麼，避免這裡與
// layout.tsx 的 skin/主題色對照表各存一份、將來新增 skin 時漏改一邊）。
export function restoreOriginalSkin(originalSkin: string, originalThemeColor: string) {
  safeRemoveItem(OVERRIDE_KEY)
  if (typeof document === 'undefined') return
  if (!originalSkin || originalSkin === 'default') {
    delete document.documentElement.dataset.skin
  } else {
    document.documentElement.dataset.skin = originalSkin
  }
  setThemeColorMeta(originalThemeColor)
}

// clearScifiOverride：登出／換帳號時呼叫的語意別名（行為與 restoreOriginalSkin 完全相同，只是呼叫端
// 表達的情境不同——SkinOverride.tsx 在 uid 消失時呼叫這支，行為上就是「收回覆寫、恢復原值」）。
export const clearScifiOverride = restoreOriginalSkin

// readOverrideRecord 讀目前的覆寫記錄（除錯／單元測試用；正常運作路徑不需要呼叫，SkinOverride.tsx
// 直接以 uid + dashboard.scifi_entry 現算，不依賴讀回這份記錄）。
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
