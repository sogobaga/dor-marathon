'use client'

import { useEffect, useRef, useState } from 'react'
import type { EventDef } from '@/lib/api'
import DpCoin from './DpCoin'

// 觸發演出階段：announce=全螢幕紅閃警報 / offer=任務目標面板(等接受) / countdown=321 / active=正式進行中
export type EventPhase = 'announce' | 'offer' | 'countdown' | 'active'
// raceMode：僅 Phase B 多人事件才有意義；'collective'＝共享累積目標（需跑貢獻迴圈+渲染群體進度條），
// 未設定/'individual' 為既有個人賽（含 Phase A 日常事件）行為，完全不變。
export type ActiveEvent = { def: EventDef; occId: string; triggerD: number; triggerT: number; readyUntil: number; deadline: number; raceInstanceId?: string; raceMode?: 'individual' | 'collective'; baseSpk?: number; phase?: EventPhase }

// 配速（秒/公里）格式化為 M:SS/km；無效回 '—'
export function fmtPace(spk: number): string {
  if (!isFinite(spk) || spk <= 0) return '—'
  const m = Math.floor(spk / 60), s = Math.round(spk % 60)
  return `${m}:${s.toString().padStart(2, '0')}`
}
export type EventResult = { status: 'completed' | 'failed'; def: EventDef; reward_exp: number; reward_dp: number; stars?: number; bonus_exp?: number; bonus_dp?: number; pending?: boolean }

export function isInteractionType(ct: string): boolean { return ct === 'tap_burst' || ct === 'hold_press' || ct === 'swipe_charge' || ct === 'dodge_swipe' || ct === 'draw_shape' }

// 依跑者當下時間挑時段插圖：白天 06–17、黃昏 17–19、晚上 19–06；未設定該時段回退預設圖。
// 事件任務 / 多人事件邀請共用（欄位名一致）。
export function pickTimeImage(imgs: { image_url?: string; image_day_url?: string; image_dusk_url?: string; image_night_url?: string }): string {
  const h = new Date().getHours()
  const seg = (h >= 6 && h < 17) ? imgs.image_day_url : (h >= 17 && h < 19) ? imgs.image_dusk_url : imgs.image_night_url
  return seg || imgs.image_url || ''
}
export function pickEventImage(def: EventDef): string { return pickTimeImage(def) }

function goalText(def: EventDef): string {
  if (def.goal_text?.trim()) return def.goal_text.trim() // 後台自訂優先；留空才用下方依完成條件自動產生（防呆）
  const p = def.completion_params
  const r = (k: string) => Math.round(p[k] ?? 0)
  switch (def.completion_type) {
    case 'move_more': return `${r('limit_s')} 秒內再移動 ${r('target_m')} 公尺`
    case 'move_less': return `維持 ${r('limit_s')} 秒，移動不超過 ${r('max_m')} 公尺`
    case 'hold_pace': return `維持 ${r('limit_s')} 秒不停下（每 ${r('check_s')} 秒至少移動 ${r('min_m')}m）`
    case 'sprint': return `${r('limit_s')} 秒內衝刺：任一 ${r('burst_s')} 秒移動 ≥ ${r('burst_m')}m`
    case 'negative_split': return `${r('limit_s')} 秒內後段加速：後半移動 ≥ 前半的 ${r('ratio_pct')}%`
    case 'pace_shift': return p.faster >= 0.5
      ? `維持 ${r('limit_s')} 秒，配速比你的平均快 ${r('delta_spk')} 秒/公里`
      : `維持 ${r('limit_s')} 秒，配速比你的平均慢 ${r('delta_spk')} 秒/公里`
    case 'tap_burst': return `${r('limit_s')} 秒內連續點擊 ${r('target_taps')} 次（物理攻擊）`
    case 'hold_press': return `按住螢幕 ${r('hold_s')} 秒（物理防禦）`
    case 'swipe_charge': return `${r('limit_s')} 秒內連續滑動蓄力（魔法攻擊）`
    case 'dodge_swipe': return `${r('limit_s')} 秒內連續滑動 ${r('target_swipes')} 次閃避`
    case 'draw_shape': return `${r('limit_s')} 秒內畫出指定圖形（魔法陣）`
  }
  return ''
}

