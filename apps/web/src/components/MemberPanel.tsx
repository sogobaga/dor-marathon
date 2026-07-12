'use client'

import { useEffect, useState } from 'react'
import useSWR from 'swr'
import { settingsApi, type DashboardInfo } from '@/lib/api'
import { useDashboard } from '@/lib/useDashboard'
import { LoginModal } from './UserAuthBar'
import DpCoin from './DpCoin'
import MailPanel from './MailPanel'

// 會員資訊面板（首頁與「會員資訊頁」共用，內容一致）。
// - 未帶 dash：自行抓取（首頁用法），並在資料就緒時呼叫 onReady。
// - 有帶 dash：受控（會員資訊頁用法，用該頁既有的 dashboard 資料）。
// - onOpenProfile：整張卡可點 → 開會員資訊頁（首頁）。
// - onUploadAvatar：頭像變成可上傳（會員資訊頁）。
export default function MemberPanel({
  dash: dashProp,
  onOpenProfile,
  onOpenPersonalTasks,
  onOpenExplore,
  onOpenGallery,
  onOpenTitle,
  onOpenAchievement,
  onUploadAvatar,
  uploadingAvatar,
  onReady,
  showEntries = true,
}: {
  dash?: DashboardInfo | null
  onOpenProfile?: () => void
  onOpenPersonalTasks?: () => void
  onOpenExplore?: () => void
  onOpenGallery?: () => void
  onOpenTitle?: () => void
  onOpenAchievement?: () => void
  onUploadAvatar?: (file: File) => void
  uploadingAvatar?: boolean
  onReady?: () => void
  showEntries?: boolean // 城市探索/卡片圖鑑入口：首頁隱藏(小尺寸會被遮)、僅會員資料頁顯示
}) {
  const controlled = dashProp !== undefined // 有傳 dash（含 null）＝受控；未傳＝用共用快取
  const { dash: hookDash, loading, user } = useDashboard() // 共用快取：與會員資訊頁同一份、切頁不再 loading
  const [showLogin, setShowLogin] = useState(false)
  const { data: settings } = useSWR('site-settings', () => settingsApi.get())
  const bgUrl = settings?.settings.member_panel_bg_url
  const dash = controlled ? dashProp ?? null : hookDash

  // 資料就緒（有快取即時顯示、或載完、或未登入）→ 通知父層（拖曳面板量測用）
  useEffect(() => { if (!controlled && !loading) onReady?.() }, [controlled, loading, onReady])

  const expPct =
    dash && dash.next_level_exp != null && dash.next_level_exp > dash.level_floor
      ? Math.max(0, Math.min(100, ((dash.exp - dash.level_floor) / (dash.next_level_exp - dash.level_floor)) * 100))
      : 100

  // 體力值 SP（凍結倒數需隨時間更新 → nowMs 每 30 秒 tick，僅凍結期間運作）
  const [nowMs, setNowMs] = useState(() => Date.now())
  const spMax = dash?.sp_max ?? 0
  const spPct = spMax > 0 ? Math.max(0, Math.min(100, ((dash?.sp ?? 0) / spMax) * 100)) : 0
  const spFreezeMs = dash?.sp_freeze_until ? new Date(dash.sp_freeze_until).getTime() : 0
  const spFrozen = spFreezeMs > nowMs
  const spFreezeLeftS = Math.max(0, Math.round((spFreezeMs - nowMs) / 1000))
  const spLow = !spFrozen && spMax > 0 && (dash?.sp ?? 0) < spMax * 0.25 // 剩餘 SP < 上限 25% → 橘紅警示
  useEffect(() => {
    if (!spFrozen) return
    const id = setInterval(() => setNowMs(Date.now()), 30000)
    return () => clearInterval(id)
  }, [spFrozen])

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
        {/* 頭像 + 右側兩排（面板高度不變，仍由頭像高度決定）：
            名稱排＝VIP＋名稱（左）… 信件icon（右，靠右對齊、獨佔右上，不受 DP 增長排擠）；
            暱稱排＝暱稱（左）… DP（右，與暱稱同高；DP 變長只壓縮左側暱稱、不影響信件）。 */}
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
          {user ? (
            <div style={{ flex: 1, minWidth: 0 }}>
              {/* 名稱排：VIP＋名稱（左）… 信件icon（右，靠右對齊） */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                {dash?.is_vip && <span style={{ ...vipBadge, flexShrink: 0 }}>VIP</span>}
                <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1, minWidth: 0 }}>{dash?.name || user.name}</span>
                <span onClick={(e) => e.stopPropagation()} style={{ display: 'inline-flex', flexShrink: 0 }}><MailPanel /></span>
              </div>
              {/* 暱稱排：暱稱（左）… DP（右，與暱稱同高） */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 2 }}>
                {dash?.nickname
                  ? <span style={{ fontSize: 12, color: 'var(--tx-dim)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1, minWidth: 0 }}>{dash.nickname}</span>
                  : <span style={{ flex: 1 }} />}
                {dash && <span style={dpBadge} title="DP 幣"><DpCoin size={16} />{(dash.dp ?? 0).toLocaleString()}</span>}
              </div>
              {/* 帳號編碼已移至「個人資料」分頁，避免面板截圖外流 */}
            </div>
          ) : (
            <div style={{ flex: 1, minWidth: 0 }}>
              <button onClick={(e) => { e.stopPropagation(); setShowLogin(true) }} style={loginBtn}>註冊 / 登入</button>
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
          </div>
        )}

        {/* 體力值 SP（跑步扣、依時間恢復；扣到 0 凍結 6 小時） */}
        {user && dash && (
          <div style={{ marginTop: 10 }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, fontSize: 12 }}>
              <span style={{ fontWeight: 800, color: spFrozen ? 'var(--tx-dim)' : spLow ? '#f4623a' : '#2fbf71', flexShrink: 0 }}>
                體力 SP{spFrozen ? ' · 凍結中' : ''}
              </span>
              {/* 中間資訊：凍結中→剩餘恢復時間；SP 過低→強制休息警示 */}
              <span style={{ flex: 1, minWidth: 0, textAlign: 'center', fontSize: 10.5, fontWeight: 700, color: spFrozen ? 'var(--tx-dim)' : '#f4623a', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {spFrozen ? fmtFreezeLeft(spFreezeLeftS) : spLow ? 'SP 0 時，將有 6 小時強制休息' : ''}
              </span>
              <span style={{ color: spLow ? '#f4623a' : 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums', flexShrink: 0 }}>{dash.sp} / {dash.sp_max}</span>
            </div>
            <div style={barOuter}>
              <div style={{ ...barInner, width: `${spPct}%`, background: spFrozen ? 'var(--line-2)' : spLow ? 'linear-gradient(90deg,#f4623a,#ff8a5c)' : 'linear-gradient(90deg,#2fbf71,#57d98a)' }} />
            </div>
          </div>
        )}

        {/* 累計完成里程（重點）+ 個人任務入口（後台可控可見性） */}
        {user && dash && (
          <div style={{ display: 'flex', gap: 10, marginTop: 14, alignItems: 'stretch' }}>
            <div style={{ ...mileageBox, flex: dash.personal_entry === 'hidden' ? 1 : '0 0 auto' }}>
              <div style={{ fontSize: 22, fontWeight: 900, color: 'var(--fug)', fontVariantNumeric: 'tabular-nums', lineHeight: 1.05 }}>
                {dash.total_km.toFixed(1)}<span style={{ fontSize: 13, marginLeft: 2 }}>K</span>
              </div>
              <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 3, whiteSpace: 'nowrap' }}>累計完成里程</div>
            </div>
            {dash.personal_entry !== 'hidden' && (
              <button
                disabled={dash.personal_entry === 'locked'}
                onClick={(e) => { e.stopPropagation(); if (dash.personal_entry === 'shown') onOpenPersonalTasks?.() }}
                style={{ ...taskBtn, cursor: dash.personal_entry === 'shown' ? 'pointer' : 'default', opacity: dash.personal_entry === 'shown' ? 1 : 0.62 }}
              >
                <span style={{ fontSize: 15, fontWeight: 900 }}>個人任務</span>
                <span style={{ fontSize: 11, fontWeight: 700 }}>{dash.personal_entry === 'locked' ? '即將開放 ›' : '開始你的訓練旅程 ›'}</span>
              </button>
            )}
          </div>
        )}

        {/* 次要戰績（一行小字，不搶重點） */}
        {user && dash && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 14px', marginTop: 12, fontSize: 11.5 }}>
            <MiniStat label="完成" value={dash.completed_count} />
            <MiniStat label="報名" value={dash.race_count} />
            <MiniStat label="進行中" value={dash.ongoing_count} />
            <MiniStat label="追蹤" value={dash.following_count} />
            <MiniStat label="粉絲" value={dash.follower_count} />
          </div>
        )}

      </div>

      {/* 探索入口（面板下方、後台可控可見性；首頁不顯示）：上排 城市探索|卡片圖鑑、下排 PB探索|成就探索 */}
      {showEntries && user && dash && (dash.explore_entry !== 'hidden' || dash.gallery_entry !== 'hidden' || dash.title_entry !== 'hidden' || dash.achievement_entry !== 'hidden') && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 10 }}>
          {(dash.explore_entry !== 'hidden' || dash.gallery_entry !== 'hidden') && (
            <div style={{ display: 'flex', gap: 10 }}>
              {dash.explore_entry !== 'hidden' && (
                <button disabled={dash.explore_entry === 'locked'}
                  onClick={(e) => { e.stopPropagation(); if (dash.explore_entry === 'shown') onOpenExplore?.() }}
                  style={{ ...entryBtn, opacity: dash.explore_entry === 'shown' ? 1 : 0.6, cursor: dash.explore_entry === 'shown' ? 'pointer' : 'default' }}>
                  <span style={{ fontSize: 14, fontWeight: 900, color: 'var(--tx)' }}>城市探索</span>
                  <span style={{ fontSize: 10.5, fontWeight: 700, color: 'var(--tx-dim)' }}>{dash.explore_entry === 'locked' ? '即將開放 ›' : '發現城市美好 ›'}</span>
                </button>
              )}
              {dash.gallery_entry !== 'hidden' && (
                <button disabled={dash.gallery_entry === 'locked'}
                  onClick={(e) => { e.stopPropagation(); if (dash.gallery_entry === 'shown') onOpenGallery?.() }}
                  style={{ ...entryBtn, opacity: dash.gallery_entry === 'shown' ? 1 : 0.6, cursor: dash.gallery_entry === 'shown' ? 'pointer' : 'default' }}>
                  <span style={{ fontSize: 14, fontWeight: 900, color: 'var(--tx)' }}>卡片探索</span>
                  <span style={{ fontSize: 10.5, fontWeight: 700, color: 'var(--tx-dim)' }}>{dash.gallery_entry === 'locked' ? '即將開放 ›' : '挑戰各方好手 ›'}</span>
                </button>
              )}
            </div>
          )}
          {(dash.title_entry !== 'hidden' || dash.achievement_entry !== 'hidden') && (
            <div style={{ display: 'flex', gap: 10 }}>
              {dash.title_entry !== 'hidden' && (
                <button disabled={dash.title_entry === 'locked'}
                  onClick={(e) => { e.stopPropagation(); if (dash.title_entry === 'shown') onOpenTitle?.() }}
                  style={{ ...entryBtn, opacity: dash.title_entry === 'shown' ? 1 : 0.6, cursor: dash.title_entry === 'shown' ? 'pointer' : 'default' }}>
                  <span style={{ fontSize: 14, fontWeight: 900, color: 'var(--tx)' }}>PB探索</span>
                  <span style={{ fontSize: 10.5, fontWeight: 700, color: 'var(--tx-dim)' }}>{dash.title_entry === 'locked' ? '即將開放 ›' : '解鎖你的稱號 ›'}</span>
                </button>
              )}
              {dash.achievement_entry !== 'hidden' && (
                <button disabled={dash.achievement_entry === 'locked'}
                  onClick={(e) => { e.stopPropagation(); if (dash.achievement_entry === 'shown') onOpenAchievement?.() }}
                  style={{ ...entryBtn, opacity: dash.achievement_entry === 'shown' ? 1 : 0.6, cursor: dash.achievement_entry === 'shown' ? 'pointer' : 'default' }}>
                  <span style={{ fontSize: 14, fontWeight: 900, color: 'var(--tx)' }}>成就探索</span>
                  <span style={{ fontSize: 10.5, fontWeight: 700, color: 'var(--tx-dim)' }}>{dash.achievement_entry === 'locked' ? '即將開放 ›' : '你的數據成就 ›'}</span>
                </button>
              )}
            </div>
          )}
        </div>
      )}
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

