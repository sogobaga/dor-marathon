'use client'

import useSWR from 'swr'
import { racesApi, type Race, type MyRegLite } from '@/lib/api'
import { getUserToken, useUser, clearUserSession } from '@/lib/userAuth'
import MemberPanel from './MemberPanel'

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

function fmtFee(cents: number) {
  return 'NT$ ' + Math.round(cents / 100).toLocaleString('zh-TW')
}

function fmtDate(iso: string) {
  const d = new Date(iso)
  return `${d.getMonth() + 1}/${d.getDate()}`
}
function fmtDateTime(iso: string) {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function periodText(start?: string | null, end?: string | null, withTime?: boolean) {
  const f = withTime ? fmtDateTime : fmtDate
  if (!start && !end) return '未設定'
  return `${start ? f(start) : '—'} – ${end ? f(end) : '—'}`
}

export default function RacesScreen({
  onOpenRanking,
  onRegister,
  onPay,
  onOpenProfile,
  onOpenBrochure,
}: {
  onOpenRanking?: (race: Race) => void
  onRegister?: (race: Race) => void
  onPay?: (race: Race) => void
  onOpenProfile?: () => void
  onOpenBrochure?: (race: Race) => void
}) {
  const user = useUser() // 登入狀態變動時重新渲染 → 用最新 token 重抓報名狀態
  const token = getUserToken() || undefined
  const { data, error, isLoading } = useSWR(['races', user?.id ?? null, token], () => racesApi.list(token))
  const regs = data?.registrations || {}

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      {/* Header（精簡品牌列；登出置最右上角） */}
      <header style={{ padding: '52px 22px 10px', flexShrink: 0, display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
        <div style={{ fontSize: 11, letterSpacing: '.18em', color: 'var(--fug)', fontWeight: 600 }}>
          DOR · 雲端馬拉松
        </div>
        {user && (
          <button
            onClick={() => clearUserSession()}
            style={{ flexShrink: 0, background: 'rgba(255,255,255,.05)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 11px', cursor: 'pointer', fontSize: 12 }}
          >登出</button>
        )}
      </header>

      {/* Body */}
      <div style={{ flex: 1, overflowY: 'scroll', WebkitOverflowScrolling: 'touch', overscrollBehaviorY: 'auto' }}>
        {/* minHeight 比容器高 1px → 內容沒滿版時也可滑動，保留 iOS 回彈手感 */}
        <div style={{ minHeight: 'calc(100% + 1px)', padding: '4px 18px 28px' }}>
        {/* 會員資訊面板 */}
        <MemberPanel onOpenProfile={onOpenProfile} />

        {/* GPS 跑步追蹤（PoC） */}
        <a href="/track" style={{ display: 'block', marginTop: 12, textDecoration: 'none', textAlign: 'center', background: 'rgba(70,227,160,.1)', border: '1px solid rgba(70,227,160,.35)', color: 'var(--fug)', fontWeight: 800, borderRadius: 12, padding: '12px 16px', fontSize: 14 }}>🏃 開始跑步</a>

        <h1 style={{ margin: '22px 0 12px', fontSize: 20, fontWeight: 800, color: 'var(--tx)' }}>賽事列表</h1>
        {isLoading && <Hint>載入中…</Hint>}
        {error && <Hint color="var(--hunt)">無法載入賽事：{String(error.message || error)}</Hint>}
        {data && data.races.length === 0 && <Hint>目前沒有賽事</Hint>}

        {data && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {data.races.map((r) => (
              <RaceCard key={r.id} race={r} reg={regs[r.id]} onOpenRanking={onOpenRanking} onRegister={onRegister} onPay={onPay} onOpenBrochure={onOpenBrochure} />
            ))}
          </div>
        )}
        </div>
      </div>
    </div>
  )
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
      onClick={() => onOpenBrochure?.(race)}
      style={{
        background: 'var(--bg-1)',
        border: '1px solid var(--line)',
        borderRadius: 18,
        padding: 18,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
        cursor: onOpenBrochure ? 'pointer' : 'default',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 10 }}>
        <div>
          <div style={{ fontSize: 19, fontWeight: 800, color: 'var(--tx)' }}>{race.title}</div>
          <div style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-dim)', marginTop: 2 }}>
            {race.subtitle}
          </div>
        </div>
        <span
          style={{
            flexShrink: 0,
            fontSize: 11,
            padding: '4px 10px',
            borderRadius: 999,
            color: s.color,
            border: `1px solid ${s.color}`,
            background: 'rgba(255,255,255,.03)',
          }}
        >
          ● {s.label}
        </span>
      </div>

      {race.world && (
        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>{race.world}</div>
      )}

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
        {race.distances.map((d) => (
          <span
            key={d}
            style={{
              fontSize: 11.5,
              padding: '3px 9px',
              borderRadius: 8,
              background: 'var(--bg-2)',
              border: '1px solid var(--line-2)',
              color: 'var(--tx)',
            }}
          >
            {d}K
          </span>
        ))}
      </div>

      <div style={{ marginTop: 4, paddingTop: 12, borderTop: '1px solid var(--line)', display: 'flex', flexDirection: 'column', gap: 4 }}>
        <div style={{ display: 'flex', gap: 8, fontSize: 12.5 }}>
          <span style={{ color: 'var(--tx-faint)', width: 56, flexShrink: 0 }}>報名期間</span>
          <span style={{ color: 'var(--tx)' }}>{periodText(race.registration_start, race.registration_end, true)}</span>
        </div>
        <div style={{ display: 'flex', gap: 8, fontSize: 12.5 }}>
          <span style={{ color: 'var(--tx-faint)', width: 56, flexShrink: 0 }}>賽事期間</span>
          <span style={{ color: 'var(--tx)' }}>{periodText(race.start_date, race.end_date, true)}</span>
        </div>
      </div>

      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          fontSize: 12.5,
          color: 'var(--tx-dim)',
        }}
      >
        <span style={{ color: 'var(--gold)', fontWeight: 700 }}>{fmtFee(race.entry_fee)}</span>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          {isCompetition && onOpenRanking && (
            <button onClick={(e) => { stop(e); onOpenRanking(race) }} style={linkBtnStyle}>排行榜</button>
          )}
          {reg ? (
            reg.status === 'paid' ? (
              <span style={{ color: 'var(--fug)', fontWeight: 700, fontSize: 13 }}>報名完成</span>
            ) : (
              <button onClick={(e) => { stop(e); onPay?.(race) }} style={payBtnStyle}>已報名，前往繳費</button>
            )
          ) : canRegister && onRegister ? (
            <button onClick={(e) => { stop(e); onRegister(race) }} style={registerBtnStyle}>報名</button>
          ) : (
            <span style={{ color: 'var(--tx-faint)', fontSize: 12.5 }}>{s.label}</span>
          )}
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

const linkBtnStyle: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 12.5, padding: 0,
}
const registerBtnStyle: React.CSSProperties = {
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 9, padding: '6px 14px', cursor: 'pointer', fontSize: 13,
}
const payBtnStyle: React.CSSProperties = {
  background: 'var(--gold)', color: '#1a1200', fontWeight: 700, border: 'none',
  borderRadius: 9, padding: '6px 14px', cursor: 'pointer', fontSize: 13,
}
