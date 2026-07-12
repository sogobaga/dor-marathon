'use client'

import { useState } from 'react'
import useSWR from 'swr'
import { racesApi, type Race, type MyRegLite } from '@/lib/api'
import { getUserToken, useUser, clearUserSession } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import { useDraggableSheet } from '@/lib/useDraggableSheet'
import MemberPanel from './MemberPanel'
import UpgradeVipModal from './UpgradeVipModal'

const DISPLAY_STATUS: Record<string, { label: string; color: string }> = {
  upcoming_reg: { label: '即將報名', color: 'var(--violet)' },
  registering: { label: '報名中', color: 'var(--gold)' },
  reg_closed: { label: '報名結束', color: 'var(--tx-faint)' },
  starting_soon: { label: '賽事即將開始', color: 'var(--violet)' },
  racing: { label: '賽事進行中', color: 'var(--fug)' },
  ended: { label: '賽事結束', color: 'var(--tx-faint)' },
  paused: { label: '暫停報名', color: 'var(--hunt)' },
  suspended: { label: '賽事中止', color: 'var(--hunt)' },
}

// 搜尋標籤分類：把 display_status 歸到 報名中/進行中/已結束
type FilterKey = 'all' | 'reg' | 'racing' | 'ended'
const CATEGORY: Record<string, Exclude<FilterKey, 'all'>> = {
  upcoming_reg: 'reg', registering: 'reg', paused: 'reg',
  reg_closed: 'racing', starting_soon: 'racing', racing: 'racing',
  ended: 'ended', suspended: 'ended',
}
const FILTER_TABS: { key: FilterKey; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'reg', label: '報名中' },
  { key: 'racing', label: '進行中' },
  { key: 'ended', label: '已結束' },
]

function fmtFee(cents: number) {
  return 'NT$ ' + Math.round(cents / 100).toLocaleString('zh-TW')
}

