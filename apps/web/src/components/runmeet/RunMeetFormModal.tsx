'use client'

import { useMemo, useRef, useState } from 'react'
import { runMeetApi, type RunMeetDetail, type RunMeetInput, type RunMeetMemberDetail, type RunMeetQuota } from '@/lib/api'
import { withUserAuth } from '@/lib/userAuth'
import { fmtMeetAtConfirm, isDeviceTaipei, isoToTaipeiLocalInput, taipeiLocalToISO, taipeiParts } from '@/lib/runMeet'
import RunMeetConfirmModal from './RunMeetConfirmModal'
import RunMeetLocationPicker, { type LocationValue } from './RunMeetLocationPicker'
import {
  RunMeetModal, errText, fieldHint, fieldLabel, ghostBtn, inputStyle, modalTitle,
  primaryBtn, textareaStyle, tinyBtn,
} from './ui'

// 建立／編輯團練表單。
// - 圖片張數：建立用 quota.image_limit（當下 VIP 快照）、編輯用該團 image_limit（建立當下的快照，
//   刻意不即時判定——VIP 用 4 張建團後到期，只想改人數上限時不該被 400 擋死）。
// - 送出（建立）前一定會跳確認彈窗（需求指定），文案見 RunMeetConfirmModal。
// - client_token：本表單開啟時產生一次，重試沿用同一個 → 後端部分唯一索引擋重複扣配額。

const pad2 = (n: number) => String(n).padStart(2, '0')

/** 預設集合時間：明天 06:00（台北牆上時間）。 */
function defaultMeetAtInput(now = new Date()): string {
  const t = taipeiParts(new Date(now.getTime() + 24 * 3600 * 1000))
  return `${t.y}-${pad2(t.m)}-${pad2(t.d)}T06:00`
}
function nowTaipeiInput(now = new Date()): string {
  const t = taipeiParts(now)
  return `${t.y}-${pad2(t.m)}-${pad2(t.d)}T${pad2(t.hh)}:${pad2(t.mm)}`
}
function maxTaipeiInput(now = new Date()): string {
  const t = taipeiParts(new Date(now.getTime() + 90 * 24 * 3600 * 1000))
  return `${t.y}-${pad2(t.m)}-${pad2(t.d)}T${pad2(t.hh)}:${pad2(t.mm)}`
}

type PwMode = 'keep' | 'set' | 'remove'

