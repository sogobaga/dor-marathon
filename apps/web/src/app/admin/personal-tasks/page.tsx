'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminPersonalTasksApi, type PersonalPlan, type PersonalTask } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 個人任務後台：檢視現況 + xlsx 匯入/匯出（灌初始 10 計畫 × 100 天任務）。
// 匯入直接 upsert 進 DB（依 計畫ID / (計畫,Day) 更新，保留既有 id 與玩家進度）。前置任務鏈由後端自動連結。

type ParsedPlan = { code: string; name: string; lifecycle: string; stage_order: number; target_km: number; target_time: string; entry_note: string; data_source: string }
type ParsedTask = { plan_code: string; day: number; week: number; title: string; workout: string; workout_type: string; target_km: number; target_min: number; intensity: string; complete_cond: string; reward_exp: number; reward_dp: number; data_source: string; safety_note: string }
type Pending = { plans: ParsedPlan[]; tasks: ParsedTask[]; fileName: string }

// 欄位候選（比對時去空白/底線、轉小寫、簡繁常見寫法都涵蓋）
const FIELDS: Record<string, string[]> = {
  plan_code: ['計畫id', '計劃id', '計畫編號', '計畫编号', 'plancode', 'planid'],
  plan_name: ['計畫名稱', '計劃名稱', '計畫名', 'planname'],
  stage_order: ['階段序', '阶段序', 'stageorder', '階段順序'],
  lifecycle: ['生命周期層級', '生命週期層級', '生命周期', 'lifecycle', '層級'],
  data_source: ['資料來源', '数据来源', '資料来源', 'datasource', '來源'],
  entry_note: ['進入門檻', '进入门槛', '門檻', 'entrynote'],
  day: ['day', '天數', '第幾天'],
  week: ['week', '週次', '周次'],
  title: ['dor任務文案', '任務文案', '任务文案', '任務名稱', 'title'],
  workout: ['訓練菜單', '训练菜单', '訓練內容', '課表內容', '菜單', 'workout'],
  workout_type: ['課表類型', '课表类型', 'workouttype'],
  target_km: ['目標里程km', '目标里程km', '目標里程', '目标里程', 'targetkm'],
  target_min: ['預估時間min', '预估时间min', '預估時間', '预估时间', 'targetmin'],
  intensity: ['強度', '强度', 'intensity'],
  complete_cond: ['完成條件', '完成条件', 'completecond'],
  safety_note: ['安全退階規則', '安全退阶规则', '安全退階', '退階規則', 'safetynote'],
  reward_exp: ['獎勵exp', '奖励exp', 'rewardexp'],
  reward_dp: ['獎勵dp', '奖励dp', 'rewarddp'],
}

const normHeader = (s: unknown) => String(s ?? '').replace(/[\s_]/g, '').toLowerCase()
const toInt = (v: unknown) => { const n = parseInt(String(v ?? '').replace(/[^\d.-]/g, ''), 10); return Number.isFinite(n) ? n : 0 }
const toNum = (v: unknown) => { const n = parseFloat(String(v ?? '').replace(/[^\d.-]/g, '')); return Number.isFinite(n) ? n : 0 }
const str = (v: unknown) => String(v ?? '').trim()
// 資料來源正規化：先排除「無手錶／皆可」（新手 GPS），再判斷 strava/手錶（進階）。
// 例：P01「DOR系統里程；無手錶」→gps、P02「DOR/STRAVA/手錶皆可」→gps、P03+「STRAVA/手錶…」→strava。
const normSrc = (v: unknown) => {
  const s = String(v ?? '').toLowerCase()
  if (/無手錶|無手表|不需手錶|不需手表|皆可/.test(s)) return 'gps'
  if (/strava|手錶|手表|watch|garmin/.test(s)) return 'strava'
  return 'gps'
}

