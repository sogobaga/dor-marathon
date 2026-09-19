'use client'

// DORPG P6（酒館／隊伍／傭兵腳本，見 scratchpad/dorpg_p6/CONTRACT.md §3、WIRE.md §REST）：
// 沿用 CharacterScreen 的元件與樣式語言（同一批 var(--bg)/var(--tx)/var(--gold) CSS 變數、同一種
// header/section/卡片排版），但欄位不共用——酒館頁編輯的是「未上場的腳本草稿」，CharacterScreen
// 編輯的是「玩家本人已持久化的角色」，兩邊的存檔時機與驗證流程完全不同，故各自獨立成檔，不互相
// import 對方的子元件或樣式常數。
//
// 畫面分兩層：
//   1. 主畫面（隊伍 4 格 + 傭兵 4 張卡）
//   2. 腳本編輯器（PresetEditor，點「編輯腳本」後整頁切換過去，不是 fixed 覆蓋層——全站規則見
//      CLAUDE.md「PC手機模擬框覆蓋層」，本頁刻意用「同一個 return 樹裡切換整頁內容」的既有慣例
//      （同 PhoneShell 的 battleView 分支切換手法），完全不新增 position:fixed 元素）。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  rpgTavernApi,
  type ArmorDTO,
  type CompanionPresetDTO,
  type EquipmentSlot,
  type EquippedGearDTO,
  type JobDTO,
  type MercenaryDTO,
  type PartySlotDTO,
  type PresetEquipmentInput,
  type PresetSaveBody,
  type PresetValidateResponse,
  type RpgStatKey,
  type RpgStats,
  type SkillDTO,
  type StrategyDTO,
  type TavernGearResponse,
  type TavernResponse,
  type WeaponDTO,
} from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import { charPortrait } from '@/lib/dorpg/assets'
import {
  STAT_META, jobEmoji, SKILL_KIND_LABEL, formatEffectAtLevel, formatTauntEffect, estimateMaxStatValue,
  EQUIP_SLOT_LABEL, EQUIP_SLOT_ORDER, formatEquipBonus,
} from '@/lib/rpgMeta'
import EquipmentPanel from './EquipmentPanel'

// 隊伍/腳本操作 400 錯誤碼 → 中文（WIRE §REST：PUT /rpg/party 與 POST/PUT /rpg/presets 系列）。
const PARTY_ERR_LABEL: Record<string, string> = {
  party_full: '隊伍已滿（最多 4 位傭兵）',
  duplicate_companion: '這位傭兵已經在隊伍裡了',
  not_mercenary: '這個角色不是可雇用的傭兵',
  preset_mismatch: '這個腳本不屬於這位傭兵，或不是你的腳本',
}
const PRESET_ERR_LABEL: Record<string, string> = {
  stat_over_budget: '素質配點超過可用點數',
  stat_over_cap: '素質超過該等級上限',
  stat_below_initial: '素質不可低於初始值',
  skill_over_budget: '技能點數不足',
  skill_prereq: '尚未達成前置技能等級需求',
  skill_max_level: '已達技能等級上限',
  skill_not_in_job: '這個技能不屬於該傭兵的職業',
  level_range: '等級需為 1–99 的整數',
  name_required: '請輸入腳本名稱',
  preset_readonly: '系統預設腳本唯讀，請另存新腳本',
}
// DORPG P9（契約 §3、WIRE §REST）：POST/PUT /rpg/presets/validate 新增的 equipment.<slot>／
// strategy_id 錯誤碼 → 中文（equipment 五碼同 EquipmentScreen 的 EQUIP_ERR_LABEL，這裡各自獨立
// 一份，理由同檔頭「各畫面不互相 import 對方常數」的既有慣例）。
const EQUIPMENT_ERR_LABEL: Record<string, string> = {
  not_found: '找不到這件裝備',
  wrong_job: '這件裝備不屬於這位傭兵的職業',
  wrong_slot: '這件裝備不能裝在這個格子',
  level_too_low: '等級尚未達到裝備門檻',
  duplicate_accessory: '兩個飾品格不能裝同一件',
}
const STRATEGY_ERR_LABEL: Record<string, string> = { unknown_strategy: '未知的 AI 策略，請重新選擇' }
const DEFAULT_STRATEGY_ID = 'balanced'
function friendlyErr(e: any, table: Record<string, string>, fallback: string): string {
  const code = e?.message
  return (code && table[code]) || (typeof code === 'string' && code && code !== 'request failed' ? code : '') || fallback
}

