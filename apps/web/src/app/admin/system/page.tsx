'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminAppSettingsApi, adminImagesApi, settingsApi, adminSettingsApi, type CancellationPolicy, type SiteSettings } from '@/lib/api'
import { SETTINGS_SPECS, type SettingSpec } from '@/lib/appSettings'
import { getToken, clearToken } from '@/lib/adminAuth'
import { CancellationPolicyFields, DEFAULT_CANCELLATION_POLICY, sortTiers, validateCancellationPolicy } from '../CancelPolicyEditor'

export default function AdminSystemPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  const [edit, setEdit] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const seeded = useRef(false)
  // Strava 標章存在 SiteSettings（與上方 app_settings 是不同設定庫），故獨立一組 state/載入/上傳。
  const [settings, setSettings] = useState<SiteSettings | null>(null)
  const [imgBusy, setImgBusy] = useState('')

  // 取消退費政策系統預設（複雜巢狀結構，不走 SETTINGS_SPECS 的通用 number/select/text 渲染，獨立一塊）
  const [cancelPolicy, setCancelPolicy] = useState<CancellationPolicy>(DEFAULT_CANCELLATION_POLICY)
  const [cancelPolicyBusy, setCancelPolicyBusy] = useState(false)
  const [cancelPolicyErr, setCancelPolicyErr] = useState('')
  const [cancelPolicyMsg, setCancelPolicyMsg] = useState('')
  const cancelPolicySeeded = useRef(false)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminAppSettingsApi.list(t).then((r) => {
      const s = r.settings || {}
      setValues(s)
      // 只在「首次載入」用伺服器值填入輸入框（之後各列自儲存後單獨同步；save 的 setValues 不會再觸發填入，
      // 避免儲存某列時洗掉其他列正在輸入但未儲存的值）。
      if (!seeded.current) {
        seeded.current = true
        const next: Record<string, string> = {}
        for (const spec of SETTINGS_SPECS) {
          const raw = s[spec.key]
          // 對抗式審查修正（high finding）：只有 number 型才做「儲存值 ÷ scale」的換算。舊版本
          // 對所有 spec 一律 Number(raw)/scale，text 型（白名單那幾個，值是 email／帳號編碼）會
          // 被算成 NaN，輸入框顯示字面字串 "NaN"；管理員只要在那一列按一次「儲存」（連改都不用
          // 改）就會把白名單洗成 "NaN" 並回 200——正式 DB 的 strategy_entry_whitelist 已經真的
          // 被這樣毀過一次。gps_calib_notify_whitelist 是「只發給指定帳號」的唯一控制點，被洗掉
          // 就變成一封都不發（連 sogobaga 也收不到）且完全靜默。
          if (spec.type === 'number') {
            const scale = spec.scale ?? 1
            next[spec.key] = raw ? String(Number(raw) / scale) : ''
          } else {
            next[spec.key] = raw ?? ''
          }
        }
        setEdit(next)
      }
      if (!cancelPolicySeeded.current) {
        cancelPolicySeeded.current = true
        const raw = s['cancellation_policy']
        if (raw) {
          try { setCancelPolicy(JSON.parse(raw)) } catch { /* 壞資料時維持內建預設，不擋頁面載入 */ }
        }
      }
    }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('無「系統設定」權限')
      else setErr(e?.message || '載入失敗')
    })
    settingsApi.get().then((r) => setSettings(r.settings)).catch(() => setErr('Strava 標章設定載入失敗，請重新整理'))
  }, [router])
  useEffect(() => { load() }, [load])

  async function saveCancelPolicy() {
    if (!token) return
    const normalized: CancellationPolicy = { deadline_days: cancelPolicy.deadline_days, tiers: sortTiers(cancelPolicy.tiers ?? []) }
    const vErr = validateCancellationPolicy(normalized)
    if (vErr) { setCancelPolicyErr(vErr); setCancelPolicyMsg(''); return }
    setCancelPolicyBusy(true); setCancelPolicyErr(''); setCancelPolicyMsg('')
    try {
      const r = await adminAppSettingsApi.set(token, 'cancellation_policy', JSON.stringify(normalized))
      setValues(r.settings || {})
      setCancelPolicy(normalized)
      setCancelPolicyMsg('✓ 已儲存退費政策預設值')
    } catch (e: any) {
      setCancelPolicyErr(e?.message || '儲存失敗')
    } finally {
      setCancelPolicyBusy(false)
    }
  }

  async function save(spec: SettingSpec) {
    if (!token) return
    const key = spec.key
    const raw = (edit[key] ?? '').trim()
    let val: string
    let editVal: string
    if (spec.type === 'number') {
      const scale = spec.scale ?? 1
      const min = spec.min ?? 0, max = spec.max ?? 999999, def = parseFloat(spec.def) || 0
      const disp = raw === '' ? def : Math.min(max, Math.max(min, Math.round(parseFloat(raw) || def)))
      editVal = String(disp)
      val = String(Math.round(disp * scale))
    } else if (spec.type === 'text') {
      val = raw // 多行文字：允許清空（存空字串）
      editVal = val
    } else {
      val = raw || spec.def
      editVal = val
    }
    setBusy(key); setErr(''); setMsg('')
    try {
      const r = await adminAppSettingsApi.set(token, key, val)
      setValues(r.settings || {}); setEdit((s) => ({ ...s, [key]: editVal })); setMsg(`✓ 已儲存「${spec.label}」`)
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy('') }
  }

  async function uploadFavicon(file: File) {
    if (!token) return
    setBusy('favicon'); setErr(''); setMsg('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      const r = await adminAppSettingsApi.set(token, 'favicon_url', url)
      setValues(r.settings || {}); setMsg('✓ 已更新 favicon（前台下次載入生效）')
    } catch (e: any) { setErr(e?.message || '上傳失敗') } finally { setBusy('') }
  }
  async function clearFavicon() {
    if (!token) return
    setBusy('favicon'); setErr('')
    try { const r = await adminAppSettingsApi.set(token, 'favicon_url', ''); setValues(r.settings || {}); setMsg('已還原為內建 favicon') }
    catch (e: any) { setErr(e?.message || '還原失敗') } finally { setBusy('') }
  }

  // Strava 標章（雙版本）走 SiteSettings：一律送完整 settings 合併 patch，避免清掉其他外觀設定。
  async function saveSettings(patch: Partial<SiteSettings>): Promise<void> {
    if (!token) throw new Error('未登入')
    if (!settings) throw new Error('外觀設定尚未載入，請重新整理後再試')
    const r = await adminSettingsApi.set(token, { ...settings, ...patch })
    setSettings(r.settings)
  }
  async function uploadImage(key: keyof SiteSettings, file: File, okMsg: string) {
    if (!token || imgBusy) return
    setImgBusy(key); setErr(''); setMsg('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      await saveSettings({ [key]: url })
      setMsg(okMsg)
    } catch (e: any) { setErr(e?.message || '上傳失敗') } finally { setImgBusy('') }
  }
  async function removeImage(key: keyof SiteSettings, okMsg: string) {
    if (imgBusy) return
    setImgBusy(key); setErr(''); setMsg('')
    try { await saveSettings({ [key]: '' }); setMsg(okMsg) }
    catch (e: any) { setErr(e?.message || '移除失敗') } finally { setImgBusy('') }
  }

  const groups = Array.from(new Set(SETTINGS_SPECS.map((s) => s.group)))
  const favUrl = values['favicon_url'] || ''

  return (
    <div style={{ maxWidth: 760, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>系統設定</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px' }}>
        可調整的系統參數。修改後前台下次載入即生效（跑步中的使用者會在「下次開始跑步」或重整後套用）。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      {groups.map((g) => (
        <div key={g} style={{ marginTop: 18 }}>
          <div style={{ fontSize: 13, fontWeight: 800, letterSpacing: '.05em', marginBottom: 8 }}>{g}</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {SETTINGS_SPECS.filter((s) => s.group === g).map((s) => {
              const cur = values[s.key]
              const hasCur = cur != null && cur !== ''
              const curLabel = s.type === 'select'
                ? (s.options?.find((o) => o.value === (hasCur ? cur : s.def))?.label ?? (hasCur ? cur : s.def))
                : s.type === 'text'
                ? (hasCur ? `已設定（${cur.split(/[\n,;\s]+/).filter(Boolean).length} 筆）` : '未設定')
                : `${hasCur ? String(Number(cur ?? '0') / (s.scale ?? 1)) : s.def} ${s.unit ?? ''}`
              return (
                <div key={s.key} style={card}>
                  <div style={{ fontWeight: 800, fontSize: 15 }}>{s.label}</div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.7 }}>{s.help}</div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 8, flexWrap: 'wrap' }}>
                    {s.type === 'select' ? (
                      <select value={edit[s.key] || s.def} onChange={(e) => setEdit((st) => ({ ...st, [s.key]: e.target.value }))} style={{ ...inp, width: 260 }}>
                        {s.options?.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                      </select>
                    ) : s.type === 'text' ? (
                      <textarea value={edit[s.key] ?? ''} placeholder={s.placeholder} rows={s.rows ?? 3}
                        onChange={(e) => setEdit((st) => ({ ...st, [s.key]: e.target.value }))}
                        style={{ ...inp, width: '100%', resize: 'vertical', lineHeight: 1.6 }} />
                    ) : (
                      <>
                        <input type="number" min={s.min} max={s.max} value={edit[s.key] ?? ''} placeholder={`預設 ${s.def}`}
                          onChange={(e) => setEdit((st) => ({ ...st, [s.key]: e.target.value }))} style={inp} />
                        <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{s.unit}</span>
                      </>
                    )}
                    <button onClick={() => save(s)} disabled={busy === s.key} style={{ ...primaryBtn, opacity: busy === s.key ? 0.5 : 1 }}>儲存</button>
                    <span style={{ fontSize: 11.5, color: hasCur ? 'var(--gold)' : 'var(--tx-faint)' }}>
                      {hasCur ? `目前：${curLabel}` : `使用預設：${curLabel}`}
                    </span>
                    {s.type === 'number' && <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>範圍 {s.min}–{s.max}</span>}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      ))}

      {/* 取消退費政策（系統預設；個別賽事可在賽事表單覆寫） */}
      <div style={{ marginTop: 18 }}>
        <div style={{ fontSize: 13, fontWeight: 800, letterSpacing: '.05em', marginBottom: 8 }}>退費政策預設值</div>
        <div style={card}>
          <div style={{ fontWeight: 800, fontSize: 15 }}>取消報名退費政策（系統預設）</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.7 }}>
            使用者申請取消報名時，依此政策計算可退費比例。個別賽事若無另外設定，一律套用此系統預設；
            個別賽事的覆寫請到「賽事管理 → 編輯賽事 → 取消退費」分頁設定。
          </div>
          <div style={{ marginTop: 10 }}>
            <CancellationPolicyFields policy={cancelPolicy} onChange={setCancelPolicy} />
          </div>
          {cancelPolicyErr && <div style={{ color: 'var(--hunt)', fontSize: 12.5, marginTop: 8 }}>{cancelPolicyErr}</div>}
          {cancelPolicyMsg && <div style={{ color: 'var(--fug)', fontSize: 12.5, marginTop: 8 }}>{cancelPolicyMsg}</div>}
          <div style={{ marginTop: 10 }}>
            <button onClick={saveCancelPolicy} disabled={cancelPolicyBusy} style={{ ...primaryBtn, opacity: cancelPolicyBusy ? 0.5 : 1 }}>
              {cancelPolicyBusy ? '儲存中…' : '儲存退費政策'}
            </button>
          </div>
        </div>
      </div>

      {/* 品牌圖示（favicon） */}
      <div style={{ marginTop: 22 }}>
        <div style={{ fontSize: 13, fontWeight: 800, letterSpacing: '.05em', marginBottom: 8 }}>品牌圖示</div>
        <div style={card}>
          <div style={{ fontWeight: 800, fontSize: 15 }}>瀏覽器分頁圖示（favicon）</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.7 }}>
            上傳後取代瀏覽器分頁的小圖示（同時套用到 iOS 加入主畫面的圖示）。建議<b>正方形 PNG、512×512 以上、主體置中</b>。
            未設定則用內建圖示。前台「下次載入」生效；favicon 瀏覽器快取較久，換完請強制重整（換新圖網址會變、通常會自動更新）。
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginTop: 10, flexWrap: 'wrap' }}>
            <div style={{ width: 56, height: 56, borderRadius: 12, border: '1px solid var(--line-2)', background: 'var(--bg-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', overflow: 'hidden' }}>
              <img src={favUrl || '/icon-192.png'} alt="favicon" style={{ width: '100%', height: '100%', objectFit: 'contain', opacity: favUrl ? 1 : 0.8 }} />
            </div>
            <label style={{ ...primaryBtn, opacity: busy === 'favicon' ? 0.5 : 1 }}>
              {busy === 'favicon' ? '上傳中…' : (favUrl ? '更換 favicon' : '上傳 favicon')}
              <input type="file" accept="image/*" style={{ display: 'none' }} disabled={busy === 'favicon'}
                onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadFavicon(f); e.target.value = '' }} />
            </label>
            {favUrl && <button onClick={clearFavicon} disabled={busy === 'favicon'} style={{ ...primaryBtn, background: 'rgba(255,255,255,.06)', color: 'var(--hunt)' }}>還原內建</button>}
            <span style={{ fontSize: 11.5, color: favUrl ? 'var(--gold)' : 'var(--tx-faint)' }}>{favUrl ? '目前：自訂' : '目前：內建圖示'}</span>
          </div>
        </div>
      </div>

      {/* Strava 標章（Powered by Strava）— 自「等級設定」搬來；存於 SiteSettings，前台依 skin 自動顯示對應版本 */}
      <div style={{ marginTop: 22 }}>
        <div style={{ fontSize: 13, fontWeight: 800, letterSpacing: '.05em', marginBottom: 8 }}>Strava 標章</div>
        <div style={card}>
          <div style={{ fontWeight: 800, fontSize: 15 }}>Strava 標章（Powered by Strava）</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.7 }}>
            顯示在「會員資訊頁 → 運動數據」底部。一個檔案無法同時適用深/淺色 skin，故分兩版；前台會依目前 skin 自動顯示對應版本。請上傳 Strava 官方資產（.svg 或 .png），未上傳則用內建佔位圖。
          </div>
          <ImgSlot
            title="深色 skin 用（白字版）"
            hint="套用於預設深色主題。請上傳白色文字的版本（深底才看得清）。"
            url={settings?.strava_powered_dark_url ?? ''}
            dark
            busy={imgBusy === 'strava_powered_dark_url'}
            disabled={imgBusy !== ''}
            onUpload={(f) => uploadImage('strava_powered_dark_url', f, '✓ 已更新（深色 skin 用）')}
            onRemove={() => removeImage('strava_powered_dark_url', '✓ 已移除（深色 skin 用）')}
          />
          <ImgSlot
            title="淺色 skin 用（深字版）"
            hint="套用於暖色/淺色主題（warm、warm2…）。請上傳深色文字的版本（淺底才看得清）。"
            url={settings?.strava_powered_light_url ?? ''}
            dark={false}
            busy={imgBusy === 'strava_powered_light_url'}
            disabled={imgBusy !== ''}
            onUpload={(f) => uploadImage('strava_powered_light_url', f, '✓ 已更新（淺色 skin 用）')}
            onRemove={() => removeImage('strava_powered_light_url', '✓ 已移除（淺色 skin 用）')}
          />
        </div>
      </div>
    </div>
  )
}

