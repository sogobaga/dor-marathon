'use client'

import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { profileApi, settingsApi, type DashboardInfo } from '@/lib/api'
import { useUser, getUserToken, withUserAuth } from '@/lib/userAuth'
import { LoginModal } from './UserAuthBar'
import DpCoin from './DpCoin'

// 會員資訊面板（首頁與「會員資訊頁」共用，內容一致）。
// - 未帶 dash：自行抓取（首頁用法），並在資料就緒時呼叫 onReady。
// - 有帶 dash：受控（會員資訊頁用法，用該頁既有的 dashboard 資料）。
// - onOpenProfile：整張卡可點 → 開會員資訊頁（首頁）。
// - onUploadAvatar：頭像變成可上傳（會員資訊頁）。
export default function MemberPanel({
  dash: dashProp,
  onOpenProfile,
  onUploadAvatar,
  uploadingAvatar,
  onReady,
}: {
  dash?: DashboardInfo | null
  onOpenProfile?: () => void
  onUploadAvatar?: (file: File) => void
  uploadingAvatar?: boolean
  onReady?: () => void
}) {
  const controlled = dashProp !== undefined // 有傳 dash（含 null）＝受控；未傳＝自行抓取
  const user = useUser()
  const [selfDash, setSelfDash] = useState<DashboardInfo | null>(null)
  const [ready, setReady] = useState(false)
  const [showLogin, setShowLogin] = useState(false)
  const [copied, setCopied] = useState(false)
  const { data: settings } = useSWR('site-settings', () => settingsApi.get())
  const bgUrl = settings?.settings.member_panel_bg_url
  const dash = controlled ? dashProp ?? null : selfDash

  useEffect(() => {
    if (controlled) return
    let cancelled = false
    if (user && getUserToken()) {
      withUserAuth((t) => profileApi.dashboard(t))
        .then((r) => { if (!cancelled) setSelfDash(r.dashboard) })
        .catch(() => {})
        .finally(() => { if (!cancelled) setReady(true) }) // 儀表板到齊（成功/失敗都算就緒）
    } else {
      setSelfDash(null)
      if (!getUserToken()) setReady(true) // 確定未登入 → 面板已定版
    }
    return () => { cancelled = true } // 登出後別讓還在飛的請求把舊資料寫回（頭像殘留）
  }, [user, controlled])
  // 安全網：最多等 2.5 秒，避免帳號資料異常時 onReady 永不觸發
  useEffect(() => { if (controlled) return; const t = setTimeout(() => setReady(true), 2500); return () => clearTimeout(t) }, [controlled])
  useEffect(() => { if (ready) onReady?.() }, [ready, onReady])

  const expPct =
    dash && dash.next_level_exp != null && dash.next_level_exp > dash.level_floor
      ? Math.max(0, Math.min(100, ((dash.exp - dash.level_floor) / (dash.next_level_exp - dash.level_floor)) * 100))
      : 100

  function copyCode(e: React.MouseEvent) {
    e.stopPropagation()
    if (!dash?.account_code) return
    navigator.clipboard?.writeText(dash.account_code).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500) }).catch(() => {})
  }

  const clickable = !!user && !!onOpenProfile

  return (
    <>
      <div
        style={{
          ...card,
          ...(bgUrl
            ? { backgroundImage: `linear-gradient(rgba(10,13,12,.74),rgba(10,13,12,.84)), url(${bgUrl})`, backgroundSize: 'cover', backgroundPosition: 'center' }
            : {}),
          cursor: clickable ? 'pointer' : 'default',
        }}
        onClick={clickable ? onOpenProfile : undefined}
      >
        {/* 頭像 + 名稱/暱稱/編碼 + DP */}
        <div style={{ display: 'flex', gap: 14, alignItems: 'center' }}>
          {onUploadAvatar ? (
            <label style={{ ...avatarWrap, cursor: 'pointer' }} title="更換頭像" onClick={(e) => e.stopPropagation()}>
              <Avatar user={!!user} dash={dash} />
              <span style={avatarEdit}>{uploadingAvatar ? '…' : '✎'}</span>
              <input type="file" accept="image/*" style={{ display: 'none' }}
                onChange={(e) => { const f = e.target.files?.[0]; if (f) onUploadAvatar(f); e.target.value = '' }} />
            </label>
          ) : (
            <div style={avatarWrap}><Avatar user={!!user} dash={dash} /></div>
          )}
          <div style={{ flex: 1, minWidth: 0 }}>
            {user ? (
              <>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{dash?.name || user.name}</span>
                  {dash?.is_vip && <span style={vipBadge}>VIP</span>}
                </div>
                {dash?.nickname && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{dash.nickname}</div>}
                <button onClick={copyCode} style={codeChip} title="複製帳號編碼">#{dash?.account_code ?? '…'} {copied ? '已複製' : '⧉'}</button>
              </>
            ) : (
              <button onClick={(e) => { e.stopPropagation(); setShowLogin(true) }} style={loginBtn}>註冊 / 登入</button>
            )}
          </div>
          {user && dash && (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 5, flexShrink: 0 }}>
              {/* 會員身分（移到右上角）：VIP 顯示到期日，否則「一般會員」 */}
              <span style={{ fontSize: 10.5, fontWeight: dash.is_vip ? 800 : 500, color: dash.is_vip ? 'var(--gold)' : 'var(--tx-faint)', whiteSpace: 'nowrap' }}>
                {dash.is_vip ? `VIP${dash.vip_expires_at ? ` · 至 ${fmtDate10(dash.vip_expires_at)}` : ' 會員'}` : '一般會員'}
              </span>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, color: '#FFD24D', fontWeight: 900, fontSize: 14, fontVariantNumeric: 'tabular-nums' }} title="DP 幣">
                <DpCoin size={16} />{(dash.dp ?? 0).toLocaleString()}
              </span>
            </div>
          )}
        </div>

        {/* 等級 + EXP */}
        {user && dash && (
          <div style={{ marginTop: 14 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', fontSize: 12 }}>
              <span style={{ fontWeight: 800, color: 'var(--fug)' }}>Lv.{dash.level}{dash.level_title ? ` ${dash.level_title}` : ''}</span>
              <span style={{ color: 'var(--tx-dim)' }}>{dash.exp} EXP</span>
            </div>
            <div style={barOuter}><div style={{ ...barInner, width: `${expPct}%` }} /></div>
            <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 4, textAlign: 'right' }}>
              {dash.next_level_exp == null ? '已達最高等級' : `距 Lv.${dash.level + 1} 還需 ${dash.next_level_exp - dash.exp} EXP`}
            </div>
          </div>
        )}

        {/* 戰績（6 格）：完成 / 報名 / 進行中 / 完成里程 / 追蹤 / 粉絲 */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 8, marginTop: 12 }}>
          <Stat label="完成" value={user && dash ? `${dash.completed_count}` : '--'} />
          <Stat label="報名" value={user && dash ? `${dash.race_count}` : '--'} />
          <Stat label="進行中" value={user && dash ? `${dash.ongoing_count}` : '--'} />
          <Stat label="完成里程" value={user && dash ? `${dash.total_km.toFixed(1)}K` : '--'} />
          <Stat label="追蹤" value={user && dash ? `${dash.following_count}` : '--'} />
          <Stat label="粉絲" value={user && dash ? `${dash.follower_count}` : '--'} />
        </div>

      </div>
      {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
    </>
  )
}

