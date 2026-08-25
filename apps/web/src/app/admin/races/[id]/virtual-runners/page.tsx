'use client'

// 後台「虛擬選手」（單一賽事的加入/移除）。仿 rewards/page.tsx 的守門模式：先 adminRacesApi.get 拿
// RaceDetail 確認 403/404，再渲染主體。選手庫（新增/批次產生/停用/刪除/等級參數）在 admin/virtual-runners，
// 此頁只處理「把哪些虛擬選手加入這場賽事、分到哪組」。

import { useCallback, useEffect, useState } from 'react'
import { useRouter, useParams } from 'next/navigation'
import Link from 'next/link'
import {
  adminRacesApi, adminVirtualRunnersApi,
  type RaceDetail, type VirtualRunner, type VirtualRunnerLevelPreset,
  type VirtualRunnerRaceAssignedRow, type VirtualRunnerRaceGroupRow, type VirtualRunnerAssignSkip,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const GENDER_LABEL: Record<'male' | 'female', string> = { male: '男', female: '女' }
const SKIP_REASON_LABEL: Record<string, string> = {
  duplicate: '已加入過（重複）', group_full: '分組已滿', disabled: '已停用', not_found: '找不到此選手',
}

export default function AdminRaceVirtualRunnersPage() {
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

  return <RaceVirtualRunnersPanel token={token} raceId={raceId} race={race} />
}

type Mode = 'random' | 'manual'

function RaceVirtualRunnersPanel({ token, raceId, race }: { token: string; raceId: string; race: RaceDetail }) {
  const router = useRouter()
  const [assigned, setAssigned] = useState<VirtualRunnerRaceAssignedRow[] | null>(null)
  const [groups, setGroups] = useState<VirtualRunnerRaceGroupRow[] | null>(null)
  const [candidatesCount, setCandidatesCount] = useState(0)
  const [allRunners, setAllRunners] = useState<VirtualRunner[] | null>(null)
  const [presets, setPresets] = useState<VirtualRunnerLevelPreset[] | null>(null)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const [mode, setMode] = useState<Mode>('random')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [randomCount, setRandomCount] = useState(5)
  const [groupId, setGroupId] = useState('') // '' = 逐位隨機分組
  const [busy, setBusy] = useState(false)
  const [lastSkipped, setLastSkipped] = useState<VirtualRunnerAssignSkip[] | null>(null)

  const load = useCallback(() => {
    Promise.all([adminVirtualRunnersApi.race(token, raceId), adminVirtualRunnersApi.list(token)])
      .then(([raceRes, listRes]) => {
        setAssigned(raceRes.assigned)
        setGroups(raceRes.groups)
        setCandidatesCount(raceRes.candidates_count)
        setAllRunners(listRes.runners)
        setPresets(listRes.presets)
      })
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
    load()
  }, [load])

  const levelLabel = (lv: string) => presets?.find((p) => p.level === lv)?.label ?? lv
  const runnerName = (userId: string) => allRunners?.find((r) => r.user_id === userId)?.name ?? userId

  const assignedIds = new Set((assigned ?? []).map((a) => a.user_id))
  const candidates = (allRunners ?? []).filter((r) => r.enabled && !assignedIds.has(r.user_id))

  function toggleSelect(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function handleAssign() {
    setErr('')
    setMsg('')
    setLastSkipped(null)
    const body: { user_ids?: string[]; random_count?: number; group_id?: string } = {}
    if (groupId) body.group_id = groupId
    if (mode === 'manual') {
      if (selected.size === 0) {
        setErr('請至少選擇一位選手')
        return
      }
      body.user_ids = Array.from(selected)
    } else {
      if (randomCount <= 0) {
        setErr('隨機抽取數量須大於 0')
        return
      }
      body.random_count = randomCount
    }
    setBusy(true)
    try {
      const res = await adminVirtualRunnersApi.assign(token, raceId, body)
      setMsg(`✓ 已加入 ${res.added} 位` + (res.skipped.length > 0 ? `，跳過 ${res.skipped.length} 位` : ''))
      setLastSkipped(res.skipped.length > 0 ? res.skipped : null)
      setSelected(new Set())
      load()
    } catch (e: any) {
      setErr(e?.message || '加入失敗')
    } finally {
      setBusy(false)
    }
  }

  async function handleRemove(userID: string, name: string) {
    if (!confirm(`確定將「${name}」移出此賽事？`)) return
    setErr('')
    try {
      await adminVirtualRunnersApi.unassign(token, raceId, userID)
      load()
    } catch (e: any) {
      setErr(e?.message || '移除失敗')
    }
  }

  return (
    <div style={{ maxWidth: 1040, margin: '0 auto' }}>
      <Link href={`/admin/races/${raceId}`} style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
        ← 返回賽事編輯
      </Link>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '14px 0 4px' }}>虛擬選手：{race.title}</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>
        將虛擬選手（is_virtual 人頭帳號）加入此賽事以補熱度/陪跑。可指定多選或隨機抽取，分組可指定或逐位隨機。
        選手庫的新增/批次產生/停用請至「虛擬選手」總管理頁。
      </p>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13, whiteSpace: 'pre-wrap' }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}
      {lastSkipped && (
        <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 14px', fontSize: 12.5, color: 'var(--tx-dim)', marginBottom: 10 }}>
          跳過名單：{lastSkipped.map((s) => `${runnerName(s.user_id)}（${SKIP_REASON_LABEL[s.reason] ?? s.reason}）`).join('、')}
        </div>
      )}

      {/* 各分組名額現況 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 10, margin: '14px 0' }}>
        {groups?.map((g) => (
          <div key={g.id} style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 14px' }}>
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{g.name}</div>
            <div style={{ fontSize: 18, fontWeight: 800 }}>
              {g.slots_taken} <span style={{ fontSize: 12, color: 'var(--tx-dim)', fontWeight: 400 }}>/ {g.slot_limit ?? '不限'}</span>
            </div>
          </div>
        ))}
        {groups && groups.length === 0 && <div style={{ color: 'var(--tx-faint)', fontSize: 13 }}>此賽事無分組</div>}
        {!groups && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>載入中…</div>}
      </div>

      {/* 加入操作 */}
      <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 16, marginBottom: 20 }}>
        <div style={{ fontWeight: 800, marginBottom: 12 }}>
          加入虛擬選手 <span style={{ fontWeight: 400, fontSize: 12, color: 'var(--tx-dim)' }}>（候選 {candidatesCount} 位）</span>
        </div>

        <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
          <button onClick={() => setMode('random')} style={{ ...chip, ...(mode === 'random' ? chipOn : {}) }}>🎲 隨機抽 N 位</button>
          <button onClick={() => setMode('manual')} style={{ ...chip, ...(mode === 'manual' ? chipOn : {}) }}>✓ 手動勾選</button>
        </div>

        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end', marginBottom: 12 }}>
          {mode === 'random' ? (
            <Field label="隨機抽取數量">
              <input
                type="number" min={1} style={{ ...inp, width: 100 }} value={randomCount}
                onChange={(e) => setRandomCount(Math.max(1, parseInt(e.target.value, 10) || 1))}
              />
            </Field>
          ) : (
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', height: 36, display: 'flex', alignItems: 'center' }}>已勾選 {selected.size} 位</div>
          )}
          <Field label="分組（空＝逐位隨機分組）">
            <select style={{ ...inp, width: 220 }} value={groupId} onChange={(e) => setGroupId(e.target.value)}>
              <option value="">隨機分組</option>
              {groups?.map((g) => (
                <option key={g.id} value={g.id}>{g.name}（{g.slots_taken}/{g.slot_limit ?? '不限'}）</option>
              ))}
            </select>
          </Field>
        </div>

        {mode === 'manual' && (
          <div style={{ maxHeight: 260, overflowY: 'auto', border: '1px solid var(--line-2)', borderRadius: 10, padding: 8, marginBottom: 12 }}>
            {!allRunners && <div style={{ color: 'var(--tx-dim)', fontSize: 13, padding: 8 }}>載入中…</div>}
            {allRunners && candidates.length === 0 && (
              <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: 8 }}>無可加入的選手（已全數加入此賽事，或選手庫尚無啟用中選手）</div>
            )}
            {candidates.map((r) => (
              <label key={r.user_id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '5px 6px', fontSize: 13, cursor: 'pointer' }}>
                <input type="checkbox" checked={selected.has(r.user_id)} onChange={() => toggleSelect(r.user_id)} />
                {r.name}
                <span style={{ color: 'var(--tx-dim)', fontSize: 11.5 }}>{GENDER_LABEL[r.gender]} · {levelLabel(r.level)}</span>
              </label>
            ))}
          </div>
        )}

        <button onClick={handleAssign} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>
          {busy ? '加入中…' : '加入'}
        </button>
      </div>

      {/* 已加入清單 */}
      <div style={{ fontSize: 16, fontWeight: 800, margin: '20px 0 10px' }}>
        已加入清單<span style={{ color: 'var(--tx-dim)', fontWeight: 400, fontSize: 13 }}>（{assigned?.length ?? 0}）</span>
      </div>
      <div style={{ border: '1px solid var(--line)', borderRadius: 12, overflow: 'hidden' }}>
        <div style={{ ...rowStyle, ...headRow }}>
          <span style={{ flex: '0 0 140px' }}>姓名</span>
          <span style={{ flex: '0 0 50px' }}>性別</span>
          <span style={{ flex: '0 0 150px' }}>等級</span>
          <span style={{ flex: '0 0 150px' }}>分組</span>
          <span style={{ flex: '0 0 100px' }}>報名狀態</span>
          <span style={{ flex: '0 0 60px' }} />
        </div>
        {!assigned && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>載入中…</div>}
        {assigned && assigned.length === 0 && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>尚未加入任何虛擬選手</div>}
        {assigned?.map((a) => (
          <div key={a.user_id} style={rowStyle}>
            <span style={{ flex: '0 0 140px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.name}</span>
            <span style={{ flex: '0 0 50px', color: 'var(--tx-dim)', fontSize: 12 }}>{GENDER_LABEL[a.gender]}</span>
            <span style={{ flex: '0 0 150px', color: 'var(--tx-dim)', fontSize: 12 }}>{levelLabel(a.level)}</span>
            <span style={{ flex: '0 0 150px', color: 'var(--tx-dim)', fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{a.group_name || '—'}</span>
            <span style={{ flex: '0 0 100px', color: 'var(--tx-dim)', fontSize: 12 }}>{a.reg_status}</span>
            <span style={{ flex: '0 0 60px', textAlign: 'right' }}>
              <button onClick={() => handleRemove(a.user_id, a.name)} style={{ ...ghostBtn, color: 'var(--hunt)', padding: '4px 10px', fontSize: 12 }}>
                移除
              </button>
            </span>
          </div>
        ))}
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

const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5 }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 14px', cursor: 'pointer', fontSize: 13 }
const chip: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '6px 14px', fontSize: 12.5, cursor: 'pointer', color: 'var(--tx-dim)' }
const chipOn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: '1px solid var(--fug)' }
const rowStyle: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 12, padding: '10px 14px', borderBottom: '1px solid var(--line)', fontSize: 13 }
const headRow: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.05em', fontWeight: 700 }
