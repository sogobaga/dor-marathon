'use client'

// SkinOverride：帳號層級「風格設定」的權威切換器（第 22 套未來科技＋第 23 套復古 RPG＋第 24 套
// 溫馨可愛，見契約
// scratchpad/retro_skin/CONTRACT.md §2）。掛在 root layout 的 client providers 內，本身不渲染畫面
// 以外的「風格背景層」（scifi 的 ParticleField／retro 的 RetroBackground），純粹依「登入 uid +
// dashboard.skin_select_entry + dashboard.ui_skin」三個條件同步 <html data-skin>／meta
// theme-color／localStorage 覆寫記錄。
//
// 三個條件缺一律恢復 originalSkin/originalThemeColor（layout.tsx 依 SSR 讀到的 active_skin 算好傳入）：
//   - 未登入 / 登出 / 換帳號 → uid 為 null 或改變
//   - dashboard.skin_select_entry !== 'shown'（非白名單帳號、或後台把入口關掉）
//   - dashboard.ui_skin 是 'default' 或 null（使用者在 ProfileScreen「風格設定」選了預設風格）
//
// ⚠️ 只有「確定」符合以上條件才會恢復，「還不知道」不算——身分（useUser()）與 dashboard 都是非同步
// 才回來的資料，掛載後的第一輪、SWR 重抓中、SWR key 剛換發（例如 uid 剛確定）等「暫時性未知」狀態，
// 一律維持現狀不動（見下方 userKnown／Gate 0-2a 的註解）。這是因為開機腳本（layout.tsx 的
// skinOverrideBootJs）已經在 React 開始渲染前，用同一把 localStorage 記錄把 <html data-skin> 設成
// 正確值；早年版本只要這裡任何一個條件「暫時」讀到 falsy 就立刻呼叫 restoreOriginalSkin，等於把開機
// 腳本設好的正確風格搶先撤銷、資料回來後才又套回去，使用者因此會在切換頁面（尤其整頁重載）時看到
// 「先閃一下預設風格，才跳成指定風格」。
// 「請求確定失敗」則不是無條件維持現狀：只有手上還有「上一次成功」的 dash 資料可信任時才維持現狀
// （Gate 2b），因為那份舊資料本身就是這個帳號上次確定的答案；如果連上一次成功的資料都沒有（例如
// 剛換版號清了持久化 SWR 快取、或這是全新裝置/無痕模式第一次抓就失敗），開機腳本的樂觀猜測就只是
// 本機殘留、可能早就過期的紀錄，不能無限期（SWR 重試預算用完後可能卡到下次 focus/reconnect 才會
// 再試，見 AppProviders.tsx）繼續信任，這時要落回下面 dash=null 算出的安全預設——維持這套系統
// 「三者缺一必須恢復」的 fail-safe 承諾，不讓已被收回資格的帳號無限期卡在舊風格上（code review 抓到）。
//
// 非白名單使用者完全不受影響的關鍵：useDashboard() 本來就是所有頁面共用的同一份請求（不會因為多了這個
// 元件而多打一次 API），且當條件「確定」不滿足時這裡只會呼叫 restoreOriginalSkin（等同 no-op，因為
// dataset.skin 本來就等於 originalSkin，从未被改成 scifi/retro 過）。
import { useEffect, useState } from 'react'
import dynamic from 'next/dynamic'
import { useUser } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import { applySkinOverride, restoreOriginalSkin, readOverrideRecord, SKIN_CHANGE_EVENT, type OverrideSkin } from '@/lib/skinOverride'

// 全站背景粒子層（未來科技，契約 scifi_skin/CONTRACT.md §3）：next/dynamic(ssr:false) 動態載入，
// 只在下方 active==='scifi' 時才會實際掛載 render——非白名單使用者（active 恆 null）從未 import 這支
// 元件，不進首屏 bundle，也不進任何獨立 chunk 的網路請求（dynamic import 的 chunk 只在被 render
// 時才會被瀏覽器抓取）。
const ParticleField = dynamic(() => import('@/components/scifi/ParticleField'), { ssr: false })
// 全站像素背景層（復古 RPG，契約 retro_skin/CONTRACT.md §3）：同上道理，只在 active==='retro' 時才
// 掛載。RetroBackground 由 THEME 工人另外建立於 components/retro/RetroBackground.tsx（default
// export）；在它落地前這行 import 找不到模組是預期中的單一 tsc 錯誤（契約 §6 已註明可接受）。
const RetroBackground = dynamic(() => import('@/components/retro/RetroBackground'), { ssr: false })
// 全站彩色紙屑/星星/雲朵背景層（溫馨可愛，第 24 套，契約 docs/skins/CUTE_CONTRACT.md §3）：同上
// 道理，只在 active==='cute' 時才掛載。CuteBackground 由 THEME 工人另外建立於
// components/cute/CuteBackground.tsx（default export）；在它落地前這行 import 找不到模組是預期中的
// 單一 tsc 錯誤（契約 §6 已註明可接受，同 RetroBackground 前例）。
const CuteBackground = dynamic(() => import('@/components/cute/CuteBackground'), { ssr: false })

