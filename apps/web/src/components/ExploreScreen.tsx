'use client'

import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { exploreApi, type ExploreBoss } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import BossRankingPanel from '@/components/BossRankingPanel'

// region「臺北市·大安區」→ 縣市
const countyOf = (r: string) => (r || '').split('·')[0]
function havM(aLat: number, aLng: number, bLat: number, bLng: number) {
  if (!bLat && !bLng) return Infinity
  const R = 6371000, rad = Math.PI / 180
  const dLat = (bLat - aLat) * rad, dLng = (bLng - aLng) * rad
  const x = Math.sin(dLat / 2) ** 2 + Math.cos(aLat * rad) * Math.cos(bLat * rad) * Math.sin(dLng / 2) ** 2
  return R * 2 * Math.atan2(Math.sqrt(x), Math.sqrt(1 - x))
}
const fmtDist = (m: number) => (m < 1000 ? `${Math.round(m)}m` : `${(m / 1000).toFixed(1)}km`)

// 城市探索：找打卡點。未打卡前只顯示「地點」(神秘，保留收集/交換樂趣)；到現場用 GPS 追蹤打卡後才揭露背後關主
// (Scene 圖 + 名稱)，「打卡」按鈕切換成「挑戰」。關主資料由伺服器對未揭露者遮蔽(devtools 也看不到)。
export default function ExploreScreen({ onBack, onOpenTrack }: { onBack: () => void; onOpenTrack?: (bossId?: string) => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data } = useSWR(
    uid && getUserToken() ? ['explore-list', uid] : null,
    () => withUserAuth((t) => exploreApi.list(t)).then((r) => r.bosses),
  )
  const bosses = (data ?? null) as ExploreBoss[] | null
  const [rankingBoss, setRankingBoss] = useState<{ id: string; name: string } | null>(null)
  const [pos, setPos] = useState<{ lat: number; lng: number } | null>(null)
  const [county, setCounty] = useState('') // 縣市篩選（空＝全部）

  // 取一次目前位置（用於「越近排越上」；拒絕/失敗則維持原順序）
  useEffect(() => {
    if (typeof navigator === 'undefined' || !navigator.geolocation) return
    navigator.geolocation.getCurrentPosition(
      (p) => setPos({ lat: p.coords.latitude, lng: p.coords.longitude }),
      () => {},
      { enableHighAccuracy: false, timeout: 10000, maximumAge: 300000 },
    )
  }, [])

  const counties = useMemo(
    () => Array.from(new Set((bosses ?? []).map((b) => countyOf(b.region)).filter(Boolean))).sort(),
    [bosses],
  )
  // 依縣市篩選 → 依距離排序（最近在最上；未定位則維持伺服器順序）
  const shown = useMemo(() => {
    let list = (bosses ?? []).filter((b) => !county || countyOf(b.region) === county)
    if (pos) list = list.slice().sort((a, b) => havM(pos.lat, pos.lng, a.lat, a.lng) - havM(pos.lat, pos.lng, b.lat, b.lng))
    return list
  }, [bosses, county, pos])

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>城市探索</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '2px 2px 12px', lineHeight: 1.7 }}>
          全台各地藏著一個個打卡點。點「前往打卡」到 GPS 地圖，走到現場、在範圍內即可打卡揭曉（不必邊跑邊打卡）。
          {pos ? '（已依你的位置由近到遠排序）' : ''}
        </p>

        {/* 縣市篩選 */}
        {bosses && counties.length > 1 && (
          <div style={{ display: 'flex', gap: 6, overflowX: 'auto', padding: '0 0 12px', WebkitOverflowScrolling: 'touch' }}>
            <button onClick={() => setCounty('')} style={countyChip(county === '')}>全部</button>
            {counties.map((c) => <button key={c} onClick={() => setCounty(c)} style={countyChip(county === c)}>{c}</button>)}
          </div>
        )}

        {bosses === null ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : bosses.length === 0 ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>目前尚無探索點<br /><span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>（後台新增後即會顯示）</span></div>
        ) : shown.length === 0 ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>此縣市目前沒有打卡點</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {shown.map((b) => {
              const dm = pos ? havM(pos.lat, pos.lng, b.lat, b.lng) : null
              return b.discovered ? (
                // 已打卡揭露：Scene banner + 關主資訊 + 挑戰
                <RevealCard key={b.id} b={b} dist={dm} onChallenge={() => onOpenTrack?.(b.id)} onRanking={() => setRankingBoss({ id: b.id, name: b.name })} />
              ) : (
                // 未打卡：只顯示地點（神秘）
                <div key={b.id} style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: '14px' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                    <div style={mysteryIcon}>？</div>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontSize: 15.5, fontWeight: 900, color: 'var(--tx)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{b.place || '神秘打卡點'}</div>
                      <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>📍 {b.region || '未知地區'}{dm != null && dm !== Infinity ? ` · ${fmtDist(dm)}` : ''}</div>
                    </div>
                    <span style={{ fontSize: 11.5, color: 'var(--tx-faint)', flexShrink: 0 }}>未探索</span>
                  </div>
                  <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.6 }}>到「{b.place || '此地'}」附近，於 GPS 地圖上打卡即可揭曉（在範圍內就算成功）。</div>
                  {b.access_note && <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4, lineHeight: 1.6 }}>📍 開放：{b.access_note}</div>}
                  {onOpenTrack && <button onClick={() => onOpenTrack(b.id)} style={ghostFullBtn}>前往打卡</button>}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {rankingBoss && (
        <BossRankingPanel bossId={rankingBoss.id} bossName={rankingBoss.name} onClose={() => setRankingBoss(null)} />
      )}
    </div>
  )
}

