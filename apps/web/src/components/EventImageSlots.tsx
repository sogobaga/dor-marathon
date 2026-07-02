'use client'

import { useState } from 'react'
import { adminImagesApi } from '@/lib/api'

// 事件插圖（預設 + 白天/黃昏/晚上）上傳格。事件任務 / 多人事件共用，前台依跑者當下時間顯示對應時段圖。
export type EventImages = { image_url?: string; image_day_url?: string; image_dusk_url?: string; image_night_url?: string }

const SLOTS = [
  { field: 'image_url', label: '預設圖', emoji: '🖼️', hint: '（時段未設定時）' },
  { field: 'image_day_url', label: '白天', emoji: '☀️', hint: '06–17' },
  { field: 'image_dusk_url', label: '黃昏', emoji: '🌆', hint: '17–19' },
  { field: 'image_night_url', label: '晚上', emoji: '🌙', hint: '19–06' },
] as const

export default function EventImageSlots({ value, token, onChange }: {
  value: EventImages
  token: string | null
  onChange: (patch: EventImages) => void
}) {
  const [uploadingField, setUploadingField] = useState('')
  const [err, setErr] = useState('')

  async function upload(field: keyof EventImages, file: File) {
    if (!token) return
    setUploadingField(field); setErr('')
    try { const { url } = await adminImagesApi.upload(token, file); onChange({ [field]: url }) }
    catch (e: any) { setErr(e?.message || '圖片上傳失敗') } finally { setUploadingField('') }
  }

  return (
    <div>
      <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--tx)', marginBottom: 2 }}>事件插圖（前台橫幅顯示）</div>
      <div style={{ fontSize: 11, color: 'var(--gold)', marginBottom: 8, lineHeight: 1.6 }}>
        建議尺寸 <strong>800 × 400 px（2:1 橫幅，JPG/PNG）</strong>；前台會等比裁切為滿版、約 120px 高。<br />
        可分時段上圖：跑者當下時間落在哪個時段就顯示對應圖，該時段未設定則回退「預設圖」。
      </div>
      {err && <div style={{ color: 'var(--hunt)', fontSize: 12, marginBottom: 6 }}>{err}</div>}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: 10 }}>
        {SLOTS.map((s) => {
          const url = (value[s.field] as string) || ''
          return (
            <div key={s.field} style={{ border: '1px solid var(--line-2)', borderRadius: 10, padding: 10 }}>
              <div style={{ fontSize: 12, fontWeight: 700, marginBottom: 6 }}>{s.emoji} {s.label} <span style={{ color: 'var(--tx-faint)', fontWeight: 400 }}>{s.hint}</span></div>
              {url
                ? <img src={url} alt="" style={{ width: '100%', height: 74, objectFit: 'cover', borderRadius: 8, border: '1px solid var(--line-2)' }} />
                : <div style={{ width: '100%', height: 74, borderRadius: 8, border: '1px dashed var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--tx-faint)', fontSize: 12 }}>未設定</div>}
              <div style={{ display: 'flex', gap: 6, marginTop: 6 }}>
                <label style={{ ...ghostBtn, display: 'inline-block' }}>
                  {uploadingField === s.field ? '上傳中…' : (url ? '更換' : '上傳')}
                  <input type="file" accept="image/*" style={{ display: 'none' }} disabled={!!uploadingField}
                    onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(s.field, f); e.target.value = '' }} />
                </label>
                {url && <button onClick={() => onChange({ [s.field]: '' })} style={{ ...ghostBtn, color: 'var(--hunt)' }}>移除</button>}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 10px', cursor: 'pointer', fontSize: 12 }
