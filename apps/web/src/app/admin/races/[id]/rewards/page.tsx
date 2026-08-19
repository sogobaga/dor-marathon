'use client'

// 後台「獎勵管理」（賽後抽獎）。分兩套並行、依賽事 event_mode 分流：
//   personal（個人挑戰模式）：完成者名單（每一筆完成＝一個抽獎資格），沿用 registrations.reward_status 舊制
//     （P5，見 personal-challenge-mode 設計）——見下方 PersonalRewardsPage，邏輯完全不動。
//   其餘所有模式：獎勵管理一般化（migration 135）——抽獎資格底線＝完賽（各分組 target_distance_km，
//     與前台排行榜/完賽證明同一條線），可對同一賽事建立多次抽獎批次，範圍(scope)分「全體完賽者」或
//     「獲勝分組內完賽者」兩種——見下方 GeneralRewardsPage。
// 入口：admin/races 列表頁「🎁 獎勵」連結，兩種模式皆顯示（見 page.tsx handleRewardsClick）。

import { useCallback, useEffect, useState } from 'react'
import { useRouter, useParams } from 'next/navigation'
import Link from 'next/link'
import {
  adminRacesApi, adminRewardsApi, adminRewardDrawsApi,
  type RewardCompletionRow, type RewardCompletionsResponse,
  type RaceDetail, type RewardDraw, type RewardDrawScope, type RewardDrawWinRule, type RewardWinnerRow,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

function fmt(iso: string) {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function AdminRaceRewardsPage() {
  const router = useRouter()
  const params = useParams()
  const raceId = params.id as string

  const [token, setTokenState] = useState<string | null>(null)
  const [race, setRace] = useState<RaceDetail | null>(null)
  const [loadErr, setLoadErr] = useState('')

  useEffect(() => {
    const t = getToken()
    if (!t) {
      router.replace('/admin/login')
      return
    }
    setTokenState(t)
    adminRacesApi
      .get(t, raceId)
      .then((res) => setRace(res.race))
      .catch((e: any) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else if (e?.status === 403) {
          setLoadErr('此頁僅具「賽事管理」權限的管理者可存取')
        } else if (e?.status === 404) {
          setLoadErr('找不到此賽事')
        } else {
          setLoadErr(e?.message || '載入失敗')
        }
      })
  }, [raceId, router])

  if (loadErr) {
    return (
      <div style={{ maxWidth: 1040, margin: '0 auto' }}>
        <Link href={`/admin/races/${raceId}`} style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
          ← 返回賽事編輯
        </Link>
        <div style={{ color: 'var(--hunt)', padding: '20px 0', fontSize: 14 }}>{loadErr}</div>
      </div>
    )
  }
  if (!token || !race) {
    return <div style={{ padding: 40, color: 'var(--tx-dim)' }}>載入中…</div>
  }

  return race.event_mode === 'personal'
    ? <PersonalRewardsPage token={token} raceId={raceId} raceTitle={race.title} />
    : <GeneralRewardsPage token={token} raceId={raceId} race={race} />
}

// ============================================================
// personal（個人挑戰模式）：既有獎勵管理，完全比照原本邏輯，僅將 token/raceTitle 改為外層傳入
// （避免與外層重複打 adminRacesApi.get）。
// ============================================================

const PAGE = 50
const STATUS_FILTERS: { k: string; t: string }[] = [
  { k: 'all', t: '全部' },
  { k: '', t: '待處理' },
  { k: 'won', t: '中獎待發' },
  { k: 'fulfilled', t: '已發放' },
]

function PersonalRewardsPage({ token, raceId, raceTitle }: { token: string; raceId: string; raceTitle: string }) {
  const router = useRouter()
  const [data, setData] = useState<RewardCompletionsResponse | null>(null)
  const [filter, setFilter] = useState('all')
  const [offset, setOffset] = useState(0)
  const [err, setErr] = useState('')
  const [drawN, setDrawN] = useState(1)
  const [drawing, setDrawing] = useState(false)

  const load = useCallback((t: string, off: number, f: string) => {
    adminRewardsApi
      .list(t, raceId, { reward_status: f, limit: PAGE, offset: off })
      .then((res) => setData(res))
      .catch((e: any) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else if (e?.status === 403) {
          setErr('此頁僅具「賽事管理」權限的管理者可存取')
        } else if (e?.status === 404) {
          setErr('找不到此賽事，或此賽事非個人挑戰模式')
        } else {
          setErr(e?.message || '載入失敗')
        }
      })
  }, [raceId, router])

  useEffect(() => {
    load(token, 0, 'all')
  }, [token, load])

  function changeFilter(f: string) {
    setFilter(f)
    setOffset(0)
    load(token, 0, f)
  }
  function page(off: number) {
    setOffset(off)
    load(token, off, filter)
  }

  async function handleDraw() {
    if (drawN <= 0) return
    if (!window.confirm(`確定要從「待處理」完成者中隨機抽出 ${drawN} 位中獎？此動作無法復原。`)) return
    setDrawing(true)
    setErr('')
    try {
      const res = await adminRewardsApi.draw(token, raceId, drawN)
      window.alert(`已抽出 ${res.count} 位中獎者。`)
      load(token, offset, filter)
    } catch (e: any) {
      setErr(e?.message || '抽獎失敗')
    } finally {
      setDrawing(false)
    }
  }

  async function handleRowSave(regId: string, status: string, note: string) {
    await adminRewardsApi.update(token, regId, { reward_status: status as '' | 'won' | 'fulfilled', reward_note: note })
    load(token, offset, filter)
  }

  const summary = data?.summary

  return (
    <div style={{ maxWidth: 1040, margin: '0 auto' }}>
      <Link href={`/admin/races/${raceId}`} style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
        ← 返回賽事編輯
      </Link>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '14px 0 4px' }}>獎勵管理{raceTitle ? `：${raceTitle}` : ''}</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>
        個人挑戰模式完成者名單。每一筆「完成」皆為獨立抽獎資格；LINE Point 由後台人工發放，此頁僅記錄資格與發放狀態。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}

      {summary && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 10, margin: '14px 0' }}>
          <SummaryTile label="完成總筆數" value={summary.total} c="var(--tx)" />
          <SummaryTile label="待處理" value={summary.pending} c="var(--tx-dim)" />
          <SummaryTile label="中獎待發" value={summary.won} c="var(--gold)" />
          <SummaryTile label="已發放" value={summary.fulfilled} c="var(--fug)" />
        </div>
      )}

      <div
        style={{
          display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', margin: '14px 0',
          background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 14,
        }}
      >
        <span style={{ fontSize: 14 }}>🎲 隨機抽</span>
        <input
          type="number"
          min={1}
          value={drawN}
          onChange={(e) => setDrawN(Math.max(1, parseInt(e.target.value, 10) || 1))}
          style={{ ...inp, width: 80 }}
        />
        <span style={{ fontSize: 14 }}>位（僅從「待處理」抽取，抽中即設為中獎待發）</span>
        <button onClick={handleDraw} disabled={drawing} style={{ ...drawBtn, opacity: drawing ? 0.6 : 1 }}>
          {drawing ? '抽獎中…' : '開始抽獎'}
        </button>
      </div>

      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', margin: '10px 0 14px' }}>
        {STATUS_FILTERS.map((f) => (
          <button
            key={f.k}
            onClick={() => changeFilter(f.k)}
            style={{ ...chip, ...(filter === f.k ? chipOn : {}) }}
          >
            {f.t}
          </button>
        ))}
      </div>

      <div style={{ border: '1px solid var(--line)', borderRadius: 12, overflow: 'hidden' }}>
        <div style={{ ...rowStyle, ...headRow }}>
          <span style={{ flex: '0 0 120px' }}>完成時間</span>
          <span style={{ flex: '0 0 50px', textAlign: 'center' }}>次數</span>
          <span style={{ flex: '0 0 130px' }}>姓名</span>
          <span style={{ flex: '0 0 190px' }}>Email</span>
          <span style={{ flex: '0 0 110px' }}>狀態</span>
          <span style={{ flex: 1, minWidth: 0 }}>備註</span>
          <span style={{ flex: '0 0 60px' }} />
        </div>
        {!data && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>載入中…</div>}
        {data && data.completions.length === 0 && (
          <div style={{ padding: 16, color: 'var(--tx-dim)' }}>目前沒有符合條件的完成紀錄</div>
        )}
        {data?.completions.map((c) => (
          <RewardRow key={c.registration_id} row={c} onSave={handleRowSave} />
        ))}
      </div>

      {data && (
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 12, fontSize: 13, color: 'var(--tx-dim)' }}>
          <span>共 {data.count} 筆 · 第 {Math.floor(offset / PAGE) + 1} / {Math.max(1, Math.ceil(data.count / PAGE))} 頁</span>
          <div style={{ display: 'flex', gap: 8 }}>
            <button disabled={offset <= 0} onClick={() => page(Math.max(0, offset - PAGE))} style={{ ...pgBtn, opacity: offset <= 0 ? 0.4 : 1 }}>
              上一頁
            </button>
            <button disabled={offset + PAGE >= data.count} onClick={() => page(offset + PAGE)} style={{ ...pgBtn, opacity: offset + PAGE >= data.count ? 0.4 : 1 }}>
              下一頁
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function SummaryTile({ label, value, c }: { label: string; value: number; c: string }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 14px' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</div>
      <div style={{ fontSize: 20, fontWeight: 800, color: c }}>{value}</div>
    </div>
  )
}

