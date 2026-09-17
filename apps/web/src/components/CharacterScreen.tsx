'use client'

// 遊戲化角色數值（第 21 套，參考 RO 素質系統）：只有 VVIP／白名單管理者能看到本頁（入口在
// MemberPanel「🎮 角色」，由 dashboard.rpg_entry 控管），本頁本身也直接打 /rpg/me，若後端判定
// 沒資格會回 403 ——雙保險，不只信前端的入口判斷（見任務決策 D3/D8）。
//
// DORPG P5（職業／測試等級／配點技能規則，見 scratchpad/dorpg_p5/CONTRACT.md）：新增「職業」
// 「測試等級」「技能」三個區塊，配點區改為 +1/Max/還原預設（移除 +5，因為加點成本隨數值遞增，
// 前端算不出精確的 5 點總價，見下方舊註解）。
// ⚠️ 本檔只依賴 lib/api.ts 的型別（FRONTEND 自己擁有、已按 CONTRACT.md 加好），不依賴
// lib/dorpg/{engine,fromApi,types}.ts（那些是 ENGINE 角色的檔案，本頁本來就不吃戰鬥引擎型別）。
import { useCallback, useEffect, useState } from 'react'
import {
  rpgApi,
  type EffectAtLevel,
  type JobDTO,
  type RpgMe,
  type RpgSkillsResponse,
  type RpgStatKey,
  type SkillDTO,
} from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import {
  STAT_META, DERIVED_META, resistLabel, BATTLE_DISPLAY_DEFAULTS, estimateAttackCooldownMs, estimateCastMs,
  jobEmoji, SKILL_KIND_LABEL, formatEffectAtLevel, sortJobs,
} from '@/lib/rpgMeta'

// 配點 400 錯誤代碼 → 中文（契約 §3：超過 stat_cap 回 400 {error:"stat_cap"}）。
const STAT_ERR_LABEL: Record<string, string> = { stat_cap: '已達該項素質上限' }
// 技能配點 400 錯誤代碼 → 中文（契約 §4／WIRE：prereq/no_points/max_level/min_level）。
const SKILL_ERR_LABEL: Record<string, string> = {
  prereq: '尚未達成前置技能等級需求',
  no_points: '技能點不足',
  max_level: '已達技能等級上限',
  min_level: '已是最低等級（無法再降級）',
  // 審查#3：降級會讓依賴此技能的其他已投資技能前置條件失效時，後端拒絕並回這個代碼。
  prereq_dependents: '請先降低依賴此技能的後續技能等級',
}
// 測試等級 400/403 錯誤代碼 → 中文（審查#2：test_level_enabled 關閉時 PUT /rpg/test-level 一律 403）。
const TEST_LEVEL_ERR_LABEL: Record<string, string> = {
  test_level_disabled: '測試等級功能已關閉',
}
function friendlyErr(e: any, table: Record<string, string>, fallback: string): string {
  const code = e?.message
  return (code && table[code]) || (typeof code === 'string' && code && code !== 'request failed' ? code : '') || fallback
}

