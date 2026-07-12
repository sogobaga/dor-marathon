'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminExploreApi, adminImagesApi, type ExploreBoss } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 城市探索後台：關主（打卡點）CRUD——基本資料、難度、地點座標、對話、Scene/Card 圖、結構化課表。
// 綁玩家個人、與賽事無關的獨立管理頁（非賽事設定內）。

type Form = Partial<ExploreBoss> & { _segText?: string }

const EMPTY: Form = {
  code: '', name: '', title: '', region: '', place: '', gender: '女', age: 0, workout_label: '',
  difficulty_stars: 3, quote: '', skill_name: '', skill_desc: '', dialogue_intro: '', dialogue_start: '',
  scene_image_url: '', card_image_url: '', lat: 25.0296, lng: 121.5357, radius_m: 40,
  reward_exp: 100, reward_dp: 20, retry_dp_cost: 0, workout_kind: 'mixed', data_source: 'gps',
  display_order: 0, enabled: true, access_note: '', _segText: '[]',
}

export default function AdminExplorePage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [bosses, setBosses] = useState<ExploreBoss[] | null>(null)
  const [form, setForm] = useState<Form>(EMPTY)
  const [busy, setBusy] = useState(false)
  const [imgBusy, setImgBusy] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminExploreApi.list(t)
      .then((r) => setBosses(r.bosses))
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「事件任務」權限（城市探索沿用同權限）')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  function edit(b: ExploreBoss) {
    setForm({ ...b, _segText: JSON.stringify(b.segments ?? [], null, 2) })
    setMsg(''); setErr('')
  }
  function fresh() { setForm({ ...EMPTY }); setMsg(''); setErr('') }
  function setF<K extends keyof Form>(k: K, v: Form[K]) { setForm((f) => ({ ...f, [k]: v })) }

  async function uploadImg(field: 'scene_image_url' | 'card_image_url', file: File) {
    if (!token) return
    setImgBusy(field); setErr('')
    try { const { url } = await adminImagesApi.upload(token, file); setF(field, url); setMsg('✓ 圖片已上傳') }
    catch (e: any) { setErr(e?.message || '上傳失敗') } finally { setImgBusy('') }
  }

  async function save() {
    if (!token) return
    if (!form.code) { setErr('請填關主編號 code'); return }
    let segments: unknown = []
    try { segments = JSON.parse(form._segText || '[]'); if (!Array.isArray(segments)) throw new Error() }
    catch { setErr('分段課表 JSON 格式錯誤'); return }
    setBusy(true); setErr(''); setMsg('')
    try {
      const { _segText, ...rest } = form
      await adminExploreApi.save(token, { ...rest, segments: segments as ExploreBoss['segments'] })
      setMsg(`✓ 已儲存 ${form.code}`)
      load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }
  async function del(b: ExploreBoss) {
    if (!token || !confirm(`確定刪除關主「${b.name}（${b.code}）」？會連帶清除玩家進度。`)) return
    try { await adminExploreApi.del(token, b.id); setMsg('已刪除'); if (form.id === b.id) fresh(); load() }
    catch (e: any) { setErr(e?.message || '刪除失敗') }
  }

  return (
    <div style={{ maxWidth: 900, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>城市探索 · 關主管理</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        每個打卡點＝一位關主，玩家到點打卡→接受挑戰（結構化課表）→完成得 1-3★，3★ 取得關主卡片。難度星數決定挑戰消耗 DP（難度×10）。
        入口顯示與白名單請到 <Link href="/admin/system" style={{ color: 'var(--fug)' }}>系統設定</Link> 控制（城市探索入口 / 卡片探索入口）。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      <div style={{ display: 'grid', gridTemplateColumns: '260px 1fr', gap: 16, alignItems: 'start' }}>
        {/* 關主列表 */}
        <div style={card}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 }}>
            <b style={{ fontSize: 14 }}>關主（{bosses?.length ?? '—'}）</b>
            <button onClick={fresh} style={primaryBtn}>＋ 新增</button>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {bosses?.map((b) => (
              <button key={b.id} onClick={() => edit(b)} style={{ ...rowBtn, borderColor: form.id === b.id ? 'var(--fug)' : 'var(--line)' }}>
                <img src={b.card_image_url || undefined} alt="" style={{ width: 34, height: 46, objectFit: 'cover', borderRadius: 5, background: 'var(--bg-2)', flexShrink: 0 }} />
                <div style={{ minWidth: 0, textAlign: 'left' }}>
                  <div style={{ fontSize: 13, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{b.name || b.code} {'★'.repeat(b.difficulty_stars)}</div>
                  <div style={{ fontSize: 11, color: 'var(--tx-dim)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{b.code} · {b.place}{b.enabled ? '' : ' · 停用'}</div>
                </div>
              </button>
            ))}
            {bosses && bosses.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>尚無關主，按「新增」建立。</div>}
          </div>
        </div>

        {/* 編輯表單 */}
        <div style={card}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            <F label="關主編號 code"><input style={inp} value={form.code || ''} onChange={(e) => setF('code', e.target.value)} placeholder="DOR-TPE-001" /></F>
            <F label="難度星數 (1-6)"><input style={inp} type="number" value={form.difficulty_stars ?? 3} onChange={(e) => setF('difficulty_stars', +e.target.value)} /></F>
            <F label="名稱"><input style={inp} value={form.name || ''} onChange={(e) => setF('name', e.target.value)} placeholder="大安小鹿" /></F>
            <F label="稱號"><input style={inp} value={form.title || ''} onChange={(e) => setF('title', e.target.value)} placeholder="大安的傳說級守門人" /></F>
            <F label="地區"><input style={inp} value={form.region || ''} onChange={(e) => setF('region', e.target.value)} placeholder="臺北市·大安區" /></F>
            <F label="地點"><input style={inp} value={form.place || ''} onChange={(e) => setF('place', e.target.value)} placeholder="大安森林公園" /></F>
            <F label="性別"><input style={inp} value={form.gender || ''} onChange={(e) => setF('gender', e.target.value)} /></F>
            <F label="年齡"><input style={inp} type="number" value={form.age ?? 0} onChange={(e) => setF('age', +e.target.value)} /></F>
            <F label="課表型（標籤）"><input style={inp} value={form.workout_label || ''} onChange={(e) => setF('workout_label', e.target.value)} placeholder="節奏跑混合型" /></F>
            <F label="課表 kind"><input style={inp} value={form.workout_kind || ''} onChange={(e) => setF('workout_kind', e.target.value)} placeholder="mixed/interval/tempo" /></F>
            <F label="必殺技能"><input style={inp} value={form.skill_name || ''} onChange={(e) => setF('skill_name', e.target.value)} placeholder="樹影瞬移" /></F>
            <F label="卡片標語 quote"><input style={inp} value={form.quote || ''} onChange={(e) => setF('quote', e.target.value)} /></F>
          </div>
          <F label="技能說明"><textarea style={ta} rows={2} value={form.skill_desc || ''} onChange={(e) => setF('skill_desc', e.target.value)} /></F>
          <F label="開放資訊（開放時段/場地備註，來自官方開放場地資料；顯示於前台提醒玩家）"><textarea style={ta} rows={2} value={form.access_note || ''} onChange={(e) => setF('access_note', e.target.value)} placeholder="例：平日 17:00 後及例假日全天開放" /></F>
          <F label="打卡後對話（挑戰前，關主說的話；<br> 換行）"><textarea style={ta} rows={2} value={form.dialogue_intro || ''} onChange={(e) => setF('dialogue_intro', e.target.value)} /></F>
          <F label="接受後對話（挑戰開始前；<br> 換行）"><textarea style={ta} rows={2} value={form.dialogue_start || ''} onChange={(e) => setF('dialogue_start', e.target.value)} /></F>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 4 }}>
            <ImgField label="挑戰場景圖 (Scene)" url={form.scene_image_url || ''} busy={imgBusy === 'scene_image_url'} onUpload={(f) => uploadImg('scene_image_url', f)} onClear={() => setF('scene_image_url', '')} ratio="16 / 9" />
            <ImgField label="卡片圖 (Card)" url={form.card_image_url || ''} busy={imgBusy === 'card_image_url'} onUpload={(f) => uploadImg('card_image_url', f)} onClear={() => setF('card_image_url', '')} ratio="3 / 4" />
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 10, marginTop: 4 }}>
            <F label="緯度 lat"><input style={inp} type="number" step="0.0001" value={form.lat ?? 0} onChange={(e) => setF('lat', +e.target.value)} /></F>
            <F label="經度 lng"><input style={inp} type="number" step="0.0001" value={form.lng ?? 0} onChange={(e) => setF('lng', +e.target.value)} /></F>
            <F label="打卡半徑 m"><input style={inp} type="number" value={form.radius_m ?? 40} onChange={(e) => setF('radius_m', +e.target.value)} /></F>
            <F label="獎勵 EXP"><input style={inp} type="number" value={form.reward_exp ?? 0} onChange={(e) => setF('reward_exp', +e.target.value)} /></F>
            <F label="獎勵 DP"><input style={inp} type="number" value={form.reward_dp ?? 0} onChange={(e) => setF('reward_dp', +e.target.value)} /></F>
            <F label="重挑 DP (0=難度×10)"><input style={inp} type="number" value={form.retry_dp_cost ?? 0} onChange={(e) => setF('retry_dp_cost', +e.target.value)} /></F>
            <F label="排序"><input style={inp} type="number" value={form.display_order ?? 0} onChange={(e) => setF('display_order', +e.target.value)} /></F>
            <F label="啟用"><label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, paddingTop: 8 }}><input type="checkbox" checked={!!form.enabled} onChange={(e) => setF('enabled', e.target.checked)} />顯示於前台</label></F>
          </div>

          <F label="分段課表 segments (JSON；比照個人任務課表)">
            <textarea style={{ ...ta, fontFamily: 'monospace', fontSize: 12 }} rows={8} value={form._segText || ''} onChange={(e) => setF('_segText', e.target.value)} />
          </F>

          <div style={{ display: 'flex', gap: 10, marginTop: 8 }}>
            <button onClick={save} disabled={busy} style={{ ...primaryBtn, padding: '9px 20px', opacity: busy ? 0.5 : 1 }}>{busy ? '儲存中…' : '儲存'}</button>
            {form.id && <button onClick={() => del(form as ExploreBoss)} style={dangerBtn}>刪除此關主</button>}
          </div>
        </div>
      </div>
    </div>
  )
}

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return <label style={{ display: 'block', marginTop: 8 }}><span style={{ display: 'block', fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 3 }}>{label}</span>{children}</label>
}
function ImgField({ label, url, busy, ratio, onUpload, onClear }: { label: string; url: string; busy: boolean; ratio: string; onUpload: (f: File) => void; onClear: () => void }) {
  return (
    <div style={{ marginTop: 8 }}>
      <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 3 }}>{label}</div>
      <div style={{ aspectRatio: ratio, background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 8, overflow: 'hidden', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        {url ? <img src={url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} /> : <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>未上傳</span>}
      </div>
      <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
        <label style={{ ...primaryBtn, cursor: 'pointer', opacity: busy ? 0.5 : 1 }}>{busy ? '上傳中…' : '上傳圖片'}<input type="file" accept="image/*" disabled={busy} style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) onUpload(f); e.target.value = '' }} /></label>
        {url && <button onClick={onClear} style={ghostBtn}>清除</button>}
      </div>
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16 }
const inp: React.CSSProperties = { width: '100%', background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, boxSizing: 'border-box', fontFamily: 'inherit' }
const ta: React.CSSProperties = { ...inp, resize: 'vertical', lineHeight: 1.5 }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const dangerBtn: React.CSSProperties = { background: 'transparent', color: 'var(--hunt)', border: '1px solid var(--hunt)', borderRadius: 8, padding: '9px 16px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const rowBtn: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 8, background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line)', borderRadius: 8, padding: 6, cursor: 'pointer', width: '100%', color: 'inherit', fontFamily: 'inherit' }
