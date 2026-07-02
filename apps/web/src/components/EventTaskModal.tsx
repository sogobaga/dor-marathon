'use client'

import { useEffect, useState } from 'react'
import type { EventDef } from '@/lib/api'
import DpCoin from './DpCoin'

export type ActiveEvent = { def: EventDef; occId: string; triggerD: number; triggerT: number; readyUntil: number; deadline: number; raceInstanceId?: string }
export type EventResult = { status: 'completed' | 'failed'; def: EventDef; reward_exp: number; reward_dp: number; stars?: number; pending?: boolean }

export function isInteractionType(ct: string): boolean { return ct === 'tap_burst' || ct === 'hold_press' || ct === 'swipe_charge' || ct === 'dodge_swipe' }

// 依跑者當下時間挑時段插圖：白天 06–17、黃昏 17–19、晚上 19–06；未設定該時段回退預設圖。
// 事件任務 / 多人事件邀請共用（欄位名一致）。
export function pickTimeImage(imgs: { image_url?: string; image_day_url?: string; image_dusk_url?: string; image_night_url?: string }): string {
  const h = new Date().getHours()
  const seg = (h >= 6 && h < 17) ? imgs.image_day_url : (h >= 17 && h < 19) ? imgs.image_dusk_url : imgs.image_night_url
  return seg || imgs.image_url || ''
}
export function pickEventImage(def: EventDef): string { return pickTimeImage(def) }

function goalText(def: EventDef): string {
  const p = def.completion_params
  const r = (k: string) => Math.round(p[k] ?? 0)
  switch (def.completion_type) {
    case 'move_more': return `${r('limit_s')} 秒內再移動 ${r('target_m')} 公尺`
    case 'move_less': return `維持 ${r('limit_s')} 秒，移動不超過 ${r('max_m')} 公尺`
    case 'hold_pace': return `維持 ${r('limit_s')} 秒不停下（每 ${r('check_s')} 秒至少移動 ${r('min_m')}m）`
    case 'sprint': return `${r('limit_s')} 秒內衝刺：任一 ${r('burst_s')} 秒移動 ≥ ${r('burst_m')}m`
    case 'negative_split': return `${r('limit_s')} 秒內後段加速：後半移動 ≥ 前半的 ${r('ratio_pct')}%`
    case 'tap_burst': return `${r('limit_s')} 秒內連續點擊 ${r('target_taps')} 次（物理攻擊）`
    case 'hold_press': return `按住螢幕 ${r('hold_s')} 秒（物理防禦）`
    case 'swipe_charge': return `${r('limit_s')} 秒內連續滑動蓄力（魔法攻擊）`
    case 'dodge_swipe': return `${r('limit_s')} 秒內連續滑動 ${r('target_swipes')} 次閃避`
  }
  return ''
}

// 進行中事件：夾在地圖與數據之間、常駐可見的橫幅（不擋畫面、不能被隨手關掉）
export function EventBanner({ active, moved }: { active: ActiveEvent; moved: number }) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 500); return () => clearInterval(t) }, [])
  const ready = now < active.readyUntil
  const readyRemain = Math.max(0, Math.ceil((active.readyUntil - now) / 1000))
  const remain = Math.max(0, Math.ceil((active.deadline - now) / 1000))
  const def = active.def
  const ct = def.completion_type
  const p = def.completion_params
  const isLess = ct === 'move_less'
  // 各完成型態的「進度條上限」：填滿＝達標（move_less 例外，填滿＝危險）。negative_split 無單一進度條。
  const limit = ct === 'move_less' ? (p.max_m ?? 0)
    : ct === 'move_more' ? (p.target_m ?? 0)
    : ct === 'sprint' ? (p.burst_m ?? 0)
    : ct === 'hold_pace' ? (p.min_m ?? 0)
    : 0
  const pct = limit > 0 ? Math.max(0, Math.min(100, (moved / limit) * 100)) : 0
  const barLabel = isLess ? '不可超過上限' : ct === 'hold_pace' ? '保持不低於' : '達標即完成'
  const barColor = isLess
    ? (pct > 80 ? 'linear-gradient(90deg,#ff6b6b,#ff4b5c)' : 'linear-gradient(90deg,#FFC24B,#ff8a4b)')
    : 'linear-gradient(90deg,#FFD24D,#46E3A0)'

  return (
    <div style={{ ...banner, borderColor: 'rgba(255,194,75,.55)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 10 }}>
        <span style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--gold)', fontWeight: 800 }}>⚡ 事件任務</span>
        <span style={{ fontSize: 20, fontWeight: 900, color: ready ? 'var(--fug)' : remain <= 10 ? 'var(--hunt)' : 'var(--gold)', fontVariantNumeric: 'tabular-nums' }}>
          {ready ? `準備 ${readyRemain}` : `${remain}s`}
        </span>
      </div>
      {pickEventImage(def) && <img src={pickEventImage(def)} alt="" style={{ width: '100%', height: 120, objectFit: 'cover', borderRadius: 8, margin: '8px 0 2px', display: 'block' }} />}
      <div style={{ fontSize: 14.5, fontWeight: 700, color: 'var(--tx)', marginTop: 4, lineHeight: 1.5 }}>{def.message || def.name}</div>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginTop: 5 }}>
        <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>目標：{goalText(def)}</span>
        {(def.reward_exp > 0 || def.reward_dp > 0) && (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, background: 'rgba(255,210,77,.12)', border: '1px solid rgba(255,210,77,.35)', borderRadius: 999, padding: '3px 10px' }}>
            <span style={{ fontSize: 10, letterSpacing: '.1em', color: 'var(--tx-faint)', fontWeight: 700 }}>獎勵</span>
            {def.reward_exp > 0 && <span style={{ fontSize: 13, fontWeight: 900, color: 'var(--gold)' }}>+{def.reward_exp} EXP</span>}
            {def.reward_dp > 0 && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3, fontSize: 13, fontWeight: 900, color: '#FFD24D' }}><DpCoin size={14} />+{def.reward_dp}</span>}
          </span>
        )}
      </div>
      {ready ? (
        // 準備期（吸收偵測+反應+延遲）：倒數結束才開始計算，讓跑者先反應
        <div style={{ textAlign: 'center', margin: '10px 0 2px', fontSize: 17, fontWeight: 900, color: 'var(--fug)' }}>
          {isLess ? '準備停下！' : '準備出發！'} {readyRemain}…
        </div>
      ) : limit > 0 && (
        <>
          <div style={barOuter}><div style={{ ...barInner, width: `${pct}%`, background: barColor }} /></div>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, marginTop: 3 }}>
            <span style={{ color: isLess ? (pct > 80 ? 'var(--hunt)' : 'var(--tx-faint)') : 'var(--tx-faint)' }}>
              {barLabel}
            </span>
            <span style={{ color: 'var(--tx)', fontWeight: 700, fontVariantNumeric: 'tabular-nums' }}>
              已移動 {Math.round(moved)} / {Math.round(limit)} m
            </span>
          </div>
        </>
      )}
    </div>
  )
}