function RevealCard({ b, dist, onChallenge, onRanking }: { b: ExploreBoss; dist?: number | null; onChallenge?: () => void; onRanking?: () => void }) {
  const st = b.stars ?? 0
  const status = b.card_obtained ? { t: '✓ 已取得卡片', c: 'var(--fug)' }
    : st > 0 ? { t: `已挑戰 ${st}★（3★ 得卡）`, c: 'var(--gold)' }
      : { t: '待挑戰', c: 'var(--tx-dim)' }
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
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
          {dist != null && dist !== Infinity && <span style={chip}>{fmtDist(dist)}</span>}
          <span style={chip}>{b.place}</span>
          {b.workout_label && <span style={chip}>{b.workout_label}</span>}
          <span style={{ ...chip, color: 'var(--gold)' }}>挑戰 {b.difficulty_stars * 10} DP</span>
        </div>
        {b.access_note && <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 6, lineHeight: 1.6 }}>📍 開放：{b.access_note}</div>}
        {!b.card_obtained && onChallenge && <button onClick={onChallenge} style={{ ...ghostFullBtn, background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', fontWeight: 800 }}>▶ 前往挑戰（到「{b.place}」打卡點）</button>}
        {b.card_obtained && <div style={{ fontSize: 11.5, color: 'var(--fug)', marginTop: 10, textAlign: 'center', fontWeight: 700 }}>已收服此關主 · 卡片已收藏</div>}
        {onRanking && <button onClick={onRanking} style={{ ...ghostFullBtn, marginTop: 8 }}>🏆 挑戰者排行</button>}
      </div>
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const chip: React.CSSProperties = { fontSize: 10.5, fontWeight: 600, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 6, padding: '2px 8px' }
const mysteryIcon: React.CSSProperties = { width: 48, height: 48, borderRadius: 10, background: 'var(--bg-2)', border: '1px dashed var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 24, fontWeight: 900, color: 'var(--tx-faint)', flexShrink: 0 }
const ghostFullBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px', fontSize: 13.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
function countyChip(active: boolean): React.CSSProperties {
  return { flexShrink: 0, border: '1px solid ' + (active ? 'var(--fug)' : 'var(--line-2)'), background: active ? 'var(--fug)' : 'var(--bg-2)', color: active ? 'var(--fug-ink)' : 'var(--tx-dim)', borderRadius: 999, padding: '5px 12px', fontSize: 12, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit', whiteSpace: 'nowrap' }
}