export default function SkinOverride({
  originalSkin,
  originalThemeColor,
}: {
  originalSkin: string
  originalThemeColor: string
}) {
  const user = useUser()
  const { dash, loading: dashLoading, error: dashError } = useDashboard()
  const uid = user?.id ?? null
  const entryShown = dash?.skin_select_entry === 'shown'
  const uiSkin = dash?.ui_skin ?? null
  const [active, setActive] = useState<OverrideSkin | null>(null)

  // userKnown：「身分是否已經問過」的門閂，只在掛載後第一輪之後才會變 true，而且一旦變 true
  // 就不會再變回 false。用意：useUser() 本身故意在掛載後才用自己的 useEffect 讀 localStorage
  // （避免 SSR 不一致，見該檔案註解），所以這個元件掛載後的**第一輪** effect 一定會看到
  // uid=null——這不代表「確定未登入」，只是「還沒問過」。下面的 sync() 若在這個假 null 上就判定
  // 「沒有覆寫」而呼叫 restoreOriginalSkin()，會把開機腳本（layout.tsx 的 skinOverrideBootJs）
  // 已經在畫面繪製前設好的正確 data-skin 撤銷，等 uid／dashboard 真正回來後才又套回去——這正是
  // 「切換頁面時先看到預設風格、才跳成指定風格」的成因（追蹤報告已確認）。userKnown 這顆 state
  // 要等到「這一輪 render 的所有 mount effects 都跑過」才會變 true（React 同一個 fiber 的 effects
  // 依 hook 呼叫順序執行，這支 useEffect 排在 useUser()/useDashboard() 兩個 useUser() 呼叫之後，
  // 保證等它真正變 true 時 uid 已經是那一輪 render 讀到的最新值)，藉此把「身分未知」與「確定
  // 未登入」分開判斷。
  const [userKnown, setUserKnown] = useState(false)
  useEffect(() => {
    setUserKnown(true)
  }, [])

  useEffect(() => {
    function sync() {
      // Gate 0：身分還沒問過（掛載後第一輪）→ 什麼都不做，保留開機腳本已經設好的畫面，
      // 不要用還沒問過的假 null 去撤銷它。
      if (!userKnown) return

      // Gate 1：本機覆寫記錄若是「別人」留下的（同一分頁內換帳號但沒有整頁重載，或上一位使用者
      // 登出流程被中斷、記錄沒清乾淨），不論這個新帳號自己的 dashboard 有沒有回來，都要立刻清掉
      // ——這不是「資料不明所以先不動」，而是「已經確定這筆記錄不屬於目前登入者」的明確訊號，拖到
      // 新帳號的 dashboard 資料回來才清，等於讓新使用者先看到上一位使用者的風格。清掉之後不
      // return：如果這一輪剛好也拿到了目前使用者自己的確定答案，就直接往下套用，不必再等一輪。
      const staleRec = uid ? readOverrideRecord() : null
      if (staleRec && staleRec.uid !== uid) {
        restoreOriginalSkin(originalSkin, originalThemeColor)
        setActive(null)
      }

      if (!uid) {
        // 身分已知、且確定是「登出」（不是掛載瞬間的假 null）→ 收回覆寫。
        setActive(null)
        restoreOriginalSkin(originalSkin, originalThemeColor)
        return
      }

      // Gate 2a：dashboard 還在抓（含 uid 剛確定、SWR key 剛從 null 切成 ['dashboard', uid] 的
      // 第一輪）→ 答案未知，維持現狀不動——可能是開機腳本已設好的值，也可能是上一次確定答案套用
      // 的結果，不能因為「還沒問到」就猜測性地改成預設外觀（那會製造一次「先跳預設、問到後才跳
      // 回來」的多餘閃爍，比原本的 bug 更糟）。
      if (dashLoading) return

      // Gate 2b：這次請求「確定」失敗（連線不穩／伺服器忽然 500，且 SWR 的重試預算已用完，見
      // AppProviders.tsx 的 onErrorRetry：401/403/404 不重試、其餘最多 3 次指數退避後放棄，之後
      // 要等 revalidateOnFocus/onReconnect 或手動 refreshDashboard() 才會再試）——但只有「手上還有
      // 上一次成功的 dash 資料」時才維持現狀不動：那份舊資料就是這個帳號上一次確定的答案，繼續信任
      // 它不會錯，等下次重抓成功自然會用新答案覆蓋過去。反過來，如果連上一次成功的資料都沒有
      // （dash 仍是 null——例如剛換版號清了持久化 SWR 快取、或這是全新裝置/無痕模式第一次抓就失敗），
      // 畫面上唯一的依據只剩開機腳本用本機殘留 dor_skin_override 記錄做的樂觀猜測，這筆記錄可能早就
      // 過期（帳號的風格入口/選擇已經在別處被收回），而且錯誤狀態可能沒有上限地卡住（若使用者剛好
      // 留在同一個已連線、沒有失焦過的分頁，可能整個 session 都不會再自動重試）——這時不能繼續賭
      // 這個猜測是對的，要往下走、用 dash=null 算出的安全預設收尾（等同原本「查不到資料就退回預設」
      // 的 fail-safe，不讓已被收回資格的帳號無限期 fail-open 卡在舊風格上；code review 抓到）。
      if (dashError && dash) return

      const isOverride = entryShown && (uiSkin === 'scifi' || uiSkin === 'retro' || uiSkin === 'cute')
      const next: OverrideSkin | null = isOverride ? (uiSkin as OverrideSkin) : null
      setActive(next)
      if (next) {
        applySkinOverride(uid, next)
      } else {
        restoreOriginalSkin(originalSkin, originalThemeColor)
      }
    }
    sync()
    // 舊版(第22套)偏好開關已移除，但 ProfileScreen 選擇風格後仍會廣播這個事件（配合 dashboard 重抓），
    // 讓畫面不必整頁重整就能立即套用／收回。
    window.addEventListener(SKIN_CHANGE_EVENT, sync)
    return () => window.removeEventListener(SKIN_CHANGE_EVENT, sync)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uid, userKnown, dashLoading, dashError, entryShown, uiSkin, originalSkin, originalThemeColor])

  if (active === 'scifi') return <ParticleField />
  if (active === 'retro') return <RetroBackground />
  if (active === 'cute') return <CuteBackground />
  return null
}
