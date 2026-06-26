'use client'

import { useEffect, useState } from 'react'
import { useRouter, useParams } from 'next/navigation'
import Link from 'next/link'
import { adminRacesApi, type Race } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const STATUSES = [
  { v: 'soon', t: '即將開始' },
  { v: 'open', t: '報名中' },
  { v: 'live', t: '進行中' },
  { v: 'done', t: '已結束' },
]

function toDateInput(iso: string) {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  return d.toISOString().slice(0, 10)
}

export default function AdminRaceEdit() {
  const router = useRouter()
  const params = useParams()
  const id = params.id as string

  const [race, setRace] = useState<Race | null>(null)
  const [distancesStr, setDistancesStr] = useState('')
  const [err, setErr] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    const token = getToken()
    if (!token) {
      router.replace('/admin/login')
      return
    }
    adminRacesApi
      .get(token, id)
      .then((res) => {
        setRace(res.race)
        setDistancesStr(res.race.distances.join(', '))
      })
      .catch((e) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else {
          setErr(e?.message || '載入失敗')
        }
      })
  }, [id, router])

  function set<K extends keyof Race>(key: K, value: Race[K]) {
    setRace((r) => (r ? { ...r, [key]: value } : r))
    setSaved(false)
  }

  async function save(e: React.FormEvent) {
    e.preventDefault()
    if (!race) return
    const token = getToken()
    if (!token) {
      router.replace('/admin/login')
      return
    }
    setErr('')
    setSaving(true)
    setSaved(false)
    try {
      const distances = distancesStr
        .split(',')
        .map((s) => parseInt(s.trim(), 10))
        .filter((n) => !isNaN(n))
      const payload: Race = {
        ...race,
        distances,
        start_date: race.start_date ? new Date(race.start_date).toISOString() : race.start_date,
        end_date: race.end_date ? new Date(race.end_date).toISOString() : race.end_date,
      }
      const res = await adminRacesApi.update(token, id, payload)
      setRace(res.race)
      setDistancesStr(res.race.distances.join(', '))
      setSaved(true)
    } catch (e: any) {
      setErr(e?.message || '儲存失敗')
    } finally {
      setSaving(false)
    }
  }

  if (err && !race) return <Centered>{err}</Centered>
  if (!race) return <Centered color="var(--tx-dim)">載入中…</Centered>

  return (
    <div style={{ maxWidth: 640, margin: '0 auto', padding: '32px 24px 60px' }}>
      <Link href="/admin/races" style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
        ← 返回賽事列表
      </Link>
      <h1 style={{ margin: '14px 0 22px', fontSize: 24, fontWeight: 800 }}>編輯賽事</h1>

      <form onSubmit={save} style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <Field label="賽事名稱">
          <input style={inp} value={race.title} onChange={(e) => set('title', e.target.value)} />
        </Field>
        <Field label="副標題">
          <input style={inp} value={race.subtitle} onChange={(e) => set('subtitle', e.target.value)} />
        </Field>
        <Field label="世界觀 (world)">
          <input style={inp} value={race.world} onChange={(e) => set('world', e.target.value)} />
        </Field>
        <Field label="說明 (blurb)">
          <textarea style={{ ...inp, minHeight: 70, resize: 'vertical' }} value={race.blurb} onChange={(e) => set('blurb', e.target.value)} />
        </Field>

        <Row>
          <Field label="狀態">
            <select style={inp} value={race.status} onChange={(e) => set('status', e.target.value as Race['status'])}>
              {STATUSES.map((s) => (
                <option key={s.v} value={s.v}>
                  {s.t}
                </option>
              ))}
            </select>
          </Field>
          <Field label="距離 (公里，逗號分隔)">
            <input style={inp} value={distancesStr} onChange={(e) => setDistancesStr(e.target.value)} placeholder="10, 21, 42" />
          </Field>
        </Row>

        <Row>
          <Field label="總名額">
            <input style={inp} type="number" value={race.slots_total} onChange={(e) => set('slots_total', parseInt(e.target.value || '0', 10))} />
          </Field>
          <Field label="報名費 (分 / cents)">
            <input style={inp} type="number" value={race.entry_fee} onChange={(e) => set('entry_fee', parseInt(e.target.value || '0', 10))} />
          </Field>
        </Row>

        <Row>
          <Field label="開始日期">
            <input style={inp} type="date" value={toDateInput(race.start_date)} onChange={(e) => set('start_date', e.target.value)} />
          </Field>
          <Field label="結束日期">
            <input style={inp} type="date" value={toDateInput(race.end_date)} onChange={(e) => set('end_date', e.target.value)} />
          </Field>
        </Row>

        {err && <div style={{ color: 'var(--hunt)', fontSize: 13 }}>{err}</div>}
        {saved && <div style={{ color: 'var(--fug)', fontSize: 13 }}>✓ 已儲存到資料庫</div>}

        <div style={{ display: 'flex', gap: 10, marginTop: 6 }}>
          <button type="submit" disabled={saving} style={primaryBtn}>
            {saving ? '儲存中…' : '儲存變更'}
          </button>
          <Link href="/admin/races" style={{ ...ghostBtn, textDecoration: 'none', display: 'inline-flex', alignItems: 'center' }}>
            取消
          </Link>
        </div>
      </form>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6, flex: 1 }}>
      <span style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
        {label}
      </span>
      {children}
    </label>
  )
}

function Row({ children }: { children: React.ReactNode }) {
  return <div style={{ display: 'flex', gap: 14 }}>{children}</div>
}

function Centered({ children, color = 'var(--hunt)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ display: 'grid', placeItems: 'center', minHeight: '100vh', color }}>{children}</div>
}

const inp: React.CSSProperties = {
  background: 'var(--bg-2)',
  border: '1px solid var(--line-2)',
  borderRadius: 10,
  padding: '10px 12px',
  color: 'var(--tx)',
  fontSize: 14,
  width: '100%',
  fontFamily: 'inherit',
}

const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)',
  color: '#05140e',
  fontWeight: 700,
  border: 'none',
  borderRadius: 10,
  padding: '11px 20px',
  cursor: 'pointer',
  fontSize: 14,
}

const ghostBtn: React.CSSProperties = {
  background: 'rgba(255,255,255,.05)',
  color: 'var(--tx)',
  border: '1px solid var(--line-2)',
  borderRadius: 10,
  padding: '11px 16px',
  cursor: 'pointer',
  fontSize: 14,
}
