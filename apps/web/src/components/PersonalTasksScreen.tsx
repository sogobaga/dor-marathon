'use client'

import { useCallback, useEffect, useState } from 'react'
import { personalTasksApi, type PersonalPlan, type PersonalTask, type PersonalCurrent } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'

// 個人任務頁：跑者生命週期 10 計畫，每計畫 100 天鏈式任務（完成前一個才開下一個）。
// Phase 1：選計畫 → 看進度 → 手動回報完成。Phase 2：自動里程結算（依 data_source 比對 target_km + 星星）。
export default function PersonalTasksScreen({ onBack }: { onBack: () => void }) {
  const user = useUser()
  const [plans, setPlans] = useState<PersonalPlan[] | null>(null)
  const [err, setErr] = useState('')
  const [sel, setSel] = useState<PersonalPlan | null>(null) // 選中的計畫
  const [tasks, setTasks] = useState<PersonalTask[] | null>(null)
  const [current, setCurrent] = useState<PersonalCurrent | null>(null) // 目前任務的里程進度（自動結算）
  const [busy, setBusy] = useState('') // 完成中的 taskId
  const [toast, setToast] = useState('')

  // 自動里程結算：達標的任務會被自動完成。回傳目前任務進度供顯示；有新完成則跳提示。
  const settle = useCallback(async () => {
    if (!getUserToken()) return
    try {
      const r = await withUserAuth((t) => personalTasksApi.settle(t))
      setCurrent(r.current)
      if (r.settled.length > 0) {
        const s = r.settled[r.settled.length - 1]
        const extra = r.settled.length > 1 ? `（共 ${r.settled.length} 項）` : ''
        setToast(`自動結算：Day ${s.day} 完成 ${'★'.repeat(s.stars)}${s.reward_exp ? ` +${s.reward_exp}EXP` : ''}${s.reward_dp ? ` +${s.reward_dp}DP` : ''}${extra}`)
      }
      return r.settled.length > 0
    } catch { /* 結算失敗不擋畫面 */ }
  }, [])

  const loadPlans = useCallback(() => {
    if (!getUserToken()) { setPlans([]); return }
    withUserAuth((t) => personalTasksApi.listPlans(t))
      .then((r) => setPlans(r.plans))
      .catch((e) => setErr(e?.message || '載入失敗'))
  }, [])
  // 進頁：先自動結算，再載入計畫列表（列表即反映最新完成數）
  useEffect(() => { (async () => { await settle(); loadPlans() })() }, [settle, loadPlans, user])

  const loadDetail = useCallback((code: string) => {
    setTasks(null)
    withUserAuth((t) => personalTasksApi.planDetail(t, code))
      .then((r) => { setSel(r.plan); setTasks(r.tasks) })
      .catch((e) => setErr(e?.message || '載入失敗'))
  }, [])

  // 開啟計畫：先結算（抓最新完成 + 目前任務進度），再載入該計畫任務
  const openPlan = useCallback(async (p: PersonalPlan) => {
    setSel(p); setTasks(null)
    await settle()
    loadDetail(p.code)
  }, [settle, loadDetail])

  useEffect(() => { if (toast) { const t = setTimeout(() => setToast(''), 3000); return () => clearTimeout(t) } }, [toast])

  async function complete(task: PersonalTask) {
    if (!sel) return
    setBusy(task.id); setErr('')
    try {
      const r = await withUserAuth((t) => personalTasksApi.complete(t, task.id))
      setToast(r.already ? '已完成過了' : `完成！${'★'.repeat(r.stars)}${r.reward_exp ? ` +${r.reward_exp}EXP` : ''}${r.reward_dp ? ` +${r.reward_dp}DP` : ''}`)
      await settle()          // 完成後再結算一次（可能連帶推進下一個里程任務）
      loadDetail(sel.code)    // 重抓進度（done/stars）
    } catch (e: any) {
      setErr(e?.message || '完成失敗')
    } finally { setBusy('') }
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      {/* Header：與其他頁一致的高度 */}
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={sel ? () => { setSel(null); setTasks(null) } : onBack} style={backBtn}>← {sel ? '計畫列表' : '返回'}</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>{sel ? sel.name : '個人任務'}</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '6px 18px 28px' }}>
        {err && <div style={{ color: 'var(--hunt)', padding: '8px 2px', fontSize: 13 }}>{err}</div>}

        {!sel ? (
          /* ── 計畫列表 ── */
          <PlanList plans={plans} onOpen={openPlan} />
        ) : (
          /* ── 計畫詳情（任務列表）── */
          <TaskList plan={sel} tasks={tasks} current={current} busy={busy} onComplete={complete} />
        )}
      </div>

      {toast && (
        <div style={{ position: 'absolute', left: '50%', bottom: 'calc(env(safe-area-inset-bottom, 0px) + 28px)', transform: 'translateX(-50%)', background: 'var(--fug)', color: '#05140e', fontWeight: 800, fontSize: 13, padding: '9px 18px', borderRadius: 999, boxShadow: '0 6px 20px rgba(0,0,0,.3)', zIndex: 600, maxWidth: '86%', textAlign: 'center' }}>
          {toast}
        </div>
      )}
    </div>
  )
}

