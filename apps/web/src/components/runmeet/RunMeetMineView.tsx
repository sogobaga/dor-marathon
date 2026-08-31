'use client'

import useSWR from 'swr'
import { runMeetApi, type RunMeetCard } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { createBtnText } from '@/lib/runMeet'
import { MeetCard, emptyBox, ghostBtn, primaryBtn } from './ui'

// 我的團練：三段（我發起的／我參加的／申請中）。
// 已結束超過保留天數的團練會從探索消失，但成員仍能在這裡看到（資料不刪）。
export default function RunMeetMineView({
  onOpen, onCreate, onGoExplore, remaining,
}: {
  onOpen: (m: RunMeetCard) => void
  onCreate: () => void
  onGoExplore: () => void
  remaining: number
}) {
  const user = useUser()
  const uid = user?.id ?? 'guest'
  const { data, error, isLoading } = useSWR(
    getUserToken() ? ['run-meet-mine', uid] : null,
    () => withUserAuth((t) => runMeetApi.mine(t)),
  )

  if (isLoading) return <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
  if (error) return <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>

  const owned = data?.owned ?? []
  const joined = data?.joined ?? []
  const pending = data?.pending ?? []

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <Section title="我發起的" count={owned.length}>
        {owned.length === 0 ? (
          <div style={emptyBox}>
            <div style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>你還沒有發起過團練</div>
            <div style={{ fontSize: 12.5, marginTop: 6 }}>
              揪一場團練，找跑友一起練
            </div>
            <button onClick={onCreate} style={{ ...primaryBtn, width: 'auto', padding: '10px 20px', marginTop: 12 }}>{createBtnText(remaining)}</button>
          </div>
        ) : owned.map((m) => <MeetCard key={m.id} meet={m} onOpen={() => onOpen(m)} />)}
      </Section>

      <Section title="我參加的" count={joined.length}>
        {joined.length === 0 ? (
          <div style={emptyBox}>
            <div style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>還沒有參加任何團練</div>
            <div style={{ fontSize: 12.5, marginTop: 6 }}>去探索看看有沒有想跑的團</div>
            <button onClick={onGoExplore} style={{ ...ghostBtn, marginTop: 12 }}>去探索</button>
          </div>
        ) : joined.map((m) => <MeetCard key={m.id} meet={m} onOpen={() => onOpen(m)} />)}
      </Section>

      <Section title="申請中" count={pending.length}>
        {pending.length === 0 ? (
          <div style={emptyBox}><div style={{ fontSize: 13.5, color: 'var(--tx-dim)' }}>目前沒有審核中的申請</div></div>
        ) : pending.map((m) => <MeetCard key={m.id} meet={m} onOpen={() => onOpen(m)} />)}
      </Section>
    </div>
  )
}

function Section({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ fontSize: 13.5, fontWeight: 900, color: 'var(--tx)', marginBottom: 10 }}>
        {title} <span style={{ color: 'var(--tx-faint)', fontWeight: 700 }}>{count}</span>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>{children}</div>
    </div>
  )
}
