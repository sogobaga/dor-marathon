'use client'

import { useEffect, useState } from 'react'
import type { EventDef } from '@/lib/api'
import DpCoin from './DpCoin'

export type ActiveEvent = { def: EventDef; occId: string; triggerD: number; triggerT: number; deadline: number }
export type EventResult = { status: 'completed' | 'failed'; def: EventDef; reward_exp: number; reward_dp: number }

function goalText(def: EventDef): string {
  const p = def.completion_params
  if (def.completion_type === 'move_more') return `${Math.round(p.limit_s ?? 0)} 秒內再移動 ${Math.round(p.target_m ?? 0)} 公尺`
  if (def.completion_type === 'move_less') return `維持 ${Math.round(p.limit_s ?? 0)} 秒，移動不超過 ${Math.round(p.max_m ?? 0)} 公尺`
  return ''
}

// 進行中事件：夾在地圖與數據之間、常駐可見的橫幅（不擋畫面、不能被隨手關掉）
export function EventBanner({ active, moved }: { active: ActiveEvent; moved: number }) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 500); return () => clearInterval(t) }, [])
  const remain = Math.max(0, Math.ceil((active.deadline - now) / 1000))
  const def = active.def
  const target = def.completion_type === 'move_more' ? (def.completion_params.target_m ?? 0) : 0
  const pct = target > 0 ? Math.max(0, Math.min(100, (moved / target) * 100)) : 0

  return (
    <div style={{ ...banner, borderColor: 'rgba(255,194,75,.5)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 10 }}>
        <span style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--gold)', fontWeight: 800 }}>⚡ 事件任務</span>
        <span style={{ fontSize: 20, fontWeight: 900, color: remain <= 10 ? 'var(--hunt)' : 'var(--gold)', fontVariantNumeric: 'tabular-nums' }}>{remain}s</span>
      </div>
      <div style={{ fontSize: 14.5, fontWeight: 700, color: 'var(--tx)', marginTop: 4, lineHeight: 1.5 }}>{def.message || def.name}</div>
      <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4 }}>目標：{goalText(def)}</div>
      {target > 0 && (
        <>
          <div style={barOuter}><div style={{ ...barInner, width: `${pct}%` }} /></div>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', textAlign: 'right', marginTop: 3 }}>已移動 {Math.round(moved)} / {Math.round(target)} m</div>
        </>
      )}
    </div>
  )
}

// 完成/失敗結果：同樣以「內嵌橫幅」呈現（不是彈窗），需按「收下」或約 12 秒後才收起
export function EventResultBanner({ result, onClose }: { result: EventResult; onClose: () => void }) {
  useEffect(() => { const t = setTimeout(onClose, 12000); return () => clearTimeout(t) }, [onClose])
  const ok = result.status === 'completed'
  const hasReward = result.reward_exp > 0 || result.reward_dp > 0
  return (
    <div style={{
      ...banner,
      borderColor: ok ? 'rgba(70,227,160,.5)' : 'rgba(255,90,90,.4)',
      background: ok ? 'linear-gradient(180deg, rgba(70,227,160,.15), rgba(70,227,160,.05))' : 'linear-gradient(180deg, rgba(255,90,90,.13), rgba(255,90,90,.04))',
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
        <span style={{ fontSize: 16, fontWeight: 900, color: ok ? 'var(--fug)' : 'var(--hunt)' }}>{ok ? '🎉 任務完成！' : '🐾 任務失敗'}</span>
        <button onClick={onClose} style={{ background: 'rgba(255,255,255,.08)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 14px', color: 'var(--tx)', fontSize: 12.5, cursor: 'pointer', flexShrink: 0 }}>收下</button>
      </div>
      <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 3 }}>{result.def.name}</div>
      {ok && hasReward && (
        <div style={{ display: 'flex', gap: 14, alignItems: 'center', marginTop: 8 }}>
          {result.reward_exp > 0 && <span style={{ fontSize: 18, fontWeight: 900, color: 'var(--gold)' }}>+{result.reward_exp} EXP</span>}
          {result.reward_dp > 0 && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: 18, fontWeight: 900, color: '#FFD24D' }}><DpCoin size={18} />+{result.reward_dp}</span>}
        </div>
      )}
      {!ok && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 4 }}>沒關係，下次加油！</div>}
    </div>
  )
}

const banner: React.CSSProperties = { margin: '10px 16px 0', background: 'linear-gradient(180deg, rgba(255,194,75,.12), rgba(255,194,75,.05))', border: '1px solid rgba(255,194,75,.45)', borderRadius: 12, padding: '12px 14px', boxShadow: '0 4px 20px rgba(0,0,0,.3)' }
const barOuter: React.CSSProperties = { height: 8, borderRadius: 999, background: 'rgba(255,255,255,.08)', overflow: 'hidden', marginTop: 8 }
const barInner: React.CSSProperties = { height: '100%', borderRadius: 999, background: 'linear-gradient(90deg,#FFD24D,#FFC24B)', transition: 'width .3s' }