function PlanList({ plans, onOpen }: { plans: PersonalPlan[] | null; onOpen: (p: PersonalPlan) => void }) {
  if (plans === null) return <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
  if (plans.length === 0) return (
    <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, lineHeight: 1.9, padding: '24px 2px', textAlign: 'center' }}>
      目前尚無任務計畫<br /><span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>（後台匯入計畫後即會顯示）</span>
    </div>
  )
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '4px 2px 2px', lineHeight: 1.7 }}>
        依你的跑者階段循序解鎖，每天一個任務，完成前一個才開下一個。
      </p>
      {plans.map((p, i) => {
        const pct = p.total > 0 ? Math.round((p.completed / p.total) * 100) : 0
        const done = p.total > 0 && p.completed >= p.total
        return (
          <button key={p.id} onClick={() => onOpen(p)} style={planCard}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span style={stageChip}>階段 {p.stage_order || i + 1}</span>
              <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</span>
              {done && <span style={{ ...srcChip, background: 'var(--fug)', color: '#05140e' }}>已完成</span>}
            </div>
            <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
              {p.lifecycle && <span style={srcChip}>{p.lifecycle}</span>}
              <span style={srcChip}>{p.data_source === 'strava' ? 'Strava／手錶' : 'GPS 里程'}</span>
              {p.target_km > 0 && <span style={srcChip}>目標 {p.target_km}K</span>}
            </div>
            <div style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 10 }}>
              <div style={barOuter}><div style={{ ...barInner, width: `${pct}%` }} /></div>
              <span style={{ fontSize: 11.5, color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums', flexShrink: 0 }}>{p.completed}/{p.total}</span>
            </div>
          </button>
        )
      })}
    </div>
  )
}

