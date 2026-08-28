'use client'

import { useState } from 'react'
import useSWR from 'swr'
import { heroesApi, followApi, type HundredHero } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'
import FollowHeartButton from './shared/FollowHeartButton'

// 百里英雄榜：累積里程突破 100 公里的跑者，前 100 名（依 total_km desc）。公開可看（未登入也能瀏覽），
// 登入後才顯示追蹤鈕（is_self 不顯示，比照 RaceRankingScreen 的排行榜追蹤鈕慣例）。
export default function HundredHeroesScreen({ onBack }: { onBack: () => void }) {
  const token = getUserToken() || undefined
  const { data, isLoading } = useSWR(['hundred-heroes', token], () => heroesApi.hundred(token))
  const [override, setOverride] = useState<Record<string, boolean>>({})

  const heroes = data?.heroes ?? []
  const following = (h: HundredHero) => override[h.user_id] ?? h.is_following
  async function toggle(h: HundredHero) {
    const t = getUserToken()
    if (!t) return
    const cur = following(h)
    setOverride((o) => ({ ...o, [h.user_id]: !cur }))
    try {
      if (cur) await followApi.unfollow(t, h.user_id)
      else await followApi.follow(t, h.user_id)
    } catch {
      setOverride((o) => ({ ...o, [h.user_id]: cur }))
    }
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>🏅 百里英雄榜</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>累積里程突破 100 公里的跑者，前 100 名</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', overscrollBehavior: 'contain', padding: '14px 18px 28px' }}>
        {isLoading && <Hint>載入中…</Hint>}
        {!isLoading && heroes.length === 0 && <Hint>尚無跑者突破 100 公里，成為第一位英雄吧！</Hint>}
        {!isLoading && heroes.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {heroes.map((h, i) => (
              <div key={h.user_id} style={{
                display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px', borderRadius: 12,
                background: h.is_self ? 'rgba(45,212,150,.1)' : 'var(--bg-1)',
                border: h.is_self ? '1px solid var(--fug)' : '1px solid var(--line)',
              }}>
                <div style={{ width: 26, textAlign: 'center', fontWeight: 800, color: i < 3 ? 'var(--gold)' : 'var(--tx-dim)' }}>{i + 1}</div>
                <Avatar url={h.avatar_url} name={h.name} />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {h.title && <span style={{ color: 'var(--gold)', fontWeight: 800, marginRight: 5 }}>{h.title}</span>}{h.name}{h.is_self ? '（我）' : ''}
                  </div>
                </div>
                <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--fug)', whiteSpace: 'nowrap' }}>{h.total_km.toFixed(1)} K</div>
                {token && !h.is_self && (
                  <FollowHeartButton following={following(h)} onClick={() => toggle(h)} />
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

// Avatar 小頭像；無圖時以名稱首字當佔位（比照 RaceRankingScreen 慣例）
function Avatar({ url, name }: { url: string; name: string }) {
  if (url) return <img src={url} alt={name} style={{ width: 32, height: 32, borderRadius: '50%', objectFit: 'cover', flexShrink: 0 }} />
  return (
    <div style={{ width: 32, height: 32, borderRadius: '50%', flexShrink: 0, background: 'var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 13, fontWeight: 800, color: 'var(--tx-dim)' }}>
      {(name || '?').slice(0, 1)}
    </div>
  )
}

function Hint({ children }: { children: React.ReactNode }) {
  return <div style={{ textAlign: 'center', padding: '50px 20px', fontSize: 13.5, color: 'var(--tx-dim)' }}>{children}</div>
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
