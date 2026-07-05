'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminLevelsApi, settingsApi, adminSettingsApi, adminImagesApi, type LevelConfig, type ExpRules, type AthleteMetricConfig, type AthleteLevel, type SiteSettings } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import DpCoin from '@/components/DpCoin'

const METRIC_LABEL: Record<string, { t: string; u: string }> = {
  volume: { t: '跑量', u: '累積 km' },
  pace: { t: '配速', u: '秒/km（越低越好）' },
  avg_dist: { t: '平均每次距離', u: 'km' },
  longest: { t: '最長單次', u: 'km' },
  monthly_freq: { t: '月平均次數', u: '次/月' },
}

export default function AdminLevelsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [levels, setLevels] = useState<LevelConfig[] | null>(null)
  const [rules, setRules] = useState<ExpRules | null>(null)
  const [aMetrics, setAMetrics] = useState<AthleteMetricConfig[] | null>(null)
  const [aLevels, setALevels] = useState<AthleteLevel[] | null>(null)
  const [settings, setSettings] = useState<SiteSettings | null>(null)
  const [imgBusy, setImgBusy] = useState('') // 上傳中的欄位 key（''=無）
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminLevelsApi.levelConfig(t).then((r) => setLevels(r.levels)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
    adminLevelsApi.expRules(t).then((r) => setRules(r.exp_rules)).catch(() => {})
    adminLevelsApi.athleteConfig(t).then((r) => { setAMetrics(r.metrics); setALevels(r.levels) }).catch(() => {})
    settingsApi.get().then((r) => setSettings(r.settings)).catch(() => setErr('外觀設定載入失敗，請重新整理'))
  }, [router])

  // 一律送完整 settings（合併 patch），避免只送單一欄位把其他外觀設定清空。
  // settings 尚未載入時「丟錯」而非默默略過，讓呼叫端 catch 顯示錯誤（不會假成功）。
  async function saveSettings(patch: Partial<SiteSettings>): Promise<void> {
    if (!token) throw new Error('未登入')
    if (!settings) throw new Error('外觀設定尚未載入，請重新整理後再試')
    const r = await adminSettingsApi.set(token, { ...settings, ...patch })
    setSettings(r.settings)
  }
  // imgBusy!=='' 期間所有上傳/移除控制項皆停用（見 ImgSlot / 底圖區的 disabled），
  // 避免兩個存檔以過期的 settings 快照互相覆蓋對方欄位。
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

  async function saveAthlete() {
    if (!token || !aMetrics || !aLevels) return
    setSaving(true); setErr(''); setMsg('')
    try {
      const r = await adminLevelsApi.setAthleteConfig(token, {
        metrics: aMetrics.map((m) => ({ ...m, weight: Number(m.weight), ref_lo: Number(m.ref_lo), ref_hi: Number(m.ref_hi) })),
        levels: aLevels.map((l) => ({ min_score: Number(l.min_score), name: l.name })),
      })
      setAMetrics(r.metrics); setALevels(r.levels); setMsg('✓ 選手分級設定已儲存')
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }
  useEffect(() => { load() }, [load])

  function updLevel(i: number, patch: Partial<LevelConfig>) {
    setLevels((ls) => (ls ? ls.map((x, idx) => (idx === i ? { ...x, ...patch } : x)) : ls))
  }
  function addLevel() {
    setLevels((ls) => {
      const next = ls && ls.length ? Math.max(...ls.map((l) => l.level)) + 1 : 1
      return [...(ls ?? []), { level: next, title: '', exp_required: 0 }]
    })
  }
  async function saveLevels() {
    if (!token || !levels) return
    setSaving(true); setErr(''); setMsg('')
    try {
      const r = await adminLevelsApi.setLevelConfig(token, levels.map((l) => ({ ...l, level: Number(l.level), exp_required: Number(l.exp_required) })))
      setLevels(r.levels); setMsg('✓ 等級門檻已儲存')
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }

  // 匯出目前等級門檻表為 .xlsx（含欄位標題，可延續整張表的結構繼續填寫）。xlsx 動態載入，僅此頁載入。
  async function exportLevels() {
    setErr(''); setMsg('')
    try {
      const XLSX = await import('xlsx')
      const rows: (string | number)[][] = [['等級', '名稱', '所需累積EXP'], ...(levels ?? []).map((l) => [Number(l.level), l.title ?? '', Number(l.exp_required)])]
      const ws = XLSX.utils.aoa_to_sheet(rows)
      ws['!cols'] = [{ wch: 8 }, { wch: 18 }, { wch: 16 }]
      const wb = XLSX.utils.book_new()
      XLSX.utils.book_append_sheet(wb, ws, '等級門檻')
      XLSX.writeFile(wb, '等級門檻.xlsx')
      setMsg('✓ 已匯出等級門檻 .xlsx')
    } catch (e: any) { setErr(e?.message || '匯出失敗') }
  }

  // 從 .xlsx 匯入，直接取代整張等級表（載入到編輯區，確認後仍需按「儲存等級門檻」才寫入）。
  // 依標題找欄位（等級 / 名稱 / 所需累積EXP），找不到標題就用前三欄，方便手工填寫的檔案也能匯入。
  async function importLevels(file: File) {
    setErr(''); setMsg('')
    try {
      const XLSX = await import('xlsx')
      const wb = XLSX.read(await file.arrayBuffer(), { type: 'array' })
      const ws = wb.Sheets[wb.SheetNames[0]]
      if (!ws) throw new Error('檔案內沒有工作表')
      const aoa = XLSX.utils.sheet_to_json<(string | number)[]>(ws, { header: 1, blankrows: false })
      if (aoa.length === 0) throw new Error('檔案是空的')
      const header = (aoa[0] ?? []).map((h) => String(h ?? '').trim())
      const hasHeader = header.some((h) => /等級|level|名稱|title|exp|經驗|所需|門檻/i.test(h))
      const find = (re: RegExp, fb: number) => { const i = header.findIndex((h) => re.test(h)); return i >= 0 ? i : fb }
      const iLevel = hasHeader ? find(/等級|level/i, 0) : 0
      const iTitle = hasHeader ? find(/名稱|名称|title|稱號/i, 1) : 1
      const iExp = hasHeader ? find(/exp|經驗|所需|門檻/i, 2) : 2
      const toInt = (v: unknown) => parseInt(String(v ?? '').replace(/[^\d.-]/g, ''), 10)
      const parsed: LevelConfig[] = []
      for (const row of hasHeader ? aoa.slice(1) : aoa) {
        if (!row) continue
        const lv = toInt(row[iLevel]); const exp = toInt(row[iExp])
        if (!Number.isFinite(lv) && !Number.isFinite(exp)) continue // 整列空白
        if (!Number.isFinite(lv)) continue
        parsed.push({ level: lv, title: String(row[iTitle] ?? '').trim(), exp_required: Number.isFinite(exp) ? exp : 0 })
      }
      if (parsed.length === 0) throw new Error('沒有讀到有效資料（請確認欄位：等級 / 名稱 / 所需累積EXP）')
      parsed.sort((a, b) => a.level - b.level)
      setLevels(parsed)
      setMsg(`✓ 已匯入 ${parsed.length} 筆等級（尚未寫入）— 請確認後按「儲存等級門檻」`)
    } catch (e: any) { setErr(e?.message || '匯入失敗，請確認是 .xlsx 檔且欄位正確') }
  }
  async function saveRules() {
    if (!token || !rules) return
    setSaving(true); setErr(''); setMsg('')
    try {
      const r = await adminLevelsApi.setExpRules(token, {
        per_collective_task: Number(rules.per_collective_task),
        per_group_task: Number(rules.per_group_task),
        per_individual_task: Number(rules.per_individual_task),
        per_km: Number(rules.per_km),
        dp_per_collective_task: Number(rules.dp_per_collective_task),
        dp_per_group_task: Number(rules.dp_per_group_task),
        dp_per_individual_task: Number(rules.dp_per_individual_task),
        dp_per_km: Number(rules.dp_per_km),
      })
      setRules(r.exp_rules); setMsg('✓ EXP / DP 規則已儲存')
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 6px' }}>等級設定</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>
        會員透過參賽與完成任務獲得 EXP 升等。此處設定各等級門檻與 EXP 取得規則。（EXP 結算將於後續輪接上任務引擎）
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0' }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      {/* 外觀：會員資訊面板底圖 */}
      <div style={panel}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 4px' }}>會員資訊面板底圖</h2>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0 }}>
          套用於前台活動列表頂部的會員資訊面板背景（全站共用）。建議橫式、深色或會壓暗以保文字可讀。
        </p>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
          <div style={{ width: 200, height: 96, borderRadius: 12, border: '1px solid var(--line-2)', overflow: 'hidden', background: 'var(--bg-2)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            {settings?.member_panel_bg_url
              // eslint-disable-next-line @next/next/no-img-element
              ? <img src={settings.member_panel_bg_url} alt="底圖" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
              : <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>無底圖（預設）</span>}
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <label style={{ ...primaryBtn, display: 'inline-block', cursor: imgBusy ? 'default' : 'pointer', opacity: imgBusy ? 0.6 : 1, pointerEvents: imgBusy ? 'none' : 'auto' }}>
              {imgBusy === 'member_panel_bg_url' ? '上傳中…' : '上傳底圖'}
              <input type="file" accept="image/*" disabled={imgBusy !== ''} style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadImage('member_panel_bg_url', f, '✓ 會員面板底圖已更新'); e.target.value = '' }} />
            </label>
            {settings?.member_panel_bg_url && <button onClick={() => removeImage('member_panel_bg_url', '✓ 已移除會員面板底圖')} disabled={imgBusy !== ''} style={{ ...primaryBtn, background: 'var(--bg-2)', color: 'var(--hunt)', border: '1px solid var(--line-2)', opacity: imgBusy ? 0.6 : 1 }}>移除</button>}
          </div>
        </div>
      </div>

      {/* 外觀：Strava「Powered by Strava」標章（雙版本，前台依 skin 深淺自動顯示對應版本） */}
      <div style={{ ...panel, marginTop: 16 }}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 4px' }}>Strava 標章（Powered by Strava）</h2>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0, lineHeight: 1.7 }}>
          顯示在「會員資訊頁 → 運動數據」底部。一個檔案無法同時適用深/淺色 skin，故分兩版；前台會依目前 skin 自動顯示對應版本。請上傳 Strava 官方資產（.svg 或 .png），未上傳則用內建佔位圖。
        </p>
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

      {/* EXP 規則 */}
      <div style={panel}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 4px' }}>EXP 取得規則</h2>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0 }}>
          來源：① 完成賽事 → 各分組各自設定（見下方賽事的分組）② 完成任務 → 依層級 ③ 日常里程 → 每公里。
        </p>
        {rules ? (
          <>
            <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <Field label="完成「全體」任務 EXP">
                <input style={{ ...inp, width: 110 }} type="number" value={rules.per_collective_task} onChange={(e) => setRules({ ...rules, per_collective_task: parseInt(e.target.value || '0', 10) })} />
              </Field>
              <Field label="完成「分組」任務 EXP">
                <input style={{ ...inp, width: 110 }} type="number" value={rules.per_group_task} onChange={(e) => setRules({ ...rules, per_group_task: parseInt(e.target.value || '0', 10) })} />
              </Field>
              <Field label="完成「個人」任務 EXP">
                <input style={{ ...inp, width: 110 }} type="number" value={rules.per_individual_task} onChange={(e) => setRules({ ...rules, per_individual_task: parseInt(e.target.value || '0', 10) })} />
              </Field>
              <Field label="日常每 1 公里 EXP">
                <input style={{ ...inp, width: 110 }} type="number" value={rules.per_km} onChange={(e) => setRules({ ...rules, per_km: parseInt(e.target.value || '0', 10) })} />
              </Field>
            </div>
            <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
              <div style={{ fontSize: 13, fontWeight: 700, marginBottom: 8, display: 'flex', alignItems: 'center', gap: 6 }}>
                <DpCoin size={16} /> DP 幣取得規則（來源同 EXP，獨立費率）
              </div>
              <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'flex-end' }}>
                <Field label="完成「全體」任務 DP">
                  <input style={{ ...inp, width: 110 }} type="number" value={rules.dp_per_collective_task} onChange={(e) => setRules({ ...rules, dp_per_collective_task: parseInt(e.target.value || '0', 10) })} />
                </Field>
                <Field label="完成「分組」任務 DP">
                  <input style={{ ...inp, width: 110 }} type="number" value={rules.dp_per_group_task} onChange={(e) => setRules({ ...rules, dp_per_group_task: parseInt(e.target.value || '0', 10) })} />
                </Field>
                <Field label="完成「個人」任務 DP">
                  <input style={{ ...inp, width: 110 }} type="number" value={rules.dp_per_individual_task} onChange={(e) => setRules({ ...rules, dp_per_individual_task: parseInt(e.target.value || '0', 10) })} />
                </Field>
                <Field label="日常每 1 公里 DP">
                  <input style={{ ...inp, width: 110 }} type="number" value={rules.dp_per_km} onChange={(e) => setRules({ ...rules, dp_per_km: parseInt(e.target.value || '0', 10) })} />
                </Field>
              </div>
            </div>
            <div style={{ marginTop: 12 }}>
              <button onClick={saveRules} disabled={saving} style={primaryBtn}>儲存規則</button>
            </div>
          </>
        ) : <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
      </div>

      {/* 等級門檻 */}
      <div style={{ ...panel, marginTop: 16 }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap', margin: '0 0 12px' }}>
          <h2 style={{ fontSize: 16, fontWeight: 800, margin: 0 }}>等級門檻（累積 EXP）</h2>
          <div style={{ display: 'flex', gap: 8 }}>
            <button onClick={exportLevels} style={ghostBtn} title="匯出目前的等級表為 Excel（含欄位標題，可延續整張表的結構繼續填寫）">⤓ 匯出 .xlsx</button>
            <label style={{ ...ghostBtn, display: 'inline-flex', alignItems: 'center', cursor: 'pointer' }} title="從 Excel 匯入，直接取代整張等級表（匯入後請按「儲存等級門檻」才生效）">
              ⤒ 匯入 .xlsx
              <input type="file" accept=".xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" style={{ display: 'none' }}
                onChange={(e) => { const f = e.target.files?.[0]; if (f) importLevels(f); e.target.value = '' }} />
            </label>
          </div>
        </div>
        {!levels && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {levels?.map((l, i) => (
            <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'flex-end', flexWrap: 'wrap' }}>
              <Field label="等級"><input style={{ ...inp, width: 70 }} type="number" value={l.level} onChange={(e) => updLevel(i, { level: parseInt(e.target.value || '0', 10) })} /></Field>
              <Field label="名稱"><input style={{ ...inp, width: 130 }} value={l.title} onChange={(e) => updLevel(i, { title: e.target.value })} placeholder="例：菁英" /></Field>
              <Field label="所需累積 EXP"><input style={{ ...inp, width: 130 }} type="number" value={l.exp_required} onChange={(e) => updLevel(i, { exp_required: parseInt(e.target.value || '0', 10) })} /></Field>
              <button onClick={() => setLevels((ls) => (ls ? ls.filter((_, idx) => idx !== i) : ls))} style={{ ...ghostBtn, color: 'var(--hunt)' }}>移除</button>
            </div>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
          <button onClick={addLevel} style={ghostBtn}>＋ 新增等級</button>
          <button onClick={saveLevels} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存等級門檻'}</button>
        </div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.7 }}>
          提示：Level 1 的所需 EXP 應為 0；門檻需隨等級遞增。<br />
          匯出／匯入使用 .xlsx（欄位：等級 / 名稱 / 所需累積EXP）。<strong>匯入會取代整張表</strong>，載入後請確認再按「儲存等級門檻」才寫入。
        </div>
      </div>

      {/* 選手分級（報名推薦評分用；前台不顯示標籤） */}
      <div style={{ ...panel, marginTop: 16 }}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 4px' }}>選手分級評分</h2>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0 }}>
          依匯入數據將會員分級（入門/初級/中級/進階/菁英），供報名頁「追蹤者推薦」相似度評分。各指標正規化到 0–100 後依權重加總。
        </p>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {aMetrics?.map((m, i) => (
            <div key={m.metric_key} style={{ display: 'flex', gap: 8, alignItems: 'flex-end', flexWrap: 'wrap' }}>
              <div style={{ width: 110, fontSize: 13, fontWeight: 600 }}>{METRIC_LABEL[m.metric_key]?.t ?? m.metric_key}<div style={{ fontSize: 10, color: 'var(--tx-faint)', fontWeight: 400 }}>{METRIC_LABEL[m.metric_key]?.u}</div></div>
              <Field label="權重"><input style={{ ...inp, width: 70 }} type="number" value={m.weight} onChange={(e) => setAMetrics((a) => a!.map((x, idx) => idx === i ? { ...x, weight: parseInt(e.target.value || '0', 10) } : x))} /></Field>
              <Field label="0 分值"><input style={{ ...inp, width: 90 }} type="number" value={m.ref_lo} onChange={(e) => setAMetrics((a) => a!.map((x, idx) => idx === i ? { ...x, ref_lo: parseFloat(e.target.value || '0') } : x))} /></Field>
              <Field label="100 分值"><input style={{ ...inp, width: 90 }} type="number" value={m.ref_hi} onChange={(e) => setAMetrics((a) => a!.map((x, idx) => idx === i ? { ...x, ref_hi: parseFloat(e.target.value || '0') } : x))} /></Field>
            </div>
          ))}
        </div>

        <h3 style={{ fontSize: 14, fontWeight: 700, margin: '16px 0 8px' }}>等級門檻（綜合分數）</h3>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {aLevels?.map((l, i) => (
            <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
              <Field label="最低分數"><input style={{ ...inp, width: 90 }} type="number" value={l.min_score} onChange={(e) => setALevels((a) => a!.map((x, idx) => idx === i ? { ...x, min_score: parseInt(e.target.value || '0', 10) } : x))} /></Field>
              <Field label="等級名稱"><input style={{ ...inp, width: 130 }} value={l.name} onChange={(e) => setALevels((a) => a!.map((x, idx) => idx === i ? { ...x, name: e.target.value } : x))} /></Field>
              <button onClick={() => setALevels((a) => a!.filter((_, idx) => idx !== i))} style={{ ...ghostBtn, color: 'var(--hunt)' }}>移除</button>
            </div>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
          <button onClick={() => setALevels((a) => [...(a ?? []), { min_score: 0, name: '' }])} style={ghostBtn}>＋ 新增等級</button>
          <button onClick={saveAthlete} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存選手分級設定'}</button>
        </div>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

// 單張圖片上傳槽（預覽底色可切深/淺，方便看白字或深字標章）。
// disabled：有任何存檔進行中時停用所有槽，避免併發存檔以過期快照互相覆蓋。
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

const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 18 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '9px 11px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none', borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14 }
const ghostBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
