'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminMembersApi, type MemberSummary } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const GENDER_LABEL: Record<string, string> = { male: '男', female: '女', other: '其他' }

function vipDaysLeft(iso?: string): number | null {
  if (!iso) return null
  const exp = new Date(iso)
  if (isNaN(exp.getTime())) return null
  const now = new Date()
  const msPerDay = 24 * 60 * 60 * 1000
  return Math.ceil((exp.getTime() - now.getTime()) / msPerDay)
}

function fmtVipExpiry(iso?: string) {
  const d = iso ? new Date(iso) : null
  if (!d || isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())}`
}

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
            background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
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
            <div style={{ width: 54 }}>身分</div>
            <div style={{ width: 130 }}>VIP到期(剩餘)</div>
          </div>
          {members.map((m) => {
            const days = m.is_vip ? vipDaysLeft(m.vip_expires_at) : null
            return (
              <Link key={m.id} href={`/admin/members/${m.id}`} style={{ ...rowStyle, textDecoration: 'none', color: 'inherit', flexWrap: 'wrap' }}>
                <div style={{ flex: 2, minWidth: 140 }}>
                  <div style={{ fontWeight: 600 }}>{m.name || m.handle}</div>
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                    @{m.handle}{m.role !== 'user' ? ` · ${m.role}` : ''}
                  </div>
                </div>
                <div style={{ flex: 2, minWidth: 160, color: 'var(--tx-dim)', fontSize: 13, wordBreak: 'break-all' }}>{m.email}</div>
                <div style={{ flex: 1, minWidth: 70, color: 'var(--tx-dim)', fontSize: 13 }}>{m.real_name || '—'}</div>
                <div style={{ flex: 1, minWidth: 70, color: 'var(--tx-dim)', fontSize: 13 }}>{m.phone || '—'}</div>
                <div style={{ width: 50, color: 'var(--tx-dim)', fontSize: 13 }}>{GENDER_LABEL[m.gender] || '—'}</div>
                <div style={{ width: 70, textAlign: 'right', fontSize: 13 }}>{m.total_km.toFixed(1)}K</div>
                <div style={{ width: 54 }}>
                  <span style={{
                    fontSize: 11, fontWeight: 800, borderRadius: 999, padding: '2px 9px', display: 'inline-block',
                    ...(m.is_vip
                      ? { background: 'rgba(255,194,75,.14)', border: '1px solid var(--gold)', color: 'var(--gold)' }
                      : { background: 'var(--bg-2)', border: '1px solid var(--line)', color: 'var(--tx-faint)' }),
                  }}>{m.is_vip ? 'VIP' : '一般'}</span>
                </div>
                <div style={{ width: 130, fontSize: 12, color: 'var(--tx-dim)' }}>
                  {m.is_vip && m.vip_expires_at ? (
                    <>
                      <div>{fmtVipExpiry(m.vip_expires_at)}</div>
                      <div style={{ fontSize: 11, color: days !== null && days <= 3 ? 'var(--hunt)' : 'var(--tx-faint)' }}>
                        {days !== null ? `剩 ${days} 天` : ''}
                      </div>
                    </>
                  ) : '—'}
                </div>
              </Link>
            )
          })}
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
