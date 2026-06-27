'use client'

import useSWR from 'swr'
import { racesApi, type Race, type StandingRank } from '@/lib/api'

function fmtPace(s: number) {
  if (!s) return '—'
  const m = Math.floor(s / 60)
  const sec = s % 60
  return `${m}'${String(sec).padStart(2, '0')}"`
}
function fmtDuration(s: number) {
  if (!s) return '—'
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  return h > 0 ? `${h}h${String(m).padStart(2, '0')}m` : `${m}m`
}

export default function RaceRankingScreen({ race, onBack }: { race: Race; onBack: () => void }) {
  const { data, error, isLoading } = useSWR(['standings', race.id], () => racesApi.standings(race.id), {
    refreshInterval: 30000,
  })

  const isCompetition = race.event_mode === 'competition'

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: '52px 22px 14px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>{race.title}</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>分組排行榜</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', padding: '4px 18px 28px' }}>
        {!isCompetition && (
          <Hint>此賽事為「{race.event_mode === 'faction_battle' ? '分組對抗' : '一般'}」模式，無分組成績排行。</Hint>
        )}

        {isCompetition && isLoading && <Hint>載入排行榜…</Hint>}
        {isCompetition && error && <Hint color="var(--hunt)">無法載入排行榜</Hint>}

        {isCompetition && data && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
            {data.my_group && (
              <div style={myBanner}>
                <div style={{ fontSize: 11, letterSpacing: '.12em', color: 'var(--fug)' }}>我的分組</div>
                <div style={{ fontSize: 17, fontWeight: 800, marginTop: 3 }}>{data.my_group.group_name}</div>
                <div style={{ display: 'flex', gap: 18, marginTop: 8, fontSize: 13, color: 'var(--tx-dim)' }}>
                  <span>累積榜 第 <b style={{ color: 'var(--tx)' }}>{data.my_group.cumulative_rank}</b> 名</span>
                  <span>完成時間榜 第 <b style={{ color: 'var(--tx)' }}>{data.my_group.finish_rank}</b> 名</span>
                </div>
              </div>
            )}

            <RankList
              title="累積里程榜"
              subtitle="各分組總累積里程"
              entries={data.by_cumulative}
              metric={(e) => `${e.total_km.toFixed(1)} K`}
              highlightId={data.my_group?.group_id}
            />

            {data.goal_type === 'distance' && (
              <RankList
                title="完成時間榜"
                subtitle="完成指定里程的累計總時間"
                entries={data.by_finish_time}
                metric={(e) => fmtDuration(e.finish_total_s)}
                highlightId={data.my_group?.group_id}
              />
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function RankList({
  title,
  subtitle,
  entries,
  metric,
  highlightId,
}: {
  title: string
  subtitle: string
  entries: StandingRank[]
  metric: (e: StandingRank) => string
  highlightId?: string
}) {
  return (
    <div>
      <div style={{ marginBottom: 8 }}>
        <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>{title}</div>
        <div style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>{subtitle} · 前 20 名</div>
      </div>
      {entries.length === 0 ? (
        <Hint>尚無成績資料</Hint>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {entries.map((e) => {
            const mine = e.group_id === highlightId
            return (
              <div
                key={e.group_id}
                style={{
                  display: 'flex', alignItems: 'center', gap: 12, padding: '11px 14px', borderRadius: 12,
                  background: mine ? 'rgba(45,212,150,.1)' : 'var(--bg-1)',
                  border: mine ? '1px solid var(--fug)' : '1px solid var(--line)',
                }}
              >
                <div style={{ width: 26, textAlign: 'center', fontWeight: 800, color: e.rank <= 3 ? 'var(--gold)' : 'var(--tx-dim)' }}>
                  {e.rank}
                </div>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 14.5, fontWeight: 600, color: 'var(--tx)' }}>{e.group_name}</div>
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                    {e.member_count} 人 · 均 {e.avg_km.toFixed(1)}K · 配速 {fmtPace(e.avg_pace_s)}/km
                  </div>
                </div>
                <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--fug)' }}>{metric(e)}</div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '50px 20px', fontSize: 13.5, color }}>{children}</div>
}

const backBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0,
}
const myBanner: React.CSSProperties = {
  background: 'var(--bg-1)', border: '1px solid var(--fug)', borderRadius: 16, padding: 16,
}
