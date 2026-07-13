'use client'

import { useEffect, useState } from 'react'
import { exploreApi, followApi, type ExploreRankRow } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'

// 關主挑戰者排行覆蓋層：時間榜（最短完成時間，前 100）+ 追蹤。可在 /track 或探索頁上開啟。
export default function BossRankingPanel({ bossId, bossName, onClose }: {
  bossId: string
  bossName: string
  onClose: () => void
}) {
  const [data, setData] = useState<{ ranking: ExploreRankRow[]; my_rank: number } | null>(null)
  const [err, setErr] = useState(false)
  const [override, setOverride] = useState<Record<string, boolean>>({})

  useEffect(() => {
    let alive = true
    if (!getUserToken()) { setErr(true); return }
    withUserAuth((t) => exploreApi.ranking(t, bossId))
      .then((r) => { if (alive) setData(r) })
      .catch(() => { if (alive) setErr(true) })
    return () => { alive = false }
  }, [bossId])

  const following = (r: ExploreRankRow) => override[r.user_id] ?? r.is_following
  async function toggle(r: ExploreRankRow) {
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

  const rows = data?.ranking ?? []

  return (
    <div data-skin="default" style={{ position: 'fixed', inset: 0, zIndex: 3250, background: 'rgba(4,8,6,.82)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 16 }}>
      <div style={{ width: '100%', maxWidth: 400, maxHeight: '90dvh', display: 'flex', flexDirection: 'column', background: '#0b0e13', border: '1px solid var(--gold)', borderRadius: 18, boxShadow: '0 16px 50px rgba(0,0,0,.7)', overflow: 'hidden' }}>
        <div style={{ padding: '15px 16px 12px', borderBottom: '1px solid var(--line-2)' }}>
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 16, fontWeight: 900, color: '#fff' }}>🏆 挑戰者排行</div>
              <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>{bossName} · 最短完成時間 · 前 100 名</div>
            </div>
            <button onClick={onClose} style={{ background: 'transparent', border: 'none', color: 'var(--tx-dim)', fontSize: 22, lineHeight: 1, cursor: 'pointer', padding: 0, flexShrink: 0 }}>×</button>
          </div>
          {data && data.my_rank > 0 && (
            <div style={{ marginTop: 8, fontSize: 12, color: 'var(--gold)', fontWeight: 800 }}>你的名次：第 {data.my_rank} 名</div>
          )}
        </div>

        <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '12px 14px 16px' }}>
          {err ? (
            <Hint>無法載入排行</Hint>
          ) : !data ? (
            <Hint>載入排行…</Hint>
          ) : rows.length === 0 ? (
            <Hint>尚無挑戰者，成為第一個吧！</Hint>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {rows.map((r) => (
                <div key={r.user_id} style={{
                  display: 'flex', alignItems: 'center', gap: 10, padding: '9px 11px', borderRadius: 12,
                  background: r.is_me ? 'rgba(45,212,150,.12)' : 'rgba(255,255,255,.04)',
                  border: r.is_me ? '1px solid var(--fug)' : '1px solid var(--line-2)',
                }}>
                  <div style={{ width: 24, textAlign: 'center', fontWeight: 900, fontSize: 14, color: r.rank <= 3 ? 'var(--gold)' : 'var(--tx-faint)' }}>{r.rank}</div>
                  <Avatar url={r.avatar_url} name={r.nickname} />
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 14, fontWeight: 700, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {r.title && <span style={{ color: 'var(--gold)', fontWeight: 800, marginRight: 5 }}>{r.title}</span>}{r.nickname}{r.is_me ? '（我）' : ''}
                    </div>
                    <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{r.stars > 0 ? <span style={{ color: 'var(--gold)', letterSpacing: 1 }}>{'★'.repeat(r.stars)}</span> : null} {fmtDate(r.completed_at)}</div>
                  </div>
                  <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--gold)', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}>{fmtT(r.best_time_s)}</div>
                  {!r.is_me && (
                    <button onClick={() => toggle(r)} style={following(r) ? followingBtn : followBtn}>
                      {following(r) ? '追蹤中' : '＋追蹤'}
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function Avatar({ url, name }: { url: string; name: string }) {
  if (url) return <img src={url} alt={name} style={{ width: 32, height: 32, borderRadius: '50%', objectFit: 'cover', flexShrink: 0 }} />
  return (
    <div style={{ width: 32, height: 32, borderRadius: '50%', flexShrink: 0, background: 'var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 13, fontWeight: 800, color: 'var(--tx-dim)' }}>
      {(name || '?').slice(0, 1)}
    </div>
  )
}

function fmtDate(iso?: string) {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} 完成`
}

function fmtT(s: number) {
  if (!s) return '—'
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}` : `${m}:${String(sec).padStart(2, '0')}`
}

function Hint({ children }: { children: React.ReactNode }) {
  return <div style={{ textAlign: 'center', padding: '44px 20px', fontSize: 13.5, color: 'var(--tx-dim)' }}>{children}</div>
}

const followBtn: React.CSSProperties = { flexShrink: 0, background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 999, padding: '5px 11px', fontSize: 12, fontWeight: 800, cursor: 'pointer', fontFamily: 'inherit' }
const followingBtn: React.CSSProperties = { flexShrink: 0, background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '5px 11px', fontSize: 12, cursor: 'pointer', fontFamily: 'inherit' }