export default function AdminPersonalTasksPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [plans, setPlans] = useState<PersonalPlan[] | null>(null)
  const [tasks, setTasks] = useState<PersonalTask[] | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminPersonalTasksApi.list(t)
      .then((r) => { setPlans(r.plans); setTasks(r.tasks) })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「事件任務」權限（個人任務沿用同權限）')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  // 每計畫的任務數（現況）
  const taskCountByPlan = useMemo(() => {
    const m: Record<string, number> = {}
    for (const t of tasks ?? []) m[t.plan_code] = (m[t.plan_code] ?? 0) + 1
    return m
  }, [tasks])

  // ── 解析 xlsx（單一「任務」工作表，每列＝計畫欄位＋任務欄位；計畫由 計畫ID 分組） ──
  async function parseFile(file: File) {
    setErr(''); setMsg(''); setPending(null)
    try {
      const XLSX = await import('xlsx')
      const wb = XLSX.read(await file.arrayBuffer(), { type: 'array' })
      // 選任務工作表：名稱含 All_Plans / 02 / 任務 / 個人；否則取列數最多者
      const namedSheet = wb.SheetNames.find((x) => /all[\s_]*plans|02|任務|个人|個人/i.test(x))
      let ws = namedSheet ? wb.Sheets[namedSheet] : null
      if (!ws) {
        let bestN = -1
        for (const n of wb.SheetNames) {
          const rows = XLSX.utils.sheet_to_json(wb.Sheets[n], { defval: '' })
          if (rows.length > bestN) { bestN = rows.length; ws = wb.Sheets[n] }
        }
      }
      if (!ws) throw new Error('檔案內沒有工作表')
      const rows = XLSX.utils.sheet_to_json<Record<string, unknown>>(ws, { defval: '' })
      if (rows.length === 0) throw new Error('工作表沒有資料列')

      // 依實際標題建立 欄位→標題鍵 對照
      const headers = Object.keys(rows[0])
      const keyMap: Record<string, string | undefined> = {}
      for (const f of Object.keys(FIELDS)) {
        keyMap[f] = headers.find((h) => FIELDS[f].some((c) => normHeader(h).includes(c)))
      }
      const g = (row: Record<string, unknown>, f: string) => (keyMap[f] ? row[keyMap[f] as string] : '')
      if (!keyMap.plan_code || !keyMap.day) throw new Error('找不到「計畫ID」或「Day」欄位，請確認是課表工作表（如 02_All_Plans）')

      const planMap = new Map<string, ParsedPlan>()
      const outTasks: ParsedTask[] = []
      for (const row of rows) {
        const code = str(g(row, 'plan_code'))
        const day = toInt(g(row, 'day'))
        if (!code || day <= 0) continue // 略過非任務列（表頭說明、空列）
        const src = normSrc(g(row, 'data_source'))
        outTasks.push({
          plan_code: code, day, week: toInt(g(row, 'week')),
          title: str(g(row, 'title')), workout: str(g(row, 'workout')), workout_type: str(g(row, 'workout_type')),
          target_km: toNum(g(row, 'target_km')), target_min: toInt(g(row, 'target_min')), intensity: str(g(row, 'intensity')),
          complete_cond: str(g(row, 'complete_cond')), reward_exp: toInt(g(row, 'reward_exp')), reward_dp: toInt(g(row, 'reward_dp')),
          data_source: src, safety_note: str(g(row, 'safety_note')),
        })
        if (!planMap.has(code)) {
          planMap.set(code, {
            code, name: str(g(row, 'plan_name')) || code, lifecycle: str(g(row, 'lifecycle')),
            stage_order: toInt(g(row, 'stage_order')), target_km: 0, target_time: '',
            entry_note: str(g(row, 'entry_note')), data_source: src,
          })
        }
      }
      const outPlans = [...planMap.values()].sort((a, b) => (a.stage_order - b.stage_order) || a.code.localeCompare(b.code))
      if (outPlans.length === 0 || outTasks.length === 0) throw new Error('沒有讀到有效的計畫/任務（請確認每列含 計畫ID 與 Day）')
      setPending({ plans: outPlans, tasks: outTasks, fileName: file.name })
      setMsg(`已解析 ${file.name}：${outPlans.length} 計畫、${outTasks.length} 任務 — 請確認後按「確認寫入」`)
    } catch (e: any) {
      setErr(e?.message || '解析失敗，請確認是 .xlsx 檔')
    }
  }

  async function confirmImport() {
    if (!token || !pending) return
    setBusy(true); setErr(''); setMsg('')
    try {
      const r = await adminPersonalTasksApi.import(token, { plans: pending.plans, tasks: pending.tasks })
      setMsg(`✓ 已寫入 ${r.plans} 計畫、${r.tasks} 任務`)
      setPending(null)
      load()
    } catch (e: any) {
      setErr(e?.message || '寫入失敗')
    } finally { setBusy(false) }
  }

  // ── 匯出現況為單一「個人任務」工作表（每列含計畫＋任務欄位，可編輯後再匯入）──
  async function exportXlsx() {
    setErr(''); setMsg('')
    try {
      const XLSX = await import('xlsx')
      const byCode: Record<string, PersonalPlan> = {}
      for (const p of plans ?? []) byCode[p.code] = p
      const ordered = (tasks ?? []).slice().sort((a, b) =>
        (byCode[a.plan_code]?.stage_order ?? 0) - (byCode[b.plan_code]?.stage_order ?? 0) ||
        a.plan_code.localeCompare(b.plan_code) || a.day - b.day)
      const mk = (t: PersonalTask | null, p?: PersonalPlan) => ({
        '計畫ID': t?.plan_code ?? p?.code ?? '', '計畫名稱': (t ? byCode[t.plan_code]?.name : p?.name) ?? '',
        '階段序': (t ? byCode[t.plan_code]?.stage_order : p?.stage_order) ?? '',
        '生命周期層級': (t ? byCode[t.plan_code]?.lifecycle : p?.lifecycle) ?? '',
        '資料來源': t?.data_source ?? p?.data_source ?? '', '進入門檻': (t ? byCode[t.plan_code]?.entry_note : p?.entry_note) ?? '',
        'Day': t?.day ?? '', 'Week': t?.week ?? '', 'DOR任務文案': t?.title ?? '', '訓練菜單': t?.workout ?? '',
        '課表類型': t?.workout_type ?? '', '目標里程KM': t?.target_km ?? '', '預估時間Min': t?.target_min ?? '',
        '強度': t?.intensity ?? '', '完成條件': t?.complete_cond ?? '', '安全退階規則': t?.safety_note ?? '',
        '獎勵EXP': t?.reward_exp ?? '', '獎勵DP': t?.reward_dp ?? '',
      })
      const rows = ordered.length ? ordered.map((t) => mk(t)) : [mk(null)] // 無資料 → 輸出含標題的空白模板
      const ws = XLSX.utils.json_to_sheet(rows)
      const wb = XLSX.utils.book_new()
      XLSX.utils.book_append_sheet(wb, ws, '個人任務')
      XLSX.writeFile(wb, '個人任務.xlsx')
      setMsg('✓ 已匯出 個人任務.xlsx')
    } catch (e: any) { setErr(e?.message || '匯出失敗') }
  }

  return (
    <div style={{ maxWidth: 860, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>個人任務</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        跑者生命週期 10 計畫，每計畫 100 天鏈式任務。用 xlsx 灌初始資料（每列＝計畫欄位＋任務欄位，計畫由「計畫ID」分組）。
        匯入為 <b>更新式</b>：依 計畫ID／(計畫,Day) 覆蓋，<b>保留玩家既有進度</b>；前置任務鏈由系統自動連結。
        入口是否對玩家顯示，請到 <Link href="/admin/system" style={{ color: 'var(--fug)' }}>系統設定 → 個人任務入口</Link> 控制。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      {/* 現況 */}
      <div style={card}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
          <Stat label="計畫" value={plans ? plans.length : '—'} />
          <Stat label="任務總數" value={tasks ? tasks.length : '—'} />
          <div style={{ marginLeft: 'auto', display: 'flex', gap: 10 }}>
            <button onClick={exportXlsx} style={ghostBtn}>匯出 xlsx</button>
            <label style={{ ...primaryBtn, cursor: 'pointer' }}>
              匯入 xlsx
              <input type="file" accept=".xlsx,.xls" style={{ display: 'none' }}
                onChange={(e) => { const f = e.target.files?.[0]; if (f) parseFile(f); e.target.value = '' }} />
            </label>
          </div>
        </div>

        {plans && plans.length > 0 && (
          <div style={{ marginTop: 14, display: 'flex', flexDirection: 'column', gap: 6 }}>
            {plans.map((p) => (
              <div key={p.id} style={planRow}>
                <span style={chip}>{p.code}</span>
                <span style={{ fontWeight: 700, fontSize: 13.5, flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</span>
                <span style={{ ...chip, background: 'var(--bg-2)', color: 'var(--tx-dim)' }}>{p.data_source === 'strava' ? 'Strava／手錶' : 'GPS'}</span>
                <span style={{ fontSize: 12, color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums', width: 64, textAlign: 'right' }}>{taskCountByPlan[p.code] ?? 0} 天</span>
                <span style={{ fontSize: 11, color: p.enabled ? 'var(--fug)' : 'var(--tx-faint)', width: 40, textAlign: 'right' }}>{p.enabled ? '啟用' : '停用'}</span>
              </div>
            ))}
          </div>
        )}
        {plans && plans.length === 0 && (
          <div style={{ marginTop: 12, fontSize: 13, color: 'var(--tx-dim)' }}>尚無資料 — 匯入 <code>DOR_跑者生命周期_100Days訓練計劃.xlsx</code> 即可灌入。</div>
        )}
      </div>

      {/* 匯入預覽 / 確認 */}
      {pending && (
        <div style={{ ...card, marginTop: 16, borderColor: 'var(--fug)' }}>
          <div style={{ fontWeight: 800, fontSize: 15 }}>匯入預覽</div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 4 }}>
            檔案：{pending.fileName}　—　{pending.plans.length} 計畫、{pending.tasks.length} 任務
          </div>
          <div style={{ marginTop: 10, maxHeight: 220, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 5 }}>
            {pending.plans.map((p) => {
              const n = pending.tasks.filter((t) => t.plan_code === p.code).length
              return (
                <div key={p.code} style={planRow}>
                  <span style={chip}>{p.code}</span>
                  <span style={{ fontWeight: 700, fontSize: 13, flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</span>
                  <span style={{ ...chip, background: 'var(--bg-2)', color: 'var(--tx-dim)' }}>{p.data_source === 'strava' ? 'Strava' : 'GPS'}</span>
                  <span style={{ fontSize: 12, color: 'var(--tx-dim)', width: 64, textAlign: 'right' }}>{n} 天</span>
                </div>
              )
            })}
          </div>
          <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
            <button onClick={confirmImport} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>{busy ? '寫入中…' : '確認寫入'}</button>
            <button onClick={() => { setPending(null); setMsg('') }} disabled={busy} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}

function Stat({ label, value }: { label: string; value: number | string }) {
  return (
    <div>
      <div style={{ fontSize: 22, fontWeight: 900, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums', lineHeight: 1 }}>{value}</div>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 3 }}>{label}</div>
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16 }
const planRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 10, padding: '6px 8px', background: 'var(--bg-0, #0d0f14)', borderRadius: 8 }
const chip: React.CSSProperties = { fontSize: 11, fontWeight: 800, color: 'var(--fug)', background: 'rgba(46,196,138,.14)', borderRadius: 6, padding: '2px 8px', flexShrink: 0 }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '8px 16px', cursor: 'pointer', fontSize: 13 }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 16px', cursor: 'pointer', fontSize: 13 }
