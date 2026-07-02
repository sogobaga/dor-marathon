'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminInterstitialApi, adminAppSettingsApi, adminImagesApi, type InterstitialAd } from '@/lib/api'
import { CTA_PRESETS } from '@/lib/interstitial'
import { getToken, clearToken } from '@/lib/adminAuth'

const blankAd = (order: number): InterstitialAd => ({ enabled: true, sort_order: order, image_url: '', headline: '', description: '', cta_label: '', cta_url: '' })

export default function AdminInterstitialPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [ads, setAds] = useState<InterstitialAd[]>([])
  const [master, setMaster] = useState(false)
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    Promise.all([adminInterstitialApi.list(t), adminAppSettingsApi.list(t)])
      .then(([r, s]) => { setAds(r.ads || []); setMaster((s.settings?.interstitial_enabled || '0') === '1') })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「系統設定」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  function patch(i: number, p: Partial<InterstitialAd>) { setAds((prev) => prev.map((a, j) => (j === i ? { ...a, ...p } : a))) }

  async function toggleMaster(on: boolean) {
    if (!token) return
    setMaster(on); setErr(''); setMsg('')
    try { await adminAppSettingsApi.set(token, 'interstitial_enabled', on ? '1' : '0'); setMsg(on ? '✓ 已開啟蓋板廣告' : '已關閉蓋板廣告') }
    catch (e: any) { setErr(e?.message || '更新失敗'); setMaster(!on) }
  }

  async function uploadImg(i: number, file: File) {
    if (!token) return
    setBusy(`img${i}`); setErr('')
    try { const { url } = await adminImagesApi.upload(token, file); patch(i, { image_url: url }) }
    catch (e: any) { setErr(e?.message || '上傳失敗') } finally { setBusy('') }
  }

  async function save(i: number) {
    if (!token) return
    const a = ads[i]
    if (!a.image_url) { setErr('請先上傳圖片'); return }
    setBusy(`save${i}`); setErr(''); setMsg('')
    try {
      if (a.id) { await adminInterstitialApi.update(token, a.id, a) }
      else { const { id } = await adminInterstitialApi.create(token, a); setAds((prev) => prev.map((x) => (x === a ? { ...x, id } : x))) }
      setMsg('✓ 已儲存')
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy('') }
  }

  async function remove(i: number) {
    if (!token) return
    const a = ads[i]
    setBusy(`del${i}`); setErr('')
    try {
      if (a.id) await adminInterstitialApi.remove(token, a.id)
      setAds((prev) => prev.filter((_, j) => j !== i)); setMsg('已刪除')
    } catch (e: any) { setErr(e?.message || '刪除失敗') } finally { setBusy('') }
  }

  return (
    <div style={{ maxWidth: 820, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>蓋板廣告</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 12px', lineHeight: 1.7 }}>
        前台開啟時彈出的「拍立得卡片」蓋板廣告。可放多張，會以疊卡呈現，使用者左右滑動看下一張、滑完自動關閉。
        每張：上傳圖片、寫標語、描述、CTA。前台每次開啟只彈一次；使用者可勾「本日不再顯示」（跨 00:00 重置）。
      </p>

      <label style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '12px 14px', marginBottom: 14, cursor: 'pointer' }}>
        <input type="checkbox" checked={master} onChange={(e) => toggleMaster(e.target.checked)} style={{ width: 18, height: 18 }} />
        <span style={{ fontWeight: 800, fontSize: 15 }}>啟用蓋板廣告（總開關）</span>
        <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>關閉時前台完全不彈出；開啟後只顯示下方「已啟用」且有圖的卡片。</span>
      </label>

      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        {ads.map((a, i) => (
          <div key={a.id || `new${i}`} style={card}>
            <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
              {/* 拍立得預覽 */}
              <div style={{ flexShrink: 0 }}>
                <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginBottom: 4 }}>預覽</div>
                <div style={polaroid}>
                  <div style={{ ...polaroidImg, backgroundColor: a.image_url ? '#000' : 'var(--bg-2)', backgroundImage: a.image_url ? `url("${a.image_url}")` : undefined }}>
                    {!a.image_url && <span style={{ color: 'var(--tx-faint)', fontSize: 11 }}>未上傳圖片</span>}
                  </div>
                  <div style={{ padding: '10px 8px 4px', textAlign: 'center' }}>
                    <div style={{ fontSize: 13, fontWeight: 800, color: '#2a2a2a' }}>{a.headline || '（標語）'}</div>
                    {a.description && <div style={{ fontSize: 10.5, color: '#7a7a7a', marginTop: 2 }}>{a.description}</div>}
                    {a.cta_label && <div style={{ fontSize: 11, color: '#4169aa', marginTop: 4, fontWeight: 700 }}>{a.cta_label} →</div>}
                  </div>
                </div>
              </div>

              {/* 欄位 */}
              <div style={{ flex: 1, minWidth: 260, display: 'flex', flexDirection: 'column', gap: 8 }}>
                <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                  <label style={{ ...primaryBtn, opacity: busy === `img${i}` ? 0.5 : 1 }}>
                    {busy === `img${i}` ? '上傳中…' : (a.image_url ? '更換圖片' : '上傳圖片')}
                    <input type="file" accept="image/*" style={{ display: 'none' }} disabled={busy === `img${i}`}
                      onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadImg(i, f); e.target.value = '' }} />
                  </label>
                  <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>建議直式 4:5、≥ 800px 寬</span>
                </div>
                <Field label="標語（照片下方大字）"><input style={inp} value={a.headline} onChange={(e) => patch(i, { headline: e.target.value })} /></Field>
                <Field label="描述（小字，可留白）"><input style={inp} value={a.description} onChange={(e) => patch(i, { description: e.target.value })} /></Field>
                <Field label="CTA 文字">
                  <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'center' }}>
                    <input style={{ ...inp, width: 150 }} value={a.cta_label} onChange={(e) => patch(i, { cta_label: e.target.value })} placeholder="如：了解更多" />
                    {CTA_PRESETS.map((p) => (
                      <button key={p.label} onClick={() => patch(i, { cta_label: p.label, cta_url: a.cta_url || p.url })} style={chip}>{p.label}</button>
                    ))}
                  </div>
                </Field>
                <Field label="CTA 連結（內部路徑如 /track，或外部網址；留白＝按了只關閉）"><input style={inp} value={a.cta_url} onChange={(e) => patch(i, { cta_url: e.target.value })} placeholder="/ 或 https://…" /></Field>
                <div style={{ display: 'flex', gap: 14, alignItems: 'center', flexWrap: 'wrap' }}>
                  <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                    排序 <input type="number" style={{ ...inp, width: 70 }} value={a.sort_order} onChange={(e) => patch(i, { sort_order: parseInt(e.target.value) || 0 })} />
                  </label>
                  <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                    <input type="checkbox" checked={a.enabled} onChange={(e) => patch(i, { enabled: e.target.checked })} /> 啟用這張
                  </label>
                  <div style={{ flex: 1 }} />
                  <button onClick={() => save(i)} disabled={busy === `save${i}`} style={{ ...primaryBtn, opacity: busy === `save${i}` ? 0.5 : 1 }}>儲存</button>
                  <button onClick={() => remove(i)} disabled={busy === `del${i}`} style={{ ...ghostBtn, color: 'var(--hunt)' }}>刪除</button>
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>

      <button onClick={() => setAds((prev) => [...prev, blankAd(prev.length)])} style={{ ...ghostBtn, marginTop: 14 }}>＋ 新增一張蓋板</button>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14 }
const polaroid: React.CSSProperties = { width: 150, background: '#fff', borderRadius: 8, padding: 8, boxShadow: '0 6px 18px rgba(0,0,0,.35)' }
const polaroidImg: React.CSSProperties = { width: '100%', aspectRatio: '4 / 5', borderRadius: 4, display: 'flex', alignItems: 'center', justifyContent: 'center', overflow: 'hidden', backgroundSize: 'cover', backgroundPosition: 'center' }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 14px', cursor: 'pointer', fontSize: 12.5, display: 'inline-block' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 14px', cursor: 'pointer', fontSize: 12.5 }
const chip: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '5px 10px', cursor: 'pointer', fontSize: 12 }
