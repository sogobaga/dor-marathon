'use client'

import { type WoStep } from '@/lib/workout'

// 秒 → 「X 時 Y 分」/「Y 分」
function fmtMin(sec: number) {
  const m = Math.round(sec / 60)
  return m >= 60 ? `${Math.floor(m / 60)} 時 ${m % 60} 分` : `${m} 分`
}

// 自主訓練「開跑前」提示面板：清楚告知目前是自主訓練模式 + 是哪份課表 + 總距離/預估時間/段數。
// 進 GPS 追蹤頁但尚未按「開始訓練」時顯示（woPhase==='idle' 且 workout.kind==='freetrain'）。
export default function FreetrainIntroPanel({ title, steps }: { title: string; steps: WoStep[] }) {
  const totalM = steps.reduce((a, s) => a + (s.targetType === 'distance' ? s.target : 0), 0)
  const estSec = steps.reduce((a, s) => a + (s.targetType === 'time'
    ? s.target
    : (s.target / 1000) * ((s.paceFast && s.paceSlow) ? (s.paceFast + s.paceSlow) / 2 : 420)), 0)
  const work = steps.filter((s) => s.kind === 'work').length
  return (
    <div style={{ background: 'var(--bg-2)', border: '1px solid var(--fug)', borderRadius: 14, padding: '14px 16px', marginBottom: 12 }}>
      <div style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--fug)', fontWeight: 800 }}>🏃 自主訓練模式</div>
      <div style={{ fontSize: 18, fontWeight: 900, color: 'var(--tx)', margin: '6px 0 8px' }}>{title}</div>
      <div style={{ fontSize: 13, color: 'var(--tx-dim)', fontWeight: 700, fontVariantNumeric: 'tabular-nums' }}>
        總距離 {(totalM / 1000).toFixed(1)} K · 預估 {fmtMin(estSec)} · 共 {steps.length} 段{work ? `（含主課 ${work} 段）` : ''}
      </div>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.5 }}>
        按下方「▶ 開始訓練」開始；跑步中會逐段帶你、顯示每段目標配速。完成只累積日常里程 EXP、無額外獎勵。
      </div>
    </div>
  )
}
