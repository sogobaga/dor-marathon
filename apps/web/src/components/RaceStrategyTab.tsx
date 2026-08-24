'use client'

import { useEffect, useState } from 'react'
import { strategiesApi, FUEL_KIND_LABEL, type RaceStrategy, type StrategySegment, type FuelPoint, type FuelKind } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'

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

  function openCreate() {
    setEditingId(null)
    setFName('')
    setFSegs([{ to_km: '', paceMin: '', paceSec: '' }])
    setFFuel([])
    setFErr('')
    setShowForm(true)
  }
  function openEdit(s: RaceStrategy) {
    setEditingId(s.id)
    setFName(s.name)
    setFSegs(s.segments.map((seg) => ({ to_km: String(seg.to_km), paceMin: String(Math.floor(seg.pace_s / 60)), paceSec: String(seg.pace_s % 60) })))
    setFFuel(s.fuel.map((f) => ({ kind: f.kind, mode: f.mode, val: f.mode === 'time' ? fmtMinutes(f.at) : fmtKm(f.at) })))
    setFErr('')
    setShowForm(true)
  }
  function closeForm() { if (!fBusy) setShowForm(false) }

  // 第 idx 段的起點＝前一段終點（首段固定 0）；不開放輸入，僅供顯示與送出時組 segments 用
  function segFromKm(idx: number): number {
    if (idx === 0) return 0
    const prev = Number(fSegs[idx - 1].to_km)
    return Number.isFinite(prev) ? prev : 0
  }
  function addSeg() { setFSegs((prev) => [...prev, { to_km: '', paceMin: '', paceSec: '' }]) }
  function removeSeg(idx: number) { setFSegs((prev) => (prev.length > 1 ? prev.filter((_, i) => i !== idx) : prev)) }
  function updateSeg(idx: number, patch: Partial<SegRow>) { setFSegs((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r))) }

  function addFuel() { setFFuel((prev) => [...prev, { kind: 'gel', mode: 'time', val: '' }]) }
  function removeFuel(idx: number) { setFFuel((prev) => prev.filter((_, i) => i !== idx)) }
  function updateFuel(idx: number, patch: Partial<FuelRow>) { setFFuel((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r))) }

  async function submitForm() {
    const name = fName.trim()
    if (!name) { setFErr('請輸入策略名稱'); return }
    if (fSegs.length === 0) { setFErr('請至少新增一段配速'); return }

    // 組 segments：逐段檢核「終點需大於起點」（起點＝前段終點，首段固定 0）與配速合理區間
    const segments: StrategySegment[] = []
    let from = 0
    for (let i = 0; i < fSegs.length; i++) {
      const row = fSegs[i]
      const to = Number(row.to_km)
      if (!Number.isFinite(to) || to <= from) { setFErr(`第 ${i + 1} 段終點需大於 ${from.toFixed(1)} km`); return }
      const min = row.paceMin === '' ? 0 : Number(row.paceMin)
      const sec = row.paceSec === '' ? 0 : Number(row.paceSec)
      if (!Number.isFinite(min) || !Number.isFinite(sec) || min < 0 || sec < 0 || sec > 59) { setFErr(`第 ${i + 1} 段配速格式錯誤`); return }
      const pace_s = Math.round(min * 60 + sec)
      if (pace_s < PACE_MIN_S || pace_s > PACE_MAX_S) { setFErr(`第 ${i + 1} 段配速需在 ${fmtPace(PACE_MIN_S)}～${fmtPace(PACE_MAX_S)} /km 之間`); return }
      segments.push({ from_km: from, to_km: to, pace_s })
      from = to
    }

    // 組 fuel：時間模式輸入「分鐘」存秒、距離模式輸入「公里」存公尺，皆允許小數
    const fuel: FuelPoint[] = []
    for (let i = 0; i < fFuel.length; i++) {
      const row = fFuel[i]
      const v = Number(row.val)
      if (!Number.isFinite(v) || v < 0) { setFErr(`第 ${i + 1} 個補給點數值格式錯誤`); return }
      const at = row.mode === 'time' ? Math.round(v * 60) : Math.round(v * 1000)
      fuel.push({ kind: row.kind, mode: row.mode, at })
    }

    const token = getUserToken()
    if (!token) return
    setFBusy(true); setFErr('')
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
                  <button onClick={vipGate(() => startChallenge(s.id))} style={{ ...startBtnFlex, ...(isVip ? {} : lockedBtn) }}>{isVip ? '▶ 開始挑戰' : '🔒 開始挑戰（VIP專屬功能）'}</button>
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

            <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 12 }}>
              <label style={formField}>
                <span style={formLabel}>策略名稱</span>
                <input value={fName} onChange={(e) => setFName(e.target.value)} maxLength={40} placeholder="例：台北馬拉松 破4 配速計畫" style={formInput} />
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
                          <input type="number" min={0} step={0.1} inputMode="decimal" value={row.to_km} onChange={(e) => updateSeg(i, { to_km: e.target.value })} style={formInput} />
                        </div>
                        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          <span style={miniLabel}>配速·分</span>
                          <input type="number" min={0} max={60} step={1} inputMode="numeric" value={row.paceMin} onChange={(e) => updateSeg(i, { paceMin: e.target.value })} style={formInput} />
                        </div>
                        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          <span style={miniLabel}>秒</span>
                          <input type="number" min={0} max={59} step={1} inputMode="numeric" value={row.paceSec} onChange={(e) => updateSeg(i, { paceSec: e.target.value })} style={formInput} />
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
                          onChange={(e) => updateFuel(i, { val: e.target.value })}
                          placeholder={row.mode === 'time' ? '開跑後第幾分鐘' : '第幾公里'}
                          style={{ ...formInput, flex: 1 }}
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
const formSelect: React.CSSProperties = { minWidth: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 10px', color: 'var(--tx)', fontSize: 12.5, fontFamily: 'inherit' }
const rmBtn: React.CSSProperties = { flexShrink: 0, background: 'none', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 8, width: 32, height: 32, cursor: 'pointer', fontSize: 13, fontFamily: 'inherit' }
const addRowBtn: React.CSSProperties = { marginTop: 4, background: 'none', border: '1px dashed var(--line-2)', color: 'var(--fug)', borderRadius: 9, padding: '7px 0', cursor: 'pointer', fontSize: 12, fontWeight: 700, fontFamily: 'inherit' }
const modeBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 8, padding: '6px 10px', cursor: 'pointer', fontSize: 11.5, fontWeight: 700, fontFamily: 'inherit' }
const modeBtnActive: React.CSSProperties = { background: 'var(--fug)', borderColor: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800 }
