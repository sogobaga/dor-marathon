'use client'

import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { profileApi, settingsApi, type DashboardInfo } from '@/lib/api'
import { useUser, getUserToken, withUserAuth } from '@/lib/userAuth'
import { LoginModal } from './UserAuthBar'
import DpCoin from './DpCoin'

// 賽事列表頂部會員資訊面板（與會員中心 Dashboard 同尺寸；未登入顯示 -- 與「註冊/登入」）
export default function MemberPanel({ onOpenProfile, onReady }: { onOpenProfile?: () => void; onReady?: () => void }) {
  const user = useUser()
  const [dash, setDash] = useState<DashboardInfo | null>(null)
  const [showLogin, setShowLogin] = useState(false)
  const [ready, setReady] = useState(false) // 面板資料就緒（登入者含儀表板）→ 供首頁決定何時顯示「開始跑步」
  const { data: settings } = useSWR('site-settings', () => settingsApi.get())
  const bgUrl = settings?.settings.member_panel_bg_url

  useEffect(() => {
    let cancelled = false
    if (user && getUserToken()) {
      withUserAuth((t) => profileApi.dashboard(t))
        .then((r) => { if (!cancelled) setDash(r.dashboard) })
        .catch(() => {})
        .finally(() => { if (!cancelled) setReady(true) }) // 儀表板到齊（成功/失敗都算就緒）
    } else {
      setDash(null)
      if (!getUserToken()) setReady(true) // 確定未登入 → 面板已定版
    }
    return () => { cancelled = true } // 登出後別讓還在飛的請求把舊資料寫回（頭像殘留）
  }, [user])

  // 安全網：最多等 2.5 秒，避免帳號資料異常時「開始跑步」永不出現
  useEffect(() => { const t = setTimeout(() => setReady(true), 2500); return () => clearTimeout(t) }, [])
  useEffect(() => { if (ready) onReady?.() }, [ready, onReady])

  const expPct =
    dash && dash.next_level_exp != null && dash.next_level_exp > dash.level_floor
      ? Math.max(0, Math.min(100, ((dash.exp - dash.level_floor) / (dash.next_level_exp - dash.level_floor)) * 100))
      : 100

  return (
    <>
      <div
        style={{
          ...card,
          ...(bgUrl
            ? { backgroundImage: `linear-gradient(rgba(10,13,12,.74),rgba(10,13,12,.84)), url(${bgUrl})`, backgroundSize: 'cover', backgroundPosition: 'center' }
            : {}),
          cursor: user && onOpenProfile ? 'pointer' : 'default',
        }}
        onClick={user ? onOpenProfile : undefined}
      >
        <div style={{ display: 'flex', gap: 14, alignItems: 'center' }}>
          <div style={avatarWrap}>
            {user && dash?.avatar_url
              // eslint-disable-next-line @next/next/no-img-element
              ? <img src={dash.avatar_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
              : <span style={{ fontSize: 22, fontWeight: 800, color: 'var(--tx-dim)' }}>{user ? (dash?.name || user.name || '?').slice(0, 1) : '？'}</span>}
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            {user ? (
              <>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{dash?.name || user.name}</span>
                  {dash?.is_vip && <span style={vipBadge}>VIP</span>}
                </div>
                {dash?.nickname && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{dash.nickname}</div>}
                <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontFamily: 'monospace', marginTop: 2 }}>#{dash?.account_code ?? '…'}</div>
              </>
            ) : (
              <button onClick={(e) => { e.stopPropagation(); setShowLogin(true) }} style={loginBtn}>註冊 / 登入</button>
            )}
          </div>
          {user && dash && (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, color: '#FFD24D', fontWeight: 900, fontSize: 14, fontVariantNumeric: 'tabular-nums', flexShrink: 0 }} title="DP 幣">
              <DpCoin size={16} />{(dash.dp ?? 0).toLocaleString()}
            </span>
          )}
        </div>

        {user && dash && (
          <div style={{ marginTop: 12 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', fontSize: 12 }}>
              <span style={{ fontWeight: 800, color: 'var(--fug)' }}>Lv.{dash.level}{dash.level_title ? ` ${dash.level_title}` : ''}</span>
              <span style={{ color: 'var(--tx-dim)' }}>{dash.exp} EXP</span>
            </div>
            <div style={barOuter}><div style={{ ...barInner, width: `${expPct}%` }} /></div>
          </div>
        )}

        {/* 賽事戰績 */}
        <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
          <Stat label="完成" value={user && dash ? `${dash.completed_count}` : '--'} />
          <Stat label="報名" value={user && dash ? `${dash.race_count}` : '--'} />
          <Stat label="進行中" value={user && dash ? `${dash.ongoing_count}` : '--'} />
          <Stat label="完成里程" value={user && dash ? `${dash.total_km.toFixed(1)}K` : '--'} />
        </div>
      </div>
      {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
    </>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ flex: 1, background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: '8px 4px', textAlign: 'center' }}>
      <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)' }}>{value}</div>
      <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 2 }}>{label}</div>
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-lg, 16px)', padding: 'var(--card-pad, 16px)', boxShadow: 'var(--card-shadow, none)' }
const avatarWrap: React.CSSProperties = {
  width: 64, height: 64, borderRadius: '50%', overflow: 'hidden', flexShrink: 0,
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center',
}
const vipBadge: React.CSSProperties = { fontSize: 10, fontWeight: 800, color: '#1a1200', background: 'var(--gold)', borderRadius: 6, padding: '1px 7px', letterSpacing: '.05em' }
const barOuter: React.CSSProperties = { height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 5 }
const barInner: React.CSSProperties = { height: '100%', background: 'var(--fug)', borderRadius: 999, transition: 'width .3s' }
const loginBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 18px', cursor: 'pointer', fontSize: 14 }
