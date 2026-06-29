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
  const [savingPerm, setSavingPerm] = useState(false)

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

  async function toggleTeamGroupPerm() {
    const token = getToken()
    if (!token || !m) return
    const next = !m.can_create_team_group
    setSavingPerm(true)
    setErr('')
    try {
      await adminMembersApi.setTeamGroupPermission(token, id, next)
      setM((prev) => (prev ? { ...prev, can_create_team_group: next } : prev))
    } catch (e: any) {
      setErr(e?.message || '更新失敗')
    } finally {
      setSavingPerm(false)
    }
  }

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

      {/* 權限設定 */}
      <h2 style={{ margin: '26px 0 10px', fontSize: 16, fontWeight: 800 }}>權限設定</h2>
      <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 16, display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 14, fontWeight: 600 }}>開放建立跑團分組</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
            開啟後，此會員可在有「開放跑團分組申請」的競賽中，於前台自建跑團分組。
          </div>
        </div>
        <button
          onClick={toggleTeamGroupPerm}
          disabled={savingPerm}
          style={{
            flexShrink: 0, borderRadius: 999, cursor: savingPerm ? 'default' : 'pointer',
            padding: '8px 16px', fontSize: 13, fontWeight: 700,
            background: m.can_create_team_group ? 'var(--fug)' : 'var(--bg-2)',
            color: m.can_create_team_group ? '#05140e' : 'var(--tx-dim)',
            border: m.can_create_team_group ? '1px solid var(--fug)' : '1px solid var(--line-2)',
            opacity: savingPerm ? 0.6 : 1,
          }}
        >
          {savingPerm ? '更新中…' : m.can_create_team_group ? '已開放 ✓' : '未開放'}
        </button>
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
