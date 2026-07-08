'use client'

import useSWR from 'swr'
import { exploreApi, type ExploreBoss } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'

// 城市探索：找打卡點關主。列出各關主(地點/難度/狀態)，玩家到現場用 GPS 追蹤頁打卡→挑戰(Phase 3)。
export default function ExploreScreen({ onBack, onOpenTrack }: { onBack: () => void; onOpenTrack?: () => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data } = useSWR(
    uid && getUserToken() ? ['explore-list', uid] : null,
    () => withUserAuth((t) => exploreApi.list(t)).then((r) => r.bosses),
  )
  const bosses = (data ?? null) as ExploreBoss[] | null

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>城市探索</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '2px 2px 12px', lineHeight: 1.7 }}>
          全台各地都有守護打卡點的「關主」。到現場、在「GPS 跑步追蹤」頁打卡即可接受挑戰；3★ 完成就能取得關主卡片。
        </p>
        {bosses === null ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : bosses.length === 0 ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>目前尚無探索點<br /><span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>（後台新增關主後即會顯示）</span></div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {bosses.map((b) => {
              const st = b.stars ?? 0
              const status = b.card_obtained ? { t: '✓ 已取得卡片', c: 'var(--fug)' }
                : st > 0 ? { t: `已挑戰 ${st}★（3★ 得卡）`, c: 'var(--gold)' }
                : { t: '未挑戰', c: 'var(--tx-dim)' }
              return (
                <div key={b.id} style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
                  {b.scene_image_url && <img src={b.scene_image_url} alt="" style={{ width: '100%', aspectRatio: '16 / 9', objectFit: 'cover', display: 'block' }} />}
                  <div style={{ padding: '12px 14px' }}>
                    <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
                      <span style={{ fontSize: 15.5, fontWeight: 900, color: 'var(--tx)' }}>{b.name}</span>
                      <span style={{ fontSize: 11.5, color: 'var(--gold)', letterSpacing: 1 }}>{'★'.repeat(Math.max(0, b.difficulty_stars))}</span>
                      <span style={{ marginLeft: 'auto', fontSize: 12, fontWeight: 800, color: status.c }}>{status.t}</span>
                    </div>
                    {b.title && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>{b.title}</div>}
                    <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
                      <span style={chip}>📍 {b.region}</span>
                      <span style={chip}>{b.place}</span>
                      {b.workout_label && <span style={chip}>{b.workout_label}</span>}
                      <span style={{ ...chip, color: 'var(--gold)' }}>挑戰 {b.difficulty_stars * 10} DP</span>
                    </div>
                    <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.6 }}>到「{b.place}」附近，於 GPS 追蹤頁打卡即可接受挑戰。</div>
                  </div>
                </div>
              )
            })}
          </div>
        )}
        {onOpenTrack && (
          <button onClick={onOpenTrack} style={{ marginTop: 16, width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 12, padding: '13px', fontSize: 15, cursor: 'pointer', fontFamily: 'inherit' }}>▶ 前往 GPS 跑步追蹤打卡</button>
        )}
      </div>
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const chip: React.CSSProperties = { fontSize: 10.5, fontWeight: 600, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 6, padding: '2px 8px' }
