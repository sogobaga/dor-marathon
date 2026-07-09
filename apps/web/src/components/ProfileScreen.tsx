'use client'

import { useEffect, useState } from 'react'
import { profileApi, paymentsApi, integrationsApi, followApi, settingsApi, type Profile, type MyRegistration, type MyOrder, type StravaStatus, type SyncedActivity, type FollowRow, type SiteSettings } from '@/lib/api'
import { getUserToken, withUserAuth, SessionExpiredError } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import MemberPanel from './MemberPanel'
import UpgradeVipModal from './UpgradeVipModal'
import { useDraggableSheet } from '@/lib/useDraggableSheet'
import { submitEcpayForm } from '@/lib/ecpay'

const GENDERS = [
  { v: '', t: '未填' },
  { v: 'male', t: '男' },
  { v: 'female', t: '女' },
  { v: 'other', t: '其他' },
]
const REG_STATUS: Record<string, { t: string; c: string }> = {
  paid: { t: '報名完成', c: 'var(--fug)' },
  pending: { t: '待繳費', c: 'var(--gold)' },
  cancelled: { t: '已取消', c: 'var(--tx-faint)' },
}
const ITEM_LABEL: Record<string, string> = { entry: '報名費', addon: '加購', discount: '優惠折抵' }
const FLAG_LABEL: Record<string, string> = {
  multi_device_duplicate: '多裝置重複',
  cross_account_duplicate: '跨帳號重複',
  duplicate: '重複資料',
}