// onOpenBattle：DORPG 戰鬥畫面入口（P0 靜態畫面預覽）；本頁已受 dash.rpg_entry 閘門，不另設資格判斷。
export default function CharacterScreen({ onBack, onOpenBattle }: { onBack: () => void; onOpenBattle?: () => void }) {
  const [data, setData] = useState<RpgMe | null>(null)
  const [jobs, setJobs] = useState<JobDTO[]>([])
  const [skillsData, setSkillsData] = useState<RpgSkillsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState('')
  const [actionErr, setActionErr] = useState('')
  const [flash, setFlash] = useState('')
  const [busyStat, setBusyStat] = useState<RpgStatKey | null>(null) // 配點請求進行中鎖住該列按鈕，避免連點超花
  const [busyJob, setBusyJob] = useState(false)
  const [busyLevel, setBusyLevel] = useState(false)
  const [busyResetStats, setBusyResetStats] = useState(false)
  const [busySkill, setBusySkill] = useState<string | null>(null)
  const [busyResetSkills, setBusyResetSkills] = useState(false)
  const [testLevelInput, setTestLevelInput] = useState('')

  function showErr(msg: string) {
    setActionErr(msg)
    window.setTimeout(() => setActionErr(''), 3500)
  }
  function showFlash(msg: string) {
    setFlash(msg)
    window.setTimeout(() => setFlash(''), 1800)
  }

  const load = useCallback(() => {
    if (!getUserToken()) { setLoading(false); setLoadErr('請先登入'); return }
    setLoading(true)
    Promise.all([
      withUserAuth((t) => rpgApi.me(t)),
      withUserAuth((t) => rpgApi.jobs(t)),
      withUserAuth((t) => rpgApi.skills(t)),
    ])
      .then(([me, jobsRes, skillsRes]) => {
        setData(me)
        setJobs(sortJobs(jobsRes.jobs))
        setSkillsData(skillsRes)
        setLoadErr('')
      })
      .catch((e: any) => setLoadErr(e?.status === 403 ? '此功能尚未開放給你的帳號' : e?.message || '載入失敗'))
      .finally(() => setLoading(false))
  }, [])
  useEffect(() => { load() }, [load])

  // 切職業／改測試等級都會影響「目前職業的技能清單、可升條件、技能點總數」，兩者都要在拿到新
  // /rpg/me 後重新打一次 /rpg/skills，否則畫面會停在舊職業的技能表（見契約 §1/§2）。
  const refreshSkills = useCallback(async () => {
    const sk = await withUserAuth((t) => rpgApi.skills(t))
    setSkillsData(sk)
  }, [])

  async function selectJob(jobId: string | null) {
    if (busyJob) return
    setBusyJob(true)
    try {
      const me = await withUserAuth((t) => rpgApi.setJob(t, jobId))
      setData(me)
      await refreshSkills()
    } catch (e: any) {
      showErr(e?.message || '切換職業失敗，請稍後再試')
    } finally {
      setBusyJob(false)
    }
  }

  async function applyTestLevel() {
    const n = parseInt(testLevelInput, 10)
    if (isNaN(n) || n < 1 || n > 99) { showErr('測試等級需為 1–99 的整數'); return }
    setBusyLevel(true)
    try {
      const me = await withUserAuth((t) => rpgApi.setTestLevel(t, n))
      setData(me)
      await refreshSkills()
      showFlash(`已套用測試等級 ${n}`)
    } catch (e: any) {
      showErr(friendlyErr(e, TEST_LEVEL_ERR_LABEL, '設定測試等級失敗'))
    } finally {
      setBusyLevel(false)
    }
  }

  async function useRealLevel() {
    setBusyLevel(true)
    try {
      const me = await withUserAuth((t) => rpgApi.setTestLevel(t, null))
      setData(me)
      setTestLevelInput('')
      await refreshSkills()
    } catch (e: any) {
      showErr(friendlyErr(e, TEST_LEVEL_ERR_LABEL, '清除測試等級失敗'))
    } finally {
      setBusyLevel(false)
    }
  }

  async function allocate(stat: RpgStatKey, mode: number | 'max') {
    if (!getUserToken() || busyStat) return
    setBusyStat(stat)
    try {
      const body = mode === 'max' ? { stat, mode: 'max' as const } : { stat, points: mode }
      const r = await withUserAuth((t) => rpgApi.allocate(t, body))
      setData(r)
      const label = STAT_META.find((s) => s.key === stat)?.label ?? stat
      showFlash(mode === 'max' ? `${label} 已加到上限` : `${label} +${mode}`)
    } catch (e: any) {
      showErr(friendlyErr(e, STAT_ERR_LABEL, '配點失敗，請稍後再試'))
    } finally {
      setBusyStat(null)
    }
  }

  async function resetStats() {
    if (busyResetStats) return
    if (!window.confirm('確定要把六項素質全部還原為初始值嗎？已花費的點數會全數退回，此動作無法復原。')) return
    setBusyResetStats(true)
    try {
      const r = await withUserAuth((t) => rpgApi.resetStats(t))
      setData(r)
      showFlash('已還原素質為預設值')
    } catch (e: any) {
      showErr(e?.message || '還原失敗，請稍後再試')
    } finally {
      setBusyResetStats(false)
    }
  }

  async function allocateSkill(skillId: string, delta: 1 | -1 | 'max') {
    if (busySkill) return
    setBusySkill(skillId)
    try {
      const r = await withUserAuth((t) => rpgApi.allocateSkill(t, { skill_id: skillId, delta }))
      setSkillsData(r)
    } catch (e: any) {
      showErr(friendlyErr(e, SKILL_ERR_LABEL, '技能配點失敗，請稍後再試'))
    } finally {
      setBusySkill(null)
    }
  }

  async function resetSkills() {
    if (busyResetSkills) return
    if (!window.confirm('確定要重置目前職業的所有技能點數嗎？已配置的技能等級會全部歸零，此動作無法復原。')) return
    setBusyResetSkills(true)
    try {
      const r = await withUserAuth((t) => rpgApi.resetSkills(t))
      setSkillsData(r)
      showFlash('已重置技能')
    } catch (e: any) {
      showErr(e?.message || '重置失敗，請稍後再試')
    } finally {
      setBusyResetSkills(false)
    }
  }

  const ch = data?.character

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>🎮 角色</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>職業／配點／技能（測試階段）</div>
        {onOpenBattle && (
          <div style={{ marginTop: 10 }}>
            <button onClick={onOpenBattle} style={battleBtn}>⚔️ 進入戰鬥（預覽）</button>
          </div>
        )}
      </header>

      <div style={{ flex: 1, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '14px 18px 28px' }}>
        {loading && <Hint>載入中…</Hint>}
        {!loading && (!data || !data.enabled || !ch) && <Hint>{loadErr || '此功能尚未開放給你的帳號。'}</Hint>}

        {!loading && ch && (
          <>
            {actionErr && <div style={errBanner}>{actionErr}</div>}
            {flash && <div style={okBanner}>✓ {flash}</div>}

            {/* Base Lv / 測試等級 / 有效等級 */}
            <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', fontSize: 13.5, fontWeight: 800, color: 'var(--fug)', marginBottom: 12 }}>
              <span>Base Lv. {ch.base_level}</span>
              {ch.test_level != null && <span style={{ color: 'var(--gold)' }}>測試 Lv. {ch.test_level}</span>}
              <span style={{ color: 'var(--tx)' }}>有效 Lv. {ch.effective_level}</span>
            </div>

            {/* ---- 職業 ---- */}
            <JobSection jobs={jobs} currentId={ch.job?.id ?? null} busy={busyJob} onSelect={selectJob} />

            {/* ---- 測試等級 ---- */}
            <div style={{ marginTop: 22 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
                <h2 style={sectionTitle}>測試等級</h2>
                <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>測試用，不影響真實資料</span>
              </div>
              <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <input
                  type="number" min={1} max={99} inputMode="numeric"
                  value={testLevelInput}
                  onChange={(e) => setTestLevelInput(e.target.value)}
                  placeholder={String(ch.test_level ?? ch.base_level)}
                  style={numInput}
                />
                <button onClick={applyTestLevel} disabled={busyLevel} style={ghostActionBtn(busyLevel)}>套用</button>
                <button onClick={useRealLevel} disabled={busyLevel || ch.test_level == null} style={ghostActionBtn(busyLevel || ch.test_level == null)}>使用真實等級</button>
              </div>
            </div>

            {/* HP/MP（上限值；本階段無戰鬥消耗機制，顯示滿條） */}
            <div style={{ marginTop: 18 }}>
              <StatBar label="生命值 HP" value={ch.max_hp} color="#f4623a" />
              <StatBar label="魔力值 MP" value={ch.max_mp} color="#3a8ff4" />
            </div>

            {/* ---- 素質配點 ---- */}
            <div style={{ marginTop: 20, display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', flexWrap: 'wrap', gap: 6 }}>
              <h2 style={sectionTitle}>基本素質</h2>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
                <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--gold)' }}>可配點數 {ch.stat_points_free}／{ch.stat_points_total}</span>
                <button onClick={resetStats} disabled={busyResetStats} style={dangerGhostBtn}>還原預設</button>
              </div>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
              {STAT_META.map((m) => {
                const val = ch.stats[m.key] ?? 0
                const cost = ch.next_cost[m.key]
                const atCap = val >= ch.stat_cap
                const atMax = cost == null || atCap
                const canAdd = !atMax && cost! <= ch.stat_points_free
                const busy = busyStat === m.key
                return (
                  <div key={m.key} style={statRow}>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
                        <span style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>{m.label}</span>
                        <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{m.abbr}</span>
                      </div>
                      <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginTop: 2, lineHeight: 1.4 }}>{m.short}</div>
                    </div>
                    <div style={{ flexShrink: 0, textAlign: 'center', width: 56 }}>
                      <div style={{ fontSize: 20, fontWeight: 900, color: 'var(--fug)', fontVariantNumeric: 'tabular-nums' }}>{val}</div>
                      <div style={{ fontSize: 9.5, color: 'var(--tx-faint)', fontVariantNumeric: 'tabular-nums' }}>／{ch.stat_cap} 上限</div>
                    </div>
                    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 4, flexShrink: 0 }}>
                      <span style={{ fontSize: 10, color: 'var(--tx-faint)', whiteSpace: 'nowrap' }}>{atMax ? '已達上限' : `下一點 ${cost}`}</span>
                      <div style={{ display: 'flex', gap: 4 }}>
                        <button disabled={!canAdd || busy} onClick={() => allocate(m.key, 1)} style={{ ...plusBtn, opacity: canAdd && !busy ? 1 : 0.4, cursor: canAdd && !busy ? 'pointer' : 'default' }}>+1</button>
                        <button disabled={!canAdd || busy} onClick={() => allocate(m.key, 'max')} style={{ ...plusBtn, opacity: canAdd && !busy ? 1 : 0.4, cursor: canAdd && !busy ? 'pointer' : 'default' }}>Max</button>
                      </div>
                    </div>
                  </div>
                )
              })}
            </div>

            {/* 衍生數值 */}
            <h2 style={{ ...sectionTitle, margin: '22px 0 8px' }}>衍生數值</h2>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
              {DERIVED_META.map((d) => {
                const raw = ch.derived[d.key] as number

                // P3（AGI 攻速／DEX 詠唱縮減，使用者當面要求）：aspd 原始評級數字（如「150.6」）
                // 對玩家沒有意義，換算成看得懂的「攻擊間隔 X 秒」；換算係數見 rpgMeta.ts
                // BATTLE_DISPLAY_DEFAULTS 上方註解（/rpg/me 目前不回傳戰鬥 config，這裡是
                // 預設值下的估算，非精算）。
                if (d.key === 'aspd') {
                  const cooldownMs = estimateAttackCooldownMs(raw)
                  return (
                    <div key={d.key} style={derivedCell}>
                      <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>
                        {d.label} <span style={{ color: 'var(--tx-faint)' }}>{d.abbr} {fmtNum(raw)}</span>
                      </div>
                      <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
                        攻擊間隔 {(cooldownMs / 1000).toFixed(2)} 秒
                      </div>
                      <div style={{ fontSize: 9.5, color: 'var(--tx-faint)', marginTop: 2, lineHeight: 1.3 }}>
                        AGI/DEX 越高攻擊越快；估算值，若後台調過戰鬥參數以實際對戰為準
                      </div>
                    </div>
                  )
                }

                // 同理：「詠唱縮減 x%」旁邊補上實際效果說明——以戰鬥預設施放時間為例算出縮短後的
                // 毫秒數，比單看百分比更有感。
                if (d.key === 'cast_reduction_pct') {
                  const exampleMs = estimateCastMs(BATTLE_DISPLAY_DEFAULTS.defaultCastMs, raw)
                  return (
                    <div key={d.key} style={derivedCell}>
                      <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>{d.label} <span style={{ color: 'var(--tx-faint)' }}>{d.abbr}</span></div>
                      <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
                        {fmtNum(raw)}{d.suffix ?? ''}
                      </div>
                      <div style={{ fontSize: 9.5, color: 'var(--tx-faint)', marginTop: 2, lineHeight: 1.3 }}>
                        技能施放更快；以 {BATTLE_DISPLAY_DEFAULTS.defaultCastMs}ms 的技能為例約縮短至 {exampleMs}ms（估算值）
                      </div>
                    </div>
                  )
                }

                return (
                  <div key={d.key} style={derivedCell}>
                    <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>{d.label} <span style={{ color: 'var(--tx-faint)' }}>{d.abbr}</span></div>
                    <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
                      {fmtNum(raw)}{d.suffix ?? ''}
                    </div>
                  </div>
                )
              })}
            </div>

            {!!ch.derived.resists && Object.keys(ch.derived.resists).length > 0 && (
              <div style={{ marginTop: 12 }}>
                <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginBottom: 4 }}>狀態抗性</div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 12px' }}>
                  {Object.entries(ch.derived.resists).map(([k, v]) => (
                    <span key={k} style={{ fontSize: 11.5 }}>
                      <b style={{ color: 'var(--tx)' }}>{resistLabel(k)}</b>
                      <span style={{ color: 'var(--tx-dim)', marginLeft: 4 }}>{fmtNum(v)}%</span>
                    </span>
                  ))}
                </div>
              </div>
            )}

            {/* ---- 技能 ---- */}
            <div style={{ marginTop: 24 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', flexWrap: 'wrap', gap: 6 }}>
                <h2 style={sectionTitle}>技能</h2>
                {skillsData && ch.job && (
                  <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
                    <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--gold)' }}>技能點 {skillsData.skill_points_free}／{skillsData.skill_points_total}</span>
                    <button onClick={resetSkills} disabled={busyResetSkills} style={dangerGhostBtn}>全部重置</button>
                  </div>
                )}
              </div>

              {!ch.job && <Hint>先選擇職業</Hint>}

              {ch.job && skillsData && (
                <SkillPaths job={ch.job} skills={skillsData.skills} busySkill={busySkill} onDelta={allocateSkill} />
              )}
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// ============================== 子元件 ==============================

function JobSection({ jobs, currentId, busy, onSelect }: { jobs: JobDTO[]; currentId: string | null; busy: boolean; onSelect: (id: string | null) => void }) {
  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' }}>
        <h2 style={sectionTitle}>職業</h2>
        <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>測試階段可隨時切換</span>
      </div>
      {jobs.length === 0 ? (
        <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 6 }}>職業清單載入中…</div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginTop: 8 }}>
          {jobs.map((j) => {
            const active = j.id === currentId
            return (
              <button
                key={j.id}
                disabled={busy}
                onClick={() => onSelect(j.id)}
                style={{ ...jobCard, ...(active ? jobCardActive : {}), opacity: busy ? 0.6 : 1, cursor: busy ? 'default' : 'pointer' }}
              >
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 5 }}>
                  <span style={{ fontSize: 15 }}>{jobEmoji(j.id)}</span>
                  <span style={{ fontSize: 13, fontWeight: 800, color: active ? '#fff' : 'var(--tx)' }}>{j.name}</span>
                </div>
                <div style={{ fontSize: 10, color: active ? 'rgba(255,255,255,.9)' : 'var(--tx-dim)', lineHeight: 1.35, marginTop: 2 }}>{j.tagline}</div>
              </button>
            )
          })}
        </div>
      )}
      {currentId && (
        <button onClick={() => onSelect(null)} disabled={busy} style={{ ...linkGhostBtn, marginTop: 8 }}>清除職業選擇（回到無職業狀態）</button>
      )}
    </div>
  )
}

