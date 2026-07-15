'use client'

import { useEffect, useMemo, useRef, useState } from 'react'
import useSWR from 'swr'
import { trainingApi, type WorkoutTemplate, type PaceLevel, type TrainingCalendar, type TrainingDay } from '@/lib/api'
import { resolveTemplate, saveFreetrainWorkout, totalKm, estMinutes, fmtDuration, segSummary, targetPaceBand } from '@/lib/workout'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import UpgradeVipModal from './UpgradeVipModal'

// 自主訓練（VIP 專屬）：
// P1「📚 課表庫」——依分類列出，選配速等級後即時解析出總距離/預估時間；「開始訓練」把解析後的分段
// 橋接給 /track（sessionStorage，見 lib/workout.ts saveFreetrainWorkout）→ 帶到 GPS 追蹤跑。
// P2「🗓️ 訓練月曆」——比照成就月曆的月曆殼（換月/滑動/格子），每日可排定一份課表（upsert，
// user_training_schedule PK=user+date）；格內顯示 category 色徽章 + 是否已有實跑；點日期開「選課表」
// modal 排定/更換/取消，已排的日可直接「開始此課表」。都不是挑戰制：跑步照常走 GPS 上傳自動發里程 EXP。
const CATEGORY_LABELS: Record<string, string> = {
  recovery: '恢復', easy: '輕鬆', lsd: '長距離 LSD', tempo: '節奏', threshold: '閾值',
  progression: '漸速', interval: '間歇', fartlek: '法特雷克', pyramid: '金字塔',
  norwegian: '挪威 4×4', yasso: '亞索 800', rep: '重複跑',
}
// 月曆日格徽章的短標籤（空間小，全名裝不下)
const CATEGORY_SHORT: Record<string, string> = {
  recovery: '恢復', easy: '輕鬆', lsd: 'LSD', tempo: '節奏', threshold: '閾值',
  progression: '漸速', interval: '間歇', fartlek: '法特', pyramid: '金字',
  norwegian: '北歐', yasso: '亞索', rep: '重複',
}
// 月曆日格徽章顏色（依課表強度分桶：恢復/輕鬆/長距離＝綠、節奏/閾值/漸速＝金、間歇系＝紅、亞索/挪威＝紫）
const CATEGORY_COLOR: Record<string, { bg: string; fg: string }> = {
  recovery: { bg: 'rgba(45,229,154,.28)', fg: '#0b3324' },
  easy: { bg: 'rgba(45,229,154,.30)', fg: '#0b3324' },
  lsd: { bg: 'rgba(45,229,154,.22)', fg: '#0b3324' },
  tempo: { bg: 'rgba(255,194,75,.32)', fg: '#3a2705' },
  threshold: { bg: 'rgba(255,159,67,.32)', fg: '#3a1f05' },
  progression: { bg: 'rgba(255,194,75,.32)', fg: '#3a2705' },
  interval: { bg: 'rgba(255,107,107,.32)', fg: '#3a0a0a' },
  fartlek: { bg: 'rgba(255,107,107,.28)', fg: '#3a0a0a' },
  pyramid: { bg: 'rgba(255,107,107,.28)', fg: '#3a0a0a' },
  norwegian: { bg: 'rgba(199,88,255,.30)', fg: '#2a0a3a' },
  yasso: { bg: 'rgba(199,88,255,.30)', fg: '#2a0a3a' },
  rep: { bg: 'rgba(199,88,255,.26)', fg: '#2a0a3a' },
}
function catColor(cat: string) { return CATEGORY_COLOR[cat] || { bg: 'var(--bg-2)', fg: 'var(--tx-dim)' } }

const WK = ['日', '一', '二', '三', '四', '五', '六']
function ym(d: Date) { return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}` }
function shiftMonth(key: string, delta: number) { const [y, m] = key.split('-').map(Number); return ym(new Date(y, m - 1 + delta, 1)) }
function pad2(n: number) { return String(n).padStart(2, '0') }
function ymd(d: Date) { return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}` }

