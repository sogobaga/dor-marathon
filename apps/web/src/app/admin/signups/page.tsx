'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminRacesApi, adminSignupsApi, type Race, type SignupRow, type RaceGroup } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const STATUS_LABEL: Record<string, { t: string; c: string }> = {
  paid: { t: '已付款', c: 'var(--fug)' },
  pending: { t: '待繳費', c: 'var(--gold)' },
  cancelled: { t: '已取消', c: 'var(--tx-faint)' },
}

function ntd(c: number) {
  return 'NT$ ' + Math.round(c / 100).toLocaleString('zh-TW')
}

export default function AdminSignupsPage() {
  const router = useRouter()
  const [races, setRaces] = useState<Race[]>([])
  const [raceID, setRaceID] = useState('')
  const [rows, setRows] = useState<SignupRow[] | null>(null)
  const [groups, setGroups] = useState<RaceGroup[]>([])
  const [q, setQ] = useState('')
  const [appliedQ, setAppliedQ] = useState('')
  const [err, setErr] = useState('')
  const [busyGroup, setBusyGroup] = useState('')
  const [token, setTok] = useState<string | null>(null)

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setTok(t)
    adminRacesApi.list(t).then((r) => {
      setRaces(r.races)
      if (r.races.length) setRaceID(r.races[0].id)
    }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
  }, [router])

  const load = useCallback((rid: string, query: string) => {
    const t = getToken()
    if (!t || !rid) return
    setRows(null)
    setAppliedQ(query)
    adminSignupsApi.list(t, { race_id: rid, q: query })
      .then((r) => { setRows(r.signups); setGroups(r.groups ?? []) })
      .catch((e) => setErr(e?.message || '載入失敗'))
  }, [])

  async function changeGroup(s: SignupRow, groupID: string) {
    if (!token || groupID === (s.group_id ?? '')) return
    setErr(''); setBusyGroup(s.id)
    try {
      await adminSignupsApi.changeGroup(token, s.id, groupID)
      load(raceID, appliedQ) // 重載以更新各組已用名額
    } catch (e: any) {
      setErr(e?.message || '調整分組失敗')
      setBusyGroup('')
    }
  }

  useEffect(() => { if (raceID) load(raceID, '') }, [raceID, load])

  async function markPaid(s: SignupRow) {
    if (!token) return
    if (!window.confirm(`確認將「${s.user_name}」標記為已付款？`)) return
    try {
      await adminSignupsApi.markPaid(token, s.id)
      setRows((rs) => rs?.map((x) => x.id === s.id ? { ...x, status: 'paid', order_status: x.order_status ? 'paid' : x.order_status } : x) ?? rs)
    } catch (e: any) { setErr(e?.message || '操作失敗') }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 18px', fontSize: 24, fontWeight: 800 }}>報名管理</h1>

      <div style={{ display: 'flex', gap: 10, marginBottom: 18, flexWrap: 'wrap' }}>
        <select value={raceID} onChange={(e) => setRaceID(e.target.value)} style={{ ...inp, maxWidth: 280 }}>
          {races.map((r) => <option key={r.id} value={r.id}>{r.title}</option>)}
        </select>
        <form onSubmit={(e) => { e.preventDefault(); load(raceID, q) }} style={{ display: 'flex', gap: 8 }}>
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜尋姓名/Email/手機" style={{ ...inp, maxWidth: 240 }} />
          <button type="submit" style={primaryBtn}>搜尋</button>
        </form>
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: 16 }}>{err}</div>}
      {!rows && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}
      {rows && rows.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>此賽事尚無報名</div>}

      {rows && rows.length > 0 && (
        <div style={{ border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
          <Row head>
            <C w={2}>報名者</C><C w={1}>分組</C><C w={1}>報名狀態</C><C w={1}>訂單</C><C w={1}>操作</C>
          </Row>
          {rows.map((s) => {
            const st = STATUS_LABEL[s.status] ?? { t: s.status, c: 'var(--tx-dim)' }
            return (
              <Row key={s.id}>
                <C w={2}>
                  <div style={{ fontWeight: 600 }}>{s.user_name}{s.snap_real_name && s.snap_real_name !== s.user_name ? `（${s.snap_real_name}）` : ''}</div>
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{s.user_email}{s.snap_phone ? ` · ${s.snap_phone}` : ''}</div>
                </C>
                <C w={1}>
                  {groups.length === 0
                    ? (s.group_name || '—')
                    : (
                      <select
                        value={s.group_id ?? ''}
                        disabled={busyGroup === s.id}
                        onChange={(e) => changeGroup(s, e.target.value)}
                        style={{ ...inp, padding: '7px 8px', fontSize: 13, opacity: busyGroup === s.id ? 0.5 : 1 }}
                      >
                        {!s.group_id && <option value="">未分組</option>}
                        {groups.map((g) => {
                          const full = g.slot_limit != null && (g.slots_taken ?? 0) >= g.slot_limit
                          const isCur = g.id === s.group_id
                          const cap = g.slot_limit != null ? `${g.slots_taken ?? 0}/${g.slot_limit}` : `${g.slots_taken ?? 0}/∞`
                          return (
                            <option key={g.id} value={g.id} disabled={full && !isCur}>
                              {g.name}（{cap}）{full && !isCur ? ' 額滿' : ''}
                            </option>
                          )
                        })}
                      </select>
                    )}
                  {!s.group_revealed && <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 2 }}>對選手未公布</div>}
                </C>
                <C w={1}><span style={{ color: st.c }}>{st.t}</span></C>
                <C w={1}>{s.order_id ? `${ntd(s.order_total_cents)}` : '—'}</C>
                <C w={1}>
                  {s.status !== 'paid'
                    ? <button onClick={() => markPaid(s)} style={payBtn}>標記已付</button>
                    : <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>—</span>}
                </C>
              </Row>
            )
          })}
        </div>
      )}
    </div>
  )
}

function Row({ children, head }: { children: React.ReactNode; head?: boolean }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px', borderBottom: '1px solid var(--line)',
      background: head ? 'var(--bg-1)' : 'transparent',
      fontSize: head ? 11 : 14, letterSpacing: head ? '.08em' : undefined,
      color: head ? 'var(--tx-faint)' : 'var(--tx)', textTransform: head ? 'uppercase' : 'none',
    }}>{children}</div>
  )
}
function C({ children, w }: { children: React.ReactNode; w: number }) {
  return <div style={{ flex: w, minWidth: 0 }}>{children}</div>
}

const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '10px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
  borderRadius: 10, padding: '10px 16px', cursor: 'pointer', fontSize: 14,
}
const payBtn: React.CSSProperties = {
  background: 'var(--gold)', color: '#1a1200', fontWeight: 700, border: 'none',
  borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
}
