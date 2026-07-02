'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminEffectsApi, adminImagesApi } from '@/lib/api'
import { EFFECT_SPECS, type EffectSpec } from '@/lib/effects'
import { getToken, clearToken } from '@/lib/adminAuth'
import { unlockAudio, playEventAlert, playEventComplete, playTapHit, playDefend, playDing } from '@/lib/sfx'

const SYNTH: Record<string, () => void> = {
  'sound.event_alert': playEventAlert,
  'sound.event_complete': playEventComplete,
  'sound.tap_hit': playTapHit,
  'sound.defend': playDefend,
  'sound.ding': playDing,
}

export default function AdminEffectsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [assets, setAssets] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

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
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14 }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, display: 'inline-block' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5 }
