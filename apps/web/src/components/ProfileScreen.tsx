'use client'

import { useEffect, useState } from 'react'
import { profileApi, paymentsApi, integrationsApi, followApi, type Profile, type MyRegistration, type MyOrder, type StravaStatus, type SyncedActivity, type DashboardInfo, type FollowRow } from '@/lib/api'
import { getUserToken, withUserAuth, SessionExpiredError } from '@/lib/userAuth'
import DpCoin from './DpCoin'
import ScrollArea from './ScrollArea'

// 動態建立 hidden 表單並 POST 到綠界（瀏覽器導去付款頁）
function submitEcpayForm(actionURL: string, params: Record<string, string>) {
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = actionURL
  form.acceptCharset = 'UTF-8'
  for (const [k, v] of Object.entries(params)) {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = k
    input.value = v
    form.appendChild(input)
  }
  document.body.appendChild(form)
  form.submit()
}

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
function expPct(d: { exp: number; level_floor: number; next_level_exp: number | null }) {
  if (d.next_level_exp == null) return 100
  const span = d.next_level_exp - d.level_floor
  if (span <= 0) return 100
  return Math.max(0, Math.min(100, ((d.exp - d.level_floor) / span) * 100))
}

export default function ProfileScreen({ onBack, focusRaceID }: { onBack: () => void; focusRaceID?: string }) {
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
  const [dash, setDash] = useState<DashboardInfo | null>(null)
  const [tab, setTab] = useState<'info' | 'sports' | 'records' | 'follows'>('info')
  const [uploadingAvatar, setUploadingAvatar] = useState(false)
  const [codeCopied, setCodeCopied] = useState(false)
  const [follows, setFollows] = useState<FollowRow[] | null>(null)

  function loadDashboard() {
    withUserAuth((t) => profileApi.dashboard(t)).then((r) => setDash(r.dashboard)).catch(() => {})
  }
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
  function copyCode() {
    if (!dash?.account_code) return
    navigator.clipboard?.writeText(dash.account_code).then(() => { setCodeCopied(true); setTimeout(() => setCodeCopied(false), 1500) }).catch(() => {})
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
      <header style={{ padding: 'var(--app-top) 22px 8px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
      </header>

      <ScrollArea padding="4px 18px 28px">
        {err && <div style={{ color: 'var(--hunt)', padding: 16, fontSize: 13 }}>{err}</div>}

        {/* Dashboard */}
        {dash && (
          <div style={dashCard}>
            <div style={{ display: 'flex', gap: 14, alignItems: 'center' }}>
              <label style={avatarWrap} title="更換頭像">
                {dash.avatar_url
                  // eslint-disable-next-line @next/next/no-img-element
                  ? <img src={dash.avatar_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                  : <span style={{ fontSize: 24, fontWeight: 800, color: 'var(--tx-dim)' }}>{(dash.name || '?').slice(0, 1)}</span>}
                <span style={avatarEdit}>{uploadingAvatar ? '…' : '✎'}</span>
                <input type="file" accept="image/*" style={{ display: 'none' }}
                  onChange={(e) => { const f = e.target.files?.[0]; if (f) onAvatar(f); e.target.value = '' }} />
              </label>
              <div style={{ minWidth: 0, flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)' }}>{dash.name || '未命名'}</span>
                  {dash.is_vip && <span style={vipBadge}>VIP</span>}
                </div>
                {dash.nickname && <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{dash.nickname}</div>}
                <button onClick={copyCode} style={codeChip} title="複製帳號編碼">
                  #{dash.account_code} {codeCopied ? '已複製' : '⧉'}
                </button>
              </div>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, color: '#FFD24D', fontWeight: 900, fontSize: 14, fontVariantNumeric: 'tabular-nums', flexShrink: 0 }} title="DP 幣">
                <DpCoin size={16} />{(dash.dp ?? 0).toLocaleString()}
              </span>
            </div>

            {/* 等級 + EXP */}
            <div style={{ marginTop: 14 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', fontSize: 12 }}>
                <span style={{ fontWeight: 800, color: 'var(--fug)' }}>Lv.{dash.level}{dash.level_title ? ` ${dash.level_title}` : ''}</span>
                <span style={{ color: 'var(--tx-dim)' }}>{dash.exp} EXP</span>
              </div>
              <div style={expBarOuter}>
                <div style={{ ...expBarInner, width: `${expPct(dash)}%` }} />
              </div>
              <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 4, textAlign: 'right' }}>
                {dash.next_level_exp == null ? '已達最高等級'
                  : `距 Lv.${dash.level + 1} 還需 ${dash.next_level_exp - dash.exp} EXP`}
              </div>
            </div>


            <div style={{ display: 'flex', gap: 16, marginTop: 12, fontSize: 11.5, color: 'var(--tx-dim)', flexWrap: 'wrap' }}>
              <span>累積 {dash.total_km.toFixed(1)} K</span>
              <span>報名 {dash.race_count} 場</span>
              <span>追蹤 <b style={{ color: 'var(--tx)' }}>{dash.following_count}</b></span>
              <span>粉絲 <b style={{ color: 'var(--tx)' }}>{dash.follower_count}</b></span>
              <span>{dash.is_vip ? `VIP 至 ${dash.vip_expires_at ? fmtDate(dash.vip_expires_at).slice(0, 10) : ''}` : '一般會員'}</span>
            </div>
          </div>
        )}

        {/* 頁籤 */}
        <div style={{ display: 'flex', gap: 6, margin: '18px 0 14px', borderBottom: '1px solid var(--line)' }}>
          {([['info', '個人資料'], ['sports', '運動數據'], ['records', '報名紀錄'], ['follows', '追蹤']] as const).map(([v, label]) => (
            <button key={v} onClick={() => setTab(v)} style={{
              padding: '8px 9px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 14, whiteSpace: 'nowrap',
              color: tab === v ? 'var(--tx)' : 'var(--tx-dim)', fontWeight: tab === v ? 700 : 400,
              borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent',
            }}>{label}</button>
          ))}
        </div>

        {!p && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}

        {/* 頁籤①個人資料 */}
        {tab === 'info' && p && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
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

          {/* Strava 資料來源歸屬（品牌合規） */}
          <div style={{ marginTop: 16, display: 'flex', justifyContent: 'center' }}>
            <a href="https://www.strava.com" target="_blank" rel="noreferrer" aria-label="Powered by Strava">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src="/strava/powered_by_strava.svg" alt="Powered by Strava" style={{ height: 18, display: 'block', opacity: 0.85 }} />
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
            聯絡我們：<a href="mailto:info@hero-mi.com" style={{ color: 'var(--fug)', textDecoration: 'none', fontWeight: 700 }}>info@hero-mi.com</a>
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
      </ScrollArea>

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
                <button onClick={goEcpay} disabled={paying} style={{ ...primaryBtn, width: '100%', background: 'var(--gold)', color: '#1a1200' }}>
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
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 'var(--radius-btn, 10px)', padding: '12px 20px', cursor: 'pointer', fontSize: 14, marginTop: 4,
}
const recCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 14px)', padding: 14 }
const ghostBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)',
  borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit',
}
const dashCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-lg, 16px)', padding: 16, boxShadow: 'var(--card-shadow, none)' }
const avatarWrap: React.CSSProperties = {
  position: 'relative', width: 64, height: 64, borderRadius: '50%', overflow: 'hidden', flexShrink: 0,
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer',
}
const avatarEdit: React.CSSProperties = { position: 'absolute', bottom: 0, left: 0, right: 0, fontSize: 10, textAlign: 'center', background: 'rgba(0,0,0,.55)', color: '#fff', padding: '1px 0' }
const vipBadge: React.CSSProperties = { fontSize: 10, fontWeight: 800, color: '#1a1200', background: 'var(--gold)', borderRadius: 6, padding: '1px 7px', letterSpacing: '.05em' }
const codeChip: React.CSSProperties = { marginTop: 4, fontSize: 11, color: 'var(--tx-dim)', background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '2px 10px', cursor: 'pointer', fontFamily: 'monospace' }
const expBarOuter: React.CSSProperties = { height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 5 }
const expBarInner: React.CSSProperties = { height: '100%', background: 'var(--fug)', borderRadius: 999, transition: 'width .3s' }
const payBtn: React.CSSProperties = {
  background: 'var(--gold)', color: '#1a1200', fontWeight: 700, border: 'none',
  borderRadius: 'var(--radius-btn, 9px)', padding: '7px 14px', cursor: 'pointer', fontSize: 13,
}
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 60, padding: 20 }
const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: 20, width: '100%', maxWidth: 420 }