function fmtDateTime(iso: string) {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function RacesScreen({
  onOpenRanking,
  onRegister,
  onPay,
  onOpenProfile,
  onOpenPersonalTasks,
  onOpenExplore,
  onOpenGallery,
  onOpenTitle,
  onOpenAchievement,
  onOpenBrochure,
}: {
  onOpenRanking?: (race: Race) => void
  onRegister?: (race: Race) => void
  onPay?: (race: Race) => void
  onOpenProfile?: () => void
  onOpenPersonalTasks?: () => void
  onOpenExplore?: () => void
  onOpenGallery?: () => void
  onOpenTitle?: () => void
  onOpenAchievement?: () => void
  onOpenBrochure?: (race: Race) => void
}) {
  const user = useUser() // 登入狀態變動時重新渲染 → 用最新 token 重抓報名狀態
  const token = getUserToken() || undefined
  const { data, error, isLoading } = useSWR(['races', user?.id ?? null, token], () => racesApi.list(token))
  const regs = data?.registrations || {}
  // COROS 式 UX：會員面板固定最上方，活動列表做成可上下拖曳的面板（收合看完整會員面板／半展看列表／全展看整份列表）
  const sheet = useDraggableSheet('half')
  const [filter, setFilter] = useState<FilterKey>('all')
  const [showUpgrade, setShowUpgrade] = useState(false)
  const { dash } = useDashboard()
  const races = data?.races ?? []
  const filtered = filter === 'all' ? races : races.filter((r) => CATEGORY[r.display_status] === filter)

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      {/* Header（精簡品牌列；登出置最右上角） */}
      <header style={{ padding: 'var(--app-top) max(22px, env(safe-area-inset-right, 0px)) 0 max(22px, env(safe-area-inset-left, 0px))', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
        <div style={{ fontSize: 11, letterSpacing: '.18em', color: 'var(--fug)', fontWeight: 600 }}>
          DOR · 城市探索
        </div>
        {user && (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
            {!dash?.is_vip && (
              <button
                onClick={() => setShowUpgrade(true)}
                style={{ background: 'var(--gold)', color: '#fff', border: 'none', borderRadius: 8, padding: '5px 11px', cursor: 'pointer', fontSize: 12, fontWeight: 800 }}
              >✦ 升級VIP</button>
            )}
            <button
              onClick={() => clearUserSession()}
              style={{ background: 'rgba(255,255,255,.05)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 11px', cursor: 'pointer', fontSize: 12 }}
            >登出</button>
          </div>
        )}
      </header>

      {showUpgrade && <UpgradeVipModal onClose={() => setShowUpgrade(false)} />}

      {/* 會員面板（固定最上方，背景層）+ 可拖曳活動列表面板 */}
      <div ref={sheet.wrapRef} style={{ position: 'relative', flex: 1, minHeight: 0, overflow: 'hidden' }}>
        {/* 會員資訊面板：固定最上方；面板收合時完整顯示，可自行捲動 */}
        <div style={{ position: 'absolute', inset: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '4px 18px 0' }}>
          <MemberPanel onOpenProfile={onOpenProfile} onOpenPersonalTasks={onOpenPersonalTasks} onOpenExplore={onOpenExplore} onOpenGallery={onOpenGallery} onOpenTitle={onOpenTitle} onOpenAchievement={onOpenAchievement} showEntries={false} />
        </div>

        {/* 可拖曳活動列表面板 */}
        <div style={{
          position: 'absolute', left: 0, right: 0, top: sheet.curY, bottom: 0,
          transition: !sheet.dragging && sheet.ready ? 'top .28s cubic-bezier(.22,.61,.36,1)' : 'none',
          opacity: sheet.ready ? 1 : 0,
          display: 'flex', flexDirection: 'column',
          background: 'var(--bg)', color: 'var(--tx)',
          borderTopLeftRadius: 18, borderTopRightRadius: 18,
          borderTop: '1px solid var(--line)', boxShadow: '0 -10px 30px rgba(0,0,0,.22)',
          zIndex: 500, userSelect: 'none', WebkitUserSelect: 'none',
        }}>
          {/* 把手 + 標題（收合時僅露這區；此整區皆可拖曳） */}
          <div ref={sheet.peekRef} {...sheet.handlers}
               style={{ flexShrink: 0, padding: '8px 18px 10px', cursor: 'grab', touchAction: 'none' }}>
            <div style={{ width: 40, height: 5, borderRadius: 3, background: 'var(--line-2)', margin: '0 auto 10px' }} />
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <h1 style={{ margin: 0, fontSize: 18, fontWeight: 800, color: 'var(--tx)' }}>活動列表</h1>
              {/* 搜尋標籤（置右）：全部/報名中/進行中/已結束 */}
              <div style={{ display: 'flex', gap: 6, marginLeft: 'auto', flexWrap: 'wrap', justifyContent: 'flex-end' }}>
                {FILTER_TABS.map(({ key, label }) => {
                  const on = filter === key
                  return (
                    <button key={key} onClick={() => setFilter(key)} style={{
                      fontSize: 12, padding: '4px 11px', borderRadius: 999, cursor: 'pointer', whiteSpace: 'nowrap',
                      background: on ? 'var(--fug)' : 'transparent',
                      color: on ? 'var(--fug-ink)' : 'var(--tx-dim)',
                      border: `1px solid ${on ? 'var(--fug)' : 'var(--line-2)'}`,
                      fontWeight: on ? 700 : 500,
                    }}>{label}</button>
                  )
                })}
              </div>
            </div>
          </div>
          {/* 可捲動活動列表 */}
          <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', touchAction: 'pan-y', padding: '4px 18px calc(16px + var(--cta-safe, 0px))' }}>
            {isLoading && <Hint>載入中…</Hint>}
            {error && <Hint color="var(--hunt)">無法載入賽事：{String(error.message || error)}</Hint>}
            {data && races.length === 0 && <Hint>目前沒有賽事</Hint>}
            {data && races.length > 0 && filtered.length === 0 && <Hint>此分類目前沒有活動</Hint>}

            {filtered.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                {filtered.map((r) => (
                  <RaceCard key={r.id} race={r} reg={regs[r.id]} onOpenRanking={onOpenRanking} onRegister={onRegister} onPay={onPay} onOpenBrochure={onOpenBrochure} />
                ))}
              </div>
            )}
            <div className="skin-footer-deco" aria-hidden />
          </div>
        </div>
      </div>

      {/* 開始跑步（比照 GPS 跑步追蹤頁：置底整排綠色 CTA） */}
      <div style={{ padding: '14px 16px calc(20px + var(--cta-safe, 0px))', flexShrink: 0, borderTop: '1px solid var(--line)', background: 'var(--bg)' }}>
        <a href="/track" className="skin-btn-start" style={startBtn}>▶ 開始跑步</a>
      </div>
    </div>
  )
}

const startBtn: React.CSSProperties = {
  display: 'block', width: '100%', boxSizing: 'border-box', textAlign: 'center', textDecoration: 'none',
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none',
  borderRadius: 'var(--radius-btn, 12px)', padding: '15px 20px', fontSize: 16, cursor: 'pointer',
}

function RaceCard({
  race,
  reg,
  onOpenRanking,
  onRegister,
  onPay,
  onOpenBrochure,
}: {
  race: Race
  reg?: MyRegLite
  onOpenRanking?: (race: Race) => void
  onRegister?: (race: Race) => void
  onPay?: (race: Race) => void
  onOpenBrochure?: (race: Race) => void
}) {
  const s = DISPLAY_STATUS[race.display_status] ?? { label: race.display_status, color: 'var(--tx-faint)' }
  const isCompetition = race.event_mode === 'competition'
  const canRegister = race.can_register
  const stop = (e: React.MouseEvent) => e.stopPropagation()
  return (
    <div
      className="skin-frame"
      onClick={() => onOpenBrochure?.(race)}
      style={{
        background: 'var(--bg-1)',
        border: '1px solid var(--line)',
        borderRadius: 'var(--radius-lg, 18px)',
        boxShadow: 'var(--card-shadow, none)',
        overflow: 'hidden',
        cursor: onOpenBrochure ? 'pointer' : 'default',
      }}
    >
      {/* 頂部 Banner：與「活動說明頁」頂部同一張 hero_image_url */}
      {race.hero_image_url && (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={race.hero_image_url} alt="" style={{ width: '100%', display: 'block' }} />
      )}

      <div style={{ padding: 'var(--card-pad, 18px)', display: 'flex', flexDirection: 'column', gap: 12 }}>
        {/* 標題 + 距離 chip；右上角狀態／報名徽章直排 */}
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 10 }}>
          <div style={{ minWidth: 0 }}>
            <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)', lineHeight: 1.3, wordBreak: 'keep-all', overflowWrap: 'break-word', display: 'block' }}>{race.title}</span>
            {/* 距離 chip 換行至標題下方（不接在名稱後面） */}
            {race.distances.length > 0 && (
              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginTop: 6 }}>
                {race.distances.map((d) => (
                  <span key={d} style={{ fontSize: 11.5, padding: '2px 8px', borderRadius: 8, background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--tx)', flexShrink: 0 }}>{d}K</span>
                ))}
              </div>
            )}
            {race.subtitle && <div style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-dim)', marginTop: 3 }}>{race.subtitle}</div>}
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 6, flexShrink: 0 }}>
            <span style={{ fontSize: 11, padding: '4px 10px', borderRadius: 999, color: s.color, border: `1px solid ${s.color}`, background: 'rgba(255,255,255,.03)', whiteSpace: 'nowrap' }}>● {s.label}</span>
            <span style={{ fontSize: 12, fontWeight: 700, padding: '4px 12px', borderRadius: 999, color: 'var(--gold)', border: '1px solid var(--gold)', whiteSpace: 'nowrap' }}>{fmtFee(race.entry_fee)}</span>
          </div>
        </div>

        {/* 報名期間 / 賽事期間：兩並排資訊卡（各帶 icon） */}
        <div style={{ display: 'flex', gap: 8 }}>
          <PeriodBox icon="/source/ui/01_icons/icon_calendar_orange.png" label="報名期間" start={dt(race.registration_start)} end={dt(race.registration_end)} />
          <PeriodBox icon="/source/ui/01_icons/icon_runner_green.png" label="活動期間" start={dt(race.start_date)} end={dt(race.end_date)} />
        </div>

        {/* 底列：排行榜（左）＋ 立即報名／報名完成／前往繳費（右） */}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 13 }}>
          {isCompetition && onOpenRanking
            ? <button onClick={(e) => { stop(e); onOpenRanking(race) }} style={linkBtnStyle}>排行榜</button>
            : <span />}
          {reg
            ? (reg.status === 'paid'
                ? <span style={{ ...ctaLink, cursor: 'default' }}>報名完成 ›</span>
                : <button onClick={(e) => { stop(e); onPay?.(race) }} style={ctaLink}>前往繳費 ›</button>)
            : (canRegister && onRegister
                ? <button onClick={(e) => { stop(e); onRegister(race) }} style={ctaLink}>立即報名 ›</button>
                : <span style={{ color: 'var(--tx-faint)', fontSize: 12.5 }}>{s.label}</span>)}
        </div>
      </div>
    </div>
  )
}

