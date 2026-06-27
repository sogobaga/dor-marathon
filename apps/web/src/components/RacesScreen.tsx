'use client'

import useSWR from 'swr'
import { racesApi, type Race } from '@/lib/api'
import UserAuthBar from './UserAuthBar'

const STATUS: Record<Race['status'], { label: string; color: string }> = {
  live: { label: '進行中', color: 'var(--fug)' },
  open: { label: '報名中', color: 'var(--gold)' },
  soon: { label: '即將開始', color: 'var(--violet)' },
  done: { label: '已結束', color: 'var(--tx-faint)' },
}

function fmtFee(cents: number) {
  return 'NT$ ' + Math.round(cents / 100).toLocaleString('zh-TW')
}

function fmtDate(iso: string) {
  const d = new Date(iso)
  return `${d.getMonth() + 1}/${d.getDate()}`
}

export default function RacesScreen({ onOpenRanking }: { onOpenRanking?: (race: Race) => void }) {
  const { data, error, isLoading } = useSWR('races', racesApi.list)

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      {/* Header */}
      <header style={{ padding: '52px 22px 16px', flexShrink: 0 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
          <div>
            <div style={{ fontSize: 11, letterSpacing: '.18em', color: 'var(--fug)', fontWeight: 600 }}>
              DOR · 雲端馬拉松
            </div>
            <h1 style={{ margin: '6px 0 0', fontSize: 26, fontWeight: 800, color: 'var(--tx)' }}>賽事列表</h1>
          </div>
          <UserAuthBar />
        </div>
      </header>

      {/* Body */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '4px 18px 28px' }}>
        {isLoading && <Hint>載入中…</Hint>}
        {error && <Hint color="var(--hunt)">無法載入賽事：{String(error.message || error)}</Hint>}
        {data && data.races.length === 0 && <Hint>目前沒有賽事</Hint>}

        {data && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {data.races.map((r) => (
              <RaceCard key={r.id} race={r} onOpenRanking={onOpenRanking} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function RaceCard({ race, onOpenRanking }: { race: Race; onOpenRanking?: (race: Race) => void }) {
  const s = STATUS[race.status] ?? STATUS.done
  const isCompetition = race.event_mode === 'competition'
  return (
    <div
      onClick={() => onOpenRanking?.(race)}
      style={{
        background: 'var(--bg-1)',
        border: '1px solid var(--line)',
        borderRadius: 18,
        padding: 18,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
        cursor: onOpenRanking ? 'pointer' : 'default',
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

      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginTop: 4,
          paddingTop: 12,
          borderTop: '1px solid var(--line)',
          fontSize: 12.5,
          color: 'var(--tx-dim)',
        }}
      >
        <span>
          {fmtDate(race.start_date)} – {fmtDate(race.end_date)}
        </span>
        {isCompetition && onOpenRanking ? (
          <span style={{ color: 'var(--fug)', fontWeight: 700 }}>分組排行榜 →</span>
        ) : (
          <span style={{ color: 'var(--gold)', fontWeight: 700 }}>{fmtFee(race.entry_fee)}</span>
        )}
      </div>
    </div>
  )
}

function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return (
    <div style={{ textAlign: 'center', padding: '60px 20px', fontSize: 13.5, color }}>{children}</div>
  )
}