// 完成/失敗結果：同樣以「內嵌橫幅」呈現（不是彈窗），需按「收下」或約 12 秒後才收起
export function EventResultBanner({ result, onClose }: { result: EventResult; onClose: () => void }) {
  useEffect(() => { if (result.pending) return; const t = setTimeout(onClose, 12000); return () => clearTimeout(t) }, [onClose, result.pending])
  const isInter = isInteractionType(result.def.completion_type)
  const stars = result.stars ?? 0
  const pending = !!result.pending // 結算中：中性樣式，避免先閃紅（失敗）再變綠（完成）
  const weak = !pending && result.status === 'completed' && isInter && stars === 0 // 互動 0★＝差一點
  const ok = !pending && result.status === 'completed' && !weak
  const hasReward = result.reward_exp > 0 || result.reward_dp > 0
  const title = pending ? '🎉 任務完成！' : ok ? '🎉 任務完成！' : weak ? '🐾 差一點…' : '🐾 任務失敗'
  const borderColor = pending ? 'rgba(255,194,75,.5)' : ok ? 'rgba(70,227,160,.5)' : 'rgba(255,90,90,.4)'
  const bg = pending ? 'linear-gradient(180deg, rgba(255,194,75,.14), rgba(9,12,16,.95))'
    : ok ? 'linear-gradient(180deg, rgba(70,227,160,.20), rgba(9,12,16,.95))'
      : 'linear-gradient(180deg, rgba(255,90,90,.18), rgba(9,12,16,.95))'
  const titleColor = pending ? 'var(--gold)' : ok ? 'var(--fug)' : 'var(--hunt)'
  return (
    <div style={{ ...banner, borderColor, background: bg }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
        <span style={{ fontSize: 16, fontWeight: 900, color: titleColor }}>{title}</span>
        {!result.pending && <button onClick={onClose} style={{ background: 'rgba(255,255,255,.08)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 14px', color: 'var(--tx)', fontSize: 12.5, cursor: 'pointer', flexShrink: 0 }}>收下</button>}
      </div>
      <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 3 }}>{result.def.name}</div>
      {isInter && result.status === 'completed' && !result.pending && (
        <div style={{ fontSize: 22, letterSpacing: 4, marginTop: 6 }}>
          {[0, 1, 2].map((i) => <span key={i} style={{ color: i < stars ? '#FFD24D' : 'rgba(255,255,255,.18)' }}>★</span>)}
          <span style={{ fontSize: 12, color: 'var(--tx-faint)', marginLeft: 8 }}>完成度 {stars * 33 + (stars === 3 ? 1 : 0)}%+</span>
        </div>
      )}
      {result.pending && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 4 }}>結算中…</div>}
      {ok && hasReward && !result.pending && (
        <div style={{ display: 'flex', gap: 14, alignItems: 'center', marginTop: 8 }}>
          {result.reward_exp > 0 && <span style={{ fontSize: 18, fontWeight: 900, color: 'var(--gold)' }}>+{result.reward_exp} EXP</span>}
          {result.reward_dp > 0 && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: 18, fontWeight: 900, color: '#FFD24D' }}><DpCoin size={18} />+{result.reward_dp}</span>}
        </div>
      )}
      {!ok && !result.pending && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 4 }}>沒關係，下次加油！</div>}
    </div>
  )
}

const banner: React.CSSProperties = { margin: '10px 12px 0', background: 'rgba(9,12,16,.94)', border: '1px solid rgba(255,194,75,.45)', borderRadius: 12, padding: '12px 14px', boxShadow: '0 6px 24px rgba(0,0,0,.5)', backdropFilter: 'blur(3px)' }
const barOuter: React.CSSProperties = { height: 8, borderRadius: 999, background: 'rgba(255,255,255,.08)', overflow: 'hidden', marginTop: 8 }
const barInner: React.CSSProperties = { height: '100%', borderRadius: 999, background: 'linear-gradient(90deg,#FFD24D,#FFC24B)', transition: 'width .3s' }
