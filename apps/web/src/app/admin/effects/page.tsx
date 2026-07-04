'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminEffectsApi, adminImagesApi } from '@/lib/api'
import { EFFECT_SPECS, type EffectSpec } from '@/lib/effects'
import { getToken, clearToken } from '@/lib/adminAuth'
import { unlockAudio, playEventAlert, playEventAlarm, playEventComplete, playTapHit, playDefend, playDing } from '@/lib/sfx'

const SYNTH: Record<string, () => void> = {
  'sound.event_alarm': playEventAlarm,
  'sound.event_alert': playEventAlert,
  'sound.event_complete': playEventComplete,
  'sound.tap_hit': playTapHit,
  'sound.defend': playDefend,
  'sound.ding': playDing,
}

// 圖形魔法「導引虛線縮放」：預設百分比 = lib/shapes.ts SHAPE_GUIDE_SCALE ×100，量測自現行底圖
const SHAPE_SCALE_ROWS = [
  { k: 3, label: '△ 三角形', def: 56 },
  { k: 4, label: '◇ 四角形', def: 58 },
  { k: 5, label: '✦ 五芒星', def: 59 },
]
// 把儲存值（百分比或比例字串）正規化為整數百分比顯示；無效/未設回傳 null
function storedPct(raw?: string): number | null {
  if (!raw || !raw.trim()) return null
  let n = parseFloat(raw)
  if (!isFinite(n) || n <= 0) return null
  if (n <= 1.5) n *= 100
  return Math.round(n)
}