function RewardRow({
  row,
  onSave,
}: {
  row: RewardCompletionRow
  onSave: (regId: string, status: string, note: string) => Promise<void>
}) {
  const [status, setStatus] = useState(row.reward_status)
  const [note, setNote] = useState(row.reward_note || '')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  async function handleSave() {
    setSaving(true)
    setSaved(false)
    try {
      await onSave(row.registration_id, status, note)
      setSaved(true)
    } catch {
      // 錯誤交給父層 err 狀態顯示；此處只需結束 loading
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={rowStyle}>
      <span style={{ flex: '0 0 120px', color: 'var(--tx-dim)', fontSize: 12 }}>{fmt(row.completed_at)}</span>
      <span style={{ flex: '0 0 50px', textAlign: 'center' }}>#{row.attempt_no}</span>
      <span style={{ flex: '0 0 130px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.user_name}</span>
      <span style={{ flex: '0 0 190px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--tx-dim)', fontSize: 12 }}>
        {row.user_email}
      </span>
      <span style={{ flex: '0 0 110px' }}>
        <select value={status} onChange={(e) => { setStatus(e.target.value as '' | 'won' | 'fulfilled'); setSaved(false) }} style={sel}>
          <option value="">待處理</option>
          <option value="won">中獎待發</option>
          <option value="fulfilled">已發放</option>
        </select>
      </span>
      <span style={{ flex: 1, minWidth: 0 }}>
        <input
          value={note}
          onChange={(e) => { setNote(e.target.value); setSaved(false) }}
          placeholder="LINE Point 序號/備註"
          style={{ ...inp, padding: '5px 8px', fontSize: 12, width: '100%' }}
        />
      </span>
      <span style={{ flex: '0 0 60px', textAlign: 'right' }}>
        <button onClick={handleSave} disabled={saving} style={saveBtn}>
          {saving ? '…' : saved ? '✓' : '儲存'}
        </button>
      </span>
    </div>
  )
}

// ============================================================
// 其餘模式：獎勵管理一般化（migration 135）——完賽者池抽獎，可建多批次
// ============================================================

const SCOPE_LABEL: Record<RewardDrawScope, string> = { all_finishers: '全體完賽者', winning_group: '獲勝分組內完賽者' }
const WIN_RULE_LABEL: Record<string, string> = { highest_metric: '分組指標最高', first_to_target: '最先達到分組目標' }

function GeneralRewardsPage({ token, raceId, race }: { token: string; raceId: string; race: RaceDetail }) {
  const router = useRouter()
  const [draws, setDraws] = useState<RewardDraw[] | null>(null)
  const [err, setErr] = useState('')
  const [creating, setCreating] = useState(false)

  const [title, setTitle] = useState('')
  const [scope, setScope] = useState<RewardDrawScope>('all_finishers')
  const [winRule, setWinRule] = useState<RewardDrawWinRule>('highest_metric')
  const [winTaskId, setWinTaskId] = useState('')
  const [winnerCount, setWinnerCount] = useState(1)
  const [excludePrior, setExcludePrior] = useState(true)

  const groupTasks = race.tasks.filter((t) => t.scope === 'group_team' && t.metric_type !== 'checkpoint')
  const groupNameById: Record<string, string> = {}
  for (const g of race.groups) if (g.id) groupNameById[g.id] = g.name

  const loadDraws = useCallback(() => {
    adminRewardDrawsApi
      .list(token, raceId)
      .then((res) => setDraws(res.draws))
      .catch((e: any) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else {
          setErr(e?.message || '載入失敗')
        }
      })
  }, [token, raceId, router])

  useEffect(() => {
    loadDraws()
  }, [loadDraws])

  async function handleCreate() {
    if (winnerCount <= 0) return
    if (scope === 'winning_group' && !winRule) {
      setErr('請選擇獲勝組判定方式')
      return
    }
    if (!window.confirm(`確定要建立這次抽獎批次（預計抽 ${winnerCount} 位）？此動作無法復原。`)) return
    setCreating(true)
    setErr('')
    try {
      const res = await adminRewardDrawsApi.create(token, raceId, {
        title: title.trim(),
        scope,
        win_rule: scope === 'winning_group' ? winRule : undefined,
        win_task_id: scope === 'winning_group' ? winTaskId : undefined,
        winner_count: winnerCount,
        exclude_prior: excludePrior,
      })
      const actual = res.draw.winners.length
      window.alert(
        actual < winnerCount
          ? `資格池僅 ${res.draw.pool_size} 人，已全池皆中獎（${actual} 位，少於原訂 ${winnerCount} 位）。`
          : `已抽出 ${actual} 位中獎者。`,
      )
      setTitle('')
      setWinnerCount(1)
      loadDraws()
    } catch (e: any) {
      setErr(e?.message || '抽獎失敗')
    } finally {
      setCreating(false)
    }
  }

  async function handleWinnerSave(winnerId: string, status: string, note: string) {
    await adminRewardDrawsApi.updateWinner(token, winnerId, { reward_status: status as '' | 'fulfilled', reward_note: note })
    loadDraws()
  }

  return (
    <div style={{ maxWidth: 1040, margin: '0 auto' }}>
      <Link href={`/admin/races/${raceId}`} style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
        ← 返回賽事編輯
      </Link>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '14px 0 4px' }}>獎勵管理：{race.title}</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>
        抽獎資格底線＝完賽（達成各分組完賽門檻）。可對此賽事建立多次抽獎批次（如頭獎/普獎分開抽）；
        「排除先前批次中獎者」勾選時，本批次不會再抽到本賽事先前批次已中獎的人。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}

      <div
        style={{
          display: 'flex', flexDirection: 'column', gap: 10, margin: '14px 0',
          background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 16,
        }}
      >
        <div style={{ fontSize: 14, fontWeight: 700 }}>🎲 建立抽獎批次</div>
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="批次名稱（如：頭獎）"
            style={{ ...inp, width: 180 }}
          />
          <select value={scope} onChange={(e) => setScope(e.target.value as RewardDrawScope)} style={inp}>
            <option value="all_finishers">全體完賽者</option>
            <option value="winning_group">獲勝分組內完賽者</option>
          </select>
          {scope === 'winning_group' && (
            <>
              <select value={winRule} onChange={(e) => setWinRule(e.target.value as RewardDrawWinRule)} style={inp}>
                <option value="highest_metric">獲勝判定：分組指標最高</option>
                <option value="first_to_target">獲勝判定：最先達到分組目標</option>
              </select>
              <select value={winTaskId} onChange={(e) => setWinTaskId(e.target.value)} style={inp}>
                <option value="">判定任務：預設（第一個分組任務）</option>
                {groupTasks.map((t) => (
                  <option key={t.id} value={t.id}>判定任務：{t.title}</option>
                ))}
              </select>
              {groupTasks.length === 0 && (
                <span style={{ fontSize: 12, color: 'var(--hunt)' }}>此賽事無可用分組(團體)任務，無法用獲勝組抽獎</span>
              )}
            </>
          )}
          <span style={{ fontSize: 14 }}>中獎</span>
          <input
            type="number"
            min={1}
            value={winnerCount}
            onChange={(e) => setWinnerCount(Math.max(1, parseInt(e.target.value, 10) || 1))}
            style={{ ...inp, width: 70 }}
          />
          <span style={{ fontSize: 14 }}>位</span>
        </div>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--tx-dim)' }}>
          <input type="checkbox" checked={excludePrior} onChange={(e) => setExcludePrior(e.target.checked)} />
          排除先前批次中獎者
        </label>
        <div>
          <button onClick={handleCreate} disabled={creating} style={{ ...drawBtn, opacity: creating ? 0.6 : 1 }}>
            {creating ? '抽獎中…' : '開始抽獎'}
          </button>
        </div>
      </div>

      {!draws && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>載入中…</div>}
      {draws && draws.length === 0 && (
        <div style={{ padding: 16, color: 'var(--tx-dim)', border: '1px solid var(--line)', borderRadius: 12 }}>
          尚無抽獎批次
        </div>
      )}
      {draws?.map((d) => (
        <DrawCard key={d.id} draw={d} groupNameById={groupNameById} onSaveWinner={handleWinnerSave} />
      ))}
    </div>
  )
}

