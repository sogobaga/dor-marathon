'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminPartnersApi, adminImagesApi, type AdminPartnerShop, type PartnerShopWriteBody } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 跑者充電站後台：特約商店（Partner Shops）CRUD——基本資訊、Banner/多圖、詳細內文(HTML)、YouTube 影片、前往連結、排序、上下架。

type Form = Partial<AdminPartnerShop>

const EMPTY: Form = {
  name: '', summary: '', banner_url: '', detail_html: '', photo_urls: [], video_url: '',
  cta_url: '', cta_label: '', display_order: 0, enabled: true,
}

export default function AdminPartnersPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [shops, setShops] = useState<AdminPartnerShop[] | null>(null)
  const [form, setForm] = useState<Form>(EMPTY)
  const [busy, setBusy] = useState(false)
  const [imgBusy, setImgBusy] = useState('') // '' | 'banner' | 'photo'
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminPartnersApi.list(t)
      .then((r) => setShops([...r.shops].sort((a, b) => a.display_order - b.display_order || a.name.localeCompare(b.name))))
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「跑者充電站」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  function edit(s: AdminPartnerShop) { setForm({ ...s }); setMsg(''); setErr('') }
  function fresh() { setForm({ ...EMPTY, photo_urls: [] }); setMsg(''); setErr('') }
  function setF<K extends keyof Form>(k: K, v: Form[K]) { setForm((f) => ({ ...f, [k]: v })) }

  async function uploadBanner(file: File) {
    if (!token) return
    setImgBusy('banner'); setErr('')
    try { const { url } = await adminImagesApi.upload(token, file); setF('banner_url', url); setMsg('✓ Banner 已上傳') }
    catch (e: any) { setErr(e?.message || '上傳失敗') } finally { setImgBusy('') }
  }
  async function addPhoto(file: File) {
    if (!token) return
    setImgBusy('photo'); setErr('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      setF('photo_urls', [...(form.photo_urls || []), url])
      setMsg('✓ 圖片已新增')
    } catch (e: any) { setErr(e?.message || '上傳失敗') } finally { setImgBusy('') }
  }
  function removePhoto(idx: number) {
    setF('photo_urls', (form.photo_urls || []).filter((_, i) => i !== idx))
  }
  function movePhoto(idx: number, dir: -1 | 1) {
    const arr = [...(form.photo_urls || [])]
    const j = idx + dir
    if (j < 0 || j >= arr.length) return
    ;[arr[idx], arr[j]] = [arr[j], arr[idx]]
    setF('photo_urls', arr)
  }

  async function save() {
    if (!token) return
    if (!form.name?.trim()) { setErr('請填名稱'); return }
    setBusy(true); setErr(''); setMsg('')
    try {
      const body: PartnerShopWriteBody = {
        name: form.name.trim(),
        summary: form.summary || '',
        banner_url: form.banner_url || '',
        detail_html: form.detail_html || '',
        photo_urls: form.photo_urls || [],
        video_url: form.video_url || '',
        cta_url: form.cta_url || '',
        cta_label: form.cta_label || '',
        display_order: form.display_order ?? 0,
        enabled: !!form.enabled,
      }
      if (form.id) {
        await adminPartnersApi.update(token, form.id, body)
      } else {
        const { shop } = await adminPartnersApi.create(token, body)
        setForm((f) => ({ ...f, ...shop })) // 寫回新建的 id，避免再按一次「儲存」變成重複建立
      }
      setMsg(`✓ 已儲存 ${body.name}`)
      load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }
  async function del(s: AdminPartnerShop) {
    if (!token || !confirm(`確定刪除商家「${s.name}」？`)) return
    try { await adminPartnersApi.remove(token, s.id); setMsg('已刪除'); if (form.id === s.id) fresh(); load() }
    catch (e: any) { setErr(e?.message || '刪除失敗') }
  }

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>跑者充電站 · 特約商店管理</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        管理前台「跑者充電站」顯示的合作商家：基本資訊、Banner／多圖、詳細內文、YouTube 影片與前往連結。排序數字越小越前面。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,280px) minmax(0,1fr)', gap: 16, alignItems: 'start' }}>
        {/* 商店列表 */}
        <div style={card}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 }}>
            <b style={{ fontSize: 14 }}>商店（{shops?.length ?? '—'}）</b>
            <button onClick={fresh} style={primaryBtn}>＋ 新增</button>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {shops?.map((s) => (
              <div key={s.id} style={{ ...rowCard, borderColor: form.id === s.id ? 'var(--fug)' : 'var(--line)', opacity: s.enabled ? 1 : 0.55 }}>
                <div onClick={() => edit(s)} style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', minWidth: 0 }}>
                  <img src={s.banner_url || undefined} alt="" style={{ width: 46, height: 30, objectFit: 'cover', borderRadius: 5, background: 'var(--bg-2)', flexShrink: 0 }} />
                  <div style={{ minWidth: 0, textAlign: 'left', flex: 1 }}>
                    <div style={{ fontSize: 13, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      #{s.display_order} {s.name}
                    </div>
                    <div style={{ fontSize: 11, color: s.enabled ? 'var(--fug)' : 'var(--hunt)', marginTop: 1 }}>
                      {s.enabled ? '● 上架中' : '○ 已下架'}
                    </div>
                  </div>
                </div>
                <div style={{ display: 'flex', gap: 10, marginTop: 6, paddingLeft: 54 }}>
                  <button onClick={() => edit(s)} style={linkBtn}>編輯</button>
                  <button onClick={() => del(s)} style={{ ...linkBtn, color: 'var(--hunt)' }}>刪除</button>
                </div>
              </div>
            ))}
            {shops && shops.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>尚無商店，按「新增」建立。</div>}
          </div>
        </div>

        {/* 編輯表單 */}
        <div style={card}>
          <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 10 }}>
            <F label="名稱（必填）"><input style={inp} value={form.name || ''} onChange={(e) => setF('name', e.target.value)} placeholder="商家名稱" /></F>
            <F label="排序 display_order"><input style={inp} type="number" value={form.display_order ?? 0} onChange={(e) => setF('display_order', +e.target.value)} /></F>
          </div>
          <F label="簡短資訊 summary"><input style={inp} value={form.summary || ''} onChange={(e) => setF('summary', e.target.value)} placeholder="一行簡介" /></F>

          <div style={{ marginTop: 8 }}>
            <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 3 }}>Banner 圖</div>
            <div style={{ aspectRatio: '16 / 9', maxWidth: 360, background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 8, overflow: 'hidden', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              {form.banner_url ? <img src={form.banner_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} /> : <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>未上傳</span>}
            </div>
            <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
              <label style={{ ...primaryBtn, cursor: 'pointer', opacity: imgBusy === 'banner' ? 0.5 : 1 }}>
                {imgBusy === 'banner' ? '上傳中…' : '上傳 Banner'}
                <input type="file" accept="image/*" disabled={imgBusy === 'banner'} style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadBanner(f); e.target.value = '' }} />
              </label>
              {form.banner_url && <button onClick={() => setF('banner_url', '')} style={ghostBtn}>清除</button>}
            </div>
          </div>

          <div style={{ marginTop: 12 }}>
            <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 3 }}>多張圖片 photo_urls</div>
            {!!(form.photo_urls && form.photo_urls.length) && (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 8 }}>
                {form.photo_urls.map((url, i) => (
                  <div key={i} style={{ width: 100 }}>
                    <img src={url} alt="" style={{ width: 100, height: 70, objectFit: 'cover', borderRadius: 6, background: 'var(--bg-2)', border: '1px solid var(--line)' }} />
                    <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 4, gap: 4 }}>
                      <button onClick={() => movePhoto(i, -1)} disabled={i === 0} style={{ ...tinyBtn, opacity: i === 0 ? 0.35 : 1 }}>↑</button>
                      <button onClick={() => movePhoto(i, 1)} disabled={i === form.photo_urls!.length - 1} style={{ ...tinyBtn, opacity: i === form.photo_urls!.length - 1 ? 0.35 : 1 }}>↓</button>
                      <button onClick={() => removePhoto(i)} style={{ ...tinyBtn, color: 'var(--hunt)' }}>刪除</button>
                    </div>
                  </div>
                ))}
              </div>
            )}
            <label style={{ ...ghostBtn, cursor: 'pointer', opacity: imgBusy === 'photo' ? 0.5 : 1, display: 'inline-block' }}>
              {imgBusy === 'photo' ? '上傳中…' : '＋ 新增圖片'}
              <input type="file" accept="image/*" disabled={imgBusy === 'photo'} style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) addPhoto(f); e.target.value = '' }} />
            </label>
          </div>

          <F label="詳細內文 detail_html">
            <textarea style={{ ...ta, fontFamily: 'monospace', fontSize: 12 }} rows={8} value={form.detail_html || ''} onChange={(e) => setF('detail_html', e.target.value)} placeholder="<p>介紹內容…</p>" />
          </F>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 2 }}>
            支援排版標籤（粗體/斜體/清單/連結/換行等）；為安全起見 script、iframe、事件屬性等會被自動移除。
          </div>

          <F label="YouTube 影片連結 video_url"><input style={inp} value={form.video_url || ''} onChange={(e) => setF('video_url', e.target.value)} placeholder="https://www.youtube.com/watch?v=..." /></F>

          <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 10, marginTop: 4 }}>
            <F label="前往連結 cta_url"><input style={inp} value={form.cta_url || ''} onChange={(e) => setF('cta_url', e.target.value)} placeholder="https://..." /></F>
            <F label="按鈕文字 cta_label"><input style={inp} value={form.cta_label || ''} onChange={(e) => setF('cta_label', e.target.value)} placeholder="立即前往" /></F>
          </div>

          <F label="上下架"><label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, paddingTop: 8 }}><input type="checkbox" checked={!!form.enabled} onChange={(e) => setF('enabled', e.target.checked)} />啟用（上架，顯示於前台）</label></F>

          <div style={{ display: 'flex', gap: 10, marginTop: 8 }}>
            <button onClick={save} disabled={busy} style={{ ...primaryBtn, padding: '9px 20px', opacity: busy ? 0.5 : 1 }}>{busy ? '儲存中…' : '儲存'}</button>
            {form.id && <button onClick={() => del(form as AdminPartnerShop)} style={dangerBtn}>刪除此商店</button>}
          </div>
        </div>
      </div>
    </div>
  )
}

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return <label style={{ display: 'block', marginTop: 8 }}><span style={{ display: 'block', fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 3 }}>{label}</span>{children}</label>
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16, minWidth: 0 }
const inp: React.CSSProperties = { width: '100%', background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, boxSizing: 'border-box', fontFamily: 'inherit' }
const ta: React.CSSProperties = { ...inp, resize: 'vertical', lineHeight: 1.5 }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const dangerBtn: React.CSSProperties = { background: 'transparent', color: 'var(--hunt)', border: '1px solid var(--hunt)', borderRadius: 8, padding: '9px 16px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const rowCard: React.CSSProperties = { background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line)', borderRadius: 8, padding: 8, width: '100%', color: 'inherit', fontFamily: 'inherit' }
const linkBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--fug)', cursor: 'pointer', fontSize: 12, padding: 0, fontFamily: 'inherit' }
const tinyBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', border: '1px solid var(--line-2)', borderRadius: 5, color: 'var(--tx)', cursor: 'pointer', fontSize: 11, padding: '2px 6px', fontFamily: 'inherit' }
