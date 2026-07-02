'use client'

// 「畫出圖形」完成條件的專屬編輯器：時限/次數 + 三形狀各一列（權重｜加碼 EXP｜加碼 DP）。
const SHAPES = [{ k: 3, label: '△ 三角形' }, { k: 4, label: '◇ 四角形' }, { k: 5, label: '✦ 五芒星' }]

export default function ShapeCompletionEditor({ value, onChange }: {
  value: Record<string, number>
  onChange: (patch: Record<string, number>) => void
}) {
  const v = (k: string) => (value[k] ?? '')
  const set = (k: string) => (e: React.ChangeEvent<HTMLInputElement>) => onChange({ [k]: parseFloat(e.target.value) || 0 })

  return (
    <div>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
        <label style={col}><span style={lab}>時限（秒）</span><input style={inp} type="number" value={v('limit_s')} onChange={set('limit_s')} /></label>
        <label style={col}><span style={lab}>可嘗試次數</span><input style={inp} type="number" value={v('attempts')} onChange={set('attempts')} /></label>
      </div>

      <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', margin: '10px 0 6px', lineHeight: 1.6 }}>
        各圖形：<b>出現權重</b>（0＝不出現、全 0＝三種平均隨機）、<b>加碼</b>獎勵（疊加在下方「共用基礎完成獎勵」之上，依畫得準的星等分級）。
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '92px 1fr 1fr 1fr', gap: 8, alignItems: 'center' }}>
        <span style={hd}>圖形</span><span style={hd}>權重</span><span style={hd}>加碼 EXP</span><span style={hd}>加碼 DP</span>
        {SHAPES.map((s) => (
          <FragmentRow key={s.k} label={s.label} k={s.k} v={v} set={set} />
        ))}
      </div>
    </div>
  )
}

function FragmentRow({ label, k, v, set }: { label: string; k: number; v: (k: string) => number | ''; set: (k: string) => (e: React.ChangeEvent<HTMLInputElement>) => void }) {
  return (
    <>
      <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx)' }}>{label}</span>
      <input style={inp} type="number" value={v(`w${k}`)} onChange={set(`w${k}`)} placeholder="0" />
      <input style={inp} type="number" value={v(`x${k}_exp`)} onChange={set(`x${k}_exp`)} placeholder="0" />
      <input style={inp} type="number" value={v(`x${k}_dp`)} onChange={set(`x${k}_dp`)} placeholder="0" />
    </>
  )
}

const col: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4 }
const lab: React.CSSProperties = { fontSize: 11, color: 'var(--tx-faint)' }
const hd: React.CSSProperties = { fontSize: 11, color: 'var(--tx-faint)', fontWeight: 700 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%' }