export default function AdminEffectsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [assets, setAssets] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [scaleEdit, setScaleEdit] = useState<Record<number, string>>({ 3: '', 4: '', 5: '' })
  const scaleSeeded = useRef(false)

  // 只在「首次載入到 assets」時用伺服器值填入輸入框；之後不再因 assets 變動而覆寫，
  // 以免其他操作（上傳圖片/音效、儲存另一列）觸發 setAssets 時洗掉使用者正在輸入但未儲存的值。
  // 各列自己儲存/還原後，由該列處理常式單獨同步（見 saveScale/clearScale）。
  useEffect(() => {
    if (scaleSeeded.current || Object.keys(assets).length === 0) return
    scaleSeeded.current = true
    setScaleEdit((prev) => {
      const next = { ...prev }
      for (const row of SHAPE_SCALE_ROWS) {
        const p = storedPct(assets[`interaction.shape.scale${row.k}`])
        next[row.k] = p == null ? '' : String(p)
      }
      return next
    })
  }, [assets])

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminEffectsApi.list(t).then((r) => setAssets(r.assets || {})).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('無「事件任務」權限')
      else setErr(e?.message || '載入失敗')
    })
  }, [router])
  useEffect(() => { load() }, [load])

  async function upload(spec: EffectSpec, file: File) {
    if (!token) return
    setBusy(spec.slug); setErr(''); setMsg('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      const r = await adminEffectsApi.set(token, spec.slug, url)
      setAssets(r.assets || {}); setMsg(`✓ 已更新「${spec.label}」`)
    } catch (e: any) { setErr(e?.message || '上傳失敗') } finally { setBusy('') }
  }
  async function clearAsset(spec: EffectSpec) {
    if (!token) return
    setBusy(spec.slug); setErr('')
    try { const r = await adminEffectsApi.clear(token, spec.slug); setAssets(r.assets || {}); setMsg(`已還原「${spec.label}」為暫代`) }
    catch (e: any) { setErr(e?.message || '還原失敗') } finally { setBusy('') }
  }
  async function saveScale(k: number, def: number) {
    if (!token) return
    const slug = `interaction.shape.scale${k}`
    const raw = (scaleEdit[k] ?? '').trim()
    setBusy(slug); setErr(''); setMsg('')
    try {
      if (raw === '') {
        const r = await adminEffectsApi.clear(token, slug); setAssets(r.assets || {}); setScaleEdit((s) => ({ ...s, [k]: '' })); setMsg(`已還原導引縮放為預設 ${def}%`)
      } else {
        const n = Math.min(100, Math.max(20, Math.round(parseFloat(raw) || def)))
        const r = await adminEffectsApi.set(token, slug, String(n)); setAssets(r.assets || {}); setScaleEdit((s) => ({ ...s, [k]: String(n) })); setMsg(`✓ 導引縮放已設為 ${n}%`)
      }
    } catch (e: any) { setErr(e?.message || '更新失敗') } finally { setBusy('') }
  }
  async function clearScale(k: number, def: number) {
    if (!token) return
    const slug = `interaction.shape.scale${k}`
    setBusy(slug); setErr(''); setMsg('')
    try { const r = await adminEffectsApi.clear(token, slug); setAssets(r.assets || {}); setScaleEdit((s) => ({ ...s, [k]: '' })); setMsg(`已還原導引縮放為預設 ${def}%`) }
    catch (e: any) { setErr(e?.message || '還原失敗') } finally { setBusy('') }
  }
  function playSynth(slug: string) { unlockAudio(); SYNTH[slug]?.() }
  function playFile(url: string) { unlockAudio(); try { new Audio(url).play() } catch { /* ignore */ } }

  const cats = Array.from(new Set(EFFECT_SPECS.map((s) => s.category)))

  return (
    <div style={{ maxWidth: 900, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>效果管理</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 6px' }}>
        目前的圖示與音效為「程式暫代」（emoji／合成音）。這裡可逐一換成正式素材：上傳後前台跑步引擎會立即改用，隨時可還原。
      </p>
      <div style={{ background: 'rgba(255,194,75,.1)', border: '1px solid rgba(255,194,75,.35)', borderRadius: 10, padding: '9px 12px', fontSize: 12.5, color: 'var(--gold)', marginBottom: 14, lineHeight: 1.6 }}>
        💡 事件橫幅插圖不在此頁：那些在「事件任務／多人事件」各自的編輯頁上傳（含白天/黃昏/晚上時段圖）。此頁是「圖示、特效、音效」等共用效果。
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      {cats.map((cat) => (
        <div key={cat} style={{ marginTop: 18 }}>
          <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)', letterSpacing: '.05em', marginBottom: 8 }}>{cat}</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {EFFECT_SPECS.filter((s) => s.category === cat).map((spec) => {
              const cur = assets[spec.slug] || ''
              const isImg = spec.type === 'image'
              return (
                <div key={spec.slug} style={card}>
                  <div style={{ display: 'flex', gap: 14, alignItems: 'flex-start', flexWrap: 'wrap' }}>
                    {/* 目前暫代 */}
                    <div style={{ textAlign: 'center', minWidth: 72 }}>
                      <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginBottom: 4 }}>目前暫代</div>
                      {isImg
                        ? <div style={{ width: 64, height: 64, borderRadius: 10, background: 'var(--bg-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 30 }}>{spec.placeholder}</div>
                        : <button onClick={() => playSynth(spec.slug)} style={{ ...ghostBtn, fontSize: 12 }}>▶ 試聽合成音</button>}
                    </div>
                    {/* 資訊 */}
                    <div style={{ flex: 1, minWidth: 200 }}>
                      <div style={{ fontWeight: 800, fontSize: 15 }}>{spec.label}</div>
                      <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>{spec.where}</div>
                      <div style={{ fontSize: 12, color: 'var(--fug)', marginTop: 6, lineHeight: 1.6 }}>
                        建議：{spec.size}・{spec.format}・檔案 ≤ {spec.maxKB}KB
                      </div>
                      {spec.note && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 3 }}>※ {spec.note}</div>}
                      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 3, fontFamily: 'monospace' }}>slug: {spec.slug}</div>
                    </div>
                    {/* 正式素材 + 操作 */}
                    <div style={{ textAlign: 'center', minWidth: 120 }}>
                      <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginBottom: 4 }}>正式素材</div>
                      {cur
                        ? (isImg
                          ? <img src={cur} alt="" style={{ width: 64, height: 64, objectFit: 'contain', borderRadius: 10, border: '1px solid var(--line-2)', background: 'var(--bg-2)' }} />
                          : <button onClick={() => playFile(cur)} style={{ ...ghostBtn, fontSize: 12 }}>▶ 試聽正式音</button>)
                        : <div style={{ width: 64, height: 64, borderRadius: 10, border: '1px dashed var(--line-2)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', color: 'var(--tx-faint)', fontSize: 11 }}>未設定</div>}
                      <div style={{ display: 'flex', gap: 6, marginTop: 6, justifyContent: 'center' }}>
                        <label style={{ ...primaryBtn, opacity: busy === spec.slug ? 0.5 : 1 }}>
                          {busy === spec.slug ? '上傳中…' : (cur ? '更換' : '上傳')}
                          <input type="file" accept={isImg ? 'image/*' : 'audio/*'} style={{ display: 'none' }} disabled={busy === spec.slug}
                            onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(spec, f); e.target.value = '' }} />
                        </label>
                        {cur && <button onClick={() => clearAsset(spec)} disabled={busy === spec.slug} style={{ ...ghostBtn, color: 'var(--hunt)' }}>還原</button>}
                      </div>
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      ))}

      {/* 圖形魔法・導引縮放 */}
      <div style={{ marginTop: 22 }}>
        <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)', letterSpacing: '.05em', marginBottom: 8 }}>圖形魔法・導引縮放</div>
        <div style={card}>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.7, marginBottom: 4 }}>
            「畫圖形」時導引虛線的大小（外頂點占畫布半徑的比例）。若換了魔法陣底圖後，虛線比底圖線條偏大或偏小，
            在這裡微調到<b>剛好貼齊底圖線條</b>即可（玩家沿虛線描 = 描在底圖上 = 容易滿分）。
          </div>
          <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', lineHeight: 1.6, marginBottom: 6 }}>
            留空＝用內建預設（△56 / ◇58 / ✦59 %，量測自目前底圖）。範圍 20–100%。圖形辨識為大小無關，調整只影響導引外觀、不影響評分。
          </div>
          {SHAPE_SCALE_ROWS.map((row) => {
            const slug = `interaction.shape.scale${row.k}`
            const cur = storedPct(assets[slug])
            return (
              <div key={row.k} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 0', borderTop: '1px solid var(--line)', flexWrap: 'wrap' }}>
                <span style={{ width: 84, fontWeight: 700, fontSize: 14 }}>{row.label}</span>
                <input type="number" min={20} max={100} value={scaleEdit[row.k] ?? ''} placeholder={`預設 ${row.def}`}
                  onChange={(e) => setScaleEdit((s) => ({ ...s, [row.k]: e.target.value }))}
                  style={{ ...inputStyle, width: 88 }} />
                <span style={{ color: 'var(--tx-faint)', fontSize: 12 }}>%</span>
                <button onClick={() => saveScale(row.k, row.def)} disabled={busy === slug} style={{ ...primaryBtn, opacity: busy === slug ? 0.5 : 1 }}>儲存</button>
                {cur != null && <button onClick={() => clearScale(row.k, row.def)} disabled={busy === slug} style={{ ...ghostBtn, color: 'var(--hunt)' }}>還原預設</button>}
                <span style={{ fontSize: 11.5, color: cur != null ? 'var(--gold)' : 'var(--tx-faint)' }}>{cur != null ? `目前 ${cur}%（自訂）` : `使用預設 ${row.def}%`}</span>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14 }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, display: 'inline-block' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5 }
const inputStyle: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit' }