// 依路線（a/b）分兩欄呈現，每欄依 tier 排序（契約 §5：每職業 10 個＝路線 A 5 個＋路線 B 5 個）。
function SkillPaths({ job, skills, busySkill, onDelta }: { job: JobDTO; skills: SkillDTO[]; busySkill: string | null; onDelta: (id: string, d: 1 | -1 | 'max') => void }) {
  const paths: Array<{ key: 'a' | 'b'; name: string; desc: string }> = [
    { key: 'a', name: job.path_a.name, desc: job.path_a.desc },
    { key: 'b', name: job.path_b.name, desc: job.path_b.desc },
  ]
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18, marginTop: 10 }}>
      {paths.map((p) => {
        const list = skills.filter((s) => s.path === p.key).sort((a, b) => a.tier - b.tier)
        return (
          <div key={p.key}>
            <div style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{p.name}</div>
            {p.desc && <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginTop: 1, marginBottom: 8, lineHeight: 1.4 }}>{p.desc}</div>}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {list.length === 0 ? (
                <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>（此路線暫無技能資料）</div>
              ) : (
                list.map((sk) => (
                  <SkillRow key={sk.id} skill={sk} allSkills={skills} busy={busySkill === sk.id} onDelta={(d) => onDelta(sk.id, d)} />
                ))
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function SkillRow({ skill, allSkills, busy, onDelta }: { skill: SkillDTO; allSkills: SkillDTO[]; busy: boolean; onDelta: (d: 1 | -1 | 'max') => void }) {
  const prereq = skill.prereq_skill_id ? allSkills.find((s) => s.id === skill.prereq_skill_id) : null
  const canMinus = skill.level > 0 && !busy
  const canPlus = skill.can_level_up && !busy
  const kindLabel = SKILL_KIND_LABEL[skill.kind] ?? skill.kind

  return (
    <div style={skillRowStyle}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, minWidth: 0 }}>
          <span style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{skill.name}</span>
          <span style={{ fontSize: 11, color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums' }}>Lv {skill.level}/{skill.max_level}</span>
        </div>
        <div style={{ display: 'flex', gap: 4, alignItems: 'center', flexShrink: 0 }}>
          <span style={kindTagStyle}>{kindLabel}</span>
          {!skill.implemented && <span style={unimplTagStyle}>未實裝</span>}
        </div>
      </div>

      <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.5 }}>{skill.display_text}</div>

      <EffectPreview skill={skill} />

      <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 4 }}>
        MP {skill.mp_cost}・冷卻 {(skill.cooldown_ms / 1000).toFixed(1)}s・詠唱 {skill.cast_ms}ms
      </div>

      {prereq && (
        <div style={{ fontSize: 10, color: skill.prereq_ok ? 'var(--fug)' : 'var(--tx-faint)', marginTop: 4 }}>
          前置：「{prereq.name}」達 Lv {skill.prereq_level}{skill.prereq_ok ? '（已達成）' : `（目前 Lv ${prereq.level}）`}
        </div>
      )}

      <div style={{ display: 'flex', gap: 4, marginTop: 8 }}>
        <button disabled={!canMinus} onClick={() => onDelta(-1)} style={smallBtn(canMinus)}>−</button>
        <button disabled={!canPlus} onClick={() => onDelta(1)} style={smallBtn(canPlus)}>+</button>
        <button disabled={!canPlus} onClick={() => onDelta('max')} style={smallBtn(canPlus)}>Max</button>
      </div>
    </div>
  )
}