function DrawCard({
  draw,
  groupNameById,
  onSaveWinner,
}: {
  draw: RewardDraw
  groupNameById: Record<string, string>
  onSaveWinner: (winnerId: string, status: string, note: string) => Promise<void>
}) {
  const winningGroupNames = (draw.winning_group_ids || []).map((id) => groupNameById[id] || id)
  return (
    <div style={{ border: '1px solid var(--line)', borderRadius: 12, overflow: 'hidden', marginBottom: 16 }}>
      <div style={{ background: 'var(--bg-2)', padding: '12px 16px', display: 'flex', flexWrap: 'wrap', gap: 14, alignItems: 'center' }}>
        <span style={{ fontSize: 15, fontWeight: 700 }}>{draw.title || '（未命名批次）'}</span>
        <Badge>{SCOPE_LABEL[draw.scope]}</Badge>
        {draw.win_rule && <Badge>{WIN_RULE_LABEL[draw.win_rule] || draw.win_rule}</Badge>}
        {winningGroupNames.length > 0 && <Badge tone="gold">獲勝組：{winningGroupNames.join('、')}</Badge>}
        <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>資格池 {draw.pool_size} 人 · 中獎 {draw.winners.length}/{draw.winner_count} 位</span>
        <span style={{ fontSize: 12, color: 'var(--tx-faint)', marginLeft: 'auto' }}>{fmt(draw.drawn_at)}</span>
      </div>
      <div style={{ ...rowStyle, ...headRow }}>
        <span style={{ flex: '0 0 130px' }}>姓名</span>
        <span style={{ flex: '0 0 190px' }}>Email</span>
        <span style={{ flex: '0 0 100px' }}>分組</span>
        <span style={{ flex: '0 0 100px' }}>狀態</span>
        <span style={{ flex: 1, minWidth: 0 }}>備註</span>
        <span style={{ flex: '0 0 60px' }} />
      </div>
      {draw.winners.map((w) => (
        <WinnerRow key={w.id} row={w} onSave={onSaveWinner} />
      ))}
    </div>
  )
}

