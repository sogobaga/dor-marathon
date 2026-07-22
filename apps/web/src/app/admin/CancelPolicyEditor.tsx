'use client'

import type { CancellationPolicy, CancellationTier } from '@/lib/api'

// 程式內建預設（migration 095 寫入 app_settings 的初始值一致）；系統設定尚未載入完成前的暫時顯示用。
export const DEFAULT_CANCELLATION_POLICY: CancellationPolicy = {
  deadline_days: 14,
  tiers: [
    { days_before: 30, ratio: 90 },
    { days_before: 14, ratio: 50 },
  ],
}

// 依 days_before 由大到小排序（後端計算也會排，這裡先排讓使用者看到的順序就是實際生效順序）。
export function sortTiers(tiers: CancellationTier[]): CancellationTier[] {
  return [...tiers].sort((a, b) => b.days_before - a.days_before)
}

// 白話說明目前規則（畫面顯示用）
export function describeCancellationPolicy(policy: CancellationPolicy): string {
  const sorted = sortTiers(policy.tiers ?? [])
  const tierText = sorted.length
    ? sorted.map((t) => `距賽事 ≥${t.days_before} 天退 ${t.ratio}%`).join('、')
    : '未設定任何退費級距（一律不退費）'
  return `${tierText}；賽事開始前 ${policy.deadline_days} 天內不可申請取消。`
}

// 儲存前驗證：deadline_days ≥ 0；tiers 的 days_before ≥ 0、ratio 0–100。回傳錯誤訊息，null＝通過。
export function validateCancellationPolicy(policy: CancellationPolicy): string | null {
  if (!Number.isFinite(policy.deadline_days) || policy.deadline_days < 0) return '取消申請截止天數不可為負'
  for (const t of policy.tiers ?? []) {
    if (!Number.isFinite(t.days_before) || t.days_before < 0) return '退費級距的「距賽事天數」不可為負'
    if (!Number.isFinite(t.ratio) || t.ratio < 0 || t.ratio > 100) return '退費級距的「退費比例」需介於 0–100'
  }
  return null
}

/** 取消退費政策編輯區塊（deadline_days + tiers 動態增刪列），供「系統設定」與「賽事表單」共用。 */
export function CancellationPolicyFields({
  policy,
  onChange,
}: {
  policy: CancellationPolicy
  onChange: (p: CancellationPolicy) => void
}) {
  const tiers = policy.tiers ?? []
  const updateTier = (i: number, patch: Partial<CancellationTier>) =>
    onChange({ ...policy, tiers: tiers.map((t, idx) => (idx === i ? { ...t, ...patch } : t)) })
  const removeTier = (i: number) => onChange({ ...policy, tiers: tiers.filter((_, idx) => idx !== i) })
  const addTier = () => onChange({ ...policy, tiers: [...tiers, { days_before: 0, ratio: 0 }] })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <Field label="取消申請截止（賽事開始前幾天內不可取消）">
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <input
            style={{ ...inp, width: 100 }}
            type="number"
            min={0}
            value={policy.deadline_days}
            onChange={(e) => onChange({ ...policy, deadline_days: e.target.value === '' ? 0 : Math.max(0, parseInt(e.target.value, 10) || 0) })}
          />
          <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>天</span>
        </div>
      </Field>

      <div>
        <span style={{ fontSize: 11, letterSpacing: '.05em', color: 'var(--tx-faint)' }}>
          退費級距（依「距賽事天數」由大到小比對，取第一個符合的比例）
        </span>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
          {tiers.map((t, i) => (
            <div key={i} style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr) auto', gap: 8, alignItems: 'flex-end' }}>
              <Field label="距賽事天數 ≥ (天)">
                <input
                  style={inp} type="number" min={0} value={t.days_before}
                  onChange={(e) => updateTier(i, { days_before: e.target.value === '' ? 0 : Math.max(0, parseInt(e.target.value, 10) || 0) })}
                />
              </Field>
              <Field label="退費比例 (%)">
                <input
                  style={inp} type="number" min={0} max={100} value={t.ratio}
                  onChange={(e) => updateTier(i, { ratio: e.target.value === '' ? 0 : Math.min(100, Math.max(0, parseInt(e.target.value, 10) || 0)) })}
                />
              </Field>
              <button type="button" onClick={() => removeTier(i)} style={removeBtn}>移除</button>
            </div>
          ))}
        </div>
        <button type="button" onClick={addTier} style={{ ...smallBtn, marginTop: 8 }}>＋ 新增退費級距</button>
        {tiers.length === 0 && (
          <div style={hint}>目前無任何退費級距：只要在截止天數之前申請取消，一律退 0%（不退費）。</div>
        )}
      </div>

      <div style={{ ...hint, fontWeight: 600 }}>{describeCancellationPolicy(policy)}</div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8,
  padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%',
}
const smallBtn: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8,
  padding: '7px 12px', color: 'var(--tx)', fontSize: 12.5, cursor: 'pointer',
}
const removeBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--hunt)', cursor: 'pointer', fontSize: 13, padding: '8px 4px',
}
const hint: React.CSSProperties = { fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 6, lineHeight: 1.6 }