function TaskList({ plan, tasks, current, busy, onComplete }: { plan: PersonalPlan; tasks: PersonalTask[] | null; current: PersonalCurrent | null; busy: string; onComplete: (t: PersonalTask) => void }) {
  if (tasks === null) return <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
  if (tasks.length === 0) return <div style={{ color: 'var(--tx-dim)', fontSize: 13, padding: '24px 2px', textAlign: 'center' }}>此計畫尚無任務內容</div>
  // 第一個未完成的任務＝目前可完成的；其後為鎖定（完成前一個才開下一個）
  const firstOpen = tasks.findIndex((t) => !t.done)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {plan.entry_note && <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '4px 2px 6px', lineHeight: 1.75 }}>{plan.entry_note}</p>}
      {tasks.map((t, i) => {
        const locked = !t.done && i !== firstOpen
        const isCurrent = i === firstOpen
        const acc = current?.task_id === t.id ? current.acc_km : 0 // 目前任務的窗口累積里程（自動結算）
        const pct = t.target_km > 0 ? Math.min(100, Math.round((acc / t.target_km) * 100)) : 0
        return (
          <div key={t.id} style={{ ...taskCard, opacity: locked ? 0.5 : 1, borderColor: isCurrent ? 'var(--fug)' : 'var(--line)' }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
              <span style={{ fontSize: 11, fontWeight: 800, color: 'var(--fug)', flexShrink: 0 }}>Day {t.day}</span>
              <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--tx)', flex: 1, minWidth: 0 }}>{t.title || t.workout || '（未命名任務）'}</span>
              {t.done && <span style={{ fontSize: 13, color: 'var(--gold)', flexShrink: 0 }}>{'★'.repeat(t.stars || 1)}</span>}
              {locked && <span style={{ fontSize: 12, color: 'var(--tx-faint)', flexShrink: 0 }}>🔒</span>}
            </div>
            {t.workout && t.workout !== t.title && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.6 }}>{t.workout}</div>}
            <div style={{ display: 'flex', gap: 6, marginTop: 6, flexWrap: 'wrap', alignItems: 'center' }}>
              {t.target_km > 0 && <span style={metaChip}>{t.target_km}K</span>}
              {t.target_min > 0 && <span style={metaChip}>{t.target_min} 分</span>}
              {t.intensity && <span style={metaChip}>{t.intensity}</span>}
              <span style={{ ...metaChip, color: 'var(--fug)' }}>{t.data_source === 'strava' ? 'Strava／手錶' : 'GPS'}</span>
              {(t.reward_exp > 0 || t.reward_dp > 0) && (
                <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginLeft: 'auto' }}>
                  獎勵{t.reward_exp > 0 ? ` ${t.reward_exp}EXP` : ''}{t.reward_dp > 0 ? ` ${t.reward_dp}DP` : ''}
                </span>
              )}
            </div>
            {isCurrent && t.target_km > 0 && (
              <div style={{ marginTop: 9 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, marginBottom: 4 }}>
                  <span style={{ color: 'var(--fug)', fontWeight: 700 }}>自動結算 · 依{t.data_source === 'strava' ? ' Strava／手錶' : ' GPS'} 里程</span>
                  <span style={{ color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums' }}>{acc.toFixed(1)} / {t.target_km} K</span>
                </div>
                <div style={barFull}><div style={{ ...barInner, width: `${pct}%` }} /></div>
                <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 5 }}>跑到目標里程會自動完成並發獎；也可手動標記完成。</div>
              </div>
            )}
            {isCurrent && (
              <button onClick={() => onComplete(t)} disabled={busy === t.id} style={{ ...doneBtn, opacity: busy === t.id ? 0.5 : 1 }}>
                {busy === t.id ? '回報中…' : (t.target_km > 0 ? '手動標記完成' : '回報完成')}
              </button>
            )}
          </div>
        )
      })}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 11px', cursor: 'pointer', fontSize: 12.5, flexShrink: 0 }
const planCard: React.CSSProperties = { display: 'block', width: '100%', textAlign: 'left', background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14, cursor: 'pointer', fontFamily: 'inherit', color: 'inherit' }
const stageChip: React.CSSProperties = { fontSize: 10.5, fontWeight: 800, color: 'var(--fug)', background: 'rgba(46,196,138,.14)', borderRadius: 6, padding: '2px 7px', flexShrink: 0 }
const srcChip: React.CSSProperties = { fontSize: 10.5, fontWeight: 600, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 6, padding: '2px 8px' }
const barOuter: React.CSSProperties = { flex: 1, height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden' }
const barFull: React.CSSProperties = { height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden' }
const barInner: React.CSSProperties = { height: '100%', background: 'var(--fug)', borderRadius: 999, transition: 'width .3s' }
const taskCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '11px 13px' }
const metaChip: React.CSSProperties = { fontSize: 10.5, fontWeight: 600, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 6, padding: '2px 8px' }
const doneBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 0', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
