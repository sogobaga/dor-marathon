'use client'

// 遊戲化角色數值（第 21 套，參考 RO 素質系統）：只有 VVIP／白名單管理者能看到本頁（入口在
// MemberPanel「🎮 角色」，由 dashboard.rpg_entry 控管），本頁本身也直接打 /rpg/me，若後端判定
// 沒資格會回 403 ——雙保險，不只信前端的入口判斷（見任務決策 D3/D8）。
import { useCallback, useEffect, useState } from 'react'
import { rpgApi, type RpgMe, type RpgStatKey } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import { STAT_META, DERIVED_META, resistLabel } from '@/lib/rpgMeta'

export default function CharacterScreen({ onBack }: { onBack: () => void }) {
  const [data, setData] = useState<RpgMe | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState('')
  const [actionErr, setActionErr] = useState('')
  const [flash, setFlash] = useState('')
  const [busyStat, setBusyStat] = useState<RpgStatKey | null>(null) // 配點請求進行中鎖住該列按鈕，避免連點超花

  const load = useCallback(() => {
    if (!getUserToken()) { setLoading(false); setLoadErr('請先登入'); return }
    setLoading(true)
    withUserAuth((t) => rpgApi.me(t))
      .then((r) => { setData(r); setLoadErr('') })
      .catch((e: any) => setLoadErr(e?.status === 403 ? '此功能尚未開放給你的帳號' : e?.message || '載入失敗'))
      .finally(() => setLoading(false))
  }, [])
  useEffect(() => { load() }, [load])

  async function allocate(stat: RpgStatKey, points: number) {
    if (!getUserToken() || busyStat) return
    setBusyStat(stat)
    setActionErr('')
    try {
      const r = await withUserAuth((t) => rpgApi.allocate(t, { stat, points }))
      setData(r)
      const label = STAT_META.find((s) => s.key === stat)?.label ?? stat
      setFlash(`${label} +${points}`)
      window.setTimeout(() => setFlash(''), 1800)
    } catch (e: any) {
      setActionErr(e?.message || '配點失敗，請稍後再試')
      window.setTimeout(() => setActionErr(''), 3500)
    } finally {
      setBusyStat(null)
    }
  }

  const ch = data?.character

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>🎮 角色</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>基本素質配點</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '14px 18px 28px' }}>
        {loading && <Hint>載入中…</Hint>}
        {!loading && (!data || !data.enabled || !ch) && <Hint>{loadErr || '此功能尚未開放給你的帳號。'}</Hint>}

        {!loading && ch && (
          <>
            {actionErr && <div style={errBanner}>{actionErr}</div>}
            {flash && <div style={okBanner}>✓ {flash}</div>}

            {/* Base Lv / Job Lv */}
            <div style={{ display: 'flex', gap: 16, fontSize: 13.5, fontWeight: 800, color: 'var(--fug)', marginBottom: 12 }}>
              <span>Base Lv. {ch.base_level}</span>
              <span>Job Lv. {ch.job_level}</span>
            </div>

            {/* HP/MP（上限值；本階段無戰鬥消耗機制，顯示滿條） */}
            <StatBar label="生命值 HP" value={ch.max_hp} color="#f4623a" />
            <StatBar label="魔力值 MP" value={ch.max_mp} color="#3a8ff4" />

            {/* 素質配點 */}
            <div style={{ marginTop: 20, display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' }}>
              <h2 style={{ margin: 0, fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>基本素質</h2>
              <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--gold)' }}>可配點數 {ch.free_points}</span>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
              {STAT_META.map((m) => {
                const val = ch.stats[m.key] ?? 0
                const cost = ch.next_cost[m.key]
                const atMax = cost == null
                const can1 = !atMax && cost! <= ch.free_points
                // 加點成本隨數值遞增（每 cost_step_every 點调高一階，該係數只在後台「參數設定」
                // 可調，會員端 /rpg/me 沒有回傳），前端算不出精確的 5 點總價，因此不猜——只要
                // 買得起下一點就先讓按鈕看起來能按，真正的把關永遠是伺服器那筆交易（超花會被拒絕，
                // 並顯示下方的 actionErr 橫幅），比「用下界估計导致明明變灰卻還是被拒」更誠實。
                const can5 = !atMax && cost! <= ch.free_points
                const busy = busyStat === m.key
                return (
                  <div key={m.key} style={statRow}>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
                        <span style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>{m.label}</span>
                        <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{m.abbr}</span>
                      </div>
                      <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginTop: 2, lineHeight: 1.4 }}>{m.short}</div>
                    </div>
                    <div style={{ fontSize: 20, fontWeight: 900, color: 'var(--fug)', fontVariantNumeric: 'tabular-nums', width: 34, textAlign: 'center', flexShrink: 0 }}>{val}</div>
                    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 4, flexShrink: 0 }}>
                      <span style={{ fontSize: 10, color: 'var(--tx-faint)', whiteSpace: 'nowrap' }}>{atMax ? '已達上限' : `下一點 ${cost}`}</span>
                      <div style={{ display: 'flex', gap: 4 }}>
                        <button disabled={!can1 || busy} onClick={() => allocate(m.key, 1)} style={{ ...plusBtn, opacity: can1 && !busy ? 1 : 0.4, cursor: can1 && !busy ? 'pointer' : 'default' }}>+1</button>
                        <button disabled={!can5 || busy} onClick={() => allocate(m.key, 5)} style={{ ...plusBtn, opacity: can5 && !busy ? 1 : 0.4, cursor: can5 && !busy ? 'pointer' : 'default' }}>+5</button>
                      </div>
                    </div>
                  </div>
                )
              })}
            </div>

            {/* 衍生數值 */}
            <h2 style={{ margin: '22px 0 8px', fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>衍生數值</h2>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
              {DERIVED_META.map((d) => (
                <div key={d.key} style={derivedCell}>
                  <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>{d.label} <span style={{ color: 'var(--tx-faint)' }}>{d.abbr}</span></div>
                  <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
                    {fmtNum(ch.derived[d.key] as number)}{d.suffix ?? ''}
                  </div>
                </div>
              ))}
            </div>

            {!!ch.derived.resists && Object.keys(ch.derived.resists).length > 0 && (
              <div style={{ marginTop: 12 }}>
                <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginBottom: 4 }}>狀態抗性</div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 12px' }}>
                  {Object.entries(ch.derived.resists).map(([k, v]) => (
                    <span key={k} style={{ fontSize: 11.5 }}>
                      <b style={{ color: 'var(--tx)' }}>{resistLabel(k)}</b>
                      <span style={{ color: 'var(--tx-dim)', marginLeft: 4 }}>{fmtNum(v)}%</span>
                    </span>
                  ))}
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}

function StatBar({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div style={{ marginTop: 8 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, fontWeight: 700, color: 'var(--tx-dim)' }}>
        <span>{label}</span>
        <span style={{ color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>{value}</span>
      </div>
      <div style={{ height: 9, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 3 }}>
        <div style={{ height: '100%', width: '100%', background: color, borderRadius: 999 }} />
      </div>
    </div>
  )
}

function Hint({ children }: { children: React.ReactNode }) {
  return <div style={{ textAlign: 'center', color: 'var(--tx-dim)', fontSize: 13, padding: '40px 0' }}>{children}</div>
}

function fmtNum(n: number): string {
  if (n == null || isNaN(n)) return '0'
  return Number.isInteger(n) ? String(n) : n.toFixed(1)
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 13, padding: 0, cursor: 'pointer', fontFamily: 'inherit' }
const statRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }
const plusBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 7, padding: '4px 9px', fontSize: 11.5, fontWeight: 800, fontFamily: 'inherit' }
const derivedCell: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 10, padding: '8px 10px' }
const errBanner: React.CSSProperties = { background: 'rgba(244,98,58,.12)', color: '#f4623a', border: '1px solid rgba(244,98,58,.3)', borderRadius: 10, padding: '8px 12px', fontSize: 12.5, marginBottom: 10 }
const okBanner: React.CSSProperties = { background: 'rgba(45,212,150,.12)', color: 'var(--fug)', border: '1px solid rgba(45,212,150,.3)', borderRadius: 10, padding: '8px 12px', fontSize: 12.5, marginBottom: 10 }
