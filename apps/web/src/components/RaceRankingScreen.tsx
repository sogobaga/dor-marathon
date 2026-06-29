'use client'

import useSWR from 'swr'
import { useState } from 'react'
import { racesApi, followApi, type Race, type StandingRank, type LeaderboardRow } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'

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
function fmtDateTime(iso?: string) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
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

// RankingBody 排名內容，供賽事資訊頁「排名」頁籤重用。
// 競賽模式：分組榜；其他（一般）模式：個人完成排名。
export function RankingBody({ race }: { race: Race }) {
  if (race.event_mode !== 'competition') return <GeneralLeaderboard race={race} />
  return <CompetitionStandings race={race} />
}

function CompetitionStandings({ race }: { race: Race }) {
  const { data, error, isLoading } = useSWR(['standings', race.id], () => racesApi.standings(race.id), { refreshInterval: 30000 })
  const isCompetition = race.event_mode === 'competition'
  return (
    <div>
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
          <RankList title="累積里程榜" subtitle="各分組總累積里程" entries={data.by_cumulative}
            metric={(e) => `${e.total_km.toFixed(1)} K`} highlightId={data.my_group?.group_id} />
          {data.goal_type === 'distance' && (
            <RankList title="完成時間榜" subtitle="完成指定里程的累計總時間" entries={data.by_finish_time}
              metric={(e) => fmtDuration(e.finish_total_s)} highlightId={data.my_group?.group_id} />
          )}
        </div>
      )}
    </div>
  )
}

// GeneralLeaderboard 一般模式個人完成排名（完成時間榜 + 累計時間榜 + 追蹤鈕）
function GeneralLeaderboard({ race }: { race: Race }) {
  const token = getUserToken() || undefined
  const { data, isLoading } = useSWR(['leaderboard', race.id], () => racesApi.leaderboard(race.id, token), { refreshInterval: 30000 })
  const [override, setOverride] = useState<Record<string, boolean>>({})
  const lb = data?.leaderboard
  if (isLoading || !lb) return <Hint>載入排名…</Hint>

  const following = (r: LeaderboardRow) => override[r.user_id] ?? r.is_following
  async function toggle(r: LeaderboardRow) {
    const t = getUserToken()
    if (!t) return
    const cur = following(r)
    setOverride((o) => ({ ...o, [r.user_id]: !cur }))
    try {
      if (cur) await followApi.unfollow(t, r.user_id)
      else await followApi.follow(t, r.user_id)
    } catch {
      setOverride((o) => ({ ...o, [r.user_id]: cur }))
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <div style={{ ...myBanner, borderColor: 'var(--line)' }}>
        <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>
          已完成 <b style={{ color: 'var(--fug)', fontSize: 17 }}>{lb.finished_count}</b> / 報名 {lb.total_count} 人
        </div>
      </div>
      <LbList title="完成時間榜" subtitle="活動開始後最快完成" rows={lb.by_completion}
        metric={(r) => fmtDateTime(r.completion_at)} following={following} onToggle={toggle} loggedIn={!!token} />
      <LbList title="累計時間榜" subtitle="完成所花費的總時間最短" rows={lb.by_total_time}
        metric={(r) => fmtDuration(r.total_time_s)} following={following} onToggle={toggle} loggedIn={!!token} />
    </div>
  )
}

function LbList({
  title, subtitle, rows, metric, following, onToggle, loggedIn,
}: {
  title: string
  subtitle: string
  rows: LeaderboardRow[]
  metric: (r: LeaderboardRow) => string
  following: (r: LeaderboardRow) => boolean
  onToggle: (r: LeaderboardRow) => void
  loggedIn: boolean
}) {
  return (
    <div>
      <div style={{ marginBottom: 8 }}>
        <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>{title}</div>
        <div style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>{subtitle}</div>
      </div>
      {rows.length === 0 ? (
        <Hint>尚無完成者</Hint>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {rows.map((r) => (
            <div key={r.user_id} style={{
              display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 12,
              background: r.is_me ? 'rgba(45,212,150,.1)' : 'var(--bg-1)',
              border: r.is_me ? '1px solid var(--fug)' : '1px solid var(--line)',
            }}>
              <div style={{ width: 24, textAlign: 'center', fontWeight: 800, color: r.rank <= 3 ? 'var(--gold)' : 'var(--tx-dim)' }}>{r.rank}</div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {r.nickname}{r.is_me ? '（我）' : ''}
                </div>
                <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{r.group_name || ''} · {r.distance_km.toFixed(1)}K</div>
              </div>
              <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--fug)', whiteSpace: 'nowrap' }}>{metric(r)}</div>
              {loggedIn && !r.is_me && (
                <button onClick={() => onToggle(r)} style={following(r) ? followingBtn : followBtn}>
                  {following(r) ? '追蹤中' : '＋追蹤'}
                </button>
              )}
            </div>
          ))}
        </div>
      )}
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
const followBtn: React.CSSProperties = {
  flexShrink: 0, background: 'var(--fug)', color: '#05140e', border: 'none', borderRadius: 999,
  padding: '5px 12px', fontSize: 12, fontWeight: 700, cursor: 'pointer',
}
const followingBtn: React.CSSProperties = {
  flexShrink: 0, background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)',
  borderRadius: 999, padding: '5px 12px', fontSize: 12, cursor: 'pointer',
}