// 進行中事件：夾在地圖與數據之間、常駐可見的橫幅（不擋畫面、不能被隨手關掉）
// groupProgress：Phase B2 collective 專用——有值才多渲染一條「群體共享進度條」；individual 事件不傳、UI 完全不變。
export function EventBanner({ active, moved, groupProgress }: { active: ActiveEvent; moved: number; groupProgress?: { current: number; target: number; participants: number } }) {
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
    // data-skin="default"：面板底色固定深色，強制內部文字用暗色主題（亮色）token，避免暖色 skin 下暗字看不見
    <div data-skin="default" style={{ ...banner, borderColor: 'rgba(255,194,75,.55)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 10 }}>
        <span style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--gold)', fontWeight: 800 }}>⚡ 事件任務</span>
        <span style={{ fontSize: 20, fontWeight: 900, color: ready ? 'var(--fug)' : remain <= 10 ? 'var(--hunt)' : 'var(--gold)', fontVariantNumeric: 'tabular-nums' }}>
          {ready ? `準備 ${readyRemain}` : `${remain}s`}
        </span>
      </div>
      {pickEventImage(def) && <img src={pickEventImage(def)} alt="" style={{ width: '100%', height: 120, objectFit: 'cover', borderRadius: 8, margin: '8px 0 2px', display: 'block' }} />}
      <div style={{ fontSize: 14.5, fontWeight: 700, color: 'var(--tx)', marginTop: 4, lineHeight: 1.5 }}>{def.message || def.name}</div>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginTop: 5 }}>
        <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--tx)' }}>目標：{goalText(def)}</span>
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
      {groupProgress && (() => {
        const gPct = groupProgress.target > 0 ? Math.max(0, Math.min(100, (groupProgress.current / groupProgress.target) * 100)) : 0
        return (
          <div style={{ marginTop: 10 }}>
            <div style={barOuter}><div style={{ ...barInner, width: `${gPct}%`, background: 'linear-gradient(90deg,#46E3A0,#2fbf9e)' }} /></div>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, marginTop: 3 }}>
              <span style={{ color: 'var(--tx-faint)' }}>👥 {groupProgress.participants} 人一起</span>
              <span style={{ color: 'var(--tx)', fontWeight: 700, fontVariantNumeric: 'tabular-nums' }}>
                {Math.round(groupProgress.current)}/{Math.round(groupProgress.target)} m
              </span>
            </div>
          </div>
        )
      })()}
      {!ready && ct === 'pace_shift' && (() => {
        const base = active.baseSpk ?? 0
        const delta = p.delta_spk ?? 0
        const faster = (p.faster ?? 0) >= 0.5
        const target = faster ? base - delta : base + delta
        const winSec = Math.max(0.001, (now - active.readyUntil) / 1000)
        const livePace = moved > 0 ? winSec / (moved / 1000) : Infinity
        const meeting = base > 0 && (faster ? livePace <= target : (livePace >= target && moved >= winSec * 0.5))
        return (
          <div style={{ marginTop: 8 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11.5, color: 'var(--tx-faint)' }}>
              <span>你的平均 {fmtPace(base)}/km</span>
              <span>目標 {base > 0 ? `${faster ? '≤' : '≥'} ${fmtPace(target)}/km` : '—'}</span>
            </div>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginTop: 2 }}>
              <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>目前配速</span>
              <span style={{ fontSize: 20, fontWeight: 900, fontVariantNumeric: 'tabular-nums', color: meeting ? 'var(--fug)' : 'var(--gold)' }}>{fmtPace(livePace)}<span style={{ fontSize: 12, fontWeight: 700 }}>/km</span></span>
            </div>
            <div style={{ fontSize: 11, color: meeting ? 'var(--fug)' : 'var(--tx-faint)', marginTop: 2 }}>
              {meeting ? (faster ? '很好，維持這個速度！' : '很好，保持這個節奏！') : (faster ? '再加快一點！' : '再放慢一點！')}
            </div>
          </div>
        )
      })()}
    </div>
  )
}

