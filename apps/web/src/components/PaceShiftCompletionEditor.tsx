'use client'

import { useEffect } from 'react'

// 「變速跑」完成條件的專屬編輯器：持續時間 + 方向(加速/減速) + 與平均配速差(秒/公里)。
// 概念：以跑者「觸發當下的平均配速」為基準，要求這段時間內比平均快/慢 N 秒/公里。
export default function PaceShiftCompletionEditor({ value, onChange }: {
  value: Record<string, number>
  onChange: (patch: Record<string, number>) => void
}) {
  // 預設「加速」；避免未選方向時（faster 未設）被伺服器當成減速(0)。
  useEffect(() => { if (value.faster == null) onChange({ faster: 1 }) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const faster = (value.faster ?? 1) >= 0.5
  const num = (k: string) => (value[k] ?? '')
  const setNum = (k: string) => (e: React.ChangeEvent<HTMLInputElement>) => onChange({ [k]: parseFloat(e.target.value) || 0 })
  const d = Math.round(value.delta_spk ?? 0)
  const s = Math.round(value.limit_s ?? 0)

  return (
    <div>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <label style={col}><span style={lab}>持續時間（秒）</span><input style={inp} type="number" min={0} value={num('limit_s')} onChange={setNum('limit_s')} /></label>
        <label style={col}><span style={lab}>方向</span>
          <select style={{ ...inp, width: 210 }} value={faster ? '1' : '0'} onChange={(e) => onChange({ faster: e.target.value === '1' ? 1 : 0 })}>
            <option value="1">加速（比平均配速快）</option>
            <option value="0">減速（比平均配速慢）</option>
          </select>
        </label>
        <label style={col}><span style={lab}>與平均配速差（秒/公里）</span><input style={inp} type="number" min={0} value={num('delta_spk')} onChange={setNum('delta_spk')} /></label>
      </div>
      <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.7 }}>
        以跑者「觸發當下的平均配速」為基準：<b>{faster ? '加速' : '減速'} {d} 秒/公里</b> ＝ 這段時間內配速需{faster ? '快' : '慢'}於「平均 {faster ? '−' : '＋'} {d} 秒/公里」。
        <br />例：平均 6:30、設「{faster ? '加速 30' : '減速 30'}」→ 需維持{faster ? '快於 6:00' : '慢於 7:00'} /km 共 {s} 秒。{!faster && '（減速仍須持續移動，不可完全停下。）'}
      </div>
    </div>
  )
}

const col: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4 }
const lab: React.CSSProperties = { fontSize: 11, color: 'var(--tx-faint)' }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: 150 }
