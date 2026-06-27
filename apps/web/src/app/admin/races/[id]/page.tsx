'use client'

import { useEffect, useState } from 'react'
import { useRouter, useParams } from 'next/navigation'
import Link from 'next/link'
import { adminRacesApi, type RaceDetail } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import RaceForm from '../RaceForm'

export default function AdminRaceEdit() {
  const router = useRouter()
  const params = useParams()
  const id = params.id as string

  const [token, setTokenState] = useState<string | null>(null)
  const [race, setRace] = useState<RaceDetail | null>(null)
  const [err, setErr] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    const t = getToken()
    if (!t) {
      router.replace('/admin/login')
      return
    }
    setTokenState(t)
    adminRacesApi
      .get(t, id)
      .then((res) => setRace(res.race))
      .catch((e) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else {
          setErr(e?.message || '載入失敗')
        }
      })
  }, [id, router])

  if (err && !race) return <Centered>{err}</Centered>
  if (!race || !token) return <Centered color="var(--tx-dim)">載入中…</Centered>

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <Link href="/admin/races" style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
        ← 返回賽事列表
      </Link>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, margin: '14px 0 22px' }}>
        <h1 style={{ fontSize: 24, fontWeight: 800, margin: 0 }}>編輯賽事</h1>
        {saved && <span style={{ color: 'var(--fug)', fontSize: 13 }}>✓ 已儲存</span>}
      </div>

      <RaceForm
        token={token}
        initial={race}
        submitLabel="儲存變更"
        onCancel={() => router.push('/admin/races')}
        onDone={(detail) => {
          setRace(detail)
          setSaved(true)
        }}
      />
    </div>
  )
}

function Centered({ children, color = 'var(--hunt)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '60px 0', color }}>{children}</div>
}