function ntd(c: number) {
  return 'NT$ ' + Math.round(c / 100).toLocaleString('zh-TW')
}
function fmtDate(iso: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function paceStr(sec: number) {
  if (!sec || sec <= 0) return '—'
  return `${Math.floor(sec / 60)}:${String(Math.round(sec % 60)).padStart(2, '0')}`
}

export default function ProfileScreen({ onBack, focusRaceID, onOpenPersonalTasks, onOpenExplore, onOpenGallery }: { onBack: () => void; focusRaceID?: string; onOpenPersonalTasks?: () => void; onOpenExplore?: () => void; onOpenGallery?: () => void }) {
  const [p, setP] = useState<Profile | null>(null)
  const [regs, setRegs] = useState<MyRegistration[] | null>(null)
  const [payOrder, setPayOrder] = useState<MyOrder | null>(null)
  const [paying, setPaying] = useState(false)
  const [err, setErr] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [strava, setStrava] = useState<StravaStatus | null>(null)
  const [stravaBusy, setStravaBusy] = useState(false)
  const [stravaMsg, setStravaMsg] = useState('')
  const [activities, setActivities] = useState<SyncedActivity[] | null>(null)
  const [syncing, setSyncing] = useState(false)
  const { dash, revalidate: loadDashboard } = useDashboard() // 共用會員儀表板快取（與首頁會員卡同一份）
  const [tab, setTab] = useState<'info' | 'sports' | 'records' | 'follows'>('info')
  const [uploadingAvatar, setUploadingAvatar] = useState(false)
  const [codeCopied, setCodeCopied] = useState(false)
  const [showUpgrade, setShowUpgrade] = useState(false)
  const [follows, setFollows] = useState<FollowRow[] | null>(null)
  const [site, setSite] = useState<SiteSettings | null>(null) // 全站外觀設定（含 Strava 標章雙版本 URL）
  // COROS 式 UX：會員資訊面板固定最上方，分頁內容做成可上下拖曳面板（收合看完整會員面板／半展看分頁／全展看整份內容）
  const sheet = useDraggableSheet('half')

  function loadFollows() {
    withUserAuth((t) => profileApi.follows(t)).then((r) => setFollows(r.following)).catch(() => {})
  }
  async function unfollow(userId: string) {
    try {
      await withUserAuth((t) => followApi.unfollow(t, userId))
      setFollows((f) => (f ? f.filter((x) => x.user_id !== userId) : f))
      loadDashboard()
    } catch { /* ignore */ }
  }
  function profilePayload(x: Profile): Partial<Profile> {
    return { name: x.name, avatar_url: x.avatar_url, real_name: x.real_name, nickname: x.nickname, phone: x.phone, address: x.address, birthday: x.birthday, gender: x.gender }
  }
  async function onAvatar(file: File) {
    if (!p) return
    setUploadingAvatar(true); setErr('')
    try {
      const { url } = await withUserAuth((t) => profileApi.uploadAvatar(t, file))
      const res = await withUserAuth((t) => profileApi.updateMe(t, { ...profilePayload(p), avatar_url: url }))
      setP(res.profile)
      loadDashboard()
    } catch (e: any) {
      setErr(e?.message || '頭像上傳失敗')
    } finally {
      setUploadingAvatar(false)
    }
  }

  function loadStrava() {
    withUserAuth((t) => integrationsApi.stravaStatus(t))
      .then((s) => { setStrava(s); if (s.connected) loadActivities() })
      .catch(() => {})
  }
  function loadActivities() {
    withUserAuth((t) => integrationsApi.stravaActivities(t)).then((r) => setActivities(r.activities)).catch(() => {})
  }
  async function syncNow() {
    setSyncing(true)
    setStravaMsg('')
    try {
      const r = await withUserAuth((t) => integrationsApi.stravaSync(t))
      setStravaMsg(`同步完成：新增 ${r.imported} 筆${r.duplicates ? `、排除重複 ${r.duplicates} 筆` : ''}`)
      loadActivities()
    } catch (e: any) {
      setStravaMsg(e?.message || '同步失敗')
    } finally {
      setSyncing(false)
    }
  }

  useEffect(() => {
    if (!getUserToken()) {
      setErr('請先登入')
      return
    }
    loadStrava()
    settingsApi.get().then((r) => setSite(r.settings)).catch(() => {}) // Strava 標章雙版本 URL
    // 處理 Strava OAuth 導回參數
    if (typeof window !== 'undefined') {
      const sp = new URLSearchParams(window.location.search)
      const s = sp.get('strava')
      if (s) {
        setStravaMsg(s === 'connected' ? '✓ 已連接 Strava，正在同步近期活動…'
          : s === 'denied' ? '已取消授權'
          : 'Strava 連接失敗，請再試一次')
        sp.delete('strava')
        const qs = sp.toString()
        window.history.replaceState(null, '', window.location.pathname + (qs ? `?${qs}` : ''))
      }
    }
    loadDashboard()
    loadFollows()
    withUserAuth((t) => profileApi.getMe(t))
      .then((r) => setP(r.profile))
      .catch((e) => setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '載入失敗'))

    withUserAuth((t) => profileApi.registrations(t))
      .then((r) => {
        setRegs(r.registrations)
        // 從「前往繳費」進來：自動開啟該賽事待繳費訂單
        if (focusRaceID) {
          const target = r.registrations.find((x) => x.race_id === focusRaceID && x.status === 'pending' && x.order_id)
          if (target?.order_id) openPay(target.order_id)
        }
      })
      .catch(() => {})
  }, [focusRaceID])

  async function openPay(orderID: string) {
    try {
      const { order } = await withUserAuth((t) => profileApi.order(t, orderID))
      setPayOrder(order)
    } catch (e: any) {
      setErr(e?.message || '載入繳費資訊失敗')
    }
  }

  async function goEcpay() {
    if (!payOrder) return
    setPaying(true)
    try {
      const { action_url, params } = await withUserAuth((t) => paymentsApi.ecpayCheckout(t, payOrder.id))
      submitEcpayForm(action_url, params) // 導去綠界，不會 return
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請重新登入' : e?.message || '無法前往付款')
      setPaying(false)
    }
  }

  async function connectStrava() {
    setStravaBusy(true)
    try {
      // 帶回程網址＝目前頁面（同源），授權後導回這裡，session 不會掉
      const returnUrl = window.location.origin + window.location.pathname
      const { url } = await withUserAuth((t) => integrationsApi.stravaConnectUrl(t, returnUrl))
      window.location.href = url // 導去 Strava 授權
    } catch (e: any) {
      setStravaMsg(e?.message || '無法連接 Strava')
      setStravaBusy(false)
    }
  }
  async function disconnectStrava() {
    if (!window.confirm('中斷 Strava 連接？已同步的活動會保留。')) return
    setStravaBusy(true)
    try {
      await withUserAuth((t) => integrationsApi.stravaDisconnect(t))
      setStrava({ connected: false, enabled: strava?.enabled ?? true })
      setStravaMsg('已中斷 Strava 連接')
    } catch (e: any) {
      setStravaMsg(e?.message || '中斷失敗')
    } finally {
      setStravaBusy(false)
    }
  }

  function set<K extends keyof Profile>(k: K, v: Profile[K]) {
    setP((prev) => (prev ? { ...prev, [k]: v } : prev))
    setSaved(false)
  }

  async function save() {
    if (!p) return
    setErr('')
    setSaving(true)
    try {
      const res = await withUserAuth((t) => profileApi.updateMe(t, profilePayload(p)))
      setP(res.profile)
      setSaved(true)
      loadDashboard()
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '儲存失敗')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 10 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        {/* 加入 LINE 社群：與「返回」同層、靠右對齊 */}
        <a href="https://line.me/ti/g2/aWgkU9OMGvCDJy6pTCejNRzgaPB6yosiMXKkew?utm_source=invitation&utm_medium=link_copy&utm_campaign=default"
          target="_blank" rel="noopener noreferrer"
          style={{ marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 5, background: '#06C755', color: '#fff', fontSize: 12, fontWeight: 700, padding: '6px 12px', borderRadius: 999, textDecoration: 'none', whiteSpace: 'nowrap' }}>
          <span aria-hidden>👥</span>加入社群認識更多跑友
        </a>
      </header>

      {/* 會員資訊面板固定最上方 + 可拖曳（個人資料/運動數據/報名紀錄/追蹤）面板 */}
      <div ref={sheet.wrapRef} style={{ position: 'relative', flex: 1, minHeight: 0, overflow: 'hidden' }}>
        {/* 背景層：會員資訊面板（收合時完整顯示，可自行捲動） */}
        <div style={{ position: 'absolute', inset: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '4px 18px 0' }}>
        {err && <div style={{ color: 'var(--hunt)', padding: '8px 2px', fontSize: 13 }}>{err}</div>}
        {/* 會員資訊面板：與首頁共用同一元件、內容一致（此頁頭像可上傳） */}
        <MemberPanel onUploadAvatar={onAvatar} uploadingAvatar={uploadingAvatar} onOpenPersonalTasks={onOpenPersonalTasks} onOpenExplore={onOpenExplore} onOpenGallery={onOpenGallery} />
        </div>{/* /背景層：會員資訊面板 */}

        {/* 可拖曳面板：分頁（個人資料/運動數據/報名紀錄/追蹤）+ 內容 */}
        <div style={{
          position: 'absolute', left: 0, right: 0, top: sheet.curY, bottom: 0,
          transition: !sheet.dragging && sheet.ready ? 'top .28s cubic-bezier(.22,.61,.36,1)' : 'none',
          opacity: sheet.ready ? 1 : 0,
          display: 'flex', flexDirection: 'column',
          background: 'var(--bg)', color: 'var(--tx)',
          borderTopLeftRadius: 18, borderTopRightRadius: 18,
          borderTop: '1px solid var(--line)', boxShadow: '0 -10px 30px rgba(0,0,0,.22)',
          zIndex: 500, userSelect: 'none', WebkitUserSelect: 'none',
        }}>
          {/* 把手 + 分頁列：整個頂部皆可拖曳（移動超過門檻才拖曳，故分頁仍可點切換） */}
          <div ref={sheet.peekRef} {...sheet.handlers} style={{ flexShrink: 0, touchAction: 'none', cursor: 'grab' }}>
            <div style={{ padding: '8px 0 6px' }}>
              <div style={{ width: 40, height: 5, borderRadius: 3, background: 'var(--line-2)', margin: '0 auto' }} />
            </div>
            <div style={{ display: 'flex', gap: 6, padding: '2px 14px 0', borderBottom: '1px solid var(--line)' }}>
              {([['info', '個人資料'], ['sports', '運動數據'], ['records', '報名紀錄'], ['follows', '追蹤']] as const).map(([v, label]) => (
                <button key={v} onClick={() => setTab(v)} style={{
                  padding: '8px 9px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 14, whiteSpace: 'nowrap',
                  color: tab === v ? 'var(--tx)' : 'var(--tx-dim)', fontWeight: tab === v ? 700 : 400,
                  borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent',
                }}>{label}</button>
              ))}
            </div>
          </div>

          {/* 分頁內容（可捲動）：userSelect 還原成 text，避免面板的 userSelect:none 讓「個人資料」輸入框在 iOS 無法聚焦/編輯 */}
          <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', touchAction: 'pan-y', userSelect: 'text', WebkitUserSelect: 'text', padding: '14px 18px calc(20px + var(--cta-safe, 0px))' }}>
        {!p && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}

        {/* 頁籤①個人資料 */}
        {tab === 'info' && p && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {/* 帳號資訊（唯讀，純文字呈現；非可編輯項目，不用輸入框樣式。帳號編碼保留一鍵複製） */}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 9, fontSize: 14, paddingBottom: 12, borderBottom: '1px solid var(--line)' }}>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
                <span style={{ color: 'var(--tx-faint)' }}>帳號：</span>
                <span style={{ fontFamily: 'monospace', fontWeight: 600, color: 'var(--tx)' }}>{dash?.account_code ? `#${dash.account_code}` : '…'}</span>
                <button type="button" onClick={() => { if (dash?.account_code) navigator.clipboard?.writeText(dash.account_code).then(() => { setCodeCopied(true); setTimeout(() => setCodeCopied(false), 1500) }).catch(() => {}) }}
                  style={{ marginLeft: 2, background: 'none', border: 'none', padding: 0, cursor: 'pointer', fontSize: 12, color: codeCopied ? 'var(--fug)' : 'var(--tx-dim)', textDecoration: 'underline' }}>{codeCopied ? '已複製' : '複製'}</button>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <span style={{ color: 'var(--tx-faint)' }}>會員身分：</span>
                <span style={{ fontWeight: dash?.is_vip ? 700 : 500, color: dash?.is_vip ? 'var(--gold)' : 'var(--tx)' }}>
                  {dash?.is_vip ? `VIP${dash.vip_expires_at ? ` (至 ${fmtDate(dash.vip_expires_at).slice(0, 10)})` : ''}` : '一般會員'}
                </span>
                {dash && !dash.is_vip && (
                  <button type="button" onClick={() => setShowUpgrade(true)}
                    style={{ background: 'var(--gold)', color: '#fff', border: 'none', borderRadius: 7, padding: '3px 10px', cursor: 'pointer', fontSize: 11.5, fontWeight: 800 }}>✦ 升級VIP</button>
                )}
              </div>
              {dash?.is_vip && (
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
                  <span style={{ color: 'var(--tx-faint)' }}>100元活動優惠券：</span>
                  <span style={{ fontWeight: 700, color: 'var(--gold)' }}>{dash.activity_coupon_balance ?? 0} 張</span>
                </div>
              )}
            </div>
            <Field label="顯示名稱"><input style={inp} value={p.name} onChange={(e) => set('name', e.target.value)} /></Field>
            <Field label="Email（Google 帳號）"><input style={{ ...inp, opacity: 0.6 }} value={p.email} disabled /></Field>
            <Field label="真實姓名"><input style={inp} value={p.real_name} onChange={(e) => set('real_name', e.target.value)} /></Field>
            <Field label="暱稱"><input style={inp} value={p.nickname} onChange={(e) => set('nickname', e.target.value)} /></Field>
            <Field label="手機"><input style={inp} value={p.phone} onChange={(e) => set('phone', e.target.value)} /></Field>
            <Field label="地址"><input style={inp} value={p.address} onChange={(e) => set('address', e.target.value)} /></Field>
            <Field label="生日"><input style={dateInp} type="date" value={p.birthday} onChange={(e) => set('birthday', e.target.value)} /></Field>
            <Field label="性別">
              <select style={inp} value={p.gender} onChange={(e) => set('gender', e.target.value as Profile['gender'])}>
                {GENDERS.map((g) => <option key={g.v} value={g.v}>{g.t}</option>)}
              </select>
            </Field>
            {saved && <div style={{ color: 'var(--fug)', fontSize: 13 }}>✓ 已儲存</div>}
            <button onClick={save} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存'}</button>
          </div>
        )}

        {/* 頁籤④追蹤列表 */}
        {tab === 'follows' && (
          <div>
            {!follows && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>載入中…</div>}
            {follows && follows.length === 0 && <div style={{ fontSize: 13, color: 'var(--tx-dim)', padding: '8px 0' }}>尚未追蹤任何人，可在賽事「排名」頁追蹤跑者。</div>}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {follows?.map((f) => (
                <div key={f.user_id} style={{ ...recCard, padding: 10, display: 'flex', alignItems: 'center', gap: 10 }}>
                  <div style={{ width: 40, height: 40, borderRadius: '50%', overflow: 'hidden', background: 'var(--bg-2)', border: '1px solid var(--line-2)', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    {f.avatar_url
                      // eslint-disable-next-line @next/next/no-img-element
                      ? <img src={f.avatar_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                      : <span style={{ fontWeight: 800, color: 'var(--tx-dim)' }}>{(f.nickname || '?').slice(0, 1)}</span>}
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 14, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f.nickname}</div>
                    {f.account_code && <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontFamily: 'monospace' }}>#{f.account_code}</div>}
                  </div>
                  <button onClick={() => unfollow(f.user_id)} style={{ ...ghostBtn, padding: '6px 12px', fontSize: 12, flexShrink: 0 }}>解除追蹤</button>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* 頁籤②運動數據 */}
        {tab === 'sports' && (
        <div>
          <div style={recCard}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 700, color: '#fc4c02' }}>Strava</div>
                <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
                  {strava?.connected
                    ? `已連接${strava.athlete_name ? `：${strava.athlete_name}` : ''} · 活動自動同步`
                    : '連接後自動同步跑步活動（含 COROS/Garmin 等同步到 Strava 的裝置），用於任務達成與排行榜'}
                </div>
              </div>
              {strava?.connected ? (
                <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
                  <button onClick={syncNow} disabled={syncing}
                    style={{ background: '#fc4c02', color: '#fff', border: 'none', borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontWeight: 700, whiteSpace: 'nowrap', opacity: syncing ? 0.6 : 1 }}>
                    {syncing ? '同步中…' : '重新同步'}
                  </button>
                  <button onClick={disconnectStrava} disabled={stravaBusy} style={{ ...ghostBtn, whiteSpace: 'nowrap' }}>中斷</button>
                </div>
              ) : (
                <button onClick={connectStrava} disabled={stravaBusy || strava?.enabled === false}
                  aria-label="Connect with Strava"
                  style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', flexShrink: 0, opacity: stravaBusy || strava?.enabled === false ? 0.5 : 1 }}>
                  {/* 官方「Connect with Strava」按鈕（送審前以 Strava ZIP 內官方 PNG 取代同路徑檔即可） */}
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img src="/strava/btn_strava_connect_orange.svg" alt="Connect with Strava" style={{ height: 44, display: 'block' }} />
                </button>
              )}
            </div>
            {strava?.enabled === false && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 8 }}>（Strava 整合尚未由管理者設定）</div>}
            {stravaMsg && <div style={{ fontSize: 12.5, color: 'var(--fug)', marginTop: 8 }}>{stravaMsg}</div>}
            {strava?.connected && (
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>
                要更換 Strava 帳號？請先{' '}
                <a href="https://www.strava.com/logout" target="_blank" rel="noreferrer" style={{ color: '#fc4c02' }}>登出 Strava</a>
                ，再「中斷」後重新連接（連接的是你瀏覽器當下登入的 Strava 帳號）。
              </div>
            )}
          </div>

          {/* 里程優先來源（有 2 個來源時可設定；跨來源去重用） */}
          {strava?.connected && (
            <div style={{ marginTop: 12, background: 'var(--bg-2)', borderRadius: 12, padding: '12px 14px' }}>
              <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)' }}>里程優先來源</div>
              <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 3, lineHeight: 1.6 }}>你同時有「GPS 跑步追蹤」與「Strava」兩個來源。若同一趟被記成兩筆，將以此來源為準、另一筆不計入賽事。</div>
              <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                {(['gps', 'strava'] as const).map((src) => {
                  const on = (p?.preferred_data_source ?? 'gps') === src
                  return (
                    <button key={src} disabled={on}
                      onClick={async () => { setP((c) => c ? { ...c, preferred_data_source: src } : c); try { await withUserAuth((t) => profileApi.setDataSource(t, src)) } catch { /* ignore */ } }}
                      style={{ flex: 1, padding: '9px 0', borderRadius: 10, fontSize: 13, fontWeight: 800, cursor: on ? 'default' : 'pointer', background: on ? 'var(--fug)' : 'transparent', color: on ? 'var(--fug-ink)' : 'var(--tx-dim)', border: `1px solid ${on ? 'var(--fug)' : 'var(--line-2)'}` }}>
                      {src === 'gps' ? 'GPS 跑步追蹤' : 'Strava'}{on ? ' ✓' : ''}
                    </button>
                  )
                })}
              </div>
            </div>
          )}

          {/* 已同步活動 */}
          {strava?.connected && (
            <div style={{ marginTop: 12 }}>
              <div style={{ fontSize: 12.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 8 }}>已同步活動</div>
              {!activities && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>載入中…</div>}
              {activities && activities.length === 0 && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>尚無活動，按「重新同步」匯入近 30 日跑步。</div>}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {activities?.map((a) => (
                  <div key={a.id} style={{ ...recCard, padding: 12, opacity: a.flagged ? 0.6 : 1 }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8 }}>
                      <span style={{ fontWeight: 700, fontSize: 14 }}>{a.distance_km.toFixed(2)} K</span>
                      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{fmtDate(a.recorded_at)}</span>
                    </div>
                    <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 3 }}>
                      配速 {paceStr(a.avg_pace_s)}/km · {Math.round(a.duration_s / 60)} 分
                      {a.ascent_m != null ? ` · 爬升 ${Math.round(a.ascent_m)}m` : ''}
                      {a.avg_hr != null ? ` · 心率 ${a.avg_hr}` : ''}
                    </div>
                    <div style={{ fontSize: 11, marginTop: 3 }}>
                      {a.flagged
                        ? <span style={{ color: 'var(--hunt)' }}>⚠ {FLAG_LABEL[a.flag_reason ?? ''] ?? '重複'}（不計入賽事）</span>
                        : a.race_title
                          ? <span style={{ color: 'var(--fug)' }}>計入：{a.race_title}</span>
                          : <span style={{ color: 'var(--tx-faint)' }}>未對應賽事</span>}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Strava 資料來源歸屬（品牌合規）：依 skin 深淺顯示白/深字版；後台可上傳，未設定則用內建佔位圖。
              兩張都渲染、由 CSS 依 <html data-skin> 決定顯示哪一張（避免 client 判斷造成 SSR 不一致）。 */}
          <div style={{ marginTop: 16, display: 'flex', justifyContent: 'center' }}>
            <a href="https://www.strava.com" target="_blank" rel="noreferrer" aria-label="Powered by Strava">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img className="strava-badge-darkskin" src={site?.strava_powered_dark_url || '/strava/powered_by_strava.svg'} alt="Powered by Strava" style={{ height: 18 }} />
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img className="strava-badge-lightskin" src={site?.strava_powered_light_url || '/strava/powered_by_strava.svg'} alt="Powered by Strava" style={{ height: 18 }} />
            </a>
          </div>
        </div>
        )}

        {/* 頁籤③報名紀錄 */}
        {tab === 'records' && (
        <div>
          {!regs && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>載入中…</div>}
          {regs && regs.length === 0 && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>尚無報名紀錄</div>}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {regs?.map((r) => {
              const st = REG_STATUS[r.status] ?? { t: r.status, c: 'var(--tx-dim)' }
              return (
                <div key={r.registration_id} style={recCard}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 8 }}>
                    <div>
                      <div style={{ fontWeight: 700, fontSize: 15, wordBreak: 'keep-all', overflowWrap: 'break-word' }}>{r.race_title}</div>
                      <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 2 }}>
                        {r.group_revealed ? (r.group_name || '—') : '分組賽事當天公布'}
                      </div>
                    </div>
                    <span style={{ fontSize: 12, color: st.c, fontWeight: 700, flexShrink: 0 }}>{st.t}</span>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 10 }}>
                    <span style={{ fontSize: 13, color: 'var(--tx-dim)' }}>應繳 {ntd(r.order_total_cents)}</span>
                    {r.status === 'pending' && r.order_id && (
                      <button onClick={() => openPay(r.order_id!)} style={payBtn}>前往繳費</button>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
        )}

        {/* 支援與隱私（聯絡 / Strava / 隱私權） */}
        <div style={{ marginTop: 22, paddingTop: 16, borderTop: '1px solid var(--line)', display: 'flex', flexDirection: 'column', gap: 7 }}>
          <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)' }}>支援與隱私</div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
            聯絡我們：<a href="mailto:info@unityprosper.com" style={{ color: 'var(--fug)', textDecoration: 'none', fontWeight: 700 }}>info@unityprosper.com</a>
          </div>
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', lineHeight: 1.7 }}>
            地址：新北市八里區四維街 13 號 2 樓　·　電話：0933-951586　·　統一編號：83005678
          </div>
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', lineHeight: 1.7 }}>
            連接 Strava：到上方「運動數據」分頁點官方「Connect with Strava」即可；要中斷請按「中斷」。我們僅匯入你連接之後的活動，並可隨時中斷。
          </div>
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', lineHeight: 1.7 }}>
            退款：城市探索為線上活動，報名繳費後恕不退款、不適用七天鑑賞期。
          </div>
          <div style={{ fontSize: 12.5, marginTop: 2, display: 'flex', flexWrap: 'wrap', gap: '4px 8px' }}>
            <a href="/support" style={{ color: 'var(--fug)', textDecoration: 'underline' }}>支援說明</a>
            <span style={{ color: 'var(--tx-faint)' }}>·</span>
            <a href="/terms" style={{ color: 'var(--fug)', textDecoration: 'underline' }}>服務條款／退款</a>
            <span style={{ color: 'var(--tx-faint)' }}>·</span>
            <a href="/privacy" style={{ color: 'var(--fug)', textDecoration: 'underline' }}>隱私權政策</a>
          </div>
        </div>
          </div>{/* /分頁內容 */}
        </div>{/* /可拖曳面板 */}
      </div>{/* /容器 */}

      {/* 繳費頁面 */}
      {payOrder && (
        <div style={overlay} onClick={() => setPayOrder(null)}>
          <div style={panel} onClick={(e) => e.stopPropagation()}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
              <strong style={{ fontSize: 17 }}>繳費</strong>
              <button onClick={() => setPayOrder(null)} style={backBtn}>✕</button>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, marginBottom: 10 }}>{payOrder.race_title}</div>

            <div style={{ border: '1px solid var(--line-2)', borderRadius: 12, padding: 14, marginBottom: 14 }}>
              {payOrder.items.map((it, i) => (
                <div key={i} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, color: 'var(--tx-dim)', padding: '3px 0' }}>
                  <span>{ITEM_LABEL[it.item_type] ?? it.item_type}{it.addon_name ? `：${it.addon_name}` : ''}{it.qty > 1 ? ` × ${it.qty}` : ''}</span>
                  <span style={{ color: it.subtotal_cents < 0 ? 'var(--fug)' : 'var(--tx)' }}>{ntd(it.subtotal_cents)}</span>
                </div>
              ))}
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 15, fontWeight: 800, marginTop: 8, paddingTop: 8, borderTop: '1px solid var(--line)' }}>
                <span>應繳金額</span><span style={{ color: 'var(--gold)' }}>{ntd(payOrder.total_cents)}</span>
              </div>
            </div>

            {payOrder.status === 'paid' ? (
              <div style={{ color: 'var(--fug)', fontSize: 14, fontWeight: 700 }}>✓ 已完成繳費</div>
            ) : (
              <>
                <button onClick={goEcpay} disabled={paying} style={{ ...primaryBtn, width: '100%', background: 'var(--gold)', color: '#fff' }}>
                  {paying ? '前往綠界…' : '前往綠界付款'}
                </button>
                <div style={{ marginTop: 10, fontSize: 11, color: 'var(--tx-faint)', lineHeight: 1.6 }}>
                  將導向綠界 ECPay 安全付款頁（信用卡 / ATM / 超商）。付款完成後返回本站，狀態會自動更新為「報名完成」。
                  <div style={{ marginTop: 4 }}>訂單編號：{payOrder.id}</div>
                </div>
              </>
            )}
            <button onClick={() => setPayOrder(null)} style={{ ...primaryBtn, width: '100%', marginTop: 12, background: 'rgba(255,255,255,.06)', color: 'var(--tx)' }}>關閉</button>
          </div>
        </div>
      )}

      {showUpgrade && <UpgradeVipModal onClose={() => setShowUpgrade(false)} />}
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6, flex: 1, minWidth: 0 }}>
      <span style={{ fontSize: 11, letterSpacing: '.08em', color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 'var(--radius-md, 10px)',
  padding: '11px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', boxSizing: 'border-box', fontFamily: 'inherit',
}
// iOS 原生日期框：去除原生外觀以吃滿寬度、文字靠左
const dateInp: React.CSSProperties = {
  ...inp, WebkitAppearance: 'none', appearance: 'none', textAlign: 'left', minWidth: 0, maxWidth: '100%',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
  borderRadius: 'var(--radius-btn, 10px)', padding: '12px 20px', cursor: 'pointer', fontSize: 14, marginTop: 4,
}
const recCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 14px)', padding: 14 }
const ghostBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)',
  borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit',
}
const payBtn: React.CSSProperties = {
  background: 'var(--gold)', color: '#fff', fontWeight: 700, border: 'none',
  borderRadius: 'var(--radius-btn, 9px)', padding: '7px 14px', cursor: 'pointer', fontSize: 13,
}
// zIndex 需高於本頁的可拖曳資訊面板(500)，否則從「報名紀錄」開的繳費視窗會被面板蓋住（見 [[frontend-draggable-sheet]] 疊層慣例）
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1200, padding: 20 }
// maxHeight + overflowY：長訂單（多項加購）在小螢幕不會把「前往綠界付款/關閉」擠到畫面外而點不到
const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: 20, width: '100%', maxWidth: 420, maxHeight: '90dvh', overflowY: 'auto' }