// 單張圖片上傳槽（預覽底色可切深/淺，方便看白字或深字標章）。自「等級設定」搬來，與 Strava 標章一起用。
function ImgSlot({ title, hint, url, dark, busy, disabled, onUpload, onRemove }: {
  title: string; hint: string; url: string; dark: boolean; busy: boolean; disabled: boolean;
  onUpload: (f: File) => void; onRemove: () => void
}) {
  return (
    <div style={{ marginTop: 14 }}>
      <div style={{ fontSize: 13.5, fontWeight: 700 }}>{title}</div>
      <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', margin: '2px 0 8px', lineHeight: 1.6 }}>{hint}</div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
        <div style={{ width: 200, height: 56, borderRadius: 10, border: '1px solid var(--line-2)', overflow: 'hidden', background: dark ? '#0b0e13' : '#f3eee2', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 8 }}>
          {url
            // eslint-disable-next-line @next/next/no-img-element
            ? <img src={url} alt="" style={{ maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }} />
            : <span style={{ fontSize: 11, color: dark ? 'rgba(255,255,255,.5)' : 'rgba(0,0,0,.4)' }}>未設定</span>}
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <label style={{ ...primaryBtn, display: 'inline-block', cursor: disabled ? 'default' : 'pointer', opacity: busy || disabled ? 0.6 : 1, pointerEvents: disabled ? 'none' : 'auto' }}>
            {busy ? '上傳中…' : url ? '更換' : '上傳'}
            <input type="file" accept="image/*,.svg" disabled={disabled} style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) onUpload(f); e.target.value = '' }} />
          </label>
          {url && <button onClick={onRemove} disabled={disabled} style={{ ...primaryBtn, background: 'var(--bg-2)', color: 'var(--hunt)', border: '1px solid var(--line-2)', opacity: disabled ? 0.6 : 1 }}>移除</button>}
        </div>
      </div>
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14 }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '7px 14px', cursor: 'pointer', fontSize: 12.5 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: 120 }