// 完成/失敗結果：同樣以「內嵌橫幅」呈現（不是彈窗），需按「收下」或約 12 秒後才收起
export function EventResultBanner({ result, onClose }: { result: EventResult; onClose: () => void }) {
  // onClose 用 ref：父層每 250ms 重繪會換掉 onClose 識別，若放進 deps 會每次重設 12 秒計時而永遠不自動收起。
  const closeRef = useRef(onClose); closeRef.current = onClose
  useEffect(() => { if (result.pending) return; const t = setTimeout(() => closeRef.current(), 12000); return () => clearTimeout(t) }, [result.pending])
  const isInter = isInteractionType(result.def.completion_type)
  const stars = result.stars ?? 0
  const pending = !!result.pending // 結算中：中性樣式，避免先閃紅（失敗）再變綠（完成）
  const weak = !pending && result.status === 'completed' && isInter && stars === 0 // 互動 0★＝差一點
  const ok = !pending && result.status === 'completed' && !weak
  const hasReward = result.reward_exp > 0 || result.reward_dp > 0
  const title = pending ? '🎉 任務完成！' : ok ? '🎉 任務完成！' : weak ? '🐾 差一點…' : '🐾 任務失敗'
  const borderColor = pending ? 'rgba(255,194,75,.5)' : ok ? 'rgba(70,227,160,.5)' : 'rgba(255,90,90,.4)'
  const bg = pending ? 'linear-gradient(180deg, #2b2a1e, #0b0e13)'
    : ok ? 'linear-gradient(180deg, #12241c, #0b0e13)'
      : 'linear-gradient(180deg, #241315, #0b0e13)'
  const titleColor = pending ? 'var(--gold)' : ok ? 'var(--fug)' : 'var(--hunt)'
  return (
    <div data-skin="default" style={{ ...banner, borderColor, background: bg }}>
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
      {ok && !result.pending && ((result.bonus_exp ?? 0) > 0 || (result.bonus_dp ?? 0) > 0) && (
        <div style={{ marginTop: 6, display: 'inline-flex', alignItems: 'center', gap: 8, background: 'rgba(255,210,77,.14)', border: '1px solid rgba(255,210,77,.5)', borderRadius: 999, padding: '3px 12px' }}>
          <span style={{ fontSize: 12, fontWeight: 900, color: '#FFD24D' }}>🎁 完美 BONUS</span>
          {(result.bonus_exp ?? 0) > 0 && <span style={{ fontSize: 13, fontWeight: 900, color: 'var(--gold)' }}>+{result.bonus_exp} EXP</span>}
          {(result.bonus_dp ?? 0) > 0 && <span style={{ fontSize: 13, fontWeight: 900, color: '#FFD24D' }}>🪙+{result.bonus_dp}</span>}
        </div>
      )}
      {!ok && !result.pending && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 4 }}>沒關係，下次加油！</div>}
    </div>
  )
}

// 底色改「完全不透明」：跑動中地圖文字不再透出、事件文字清楚可讀（實測 6km 問題修正）
const banner: React.CSSProperties = { margin: '10px 12px 0', background: '#0b0e13', border: '1px solid rgba(255,194,75,.45)', borderRadius: 12, padding: '12px 14px', boxShadow: '0 6px 24px rgba(0,0,0,.5)' }
const barOuter: React.CSSProperties = { height: 8, borderRadius: 999, background: 'rgba(255,255,255,.08)', overflow: 'hidden', marginTop: 8 }
const barInner: React.CSSProperties = { height: '100%', borderRadius: 999, background: 'linear-gradient(90deg,#FFD24D,#FFC24B)', transition: 'width .3s' }

// ───────── 觸發演出：Step1 全螢幕紅閃 / Step2 任務目標面板 / Step3 置中 321 ─────────

// Step1：全螢幕「事件觸發」紅字閃 3 下 + 四邊紅光，約 1.6s 後 onDone → 進入任務目標面板。
// onDone 用 ref 保存：父層每 250ms（計時）重繪會換掉 onDone 識別，若放進 deps 會每次重設 timer 而永遠不觸發。
export function EventTriggerFlash({ onDone }: { onDone: () => void }) {
  const doneRef = useRef(onDone); doneRef.current = onDone
  useEffect(() => { const t = setTimeout(() => doneRef.current(), 2200); return () => clearTimeout(t) }, []) // 3 下 × 0.7s ≈ 2.1s + 緩衝
  return (
    <div className="evt-flash" aria-hidden>
      <div className="evt-flash-text">事件觸發</div>
    </div>
  )
}

// Step3：接受後的置中 3-2-1 倒數（佔九宮格中央格），數到 0 後 onDone → 事件正式開始。
// 掛載時起單一 interval、onDone 用 ref——避免父層頻繁重繪換掉 onDone 而每 tick 重置倒數。
export function Countdown321({ onDone }: { onDone: () => void }) {
  const [n, setN] = useState(3)
  const doneRef = useRef(onDone); doneRef.current = onDone
  useEffect(() => {
    let cur = 3
    const t = setInterval(() => {
      cur -= 1
      if (cur <= 0) { clearInterval(t); setN(0); doneRef.current() }
      else setN(cur)
    }, 1000)
    return () => clearInterval(t)
  }, [])
  if (n <= 0) return null
  return (
    <div className="evt-countdown" aria-hidden>
      <div key={n} className="evt-countdown-num">{n}</div>
    </div>
  )
}