export default function TrainingScreen({ onBack }: { onBack: () => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data, error } = useSWR(
    uid && getUserToken() ? ['training-templates', uid] : null,
    () => withUserAuth((t) => trainingApi.templates(t)),
  )
  const vipLocked = !!error && error?.status === 403 && error?.message === 'vip_only'
  const loadFailed = !!error && !vipLocked
  const unlocked = !!user && !vipLocked && !loadFailed && !!data

  const [tab, setTab] = useState<'library' | 'calendar'>('library')
  const [levelId, setLevelId] = useState<number | null>(null)
  // 記住上次選的配速等級（切頁/重整後維持不變，不再每次回到預設）
  useEffect(() => { const v = window.localStorage.getItem('dor_training_pace_level'); if (v) setLevelId(Number(v)) }, [])
  const [navigating, setNavigating] = useState(false)
  const [showUpgrade, setShowUpgrade] = useState(false)

  const levels = useMemo(() => data?.pace_levels ?? [], [data])
  const level: PaceLevel | null = useMemo(() => {
    if (!levels.length) return null
    const byId = levelId != null ? levels.find((l) => l.id === levelId) : undefined
    return byId ?? levels.find((l) => l.id === 5) ?? levels[Math.floor(levels.length / 2)]
  }, [levels, levelId])

  // 依 category 分組，保留課表庫原本的 sort_order（課表庫分頁 + 月曆選課表 modal 共用）
  const groups = useMemo(() => {
    const map = new Map<string, WorkoutTemplate[]>()
    for (const t of data?.templates ?? []) {
      const arr = map.get(t.category)
      if (arr) arr.push(t)
      else map.set(t.category, [t])
    }
    return Array.from(map.entries())
  }, [data])

  // 依 template_code + 指定配速等級解析並橋接給 /track（課表庫「開始訓練」與月曆「開始此課表」共用）
  function startWorkout(code: string, useLevel: PaceLevel | null) {
    const t = (data?.templates ?? []).find((x) => x.code === code)
    if (!t || !useLevel) return
    const segments = resolveTemplate(t.segments, useLevel)
    saveFreetrainWorkout(t.code, t.name, segments)
    setNavigating(true)
    setTimeout(() => { window.location.href = '/track' }, 380)
  }
  function startTemplate(t: WorkoutTemplate) { startWorkout(t.code, level) }

  // ── 訓練月曆（P2）──
  const [month, setMonth] = useState(() => ym(new Date()))
  const [cal, setCal] = useState<TrainingCalendar | null>(null)
  const [calErr, setCalErr] = useState(false)
  function loadCalendar(m: string) {
    if (!getUserToken()) return
    withUserAuth((t) => trainingApi.calendar(t, m)).then((c) => { setCal(c); setCalErr(false) }).catch(() => setCalErr(true))
  }
  useEffect(() => {
    if (tab !== 'calendar' || !unlocked) return
    setCal(null)
    loadCalendar(month)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [month, tab, unlocked])

  const touchX = useRef<number | null>(null)
  function go(delta: number) { setMonth((m) => shiftMonth(m, delta)) }
  function onTouchStart(e: React.TouchEvent) { touchX.current = e.touches[0].clientX }
  function onTouchEnd(e: React.TouchEvent) {
    if (touchX.current == null) return
    const dx = e.changedTouches[0].clientX - touchX.current
    touchX.current = null
    if (dx > 45) go(-1)        // 右滑 → 上個月
    else if (dx < -45) go(1)   // 左滑 → 下個月（未來月不鎖，可排課）
  }

  // 選課表 modal（點某日開啟；null=關閉）
  const [pickerDate, setPickerDate] = useState<string | null>(null)
  const [pickerLevelId, setPickerLevelId] = useState<number | null>(null)
  const [pickerBusy, setPickerBusy] = useState(false)
  const [pickerErr, setPickerErr] = useState('')
  const pickerDay: TrainingDay | undefined = useMemo(() => cal?.days.find((d) => d.date === pickerDate), [cal, pickerDate])
  const pickerLevel: PaceLevel | null = useMemo(() => levels.find((l) => l.id === pickerLevelId) ?? level, [levels, pickerLevelId, level])

  function openPicker(date: string) {
    const existing = cal?.days.find((d) => d.date === date)?.scheduled ?? null
    setPickerDate(date)
    setPickerLevelId(existing?.pace_level ?? levelId ?? level?.id ?? null)
    setPickerErr('')
  }
  function closePicker() { setPickerDate(null); setPickerErr('') }

  async function saveSchedule(t: WorkoutTemplate) {
    if (!pickerDate || !pickerLevel) return
    const resolved = resolveTemplate(t.segments, pickerLevel)
    const token = getUserToken()
    if (!token) return
    setPickerBusy(true); setPickerErr('')
    try {
      await withUserAuth((tok) => trainingApi.schedule(tok, {
        date: pickerDate, template_code: t.code, pace_level: pickerLevel.id,
        planned_km: totalKm(resolved), planned_min: estMinutes(resolved),
      }))
      closePicker()
      loadCalendar(month)
    } catch {
      setPickerErr('排定失敗，請稍後再試')
    } finally {
      setPickerBusy(false)
    }
  }

  async function removeSchedule() {
    if (!pickerDate) return
    const token = getUserToken()
    if (!token) return
    setPickerBusy(true); setPickerErr('')
    try {
      await withUserAuth((tok) => trainingApi.unschedule(tok, pickerDate))
      closePicker()
      loadCalendar(month)
    } catch {
      setPickerErr('刪除失敗，請稍後再試')
    } finally {
      setPickerBusy(false)
    }
  }

  // 月曆格
  const [yy, mm] = month.split('-').map(Number)
  const first = new Date(yy, mm - 1, 1).getDay()
  const daysIn = new Date(yy, mm, 0).getDate()
  const cells: (number | null)[] = [...Array(first).fill(null), ...Array.from({ length: daysIn }, (_, i) => i + 1)]
  const dayMap: Record<string, TrainingDay> = {}
  ;(cal?.days ?? []).forEach((d) => { dayMap[d.date] = d })
  const todayStr = ymd(new Date())

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🏃 自主訓練</span>
      </header>

      {unlocked && (
        <div style={{ display: 'flex', gap: 4, padding: '0 18px', borderBottom: '1px solid var(--line)', flexShrink: 0 }}>
          {([['library', '📚 課表庫'], ['calendar', '🗓️ 訓練月曆']] as const).map(([v, label]) => (
            <button key={v} onClick={() => setTab(v)} style={{
              padding: '10px 10px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 13.5, whiteSpace: 'nowrap',
              color: tab === v ? 'var(--tx)' : 'var(--tx-dim)', fontWeight: tab === v ? 800 : 500,
              borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent', fontFamily: 'inherit',
            }}>{label}</button>
          ))}
        </div>
      )}

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '6px 18px 28px' }}>
        {!user ? (
          <div style={emptyBox}>請先登入以使用自主訓練</div>
        ) : vipLocked ? (
          <div style={emptyBox}>
            <div style={{ fontSize: 32 }}>🔒</div>
            <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', marginTop: 8 }}>VIP 專屬功能</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.7 }}>
              自主訓練提供完整課表庫（恢復／輕鬆／節奏／閾值／間歇…）與訓練月曆排程，<br />升級 VIP 即可解鎖，依你的能力自訂訓練。
            </div>
            <button onClick={() => setShowUpgrade(true)} style={{ marginTop: 14, background: 'var(--gold)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 10, padding: '10px 22px', fontSize: 13.5, cursor: 'pointer' }}>✦ 升級 VIP</button>
          </div>
        ) : loadFailed ? (
          <div style={emptyBox}>課表庫載入失敗，請稍後再試</div>
        ) : !data ? (
          <div style={emptyBox}>載入中…</div>
        ) : tab === 'library' ? (
          <>
            <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '4px 2px 12px', lineHeight: 1.7 }}>
              選擇配速等級，課表庫即自動換算成你的實際配速。挑一份「開始訓練」帶到 GPS 追蹤跑——完成即照常記錄跑步、累計里程 EXP。
            </p>

            {/* 配速等級選擇 */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 14px', marginBottom: 16 }}>
              <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)', flexShrink: 0 }}>配速等級</span>
              <select
                value={level?.id ?? ''}
                onChange={(e) => { const id = Number(e.target.value); setLevelId(id); window.localStorage.setItem('dor_training_pace_level', String(id)) }}
                style={levelSelect}
              >
                {levels.map((l) => <option key={l.id} value={l.id}>Lv.{l.id} · {l.label}</option>)}
              </select>
            </div>

            {groups.length === 0 && <div style={emptyBox}>目前尚無課表庫內容</div>}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
              {groups.map(([cat, templates]) => (
                <div key={cat}>
                  <div style={{ fontSize: 12, fontWeight: 800, color: 'var(--fug)', marginBottom: 8, letterSpacing: '.05em' }}>{CATEGORY_LABELS[cat] || cat}</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    {templates.map((t) => {
                      const resolved = level ? resolveTemplate(t.segments, level) : []
                      return (
                        <div key={t.code} style={tplCard}>
                          <div style={{ fontSize: 14.5, fontWeight: 800, color: 'var(--tx)' }}>{t.name}</div>
                          {t.description && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.6 }}>{t.description}</div>}
                          {segSummary(resolved) && <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.6, padding: '7px 10px', background: 'var(--bg-2)', borderRadius: 8 }}>📋 {segSummary(resolved)}</div>}
                          {targetPaceBand(resolved) && <div style={{ fontSize: 12, color: 'var(--fug)', fontWeight: 700, marginTop: 6, fontVariantNumeric: 'tabular-nums' }}>🎯 目標配速 {targetPaceBand(resolved)}</div>}
                          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 5, fontVariantNumeric: 'tabular-nums' }}>總距離 {totalKm(resolved)} K · 預估 {fmtDuration(estMinutes(resolved))}</div>
                          <button onClick={() => startTemplate(t)} disabled={!level} style={{ ...startBtn, opacity: level ? 1 : 0.5 }}>▶ 開始訓練</button>
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          </>
        ) : (
          <>
            {/* 本月總覽 */}
            <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 14px', marginBottom: 14, fontSize: 12, color: 'var(--tx-dim)', lineHeight: 1.9 }}>
              {calErr ? '本月資料載入失敗，請稍後再試' : !cal ? '載入中…' : (
                <>
                  本月 預計 <b style={{ color: 'var(--tx)' }}>{cal.planned.days}</b> 天 · <b style={{ color: 'var(--tx)' }}>{cal.planned.km.toFixed(1)}</b> K · <b style={{ color: 'var(--tx)' }}>{cal.planned.min}</b> 分
                  ／ 實際 <b style={{ color: 'var(--fug)' }}>{cal.actual.days}</b> 天 · <b style={{ color: 'var(--fug)' }}>{cal.actual.km.toFixed(1)}</b> K · <b style={{ color: 'var(--fug)' }}>{cal.actual.min}</b> 分
                </>
              )}
            </div>

            {/* 月曆殼（比照成就月曆：換月/滑動/格子；未來月不鎖，可預先排課） */}
            <div
              onTouchStart={onTouchStart} onTouchEnd={onTouchEnd}
              style={{ background: 'linear-gradient(160deg, var(--bg-1), var(--bg-2))', border: '1px solid var(--line)', borderRadius: 18, padding: '16px 16px 14px' }}
            >
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
                <button onClick={() => go(-1)} style={navBtn}>‹</button>
                <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>{yy} 年 {mm} 月</div>
                <button onClick={() => go(1)} style={navBtn}>›</button>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7,1fr)', gap: 4, marginBottom: 4 }}>
                {WK.map((w) => <div key={w} style={{ textAlign: 'center', fontSize: 10, color: 'var(--tx-faint)' }}>{w}</div>)}
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7,1fr)', gap: 4 }}>
                {cells.map((day, i) => {
                  if (day == null) return <div key={`b${i}`} />
                  const dateStr = `${month}-${pad2(day)}`
                  const info = dayMap[dateStr]
                  const sched = info?.scheduled
                  const isToday = dateStr === todayStr
                  const col = catColor(sched?.category || '')
                  return (
                    <button key={day} onClick={() => openPicker(dateStr)} style={{
                      aspectRatio: '1', borderRadius: 8, background: 'var(--bg-2)',
                      border: isToday ? '1.5px solid var(--fug)' : '1px solid var(--line)',
                      display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 2,
                      padding: 2, cursor: 'pointer', position: 'relative', fontFamily: 'inherit',
                    }}>
                      <span style={{ fontSize: 10, color: 'var(--tx-faint)', fontWeight: 700 }}>{day}</span>
                      {sched && (
                        <span style={{ fontSize: 7.5, fontWeight: 800, padding: '1px 4px', borderRadius: 5, background: col.bg, color: col.fg, maxWidth: '94%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {CATEGORY_SHORT[sched.category] || sched.category}
                        </span>
                      )}
                      {info?.has_activity && <span style={{ position: 'absolute', top: 2, right: 3, fontSize: 9, color: 'var(--fug)', fontWeight: 900 }}>✓</span>}
                    </button>
                  )
                })}
              </div>
            </div>
            <div style={{ textAlign: 'center', fontSize: 10.5, color: 'var(--tx-faint)', margin: '8px 0 4px' }}>左右滑動或按 ‹ › 切換月份 · 點日期排定/更換課表</div>
          </>
        )}
      </div>

      {navigating && (
        <div style={{ position: 'absolute', inset: 0, zIndex: 3400, background: 'var(--bg)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, animation: 'fadeIn .3s ease' }}>
          <div style={{ fontSize: 30 }}>🏃‍♂️</div>
          <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>前往 GPS 跑步追蹤…</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>準備開始自主訓練</div>
        </div>
      )}

      {showUpgrade && <UpgradeVipModal onClose={() => setShowUpgrade(false)} />}

      {/* 選課表 modal：排定/更換某日課表，或開始已排定的課表 */}
      {pickerDate && (
        <div data-skin="default" onClick={closePicker} style={{ position: 'fixed', inset: 0, zIndex: 3600, background: 'rgba(4,8,6,.82)', display: 'flex', alignItems: 'flex-end', justifyContent: 'center' }}>
          <div onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 440, maxHeight: '86dvh', overflowY: 'auto', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--line-2)', borderTopLeftRadius: 18, borderTopRightRadius: 18, boxShadow: '0 -12px 40px rgba(0,0,0,.6)', padding: '16px 18px 22px' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ fontSize: 15, fontWeight: 900, color: '#fff' }}>{pickerDate}</div>
              <button onClick={closePicker} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 20, cursor: 'pointer', lineHeight: 1, padding: 4 }}>×</button>
            </div>

            {pickerDay?.scheduled && (
              <div style={{ background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '11px 13px', margin: '10px 0' }}>
                <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 800 }}>目前排定</div>
                <div style={{ fontSize: 14, fontWeight: 800, color: '#fff', marginTop: 4 }}>{pickerDay.scheduled.name}</div>
                <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 3, fontVariantNumeric: 'tabular-nums' }}>
                  {CATEGORY_LABELS[pickerDay.scheduled.category] || pickerDay.scheduled.category} · {pickerDay.scheduled.planned_km.toFixed(1)} K · {fmtDuration(pickerDay.scheduled.planned_min)}
                </div>
                <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                  <button
                    disabled={pickerBusy}
                    onClick={() => { const code = pickerDay.scheduled!.template_code; const lvl = levels.find((l) => l.id === pickerDay.scheduled!.pace_level) ?? null; closePicker(); startWorkout(code, lvl) }}
                    style={{ flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 0', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }}
                  >▶ 開始此課表</button>
                  <button disabled={pickerBusy} onClick={removeSchedule} style={{ background: 'none', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 9, padding: '9px 14px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }}>🗑 取消排課</button>
                </div>
              </div>
            )}

            <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 800, margin: '12px 2px 8px' }}>{pickerDay?.scheduled ? '更換課表' : '選擇課表'}</div>

            {/* 配速等級（預設沿用上次選的等級／該日原本的等級） */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '9px 13px', marginBottom: 12 }}>
              <span style={{ fontSize: 12, fontWeight: 800, color: '#fff', flexShrink: 0 }}>配速等級</span>
              <select value={pickerLevelId ?? ''} onChange={(e) => setPickerLevelId(Number(e.target.value))} style={levelSelect}>
                {levels.map((l) => <option key={l.id} value={l.id}>Lv.{l.id} · {l.label}</option>)}
              </select>
            </div>

            {pickerErr && <div style={{ fontSize: 12, color: '#ff6b6b', textAlign: 'center', margin: '4px 0 10px' }}>{pickerErr}</div>}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              {groups.length === 0 && <div style={{ ...emptyBox, padding: '16px 4px' }}>目前尚無課表庫內容</div>}
              {groups.map(([cat, templates]) => (
                <div key={cat}>
                  <div style={{ fontSize: 11, fontWeight: 800, color: 'var(--tx-faint)', marginBottom: 6, letterSpacing: '.05em' }}>{CATEGORY_LABELS[cat] || cat}</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {templates.map((t) => {
                      const resolved = pickerLevel ? resolveTemplate(t.segments, pickerLevel) : []
                      const active = pickerDay?.scheduled?.template_code === t.code
                      return (
                        <div key={t.code} style={{ background: active ? 'rgba(45,229,154,.10)' : 'rgba(255,255,255,.03)', border: `1px solid ${active ? 'var(--fug)' : 'var(--line-2)'}`, borderRadius: 11, padding: '9px 12px' }}>
                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
                            <div style={{ fontSize: 13, fontWeight: 800, color: '#fff' }}>{t.name}</div>
                            <button disabled={pickerBusy || !pickerLevel} onClick={() => saveSchedule(t)} style={{ flexShrink: 0, background: active ? 'var(--line-2)' : 'var(--fug)', color: active ? 'var(--tx-dim)' : 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 11.5, fontFamily: 'inherit' }}>{active ? '已排定' : '排定'}</button>
                          </div>
                          <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 4, fontVariantNumeric: 'tabular-nums' }}>總距離 {totalKm(resolved)} K · 預估 {fmtDuration(estMinutes(resolved))}</div>
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const emptyBox: React.CSSProperties = { color: 'var(--tx-dim)', fontSize: 13.5, lineHeight: 1.9, padding: '32px 10px', textAlign: 'center' }
const levelSelect: React.CSSProperties = { flex: 1, minWidth: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, fontFamily: 'inherit' }
const tplCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '11px 13px' }
const startBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 0', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
const navBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--tx)', borderRadius: 10, width: 34, height: 34, fontSize: 20, cursor: 'pointer', flexShrink: 0, lineHeight: 1 }
