'use client'

// 個人挑戰模式（event_mode=personal）P5：後台獎勵管理。
// 獎勵＝「完成者中抽獎/限額」，LINE Point 由後台人工發放；系統只管「資格＋發放狀態」。
// 以「每一筆完成」為單位（同一人完成多次＝多筆完成＝多個抽獎資格）。入口：admin/races 列表頁「🎁 獎勵」連結。

import { useCallback, useEffect, useState } from 'react'
import { useRouter, useParams } from 'next/navigation'
import Link from 'next/link'
import { adminRacesApi, adminRewardsApi, type RewardCompletionRow, type RewardCompletionsResponse } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const PAGE = 50
const STATUS_FILTERS: { k: string; t: string }[] = [
  { k: 'all', t: '全部' },
  { k: '', t: '待處理' },
  { k: 'won', t: '中獎待發' },
  { k: 'fulfilled', t: '已發放' },
]

function fmt(iso: string) {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function AdminRaceRewardsPage() {
  const router = useRouter()
  const params = useParams()
  const raceId = params.id as string

  const [token, setTokenState] = useState<string | null>(null)
  const [raceTitle, setRaceTitle] = useState('')
  const [data, setData] = useState<RewardCompletionsResponse | null>(null)
  const [filter, setFilter] = useState('all')
  const [offset, setOffset] = useState(0)
  const [err, setErr] = useState('')
  const [drawN, setDrawN] = useState(1)
  const [drawing, setDrawing] = useState(false)

  const load = useCallback((t: string, off: number, f: string) => {
    adminRewardsApi
      .list(t, raceId, { reward_status: f, limit: PAGE, offset: off })
      .then((res) => setData(res))
      .catch((e: any) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else if (e?.status === 403) {
          setErr('此頁僅具「賽事管理」權限的管理者可存取')
        } else if (e?.status === 404) {
          setErr('找不到此賽事，或此賽事非個人挑戰模式')
        } else {
          setErr(e?.message || '載入失敗')
        }
      })
  }, [raceId, router])

  useEffect(() => {
    const t = getToken()
    if (!t) {
      router.replace('/admin/login')
      return
    }
    setTokenState(t)
    load(t, 0, 'all')
    adminRacesApi.get(t, raceId).then((res) => setRaceTitle(res.race.title)).catch(() => {})
  }, [raceId, router, load])

  function changeFilter(f: string) {
    setFilter(f)
    setOffset(0)
    if (token) load(token, 0, f)
  }
  function page(off: number) {
    setOffset(off)
    if (token) load(token, off, filter)
  }

  async function handleDraw() {
    if (!token || drawN <= 0) return
    if (!window.confirm(`確定要從「待處理」完成者中隨機抽出 ${drawN} 位中獎？此動作無法復原。`)) return
    setDrawing(true)
    setErr('')
    try {
      const res = await adminRewardsApi.draw(token, raceId, drawN)
      window.alert(`已抽出 ${res.count} 位中獎者。`)
      load(token, offset, filter)
    } catch (e: any) {
      setErr(e?.message || '抽獎失敗')
    } finally {
      setDrawing(false)
    }
  }

  async function handleRowSave(regId: string, status: string, note: string) {
    if (!token) return
    await adminRewardsApi.update(token, regId, { reward_status: status as '' | 'won' | 'fulfilled', reward_note: note })
    load(token, offset, filter)
  }

  const summary = data?.summary

  return (
    <div style={{ maxWidth: 1040, margin: '0 auto' }}>
      <Link href={`/admin/races/${raceId}`} style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
        ← 返回賽事編輯
      </Link>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '14px 0 4px' }}>獎勵管理{raceTitle ? `：${raceTitle}` : ''}</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>
        個人挑戰模式完成者名單。每一筆「完成」皆為獨立抽獎資格；LINE Point 由後台人工發放，此頁僅記錄資格與發放狀態。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}

      {summary && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 10, margin: '14px 0' }}>
          <SummaryTile label="完成總筆數" value={summary.total} c="var(--tx)" />
          <SummaryTile label="待處理" value={summary.pending} c="var(--tx-dim)" />
          <SummaryTile label="中獎待發" value={summary.won} c="var(--gold)" />
          <SummaryTile label="已發放" value={summary.fulfilled} c="var(--fug)" />
        </div>
      )}

      <div
        style={{
          display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', margin: '14px 0',
          background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 14,
        }}
      >
        <span style={{ fontSize: 14 }}>🎲 隨機抽</span>
        <input
          type="number"
          min={1}
          value={drawN}
          onChange={(e) => setDrawN(Math.max(1, parseInt(e.target.value, 10) || 1))}
          style={{ ...inp, width: 80 }}
        />
        <span style={{ fontSize: 14 }}>位（僅從「待處理」抽取，抽中即設為中獎待發）</span>
        <button onClick={handleDraw} disabled={drawing} style={{ ...drawBtn, opacity: drawing ? 0.6 : 1 }}>
          {drawing ? '抽獎中…' : '開始抽獎'}
        </button>
      </div>

      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', margin: '10px 0 14px' }}>
        {STATUS_FILTERS.map((f) => (
          <button
            key={f.k}
            onClick={() => changeFilter(f.k)}
            style={{ ...chip, ...(filter === f.k ? chipOn : {}) }}
          >
            {f.t}
          </button>
        ))}
      </div>

      <div style={{ border: '1px solid var(--line)', borderRadius: 12, overflow: 'hidden' }}>
        <div style={{ ...rowStyle, ...headRow }}>
          <span style={{ flex: '0 0 120px' }}>完成時間</span>
          <span style={{ flex: '0 0 50px', textAlign: 'center' }}>次數</span>
          <span style={{ flex: '0 0 130px' }}>姓名</span>
          <span style={{ flex: '0 0 190px' }}>Email</span>
          <span style={{ flex: '0 0 110px' }}>狀態</span>
          <span style={{ flex: 1, minWidth: 0 }}>備註</span>
          <span style={{ flex: '0 0 60px' }} />
        </div>
        {!data && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>載入中…</div>}
        {data && data.completions.length === 0 && (
          <div style={{ padding: 16, color: 'var(--tx-dim)' }}>目前沒有符合條件的完成紀錄</div>
        )}
        {data?.completions.map((c) => (
          <RewardRow key={c.registration_id} row={c} onSave={handleRowSave} />
        ))}
      </div>

      {data && (
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 12, fontSize: 13, color: 'var(--tx-dim)' }}>
          <span>共 {data.count} 筆 · 第 {Math.floor(offset / PAGE) + 1} / {Math.max(1, Math.ceil(data.count / PAGE))} 頁</span>
          <div style={{ display: 'flex', gap: 8 }}>
            <button disabled={offset <= 0} onClick={() => page(Math.max(0, offset - PAGE))} style={{ ...pgBtn, opacity: offset <= 0 ? 0.4 : 1 }}>
              上一頁
            </button>
            <button disabled={offset + PAGE >= data.count} onClick={() => page(offset + PAGE)} style={{ ...pgBtn, opacity: offset + PAGE >= data.count ? 0.4 : 1 }}>
              下一頁
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function SummaryTile({ label, value, c }: { label: string; value: number; c: string }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 14px' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</div>
      <div style={{ fontSize: 20, fontWeight: 800, color: c }}>{value}</div>
    </div>
  )
}

