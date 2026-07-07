'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import useSWR from 'swr'
import { personalTasksApi, type PersonalPlan, type PersonalTask, type PersonalChallenge, type WorkoutSegment } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { refreshDashboard, useDashboard } from '@/lib/useDashboard'

// 個人任務頁（挑戰制）：選計畫 → 任務鏈。每個任務要先「挑戰」才開始計算；里程從挑戰起累積、
// 達標後「完成」才可按；「放棄」判失敗可重挑。可重複挑戰爬星 1→3★，難度遞增；休息日＝挑戰後
// 窗口內不能有任何里程。第一次挑戰免費，之後重挑扣 DP。
export default function PersonalTasksScreen({ onBack }: { onBack: () => void }) {
  const user = useUser()
  const { dash } = useDashboard()
  const uid = user?.id ?? null
  const { data: plansData, error: plansErr, mutate: mutatePlans } = useSWR(
    uid && getUserToken() ? ['personal-plans', uid] : null,
    () => withUserAuth((t) => personalTasksApi.listPlans(t)).then((r) => r.plans),
  )
  const plans = (plansData ?? null) as PersonalPlan[] | null
  const [err, setErr] = useState('')
  useEffect(() => { if (plansErr && !plansData) setErr('計畫載入失敗，請稍後再試') }, [plansErr, plansData])
  const [sel, setSel] = useState<PersonalPlan | null>(null)
  const [tasks, setTasks] = useState<PersonalTask[] | null>(null)
  const [challenge, setChallenge] = useState<PersonalChallenge | null>(null)
  const challengeAt = useRef(0) // 取得挑戰狀態的當下時間（給休息倒數本地補間）
  const [busy, setBusy] = useState('') // 進行中的動作 taskId
  const [toast, setToast] = useState('')
  const [navigating, setNavigating] = useState(false) // 帶往 GPS 追蹤的淡出轉場

  useEffect(() => { if (toast) { const t = setTimeout(() => setToast(''), 3200); return () => clearTimeout(t) } }, [toast])

  const applyChallenge = useCallback((c: PersonalChallenge | null) => { challengeAt.current = Date.now(); setChallenge(c) }, [])

  const fetchStatus = useCallback(async () => {
    if (!getUserToken()) return null
    try { const r = await withUserAuth((t) => personalTasksApi.status(t)); applyChallenge(r.challenge); return r.challenge } catch { return null }
  }, [applyChallenge])

  const loadDetail = useCallback((code: string) => {
    setTasks(null)
    withUserAuth((t) => personalTasksApi.planDetail(t, code))
      .then(async (r) => { setSel(r.plan); setTasks(r.tasks); await fetchStatus() })
      .catch((e) => setErr(e?.message || '載入失敗'))
  }, [fetchStatus])

  useEffect(() => { (async () => { await fetchStatus(); mutatePlans() })() }, [fetchStatus, mutatePlans, user])

  const openPlan = useCallback((p: PersonalPlan) => { setSel(p); setTasks(null); loadDetail(p.code) }, [loadDetail])

  // 有進行中挑戰時：每 3 秒回抓狀態（里程/休息窗口）
  const hasActive = !!(tasks && tasks.some((t) => t.active))
  useEffect(() => {
    if (!sel || !hasActive) return
    const id = setInterval(() => { fetchStatus() }, 3000)
    return () => clearInterval(id)
  }, [sel, hasActive, fetchStatus])

  // 結構化課表 → 帶到「GPS 跑步追蹤」：簡單淡出轉場後導頁（/track 開頁偵測進行中挑戰、321 倒數開始）
  function goToTrack() {
    setNavigating(true)
    setTimeout(() => { window.location.href = '/track' }, 380)
  }
  async function doChallenge(t: PersonalTask) {
    if (!sel) return
    setBusy(t.id); setErr('')
    try {
      const r = await withUserAuth((tk) => personalTasksApi.challenge(tk, t.id))
      if (t.workout_kind) { // 結構化課表：挑戰後帶到 GPS 追蹤跑
        if (r.charged_dp) refreshDashboard()
        goToTrack()
        return
      }
      if (r.charged_dp) { setToast(`挑戰開始 ★${r.tier}，扣 ${r.charged_dp} DP`); refreshDashboard() }
      else setToast(`挑戰開始 · 目標 ★${r.tier}`)
      loadDetail(sel.code); mutatePlans(); setBusy('')
    } catch (e: any) { setErr(e?.message || '挑戰失敗'); setBusy('') }
  }
  async function doAbandon(t: PersonalTask) {
    if (!sel) return
    setBusy(t.id); setErr('')
    try { await withUserAuth((tk) => personalTasksApi.abandon(tk, t.id)); setToast('已放棄，可重新挑戰'); applyChallenge(null); loadDetail(sel.code) }
    catch (e: any) { setErr(e?.message || '放棄失敗') } finally { setBusy('') }
  }
  async function doComplete(t: PersonalTask, opts?: { pain?: number; rpe?: number }) {
    if (!sel) return
    setBusy(t.id); setErr('')
    try {
      const r = await withUserAuth((tk) => personalTasksApi.complete(tk, t.id, opts))
      setToast(`完成！${'★'.repeat(r.stars)}${r.reward_exp ? ` +${r.reward_exp}EXP` : ''}${r.reward_dp ? ` +${r.reward_dp}DP` : ''}`)
      applyChallenge(null); refreshDashboard(); mutatePlans(); loadDetail(sel.code)
    } catch (e: any) { setErr(e?.message || '尚未達標') } finally { setBusy('') }
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={sel ? () => { setSel(null); setTasks(null) } : onBack} style={backBtn}>← {sel ? '計畫列表' : '返回'}</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>{sel ? sel.name : '個人任務'}</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '6px 18px 28px' }}>
        {err && <div style={{ color: 'var(--hunt)', padding: '8px 2px', fontSize: 13 }}>{err}</div>}
        {!sel ? (
          <PlanList plans={plans} onOpen={openPlan} />
        ) : (
          <TaskList plan={sel} tasks={tasks} challenge={challenge} challengeAt={challengeAt.current} isVip={!!dash?.is_vip}
            busy={busy} onChallenge={doChallenge} onAbandon={doAbandon} onComplete={doComplete} onTick={fetchStatus} onGoTrack={goToTrack} />
        )}
      </div>

      {navigating && (
        <div style={{ position: 'absolute', inset: 0, zIndex: 3400, background: 'var(--bg)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, animation: 'fadeIn .3s ease' }}>
          <div style={{ fontSize: 30 }}>🏃‍♂️</div>
          <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>前往 GPS 跑步追蹤…</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>準備開始課表挑戰</div>
        </div>
      )}

      {toast && (
        <div style={{ position: 'absolute', left: '50%', bottom: 'calc(env(safe-area-inset-bottom, 0px) + 28px)', transform: 'translateX(-50%)', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, fontSize: 13, padding: '9px 18px', borderRadius: 999, boxShadow: '0 6px 20px rgba(0,0,0,.3)', zIndex: 600, maxWidth: '86%', textAlign: 'center' }}>
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
        依你的跑者階段循序解鎖，每天一個任務。按「挑戰」開始計算，達標才可完成；可重複挑戰爬到 3★。
      </p>
      {plans.map((p, i) => {
        const pct = p.total > 0 ? Math.round((p.completed / p.total) * 100) : 0
        const done = p.total > 0 && p.completed >= p.total
        return (
          <button key={p.id} onClick={() => onOpen(p)} style={planCard}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span style={stageChip}>階段 {p.stage_order || i + 1}</span>
              <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</span>
              {done && <span style={{ ...srcChip, background: 'var(--fug)', color: 'var(--fug-ink)' }}>已完成</span>}
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

// 課表型別中文標籤
const WORKOUT_LABELS: Record<string, string> = {
  interval: '間歇', aerobic: '有氧', tempo: '節奏', easy: '輕鬆', recovery: '恢復',
  progression: '漸速', fartlek: '法特雷克', pyramid: '金字塔', norwegian4x4: '挪威4×4', variable: '變速',
}
// 任務類型標籤
function taskKind(t: PersonalTask): { label: string; color: string; kind: 'mileage' | 'rest' | 'manual' | 'workout' } {
  if (t.workout_kind) return { label: WORKOUT_LABELS[t.workout_kind] || '課表', color: 'var(--fug)', kind: 'workout' }
  if (t.target_km > 0) return { label: '里程', color: 'var(--fug)', kind: 'mileage' }
  if (/休息/.test(t.workout_type || '')) return { label: '休息日', color: 'var(--violet)', kind: 'rest' }
  if (/恢復|交叉|XT|走/.test(t.workout_type || '')) return { label: '恢復/交叉', color: 'var(--tx-dim)', kind: 'manual' }
  return { label: '課表', color: 'var(--tx-dim)', kind: 'manual' }
}
// 分段課表摘要：「暖身 2K → 400m ×6 → 緩和 2K」
function segSummary(segs?: WorkoutSegment[] | null): string {
  if (!segs || !segs.length) return ''
  return segs.map((s) => {
    const d = s.target_type === 'distance' ? (s.target >= 1000 ? `${s.target / 1000}K` : `${s.target}m`) : `${Math.round(s.target / 60)}分`
    const reps = s.reps && s.reps > 1 ? ` ×${s.reps}` : ''
    return `${s.label || s.kind} ${d}${reps}`
  }).join(' → ')
}

const RPE_OPTS = [{ v: 2, l: '很輕鬆' }, { v: 4, l: '輕鬆' }, { v: 6, l: '適中' }, { v: 8, l: '偏累' }, { v: 10, l: '很累' }]
const PAIN_OPTS = [{ v: 0, l: '無' }, { v: 1, l: '輕微' }, { v: 2, l: '中等' }, { v: 3, l: '明顯' }]
const stars3 = (n: number) => '★'.repeat(Math.max(0, n)) + '☆'.repeat(Math.max(0, 3 - n))

function TaskList({ plan, tasks, challenge, challengeAt, isVip, busy, onChallenge, onAbandon, onComplete, onTick, onGoTrack }: {
  plan: PersonalPlan; tasks: PersonalTask[] | null; challenge: PersonalChallenge | null; challengeAt: number; isVip: boolean
  busy: string; onChallenge: (t: PersonalTask) => void; onAbandon: (t: PersonalTask) => void
  onComplete: (t: PersonalTask, opts?: { pain?: number; rpe?: number }) => void; onTick: () => void; onGoTrack: () => void
}) {
  if (tasks === null) return <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
  if (tasks.length === 0) return <div style={{ color: 'var(--tx-dim)', fontSize: 13, padding: '24px 2px', textAlign: 'center' }}>此計畫尚無任務內容</div>
  const vipLocked = plan.stage_order >= 4 && !isVip // 階段 4+ 且非 VIP → 課表鎖住
  const firstOpen = tasks.findIndex((t) => !t.done) // 第一個未完成＝目前前沿
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {plan.entry_note && <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '4px 2px 6px', lineHeight: 1.75 }}>{plan.entry_note}</p>}
      {tasks.map((t, i) => {
        const kind = taskKind(t)
        const locked = !t.done && firstOpen !== -1 && i !== firstOpen
        const ch = challenge && challenge.task_id === t.id ? challenge : null
        return (
          <TaskCard key={t.id} t={t} kind={kind} locked={locked} ch={ch} challengeAt={challengeAt} vipLocked={vipLocked}
            busy={busy === t.id} onChallenge={onChallenge} onAbandon={onAbandon} onComplete={onComplete} onTick={onTick} onGoTrack={onGoTrack} />
        )
      })}
    </div>
  )
}

function TaskCard({ t, kind, locked, ch, challengeAt, vipLocked, busy, onChallenge, onAbandon, onComplete, onTick, onGoTrack }: {
  t: PersonalTask; kind: ReturnType<typeof taskKind>; locked: boolean; ch: PersonalChallenge | null; challengeAt: number; vipLocked: boolean
  busy: boolean; onChallenge: (t: PersonalTask) => void; onAbandon: (t: PersonalTask) => void
  onComplete: (t: PersonalTask, opts?: { pain?: number; rpe?: number }) => void; onTick: () => void; onGoTrack: () => void
}) {
  const [reporting, setReporting] = useState(false)
  const [rpe, setRpe] = useState<number | null>(null)
  const [pain, setPain] = useState<number | null>(null)
  const active = t.active
  const cost = t.attempts > 0 ? t.retry_dp_cost : 0
  const maxed = t.stars >= 3
  const canChallenge = !locked && !active && !maxed && (t.done || t.stars === 0) // 前沿或已完成可爬星
  const acc = ch?.acc_km ?? 0
  const pct = ch && ch.kind === 'mileage' && ch.target_km > 0 ? Math.min(100, Math.round((acc / ch.target_km) * 100)) : 0

  return (
    <div style={{ ...taskCard, opacity: locked ? 0.5 : 1, borderColor: active ? 'var(--fug)' : t.done ? 'var(--line-2)' : 'var(--line)' }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
        <span style={{ fontSize: 11, fontWeight: 800, color: 'var(--fug)', flexShrink: 0 }}>Day {t.day}</span>
        <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--tx)', flex: 1, minWidth: 0 }}>{t.title || t.workout || '（未命名任務）'}</span>
        <span style={{ fontSize: 13, color: t.stars > 0 ? 'var(--gold)' : 'var(--tx-faint)', flexShrink: 0, letterSpacing: 1 }}>{stars3(t.stars)}</span>
        {locked && <span style={{ fontSize: 12, color: 'var(--tx-faint)', flexShrink: 0 }}>🔒</span>}
      </div>
      {t.workout && t.workout !== t.title && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.6 }}>{t.workout}</div>}
      <div style={{ display: 'flex', gap: 6, marginTop: 6, flexWrap: 'wrap', alignItems: 'center' }}>
        <span style={{ ...metaChip, color: kind.color, fontWeight: 700 }}>{kind.label}</span>
        {kind.kind === 'mileage' && <span style={metaChip}>{t.target_km}K</span>}
        {t.intensity && <span style={metaChip}>{t.intensity}</span>}
        {kind.kind === 'mileage' && <span style={{ ...metaChip, color: 'var(--fug)' }}>{t.data_source === 'strava' ? 'Strava／手錶' : 'GPS'}</span>}
        {(t.reward_exp > 0 || t.reward_dp > 0) && (
          <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginLeft: 'auto' }}>
            每星{t.reward_exp > 0 ? ` ${t.reward_exp}EXP` : ''}{t.reward_dp > 0 ? ` ${t.reward_dp}DP` : ''}
          </span>
        )}
      </div>
      {(active || (!locked && !t.done)) && t.complete_cond && (
        <div style={{ fontSize: 12, color: 'var(--tx)', marginTop: 8, padding: '6px 9px', background: 'var(--bg-2)', borderRadius: 8 }}>完成條件：{t.complete_cond}</div>
      )}

      {/* ── workout：結構化課表（在 GPS 追蹤頁執行）── */}
      {kind.kind === 'workout' && (
        <>
          {segSummary(t.segments) && <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.6, padding: '7px 10px', background: 'var(--bg-2)', borderRadius: 8 }}>📋 {segSummary(t.segments)}</div>}
          {active ? (
            <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
              <button onClick={() => onAbandon(t)} disabled={busy} style={{ ...abandonBtn, opacity: busy ? 0.5 : 1 }}>放棄</button>
              <button onClick={onGoTrack} disabled={busy} style={{ ...doneBtn, marginTop: 0, flex: 1, opacity: busy ? 0.6 : 1 }}>▶ 前往 GPS 追蹤（進行中）</button>
            </div>
          ) : vipLocked ? (
            <button disabled style={{ marginTop: 10, width: '100%', background: 'rgba(255,194,75,.14)', color: 'var(--gold)', border: '1px solid var(--gold)', borderRadius: 9, padding: '9px 0', fontSize: 13.5, fontWeight: 800, cursor: 'not-allowed', fontFamily: 'inherit' }}>🔒 VIP 解鎖挑戰任務</button>
          ) : canChallenge ? (
            <button onClick={() => onChallenge(t)} disabled={busy} style={{ ...doneBtn, opacity: busy ? 0.5 : 1 }}>
              {busy ? '前往中…' : t.done ? `再挑戰課表 ★${t.stars + 1}　·　DP ${t.retry_dp_cost}` : cost > 0 ? `重新挑戰課表　·　DP ${cost}` : '▶ 前往 GPS 追蹤挑戰'}
            </button>
          ) : maxed ? (
            <div style={{ marginTop: 9, textAlign: 'center', fontSize: 12, color: 'var(--gold)', fontWeight: 700 }}>★★★ 已達最高星</div>
          ) : null}
        </>
      )}

      {/* ── 進行中挑戰（mileage / rest / manual）── */}
      {kind.kind !== 'workout' && active && (
        <div style={{ marginTop: 10 }}>
          {!ch ? (
            <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>讀取挑戰狀態…</div>
          ) : ch.kind === 'mileage' ? (
            <>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11.5, marginBottom: 4 }}>
                <span style={{ color: 'var(--fug)', fontWeight: 700 }}>挑戰中 ★{ch.tier} · 依{t.data_source === 'strava' ? ' Strava／手錶' : ' GPS'} 里程</span>
                <span style={{ color: ch.met ? 'var(--fug)' : 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums', fontWeight: ch.met ? 800 : 400 }}>{acc.toFixed(1)} / {ch.target_km} K</span>
              </div>
              <div style={barFull}><div style={{ ...barInner, width: `${pct}%`, background: ch.met ? 'var(--fug)' : 'var(--fug)' }} /></div>
              <div style={{ fontSize: 10.5, color: ch.met ? 'var(--fug)' : 'var(--tx-faint)', marginTop: 5 }}>{ch.met ? '已達標！可按「完成」領星與獎勵。' : '從按下挑戰起累積里程；達標後「完成」才可按。'}</div>
            </>
          ) : ch.kind === 'rest' ? (
            <RestStatus ch={ch} challengeAt={challengeAt} onTick={onTick} />
          ) : (
            <div style={{ fontSize: 11.5, color: 'var(--fug)', fontWeight: 700 }}>挑戰中 ★{ch.tier} · 完成後可直接回報</div>
          )}

          {/* manual 挑戰的選填回報表單 */}
          {ch && ch.kind === 'manual' && reporting && (
            <div style={{ marginTop: 10, padding: 11, background: 'var(--bg-2)', borderRadius: 10, display: 'flex', flexDirection: 'column', gap: 10 }}>
              <ChipRow title="訓練後感受（選填）" opts={RPE_OPTS} value={rpe} onPick={(v) => setRpe(v === rpe ? null : v)} />
              <ChipRow title="身體狀況（選填）" opts={PAIN_OPTS} value={pain} onPick={(v) => setPain(v === pain ? null : v)} />
            </div>
          )}

          <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
            <button onClick={() => onAbandon(t)} disabled={busy} style={{ ...abandonBtn, opacity: busy ? 0.5 : 1 }}>放棄</button>
            {ch && ch.kind === 'manual' && !reporting ? (
              <button onClick={() => setReporting(true)} disabled={busy} style={{ ...doneBtn, marginTop: 0, flex: 1, opacity: busy ? 0.5 : 1 }}>回報完成</button>
            ) : (
              <button onClick={() => onComplete(t, { pain: pain ?? undefined, rpe: rpe ?? undefined })}
                disabled={busy || !ch?.met}
                style={{ ...doneBtn, marginTop: 0, flex: 1, opacity: busy || !ch?.met ? 0.45 : 1, cursor: ch?.met ? 'pointer' : 'not-allowed' }}>
                {busy ? '送出中…' : ch?.met ? '完成 ✓' : '未達標'}
              </button>
            )}
          </div>
        </div>
      )}

      {/* ── 挑戰／重挑按鈕（mileage / rest / manual）── */}
      {kind.kind !== 'workout' && canChallenge && (
        <button onClick={() => onChallenge(t)} disabled={busy} style={{ ...doneBtn, opacity: busy ? 0.5 : 1 }}>
          {busy ? '處理中…'
            : t.done ? `挑戰 ★${t.stars + 1}　·　DP ${t.retry_dp_cost}`
              : cost > 0 ? `重新挑戰　·　DP ${cost}` : '開始挑戰'}
        </button>
      )}
      {kind.kind !== 'workout' && maxed && !active && <div style={{ marginTop: 9, textAlign: 'center', fontSize: 12, color: 'var(--gold)', fontWeight: 700 }}>★★★ 已達最高星</div>}
    </div>
  )
}