function MiniStat({ label, value }: { label: string; value: number }) {
  return (
    <span>
      <b style={{ color: 'var(--tx)', fontWeight: 800, fontVariantNumeric: 'tabular-nums' }}>{value}</b>
      <span style={{ color: 'var(--tx-dim)', marginLeft: 4 }}>{label}</span>
    </span>
  )
}

const card: React.CSSProperties = { position: 'relative', background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-lg, 16px)', padding: 'var(--card-pad, 16px)', boxShadow: 'var(--card-shadow, none)' }
const dpBadge: React.CSSProperties = { display: 'inline-flex', alignItems: 'center', gap: 4, color: '#FFD24D', fontWeight: 900, fontSize: 14, fontVariantNumeric: 'tabular-nums', flexShrink: 0 }
const mileageBox: React.CSSProperties = { minWidth: 96, background: 'var(--bg-2)', borderRadius: 12, padding: '10px 14px', display: 'flex', flexDirection: 'column', justifyContent: 'center' }
const taskBtn: React.CSSProperties = { flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', alignItems: 'flex-start', justifyContent: 'center', gap: 3, textAlign: 'left', border: 'none', borderRadius: 12, padding: '10px 14px', background: 'var(--fug)', color: 'var(--fug-ink)', fontFamily: 'inherit' }
const entryBtn: React.CSSProperties = { flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 3, textAlign: 'left', border: '1px solid var(--line)', borderRadius: 'var(--radius-lg, 14px)', padding: '12px 14px', background: 'var(--bg-1)', fontFamily: 'inherit', boxShadow: 'var(--card-shadow, none)' }
const avatarWrap: React.CSSProperties = {
  position: 'relative', width: 64, height: 64, borderRadius: '50%', overflow: 'hidden', flexShrink: 0,
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center',
}
const avatarEdit: React.CSSProperties = { position: 'absolute', bottom: 0, left: 0, right: 0, fontSize: 10, textAlign: 'center', background: 'rgba(0,0,0,.55)', color: '#fff', padding: '1px 0' }
const vipBadge: React.CSSProperties = { fontSize: 10, fontWeight: 800, color: '#fff', background: 'var(--gold)', borderRadius: 6, padding: '1px 7px', letterSpacing: '.05em' }
// 凍結剩餘秒數 → 「約 X 小時 Y 分後恢復」（體力凍結倒數；不足 1 分顯示「約 1 分後恢復」）
function fmtFreezeLeft(sec: number): string {
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60)
  return h > 0 ? `約 ${h} 小時 ${m} 分後恢復` : `約 ${Math.max(1, m)} 分後恢復`
}
const barOuter: React.CSSProperties = { height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 5 }
const barInner: React.CSSProperties = { height: '100%', background: 'var(--fug)', borderRadius: 999, transition: 'width .3s' }
const loginBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 18px', cursor: 'pointer', fontSize: 14 }
