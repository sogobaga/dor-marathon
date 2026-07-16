'use client'

// 成就探索：頂部月曆里程熱力圖（可左右滑月、頁點指示，適合截圖分享）＋ 下方所有累積數值牆（多巴胺）。
import { useEffect, useRef, useState } from 'react'
import { achievementApi, type AchievementStats, type AchievementCalendar } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'

const WK = ['日', '一', '二', '三', '四', '五', '六']
function ym(d: Date) { return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}` }
function shiftMonth(key: string, delta: number) { const [y, m] = key.split('-').map(Number); return ym(new Date(y, m - 1 + delta, 1)) }
function monthsDiff(a: string, b: string) { const [ay, am] = a.split('-').map(Number); const [by, bm] = b.split('-').map(Number); return (by - ay) * 12 + (bm - am) }
function fmtHM(sec: number) { const h = Math.floor(sec / 3600); const m = Math.round((sec % 3600) / 60); return h > 0 ? `${h}時${m}分` : `${m}分` }

export default function AchievementScreen({ onBack }: { onBack: () => void }) {
  const nowMonth = ym(new Date())
  const [stats, setStats] = useState<AchievementStats | null>(null)
  const [month, setMonth] = useState(nowMonth)
  const [cal, setCal] = useState<AchievementCalendar | null>(null)
  const [bounce, setBounce] = useState(0) // 右滑到當月的橡皮筋回饋
  const touchPt = useRef<{ x: number; y: number } | null>(null)

  useEffect(() => { if (getUserToken()) withUserAuth((t) => achievementApi.stats(t)).then(setStats).catch(() => {}) }, [])
  useEffect(() => {
    if (!getUserToken()) return
    setCal(null)
    withUserAuth((t) => achievementApi.calendar(t, month)).then(setCal).catch(() => {})
  }, [month])

  const canNext = monthsDiff(month, nowMonth) > 0
  function go(delta: number) {
    if (delta > 0 && !canNext) { setBounce(1); setTimeout(() => setBounce(0), 240); return } // 到當月，回彈不換
    setMonth((m) => shiftMonth(m, delta))
  }
  function onTouchStart(e: React.TouchEvent) { touchPt.current = { x: e.touches[0].clientX, y: e.touches[0].clientY } }
  function onTouchEnd(e: React.TouchEvent) {
    const st = touchPt.current
    if (!st) return
    touchPt.current = null
    const dx = e.changedTouches[0].clientX - st.x
    const dy = e.changedTouches[0].clientY - st.y
    // 垂直捲動優先：水平位移要夠大、且明顯大於垂直位移，才算「換月滑動」（避免上下捲頁誤切月份）
    if (Math.abs(dx) < 45 || Math.abs(dx) < Math.abs(dy) * 1.5) return
    if (dx > 0) go(-1)         // 右滑 → 上個月
    else go(1)                 // 左滑 → 下個月
  }

  // 月曆格
  const [yy, mm] = month.split('-').map(Number)
  const first = new Date(yy, mm - 1, 1).getDay()
  const daysIn = new Date(yy, mm, 0).getDate()
  const kmByDay: Record<number, number> = {}
  let maxKm = 0
  ;(cal?.days ?? []).forEach((d) => { const day = Number(d.date.slice(8, 10)); kmByDay[day] = d.km; if (d.km > maxKm) maxKm = d.km })
  const cells: (number | null)[] = [...Array(first).fill(null), ...Array.from({ length: daysIn }, (_, i) => i + 1)]

  const offset = Math.max(0, Math.min(11, monthsDiff(month, nowMonth))) // 0=當月

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>成就探索</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 16px 28px' }}>
        {/* 月曆里程熱力圖 */}
        <div
          onTouchStart={onTouchStart} onTouchEnd={onTouchEnd}
          style={{ background: 'linear-gradient(160deg, var(--bg-1), var(--bg-2))', border: '1px solid var(--line)', borderRadius: 18, padding: '16px 16px 14px', transform: bounce ? 'translateX(-8px)' : 'none', transition: 'transform .12s' }}
        >
          <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', marginBottom: 12 }}>
            <button onClick={() => go(-1)} style={navBtn}>‹</button>
            <div style={{ textAlign: 'center' }}>
              <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', fontWeight: 700 }}>{yy} 年 {mm} 月 · 里程</div>
              <div style={{ fontSize: 30, fontWeight: 900, color: 'var(--fug)', fontVariantNumeric: 'tabular-nums', lineHeight: 1.15 }}>
                {(cal?.total_km ?? 0).toFixed(1)}<span style={{ fontSize: 15, marginLeft: 3, color: 'var(--tx-dim)' }}>K</span>
              </div>
            </div>
            <button onClick={() => go(1)} style={{ ...navBtn, opacity: canNext ? 1 : 0.3 }}>›</button>
          </div>
          {/* 星期列 */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7,1fr)', gap: 4, marginBottom: 4 }}>
            {WK.map((w) => <div key={w} style={{ textAlign: 'center', fontSize: 10, color: 'var(--tx-faint)' }}>{w}</div>)}
          </div>
          {/* 日格 */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7,1fr)', gap: 4 }}>
            {cells.map((day, i) => {
              if (day == null) return <div key={`b${i}`} />
              const km = kmByDay[day] || 0
              const intensity = maxKm > 0 && km > 0 ? 0.18 + 0.82 * (km / maxKm) : 0
              return (
                <div key={day} style={{ aspectRatio: '1', borderRadius: 8, background: km > 0 ? `rgba(45,229,154,${intensity.toFixed(2)})` : 'var(--bg-2)', border: '1px solid var(--line)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 1 }}>
                  <span style={{ fontSize: 10, color: km > 0 && intensity > 0.5 ? '#06281c' : 'var(--tx-faint)', fontWeight: 700 }}>{day}</span>
                  {km > 0 && <span style={{ fontSize: 8.5, color: intensity > 0.5 ? '#06281c' : 'var(--tx-dim)', fontWeight: 800 }}>{km.toFixed(1)}</span>}
                </div>
              )
            })}
          </div>
          {/* 頁點 */}
          <div style={{ display: 'flex', justifyContent: 'center', gap: 5, marginTop: 12 }}>
            {Array.from({ length: 12 }, (_, i) => (
              <span key={i} style={{ width: 6, height: 6, borderRadius: '50%', background: i === 11 - offset ? 'var(--fug)' : 'var(--line-2)' }} />
            ))}
          </div>
        </div>
        <div style={{ textAlign: 'center', fontSize: 10.5, color: 'var(--tx-faint)', margin: '8px 0 16px' }}>左右滑動或按 ‹ › 切換月份</div>

        {/* 數值牆 */}
        {stats && (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2,1fr)', gap: 10 }}>
            <Stat icon="🏁" label="單次最長距離" value={`${stats.single_max_km.toFixed(1)} K`} />
            <Stat icon="🛣️" label="累積總距離" value={`${stats.cum_km.toFixed(1)} K`} />
            <Stat icon="⏱️" label="單次最長時間" value={fmtHM(stats.single_max_sec)} />
            <Stat icon="⌛" label="累積運動時間" value={fmtHM(stats.cum_sec)} />
            <Stat icon="🔥" label="連續運動天數" value={`${stats.streak_days} 天`} />
            <Stat icon="🏃" label="運動次數" value={`${stats.activity_count}`} />
            <Stat icon="📍" label="打卡地點數" value={`${stats.checkin_count}`} />
            <Stat icon="⚔️" label="關主挑戰" value={`${stats.boss_count}`} sub={`★${stats.boss_s1} ★★${stats.boss_s2} ★★★${stats.boss_s3}`} />
            <Stat icon="🎯" label="個人任務完成" value={`${stats.personal_count}`} />
            <Stat icon="⭐" label="目前等級" value={`Lv.${stats.level}`} sub={stats.level_title} />
            <Stat icon="🎴" label="收集卡片" value={`${stats.card_count}`} />
            <Stat icon="💠" label="DP 幣" value={stats.dp.toLocaleString()} />
            <Stat icon="👣" label="追蹤中" value={`${stats.following}`} />
            <Stat icon="👥" label="粉絲" value={`${stats.followers}`} />
            <Stat icon="🏆" label="參與活動" value={`${stats.race_count}`} />
          </div>
        )}
      </div>
    </div>
  )
}

function Stat({ icon, label, value, sub }: { icon: string; label: string; value: string; sub?: string }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: '12px 13px' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{icon} {label}</div>
      <div style={{ fontSize: 21, fontWeight: 900, color: 'var(--tx)', marginTop: 3, fontVariantNumeric: 'tabular-nums' }}>{value}</div>
      {sub && <div style={{ fontSize: 10.5, color: 'var(--gold)', marginTop: 2, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{sub}</div>}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const navBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--tx)', borderRadius: 10, width: 34, height: 34, fontSize: 20, cursor: 'pointer', flexShrink: 0, lineHeight: 1 }