// Step2：任務目標面板——不自動消失，等跑者按「接受/放棄」。大字目標、遊戲感外框、附圖、獎勵。
export function EventOfferPanel({ active, onAccept, onDecline }: { active: ActiveEvent; onAccept: () => void; onDecline: () => void }) {
  const def = active.def
  const img = pickEventImage(def)
  return (
    <div data-skin="default" style={offerBackdrop}>
      <div style={offerCard}>
        <span style={{ ...corner, top: 6, left: 6, borderRight: 'none', borderBottom: 'none' }} />
        <span style={{ ...corner, top: 6, right: 6, borderLeft: 'none', borderBottom: 'none' }} />
        <span style={{ ...corner, bottom: 6, left: 6, borderRight: 'none', borderTop: 'none' }} />
        <span style={{ ...corner, bottom: 6, right: 6, borderLeft: 'none', borderTop: 'none' }} />
        <div style={{ fontSize: 12, letterSpacing: '.3em', color: 'var(--gold)', fontWeight: 800, textAlign: 'center' }}>⚡ 事件任務 ⚡</div>
        {img && <img src={img} alt="" style={{ width: '100%', height: 150, objectFit: 'cover', borderRadius: 10, margin: '10px 0', display: 'block' }} />}
        <div style={{ fontSize: 15, fontWeight: 700, color: 'var(--tx)', lineHeight: 1.5, textAlign: 'center', marginTop: img ? 0 : 10 }}>{def.message || def.name}</div>
        <div style={{ marginTop: 12, padding: '10px 12px', borderRadius: 10, background: 'rgba(255,194,75,.08)', border: '1px solid rgba(255,194,75,.3)', textAlign: 'center' }}>
          <div style={{ fontSize: 12, letterSpacing: '.2em', color: 'var(--gold)', fontWeight: 800 }}>任務目標</div>
          <div style={{ fontSize: 23, fontWeight: 900, color: '#fff', marginTop: 4, lineHeight: 1.3 }}>{goalText(def)}</div>
        </div>
        {(def.reward_exp > 0 || def.reward_dp > 0) && (
          <div style={{ display: 'flex', justifyContent: 'center', gap: 14, alignItems: 'center', marginTop: 12 }}>
            <span style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-faint)', fontWeight: 700 }}>獎勵</span>
            {def.reward_exp > 0 && <span style={{ fontSize: 17, fontWeight: 900, color: 'var(--gold)' }}>+{def.reward_exp} EXP</span>}
            {def.reward_dp > 0 && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: 17, fontWeight: 900, color: '#FFD24D' }}><DpCoin size={17} />+{def.reward_dp}</span>}
          </div>
        )}
        <div style={{ display: 'flex', gap: 10, marginTop: 18 }}>
          <button onClick={onDecline} style={declineBtn}>放棄</button>
          <button onClick={onAccept} style={acceptBtn}>接受</button>
        </div>
      </div>
    </div>
  )
}

const offerBackdrop: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 2100, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 20 }
const offerCard: React.CSSProperties = { position: 'relative', width: '100%', maxWidth: 360, maxHeight: '90vh', overflowY: 'auto', background: '#0b0e13', border: '2px solid rgba(255,194,75,.55)', borderRadius: 16, padding: '20px 18px', boxShadow: '0 0 0 1px rgba(255,194,75,.15), 0 20px 60px rgba(0,0,0,.7), inset 0 0 40px rgba(255,194,75,.05)' }
const corner: React.CSSProperties = { position: 'absolute', width: 16, height: 16, border: '2px solid var(--gold)', pointerEvents: 'none' }
const acceptBtn: React.CSSProperties = { flex: 1.5, padding: '13px 0', borderRadius: 12, border: 'none', background: 'linear-gradient(180deg,#38d17f,#2fbf71)', color: '#04120a', fontSize: 17, fontWeight: 900, cursor: 'pointer', boxShadow: '0 4px 16px rgba(47,191,113,.4)' }
const declineBtn: React.CSSProperties = { flex: 1, padding: '13px 0', borderRadius: 12, border: '1px solid rgba(185,166,138,.4)', background: 'rgba(185,166,138,.08)', color: '#b9a68a', fontSize: 15, fontWeight: 700, cursor: 'pointer' }