// 效果數字：level=0 時 effect_at_level 已是「Lv1 預覽」（見 WIRE），故 0 級不必另外重複顯示
// 「下一級」（那會跟 Lv1 預覽完全一樣）；level≥1 才顯示 effect_next_level（已達上限時為 null）。
function EffectPreview({ skill }: { skill: SkillDTO }) {
  const cur: EffectAtLevel = skill.effect_at_level
  return (
    <>
      <div style={{ fontSize: 10.5, color: skill.implemented ? 'var(--tx-faint)' : 'var(--tx-faint)', opacity: skill.implemented ? 1 : 0.7, marginTop: 2 }}>
        {skill.level === 0 ? 'Lv1 效果預覽：' : `Lv${skill.level} 效果：`}{formatEffectAtLevel(cur)}
      </div>
      {skill.level > 0 && skill.effect_next_level && (
        <div style={{ fontSize: 10.5, color: 'var(--fug)', marginTop: 2 }}>
          Lv{skill.level + 1} 預覽：{formatEffectAtLevel(skill.effect_next_level)}
        </div>
      )}
    </>
  )
}

function StatBar({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div style={{ marginTop: 8 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, fontWeight: 700, color: 'var(--tx-dim)' }}>
        <span>{label}</span>
        <span style={{ color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>{value}</span>
      </div>
      <div style={{ height: 9, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 3 }}>
        <div style={{ height: '100%', width: '100%', background: color, borderRadius: 999 }} />
      </div>
    </div>
  )
}

function Hint({ children }: { children: React.ReactNode }) {
  return <div style={{ textAlign: 'center', color: 'var(--tx-dim)', fontSize: 13, padding: '18px 0' }}>{children}</div>
}

function fmtNum(n: number): string {
  if (n == null || isNaN(n)) return '0'
  return Number.isInteger(n) ? String(n) : n.toFixed(1)
}

// 金底白字（全站通則）
const battleBtn: React.CSSProperties = { background: 'var(--gold)', color: '#fff', border: 'none', borderRadius: 10, padding: '9px 14px', fontSize: 13.5, fontWeight: 800, fontFamily: 'inherit', cursor: 'pointer' }
const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 13, padding: 0, cursor: 'pointer', fontFamily: 'inherit' }
const sectionTitle: React.CSSProperties = { margin: 0, fontSize: 15, fontWeight: 800, color: 'var(--tx)' }
const statRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }
const plusBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 7, padding: '4px 9px', fontSize: 11.5, fontWeight: 800, fontFamily: 'inherit' }
const derivedCell: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 10, padding: '8px 10px' }
const errBanner: React.CSSProperties = { background: 'rgba(244,98,58,.12)', color: '#f4623a', border: '1px solid rgba(244,98,58,.3)', borderRadius: 10, padding: '8px 12px', fontSize: 12.5, marginBottom: 10 }
const okBanner: React.CSSProperties = { background: 'rgba(45,212,150,.12)', color: 'var(--fug)', border: '1px solid rgba(45,212,150,.3)', borderRadius: 10, padding: '8px 12px', fontSize: 12.5, marginBottom: 10 }

