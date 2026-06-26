'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminRacesApi, type Race } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

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

  useEffect(() => {
    const token = getToken()
    if (!token) {
      router.replace('/admin/login')
      return
    }
    adminRacesApi
      .list(token)
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

  return (
    <div style={{ maxWidth: 880, margin: '0 auto', padding: '36px 24px 60px' }}>
      <header style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
        <div>
          <div style={{ fontSize: 11, letterSpacing: '.18em', color: 'var(--fug)', fontWeight: 600 }}>
            DOR · CONSOLE
          </div>
          <h1 style={{ margin: '6px 0 0', fontSize: 24, fontWeight: 800 }}>賽事管理</h1>
        </div>
        <button
          onClick={() => {
            clearToken()
            router.replace('/admin/login')
          }}
          style={ghostBtn}
        >
          登出
        </button>
      </header>

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

const ghostBtn: React.CSSProperties = {
  background: 'rgba(255,255,255,.05)',
  color: 'var(--tx)',
  border: '1px solid var(--line-2)',
  borderRadius: 10,
  padding: '8px 14px',
  cursor: 'pointer',
  fontSize: 13,
}
