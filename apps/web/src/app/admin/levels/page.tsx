'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminLevelsApi, settingsApi, adminSettingsApi, adminImagesApi, type LevelConfig, type ExpRules, type AthleteMetricConfig, type AthleteLevel } from '@/lib/api'
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
  const [panelBg, setPanelBg] = useState('')
  const [panelBgUploading, setPanelBgUploading] = useState(false)
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
    settingsApi.get().then((r) => setPanelBg(r.settings.member_panel_bg_url)).catch(() => {})
  }, [router])

  async function uploadPanelBg(file: File) {
    if (!token) return
    setPanelBgUploading(true); setErr(''); setMsg('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      const r = await adminSettingsApi.set(token, { member_panel_bg_url: url })
      setPanelBg(r.settings.member_panel_bg_url); setMsg('✓ 會員面板底圖已更新')
    } catch (e: any) { setErr(e?.message || '上傳失敗') } finally { setPanelBgUploading(false) }
  }
  async function removePanelBg() {
    if (!token) return
    setSaving(true); setErr(''); setMsg('')
    try {
      const r = await adminSettingsApi.set(token, { member_panel_bg_url: '' })
      setPanelBg(r.settings.member_panel_bg_url); setMsg('✓ 已移除會員面板底圖')
    } catch (e: any) { setErr(e?.message || '移除失敗') } finally { setSaving(false) }
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
          套用於前台賽事列表頂部的會員資訊面板背景（全站共用）。建議橫式、深色或會壓暗以保文字可讀。
        </p>
        <div style={{ display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
          <div style={{ width: 200, height: 96, borderRadius: 12, border: '1px solid var(--line-2)', overflow: 'hidden', background: 'var(--bg-2)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            {panelBg
              // eslint-disable-next-line @next/next/no-img-element
              ? <img src={panelBg} alt="底圖" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
              : <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>無底圖（預設）</span>}
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <label style={{ ...primaryBtn, display: 'inline-block', cursor: 'pointer', opacity: panelBgUploading ? 0.6 : 1 }}>
              {panelBgUploading ? '上傳中…' : '上傳底圖'}
              <input type="file" accept="image/*" style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadPanelBg(f); e.target.value = '' }} />
            </label>
            {panelBg && <button onClick={removePanelBg} disabled={saving} style={{ ...primaryBtn, background: 'var(--bg-2)', color: 'var(--hunt)', border: '1px solid var(--line-2)' }}>移除</button>}
          </div>
        </div>
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
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 12px' }}>等級門檻（累積 EXP）</h2>
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
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>提示：Level 1 的所需 EXP 應為 0；門檻需隨等級遞增。</div>
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

const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 18 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '9px 11px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none', borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14 }
const ghostBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
