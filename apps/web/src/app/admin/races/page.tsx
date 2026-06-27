'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminRacesApi, type Race } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import NewRaceModal from './NewRaceModal'

const STATUS_LABEL: Record<string, string> = {
  live: '進行中',
  open: '報名中',
  soon: '即將開始',
  done: '已結束',
}

export default function AdminRacesList() {
  const router = useRouter()
  const [races, setRaces] = useState<Race[] | null>(null)
  const [err, setErr] = useState('')
  const [showNew, setShowNew] = useState(false)
  const [token, setTokenState] = useState<string | null>(null)

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
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: 12,
              }}
            >
              <div>
                <div style={{ fontSize: 16, fontWeight: 700 }}>{r.title}</div>
                <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
                  {r.subtitle} · {r.distances.join('/')}K · {r.slots_total} 名額
                </div>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{STATUS_LABEL[r.status] ?? r.status}</span>
                <span style={{ color: 'var(--fug)', fontSize: 13 }}>編輯 →</span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
