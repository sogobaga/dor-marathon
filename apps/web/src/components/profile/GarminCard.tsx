'use client'

// Garmin Connect 官方直連卡片（個人頁「運動數據」分頁）。後端契約見 services/api/internal/integration/garmin_connect.go，
// 前台 client／純函式見 lib/garminApi.ts，歸屬字串見 lib/attribution.ts。
//
// Garmin 只有「推送」沒有「拉取」：手錶同步到 Garmin Connect 後，Garmin 自動把活動摘要送到 DOR（可能要幾分鐘到數小時），
// 所以這張卡片沒有「匯入數據」按鈕，文案也如實說明。
//
// 顯示條件（由 ProfileScreen 決定要不要掛載；卡片自己決定要不要打 /status）：
//   - 入口 shown（Dashboard garmin_entry）或 Dashboard 的 connected_sources 含 garmin → 才打 /status；
//   - 入口 hidden 且沒有連線 → 零請求、什麼都不顯示（只有導回訊息／剛中斷的訊息會留一張只含訊息的卡）；
//   - 入口 hidden 但已有連線 → 只剩狀態與「中斷連線」（status／disconnect 不受入口限制）。
//
// 品牌規範：連接畫面要用完整名稱「Garmin Connect」（不縮寫、不風格化）並放官方 tile——官方 tile 由擁有者日後提供，
// 這裡只留一個標示清楚的預留位 [data-garmin-tile-slot]，不仿製任何 Garmin 標誌。已連接時，卡片標題正下方列「Garmin ＋ 型號」
// （型號不明只寫 Garmin），首屏可見、不放在 tooltip／頁尾／展開區。

import { useCallback, useEffect, useState } from 'react'
import { withUserAuth } from '@/lib/userAuth'
import {
  garminApi, garminView, garminShouldFetch, garminErrorText, garminDisconnectNotice, garminDay, garminShortTime,
  garminRelativeAgo, garminDataStale, isGarminAuthUrl,
  type GarminStatus, type GarminNotice,
} from '@/lib/garminApi'
import { garminAttribution } from '@/lib/attribution'

// 官方 tile 圖檔路徑（擁有者從 Garmin 開發者入口下載後放進 apps/web/public/garmin/，再把這個常數改成該路徑，
// 例如 '/garmin/garmin-connect-tile.png'；不要自己畫或改圖）。null＝顯示預留位。
const GARMIN_TILE_SRC: string | null = null

export interface GarminCardProps {
  entryHint?: string | null         // Dashboard 的 garmin_entry
  hasConnectionHint: boolean        // Dashboard 的 connected_sources 含 garmin
  notice: GarminNotice | null       // 要顯示的訊息（導回結果／操作結果）；由 ProfileScreen 持有，卡片因入口關閉收起時使用者仍看得到結果
  onNotice: (n: GarminNotice | null) => void
  reloadKey?: number                // 遞增＝重抓 /status（?garmin= 導回後）
  hasOtherSourceActivity?: boolean  // 使用者近期有其他來源的活動（超過 48 小時沒收到 Garmin 資料時才提示「手錶可能沒同步」）
  onDisconnected?: () => void       // 中斷完成：呼叫端刷新已同步活動／Dashboard／偏好來源
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 14px)', padding: 14, minWidth: 0, boxSizing: 'border-box' }
const small: React.CSSProperties = { fontSize: 11.5, color: 'var(--tx-dim)', lineHeight: 1.7, overflowWrap: 'anywhere' }
const faint: React.CSSProperties = { fontSize: 11, color: 'var(--tx-faint)', lineHeight: 1.6, overflowWrap: 'anywhere' }
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', fontFamily: 'inherit',
  borderRadius: 'var(--radius-btn, 10px)', padding: '12px 20px', fontSize: 14, whiteSpace: 'nowrap',
}
const ghostBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', fontFamily: 'inherit',
  borderRadius: 10, padding: '9px 14px', fontSize: 13, whiteSpace: 'nowrap',
}
const dangerBtn: React.CSSProperties = {
  background: 'var(--hunt)', color: '#fff', fontWeight: 700, border: 'none', fontFamily: 'inherit',
  borderRadius: 10, padding: '9px 14px', fontSize: 13, whiteSpace: 'nowrap',
}
// 全站樣式重置掉了 ul 的項目符號：說明清單要自己補回圓點，使用者才分得出一條一條的重點。
const bulletList: React.CSSProperties = { margin: '6px 0 0', paddingLeft: 18, listStyleType: 'disc', listStylePosition: 'outside', display: 'flex', flexDirection: 'column', gap: 3 }
const amber: React.CSSProperties = {
  fontSize: 11.5, color: 'var(--tx)', background: 'rgba(245,158,11,.14)', border: '1px solid rgba(245,158,11,.35)',
  borderRadius: 8, padding: '8px 10px', marginTop: 10, lineHeight: 1.7, overflowWrap: 'anywhere',
}