function RewardRow({
  row,
  onSave,
}: {
  row: RewardCompletionRow
  onSave: (regId: string, status: string, note: string) => Promise<void>
}) {
  const [status, setStatus] = useState(row.reward_status)
  const [note, setNote] = useState(row.reward_note || '')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  async function handleSave() {
    setSaving(true)
    setSaved(false)
    try {
      await onSave(row.registration_id, status, note)
      setSaved(true)
    } catch {
      // 錯誤交給父層 err 狀態顯示；此處只需結束 loading
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={rowStyle}>
      <span style={{ flex: '0 0 120px', color: 'var(--tx-dim)', fontSize: 12 }}>{fmt(row.completed_at)}</span>
      <span style={{ flex: '0 0 50px', textAlign: 'center' }}>#{row.attempt_no}</span>
      <span style={{ flex: '0 0 130px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.user_name}</span>
      <span style={{ flex: '0 0 190px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--tx-dim)', fontSize: 12 }}>
        {row.user_email}
      </span>
      <span style={{ flex: '0 0 110px' }}>
        <select value={status} onChange={(e) => { setStatus(e.target.value as '' | 'won' | 'fulfilled'); setSaved(false) }} style={sel}>
          <option value="">待處理</option>
          <option value="won">中獎待發</option>
          <option value="fulfilled">已發放</option>
        </select>
      </span>
      <span style={{ flex: 1, minWidth: 0 }}>
        <input
          value={note}
          onChange={(e) => { setNote(e.target.value); setSaved(false) }}
          placeholder="LINE Point 序號/備註"
          style={{ ...inp, padding: '5px 8px', fontSize: 12, width: '100%' }}
        />
      </span>
      <span style={{ flex: '0 0 60px', textAlign: 'right' }}>
        <button onClick={handleSave} disabled={saving} style={saveBtn}>
          {saving ? '…' : saved ? '✓' : '儲存'}
        </button>
      </span>
    </div>
  )
}

const rowStyle: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 12, padding: '10px 14px', borderBottom: '1px solid var(--line)', fontSize: 13 }
const headRow: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.05em', fontWeight: 700 }
const chip: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '5px 12px', fontSize: 12.5, cursor: 'pointer', color: 'var(--tx-dim)' }
const chipOn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: '1px solid var(--fug)' }
const pgBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 14px', fontSize: 13, cursor: 'pointer', color: 'var(--tx)' }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px 12px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit' }
const sel: React.CSSProperties = { ...inp, padding: '5px 6px', fontSize: 12 }
const saveBtn: React.CSSProperties = { background: 'var(--gold)', color: '#fff', fontWeight: 700, border: 'none', borderRadius: 8, padding: '5px 10px', cursor: 'pointer', fontSize: 12 }
const drawBtn: React.CSSProperties = { background: 'var(--gold)', color: '#fff', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 18px', cursor: 'pointer', fontSize: 14 }
