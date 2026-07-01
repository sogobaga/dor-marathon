'use client'

import { useState, useEffect, useRef } from 'react'
import { METRIC_CATALOG, METRIC_BY_KEY, type MetricType, type Checkpoint } from '@/lib/api'
import { loadLeaflet } from '@/lib/leaflet'

// 配速秒數 ↔ 「分:秒」字串
export function paceToStr(sec?: number | null): string {
  if (sec == null || isNaN(sec)) return ''
  const m = Math.floor(sec / 60)
  const s = Math.round(sec % 60)
  return `${m}:${String(s).padStart(2, '0')}`
}
export function parsePace(v: string): number | null {
  const s = v.trim()
  if (s === '') return null
  if (s.includes(':')) {
    const [mm, ss = ''] = s.split(':')
    return (parseInt(mm || '0', 10) || 0) * 60 + (parseInt(ss || '0', 10) || 0)
  }
  // 純數字：1–2 碼當「分」（5→5:00）；3 碼以上末兩碼當「秒」（630→6:30）
  const digits = s.replace(/\D/g, '')
  if (digits === '') return null
  if (digits.length <= 2) return (parseInt(digits, 10) || 0) * 60
  const sec = parseInt(digits.slice(-2), 10) || 0
  const min = parseInt(digits.slice(0, -2), 10) || 0
  return min * 60 + sec
}

// 任務/模組項目共用的可編輯欄位
export type TaskFields = {
  metric_type: MetricType
  target_value?: number | null
  range_lo?: number | null
  range_hi?: number | null
  title: string
  description?: string
  checkpoints?: Checkpoint[]
}

export function emptyTask(): TaskFields {
  return { metric_type: 'cumulative_distance', target_value: null, range_lo: null, range_hi: null, title: '', description: '' }
}

