'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminMembersApi, type MemberSummary } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const GENDER_LABEL: Record<string, string> = { male: '男', female: '女', other: '其他' }

export default function AdminMembersList() {
  const router = useRouter()
  const [members, setMembers] = useState<MemberSummary[] | null>(null)
  const [err, setErr] = useState('')
  const [q, setQ] = useState('')

  const load = useCallback(
    (query: string) => {
      const token = getToken()
      if (!token) {
        router.replace('/admin/login')
        return
      }
      setMembers(null)
      adminMembersApi
        .list(token, { q: query, limit: 100 })
        .then((res) => setMembers(res.members))
        .catch((e) => {
          if (e?.status === 401) {
            clearToken()
            router.replace('/admin/login')
          } else {
            setErr(e?.message || '載入失敗')
          }
        })
    },
    [router]
  )

  useEffect(() => {
    load('')
  }, [load])

  return (
    <div>
      <h1 style={{ margin: '0 0 18px', fontSize: 24, fontWeight: 800 }}>會員管理</h1>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          load(q)
        }}
        style={{ display: 'flex', gap: 10, marginBottom: 18 }}
      >
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="搜尋 Email / 姓名 / 真實名稱 / 手機"
          style={{
            flex: 1, maxWidth: 360, background: 'var(--bg-2)', border: '1px solid var(--line-2)',
            borderRadius: 10, padding: '10px 12px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit',
          }}
        />
        <button
          type="submit"
          style={{
            background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
            borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14,
          }}
        >
          搜尋
        </button>
      </form>

      {err && <div style={{ color: 'var(--hunt)', padding: 20 }}>{err}</div>}
      {!members && !err && <div style={{ color: 'var(--tx-dim)', padding: 20 }}>載入中…</div>}
      {members && members.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 20 }}>沒有符合的會員</div>}

      {members && members.length > 0 && (
        <div style={{ border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
          <div style={{ ...rowStyle, color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.08em', textTransform: 'uppercase', background: 'var(--bg-1)' }}>
            <div style={{ flex: 2 }}>會員</div>
            <div style={{ flex: 2 }}>Email</div>
            <div style={{ flex: 1 }}>真實名稱</div>
            <div style={{ flex: 1 }}>手機</div>
            <div style={{ width: 50 }}>性別</div>
            <div style={{ width: 70, textAlign: 'right' }}>里程</div>
          </div>
          {members.map((m) => (
            <Link key={m.id} href={`/admin/members/${m.id}`} style={{ ...rowStyle, textDecoration: 'none', color: 'inherit' }}>
              <div style={{ flex: 2 }}>
                <div style={{ fontWeight: 600 }}>{m.name || m.handle}</div>
                <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                  @{m.handle}{m.role !== 'user' ? ` · ${m.role}` : ''}
                </div>
              </div>
              <div style={{ flex: 2, color: 'var(--tx-dim)', fontSize: 13, wordBreak: 'break-all' }}>{m.email}</div>
              <div style={{ flex: 1, color: 'var(--tx-dim)', fontSize: 13 }}>{m.real_name || '—'}</div>
              <div style={{ flex: 1, color: 'var(--tx-dim)', fontSize: 13 }}>{m.phone || '—'}</div>
              <div style={{ width: 50, color: 'var(--tx-dim)', fontSize: 13 }}>{GENDER_LABEL[m.gender] || '—'}</div>
              <div style={{ width: 70, textAlign: 'right', fontSize: 13 }}>{m.total_km.toFixed(1)}K</div>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}

const rowStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 12,
  padding: '12px 16px',
  borderBottom: '1px solid var(--line)',
}
