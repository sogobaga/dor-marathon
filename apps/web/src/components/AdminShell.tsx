'use client'

import { useEffect, useState } from 'react'
import { useRouter, usePathname } from 'next/navigation'
import Link from 'next/link'
import { clearToken, getToken, getRefresh } from '@/lib/adminAuth'
import { adminMeApi, authApi, type AdminAccount } from '@/lib/api'
import VersionBadge from './VersionBadge'

type View = 'pc' | 'mobile'
const VIEW_KEY = 'dor_admin_view'

// 後台功能列表（左側導覽）。perm＝需要的模組權限（無 perm＝任何 admin 可見）；super＝僅超級管理員。
type NavItem = { k: string; t: string; href?: string; perm?: string; super?: boolean }
const NAV: { grp: string; items: NavItem[] }[] = [
  {
    grp: '營運',
    items: [
      { k: 'dash', t: '數據總覽', href: '/admin/overview' },
      { k: 'races', t: '賽事管理', href: '/admin/races', perm: 'races' },
      { k: 'members', t: '會員管理', href: '/admin/members', perm: 'members' },
      { k: 'signups', t: '報名管理', href: '/admin/signups', perm: 'signups' },
      { k: 'teams', t: '跑團管理' },
      { k: 'notifications', t: '推播通知' },
    ],
  },
  {
    grp: '遊戲設定',
    items: [
      { k: 'factions', t: '陣營設定' },
      { k: 'task-modules', t: '賽事任務', href: '/admin/task-modules', perm: 'tasks' },
      { k: 'levels', t: '等級設定', href: '/admin/levels', perm: 'settings' },
      { k: 'events', t: '事件任務', href: '/admin/events', perm: 'event_tasks' },
      { k: 'event-races', t: '多人事件', href: '/admin/event-races', perm: 'event_tasks' },
      { k: 'effects', t: '效果管理', href: '/admin/effects', perm: 'event_tasks' },
      { k: 'personal-tasks', t: '個人任務', href: '/admin/personal-tasks', perm: 'event_tasks' },
      { k: 'explore', t: '城市探索', href: '/admin/explore', perm: 'event_tasks' },
      { k: 'interstitial', t: '蓋板廣告', href: '/admin/interstitial', perm: 'settings' },
      { k: 'stores', t: '打卡門市' },
      { k: 'mileage', t: '里程規則' },
      { k: 'wheel', t: '轉盤獎勵' },
      { k: 'stickers', t: '集點卡' },
    ],
  },
  {
    grp: '系統管理',
    items: [
      { k: 'orders', t: '訂單管理', href: '/admin/orders', perm: 'orders' },
      { k: 'promo', t: '序號管理', href: '/admin/promo', perm: 'promo' },
      { k: 'vip-promos', t: '訂閱優惠管理', href: '/admin/vip-promos', perm: 'settings' },
      { k: 'gps-review', t: 'GPS 審核', href: '/admin/gps-review', perm: 'gps_review' },
      { k: 'checkin-review', t: '打卡審核', href: '/admin/checkin-review', perm: 'gps_review' },
      { k: 'whitelist', t: '測試白名單', href: '/admin/settings', perm: 'settings' },
      { k: 'system', t: '系統設定', href: '/admin/system', perm: 'settings' },
      { k: 'audit', t: '操作紀錄', href: '/admin/audit', super: true },
      { k: 'admins', t: '管理者', href: '/admin/admins', super: true },
      { k: 'i18n', t: '多語言' },
    ],
  },
]

// 依當前管理者權限決定某項目是否顯示。me=null（載入中）→ 只顯示無權限限制的項目。
function canSee(it: NavItem, me: AdminAccount | null): boolean {
  if (it.super) return !!me?.is_super
  if (it.perm) return !!me && (me.is_super || me.permissions.includes(it.perm))
  return true
}

export default function AdminShell({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const pathname = usePathname()
  const [view, setViewState] = useState<View>('pc')
  const [me, setMe] = useState<AdminAccount | null>(null)

  useEffect(() => {
    const saved = (typeof window !== 'undefined' && localStorage.getItem(VIEW_KEY)) as View | null
    if (saved === 'pc' || saved === 'mobile') setViewState(saved)
    else if (typeof window !== 'undefined' && window.matchMedia('(max-width: 760px)').matches) setViewState('mobile') // 手機未選過 → 預設行動版，避免側欄擠壓 topbar 把「登出」切掉
    const token = getToken()
    if (token) adminMeApi.get(token).then((r) => setMe(r.admin)).catch(() => {})
  }, [])

  function setView(v: View) {
    setViewState(v)
    localStorage.setItem(VIEW_KEY, v)
  }

  function logout() {
    // 先請伺服器把 refresh token 加入 denylist（撤銷），再清本機；不阻塞登出流程
    const at = getToken(), rt = getRefresh()
    if (at && rt) authApi.logout(at, rt).catch(() => {})
    clearToken()
    router.replace('/admin/login')
  }

  const topbar = (
    <header
      style={{
        minHeight: 56,
        flexShrink: 0,
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        // 讓開 iOS 瀏海/動態島（safe-area-top）＋橫向右側 inset，避免頂列「登出」被切到
        padding: 'env(safe-area-inset-top, 0px) max(18px, env(safe-area-inset-right, 0px)) 0 max(18px, env(safe-area-inset-left, 0px))',
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
              color: 'var(--fug-ink)',
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
          {NAV.map((g) => {
            const items = g.items.filter((it) => canSee(it, me))
            if (items.length === 0) return null
            return (
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
              {items.map((it) => {
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
            )
          })}
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
        {/* 版號（置底置中）：同時顯示前台 + 後端 API 版號 */}
        <VersionBadge showApi />
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
        color: view === v ? 'var(--fug-ink)' : 'var(--tx-dim)',
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