function noticeStyle(kind: GarminNotice['kind']): React.CSSProperties {
  const base: React.CSSProperties = { fontSize: 12, color: 'var(--tx)', lineHeight: 1.7, borderRadius: 8, padding: '8px 10px', marginTop: 10, overflowWrap: 'anywhere' }
  if (kind === 'success') return { ...base, background: 'rgba(70,227,160,.10)', border: '1px solid var(--fug)' }
  if (kind === 'error') return { ...base, background: 'rgba(255,75,92,.10)', border: '1px solid var(--hunt)' }
  if (kind === 'warn') return { ...base, background: 'rgba(245,158,11,.14)', border: '1px solid rgba(245,158,11,.35)' }
  return { ...base, color: 'var(--tx-dim)', background: 'var(--bg-2)', border: '1px solid var(--line-2)' }
}

// 官方 tile 預留位：有 GARMIN_TILE_SRC 就放官方圖檔，沒有就是一個虛線框（明確標示「待放」，不畫任何仿製圖形）。
function GarminTileSlot() {
  const box: React.CSSProperties = { width: 40, height: 40, flexShrink: 0, borderRadius: 10, boxSizing: 'border-box', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', overflow: 'hidden' }
  if (GARMIN_TILE_SRC) {
    return (
      <span data-garmin-tile-slot="official" style={box}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={GARMIN_TILE_SRC} alt="Garmin Connect" style={{ width: '100%', height: '100%', objectFit: 'contain', display: 'block' }} />
      </span>
    )
  }
  return (
    <span data-garmin-tile-slot="placeholder" aria-hidden="true"
      style={{ ...box, border: '1px dashed var(--line-2)', color: 'var(--tx-faint)', fontSize: 9, lineHeight: 1.2, textAlign: 'center' }}>
      圖示<br />待放
    </span>
  )
}

export default function GarminCard({ entryHint, hasConnectionHint, notice, onNotice, reloadKey = 0, hasOtherSourceActivity = false, onDisconnected }: GarminCardProps) {
  const [status, setStatus] = useState<GarminStatus | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [failed, setFailed] = useState(false)
  const [consent, setConsent] = useState(false)
  const [busy, setBusy] = useState<'' | 'connect' | 'disconnect'>('')
  const [confirming, setConfirming] = useState(false)

  const shouldFetch = garminShouldFetch(entryHint, hasConnectionHint)
  const load = useCallback(() => {
    withUserAuth((t) => garminApi.status(t))
      .then((s) => { setStatus(s); setFailed(false); setLoaded(true) })
      .catch(() => { setFailed(true); setLoaded(true) })
  }, [])
  useEffect(() => { if (shouldFetch) load() }, [shouldFetch, reloadKey, load])

  // 授權導去 Garmin 後按瀏覽器「上一頁」，頁面可能從 bfcache 原樣還原（按鈕還卡在「連接中…」）→ 還原時解除忙碌。
  useEffect(() => {
    const h = (e: PageTransitionEvent) => { if (e.persisted) setBusy('') }
    window.addEventListener('pageshow', h)
    return () => window.removeEventListener('pageshow', h)
  }, [])

  const view = garminView({ entryHint, hasConnectionHint, status, loaded, failed, hasNotice: !!notice })
  if (view.kind === 'none') return null

  // 首次連接須勾選同意；已連接者的「重新授權」不再要求（同意在首次連接時已取得，實際授權仍在 Garmin 自己的頁面完成）。
  async function connect(reauth: boolean) {
    if (!reauth && !consent) return
    setBusy('connect'); onNotice(null)
    try {
      const { url } = await withUserAuth((t) => garminApi.connect(t))
      if (!isGarminAuthUrl(url)) throw new Error('bad_auth_url')
      window.location.assign(url) // 導去 Garmin 授權；固定導回首頁 /?garmin=connected|error&reason=…（不重設 busy：整頁導向中，避免重複點擊）
    } catch (e: any) {
      onNotice({ kind: 'error', text: e?.message === 'bad_auth_url' ? '連接網址異常，請稍後再試。' : garminErrorText('connect', e) })
      setBusy('')
    }
  }

  async function disconnect() {
    setBusy('disconnect'); onNotice(null)
    try {
      const r = await withUserAuth((t) => garminApi.disconnect(t))
      setStatus((s) => ({ connected: false, entry: s?.entry }))
      setConfirming(false); setConsent(false)
      onNotice(garminDisconnectNotice(r))
      onDisconnected?.() // 後端同交易刪了 Garmin 活動、重設偏好來源：呼叫端把依賴它們的畫面一併刷新
      load()
    } catch (e: any) {
      onNotice({ kind: 'error', text: garminErrorText('disconnect', e) })
    } finally {
      setBusy('')
    }
  }

  const now = Date.now()
  const connected = view.kind === 'connected'
  const attribution = connected ? garminAttribution(status?.device_name) : ''
  const lastData = status?.last_data_at ? garminRelativeAgo(status.last_data_at, now) : ''
  const lastDataAbs = status?.last_data_at ? garminShortTime(status.last_data_at) : ''
  const stale = connected && view.entryShown && !view.needsReauth && !view.paused && hasOtherSourceActivity
    && garminDataStale(status?.last_data_at, status?.connected_at, now)

  return (
    <div data-garmin-card="" data-garmin-view={view.kind} role="region" aria-label="Garmin Connect 直連" style={card}>
      {/* 標題列：官方 tile 預留位＋完整名稱「Garmin Connect」；已連接時標題正下方就是「Garmin ＋ 型號」歸屬（首屏、緊鄰標題） */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <GarminTileSlot />
        <div style={{ minWidth: 0 }}>
          <div style={{ fontWeight: 700, fontSize: 14, color: 'var(--tx)' }}>Garmin Connect 直連</div>
          {connected
            ? <div data-garmin-attribution="" style={{ fontSize: 12, fontWeight: 700, color: 'var(--tx-dim)', marginTop: 1, overflowWrap: 'anywhere' }}>{attribution}</div>
            : view.kind !== 'closed' && <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 1 }}>在 Garmin 授權頁面連接・匯入跑步／走路／健行</div>}
        </div>
      </div>

      {notice && <div role="status" aria-live="polite" data-garmin-notice={notice.kind} style={noticeStyle(notice.kind)}>{notice.text}</div>}

      {view.kind === 'loading' && <div style={{ ...faint, marginTop: 10 }}>載入中…</div>}

      {view.kind === 'error' && (
        <div style={{ marginTop: 10 }}>
          <div style={small}>{garminErrorText('status', null)}</div>
          <button type="button" onClick={() => { setLoaded(false); load() }} style={{ ...ghostBtn, marginTop: 8, cursor: 'pointer' }}>重試</button>
        </div>
      )}

      {view.kind === 'connect' && (
        <>
          {view.legacy && (
            <div role="status" data-garmin-legacy="" style={amber}>
              <b>Garmin 已改為官方直接連接</b>，請重新授權一次（歷史紀錄不受影響）。
            </div>
          )}
          <div style={{ ...small, marginTop: 10 }}>
            連接後，你的 Garmin 手錶把跑步、走路或健行活動同步到 Garmin Connect 時，Garmin 會自動把活動摘要送到 DOR。
          </div>
          <ul style={{ ...bulletList, ...small }}>
            <li>DOR 會收到的資料：活動類型、開始時間、時間長度、距離、爬升、配速、平均心率與手錶型號；不保存位置座標與路線。</li>
            <li>資料由 Garmin 在你把手錶同步到 Garmin Connect 之後自動送達，可能需要幾分鐘到數小時；DOR 無法自行提前取得，所以沒有「匯入數據」按鈕。</li>
            <li>只匯入你連接之後開始的活動，不會匯入過去的紀錄。</li>
            <li>用於計入 DOR 里程、經驗值與獎勵，以及你參加且開放外部裝置數據的賽事；其他使用者只會看到由此算出的結果（例如里程總計、完賽與名次），看不到你的單筆活動或心率。</li>
            <li>Garmin 資料不會用於訓練 AI，也不會交給任何 AI 服務處理。</li>
            <li>可隨時按「中斷連線」：DOR 會刪除已匯入的 Garmin 活動紀錄（賽事成績會重新計算）；已獲得的 EXP／DP 等獎勵不會收回。</li>
          </ul>
          <div style={{ ...faint, marginTop: 6 }}>
            詳見 <a href="/privacy#garmin" target="_blank" rel="noreferrer" style={{ color: 'var(--fug)' }}>隱私權政策</a>。
          </div>
          <label style={{ display: 'flex', gap: 8, alignItems: 'flex-start', marginTop: 10, fontSize: 11.5, color: 'var(--tx-dim)', lineHeight: 1.6, cursor: 'pointer' }}>
            <input data-garmin-consent="" type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} style={{ marginTop: 3, flexShrink: 0 }} />
            我已了解上述說明，同意透過 Garmin Connect 將我的活動資料分享給 DOR
          </label>
          <button data-garmin-connect-btn="" type="button" onClick={() => connect(false)} disabled={!consent || busy !== ''}
            style={{ ...primaryBtn, marginTop: 10, opacity: !consent || busy !== '' ? 0.5 : 1, cursor: !consent || busy !== '' ? 'default' : 'pointer' }}>
            {busy === 'connect' ? '連接中…' : view.legacy ? '重新授權 Garmin Connect' : '連接 Garmin Connect'}
          </button>
          <div style={{ ...faint, marginTop: 6 }}>按下按鈕後會前往 Garmin 的授權頁面；在那裡登入並同意後，會自動回到 DOR。</div>
        </>
      )}

      {view.kind === 'connected' && (
        <>
          <div style={{ ...small, color: 'var(--tx)', marginTop: 10 }}>
            ✓ 已連接{status?.connected_at ? ` · ${garminDay(status.connected_at)}` : ''}
          </div>
          <div data-garmin-last-data="" style={small}>
            {lastData
              ? `最後收到資料：${lastData}${lastDataAbs ? `（${lastDataAbs}）` : ''}`
              : '尚未收到資料——手錶同步到 Garmin Connect 後才會送達，可能需要幾分鐘到數小時。'}
          </div>
          {status?.import_from && garminDay(status.import_from) && (
            <div style={small}>只匯入 {garminDay(status.import_from)} 之後開始的活動。</div>
          )}

          {!view.entryShown && (
            <div role="status" data-garmin-entry-closed="" style={amber}>
              Garmin 直連目前暫停開放，暫時無法連接或重新授權。你仍可隨時按「中斷連線」——DOR 會刪除已匯入的 Garmin 紀錄。
            </div>
          )}
          {view.entryShown && view.needsReauth && (
            <div role="status" data-garmin-reauth="" style={amber}>
              <b>Garmin 授權需要更新</b>。請按「重新授權」——不會刪除你已匯入的紀錄。更新前，DOR 可能收不到你的新紀錄。
            </div>
          )}
          {view.paused && (
            <div role="status" data-garmin-paused="" style={amber}>
              <b>你已在 Garmin Connect 關閉「活動」資料的分享</b>，DOR 目前不會收到新的紀錄（已匯入的紀錄不會被刪除）。
              要恢復：請開啟 Garmin Connect（手機 App 或網頁版），到「連結的應用程式（Connected Apps）」設定找到 DOR，重新開啟「活動」資料的分享
              {view.entryShown ? '；或按下方「重新授權」，在 Garmin 頁面重新勾選「活動」。' : '。'}
            </div>
          )}
          {stale && (
            <div data-garmin-stale="" style={{ ...faint, marginTop: 10 }}>
              已超過 48 小時沒有收到 Garmin 的資料。若你確定期間有跑步，請確認手錶已同步到 Garmin Connect。
            </div>
          )}

          {confirming ? (
            <div data-garmin-confirm="" role="group" aria-label="確認中斷 Garmin 連線"
              style={{ marginTop: 10, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px 12px' }}>
              <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx)' }}>確定要中斷 Garmin 連線嗎？</div>
              <ul style={{ ...bulletList, ...small }}>
                <li>DOR 會刪除已匯入的 Garmin 活動紀錄，你參加的賽事成績會重新計算。</li>
                <li>已獲得的 EXP／DP 等獎勵不會收回。</li>
                <li>DOR 會通知 Garmin 撤銷授權；若通知沒成功，請到 Garmin Connect 的「連結的應用程式（Connected Apps）」設定中移除 DOR。</li>
              </ul>
              <div style={{ display: 'flex', gap: 8, marginTop: 10, flexWrap: 'wrap' }}>
                <button data-garmin-confirm-yes="" type="button" onClick={disconnect} disabled={busy !== ''}
                  style={{ ...dangerBtn, opacity: busy !== '' ? 0.6 : 1, cursor: busy !== '' ? 'default' : 'pointer' }}>
                  {busy === 'disconnect' ? '處理中…' : '確定中斷'}
                </button>
                <button data-garmin-confirm-no="" type="button" onClick={() => setConfirming(false)} disabled={busy !== ''}
                  style={{ ...ghostBtn, cursor: 'pointer' }}>取消</button>
              </div>
            </div>
          ) : (
            <div style={{ display: 'flex', gap: 8, marginTop: 10, flexWrap: 'wrap' }}>
              {view.entryShown && (
                <button data-garmin-reauth-btn="" type="button" onClick={() => connect(true)} disabled={busy !== ''}
                  style={{ ...(view.needsReauth ? primaryBtn : ghostBtn), padding: '9px 14px', fontSize: 13, opacity: busy !== '' ? 0.6 : 1, cursor: busy !== '' ? 'default' : 'pointer' }}>
                  {busy === 'connect' ? '前往 Garmin…' : '重新授權'}
                </button>
              )}
              <button data-garmin-disconnect-btn="" type="button" onClick={() => setConfirming(true)} disabled={busy !== ''}
                style={{ ...ghostBtn, opacity: busy !== '' ? 0.6 : 1, cursor: busy !== '' ? 'default' : 'pointer' }}>
                中斷連線
              </button>
            </div>
          )}
          <div style={{ ...faint, marginTop: 8 }}>
            資料由 Garmin 在你的手錶同步到 Garmin Connect 後自動送達（可能需要幾分鐘到數小時）。重新授權不會刪除已匯入的紀錄。
          </div>
        </>
      )}

      {(view.kind === 'connect' || view.kind === 'connected' || view.kind === 'closed') && (
        <div data-garmin-disclaimer="" style={{ ...faint, marginTop: 8 }}>DOR 與 Garmin 沒有隸屬關係，也未獲得 Garmin 背書。</div>
      )}
    </div>
  )
}