// ---- P5 新增樣式 ----
const jobCard: React.CSSProperties = { display: 'block', textAlign: 'left', background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '9px 10px', fontFamily: 'inherit' }
// 金底白字（全站通則）：選中的職業卡用金底，文字強制白色。
const jobCardActive: React.CSSProperties = { background: 'var(--gold)', borderColor: 'var(--gold)' }
const numInput: React.CSSProperties = { width: 76, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 8, padding: '7px 10px', fontSize: 13, color: 'var(--tx)', fontFamily: 'inherit' }
function ghostActionBtn(disabled: boolean): React.CSSProperties {
  return { background: 'var(--bg-1)', border: '1px solid var(--line)', color: disabled ? 'var(--tx-faint)' : 'var(--tx)', borderRadius: 8, padding: '7px 12px', fontSize: 12.5, fontWeight: 700, fontFamily: 'inherit', cursor: disabled ? 'default' : 'pointer' }
}
const dangerGhostBtn: React.CSSProperties = { background: 'none', border: '1px solid rgba(244,98,58,.4)', color: '#f4623a', borderRadius: 8, padding: '4px 10px', fontSize: 11, fontWeight: 700, fontFamily: 'inherit', cursor: 'pointer' }
const linkGhostBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 11, textDecoration: 'underline', padding: 0, cursor: 'pointer', fontFamily: 'inherit' }
const skillRowStyle: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }
const kindTagStyle: React.CSSProperties = { fontSize: 9.5, fontWeight: 700, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 999, padding: '2px 7px', whiteSpace: 'nowrap' }
const unimplTagStyle: React.CSSProperties = { fontSize: 9.5, fontWeight: 700, color: '#f4623a', background: 'rgba(244,98,58,.14)', borderRadius: 999, padding: '2px 7px', whiteSpace: 'nowrap' }
function smallBtn(enabled: boolean): React.CSSProperties {
  return { ...plusBtn, minWidth: 30, opacity: enabled ? 1 : 0.35, cursor: enabled ? 'pointer' : 'default' }
}
