'use client'

import { useEffect, useState } from 'react'
import { useRouter, usePathname } from 'next/navigation'
import Link from 'next/link'
import { clearToken } from '@/lib/adminAuth'

type View = 'pc' | 'mobile'
const VIEW_KEY = 'dor_admin_view'

// 後台功能列表（左側導覽）。目前僅「賽事管理」已實作，其餘為規劃中項目（淡色、暫不可點）。
const NAV: { grp: string; items: { k: string; t: string; href?: string }[] }[] = [
  {
    grp: '營運',
    items: [
      { k: 'dash', t: '數據總覽' },
      { k: 'races', t: '賽事管理', href: '/admin/races' },
      { k: 'members', t: '會員管理', href: '/admin/members' },
      { k: 'signups', t: '報名管理', href: '/admin/signups' },
      { k: 'teams', t: '跑團管理' },
      { k: 'notifications', t: '推播通知' },
    ],
  },
  {
    grp: '遊戲設定',
    items: [
      { k: 'factions', t: '陣營設定' },
      { k: 'missions', t: '每日任務' },
      { k: 'stores', t: '打卡門市' },
      { k: 'mileage', t: '里程規則' },
      { k: 'wheel', t: '轉盤獎勵' },
      { k: 'stickers', t: '集點卡' },
    ],
  },
  {
    grp: '系統管理',
    items: [
      { k: 'orders', t: '訂單管理', href: '/admin/orders' },
      { k: 'audit', t: '操作紀錄' },
      { k: 'admins', t: '管理員' },
      { k: 'permissions', t: '權限' },
      { k: 'i18n', t: '多語言' },
    ],
  },
]

export default function AdminShell({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const pathname = usePathname()
  const [view, setViewState] = useState<View>('pc')

  useEffect(() => {
    const saved = (typeof window !== 'undefined' && localStorage.getItem(VIEW_KEY)) as View | null
    if (saved === 'pc' || saved === 'mobile') setViewState(saved)
  }, [])

  function setView(v: View) {
    setViewState(v)
    localStorage.setItem(VIEW_KEY, v)
  }

  function logout() {
    clearToken()
    router.replace('/admin/login')
  }

  const topbar = (
    <header
      style={{
        height: 56,
        flexShrink: 0,
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '0 18px',
        borderBottom: '1px solid var(--line)',
        background: 'var(--bg-1)',
      }}
    >
      {view === 'mobile' && (
        <div style={{ fontSize: 11, letterSpacing: '.18em', color: 'var(--fug)', fontWeight: 600, flex: 1 }}>
          DOR · CONSOLE
        </div>
      )}
      <div style={{ flex: view === 'mobile' ? '0 0 auto' : 1 }} />
      <ViewToggle view={view} onChange={setView} />
      <button onClick={logout} style={ghostBtn}>
        登出
      </button>
    </header>
  )

  // ── 手機版：無側欄，窄欄置中 ──
  if (view === 'mobile') {
    return (
      <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--bg)', color: 'var(--tx)' }}>
        {topbar}
        <div style={{ flex: 1, overflowY: 'auto' }}>
          <div style={{ maxWidth: 600, margin: '0 auto', padding: '20px 16px 48px' }}>{children}</div>
        </div>
      </div>
    )
  }

  // ── 網頁版（PC，預設）：左側功能列表 + 主內容 ──
  return (
    <div style={{ height: '100vh', display: 'flex', background: 'var(--bg)', color: 'var(--tx)' }}>
      <aside
        style={{
          width: 232,
          flexShrink: 0,
          background: 'var(--bg-0, #0d0f14)',
          borderRight: '1px solid var(--line)',
          display: 'flex',
          flexDirection: 'column',
          padding: '16px 12px',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 8px 16px' }}>
          <div
            style={{
              width: 30,
              height: 30,
              borderRadius: 9,
              background: 'var(--fug)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: '#05140e',
              fontWeight: 800,
              fontSize: 16,
            }}
          >
            ⚡
          </div>
          <div>
            <div style={{ fontFamily: 'var(--font-disp)', fontWeight: 700, fontSize: 15 }}>DOR</div>
            <div style={{ fontSize: 8, letterSpacing: '.18em', color: 'var(--tx-faint)' }}>CONSOLE</div>
          </div>
        </div>

        <nav style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 2 }}>
          {NAV.map((g) => (
            <div key={g.grp}>
              <div
                style={{
                  fontSize: 9.5,
                  letterSpacing: '.18em',
                  color: 'var(--tx-faint)',
                  textTransform: 'uppercase',
                  padding: '14px 10px 6px',
                }}
              >
                {g.grp}
              </div>
              {g.items.map((it) => {
                const active = !!it.href && pathname.startsWith(it.href)
                const base: React.CSSProperties = {
                  display: 'flex',
                  alignItems: 'center',
                  gap: 10,
                  padding: '9px 11px',
                  borderRadius: 10,
                  fontSize: 14,
                  textDecoration: 'none',
                }
                if (!it.href) {
                  return (
                    <div
                      key={it.k}
                      title="建置中"
                      style={{ ...base, color: 'var(--tx-faint)', cursor: 'default' }}
                    >
                      {it.t}
                      <span style={{ marginLeft: 'auto', fontSize: 9, opacity: 0.6 }}>soon</span>
                    </div>
                  )
                }
                return (
                  <Link
                    key={it.k}
                    href={it.href}
                    style={{
                      ...base,
                      color: active ? 'var(--fug)' : 'var(--tx-dim)',
                      background: active ? 'rgba(45,229,154,.1)' : 'transparent',
                      fontWeight: active ? 600 : 400,
                    }}
                  >
                    {it.t}
                  </Link>
                )
              })}
            </div>
          ))}
        </nav>

        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            padding: 10,
            marginTop: 8,
            borderTop: '1px solid var(--line)',
          }}
        >
          <div
            style={{
              width: 30,
              height: 30,
              borderRadius: 8,
              background: 'var(--bg-2)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: 12,
              color: 'var(--tx-dim)',
            }}
          >
            OP
          </div>
          <div style={{ fontSize: 12.5 }}>
            營運後台
            <div style={{ fontSize: 8, marginTop: 1, color: 'var(--tx-faint)' }}>admin</div>
          </div>
        </div>
      </aside>

      <main style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        {topbar}
        <div style={{ flex: 1, overflowY: 'auto', padding: 28 }}>
          <div style={{ maxWidth: 1000, margin: '0 auto' }}>{children}</div>
        </div>
      </main>
    </div>
  )
}

function ViewToggle({ view, onChange }: { view: View; onChange: (v: View) => void }) {
  const seg = (v: View, label: string) => (
    <button
      onClick={() => onChange(v)}
      style={{
        border: 'none',
        cursor: 'pointer',
        fontSize: 12,
        padding: '6px 12px',
        borderRadius: 8,
        background: view === v ? 'var(--fug)' : 'transparent',
        color: view === v ? '#05140e' : 'var(--tx-dim)',
        fontWeight: view === v ? 700 : 400,
      }}
    >
      {label}
    </button>
  )
  return (
    <div
      style={{
        display: 'inline-flex',
        gap: 2,
        padding: 3,
        borderRadius: 10,
        background: 'var(--bg-2)',
        border: '1px solid var(--line-2)',
      }}
    >
      {seg('pc', '網頁版')}
      {seg('mobile', '手機版')}
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