export default function TavernScreen({ onBack }: { onBack: () => void }) {
  const [data, setData] = useState<TavernResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState('')
  const [actionErr, setActionErr] = useState('')
  const [flash, setFlash] = useState('')
  const [busySlot, setBusySlot] = useState<number | null>(null)
  const [busyMercId, setBusyMercId] = useState<string | null>(null)
  // 尚未加入隊伍的傭兵，卡片上「腳本」下拉目前選到哪個——純本地 UI 狀態，「加入隊伍」時才送出。
  // 已在隊伍的傭兵改讀 data.party 裡真正生效的 preset_id（見 selectedPresetIdFor），不用這份 state。
  const [pendingPreset, setPendingPreset] = useState<Record<string, string>>({})
  const [editor, setEditor] = useState<{ merc: MercenaryDTO; preset: CompanionPresetDTO } | null>(null)

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
    withUserAuth((t) => rpgTavernApi.get(t))
      .then((r) => {
        setData(r)
        setPendingPreset((prev) => {
          const next = { ...prev }
          for (const m of r.mercenaries) {
            if (next[m.id]) continue
            const slot = r.party.find((p) => p.companion_id === m.id)
            next[m.id] = slot?.preset_id ?? m.presets[0]?.id ?? ''
          }
          return next
        })
        setLoadErr('')
      })
      .catch((e: any) => setLoadErr(e?.status === 403 ? '此功能尚未開放給你的帳號' : e?.message || '載入失敗'))
      .finally(() => setLoading(false))
  }, [])
  useEffect(() => { load() }, [load])

  // PUT /rpg/party「只送有人的格子；伺服器整批覆蓋 4 格」（契約 §3.3）——這裡從目前的 data.party
  // 組出完整覆蓋陣列，overrides 用來加入/移出/換腳本這三種操作共用同一個組陣列邏輯。
  function buildSlots(
    party: PartySlotDTO[],
    overrides: { slot: number; companion_id: string | null; preset_id: string | null }[],
  ): { slot: number; companion_id: string; preset_id: string | null }[] {
    const map = new Map<number, { slot: number; companion_id: string; preset_id: string | null }>()
    party.forEach((p) => { if (p.companion_id) map.set(p.slot, { slot: p.slot, companion_id: p.companion_id, preset_id: p.preset_id }) })
    overrides.forEach((o) => {
      if (o.companion_id) map.set(o.slot, { slot: o.slot, companion_id: o.companion_id, preset_id: o.preset_id })
      else map.delete(o.slot)
    })
    return Array.from(map.values())
  }

  async function removeFromParty(slot: number) {
    if (!data || busySlot != null) return
    setBusySlot(slot)
    try {
      const slots = buildSlots(data.party, [{ slot, companion_id: null, preset_id: null }])
      const next = await withUserAuth((t) => rpgTavernApi.setParty(t, slots))
      setData(next)
    } catch (e: any) {
      showErr(friendlyErr(e, PARTY_ERR_LABEL, '移出失敗，請稍後再試'))
    } finally {
      setBusySlot(null)
    }
  }

  async function joinParty(merc: MercenaryDTO) {
    if (!data || busyMercId) return
    const occupied = new Set(data.party.filter((p) => p.companion_id).map((p) => p.slot))
    const emptySlot = [1, 2, 3, 4].find((s) => !occupied.has(s))
    if (!emptySlot) { showErr('隊伍已滿（最多 4 位傭兵）'); return }
    const presetId = pendingPreset[merc.id] || merc.presets[0]?.id
    if (!presetId) { showErr('這位傭兵沒有可用的腳本'); return }
    setBusyMercId(merc.id)
    try {
      const slots = buildSlots(data.party, [{ slot: emptySlot, companion_id: merc.id, preset_id: presetId }])
      const next = await withUserAuth((t) => rpgTavernApi.setParty(t, slots))
      setData(next)
      showFlash(`${merc.name} 已加入隊伍`)
    } catch (e: any) {
      showErr(friendlyErr(e, PARTY_ERR_LABEL, '加入隊伍失敗，請稍後再試'))
    } finally {
      setBusyMercId(null)
    }
  }

  // 換腳本：已在隊伍的傭兵直接送 PUT /rpg/party 套用新腳本（不必先移出再加入，方便快速切換測試
  // 組合）；尚未加入隊伍的傭兵只更新本地 pendingPreset，留到「加入隊伍」時一起送出。
  async function switchPreset(merc: MercenaryDTO, presetId: string) {
    if (!data) return
    if (!merc.in_party) { setPendingPreset((s) => ({ ...s, [merc.id]: presetId })); return }
    const slot = data.party.find((p) => p.companion_id === merc.id)
    if (!slot) return
    setBusyMercId(merc.id)
    try {
      const slots = buildSlots(data.party, [{ slot: slot.slot, companion_id: merc.id, preset_id: presetId }])
      const next = await withUserAuth((t) => rpgTavernApi.setParty(t, slots))
      setData(next)
      setPendingPreset((s) => ({ ...s, [merc.id]: presetId }))
    } catch (e: any) {
      showErr(friendlyErr(e, PARTY_ERR_LABEL, '切換腳本失敗，請稍後再試'))
    } finally {
      setBusyMercId(null)
    }
  }

  async function afterEditorSaved() {
    await load()
    setEditor(null)
  }

  if (editor) {
    return (
      <PresetEditor
        merc={editor.merc}
        preset={editor.preset}
        strategies={data?.strategies ?? []}
        onBack={() => setEditor(null)}
        onSaved={afterEditorSaved}
      />
    )
  }

  const partyBySlot = new Map((data?.party ?? []).map((p) => [p.slot, p] as const))

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>🍺 酒館</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>隊伍與傭兵腳本（測試階段）</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '14px 18px 28px' }}>
        {loading && <Hint>載入中…</Hint>}
        {!loading && !data && <Hint>{loadErr || '此功能尚未開放給你的帳號。'}</Hint>}

        {!loading && data && (
          <>
            {actionErr && <div style={errBanner}>{actionErr}</div>}
            {flash && <div style={okBanner}>✓ {flash}</div>}

            {/* ---- 隊伍 ---- */}
            <h2 style={sectionTitle}>隊伍</h2>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
              <div style={leaderRow}>
                <span style={{ fontSize: 18 }}>{jobEmoji(data.leader.job?.id ?? null)}</span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{data.leader.name}（隊長）</div>
                  <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>
                    {data.leader.job ? data.leader.job.name : '未選職業'}・有效 Lv.{data.leader.effective_level}
                  </div>
                </div>
              </div>
              {[1, 2, 3, 4].map((slotNum) => {
                const slot = partyBySlot.get(slotNum)
                const merc = slot?.companion_id ? data.mercenaries.find((m) => m.id === slot.companion_id) ?? null : null
                return (
                  <div key={slotNum} style={slotRow}>
                    <span style={{ fontSize: 10.5, color: 'var(--tx-faint)', width: 18, flexShrink: 0 }}>#{slotNum}</span>
                    {merc ? (
                      <>
                        {merc.portrait_id && (
                          // eslint-disable-next-line @next/next/no-img-element
                          <img src={charPortrait(merc.portrait_id, 256)} alt="" style={portraitThumb} />
                        )}
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{merc.name}</div>
                          <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>
                            {slot?.preset_name ?? '（系統預設）'}{slot?.level != null ? `・Lv.${slot.level}` : ''}
                          </div>
                        </div>
                        <button onClick={() => removeFromParty(slotNum)} disabled={busySlot === slotNum} style={dangerGhostBtn}>移出</button>
                      </>
                    ) : (
                      <div style={{ flex: 1, fontSize: 12, color: 'var(--tx-faint)' }}>空</div>
                    )}
                  </div>
                )
              })}
            </div>

            {/* ---- 傭兵 ---- */}
            <div style={{ marginTop: 24 }}>
              <h2 style={sectionTitle}>傭兵</h2>
              <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 2 }}>未來需花費 DP 雇用（測試階段免費）</div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 10 }}>
                {data.mercenaries.map((m) => (
                  <MercenaryCard
                    key={m.id}
                    merc={m}
                    selectedPresetId={selectedPresetIdFor(m, data, pendingPreset)}
                    busy={busyMercId === m.id}
                    onSelectPreset={(id) => switchPreset(m, id)}
                    onJoin={() => joinParty(m)}
                    onEdit={(preset) => setEditor({ merc: m, preset })}
                  />
                ))}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// 已在隊伍：以 data.party 裡真正生效的 preset_id 為準（可能被「換腳本」立即改過）；尚未加入：讀本地 pendingPreset 草稿選擇。