function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return (
    <div style={{ textAlign: 'center', padding: '60px 20px', fontSize: 13.5, color }}>{children}</div>
  )
}

// 期間 iso → 「M/D HH:MM」；空值顯示「未設定」
function dt(iso?: string | null) { return iso ? fmtDateTime(iso) : '未設定' }

// 活動面板的「報名期間／活動期間」資訊卡（帶 icon；開始/結束分兩行，好讀、不用 – 連接）
function PeriodBox({ icon, label, start, end }: { icon: string; label: string; start: string; end: string }) {
  return (
    <div style={{ flex: 1, minWidth: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 'var(--radius-md, 12px)', padding: '9px 11px', display: 'flex', flexDirection: 'column', gap: 5 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={icon} alt="" width={16} height={16} style={{ display: 'block', flexShrink: 0 }} />
        <span style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>{label}</span>
      </div>
      <div style={{ display: 'flex', gap: 6, fontSize: 12 }}>
        <span style={{ color: 'var(--tx-faint)', flexShrink: 0 }}>開始</span>
        <span style={{ color: 'var(--tx)', fontWeight: 600 }}>{start}</span>
      </div>
      <div style={{ display: 'flex', gap: 6, fontSize: 12 }}>
        <span style={{ color: 'var(--tx-faint)', flexShrink: 0 }}>結束</span>
        <span style={{ color: 'var(--tx)', fontWeight: 600 }}>{end}</span>
      </div>
    </div>
  )
}

const linkBtnStyle: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 12.5, padding: 0,
}
const ctaLink: React.CSSProperties = {
  background: 'none', border: 'none', padding: 0, cursor: 'pointer',
  color: 'var(--fug)', fontWeight: 700, fontSize: 13.5,
}
