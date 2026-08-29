'use client'

import { useEffect, useState } from 'react'
import { strategiesApi, FUEL_KIND_LABEL, type RaceStrategy, type StrategySegment, type FuelPoint, type FuelKind } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import { generateRaceStrategy, autoStrategyName, formatSegmentPreviewLines, formatFuelPreviewLines, DISTANCE_PRESETS, type GenerateOutput } from '@/lib/strategyGenerator'

// 賽事策略（自主訓練第 3 分頁）：配速計劃（分段目標配速）＋補給計劃（時間/距離觸發提醒），
// 開跑時帶 /track?strategy=<id> 進入「比賽專注模式」（由另一位負責 track 頁串接，本檔不動 track）。
// 清單/單筆為唯讀瀏覽（登入即可，比照 TrainingScreen 課表庫/月曆的「可瀏覽不擋版」慣例）；
// 建立/修改/刪除/開始挑戰皆視為 VIP 動作（比照本頁其餘寫入操作的鎖法：入口always可見，
// 點下去非 VIP 一律改跳 UpgradeVipModal，不整面擋板）——後端建立/修改/刪除本就 requireVIP。
// 每帳號最多 5 份（後端把關，超過回 409 {error:"strategy_limit"}）；這裡只做基本前端檢核
// （分段連續遞增、配速落在合理區間），後端仍是最終把關，不可只信前端。

const FUEL_MODE_LABEL: Record<'time' | 'distance', string> = { time: '時間', distance: '距離' }
const FUEL_KIND_OPTIONS = Object.keys(FUEL_KIND_LABEL) as FuelKind[]
// 配速合理區間（基本防呆用，非嚴格賽事規則）：1:00～60:00 / km，涵蓋衝刺到健走全範圍
const PACE_MIN_S = 60
const PACE_MAX_S = 3600

