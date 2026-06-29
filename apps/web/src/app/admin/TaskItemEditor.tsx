'use client'

import { METRIC_CATALOG, METRIC_BY_KEY, type MetricType } from '@/lib/api'

// 任務/模組項目共用的可編輯欄位
export type TaskFields = {
  metric_type: MetricType
  target_value?: number | null
  range_lo?: number | null
  range_hi?: number | null
  title: string
  description?: string
}

export function emptyTask(): TaskFields {
  return { metric_type: 'cumulative_distance', target_value: null, range_lo: null, range_hi: null, title: '', description: '' }
}

// 一行摘要（清單顯示用）
export function taskSummary(t: TaskFields): string {
  const m = METRIC_BY_KEY[t.metric_type]
  if (!m) return t.title || '任務'
  const label = t.title || m.label
  if (m.kind === 'range') return `${label}（${m.label} ${t.range_lo ?? '—'}–${t.range_hi ?? '—'} ${m.unit}）`
  return `${label}（${m.label} ≥ ${t.target_value ?? '—'} ${m.unit}）`
}

function num(v: string): number | null {
  return v === '' ? null : parseFloat(v)
}

export function TaskItemEditor({
  value,
  onChange,
  onRemove,
}: {
  value: TaskFields
  onChange: (patch: Partial<TaskFields>) => void
  onRemove?: () => void
}) {
  const spec = METRIC_BY_KEY[value.metric_type]
  const isRange = spec?.kind === 'range'

  return (
    <div style={card}>
      <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <Field label="任務指標" grow>
          <select style={inp} value={value.metric_type} onChange={(e) => onChange({ metric_type: e.target.value as MetricType })}>
            {METRIC_CATALOG.map((m) => (
              <option key={m.key} value={m.key}>
                {m.label}{m.has_data ? '' : '（待資料源）'}
              </option>
            ))}
          </select>
        </Field>

        {isRange ? (
          <>
            <Field label={`下限 (${spec.unit})`}>
              <input style={{ ...inp, width: 96 }} type="number" value={value.range_lo ?? ''} onChange={(e) => onChange({ range_lo: num(e.target.value) })} />
            </Field>
            <Field label={`上限 (${spec.unit})`}>
              <input style={{ ...inp, width: 96 }} type="number" value={value.range_hi ?? ''} onChange={(e) => onChange({ range_hi: num(e.target.value) })} />
            </Field>
          </>
        ) : (
          <Field label={`目標值 (${spec?.unit ?? ''})`}>
            <input style={{ ...inp, width: 120 }} type="number" value={value.target_value ?? ''} onChange={(e) => onChange({ target_value: num(e.target.value) })} placeholder="達標數值" />
          </Field>
        )}

        {onRemove && (
          <button type="button" onClick={onRemove} style={removeBtn}>移除</button>
        )}
      </div>

      <div style={{ display: 'flex', gap: 8, marginTop: 8, flexWrap: 'wrap' }}>
        <Field label="任務標題（前台顯示，留空用指標名稱）" grow>
          <input style={inp} value={value.title} onChange={(e) => onChange({ title: e.target.value })} placeholder={spec?.label} />
        </Field>
      </div>
      <div style={{ marginTop: 8 }}>
        <Field label="說明（選填）" grow>
          <input style={inp} value={value.description ?? ''} onChange={(e) => onChange({ description: e.target.value })} />
        </Field>
      </div>

      <div style={hint}>
        {isRange
          ? `判定：實際${spec.label}落在區間內即完成`
          : `判定：實際${spec?.label ?? ''} ≥ 目標值即完成`}
        {spec && !spec.has_data ? ' · ⚠ 此指標目前無資料源，設定後待之後擴充活動上傳才會判定' : ''}
      </div>
    </div>
  )
}

function Field({ label, children, grow }: { label: string; children: React.ReactNode; grow?: boolean }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: grow ? 1 : undefined, minWidth: grow ? 160 : undefined }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 12 }
const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8,
  padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%',
}
const removeBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--hunt)', cursor: 'pointer', fontSize: 13, padding: '8px 4px',
}
const hint: React.CSSProperties = { fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }
