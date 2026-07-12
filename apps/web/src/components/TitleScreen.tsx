'use client'

// PB探索（稱號系統）：所有稱號打亂成一面牆（不分類、不顯示數量/取得數，隱藏取得規律）。
// 未解鎖顯示「？？？？？？？？」；已解鎖依 tier 由樸素到金光；可選一個展示（再點取消），會出現在各排行榜名字前。
import { useEffect, useState } from 'react'
import { titleApi, type TitleItem } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'

type Tone = { color: string; border: string; bg: string; glow?: boolean }
function tierTone(tier: number, earned: boolean): Tone {
  if (!earned) return { color: 'var(--tx-faint)', border: 'var(--line)', bg: 'var(--bg-1)' }
  return ([
    { color: 'var(--tx)', border: 'var(--line-2)', bg: 'var(--bg-2)' },
    { color: '#63a9ff', border: '#3a7bd0', bg: 'rgba(99,169,255,.12)' },
    { color: '#2de59a', border: '#1fa576', bg: 'rgba(45,229,154,.12)' },
    { color: '#c77dff', border: '#9a4dd0', bg: 'rgba(199,125,255,.14)' },
    { color: '#ffb24d', border: '#e08a1a', bg: 'rgba(255,178,77,.14)' },
    { color: '#ffd24d', border: '#ffd24d', bg: 'rgba(255,210,77,.18)', glow: true },
  ][Math.max(0, Math.min(5, tier - 1))])
}
// 依 code 的雜湊做穩定打亂（每次載入順序固定但無規律，隱藏「怎麼取得」）
function scramble(code: string): number {
  let x = 0
  for (let i = 0; i < code.length; i++) x = (x * 131 + code.charCodeAt(i) * 2654435761) | 0
  return x
}

export default function TitleScreen({ onBack }: { onBack: () => void }) {
  const [titles, setTitles] = useState<TitleItem[]>([])
  const [displayed, setDisplayed] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    if (!getUserToken()) return
    withUserAuth((t) => titleApi.list(t))
      .then((r) => { setTitles(r.titles); setDisplayed(r.displayed); setLoaded(true) })
      .catch(() => setErr('載入失敗，請稍後再試'))
  }, [])

  async function choose(code: string) {
    if (busy) return
    const next = displayed === code ? '' : code
    const prev = displayed
    setDisplayed(next); setBusy(true)
    try { await withUserAuth((t) => titleApi.display(t, next)) }
    catch { setDisplayed(prev) }
    finally { setBusy(false) }
  }

  const earnedCount = titles.filter((t) => t.earned).length
  const displayedName = titles.find((t) => t.code === displayed && t.earned)?.name || ''
  const ordered = titles.slice().sort((a, b) => scramble(a.code) - scramble(b.code))

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>PB探索</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        {/* 展示中 */}
        <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: '12px 14px', marginBottom: 14 }}>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', letterSpacing: '.1em' }}>目前展示中的稱號</div>
          <div style={{ fontSize: 17, fontWeight: 900, marginTop: 4, color: displayedName ? 'var(--gold)' : 'var(--tx-dim)' }}>
            {displayedName || '未選擇稱號'}
          </div>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>已解鎖 {earnedCount} 個 · 點下方已解鎖的稱號可設為展示，會顯示在排行榜名字前</div>
        </div>

        {err && <div style={{ color: 'var(--hunt)', fontSize: 13, padding: '10px 2px' }}>{err}</div>}
        {!loaded && !err && <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>}

        {/* 全部稱號：打亂成一面牆，不分類、不顯示數量 */}
        {loaded && (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 8 }}>
            {ordered.map((t) => {
              const tone = tierTone(t.tier, t.earned)
              const isShown = t.earned && t.code === displayed
              return (
                <button
                  key={t.code}
                  onClick={() => t.earned && choose(t.code)}
                  disabled={!t.earned}
                  style={{
                    textAlign: 'left', border: `1px solid ${isShown ? 'var(--gold)' : tone.border}`,
                    background: isShown ? 'rgba(255,210,77,.16)' : tone.bg, borderRadius: 12, padding: '11px 12px',
                    cursor: t.earned ? 'pointer' : 'default', fontFamily: 'inherit', minHeight: 58,
                    boxShadow: tone.glow ? '0 0 14px rgba(255,210,77,.35)' : 'none',
                    display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 3,
                  }}
                >
                  <span style={{ fontSize: 14, fontWeight: 900, color: tone.color, letterSpacing: t.earned ? '.02em' : '.15em', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {t.earned ? t.name : '？？？？？？？？'}
                  </span>
                  {isShown && <span style={{ fontSize: 10.5, fontWeight: 800, color: 'var(--gold)' }}>展示中 ✓</span>}
                  {t.earned && !isShown && <span style={{ fontSize: 10, color: 'var(--tx-faint)' }}>點擊設為展示</span>}
                  {!t.earned && <span style={{ fontSize: 10, color: 'var(--tx-faint)' }}>尚未解鎖</span>}
                </button>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