function Badge({ children, tone }: { children: React.ReactNode; tone?: 'gold' }) {
  return (
    <span
      style={{
        fontSize: 12, padding: '3px 9px', borderRadius: 999,
        background: tone === 'gold' ? 'rgba(255,196,0,.12)' : 'var(--bg-3, var(--bg-1))',
        color: tone === 'gold' ? 'var(--gold)' : 'var(--tx-dim)',
        border: `1px solid ${tone === 'gold' ? 'rgba(255,196,0,.3)' : 'var(--line-2)'}`,
      }}
    >
      {children}
    </span>
  )
}

function WinnerRow({
  row,
  onSave,
}: {
  row: RewardWinnerRow
  onSave: (winnerId: string, status: string, note: string) => Promise<void>
}) {
  const [status, setStatus] = useState(row.reward_status)
  const [note, setNote] = useState(row.reward_note || '')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  async function handleSave() {
    setSaving(true)
    setSaved(false)
    try {
      await onSave(row.id, status, note)
      setSaved(true)
    } catch {
      // 錯誤交給父層 err 狀態顯示
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={rowStyle}>
      <span style={{ flex: '0 0 130px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.user_name}</span>
      <span style={{ flex: '0 0 190px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--tx-dim)', fontSize: 12 }}>
        {row.user_email}
      </span>
      <span style={{ flex: '0 0 100px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 12, color: 'var(--tx-dim)' }}>
        {row.group_name || '—'}
      </span>
      <span style={{ flex: '0 0 100px' }}>
        <select value={status} onChange={(e) => { setStatus(e.target.value as '' | 'fulfilled'); setSaved(false) }} style={sel}>
          <option value="">中獎待發</option>
          <option value="fulfilled">已發放</option>
        </select>
      </span>
      <span style={{ flex: 1, minWidth: 0 }}>
        <input
          value={note}
          onChange={(e) => { setNote(e.target.value); setSaved(false) }}
          placeholder="獎品/序號/備註"
          style={{ ...inp, padding: '5px 8px', fontSize: 12, width: '100%' }}
        />
      </span>
      <span style={{ flex: '0 0 60px', textAlign: 'right' }}>
        <button onClick={handleSave} disabled={saving} style={saveBtn}>
          {saving ? '…' : saved ? '✓' : '儲存'}
        </button>
      </span>
    </div>
  )
}

const rowStyle: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 12, padding: '10px 14px', borderBottom: '1px solid var(--line)', fontSize: 13 }
const headRow: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.05em', fontWeight: 700 }
const chip: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '5px 12px', fontSize: 12.5, cursor: 'pointer', color: 'var(--tx-dim)' }
const chipOn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: '1px solid var(--fug)' }
const pgBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 14px', fontSize: 13, cursor: 'pointer', color: 'var(--tx)' }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px 12px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit' }
const sel: React.CSSProperties = { ...inp, padding: '5px 6px', fontSize: 12 }
const saveBtn: React.CSSProperties = { background: 'var(--gold)', color: '#fff', fontWeight: 700, border: 'none', borderRadius: 8, padding: '5px 10px', cursor: 'pointer', fontSize: 12 }
const drawBtn: React.CSSProperties = { background: 'var(--gold)', color: '#fff', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 18px', cursor: 'pointer', fontSize: 14 }
