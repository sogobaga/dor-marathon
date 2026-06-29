'use client'

import { useEffect, useState } from 'react'
import { profileApi, paymentsApi, integrationsApi, type Profile, type MyRegistration, type MyOrder, type StravaStatus } from '@/lib/api'
import { getUserToken, withUserAuth, SessionExpiredError } from '@/lib/userAuth'

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

function ntd(c: number) {
  return 'NT$ ' + Math.round(c / 100).toLocaleString('zh-TW')
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

  function loadStrava() {
    withUserAuth((t) => integrationsApi.stravaStatus(t)).then(setStrava).catch(() => {})
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
      const res = await withUserAuth((t) =>
        profileApi.updateMe(t, {
          real_name: p.real_name, nickname: p.nickname, phone: p.phone,
          address: p.address, birthday: p.birthday, gender: p.gender,
        })
      )
      setP(res.profile)
      setSaved(true)
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '儲存失敗')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: '52px 22px 14px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>個人資訊</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>報名時會自動帶入這些資料</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', padding: '4px 18px 28px' }}>
        {err && <div style={{ color: 'var(--hunt)', padding: 16, fontSize: 13 }}>{err}</div>}
        {!p && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}

        {p && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <Field label="Email（Google 帳號）"><input style={{ ...inp, opacity: 0.6 }} value={p.email} disabled /></Field>
            <Field label="真實姓名"><input style={inp} value={p.real_name} onChange={(e) => set('real_name', e.target.value)} /></Field>
            <Field label="暱稱"><input style={inp} value={p.nickname} onChange={(e) => set('nickname', e.target.value)} /></Field>
            <Field label="手機"><input style={inp} value={p.phone} onChange={(e) => set('phone', e.target.value)} /></Field>
            <Field label="地址"><input style={inp} value={p.address} onChange={(e) => set('address', e.target.value)} /></Field>
            <div style={{ display: 'flex', gap: 12 }}>
              <Field label="生日"><input style={inp} type="date" value={p.birthday} onChange={(e) => set('birthday', e.target.value)} /></Field>
              <Field label="性別">
                <select style={inp} value={p.gender} onChange={(e) => set('gender', e.target.value as Profile['gender'])}>
                  {GENDERS.map((g) => <option key={g.v} value={g.v}>{g.t}</option>)}
                </select>
              </Field>
            </div>
            {saved && <div style={{ color: 'var(--fug)', fontSize: 13 }}>✓ 已儲存</div>}
            <button onClick={save} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存'}</button>
          </div>
        )}

        {/* 運動數據連接 */}
        <div style={{ marginTop: 28 }}>
          <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', marginBottom: 10 }}>運動數據</div>
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
                <button onClick={disconnectStrava} disabled={stravaBusy} style={{ ...ghostBtn, whiteSpace: 'nowrap' }}>中斷連接</button>
              ) : (
                <button onClick={connectStrava} disabled={stravaBusy || strava?.enabled === false}
                  style={{ background: '#fc4c02', color: '#fff', border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5, fontWeight: 700, whiteSpace: 'nowrap', opacity: stravaBusy ? 0.6 : 1 }}>
                  {stravaBusy ? '處理中…' : '連接 Strava'}
                </button>
              )}
            </div>
            {strava?.enabled === false && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 8 }}>（Strava 整合尚未由管理者設定）</div>}
            {stravaMsg && <div style={{ fontSize: 12.5, color: 'var(--fug)', marginTop: 8 }}>{stravaMsg}</div>}
          </div>
        </div>

        {/* 報名紀錄 */}
        <div style={{ marginTop: 28 }}>
          <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', marginBottom: 10 }}>報名紀錄</div>
          {!regs && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>載入中…</div>}
          {regs && regs.length === 0 && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>尚無報名紀錄</div>}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {regs?.map((r) => {
              const st = REG_STATUS[r.status] ?? { t: r.status, c: 'var(--tx-dim)' }
              return (
                <div key={r.registration_id} style={recCard}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 8 }}>
                    <div>
                      <div style={{ fontWeight: 700, fontSize: 15 }}>{r.race_title}</div>
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
      </div>

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
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6, flex: 1 }}>
      <span style={{ fontSize: 11, letterSpacing: '.08em', color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '11px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 10, padding: '12px 20px', cursor: 'pointer', fontSize: 14, marginTop: 4,
}
const recCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14 }
const ghostBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)',
  borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit',
}
const payBtn: React.CSSProperties = {
  background: 'var(--gold)', color: '#1a1200', fontWeight: 700, border: 'none',
  borderRadius: 9, padding: '7px 14px', cursor: 'pointer', fontSize: 13,
}
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 60, padding: 20 }
const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: 20, width: '100%', maxWidth: 420 }