// 休息日倒數：不能有里程；本地每秒補間，窗口到 0 時觸發一次回抓讓「完成」解鎖。
function RestStatus({ ch, challengeAt, onTick }: { ch: PersonalChallenge; challengeAt: number; onTick: () => void }) {
  const [, force] = useState(0)
  const firedRef = useRef(false)
  useEffect(() => {
    const id = setInterval(() => force((x) => x + 1), 1000)
    return () => clearInterval(id)
  }, [])
  const extraS = Math.max(0, (Date.now() - challengeAt) / 1000)
  const remain = Math.max(0, ch.rest_window_s - ch.elapsed_s - extraS)
  useEffect(() => { firedRef.current = false }, [challengeAt])
  if (remain <= 0 && !ch.met && !ch.failed && !firedRef.current) { firedRef.current = true; onTick() }
  const mm = Math.floor(remain / 60), ss = Math.floor(remain % 60)
  if (ch.failed) return (
    <div style={{ fontSize: 12, color: 'var(--hunt)', fontWeight: 700, padding: '6px 0' }}>
      ✗ 休息窗口內偵測到里程 — 挑戰失敗，請「放棄」後重新挑戰。
    </div>
  )
  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11.5, marginBottom: 4 }}>
        <span style={{ color: 'var(--violet)', fontWeight: 700 }}>休息挑戰 ★{ch.tier} · 窗口內不能跑</span>
        <span style={{ color: ch.met ? 'var(--fug)' : 'var(--tx)', fontVariantNumeric: 'tabular-nums', fontWeight: 800 }}>
          {ch.met ? '完成窗口！' : `剩 ${mm}:${String(ss).padStart(2, '0')}`}
        </span>
      </div>
      <div style={barFull}><div style={{ ...barInner, width: `${ch.rest_window_s > 0 ? Math.min(100, Math.round((1 - remain / ch.rest_window_s) * 100)) : 0}%`, background: 'var(--violet)' }} /></div>
      <div style={{ fontSize: 10.5, color: ch.met ? 'var(--fug)' : 'var(--tx-faint)', marginTop: 5 }}>{ch.met ? '安靜度過！可按「完成」領星。' : '這段時間內不要產生任何里程，安靜度過就算成功。'}</div>
    </div>
  )
}

