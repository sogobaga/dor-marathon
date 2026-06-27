'use client'

import { useEffect, useState } from 'react'
import { useRouter, useParams } from 'next/navigation'
import Link from 'next/link'
import { adminMembersApi, type MemberDetail } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const GENDER_LABEL: Record<string, string> = { male: '男', female: '女', other: '其他' }

export default function AdminMemberDetailPage() {
  const router = useRouter()
  const params = useParams()
  const id = params.id as string

  const [m, setM] = useState<MemberDetail | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    const token = getToken()
    if (!token) {
      router.replace('/admin/login')
      return
    }
    adminMembersApi
      .get(token, id)
      .then((res) => setM(res.member))
      .catch((e) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else {
          setErr(e?.message || '載入失敗')
        }
      })
  }, [id, router])

  if (err) return <Centered>{err}</Centered>
  if (!m) return <Centered color="var(--tx-dim)">載入中…</Centered>

  return (
    <div style={{ maxWidth: 640, margin: '0 auto' }}>
      <Link href="/admin/members" style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
        ← 返回會員列表
      </Link>
      <h1 style={{ margin: '14px 0 6px', fontSize: 24, fontWeight: 800 }}>{m.name || m.handle}</h1>
      <div style={{ color: 'var(--tx-faint)', fontSize: 13, marginBottom: 22 }}>
        @{m.handle} · {m.role} · 已報名 {m.race_count} 場 · 累積 {m.total_km.toFixed(1)} K
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        <Info label="Email" value={m.email} />
        <Info label="真實名稱" value={m.real_name} />
        <Info label="暱稱" value={m.nickname} />
        <Info label="手機" value={m.phone} />
        <Info label="生日" value={m.birthday} />
        <Info label="性別" value={GENDER_LABEL[m.gender] || ''} />
        <div style={{ gridColumn: '1 / -1' }}>
          <Info label="地址" value={m.address} />
        </div>
      </div>
    </div>
  )
}

function Info({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 14 }}>
      <div style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 6 }}>
        {label}
      </div>
      <div style={{ fontSize: 15, color: value ? 'var(--tx)' : 'var(--tx-faint)' }}>{value || '未填'}</div>
    </div>
  )
}

function Centered({ children, color = 'var(--hunt)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '60px 0', color }}>{children}</div>
}