function selectedPresetIdFor(merc: MercenaryDTO, data: TavernResponse, pendingPreset: Record<string, string>): string {
  if (merc.in_party) {
    const slot = data.party.find((p) => p.companion_id === merc.id)
    return slot?.preset_id ?? merc.presets[0]?.id ?? ''
  }
  return pendingPreset[merc.id] ?? merc.presets[0]?.id ?? ''
}

// ============================== 傭兵卡 ==============================

function MercenaryCard({ merc, selectedPresetId, busy, onSelectPreset, onJoin, onEdit }: {
  merc: MercenaryDTO
  selectedPresetId: string
  busy: boolean
  onSelectPreset: (id: string) => void
  onJoin: () => void
  onEdit: (preset: CompanionPresetDTO) => void
}) {
  const selectedPreset = merc.presets.find((p) => p.id === selectedPresetId) ?? merc.presets[0] ?? null
  return (
    <div style={mercCard}>
      <div style={{ display: 'flex', gap: 10 }}>
        {merc.portrait_id && (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={charPortrait(merc.portrait_id, 256)} alt="" style={portraitThumbLg} />
        )}
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 15 }}>{jobEmoji(merc.job.id)}</span>
            <span style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>{merc.name}</span>
            {merc.in_party && <span style={inPartyTag}>隊伍中</span>}
          </div>
          <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginTop: 2 }}>{merc.job.name}・{merc.role}</div>
        </div>
      </div>

      <div style={{ display: 'flex', gap: 6, marginTop: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <select value={selectedPresetId} disabled={busy} onChange={(e) => onSelectPreset(e.target.value)} style={presetSelect}>
          {merc.presets.map((p) => (
            <option key={p.id} value={p.id}>{p.is_system ? `${p.name}（系統）` : p.name}・Lv.{p.level}</option>
          ))}
        </select>
        {selectedPreset && (
          <button onClick={() => onEdit(selectedPreset)} disabled={busy} style={ghostActionBtn(busy)}>編輯腳本</button>
        )}
        {merc.in_party ? (
          <button disabled style={{ ...ghostActionBtn(true), color: 'var(--fug)', borderColor: 'var(--fug)' }}>已在隊伍</button>
        ) : (
          <button onClick={onJoin} disabled={busy} style={joinBtn}>加入隊伍</button>
        )}
      </div>

      {selectedPreset && (
        <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 6, lineHeight: 1.6 }}>
          HP {selectedPreset.derived.hp_max}・MP {selectedPreset.derived.mp_max}・ATK {selectedPreset.derived.atk}・MATK {selectedPreset.derived.matk}・DEF {selectedPreset.derived.def}・MDEF {selectedPreset.derived.mdef}
        </div>
      )}
    </div>
  )
}

// ============================== 腳本編輯器 ==============================

const PRESET_DERIVED_META: { key: keyof CompanionPresetDTO['derived']; label: string }[] = [
  { key: 'hp_max', label: 'HP 上限' },
  { key: 'mp_max', label: 'MP 上限' },
  { key: 'atk', label: '物理攻擊力' },
  { key: 'matk', label: '魔法攻擊力' },
  { key: 'def', label: '物理防禦力' },
  { key: 'mdef', label: '魔法防禦力' },
  { key: 'hit', label: '命中率' },
  { key: 'flee', label: '迴避率' },
  { key: 'aspd', label: '攻擊速度' },
  { key: 'crit_pct', label: '暴擊率' },
]

/** EquippedGearDTO → 八格 item_id 草稿（PresetSaveBody.equipment 要送的形狀），DORPG P9。 */
function equipmentIdsFromDTO(eq: EquippedGearDTO | null | undefined): Record<EquipmentSlot, string | null> {
  return {
    weapon: eq?.weapon?.id ?? null,
    helmet: eq?.helmet?.id ?? null,
    gloves: eq?.gloves?.id ?? null,
    armor: eq?.armor?.id ?? null,
    legs: eq?.legs?.id ?? null,
    boots: eq?.boots?.id ?? null,
    accessory1: eq?.accessory1?.id ?? null,
    accessory2: eq?.accessory2?.id ?? null,
  }
}
const ARMOR_EQUIP_SLOT_LIST: EquipmentSlot[] = ['helmet', 'gloves', 'armor', 'legs', 'boots', 'accessory1', 'accessory2']

function PresetEditor({ merc, preset, strategies, onBack, onSaved }: {
  merc: MercenaryDTO
  preset: CompanionPresetDTO
  /** DORPG P9：GET /rpg/tavern 的 strategies（只含 is_active，依 sort_order）——「AI 策略」下拉的資料來源。 */
  strategies: StrategyDTO[]
  onBack: () => void
  onSaved: () => void | Promise<void>
}) {
  const isSystem = preset.is_system
  // 系統預設唯讀，只能「另存新腳本」（契約 §3.2）——預先在名稱後面補「副本」提示使用者這是複製品，
  // 不會真的動到系統預設本身；自己的腳本則維持原名，「儲存」即可原地覆蓋。
  const [name, setName] = useState(isSystem ? `${preset.name} 副本` : preset.name)
  const [levelInput, setLevelInput] = useState(String(preset.level))
  const [stats, setStats] = useState<RpgStats>(preset.stats)
  const [skillLevels, setSkillLevels] = useState<Record<string, number>>(preset.skill_levels)
  // DORPG P9（契約 §2/§5、WIRE §REST）：八格裝備草稿＋AI 策略——跟 stats/skillLevels 一樣是純
  // 本地草稿，存檔時才整包送出；儲存前的即時預覽（可裝/等級門檻/裝備加成）一樣經 validate 端點。
  const [equipmentIds, setEquipmentIds] = useState<Record<EquipmentSlot, string | null>>(() => equipmentIdsFromDTO(preset.equipment))
  const [strategyId, setStrategyId] = useState(preset.strategy_id || DEFAULT_STRATEGY_ID)
  const [gear, setGear] = useState<TavernGearResponse | null>(null)
  const [gearErr, setGearErr] = useState('')
  const [result, setResult] = useState<PresetValidateResponse | null>(null)
  const [saving, setSaving] = useState(false)
  const [actionErr, setActionErr] = useState('')
  const [flash, setFlash] = useState('')
  const [maxing, setMaxing] = useState<string | null>(null) // 目前正在收斂中的素質 key 或技能 id（見 maxStat/skillDelta），期間鎖住其他 Max 操作避免互踩
  const debounceRef = useRef<number | null>(null)
  // 審查【item4】debounce validate 的請求序號——輸入變動很快時可能同時有兩個 request 在飛，網路
  // 順序不保證跟送出順序一致，只採用「目前序號」那個回應，避免舊回應（例如使用者已經改了三次值，
  // 第一次送出的回應卻最後才回來）蓋掉新回應，讓畫面顯示的 result 跟畫面上的 stats/skillLevels 對不上。
  const reqSeqRef = useRef(0)

  function showErr(msg: string) {
    setActionErr(msg)
    window.setTimeout(() => setActionErr(''), 3500)
  }
  function showFlash(msg: string) {
    setFlash(msg)
    window.setTimeout(() => setFlash(''), 1800)
  }

  const level = (() => {
    const n = parseInt(levelInput, 10)
    if (isNaN(n)) return preset.level
    return Math.max(1, Math.min(99, n))
  })()

  // DORPG P9：腳本編輯器「裝備」區的清單來源——只在掛載時抓一次（該傭兵職業固定不變，見
  // merc.job.id），can_equip 用目前草稿 level 在本地重新判斷（見下方 weaponsForPanel/armorForPanel），
  // 不必每次調整等級都重打一次 API（規則就是契約 §2 那句「level_req ≤ 腳本等級」，前端可以自己算）。
  useEffect(() => {
    let alive = true
    withUserAuth((t) => rpgTavernApi.gear(t, merc.job.id, preset.level))
      .then((r) => { if (alive) setGear(r) })
      .catch(() => { if (alive) setGearErr('裝備清單載入失敗，請重新整理再試') })
    return () => { alive = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 純試算：只回傳結果、不寫入 result state——給下面的收斂迴圈（maxStat/skillDelta 的 'max' 分支）
  // 用，這兩處需要「立刻拿到這次試算的結果來判斷合不合法」，跟畫面預覽用的 debounce 是兩件事，
  // 共用同一顆會互踩序號、也會把使用者還沒確定的收斂中間值短暫閃現在衍生數值預覽上。
  const validateDraft = useCallback((
    lv: number, st: RpgStats, sk: Record<string, number>, eq: PresetEquipmentInput, sid: string,
  ): Promise<PresetValidateResponse | null> =>
    withUserAuth((t) => rpgTavernApi.validatePreset(t, { companion_id: preset.companion_id, level: lv, stats: st, skill_levels: sk, equipment: eq, strategy_id: sid }))
      .catch(() => null)
  , [preset.companion_id])

  const runValidate = useCallback((
    lv: number, st: RpgStats, sk: Record<string, number>, eq: PresetEquipmentInput, sid: string,
  ) => {
    // 審查【item4】遞增序號＋比對：只有仍是「目前最新一次」的請求才允許寫入 result，較舊的請求
    // 就算比較晚回來也直接丟棄（見上方 reqSeqRef 註解）。
    const seq = ++reqSeqRef.current
    validateDraft(lv, st, sk, eq, sid).then((r) => {
      if (r && reqSeqRef.current === seq) setResult(r)
      // r 為 null＝預覽失敗（validateDraft 已吞掉錯誤），不擋編輯，維持上一次的 result（若有）；
      // 存檔時仍會再檢查一次。
    })
  }, [validateDraft])

  // 等級／素質／技能／裝備／策略共用一顆 debounce 計時器（契約 §3.4：即時衍生值預覽經 validate
  // 端點；DORPG P9 把裝備與策略也併入同一顆，行為一致）；按鈕類操作（+1/Max/歸零）不必額外等，
  // 300ms 對打字輸入與連續點按都夠用。
  useEffect(() => {
    if (debounceRef.current) window.clearTimeout(debounceRef.current)
    debounceRef.current = window.setTimeout(() => runValidate(level, stats, skillLevels, equipmentIds, strategyId), 300)
    return () => { if (debounceRef.current) window.clearTimeout(debounceRef.current) }
  }, [level, stats, skillLevels, equipmentIds, strategyId, runValidate])

  // DORPG P9：等級調降時，原本裝備但已經超過新等級門檻的品項自動清空並提示（契約 §3：「腳本等級
  // 下調導致 level_too_low 時由前端自動清空該格再送出」）。用 ref 記上一次的 level 只在真的變動時
  // 才跑，避免每次 render 都重複判斷；尚未載入裝備清單（gear===null）時無從比對 level_req，略過。
  const prevLevelRef = useRef(level)
  useEffect(() => {
    if (level === prevLevelRef.current) return
    // 2026-09-19 修復：gear 尚未載入時直接 return，且不消費（更新）prevLevelRef——舊寫法在這裡
    // 提前把 ref 蓋成新 level，若使用者在 gear 還沒載入完就調降等級，等 gear 真正載入、effect
    // 因 gear 這個 dep 變化而重跑時，「level === prevLevelRef.current」已經是 true（ref 早被
    // 消費掉），比對永遠不會執行，超過新等級門檻的裝備就不會被自動清空／跳提示。改成只在
    // 「真的走到比對」這一步才更新 ref，確保 gear 就緒後補跑的這一輪仍會觸發清空邏輯。
    if (!gear) return
    prevLevelRef.current = level
    setEquipmentIds((cur) => {
      const next = { ...cur }
      const cleared: string[] = []
      EQUIP_SLOT_ORDER.forEach((slot) => {
        const id = cur[slot]
        if (!id) return
        const item = slot === 'weapon' ? gear.weapons.find((w) => w.id === id) : gear.armor_items.find((a) => a.id === id)
        if (item && item.level_req > level) {
          next[slot] = null
          cleared.push(EQUIP_SLOT_LABEL[slot])
        }
      })
      if (cleared.length > 0) showFlash(`等級調降，已自動卸下：${cleared.join('、')}`)
      return cleared.length > 0 ? next : cur
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [level, gear])

  // DORPG P9：把草稿的 item_id 八格解析成 EquipmentPanel 要的完整 DTO 形狀，並在本地重新判斷
  // can_equip／equipped／equipped_in（gear 端點回傳的這三個欄位恆為 false/null，契約要求「腳本
  // 編輯器自己比對」，見 TavernGearResponse 型別註解）。
  const weaponsForPanel: WeaponDTO[] = useMemo(() => (gear?.weapons ?? []).map((w) => ({
    ...w, can_equip: w.level_req <= level, equipped: equipmentIds.weapon === w.id,
  })), [gear, level, equipmentIds.weapon])
  const armorForPanel: ArmorDTO[] = useMemo(() => (gear?.armor_items ?? []).map((a) => ({
    ...a,
    can_equip: a.level_req <= level,
    equipped_in: ARMOR_EQUIP_SLOT_LIST.find((slot) => equipmentIds[slot] === a.id) ?? null,
  })), [gear, level, equipmentIds])
  const equippedForPanel: EquippedGearDTO = useMemo(() => ({
    weapon: weaponsForPanel.find((w) => w.id === equipmentIds.weapon) ?? null,
    helmet: armorForPanel.find((a) => a.id === equipmentIds.helmet) ?? null,
    gloves: armorForPanel.find((a) => a.id === equipmentIds.gloves) ?? null,
    armor: armorForPanel.find((a) => a.id === equipmentIds.armor) ?? null,
    legs: armorForPanel.find((a) => a.id === equipmentIds.legs) ?? null,
    boots: armorForPanel.find((a) => a.id === equipmentIds.boots) ?? null,
    accessory1: armorForPanel.find((a) => a.id === equipmentIds.accessory1) ?? null,
    accessory2: armorForPanel.find((a) => a.id === equipmentIds.accessory2) ?? null,
  }), [weaponsForPanel, armorForPanel, equipmentIds])
  function onEquipSlot(slot: EquipmentSlot, itemId: string | null) {
    setEquipmentIds((cur) => ({ ...cur, [slot]: itemId }))
  }

  // DORPG P9：validate 回應裡 field="equipment.<slot>" 的錯誤轉成 EquipmentPanel 要的 slot→文案 map；
  // field="strategy_id" 的錯誤另外拉出來給策略下拉用。
  const slotErrors = useMemo(() => {
    const out: Partial<Record<EquipmentSlot, string>> = {}
    for (const e of result?.errors ?? []) {
      if (!e.field.startsWith('equipment.')) continue
      const slot = e.field.slice('equipment.'.length) as EquipmentSlot
      out[slot] = EQUIPMENT_ERR_LABEL[e.code] || e.message
    }
    return out
  }, [result])
  const strategyErrText = (() => {
    const e = (result?.errors ?? []).find((x) => x.field === 'strategy_id')
    return e ? (STRATEGY_ERR_LABEL[e.code] || e.message) : null
  })()

  function errorFor(field: string): string | null {
    // 審查【CRITICAL】安全存取：後端合法草稿雖已修正為一律回 []，型別上仍允許 null（防禦未來漂移，
    // 見 api.ts PresetValidateResponse.errors 註解），這裡不能對 null 呼叫 .find。
    const e = (result?.errors ?? []).find((x) => x.field === field)
    return e ? (PRESET_ERR_LABEL[e.code] || e.message) : null
  }

  function setStat(key: RpgStatKey, v: number) {
    setStats((s) => ({ ...s, [key]: Math.max(0, v) }))
  }
  function bumpStat(key: RpgStatKey) {
    setStat(key, stats[key] + 1)
  }
  // Max：先用 rpgMeta.ts estimateMaxStatValue 本地估算一個候選值（沒有精算端點，公式是寫死的
  // 2/10，見該檔檔頭說明），再送 validatePreset() 收斂到「伺服器也認可」的實際最大值——本地估算
  // 可能因為後台調過成本係數、或本次編輯已經吃掉其他素質的預算而偏高：若伺服器判定
  // stat_over_budget／stat_over_cap，該素質 −1 再驗一次，最多重試 8 次；8 次都不合法就整個放棄、
  // 完全不動這個素質（不留下已知不合法的草稿），並提示使用者。
  async function maxStat(key: RpgStatKey) {
    if (!result || maxing) return
    const original = stats[key]
    let candidate = estimateMaxStatValue(original, result.stat_points_free, result.stat_cap)
    if (candidate <= original) return
    setMaxing(key)
    try {
      for (let attempt = 0; attempt < 8; attempt++) {
        const r = await validateDraft(level, { ...stats, [key]: candidate }, skillLevels, equipmentIds, strategyId)
        if (!r) { showErr('Max 試算失敗，請稍後再試'); return } // 網路/伺服器錯誤，不是配置不合法，不該用「-1 再試」硬猜
        const bad = (r.errors ?? []).some((e) => e.code === 'stat_over_budget' || (e.code === 'stat_over_cap' && e.field === key))
        if (!bad) { setStat(key, candidate); return }
        candidate -= 1
        if (candidate <= original) break
      }
      showErr('目前沒有足夠可用點數再加點')
    } finally {
      setMaxing(null)
    }
  }
  // 還原：只回到「打開這個腳本編輯器當下」的原始數值（放棄本次未儲存的修改）——跟 CharacterScreen
  // 的「還原預設」語意不同：那邊是回真正的 RO initial_stat（靠專屬的 /rpg/stats/reset 端點做，會
  // 退點數）；腳本沒有對應端點，這裡沒有管道知道 initial_stat 的即時設定值，能做到且對使用者仍然
  // 有意義的只有「丟掉這次編輯，退回腳本原本存的樣子」，故用不同文案避免誤解成兩者相同。
  function revertStats() {
    setStats(preset.stats)
  }
  // 技能歸零：跟 CharacterScreen 的「全部重置」語意一致（技能下限就是 0，沒有 initial_stat 那種
  // 未知常數問題），直接全部設回 0 讓技能點整包退回。
  function resetSkills() {
    setSkillLevels({})
  }

  // +1/-1 直接本地改動即可（跟素質 +1 一樣，合不合法交給下一次 debounce validate 判定、
  // errorFor() 顯示紅字）；'max' 分支比照 maxStat 的收斂邏輯——本地估算只是用「目前這次 validate
  // 回應留下的 skill_points_free」樂觀相加，一樣可能因為使用者在同一次編輯裡先動過其他技能／
  // 素質而偏高，故同樣送 validatePreset() 收斂：若回報 skill_over_budget，該技能 −1 再驗一次，
  // 最多重試 8 次，仍不合法就整個放棄、完全不動這個技能。
  async function skillDelta(skill: SkillDTO, d: 1 | -1 | 'max') {
    const cur = skillLevels[skill.id] ?? 0
    if (d === 1) { setSkillLevels((s) => ({ ...s, [skill.id]: Math.min(skill.max_level, cur + 1) })); return }
    if (d === -1) { setSkillLevels((s) => ({ ...s, [skill.id]: Math.max(0, cur - 1) })); return }
    if (!result || maxing) return
    let candidate = Math.min(skill.max_level, cur + result.skill_points_free)
    if (candidate <= cur) return
    setMaxing(skill.id)
    try {
      for (let attempt = 0; attempt < 8; attempt++) {
        const trial = { ...skillLevels, [skill.id]: candidate }
        const r = await validateDraft(level, stats, trial, equipmentIds, strategyId)
        if (!r) { showErr('Max 試算失敗，請稍後再試'); return }
        const bad = (r.errors ?? []).some((e) => e.code === 'skill_over_budget')
        if (!bad) { setSkillLevels((s) => ({ ...s, [skill.id]: candidate })); return }
        candidate -= 1
        if (candidate <= cur) break
      }
      showErr('目前沒有足夠技能點再加點')
    } finally {
      setMaxing(null)
    }
  }

  async function doSave(mode: 'update' | 'create') {
    if (!name.trim()) { showErr('請輸入腳本名稱'); return }
    if (!result || !result.ok) { showErr('目前配置不合法，請先修正上面標紅的項目'); return }
    if (saving) return
    const body: PresetSaveBody = {
      companion_id: preset.companion_id, name: name.trim(), level, stats, skill_levels: skillLevels,
      equipment: equipmentIds, strategy_id: strategyId,
    }
    setSaving(true)
    try {
      if (mode === 'update') {
        await withUserAuth((t) => rpgTavernApi.updatePreset(t, preset.id, body))
        showFlash('已儲存腳本')
      } else {
        await withUserAuth((t) => rpgTavernApi.createPreset(t, body))
        showFlash('已另存新腳本')
      }
      await onSaved()
    } catch (e: any) {
      showErr(friendlyErr(e, PRESET_ERR_LABEL, '儲存失敗，請稍後再試'))
      setSaving(false)
    }
  }

  async function doDelete() {
    if (isSystem || saving) return
    if (!window.confirm(`確定刪除腳本「${preset.name}」？若隊伍正在使用會自動退回系統預設，此動作無法復原。`)) return
    setSaving(true)
    try {
      await withUserAuth((t) => rpgTavernApi.deletePreset(t, preset.id))
      showFlash('已刪除腳本')
      await onSaved()
    } catch (e: any) {
      showErr(friendlyErr(e, PRESET_ERR_LABEL, '刪除失敗，請稍後再試'))
      setSaving(false)
    }
  }

  const canSubmit = !!result?.ok && !!name.trim() && !saving

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回酒館</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 20, fontWeight: 800, color: 'var(--tx)' }}>
          {jobEmoji(merc.job.id)} 編輯腳本・{merc.name}
        </h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{isSystem ? '系統預設（唯讀，可另存新腳本）' : '自訂腳本'}</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '14px 18px 28px' }}>
        {actionErr && <div style={errBanner}>{actionErr}</div>}
        {flash && <div style={okBanner}>✓ {flash}</div>}

        {/* ---- 名稱／等級 ---- */}
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <div style={{ flex: 1, minWidth: 160 }}>
            <div style={fieldLabel}>腳本名稱</div>
            <input type="text" value={name} onChange={(e) => setName(e.target.value)} style={{ ...numInput, width: '100%' }} placeholder="例如：輸出優先" />
          </div>
          <div>
            <div style={fieldLabel}>等級（1–99）</div>
            <input
              type="number" min={1} max={99} inputMode="numeric"
              value={levelInput}
              onChange={(e) => setLevelInput(e.target.value)}
              style={numInput}
            />
          </div>
        </div>

        {/* ---- 衍生數值預覽 ---- */}
        <h2 style={{ ...sectionTitle, margin: '20px 0 8px' }}>衍生數值預覽</h2>
        {result ? (
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
            {PRESET_DERIVED_META.map((d) => (
              <div key={d.key} style={derivedCell}>
                <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>{d.label}</div>
                <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>
                  {fmtNum(result.derived[d.key] as number)}{d.key === 'crit_pct' ? '%' : ''}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <Hint>計算中…</Hint>
        )}
        {result && !result.ok && (
          <div style={{ ...errBanner, marginTop: 10 }}>目前配置不合法，請修正下面標紅的項目後再儲存</div>
        )}

        {/* ---- 素質配點 ---- */}
        <div style={{ marginTop: 20, display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', flexWrap: 'wrap', gap: 6 }}>
          <h2 style={sectionTitle}>基本素質</h2>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
            {result && <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--gold)' }}>可配點數 {result.stat_points_free}／{result.stat_points_total}</span>}
            <button onClick={revertStats} style={dangerGhostBtn}>還原本次修改</button>
          </div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
          {STAT_META.map((m) => {
            const val = stats[m.key] ?? 0
            const cap = result?.stat_cap ?? val
            const atCap = val >= cap
            // maxing 非 null 時鎖住全部素質的 +1/Max：Max 收斂迴圈（見 maxStat）用 async 迴圈讀取
            // 目前的 stats 當基準，收斂期間若使用者又動了別的素質，收斂完成時可能覆蓋掉使用者剛做
            // 的修改，直接鎖住比較單純、使用者體感也就是「Max 正在算，稍等一下」。
            const canAdd = !!result && !atCap && !saving && !maxing
            const fieldErrMsg = errorFor(m.key)
            return (
              <div key={m.key} style={statRow}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
                    <span style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>{m.label}</span>
                    <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{m.abbr}</span>
                  </div>
                  {fieldErrMsg ? (
                    <div style={fieldErr}>{fieldErrMsg}</div>
                  ) : (
                    <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginTop: 2, lineHeight: 1.4 }}>{m.short}</div>
                  )}
                </div>
                <div style={{ flexShrink: 0, textAlign: 'center', width: 56 }}>
                  <div style={{ fontSize: 20, fontWeight: 900, color: 'var(--fug)', fontVariantNumeric: 'tabular-nums' }}>{val}</div>
                  <div style={{ fontSize: 9.5, color: 'var(--tx-faint)', fontVariantNumeric: 'tabular-nums' }}>／{cap} 上限</div>
                </div>
                <div style={{ display: 'flex', gap: 4, flexShrink: 0 }}>
                  <button disabled={!canAdd} onClick={() => bumpStat(m.key)} style={{ ...plusBtn, opacity: canAdd ? 1 : 0.4, cursor: canAdd ? 'pointer' : 'default' }}>+1</button>
                  <button disabled={!canAdd} onClick={() => maxStat(m.key)} style={{ ...plusBtn, opacity: canAdd ? 1 : 0.4, cursor: canAdd ? 'pointer' : 'default' }}>Max</button>
                </div>
              </div>
            )
          })}
        </div>

        {/* ---- 技能 ---- */}
        <div style={{ marginTop: 24 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', flexWrap: 'wrap', gap: 6 }}>
            <h2 style={sectionTitle}>技能</h2>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 10 }}>
              {result && <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--gold)' }}>技能點 {result.skill_points_free}／{result.skill_points_total}</span>}
              <button onClick={resetSkills} disabled={saving} style={dangerGhostBtn}>全部歸零</button>
            </div>
          </div>
          {result ? (
            // maxing 非 null 時鎖住技能按鈕，理由同素質那邊的 canAdd 註解——收斂迴圈期間避免使用者
            // 再動別的技能造成 race。
            <PresetSkillPaths job={merc.job} skills={result.skills} disabled={saving || !!maxing} onDelta={skillDelta} />
          ) : (
            <Hint>計算中…</Hint>
          )}
        </div>

        {/* ---- DORPG P9：裝備（契約 §1/§5、WIRE §5）——重用會員裝備頁的共用元件 EquipmentPanel，
             清單來自 GET /rpg/tavern/gear（依職業＋草稿等級），選擇結果只改本地草稿，存檔時才整包
             送出（見上面 equipmentIds/onEquipSlot）。 ---- */}
        <div style={{ marginTop: 24 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', flexWrap: 'wrap', gap: 6 }}>
            <h2 style={sectionTitle}>裝備</h2>
            {result && formatEquipBonus(result.equip_bonus) && (
              <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>{formatEquipBonus(result.equip_bonus)}</span>
            )}
          </div>
          {gearErr && <Hint>{gearErr}</Hint>}
          {!gear && !gearErr && <Hint>載入裝備清單中…</Hint>}
          {gear && (
            <EquipmentPanel
              equipped={equippedForPanel}
              weaponTypes={gear.weapon_types}
              weapons={weaponsForPanel}
              armorItems={armorForPanel}
              level={level}
              jobId={merc.job.id}
              busy={null}
              onEquip={onEquipSlot}
              errors={slotErrors}
              disabled={saving}
            />
          )}
        </div>

        {/* ---- DORPG P9：AI 策略（契約 §1/§4、WIRE §REST）——名稱／說明一律吃 GET /rpg/tavern 回傳
             的 strategies（後台可調文案），不用本地固定表。 ---- */}
        <div style={{ marginTop: 24 }}>
          <h2 style={sectionTitle}>AI 策略</h2>
          <select
            value={strategyId}
            disabled={saving}
            onChange={(e) => setStrategyId(e.target.value)}
            style={{ ...presetSelect, marginTop: 8 }}
          >
            {strategies.length === 0 && <option value={strategyId}>{strategyId}</option>}
            {strategies.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          {strategyErrText ? (
            <div style={fieldErr}>{strategyErrText}</div>
          ) : (
            (() => {
              const desc = strategies.find((s) => s.id === strategyId)?.description
              return desc ? <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.4 }}>{desc}</div> : null
            })()
          )}
        </div>

        {/* ---- 儲存／另存／刪除 ---- */}
        <div style={{ display: 'flex', gap: 8, marginTop: 26, flexWrap: 'wrap' }}>
          {!isSystem && (
            <button onClick={() => doSave('update')} disabled={!canSubmit} style={{ ...battleBtn, opacity: canSubmit ? 1 : 0.5 }}>
              {saving ? '儲存中…' : '儲存'}
            </button>
          )}
          <button onClick={() => doSave('create')} disabled={!canSubmit} style={{ ...ghostActionBtn(!canSubmit) }}>
            {saving ? '處理中…' : '另存新腳本'}
          </button>
          {!isSystem && (
            <button onClick={doDelete} disabled={saving} style={dangerGhostBtn}>刪除腳本</button>
          )}
        </div>
      </div>
    </div>
  )
}

// 依路線（a/b）分兩欄，跟 CharacterScreen.tsx 的 SkillPaths 佈局一致（契約 §5：每職業 10 個＝路線 A
// 5 個＋路線 B 5 個）；不重用該檔的元件是因為那邊操作對象是「已持久化的玩家技能」，這裡是「草稿」，
// 兩者的 onDelta 語意（打 API vs 純本地 state）不同，硬共用反而要塞一堆條件分支。
// DORPG P10（CONTRACT §1/§4）：路線改依 job.paths 陣列通用渲染（重騎士額外有 key='c' 的「守護」
// 路線），沒有 path_c 的職業畫面不變——job.paths 對這些職業本來就只有兩個元素。
function PresetSkillPaths({ job, skills, disabled, onDelta }: {
  job: JobDTO
  skills: SkillDTO[]
  disabled: boolean
  onDelta: (skill: SkillDTO, d: 1 | -1 | 'max') => void
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18, marginTop: 10 }}>
      {job.paths.map((p) => {
        const list = skills.filter((s) => s.path === p.key).sort((a, b) => a.tier - b.tier)
        return (
          <div key={p.key}>
            <div style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{p.name}</div>
            {p.desc && <div style={{ fontSize: 10.5, color: 'var(--tx-dim)', marginTop: 1, marginBottom: 8, lineHeight: 1.4 }}>{p.desc}</div>}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {list.map((sk) => (
                <PresetSkillRow key={sk.id} skill={sk} allSkills={skills} disabled={disabled} onDelta={(d) => onDelta(sk, d)} />
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function PresetSkillRow({ skill, allSkills, disabled, onDelta }: {
  skill: SkillDTO
  allSkills: SkillDTO[]
  disabled: boolean
  onDelta: (d: 1 | -1 | 'max') => void
}) {
  const prereq = skill.prereq_skill_id ? allSkills.find((s) => s.id === skill.prereq_skill_id) : null
  const canMinus = skill.level > 0 && !disabled
  const canPlus = skill.can_level_up && !disabled
  const kindLabel = SKILL_KIND_LABEL[skill.kind] ?? skill.kind
  // DORPG P10（CONTRACT §3/§4/§5）：kind='taunt' 改讀 skill.taunt（持續秒數／減傷／是否拉怪），
  // 沒有下一級預覽欄位可顯示，同 CharacterScreen.tsx EffectPreview 的既有取捨。
  const effectText = skill.kind === 'taunt' ? formatTauntEffect(skill.taunt) : formatEffectAtLevel(skill.effect_at_level)
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
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 2 }}>
        {skill.level === 0 ? 'Lv1 效果預覽：' : `Lv${skill.level} 效果：`}{effectText}
      </div>
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

// ============================== 共用小元件／樣式 ==============================

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
const fieldErr: React.CSSProperties = { fontSize: 10.5, color: '#f4623a', marginTop: 2, lineHeight: 1.4 }
const fieldLabel: React.CSSProperties = { fontSize: 10.5, color: 'var(--tx-dim)', marginBottom: 4 }
const numInput: React.CSSProperties = { width: 76, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 8, padding: '7px 10px', fontSize: 13, color: 'var(--tx)', fontFamily: 'inherit', boxSizing: 'border-box' }
function ghostActionBtn(disabled: boolean): React.CSSProperties {
  return { background: 'var(--bg-1)', border: '1px solid var(--line)', color: disabled ? 'var(--tx-faint)' : 'var(--tx)', borderRadius: 8, padding: '7px 12px', fontSize: 12.5, fontWeight: 700, fontFamily: 'inherit', cursor: disabled ? 'default' : 'pointer' }
}
const dangerGhostBtn: React.CSSProperties = { background: 'none', border: '1px solid rgba(244,98,58,.4)', color: '#f4623a', borderRadius: 8, padding: '4px 10px', fontSize: 11, fontWeight: 700, fontFamily: 'inherit', cursor: 'pointer' }
const skillRowStyle: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }
const kindTagStyle: React.CSSProperties = { fontSize: 9.5, fontWeight: 700, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 999, padding: '2px 7px', whiteSpace: 'nowrap' }
const unimplTagStyle: React.CSSProperties = { fontSize: 9.5, fontWeight: 700, color: '#f4623a', background: 'rgba(244,98,58,.14)', borderRadius: 999, padding: '2px 7px', whiteSpace: 'nowrap' }
function smallBtn(enabled: boolean): React.CSSProperties {
  return { ...plusBtn, minWidth: 30, opacity: enabled ? 1 : 0.35, cursor: enabled ? 'pointer' : 'default' }
}
const leaderRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--gold)', borderRadius: 12, padding: '10px 12px' }
const slotRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }
const mercCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }
const portraitThumb: React.CSSProperties = { width: 36, height: 36, borderRadius: 8, objectFit: 'cover', flexShrink: 0, background: 'var(--bg-2)' }
const portraitThumbLg: React.CSSProperties = { width: 48, height: 48, borderRadius: 10, objectFit: 'cover', flexShrink: 0, background: 'var(--bg-2)' }
// 金底白字（全站通則）：「隊伍中」標籤與「加入隊伍」鈕都是金底實心，文字強制白色。
const inPartyTag: React.CSSProperties = { fontSize: 9.5, fontWeight: 700, color: '#fff', background: 'var(--gold)', borderRadius: 999, padding: '2px 7px' }
const presetSelect: React.CSSProperties = { flex: 1, minWidth: 140, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 8, padding: '7px 8px', fontSize: 12.5, color: 'var(--tx)', fontFamily: 'inherit' }
const joinBtn: React.CSSProperties = { background: 'var(--gold)', color: '#fff', border: 'none', borderRadius: 8, padding: '7px 12px', fontSize: 12.5, fontWeight: 800, fontFamily: 'inherit', cursor: 'pointer' }