function ChipRow({ title, opts, value, onPick }: { title: string; opts: { v: number; l: string }[]; value: number | null; onPick: (v: number) => void }) {
  return (
    <div>
      <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginBottom: 5 }}>{title}</div>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        {opts.map((o) => (
          <button key={o.v} onClick={() => onPick(o.v)} style={{ ...chipBtn, ...(value === o.v ? chipBtnOn : null) }}>{o.l}</button>
        ))}
      </div>
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const planCard: React.CSSProperties = { display: 'block', width: '100%', textAlign: 'left', background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14, cursor: 'pointer', fontFamily: 'inherit', color: 'inherit' }
const stageChip: React.CSSProperties = { fontSize: 10.5, fontWeight: 800, color: 'var(--fug)', background: 'rgba(46,196,138,.14)', borderRadius: 6, padding: '2px 7px', flexShrink: 0 }
const srcChip: React.CSSProperties = { fontSize: 10.5, fontWeight: 600, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 6, padding: '2px 8px' }
const barOuter: React.CSSProperties = { flex: 1, height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden' }
const barFull: React.CSSProperties = { height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden' }
const barInner: React.CSSProperties = { height: '100%', background: 'var(--fug)', borderRadius: 999, transition: 'width .3s' }
const taskCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '11px 13px' }
const metaChip: React.CSSProperties = { fontSize: 10.5, fontWeight: 600, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 6, padding: '2px 8px' }
const doneBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 0', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
const abandonBtn: React.CSSProperties = { background: 'transparent', color: 'var(--hunt)', border: '1px solid var(--hunt)', borderRadius: 9, padding: '9px 16px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit', flexShrink: 0, fontWeight: 700 }
const chipBtn: React.CSSProperties = { background: 'var(--bg-1)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '5px 12px', cursor: 'pointer', fontSize: 12, fontFamily: 'inherit' }
const chipBtnOn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', borderColor: 'var(--fug)', fontWeight: 700 }
