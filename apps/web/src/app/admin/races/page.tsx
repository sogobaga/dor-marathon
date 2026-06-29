'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminRacesApi, type Race } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import NewRaceModal from './NewRaceModal'

const CONTROL_LABEL: Record<string, string> = {
  active: '正常運作', paused: '暫停報名', suspended: '賽事中止',
  closed: '賽事關閉', hidden: '賽事隱藏', testing: '測試中',
}
const DISPLAY_LABEL: Record<string, string> = {
  upcoming_reg: '即將報名', registering: '報名中', reg_closed: '報名結束',
  starting_soon: '賽事即將開始', racing: '賽事進行中', ended: '賽事結束',
  paused: '暫停報名', suspended: '賽事中止',
}
const MODE_LABEL: Record<string, string> = {
  general: '一般', competition: '競賽', faction_battle: '分組對抗',
}

function fmtDate(s?: string | null) {
  if (!s) return '—'
  const d = new Date(s)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function fmtFee(cents: number) {
  return cents > 0 ? `NT$${Math.round(cents / 100).toLocaleString()}` : '免費'
}

function InfoItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div style={{ minWidth: 0 }}>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginBottom: 1 }}>{label}</div>
      <div style={{ fontSize: 12, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{value}</div>
    </div>
  )
}

export default function AdminRacesList() {
  const router = useRouter()
  const [races, setRaces] = useState<Race[] | null>(null)
  const [err, setErr] = useState('')
  const [showNew, setShowNew] = useState(false)
  const [token, setTokenState] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  async function handleDelete(e: React.MouseEvent, r: Race) {
    e.preventDefault() // 阻止 Link 導航
    e.stopPropagation()
    if (!token) return
    if (!window.confirm(`確定要刪除賽事「${r.title}」？此動作無法復原。`)) return
    setErr('')
    setDeletingId(r.id)
    try {
      await adminRacesApi.remove(token, r.id)
      setRaces((rs) => (rs ? rs.filter((x) => x.id !== r.id) : rs))
    } catch (e: any) {
      setErr(e?.message || '刪除失敗')
    } finally {
      setDeletingId(null)
    }
  }

  const load = useCallback(() => {
    const t = getToken()
    if (!t) {
      router.replace('/admin/login')
      return
    }
    setTokenState(t)
    adminRacesApi
      .list(t)
      .then((res) => setRaces(res.races))
      .catch((e) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else {
          setErr(e?.message || '載入失敗')
        }
      })
  }, [router])

  useEffect(() => {
    load()
  }, [load])

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', margin: '0 0 20px' }}>
        <h1 style={{ fontSize: 24, fontWeight: 800, margin: 0 }}>賽事管理</h1>
        <button
          onClick={() => setShowNew(true)}
          style={{
            background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
            borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14,
          }}
        >
          ＋ 新增賽事
        </button>
      </div>

      {showNew && token && (
        <NewRaceModal
          token={token}
          onClose={() => setShowNew(false)}
          onCreated={() => {
            setShowNew(false)
            setRaces(null)
            load()
          }}
        />
      )}

      {err && <div style={{ color: 'var(--hunt)', padding: 20 }}>{err}</div>}
      {!races && !err && <div style={{ color: 'var(--tx-dim)', padding: 20 }}>載入中…</div>}

      {races && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {races.map((r) => (
            <Link
              key={r.id}
              href={`/admin/races/${r.id}`}
              style={{
                textDecoration: 'none',
                color: 'inherit',
                background: 'var(--bg-1)',
                border: '1px solid var(--line)',
                borderRadius: 14,
                padding: 18,
                display: 'flex',
                flexDirection: 'column',
                gap: 12,
              }}
            >
              <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 16, fontWeight: 700 }}>{r.title}</div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
                    {r.subtitle} · {r.distances.join('/')}K · {r.slots_total} 名額
                  </div>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexShrink: 0 }}>
                  <span style={{ color: 'var(--fug)', fontSize: 13 }}>編輯 →</span>
                  <button
                    onClick={(e) => handleDelete(e, r)}
                    disabled={deletingId === r.id}
                    title="刪除賽事"
                    style={{
                      background: 'rgba(255,80,80,.08)', color: 'var(--hunt)', border: '1px solid rgba(255,80,80,.25)',
                      borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
                    }}
                  >
                    {deletingId === r.id ? '刪除中…' : '刪除'}
                  </button>
                </div>
              </div>

              <div
                style={{
                  display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(130px, 1fr))', gap: 10,
                  borderTop: '1px solid var(--line)', paddingTop: 12,
                }}
              >
                <InfoItem label="賽事模式" value={MODE_LABEL[r.event_mode] ?? r.event_mode} />
                <InfoItem label="報名費" value={fmtFee(r.entry_fee)} />
                <InfoItem label="報名狀態" value={DISPLAY_LABEL[r.display_status] ?? r.display_status} />
                <InfoItem label="賽事狀態" value={CONTROL_LABEL[r.control_status] ?? r.control_status} />
                <InfoItem label="報名時間" value={`${fmtDate(r.registration_start)} ~ ${fmtDate(r.registration_end)}`} />
                <InfoItem label="賽事時間" value={`${fmtDate(r.start_date)} ~ ${fmtDate(r.end_date)}`} />
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