function Avatar({ user, dash }: { user: boolean; dash: DashboardInfo | null }) {
  if (user && dash?.avatar_url) {
    // eslint-disable-next-line @next/next/no-img-element
    return <img src={dash.avatar_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
  }
  return <span style={{ fontSize: 22, fontWeight: 800, color: 'var(--tx-dim)' }}>{user ? (dash?.name || '?').slice(0, 1) : '？'}</span>
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ background: 'var(--bg-2)', borderRadius: 'var(--radius-md, 10px)', padding: '8px 4px', textAlign: 'center' }}>
      <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>{value}</div>
      <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 2 }}>{label}</div>
    </div>
  )
}

function fmtDate10(iso?: string | null) {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())}`
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-lg, 16px)', padding: 'var(--card-pad, 16px)', boxShadow: 'var(--card-shadow, none)' }
const avatarWrap: React.CSSProperties = {
  position: 'relative', width: 64, height: 64, borderRadius: '50%', overflow: 'hidden', flexShrink: 0,
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center',
}
const avatarEdit: React.CSSProperties = { position: 'absolute', bottom: 0, left: 0, right: 0, fontSize: 10, textAlign: 'center', background: 'rgba(0,0,0,.55)', color: '#fff', padding: '1px 0' }
const vipBadge: React.CSSProperties = { fontSize: 10, fontWeight: 800, color: '#1a1200', background: 'var(--gold)', borderRadius: 6, padding: '1px 7px', letterSpacing: '.05em' }
const codeChip: React.CSSProperties = { marginTop: 4, fontSize: 11, color: 'var(--tx-dim)', background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '2px 10px', cursor: 'pointer', fontFamily: 'monospace' }
const barOuter: React.CSSProperties = { height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 5 }
const barInner: React.CSSProperties = { height: '100%', background: 'var(--fug)', borderRadius: 999, transition: 'width .3s' }
const loginBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 18px', cursor: 'pointer', fontSize: 14 }
