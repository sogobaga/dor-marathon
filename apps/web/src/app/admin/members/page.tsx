'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminMembersApi, type MemberSummary, type SignupSource } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import { signupSourceText, SIGNUP_SOURCE_OPTIONS } from '@/lib/signupSource'

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

function fmtLastLogin(iso?: string) {
  const d = iso ? new Date(iso) : null
  if (!d || isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function AdminMembersList() {
  const router = useRouter()
  const [members, setMembers] = useState<MemberSummary[] | null>(null)
  const [err, setErr] = useState('')
  const [q, setQ] = useState('')
  const [source, setSource] = useState<SignupSource | ''>('')
  const [hideVirtual, setHideVirtual] = useState(false)

  const load = useCallback(
    (query: string, src: SignupSource | '', hideVirtualArg: boolean) => {
      const token = getToken()
      if (!token) {
        router.replace('/admin/login')
        return
      }
      setMembers(null)
      adminMembersApi
        .list(token, { q: query, limit: 100, source: src || undefined, hideVirtual: hideVirtualArg })
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
    load('', '', false)
  }, [load])

  return (
    <div>
      <h1 style={{ margin: '0 0 18px', fontSize: 24, fontWeight: 800 }}>會員管理</h1>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          load(q, source, hideVirtual)
        }}
        style={{ display: 'flex', gap: 10, marginBottom: 18, flexWrap: 'wrap', alignItems: 'center' }}
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
        <select
          value={source}
          onChange={(e) => {
            const next = e.target.value as SignupSource | ''
            setSource(next)
            load(q, next, hideVirtual)
          }}
          style={{
            background: 'var(--bg-2)', border: '1px solid var(--line-2)',
            borderRadius: 10, padding: '10px 12px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit',
          }}
        >
          <option value="">來源：全部</option>
          {SIGNUP_SOURCE_OPTIONS.map((opt) => (
            <option key={opt.value} value={opt.value}>{opt.label}</option>
          ))}
        </select>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--tx-dim)', cursor: 'pointer' }}>
          <input
            type="checkbox"
            checked={hideVirtual}
            onChange={(e) => {
              const next = e.target.checked
              setHideVirtual(next)
              load(q, source, next)
            }}
          />
          隱藏虛擬選手
        </label>
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
          <div style={{ overflowX: 'auto' }}>
            <div style={{ minWidth: MEMBERS_TABLE_MIN_WIDTH }}>
              <div style={{ ...rowStyle, color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.08em', textTransform: 'uppercase', background: 'var(--bg-1)' }}>
                <div>會員</div>
                <div>Email</div>
                <div>真實名稱</div>
                <div>手機</div>
                <div>性別</div>
                <div style={{ textAlign: 'right' }}>里程</div>
                <div>身分</div>
                <div>來源</div>
                <div>VIP到期(剩餘)</div>
                <div>上次登入</div>
              </div>
              {members.map((m) => {
                const days = m.is_vip ? vipDaysLeft(m.vip_expires_at) : null
                const sourceText = signupSourceText(m.signup_source, m.signup_ref_name, m.signup_utm_source)
                const roleSuffix = m.role !== 'user' ? ` · ${m.role}` : ''
                return (
                  <Link key={m.id} href={`/admin/members/${m.id}`} style={{ ...rowStyle, textDecoration: 'none', color: 'inherit' }}>
                    <div style={{ overflow: 'hidden' }}>
                      <div style={{ fontWeight: 600, display: 'flex', alignItems: 'center', gap: 4 }}>
                        <span style={cellEllipsis} title={m.name || m.handle}>{m.name || m.handle}</span>
                        {m.is_virtual && <span title="虛擬選手">🤖</span>}
                      </div>
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)', ...cellEllipsis }} title={`@${m.handle}${roleSuffix}`}>
                        @{m.handle}{roleSuffix}
                      </div>
                    </div>
                    <div style={{ color: 'var(--tx-dim)', fontSize: 13, ...cellEllipsis }} title={m.email}>{m.email}</div>
                    <div style={{ color: 'var(--tx-dim)', fontSize: 13, ...cellEllipsis }} title={m.real_name || undefined}>{m.real_name || '—'}</div>
                    <div style={{ color: 'var(--tx-dim)', fontSize: 13, ...cellEllipsis }}>{m.phone || '—'}</div>
                    <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>{GENDER_LABEL[m.gender] || '—'}</div>
                    <div style={{ textAlign: 'right', fontSize: 13 }}>{m.total_km.toFixed(1)}K</div>
                    <div>
                      <span style={{
                        fontSize: 11, fontWeight: 800, borderRadius: 999, padding: '2px 9px', display: 'inline-block',
                        ...(m.is_vip
                          ? { background: 'rgba(255,194,75,.14)', border: '1px solid var(--gold)', color: 'var(--gold)' }
                          : { background: 'var(--bg-2)', border: '1px solid var(--line)', color: 'var(--tx-faint)' }),
                      }}>{m.is_vip ? 'VIP' : '一般'}</span>
                    </div>
                    <div style={{ fontSize: 12, color: m.signup_source ? 'var(--tx-dim)' : 'var(--tx-faint)', ...cellEllipsis }} title={sourceText}>
                      {sourceText}
                    </div>
                    <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
                      {m.is_vip && m.vip_expires_at ? (
                        <>
                          <div>{fmtVipExpiry(m.vip_expires_at)}</div>
                          <div style={{ fontSize: 11, color: days !== null && days <= 3 ? 'var(--hunt)' : 'var(--tx-faint)' }}>
                            {days !== null ? `剩 ${days} 天` : ''}
                          </div>
                        </>
                      ) : '—'}
                    </div>
                    <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{fmtLastLogin(m.last_login_at)}</div>
                  </Link>
                )
              })}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

// 表頭與資料列共用同一組欄寬模板，確保逐欄對齊；順序＝會員/Email/真實名稱/手機/性別/里程/身分/來源/VIP到期(剩餘)/上次登入
const MEMBERS_GRID_COLUMNS =
  'minmax(170px,1.6fr) minmax(190px,1.4fr) 100px 110px 50px 70px 60px 110px 130px 110px'
const MEMBERS_TABLE_MIN_WIDTH = 1100

const rowStyle: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: MEMBERS_GRID_COLUMNS,
  alignItems: 'center',
  gap: 12,
  padding: '12px 16px',
  borderBottom: '1px solid var(--line)',
}

const cellEllipsis: React.CSSProperties = {
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
}