export default function RunMeetFormModal({
  mode, initial, quota, onClose, onSaved, onLearnVip,
}: {
  mode: 'create' | 'edit'
  initial?: RunMeetMemberDetail
  quota: RunMeetQuota
  onClose: () => void
  // password 只在「建立且有設密碼」時回傳，供成功後的一次性分享文案使用（來自表單記憶體，
  // 絕不從 API 回讀——後端任何視角都不吐 hash 或明碼）。
  onSaved: (meet: RunMeetDetail, info: { created: boolean; remaining?: number; pendingKept?: number; password?: string }) => void
  onLearnVip?: () => void
}) {
  const [title, setTitle] = useState(initial?.title ?? '')
  const [meetAt, setMeetAt] = useState(initial ? isoToTaipeiLocalInput(initial.meet_at) : defaultMeetAtInput())
  const [loc, setLoc] = useState<LocationValue>({
    region: initial?.region ?? '',
    place_label: initial?.place_label ?? '',
    lat: initial?.lat ?? null,
    lng: initial?.lng ?? null,
    meeting_detail: initial?.meeting_detail ?? '',
  })
  const [capacity, setCapacity] = useState<number>(initial?.capacity ?? 10)
  const [description, setDescription] = useState(initial?.description ?? '')
  const [images, setImages] = useState<string[]>(initial?.image_urls ?? [])
  const [approval, setApproval] = useState<boolean>(initial?.approval_required ?? false)
  const [pwMode, setPwMode] = useState<PwMode>(mode === 'edit' ? 'keep' : 'set')
  const [pw, setPw] = useState('')
  const [imgBusy, setImgBusy] = useState(false)
  const [imgErr, setImgErr] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const clientToken = useRef<string>(typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : String(Date.now()))

  const imageLimit = mode === 'edit' ? (initial?.image_limit ?? 1) : quota.image_limit
  const capacityMax = quota.capacity_max || 50
  const memberCount = initial?.member_count ?? 1
  const isoMeetAt = useMemo(() => taipeiLocalToISO(meetAt), [meetAt])

  function validate(): string {
    const t = title.trim()
    if (t.length < 2 || t.length > 40) return '團練名稱請填 2 到 40 個字。'
    if (!isoMeetAt) return '預計時間必須晚於現在。'
    const ms = new Date(isoMeetAt).getTime() - Date.now()
    if (ms <= 0) return '預計時間必須晚於現在。'
    if (ms > 90 * 24 * 3600 * 1000) return '預計時間最多只能設定到 90 天後。'
    const region = loc.region.trim()
    if (region.length < 2 || region.length > 30) return '縣市・行政區請填 2 到 30 個字。'
    const place = loc.place_label.trim()
    if (place.length < 2 || place.length > 60) return '地點名稱請填 2 到 60 個字。'
    if (loc.meeting_detail.length > 200) return '集合細節最多 200 字。'
    if (!Number.isFinite(capacity) || capacity < 2 || capacity > capacityMax) return `人數上限請填 2 到 ${capacityMax} 人。`
    if (mode === 'edit' && capacity < memberCount) return `目前已有 ${memberCount} 位成員，人數上限不可低於 ${memberCount} 人。`
    if (description.length > 500) return '說明最多 500 字。'
    if (images.length > imageLimit) return `這個團練最多可上傳 ${imageLimit} 張圖片。`
    if (pwMode === 'set' && pw.trim() !== '' && (pw.length < 4 || pw.length > 32)) return '密碼請填 4 到 32 個字元。'
    return ''
  }

  function buildInput(): RunMeetInput {
    const input: RunMeetInput = {
      title: title.trim(),
      meet_at: isoMeetAt,
      region: loc.region.trim(),
      place_label: loc.place_label.trim(),
      lat: loc.lat,
      lng: loc.lng,
      meeting_detail: loc.meeting_detail.trim(),
      capacity,
      description: description.trim(),
      image_urls: images,
      approval_required: approval,
    }
    // 密碼語意：建立＝非空即私密團；編輯＝省略欄位不動、'' 移除、其他重設。
    if (mode === 'create') {
      if (pw.trim() !== '') input.password = pw
      input.client_token = clientToken.current
    } else if (pwMode === 'remove') {
      input.password = ''
    } else if (pwMode === 'set' && pw.trim() !== '') {
      input.password = pw
    }
    return input
  }

  function trySubmit() {
    const v = validate()
    if (v) { setErr(v); return }
    setErr('')
    if (mode === 'create') setConfirm(true) // 建立一定要先跳確認彈窗（會消耗發起次數）
    else void doSubmit()
  }

  async function doSubmit() {
    if (busy) return
    setBusy(true); setErr('')
    try {
      if (mode === 'create') {
        const res = await withUserAuth((t) => runMeetApi.create(t, buildInput()))
        onSaved(res.meet, { created: true, remaining: res.remaining, password: pw.trim() || undefined })
      } else {
        const res = await withUserAuth((t) => runMeetApi.update(t, initial!.id, buildInput()))
        onSaved(res.meet, { created: false, pendingKept: res.pending_kept })
      }
    } catch (e: any) {
      setErr(e?.message || '系統忙碌中，請稍後再試')
      setConfirm(false)
    } finally {
      setBusy(false)
    }
  }

  async function upload(file: File) {
    if (images.length >= imageLimit) { setImgErr(`這個團練最多可上傳 ${imageLimit} 張圖片。`); return }
    setImgBusy(true); setImgErr('')
    try {
      const { url } = await withUserAuth((t) => runMeetApi.uploadImage(t, file))
      setImages((arr) => [...arr, url])
    } catch (e: any) {
      setImgErr(e?.message || '圖片上傳失敗')
    } finally {
      setImgBusy(false)
    }
  }
  function moveImg(idx: number, dir: -1 | 1) {
    setImages((arr) => {
      const next = [...arr]
      const j = idx + dir
      if (j < 0 || j >= next.length) return arr
      ;[next[idx], next[j]] = [next[j], next[idx]]
      return next
    })
  }

  return (
    <>
      <RunMeetModal onClose={busy ? () => {} : onClose} dismissOnBackdrop={false} maxWidth={392}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <div style={{ ...modalTitle, flex: 1, textAlign: 'left' }}>{mode === 'create' ? '團練邀請建立' : '編輯團練'}</div>
          <button type="button" onClick={onClose} style={{ ...ghostBtn, padding: '5px 10px' }}>關閉</button>
        </div>

        {mode === 'create' && (
          <div style={{ ...fieldHint, marginTop: 8, color: quota.remaining > 0 ? 'var(--gold)' : 'var(--hunt)', fontWeight: 800, fontSize: 12 }}>
            本月剩餘 {quota.remaining} 次發起機會（已用 {quota.used}/{quota.cap}{quota.is_vip ? ' · VIP' : ''}）
          </div>
        )}

        {/* 名稱 */}
        <div style={{ marginTop: 14 }}>
          <label style={fieldLabel}>團練名稱</label>
          <input value={title} onChange={(e) => setTitle(e.target.value)} maxLength={40} placeholder="大安森林公園晨跑 10K" style={inputStyle} />
        </div>

        {/* 時間 */}
        <div style={{ marginTop: 12 }}>
          <label style={fieldLabel}>預計時間</label>
          <input
            type="datetime-local"
            value={meetAt}
            min={nowTaipeiInput()}
            max={maxTaipeiInput()}
            onChange={(e) => setMeetAt(e.target.value)}
            style={inputStyle}
          />
          {isoMeetAt && <div style={{ ...fieldHint, color: 'var(--fug)' }}>{fmtMeetAtConfirm(isoMeetAt)}</div>}
          {/* 團練一定是台灣的實體集合 → 一律以台北時間解讀，裝置時區不同時要講明白 */}
          {!isDeviceTaipei() && <div style={{ ...fieldHint, color: 'var(--tx-faint)' }}>你的裝置時區不是台北，上方時間會以台北時間儲存</div>}
        </div>

        {/* 地點（三層揭露） */}
        <div style={{ marginTop: 14 }}>
          <label style={fieldLabel}>集合地點</label>
          <RunMeetLocationPicker value={loc} onChange={(patch) => setLoc((v) => ({ ...v, ...patch }))} />
        </div>

        {/* 人數上限 */}
        <div style={{ marginTop: 14 }}>
          <label style={fieldLabel}>人數上限（含你自己）</label>
          <input
            type="number" inputMode="numeric" min={2} max={capacityMax}
            value={capacity}
            onChange={(e) => setCapacity(Number(e.target.value))}
            style={{ ...inputStyle, width: 120 }}
          />
          <div style={fieldHint}>2 到 {capacityMax} 人。{mode === 'edit' ? `目前已有 ${memberCount} 位成員，不可低於此數。` : ''}</div>
        </div>

        {/* 說明 */}
        <div style={{ marginTop: 12 }}>
          <label style={fieldLabel}>說明（選填）</label>
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} maxLength={500} placeholder="輕鬆配速 6:30，跑完一起吃早餐。新手歡迎！" style={textareaStyle} />
          <div style={fieldHint}>{description.length} / 500 字。純文字，不支援 HTML 與連結預覽。</div>
        </div>

        {/* 圖片 */}
        <div style={{ marginTop: 14 }}>
          <label style={fieldLabel}>圖片（{images.length} / {imageLimit}）</label>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            {images.map((url, i) => (
              <div key={url} style={{ width: 96, border: '1px solid var(--line-2)', borderRadius: 10, overflow: 'hidden' }}>
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={url} alt="" style={{ width: '100%', height: 62, objectFit: 'cover', display: 'block' }} />
                <div style={{ display: 'flex', gap: 3, padding: 4 }}>
                  <button type="button" onClick={() => moveImg(i, -1)} style={{ ...tinyBtn, padding: '3px 6px' }}>←</button>
                  <button type="button" onClick={() => moveImg(i, 1)} style={{ ...tinyBtn, padding: '3px 6px' }}>→</button>
                  <button type="button" onClick={() => setImages((arr) => arr.filter((_, j) => j !== i))} style={{ ...tinyBtn, padding: '3px 6px', color: 'var(--hunt)' }}>✕</button>
                </div>
              </div>
            ))}
            {images.length < imageLimit && (
              <label style={{ ...ghostBtn, display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: 96, height: 92, boxSizing: 'border-box', cursor: imgBusy ? 'default' : 'pointer' }}>
                {imgBusy ? '上傳中…' : '＋ 上傳'}
                {/* 與後端白名單一致：只收 JPG/PNG（不寫 image/*）；選完清 value 才能重選同一檔 */}
                <input
                  type="file" accept="image/jpeg,image/png" style={{ display: 'none' }} disabled={imgBusy}
                  onChange={(e) => { const f = e.target.files?.[0]; if (f) void upload(f); e.target.value = '' }}
                />
              </label>
            )}
          </div>
          {imgErr && <div style={errText}>{imgErr}</div>}
          <div style={fieldHint}>
            只接受 JPG／PNG，單張 5MB 以內；上傳後會重新編碼（自動移除 EXIF 含 GPS 座標）。<br />
            圖片連結為公開連結，請勿上傳含個資或不願外流的影像。
            {!quota.is_vip && mode === 'create' && (
              <>
                <br />VIP 每個團練可上傳 {quota.vip_image_limit} 張圖片
                {onLearnVip && <button type="button" onClick={onLearnVip} style={{ background: 'none', border: 'none', color: 'var(--gold)', fontWeight: 800, fontSize: 11, cursor: 'pointer', padding: '0 0 0 6px', fontFamily: 'inherit' }}>了解 VIP ›</button>}
              </>
            )}
          </div>
        </div>

        {/* 審核開關 */}
        <div style={{ marginTop: 14 }}>
          <label style={fieldLabel}>加入方式</label>
          <div style={{ display: 'flex', gap: 8 }}>
            <button type="button" onClick={() => setApproval(false)} style={{ ...ghostBtn, flex: 1, borderColor: approval ? 'var(--line-2)' : 'var(--fug)', color: approval ? 'var(--tx-dim)' : 'var(--fug)' }}>⚡ 自由加入</button>
            <button type="button" onClick={() => setApproval(true)} style={{ ...ghostBtn, flex: 1, borderColor: approval ? 'var(--fug)' : 'var(--line-2)', color: approval ? 'var(--fug)' : 'var(--tx-dim)' }}>⏳ 需要審核</button>
          </div>
          <div style={fieldHint}>
            {approval ? '申請後由你決定要不要放行，同意後才佔用名額。' : '人數上限內，任何人都可以直接加入。'}
            {mode === 'edit' && !approval && initial && initial.pending_count > 0 && (
              <div style={{ color: 'var(--gold)', marginTop: 4 }}>目前仍有 {initial.pending_count} 筆待審核申請，切換後不會自動通過，請記得手動處理。</div>
            )}
          </div>
        </div>

        {/* 密碼（私密團） */}
        <div style={{ marginTop: 14 }}>
          <label style={fieldLabel}>加入密碼（選填）</label>
          {mode === 'edit' && (
            <div style={{ display: 'flex', gap: 6, marginBottom: 8, flexWrap: 'wrap' }}>
              <button type="button" onClick={() => setPwMode('keep')} style={{ ...tinyBtn, borderColor: pwMode === 'keep' ? 'var(--fug)' : 'var(--line-2)' }}>不變更</button>
              <button type="button" onClick={() => setPwMode('set')} style={{ ...tinyBtn, borderColor: pwMode === 'set' ? 'var(--fug)' : 'var(--line-2)' }}>{initial?.is_private ? '重設密碼' : '設定密碼'}</button>
              {initial?.is_private && <button type="button" onClick={() => setPwMode('remove')} style={{ ...tinyBtn, borderColor: pwMode === 'remove' ? 'var(--hunt)' : 'var(--line-2)', color: pwMode === 'remove' ? 'var(--hunt)' : 'var(--tx)' }}>移除密碼（改公開）</button>}
            </div>
          )}
          {pwMode === 'set' && (
            <input value={pw} onChange={(e) => setPw(e.target.value)} maxLength={32} placeholder="留白＝公開團練" autoComplete="new-password" style={inputStyle} />
          )}
          <div style={fieldHint}>
            {pwMode === 'remove'
              ? '移除密碼後，任何人都能直接加入。'
              : '留白＝公開團練。密碼只用來限制加入；團練名稱、時間、地點與說明仍會顯示在探索列表。'}
            <br />建議 6 碼以上，勿與登入密碼相同。
            {mode === 'edit' && pwMode === 'set' && <><br />重設密碼後，先前用舊密碼解鎖但尚未加入的跑者需要重新輸入。已加入的團員不受影響。</>}
          </div>
        </div>

        {err && <div style={errText}>{err}</div>}

        <button type="button" onClick={trySubmit} disabled={busy} style={{ ...primaryBtn, marginTop: 18, opacity: busy ? 0.7 : 1 }}>
          {busy ? '處理中…' : mode === 'create' ? '建立團練' : '儲存變更'}
        </button>
        {mode === 'create' && <div style={{ ...fieldHint, textAlign: 'center' }}>按下後會再跳一次確認，確認才會消耗發起次數。</div>}
      </RunMeetModal>

      {confirm && (
        <RunMeetConfirmModal
          remaining={quota.remaining}
          resetsAt={quota.resets_at}
          isVip={quota.is_vip}
          vipCap={quota.vip_cap}
          vipImages={quota.vip_image_limit}
          busy={busy}
          onCancel={() => setConfirm(false)}
          onConfirm={() => void doSubmit()}
          onLearnVip={onLearnVip}
        />
      )}
    </>
  )
}
