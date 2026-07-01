'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminEventsApi, adminImagesApi, type EventDef, type EventTypeSpec } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import DpCoin from '@/components/DpCoin'

function emptyDef(tCat: EventTypeSpec[], cCat: EventTypeSpec[]): EventDef {
  return {
    name: '', description: '', enabled: true, weight: 100, cooldown_sec: 300,
    trigger_type: tCat[0]?.key ?? '', trigger_params: {},
    completion_type: cCat[0]?.key ?? '', completion_params: {},
    message: '', image_url: '', reward_exp: 0, reward_dp: 0,
  }
}

export default function AdminEventsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [defs, setDefs] = useState<EventDef[] | null>(null)
  const [tCat, setTCat] = useState<EventTypeSpec[]>([])
  const [cCat, setCCat] = useState<EventTypeSpec[]>([])
  const [edit, setEdit] = useState<EventDef | null>(null)
  const [isNew, setIsNew] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const [imgUploading, setImgUploading] = useState(false)
  const [pushFor, setPushFor] = useState<string | null>(null)
  const [pushEmail, setPushEmail] = useState('')
  const [pushBusy, setPushBusy] = useState(false)

  async function doPush(d: EventDef) {
    if (!token || !d.id || !pushEmail.trim()) return
    setPushBusy(true); setErr(''); setMsg('')
    try {
      const r = await adminEventsApi.push(token, d.id, pushEmail.trim())
      setMsg(`✓ 已觸發「${d.name}」給 ${r.target}。對方需正在「開始跑步」，約數秒內出現；若當下沒在跑步，本次觸發 3 分鐘後失效。`)
      setPushFor(null); setPushEmail('')
    } catch (e: any) { setErr(e?.message || '觸發失敗') } finally { setPushBusy(false) }
  }

  async function uploadImg(file: File) {
    if (!token || !edit) return
    setImgUploading(true); setErr('')
    try { const { url } = await adminImagesApi.upload(token, file); setEdit((e) => e ? { ...e, image_url: url } : e) }
    catch (e: any) { setErr(e?.message || '圖片上傳失敗') } finally { setImgUploading(false) }
  }

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminEventsApi.list(t).then((r) => { setDefs(r.defs); setTCat(r.trigger_catalog); setCCat(r.completion_catalog) })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「事件任務」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  function startNew() { setEdit(emptyDef(tCat, cCat)); setIsNew(true); setErr(''); setMsg('') }
  function startEdit(d: EventDef) { setEdit(JSON.parse(JSON.stringify(d))); setIsNew(false); setErr(''); setMsg('') }
  function duplicate(d: EventDef) { const c = JSON.parse(JSON.stringify(d)); delete c.id; c.name = d.name + '（複製）'; setEdit(c); setIsNew(true); setErr(''); setMsg('') }

  async function save() {
    if (!token || !edit) return
    if (!edit.name.trim()) { setErr('請填名稱'); return }
    setBusy(true); setErr(''); setMsg('')
    try {
      if (isNew) await adminEventsApi.create(token, edit)
      else await adminEventsApi.update(token, edit.id!, edit)
      setMsg('✓ 已儲存'); setEdit(null); load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }
  async function remove(d: EventDef) {
    if (!token || !d.id) return
    if (!confirm(`刪除事件「${d.name}」？`)) return
    setBusy(true); setErr('')
    try { await adminEventsApi.remove(token, d.id); setMsg('✓ 已刪除'); load() }
    catch (e: any) { setErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  const tSpec = tCat.find((t) => t.key === edit?.trigger_type)
  const cSpec = cCat.find((c) => c.key === edit?.completion_type)
  const summary = (d: EventDef) => {
    const t = tCat.find((x) => x.key === d.trigger_type)
    const c = cCat.find((x) => x.key === d.completion_type)
    const pv = (spec: EventTypeSpec | undefined, params: Record<string, number>) =>
      (spec?.params ?? []).map((p) => `${p.label} ${params[p.key] ?? '—'}${p.unit}`).join('、')
    return `觸發：${t?.label ?? d.trigger_type}（${pv(t, d.trigger_params)}） → 完成：${c?.label ?? d.completion_type}（${pv(c, d.completion_params)}）`
  }

  return (
    <div style={{ maxWidth: 860, margin: '0 auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 10 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>事件任務（日常隨機）</h1>
          <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: 0 }}>跑步中依 GPS 即時觸發的小任務。設定好的組合即為可重複引用的範本，完成給 EXP/DP。</p>
        </div>
        {!edit && <button onClick={startNew} style={primaryBtn}>＋ 新增事件</button>}
      </div>

      <div style={{ background: 'rgba(255,194,75,.1)', border: '1px solid rgba(255,194,75,.35)', borderRadius: 10, padding: '10px 12px', fontSize: 12.5, color: 'var(--gold)', marginTop: 12, lineHeight: 1.7 }}>
        🧪 <strong>測試觸發說明</strong>：每張事件卡右下的「測試觸發」可把該事件<strong>直接推給指定帳號</strong>，用來測試前台顯示與流程。<br />
        ⚠️ 前提：<strong>該帳號必須正在「開始跑步」狀態</strong>（其跑步頁會在數秒內認領並觸發）。若對方當下沒在跑步，這次觸發會在 <strong>3 分鐘</strong>後自動失效，需重新觸發。
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}

      {/* 編輯表單 */}
      {edit && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>{isNew ? '新增事件' : '編輯事件'}</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Field label="名稱" grow><input style={inp} value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="如：狗追來了" /></Field>
            <Field label="啟用"><select style={{ ...inp, width: 90 }} value={edit.enabled ? '1' : '0'} onChange={(e) => setEdit({ ...edit, enabled: e.target.value === '1' })}><option value="1">啟用</option><option value="0">停用</option></select></Field>
            <Field label="隨機權重"><input style={{ ...inp, width: 90 }} type="number" value={edit.weight} onChange={(e) => setEdit({ ...edit, weight: parseInt(e.target.value || '0', 10) })} /></Field>
            <Field label="冷卻(秒)"><input style={{ ...inp, width: 90 }} type="number" value={edit.cooldown_sec} onChange={(e) => setEdit({ ...edit, cooldown_sec: parseInt(e.target.value || '0', 10) })} /></Field>
          </div>

          {/* 觸發 */}
          <div style={sect}>
            <div style={sectTitle}>觸發條件</div>
            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <Field label="類型"><select style={{ ...inp, width: 200 }} value={edit.trigger_type} onChange={(e) => setEdit({ ...edit, trigger_type: e.target.value, trigger_params: {} })}>{tCat.map((t) => <option key={t.key} value={t.key}>{t.label}</option>)}</select></Field>
              {(tSpec?.params ?? []).map((p) => (
                <Field key={p.key} label={`${p.label}（${p.unit}）`}>
                  <input style={{ ...inp, width: 120 }} type="number" value={edit.trigger_params[p.key] ?? ''} onChange={(e) => setEdit({ ...edit, trigger_params: { ...edit.trigger_params, [p.key]: parseFloat(e.target.value) || 0 } })} />
                </Field>
              ))}
            </div>
          </div>

          {/* 完成 */}
          <div style={sect}>
            <div style={sectTitle}>完成條件</div>
            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <Field label="類型"><select style={{ ...inp, width: 200 }} value={edit.completion_type} onChange={(e) => setEdit({ ...edit, completion_type: e.target.value, completion_params: {} })}>{cCat.map((c) => <option key={c.key} value={c.key}>{c.label}</option>)}</select></Field>
              {(cSpec?.params ?? []).map((p) => (
                <Field key={p.key} label={`${p.label}（${p.unit}）`}>
                  <input style={{ ...inp, width: 120 }} type="number" value={edit.completion_params[p.key] ?? ''} onChange={(e) => setEdit({ ...edit, completion_params: { ...edit.completion_params, [p.key]: parseFloat(e.target.value) || 0 } })} />
                </Field>
              ))}
            </div>
          </div>

          {/* 文案 + 獎勵 */}
          <div style={{ marginTop: 12 }}>
            <Field label="觸發文案（跑者看到的劇情）" grow>
              <textarea style={{ ...inp, minHeight: 60, resize: 'vertical' }} value={edit.message} onChange={(e) => setEdit({ ...edit, message: e.target.value })} placeholder="後方有三隻狗往你衝過來，請趕快跑起來！" />
            </Field>
          </div>

          {/* 事件插圖（前台橫幅顯示，增加沉浸感） */}
          <div style={{ marginTop: 12 }}>
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 4 }}>事件插圖（前台橫幅顯示，如狗群插圖）</div>
            <div style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
              {edit.image_url
                ? <img src={edit.image_url} alt="事件插圖" style={{ width: 72, height: 72, objectFit: 'cover', borderRadius: 10, border: '1px solid var(--line-2)' }} />
                : <div style={{ width: 72, height: 72, borderRadius: 10, border: '1px dashed var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--tx-faint)', fontSize: 22 }}>🐾</div>}
              <label style={{ ...ghostBtn, display: 'inline-block' }}>
                {imgUploading ? '上傳中…' : (edit.image_url ? '更換圖片' : '上傳圖片')}
                <input type="file" accept="image/*" style={{ display: 'none' }} disabled={imgUploading}
                  onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadImg(f); e.target.value = '' }} />
              </label>
              {edit.image_url && <button onClick={() => setEdit({ ...edit, image_url: '' })} style={{ ...ghostBtn, color: 'var(--hunt)' }}>移除</button>}
            </div>
          </div>
          <div style={{ display: 'flex', gap: 12, marginTop: 12, flexWrap: 'wrap' }}>
            <Field label="完成獎勵 EXP"><input style={{ ...inp, width: 110 }} type="number" value={edit.reward_exp} onChange={(e) => setEdit({ ...edit, reward_exp: parseInt(e.target.value || '0', 10) })} /></Field>
            <Field label="完成獎勵 DP"><input style={{ ...inp, width: 110 }} type="number" value={edit.reward_dp} onChange={(e) => setEdit({ ...edit, reward_dp: parseInt(e.target.value || '0', 10) })} /></Field>
          </div>

          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>儲存</button>
            <button onClick={() => setEdit(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      {/* 清單 */}
      <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 10 }}>
        {!defs && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {defs && defs.length === 0 && <div style={{ color: 'var(--tx-faint)' }}>尚無事件，點右上「新增事件」。</div>}
        {defs?.map((d) => (
          <div key={d.id} style={card}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 800, fontSize: 15 }}>
                  {d.name}
                  {!d.enabled && <span style={{ ...badge, color: 'var(--tx-dim)', borderColor: 'var(--line-2)' }}>停用</span>}
                </div>
                <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4 }}>{summary(d)}</div>
                {d.message && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 4, fontStyle: 'italic' }}>「{d.message}」</div>}
                <div style={{ display: 'inline-flex', alignItems: 'center', gap: 10, marginTop: 6, fontSize: 12.5 }}>
                  <span style={{ color: 'var(--gold)', fontWeight: 700 }}>+{d.reward_exp} EXP</span>
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3, color: '#FFD24D', fontWeight: 700 }}><DpCoin size={13} />+{d.reward_dp}</span>
                  <span style={{ color: 'var(--tx-faint)' }}>權重 {d.weight}・冷卻 {d.cooldown_sec}s</span>
                </div>
              </div>
              <div style={{ display: 'flex', gap: 8, flexShrink: 0, flexWrap: 'wrap' }}>
                <button onClick={() => { setPushFor(pushFor === d.id ? null : d.id!); setPushEmail('') }} style={{ ...ghostBtn, color: 'var(--gold)', borderColor: 'rgba(255,194,75,.4)' }}>🧪 測試觸發</button>
                <button onClick={() => startEdit(d)} style={ghostBtn}>編輯</button>
                <button onClick={() => duplicate(d)} style={ghostBtn}>複製</button>
                <button onClick={() => remove(d)} style={{ ...ghostBtn, color: 'var(--hunt)' }}>刪除</button>
              </div>
            </div>
            {pushFor === d.id && (
              <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--line)', display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <span style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>觸發給帳號：</span>
                <input style={{ ...inp, width: 240 }} type="email" value={pushEmail} onChange={(e) => setPushEmail(e.target.value)} placeholder="對方登入 email（需正在開始跑步）" onKeyDown={(e) => { if (e.key === 'Enter') doPush(d) }} />
                <button onClick={() => doPush(d)} disabled={pushBusy || !pushEmail.trim()} style={{ ...primaryBtn, opacity: pushBusy || !pushEmail.trim() ? 0.5 : 1 }}>{pushBusy ? '觸發中…' : '送出觸發'}</button>
                <button onClick={() => { setPushFor(null); setPushEmail('') }} style={ghostBtn}>取消</button>
              </div>
            )}
          </div>
        ))}
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

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5 }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 14px', cursor: 'pointer', fontSize: 13 }
const sect: React.CSSProperties = { marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--line)' }
const sectTitle: React.CSSProperties = { fontSize: 13, fontWeight: 700, marginBottom: 8, color: 'var(--tx)' }
const badge: React.CSSProperties = { marginLeft: 8, fontSize: 10.5, fontWeight: 700, border: '1px solid', borderRadius: 999, padding: '2px 8px', verticalAlign: 'middle' }