// 秒/km → "m:ss"（配速顯示，四捨五入到整秒再切分，避免秒數進位到 60）
function fmtPace(paceS: number) {
  const t = Math.round(paceS)
  const m = Math.floor(t / 60)
  const s = t % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
// 秒 → 分鐘顯示（補給點時間模式；允許小數，去除多餘的 .0）
function fmtMinutes(sec: number) {
  const m = sec / 60
  return Number.isInteger(m) ? String(m) : (Math.round(m * 10) / 10).toString()
}
// 公尺 → 公里顯示（補給點距離模式；允許小數）
function fmtKm(m: number) {
  const km = m / 1000
  return Number.isInteger(km) ? String(km) : (Math.round(km * 100) / 100).toString()
}

type SegRow = { to_km: string; paceMin: string; paceSec: string }
type FuelRow = { kind: FuelKind; mode: 'time' | 'distance'; val: string }

export default function RaceStrategyTab({ isVip, openUpgrade }: { isVip: boolean; openUpgrade: (reason: string) => void }) {
  // 寫入類操作的共用守門（比照 TrainingScreen 的 vipGate）：VIP 直接執行，非 VIP 一律攔截改跳升級彈窗
  function vipGate(action: () => void) { return () => { if (isVip) action(); else openUpgrade('賽事策略為 VIP 專屬功能。') } }

  const [list, setList] = useState<RaceStrategy[] | null>(null)
  const [listErr, setListErr] = useState(false)
  const [limit, setLimit] = useState(5)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [actionErr, setActionErr] = useState('')

  function loadList() {
    if (!getUserToken()) return
    withUserAuth((t) => strategiesApi.list(t)).then((r) => { setList(r.strategies); setLimit(r.limit); setListErr(false) }).catch(() => setListErr(true))
  }
  useEffect(() => { loadList() }, [])

  // ── 建立/編輯表單（bottom sheet，比照本頁其餘 modal 慣例） ──
  const [showForm, setShowForm] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null) // null=新建
  const [fName, setFName] = useState('')
  const [fSegs, setFSegs] = useState<SegRow[]>([{ to_km: '', paceMin: '', paceSec: '' }])
  const [fFuel, setFFuel] = useState<FuelRow[]>([])
  const [fBusy, setFBusy] = useState(false)
  const [fErr, setFErr] = useState('')
  // 逐欄位驗證失敗標記（送出時填入、該欄 onChange 時清除）：key 格式 'name' / `seg-${i}-to` / `seg-${i}-min` /
  // `seg-${i}-sec` / `fuel-${i}-val`，供輸入框套用紅框樣式，讓使用者一眼看出「哪裡」沒填好（不只是籠統失敗）。
  const [fErrFields, setFErrFields] = useState<Set<string>>(new Set())
  function clearFieldErr(key: string) {
    setFErrFields((prev) => {
      if (!prev.has(key)) return prev
      const next = new Set(prev)
      next.delete(key)
      return next
    })
  }

  // ── 自動建立策略（純函式 generateRaceStrategy，見 lib/strategyGenerator.ts）：
  // 選距離＋目標完賽時間 → 產生建議（配速分段＋補給計畫）→ 使用者確認後一次套入上面的表單 state，
  // 仍可手動微調再走既有 submitForm 儲存流程，不另開一條寫入路徑。
  const [autoOpen, setAutoOpen] = useState(false)
  const [autoDistMode, setAutoDistMode] = useState<'full' | 'half' | 'custom'>('full')
  const [autoCustomKm, setAutoCustomKm] = useState('')
  const [autoGoalH, setAutoGoalH] = useState('')
  const [autoGoalM, setAutoGoalM] = useState('')
  const [autoGoalS, setAutoGoalS] = useState('')
  const [autoErr, setAutoErr] = useState('')
  const [autoResult, setAutoResult] = useState<GenerateOutput | null>(null)
  const [autoParams, setAutoParams] = useState<{ distanceKm: number; targetSeconds: number } | null>(null)
  // 「套用到表單」成功後的一次性提示；關閉表單或重新產生建議時清除
  const [appliedNotice, setAppliedNotice] = useState(false)
  // 每帳號最多 limit 份（見上方 loadList），自動建立區塊達上限時「產生建議」／「套用到表單」都要擋下
  // （不動「+ 建立賽事策略」本身）
  const atAutoLimit = !!list && list.length >= limit

  function resetAuto() {
    setAutoResult(null); setAutoParams(null); setAutoErr(''); setAppliedNotice(false)
  }
  function openAuto() { setAutoOpen((v) => !v) }

  function generateAutoPreview() {
    if (atAutoLimit) return
    setAppliedNotice(false)
    setAutoErr('')
    let distanceKm: number
    if (autoDistMode === 'full') distanceKm = DISTANCE_PRESETS.full
    else if (autoDistMode === 'half') distanceKm = DISTANCE_PRESETS.half
    else {
      const km = Number(autoCustomKm.trim())
      if (!Number.isFinite(km) || km < 3 || km > 100) { setAutoErr('請輸入 3–100 公里的距離'); setAutoResult(null); return }
      distanceKm = km
    }
    const h = autoGoalH.trim() === '' ? 0 : Number(autoGoalH)
    const m = autoGoalM.trim() === '' ? 0 : Number(autoGoalM)
    const s = autoGoalS.trim() === '' ? 0 : Number(autoGoalS)
    if (!Number.isFinite(h) || !Number.isFinite(m) || !Number.isFinite(s) || h < 0 || m < 0 || s < 0) { setAutoErr('請輸入正確的目標完賽時間'); setAutoResult(null); return }
    const targetSeconds = Math.round(h * 3600 + m * 60 + s)
    if (targetSeconds <= 0) { setAutoErr('請輸入目標完賽時間'); setAutoResult(null); return }
    setAutoResult(generateRaceStrategy({ distanceKm, targetSeconds }))
    setAutoParams({ distanceKm, targetSeconds })
  }

  // 套用建議到上面的表單 state：若表單已有內容（名稱/分段/補給任一非空）先確認是否覆蓋。
  function applyAutoResult() {
    if (atAutoLimit) return
    if (!autoResult || !autoParams || autoResult.segments.length === 0) return
    const hasContent = fName.trim() !== '' || fFuel.length > 0 ||
      fSegs.some((r) => r.to_km.trim() !== '' || r.paceMin.trim() !== '' || r.paceSec.trim() !== '')
    if (hasContent && !window.confirm('套用自動建議將覆蓋目前表單內容，確定要覆蓋嗎？')) return

    const baseName = autoStrategyName(autoParams.distanceKm, autoParams.targetSeconds)
    const existingNames = new Set((list ?? []).filter((s) => s.id !== editingId).map((s) => s.name))
    let name = baseName
    for (let n = 2; existingNames.has(name); n++) name = `${baseName} (${n})`

    setFName(name)
    setFSegs(autoResult.segments.map((seg) => ({ to_km: String(seg.to_km), paceMin: String(Math.floor(seg.pace_s / 60)), paceSec: String(seg.pace_s % 60) })))
    setFFuel(autoResult.fuel.map((f) => ({ kind: f.kind, mode: f.mode, val: String(f.mode === 'time' ? f.at / 60 : f.at / 1000) })))
    setFErr('')
    setFErrFields(new Set())
    setAppliedNotice(true)
  }

  function openCreate() {
    setEditingId(null)
    setFName('')
    setFSegs([{ to_km: '', paceMin: '', paceSec: '' }])
    setFFuel([])
    setFErr('')
    setFErrFields(new Set())
    setAutoOpen(false)
    resetAuto()
    setAutoDistMode('full'); setAutoCustomKm(''); setAutoGoalH(''); setAutoGoalM(''); setAutoGoalS('')
    setShowForm(true)
  }
  function openEdit(s: RaceStrategy) {
    setEditingId(s.id)
    setFName(s.name)
    setFSegs(s.segments.map((seg) => ({ to_km: String(seg.to_km), paceMin: String(Math.floor(seg.pace_s / 60)), paceSec: String(seg.pace_s % 60) })))
    setFFuel(s.fuel.map((f) => ({ kind: f.kind, mode: f.mode, val: f.mode === 'time' ? fmtMinutes(f.at) : fmtKm(f.at) })))
    setFErr('')
    setFErrFields(new Set())
    setAutoOpen(false)
    resetAuto()
    setAutoDistMode('full'); setAutoCustomKm(''); setAutoGoalH(''); setAutoGoalM(''); setAutoGoalS('')
    setShowForm(true)
  }
  function closeForm() { if (!fBusy) { setShowForm(false); setAppliedNotice(false) } }

  // 第 idx 段的起點＝前一段終點（首段固定 0）；不開放輸入，僅供顯示與送出時組 segments 用
  function segFromKm(idx: number): number {
    if (idx === 0) return 0
    const prev = Number(fSegs[idx - 1].to_km)
    return Number.isFinite(prev) ? prev : 0
  }
  function addSeg() { setFSegs((prev) => [...prev, { to_km: '', paceMin: '', paceSec: '' }]) }
  // 刪除會使後續列 index 位移，既有的逐欄錯誤標記可能對不上新 index，一併清除避免紅框錯位
  function removeSeg(idx: number) { setFSegs((prev) => (prev.length > 1 ? prev.filter((_, i) => i !== idx) : prev)); setFErrFields(new Set()); setFErr('') }
  function updateSeg(idx: number, patch: Partial<SegRow>) { setFSegs((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r))) }

  function addFuel() { setFFuel((prev) => [...prev, { kind: 'gel', mode: 'time', val: '' }]) }
  function removeFuel(idx: number) { setFFuel((prev) => prev.filter((_, i) => i !== idx)); setFErrFields(new Set()); setFErr('') }
  function updateFuel(idx: number, patch: Partial<FuelRow>) { setFFuel((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r))) }

  async function submitForm() {
    const name = fName.trim()
    if (fSegs.length === 0) { setFErr('請至少新增一段配速'); return }

    // 全欄位一次檢核（不像過去逐段擋在第一個錯誤就 return，只看得到一個問題）：
    // 空值／NaN／負數／既有規則（終點需大於前段終點、配速需在合理區間）皆記入 badFields，
    // 送出前一次標紅所有有問題的欄位，讓使用者一次看清楚哪裡沒填好。
    const badFields = new Set<string>()
    if (!name) badFields.add('name')

    // 組 segments：逐段檢核「終點需大於起點」（起點＝前段終點，首段固定 0）與配速合理區間
    const segments: StrategySegment[] = []
    let from = 0
    for (let i = 0; i < fSegs.length; i++) {
      const row = fSegs[i]
      const segFrom = from
      const toRaw = row.to_km.trim()
      const to = toRaw === '' ? NaN : Number(toRaw)
      const toOk = Number.isFinite(to) && to > segFrom
      if (!toOk) badFields.add(`seg-${i}-to`)

      const minRaw = row.paceMin.trim()
      const secRaw = row.paceSec.trim()
      const min = minRaw === '' ? NaN : Number(minRaw)
      const sec = secRaw === '' ? NaN : Number(secRaw)
      const minOk = Number.isFinite(min) && min >= 0
      const secOk = Number.isFinite(sec) && sec >= 0 && sec <= 59
      if (!minOk) badFields.add(`seg-${i}-min`)
      if (!secOk) badFields.add(`seg-${i}-sec`)

      let pace_s = 0
      if (minOk && secOk) {
        pace_s = Math.round(min * 60 + sec)
        if (pace_s < PACE_MIN_S || pace_s > PACE_MAX_S) { badFields.add(`seg-${i}-min`); badFields.add(`seg-${i}-sec`) }
      }

      if (toOk) { segments.push({ from_km: segFrom, to_km: to, pace_s }); from = to }
    }

    // 組 fuel：時間模式輸入「分鐘」存秒、距離模式輸入「公里」存公尺，皆允許小數
    const fuel: FuelPoint[] = []
    for (let i = 0; i < fFuel.length; i++) {
      const row = fFuel[i]
      const valRaw = row.val.trim()
      const v = valRaw === '' ? NaN : Number(valRaw)
      const vOk = Number.isFinite(v) && v >= 0
      if (!vOk) { badFields.add(`fuel-${i}-val`); continue }
      const at = row.mode === 'time' ? Math.round(v * 60) : Math.round(v * 1000)
      fuel.push({ kind: row.kind, mode: row.mode, at })
    }

    if (badFields.size > 0) { setFErrFields(badFields); setFErr('請填上您預計的策略資訊。'); return }

    const token = getUserToken()
    if (!token) return
    setFBusy(true); setFErr(''); setFErrFields(new Set())
    try {
      if (editingId) {
        await withUserAuth((t) => strategiesApi.update(t, editingId, { name, segments, fuel }))
      } else {
        await withUserAuth((t) => strategiesApi.create(t, { name, segments, fuel }))
      }
      setShowForm(false)
      loadList()
    } catch (e: any) {
      if (e?.status === 409 && e?.message === 'strategy_limit') {
        setFErr(`已達 ${limit} 份策略上限，請先刪除一份再建立`)
      } else if (e?.status === 403 && e?.message === 'vip_only') {
        // 理論上已被前端 vipGate 擋下，這裡僅作防禦：token 過期間 VIP 狀態變動等邊角情況
        setShowForm(false)
        openUpgrade('賽事策略為 VIP 專屬功能。')
      } else {
        setFErr(editingId ? '更新失敗，請稍後再試' : '建立失敗，請稍後再試')
      }
    } finally {
      setFBusy(false)
    }
  }

  async function removeStrategy(id: string) {
    if (!window.confirm('刪除此賽事策略？')) return
    const token = getUserToken()
    if (!token) return
    setDeletingId(id)
    try {
      await withUserAuth((t) => strategiesApi.remove(t, id))
      loadList()
      if (expandedId === id) setExpandedId(null)
    } catch {
      setActionErr('刪除失敗，請稍後再試')
      setTimeout(() => setActionErr(''), 3200)
    } finally {
      setDeletingId(null)
    }
  }

  function startChallenge(id: string) { window.location.href = '/track?strategy=' + id }

  return (
    <>
      <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '4px 2px 12px', lineHeight: 1.7 }}>
        規劃比賽日的配速分段與補給提醒——設定每段的目標配速、何時補能量膠或鹽錠，開跑時進入「比賽專注模式」即時提示，適合正式比賽或模擬賽演練。
      </p>

      <div style={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: 10, marginBottom: 12 }}>
        <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>策略 {list ? list.length : '…'}/{limit}</span>
        <button onClick={vipGate(openCreate)} style={{ ...ctaBtn, ...(isVip ? {} : lockedBtnSmall) }}>{isVip ? '+ 建立賽事策略' : '🔒 建立賽事策略'}</button>
      </div>

      {actionErr && <div style={{ fontSize: 12, color: '#ff6b6b', fontWeight: 700, textAlign: 'center', margin: '0 0 10px' }}>{actionErr}</div>}

      {listErr ? (
        <div style={emptyBox}>賽事策略載入失敗，請稍後再試</div>
      ) : !list ? (
        <div style={emptyBox}>載入中…</div>
      ) : list.length === 0 ? (
        <div style={emptyBox}>
          尚無賽事策略。建立一份配速＋補給計畫，開跑時就能依你設定的節奏收到即時提醒——適合正式比賽或重要的模擬賽。
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {list.map((s) => {
            const expanded = expandedId === s.id
            return (
              <div key={s.id} style={cardStyle}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10 }}>
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div style={{ fontSize: 14.5, fontWeight: 800, color: 'var(--tx)' }}>{s.name}</div>
                    <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 5, lineHeight: 1.8, fontVariantNumeric: 'tabular-nums' }}>
                      總距離 {s.total_km.toFixed(1)} km · 配速 {s.segments.length} 段 · 補給 {s.fuel.length} 點
                    </div>
                  </div>
                  <button onClick={() => setExpandedId(expanded ? null : s.id)} style={expandBtn}>{expanded ? '收合 ▲' : '明細 ▼'}</button>
                </div>

                {expanded && (
                  <div style={{ marginTop: 10, background: 'var(--bg-2)', borderRadius: 10, padding: '9px 11px', display: 'flex', flexDirection: 'column', gap: 8 }}>
                    <div>
                      <div style={{ fontSize: 10.5, fontWeight: 800, color: 'var(--tx-faint)', marginBottom: 4 }}>配速分段</div>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                        {s.segments.map((seg, i) => (
                          <div key={i} style={{ fontSize: 12, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
                            {seg.from_km.toFixed(1)}–{seg.to_km.toFixed(1)} km @ {fmtPace(seg.pace_s)}/km
                          </div>
                        ))}
                      </div>
                    </div>
                    {s.fuel.length > 0 && (
                      <div>
                        <div style={{ fontSize: 10.5, fontWeight: 800, color: 'var(--tx-faint)', marginBottom: 4 }}>補給計畫</div>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                          {s.fuel.map((f, i) => (
                            <div key={i} style={{ fontSize: 12, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
                              {f.mode === 'time' ? `第 ${fmtMinutes(f.at)} 分鐘` : `第 ${fmtKm(f.at)} km`}：{FUEL_KIND_LABEL[f.kind]}
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                )}

                <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                  <button onClick={vipGate(() => startChallenge(s.id))} style={{ ...startBtnFlex, ...(isVip ? {} : lockedBtn) }}>{isVip ? '▶ 啟動賽事模式' : '🔒 開始挑戰（VIP專屬功能）'}</button>
                  <button onClick={vipGate(() => openEdit(s))} style={smallBtn}>✏️ 編輯</button>
                  <button disabled={deletingId === s.id} onClick={vipGate(() => removeStrategy(s.id))} style={smallBtn}>🗑 刪除</button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* 建立/編輯表單（bottom sheet；比照 TrainingScreen 選課表/一鍵安排 modal 慣例：
          data-skin="default" 固定深色底＋亮字，不隨前台 skin 切換而變動可讀性） */}
      {showForm && (
        <div data-skin="default" onClick={closeForm} style={{ position: 'fixed', inset: 0, zIndex: 3600, background: 'rgba(4,8,6,.82)', display: 'flex', alignItems: 'flex-end', justifyContent: 'center' }}>
          <div onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 440, maxHeight: '86dvh', overflowY: 'auto', overflowX: 'hidden', touchAction: 'pan-y', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--line-2)', borderTopLeftRadius: 18, borderTopRightRadius: 18, boxShadow: '0 -12px 40px rgba(0,0,0,.6)', padding: '16px 18px 22px' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ fontSize: 15, fontWeight: 900, color: '#fff' }}>{editingId ? '編輯賽事策略' : '建立賽事策略'}</div>
              <button disabled={fBusy} onClick={closeForm} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 20, cursor: 'pointer', lineHeight: 1, padding: 4 }}>×</button>
            </div>

            {appliedNotice && (
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', lineHeight: 1.7, marginTop: 8 }}>
                已依保守策略自動填入：慢起步、中段巡航、後段預留掉速；配速為含補給/走路的區段平均，可手動微調。請先在長距離訓練測試補給耐受度。
              </div>
            )}

            {/* 自動建立策略：選距離＋目標完賽時間 → generateRaceStrategy 純函式產生建議（配速分段＋補給計畫），
                預覽後才「套用到表單」，不繞過下面既有的欄位編輯與 submitForm 儲存流程。 */}
            <div style={{ marginTop: 12, background: 'rgba(255,255,255,.03)', border: '1px solid var(--line-2)', borderRadius: 12, overflow: 'hidden' }}>
              <button type="button" onClick={openAuto} style={{ width: '100%', display: 'flex', alignItems: 'center', justifyContent: 'space-between', background: 'none', border: 'none', color: 'var(--tx)', padding: '10px 12px', cursor: 'pointer', fontSize: 13, fontWeight: 800, fontFamily: 'inherit' }}>
                <span>⚡ 自動建立策略</span>
                <span style={{ color: 'var(--tx-faint)', fontSize: 11 }}>{autoOpen ? '收合 ▲' : '展開 ▼'}</span>
              </button>
              {autoOpen && (
                <div style={{ padding: '0 12px 12px', display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)', lineHeight: 1.7 }}>
                    依距離與目標完賽時間，自動產生一組保守配速分段與補給提醒，可套用後再手動微調。
                  </div>

                  <div style={formField}>
                    <span style={formLabel}>賽事距離</span>
                    <div style={{ display: 'flex', gap: 6 }}>
                      {(['full', 'half', 'custom'] as const).map((mode) => (
                        <button key={mode} type="button" onClick={() => { setAutoDistMode(mode); resetAuto() }} style={{ ...modeBtn, flex: 1, ...(autoDistMode === mode ? modeBtnActive : {}) }}>
                          {mode === 'full' ? '全馬' : mode === 'half' ? '半馬' : '自訂'}
                        </button>
                      ))}
                    </div>
                    {autoDistMode === 'custom' && (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 6 }}>
                        <input type="number" min={3} max={100} step={0.1} inputMode="decimal" value={autoCustomKm} onChange={(e) => { setAutoCustomKm(e.target.value); resetAuto() }} placeholder="3–100" style={{ ...formInput, flex: 1 }} />
                        <span style={{ fontSize: 11.5, color: 'var(--tx-dim)', flexShrink: 0 }}>km</span>
                      </div>
                    )}
                  </div>

                  <div style={formField}>
                    <span style={formLabel}>目標完賽時間</span>
                    <div style={{ display: 'flex', gap: 8 }}>
                      <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 5 }}>
                        <input type="number" min={0} max={24} step={1} inputMode="numeric" value={autoGoalH} onChange={(e) => { setAutoGoalH(e.target.value); resetAuto() }} placeholder="4" style={formInput} />
                        <span style={{ fontSize: 11, color: 'var(--tx-dim)', flexShrink: 0 }}>時</span>
                      </div>
                      <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 5 }}>
                        <input type="number" min={0} max={59} step={1} inputMode="numeric" value={autoGoalM} onChange={(e) => { setAutoGoalM(e.target.value); resetAuto() }} placeholder="30" style={formInput} />
                        <span style={{ fontSize: 11, color: 'var(--tx-dim)', flexShrink: 0 }}>分</span>
                      </div>
                      <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 5 }}>
                        <input type="number" min={0} max={59} step={1} inputMode="numeric" value={autoGoalS} onChange={(e) => { setAutoGoalS(e.target.value); resetAuto() }} placeholder="0" style={formInput} />
                        <span style={{ fontSize: 11, color: 'var(--tx-dim)', flexShrink: 0 }}>秒</span>
                      </div>
                    </div>
                  </div>

                  {atAutoLimit ? (
                    <div style={{ fontSize: 11.5, color: '#f0b429', textAlign: 'center', lineHeight: 1.6 }}>策略已達 {limit} 份上限，請先刪除一份再自動建立</div>
                  ) : null}

                  <button type="button" disabled={atAutoLimit} onClick={generateAutoPreview} style={{ ...modeBtn, background: 'var(--fug)', borderColor: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, padding: '9px 0', opacity: atAutoLimit ? 0.5 : 1, cursor: atAutoLimit ? 'default' : 'pointer' }}>產生建議</button>

                  {autoErr && <div style={{ fontSize: 12, color: '#ff6b6b', textAlign: 'center' }}>{autoErr}</div>}

                  {autoResult && (
                    <div style={{ background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 11px', display: 'flex', flexDirection: 'column', gap: 6 }}>
                      {autoResult.segments.length > 0 ? (
                        <div style={{ fontSize: 12, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums', lineHeight: 1.8 }}>
                          平均配速 {fmtPace(autoResult.avgPaceSecPerKm)}/km · 配速 {autoResult.segments.length} 段 · 補給 {autoResult.fuel.length} 點
                        </div>
                      ) : null}
                      {autoResult.warnings.length > 0 && (
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                          {autoResult.warnings.map((w, i) => (
                            <div key={i} style={{ fontSize: 11, color: '#f0b429', lineHeight: 1.6 }}>⚠ {w}</div>
                          ))}
                        </div>
                      )}
                      {autoResult.segments.length > 0 && (
                        <div style={{ maxHeight: '40vh', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 8, paddingRight: 2 }}>
                          <div>
                            <div style={{ fontSize: 10.5, fontWeight: 800, color: 'var(--tx-faint)', marginBottom: 4 }}>配速分段</div>
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                              {formatSegmentPreviewLines(autoResult.segments, autoResult.avgPaceSecPerKm).map((line, i) => (
                                <div key={i} style={{ fontSize: 11.5, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>{line}</div>
                              ))}
                            </div>
                          </div>
                          {autoResult.fuel.length > 0 && (
                            <div>
                              <div style={{ fontSize: 10.5, fontWeight: 800, color: 'var(--tx-faint)', marginBottom: 4 }}>補給計畫</div>
                              <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                                {formatFuelPreviewLines(autoResult.fuel, autoResult.segments).map((line, i) => (
                                  <div key={i} style={{ fontSize: 11.5, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>{line}</div>
                                ))}
                              </div>
                            </div>
                          )}
                        </div>
                      )}
                      {autoResult.fuel.some((f) => f.kind === 'caffeine') && (
                        <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>咖啡因為選填，平時不喝咖啡或心悸者請刪除此點。</div>
                      )}
                      {autoResult.segments.length > 0 && (
                        <button type="button" disabled={atAutoLimit} onClick={applyAutoResult} style={{ ...addRowBtn, marginTop: 2, borderStyle: 'solid', borderColor: 'var(--fug)', color: 'var(--fug)', opacity: atAutoLimit ? 0.5 : 1, cursor: atAutoLimit ? 'default' : 'pointer' }}>套用到表單</button>
                      )}
                    </div>
                  )}
                </div>
              )}
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 12 }}>
              <label style={formField}>
                <span style={formLabel}>策略名稱</span>
                <input value={fName} onChange={(e) => { setFName(e.target.value); clearFieldErr('name') }} maxLength={40} placeholder="例：台北馬拉松 破4 配速計畫" style={{ ...formInput, ...(fErrFields.has('name') ? errInput : {}) }} />
              </label>

              <div style={formField}>
                <span style={formLabel}>配速分段</span>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {fSegs.map((row, i) => (
                    <div key={i} style={{ background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 11px' }}>
                      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginBottom: 6, fontVariantNumeric: 'tabular-nums' }}>第 {i + 1} 段 · 起點 {segFromKm(i).toFixed(1)} km</div>
                      <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
                        <div style={{ flex: 1.3, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          <span style={miniLabel}>終點 km</span>
                          <input type="number" min={0} step={0.1} inputMode="decimal" value={row.to_km} onChange={(e) => { updateSeg(i, { to_km: e.target.value }); clearFieldErr(`seg-${i}-to`) }} style={{ ...formInput, ...(fErrFields.has(`seg-${i}-to`) ? errInput : {}) }} />
                        </div>
                        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          <span style={miniLabel}>配速·分</span>
                          <input type="number" min={0} max={60} step={1} inputMode="numeric" value={row.paceMin} onChange={(e) => { updateSeg(i, { paceMin: e.target.value }); clearFieldErr(`seg-${i}-min`) }} style={{ ...formInput, ...(fErrFields.has(`seg-${i}-min`) ? errInput : {}) }} />
                        </div>
                        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          <span style={miniLabel}>秒</span>
                          <input type="number" min={0} max={59} step={1} inputMode="numeric" value={row.paceSec} onChange={(e) => { updateSeg(i, { paceSec: e.target.value }); clearFieldErr(`seg-${i}-sec`) }} style={{ ...formInput, ...(fErrFields.has(`seg-${i}-sec`) ? errInput : {}) }} />
                        </div>
                        <button type="button" disabled={fSegs.length <= 1} onClick={() => removeSeg(i)} style={{ ...rmBtn, opacity: fSegs.length <= 1 ? 0.35 : 1 }}>🗑</button>
                      </div>
                    </div>
                  ))}
                </div>
                <button type="button" onClick={addSeg} style={addRowBtn}>+ 新增分段</button>
              </div>

              <div style={formField}>
                <span style={formLabel}>補給計畫（選填）</span>
                {fFuel.length === 0 && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>尚未加入補給點</div>}
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {fFuel.map((row, i) => (
                    <div key={i} style={{ background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 11px' }}>
                      <div style={{ display: 'flex', gap: 8 }}>
                        <select value={row.kind} onChange={(e) => updateFuel(i, { kind: e.target.value as FuelKind })} style={{ ...formSelect, flex: 1.3 }}>
                          {FUEL_KIND_OPTIONS.map((k) => <option key={k} value={k}>{FUEL_KIND_LABEL[k]}</option>)}
                        </select>
                        <div style={{ display: 'flex', gap: 4, flexShrink: 0 }}>
                          {(['time', 'distance'] as const).map((m) => (
                            <button key={m} type="button" onClick={() => updateFuel(i, { mode: m, val: '' })} style={{ ...modeBtn, ...(row.mode === m ? modeBtnActive : {}) }}>{FUEL_MODE_LABEL[m]}</button>
                          ))}
                        </div>
                        <button type="button" onClick={() => removeFuel(i)} style={rmBtn}>🗑</button>
                      </div>
                      <div style={{ marginTop: 6, display: 'flex', alignItems: 'center', gap: 6 }}>
                        <input
                          type="number" min={0} step={0.1} inputMode="decimal" value={row.val}
                          onChange={(e) => { updateFuel(i, { val: e.target.value }); clearFieldErr(`fuel-${i}-val`) }}
                          placeholder={row.mode === 'time' ? '開跑後第幾分鐘' : '第幾公里'}
                          style={{ ...formInput, flex: 1, ...(fErrFields.has(`fuel-${i}-val`) ? errInput : {}) }}
                        />
                        <span style={{ fontSize: 11.5, color: 'var(--tx-dim)', flexShrink: 0 }}>{row.mode === 'time' ? '分鐘' : 'km'}</span>
                      </div>
                    </div>
                  ))}
                </div>
                <button type="button" onClick={addFuel} style={addRowBtn}>+ 新增補給點</button>
              </div>
            </div>

            {fErr && <div style={{ fontSize: 12, color: '#ff6b6b', textAlign: 'center', margin: '12px 0 0' }}>{fErr}</div>}

            <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
              <button type="button" disabled={fBusy} onClick={closeForm} style={{ ...modeBtn, flex: '0 0 auto', padding: '9px 16px' }}>取消</button>
              <button disabled={fBusy} onClick={submitForm} style={{ ...ctaBtnFlex, opacity: fBusy ? 0.6 : 1 }}>{fBusy ? '儲存中…' : editingId ? '儲存變更' : '建立策略'}</button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}

// ── 樣式：與 TrainingScreen 既有慣例同值，各自持有一份（避免跨檔互相 import 造成循環相依）──
const emptyBox: React.CSSProperties = { color: 'var(--tx-dim)', fontSize: 13.5, lineHeight: 1.9, padding: '32px 10px', textAlign: 'center' }
const cardStyle: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '11px 13px' }
const ctaBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '8px 14px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit', whiteSpace: 'nowrap' }
const ctaBtnFlex: React.CSSProperties = { ...ctaBtn, flex: 1, textAlign: 'center', padding: '10px 0' }
// 非 VIP「開始挑戰／建立策略」類按鈕上鎖樣式：降飽和＋略透明，仍完整可見可點（點擊導向升級彈窗，不是 disabled）
const lockedBtn: React.CSSProperties = { filter: 'grayscale(.65)', opacity: 0.72 }
const lockedBtnSmall: React.CSSProperties = { filter: 'grayscale(.65)', opacity: 0.7 }
const expandBtn: React.CSSProperties = { flexShrink: 0, background: 'none', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 8, padding: '5px 10px', cursor: 'pointer', fontSize: 11, fontFamily: 'inherit' }
const startBtnFlex: React.CSSProperties = { flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 0', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const smallBtn: React.CSSProperties = { flexShrink: 0, background: 'none', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 9, padding: '9px 12px', cursor: 'pointer', fontSize: 12, fontFamily: 'inherit' }
const formField: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 5 }
const formLabel: React.CSSProperties = { fontSize: 11.5, fontWeight: 800, color: 'var(--tx-dim)' }
const miniLabel: React.CSSProperties = { fontSize: 10, color: 'var(--tx-faint)' }
const formInput: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, fontFamily: 'inherit', width: '100%', boxSizing: 'border-box' }
// 欄位驗證失敗紅框（比照 admin/TaskItemEditor.tsx 的 missingInp 慣例：var(--hunt) 錯誤色 + 淡紅底）
const errInput: React.CSSProperties = { borderColor: 'var(--hunt)', background: 'rgba(255,75,92,.07)' }
const formSelect: React.CSSProperties = { minWidth: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 10px', color: 'var(--tx)', fontSize: 12.5, fontFamily: 'inherit' }
const rmBtn: React.CSSProperties = { flexShrink: 0, background: 'none', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 8, width: 32, height: 32, cursor: 'pointer', fontSize: 13, fontFamily: 'inherit' }
const addRowBtn: React.CSSProperties = { marginTop: 4, background: 'none', border: '1px dashed var(--line-2)', color: 'var(--fug)', borderRadius: 9, padding: '7px 0', cursor: 'pointer', fontSize: 12, fontWeight: 700, fontFamily: 'inherit' }
const modeBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 8, padding: '6px 10px', cursor: 'pointer', fontSize: 11.5, fontWeight: 700, fontFamily: 'inherit' }
const modeBtnActive: React.CSSProperties = { background: 'var(--fug)', borderColor: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800 }