// 一行摘要（清單顯示用）
export function taskSummary(t: TaskFields): string {
  const m = METRIC_BY_KEY[t.metric_type]
  if (!m) return t.title || '任務'
  const label = t.title || m.label
  if (m.kind === 'checkpoint') return `${label}（打卡點 ${t.checkpoints?.length ?? 0} 個）`
  if (m.kind === 'range') {
    if (t.metric_type === 'avg_pace_range') return `${label}（${m.label} ${paceToStr(t.range_lo)}–${paceToStr(t.range_hi)} /km）`
    return `${label}（${m.label} ${t.range_lo ?? '—'}–${t.range_hi ?? '—'} ${m.unit}）`
  }
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
  const isCheckpoint = spec?.kind === 'checkpoint'
  const isPace = value.metric_type === 'avg_pace_range'

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

        {isCheckpoint ? null : isRange ? (
          <>
            <Field label={isPace ? '下限 (分:秒/km)' : `下限 (${spec.unit})`}>
              {isPace
                ? <PaceInput valueSec={value.range_lo} onChangeSec={(s) => onChange({ range_lo: s })} />
                : <input style={{ ...inp, width: 96 }} type="number" value={value.range_lo ?? ''} onChange={(e) => onChange({ range_lo: num(e.target.value) })} />}
            </Field>
            <Field label={isPace ? '上限 (分:秒/km)' : `上限 (${spec.unit})`}>
              {isPace
                ? <PaceInput valueSec={value.range_hi} onChangeSec={(s) => onChange({ range_hi: s })} />
                : <input style={{ ...inp, width: 96 }} type="number" value={value.range_hi ?? ''} onChange={(e) => onChange({ range_hi: num(e.target.value) })} />}
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

      {isCheckpoint && (
        <CheckpointsEditor
          points={value.checkpoints ?? []}
          onChange={(cps) => onChange({ checkpoints: cps })}
        />
      )}

      <div style={hint}>
        {isCheckpoint
          ? '判定：在賽事期間到各打卡點半徑內打卡，集滿全部點即完成（建議搭配「開始跑步」GPS 追蹤打卡以利防弊）。⚠ 賽事開放後重新編輯任務會重置打卡紀錄，請於開賽前定稿。'
          : isRange
          ? `判定：實際${spec.label}落在區間內即完成${isPace ? '（配速可打 6:30 或直接 630→6:30；數字越小越快）' : ''}`
          : `判定：實際${spec?.label ?? ''} ≥ 目標值即完成`}
        {spec && !spec.has_data ? ' · ⚠ 此指標目前無資料源，設定後待之後擴充活動上傳才會判定' : ''}
      </div>
    </div>
  )
}

// 打卡點清單編輯：手動座標（可貼 Google Map）+ 地圖點選
function CheckpointsEditor({ points, onChange }: { points: Checkpoint[]; onChange: (p: Checkpoint[]) => void }) {
  const [activeIdx, setActiveIdx] = useState(0)
  const [showMap, setShowMap] = useState(false)
  const mapRef = useRef<any>(null)
  const layerRef = useRef<any>(null)
  const stateRef = useRef({ points, activeIdx, onChange })
  stateRef.current = { points, activeIdx, onChange }

  const r6 = (n: number) => Math.round(n * 1e6) / 1e6
  const patch = (i: number, p: Partial<Checkpoint>) => onChange(points.map((c, idx) => (idx === i ? { ...c, ...p } : c)))
  const add = () => {
    onChange([...points, { lat: 0, lng: 0, radius_m: 20, title: '', display_order: points.length }])
    setActiveIdx(points.length)
    setShowMap(true)
  }
  const remove = (i: number) => onChange(points.filter((_, idx) => idx !== i).map((c, idx) => ({ ...c, display_order: idx })))

  // 初始化地圖（顯示時）
  useEffect(() => {
    if (!showMap) return
    let cancelled = false
    loadLeaflet().then((L) => {
      if (cancelled || mapRef.current) return
      const first = points.find((p) => p.lat || p.lng)
      const center: [number, number] = [first?.lat || 25.0376, first?.lng || 121.5645] // 預設台北
      const map = L.map('cp-edit-map').setView(center, 16)
      L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
      layerRef.current = L.layerGroup().addTo(map)
      map.on('click', (e: any) => {
        const st = stateRef.current
        if (!st.points.length) return
        const i = Math.min(st.activeIdx, st.points.length - 1)
        st.onChange(st.points.map((c, idx) => (idx === i ? { ...c, lat: r6(e.latlng.lat), lng: r6(e.latlng.lng) } : c)))
      })
      mapRef.current = map
    })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showMap])

  // 重畫標記
  useEffect(() => {
    const L = (window as any).L
    if (!showMap || !L || !layerRef.current) return
    layerRef.current.clearLayers()
    points.forEach((p, i) => {
      if (!p.lat && !p.lng) return
      const active = i === activeIdx
      L.circle([p.lat, p.lng], { radius: p.radius_m || 20, color: active ? '#46E3A0' : '#888', weight: active ? 2 : 1, fillOpacity: 0.12 }).addTo(layerRef.current)
      L.marker([p.lat, p.lng]).addTo(layerRef.current).bindTooltip(`${i + 1}. ${p.title || '打卡點'}`, { permanent: false })
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [points, activeIdx, showMap])

  return (
    <div style={{ marginTop: 10, border: '1px dashed var(--line-2)', borderRadius: 10, padding: 10 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
        <span style={{ fontSize: 12.5, fontWeight: 700, color: 'var(--tx)' }}>打卡點（集滿全部即完成）</span>
        <div style={{ display: 'flex', gap: 8 }}>
          <button type="button" onClick={() => setShowMap((v) => !v)} style={smallBtn}>{showMap ? '收合地圖' : '地圖選點'}</button>
          <button type="button" onClick={add} style={smallBtn}>＋ 新增打卡點</button>
        </div>
      </div>

      {points.length === 0 && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>尚無打卡點，請新增。可貼上 Google 地圖座標，或用「地圖選點」直接點。</div>}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {points.map((p, i) => (
          <div
            key={i}
            onClick={() => setActiveIdx(i)}
            style={{ display: 'flex', gap: 6, alignItems: 'flex-end', flexWrap: 'wrap', padding: 8, borderRadius: 8, cursor: 'pointer', background: i === activeIdx ? 'rgba(70,227,160,.08)' : 'var(--bg-2)', border: `1px solid ${i === activeIdx ? 'rgba(70,227,160,.4)' : 'var(--line-2)'}` }}
          >
            <span style={{ fontSize: 12, color: 'var(--tx-dim)', width: 18 }}>{i + 1}</span>
            <Field label="名稱"><input style={{ ...inp, width: 110 }} value={p.title ?? ''} onChange={(e) => patch(i, { title: e.target.value })} placeholder="如：起點公園" /></Field>
            <Field label={p.lat ? '緯度 lat' : '緯度 lat ⚠未填'}><input style={{ ...inp, width: 110, ...(p.lat ? {} : missingInp) }} type="number" value={p.lat || ''} onChange={(e) => patch(i, { lat: parseFloat(e.target.value) || 0 })} placeholder="貼上緯度" /></Field>
            <Field label={p.lng ? '經度 lng' : '經度 lng ⚠未填'}><input style={{ ...inp, width: 110, ...(p.lng ? {} : missingInp) }} type="number" value={p.lng || ''} onChange={(e) => patch(i, { lng: parseFloat(e.target.value) || 0 })} placeholder="貼上經度" /></Field>
            <Field label="半徑(m)"><input style={{ ...inp, width: 70 }} type="number" value={p.radius_m || ''} onChange={(e) => patch(i, { radius_m: parseInt(e.target.value, 10) || 0 })} placeholder="20" /></Field>
            <button type="button" onClick={(e) => { e.stopPropagation(); remove(i) }} style={removeBtn}>移除</button>
          </div>
        ))}
      </div>
      {points.some((p) => !p.lat || !p.lng) && (
        <div style={{ fontSize: 11.5, color: 'var(--hunt)', marginTop: 6 }}>⚠ 有打卡點的座標未填（灰字為提示，不是實際值）。請填入緯度/經度，未填的打卡點不會被儲存。</div>
      )}

      {showMap && (
        <div style={{ marginTop: 8 }}>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 4 }}>點地圖即可設定「目前選取（綠框）」打卡點的座標；先點上方某列選取它，再點地圖。</div>
          <div id="cp-edit-map" style={{ width: '100%', height: 240, borderRadius: 8, background: 'var(--bg-2)' }} />
        </div>
      )}
    </div>
  )
}

const smallBtn: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8,
  padding: '5px 10px', color: 'var(--tx)', fontSize: 12, cursor: 'pointer',
}

// 配速輸入：顯示「分:秒」、對外回傳秒數。編輯時用本地字串，失焦時正規化顯示。
function PaceInput({ valueSec, onChangeSec }: { valueSec?: number | null; onChangeSec: (s: number | null) => void }) {
  const [txt, setTxt] = useState(paceToStr(valueSec))
  return (
    <input
      style={{ ...inp, width: 96 }}
      value={txt}
      placeholder="5:00"
      onChange={(e) => { setTxt(e.target.value); onChangeSec(parsePace(e.target.value)) }}
      onBlur={() => setTxt(paceToStr(parsePace(txt)))}
    />
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
const missingInp: React.CSSProperties = { borderColor: 'var(--hunt)', background: 'rgba(255,75,92,.07)' }
const removeBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--hunt)', cursor: 'pointer', fontSize: 13, padding: '8px 4px',
}
const hint: React.CSSProperties = { fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }
