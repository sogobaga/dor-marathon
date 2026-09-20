// 遊戲化角色數值（RO 素質系統）共用中文標籤／說明 — CharacterScreen（會員）與 admin/rpg（後台）
// 兩處共用，避免文案各自維護、之後改一次兩邊同步。數字係數本身一律吃 RpgConfig／預覽 API 算出的
// 結果，本檔只放「怎麼顯示」，不放任何算式（算式在後端 internal/rpg，前端不重算，見任務決策 D2）。
import type {
  ArmorItemSlot, ArmorProfile, EffectAtLevel, EquipBonusDTO, EquipmentSlot, JobDTO, RpgConfig, RpgDerived, RpgElement,
  RpgRank, RpgStatKey, SkillKind, WeaponProfile, WeaponRarity,
} from './api'

export interface StatMeta {
  key: RpgStatKey
  label: string  // 中文全名，如「力量」
  abbr: string   // RO 縮寫，如「STR」
  short: string  // 手機版一行說明（面板用，375px 安全）
  help: string   // 後台/詳細說明用完整規則
}

export const STAT_META: StatMeta[] = [
  {
    key: 'str', label: '力量', abbr: 'STR',
    short: '近戰物攻+1、裝備物攻+0.5%；負重+30（每點）',
    help: '每 1 點：裝備近距離武器時素質物理攻擊力+1、裝備物理攻擊力+0.5%；負重量+30。每 5 點：裝備遠距離武器時素質物理攻擊力+1。',
  },
  {
    key: 'agi', label: '敏捷', abbr: 'AGI',
    short: '迴避率+1、攻速提升、抗性↑（每點）',
    help: '每 1 點：迴避率+1、攻擊速度增加、狀態抗性增加［出血］［睡眠］［著火］。每 5 點：物理防禦力+1。',
  },
  {
    key: 'vit', label: '體質', abbr: 'VIT',
    short: '最大HP+1%、道具HP恢復+2%（每點）',
    help: '每 1 點：最大HP+1%、道具HP恢復量+2%、狀態抗性增加［暈眩］［中毒］。每 2 點：物理防禦力+1。每 5 點：魔法防禦力+1、HP自然恢復量+1。每 200 最大HP：HP自然恢復量+1。',
  },
  {
    key: 'dex', label: '靈巧', abbr: 'DEX',
    short: '遠程物攻+1、命中+1、詠唱縮短（每點）',
    help: '每 1 點：裝備遠距離武器時素質物理攻擊力+1、裝備物理攻擊力+0.5%；命中率+1；變動詠唱時間減少；攻擊速度稍微增加。每 5 點：裝備近距離武器時素質物理攻擊力+1；魔法攻擊力+1；魔法防禦力+1。',
  },
  {
    key: 'int', label: '智力', abbr: 'INT',
    short: '魔攻+1.5、魔防+1、最大MP+1%（每點）',
    help: '每 1 點：魔法攻擊力+1.5、魔法防禦力+1、最大MP+1%、道具MP恢復量+1%、變動詠唱時間減少（為DEX效果一半）、狀態抗性增加［黑暗］［沉默］［恐怖］。每 6 點：MP自然恢復量+1。達到 120 時：MP自然恢復量+4，之後每 2 點再+1。每 100 最大MP：MP自然恢復量+1。',
  },
  {
    key: 'luk', label: '幸運', abbr: 'LUK',
    short: '暴擊率+0.3、抗性↑（每點）',
    help: '每 1 點：暴擊率+0.3、狀態抗性增加［詛咒］［混亂］［恐怖］［著火］。每 3 點：物理攻擊力+1、魔法攻擊力+1、命中率+1。每 5 點：迴避率+1、暴擊迴避率+1。每 10 點：完全迴避+1。',
  },
]

export interface DerivedMeta { key: keyof RpgDerived; label: string; abbr: string; suffix?: string }
export const DERIVED_META: DerivedMeta[] = [
  { key: 'atk', label: '物理攻擊力', abbr: 'Atk' },
  { key: 'def', label: '物理防禦力', abbr: 'Def' },
  { key: 'matk', label: '魔法攻擊力', abbr: 'Matk' },
  { key: 'mdef', label: '魔法防禦力', abbr: 'Mdef' },
  { key: 'hit', label: '命中率', abbr: 'Hit' },
  { key: 'flee', label: '迴避率', abbr: 'Flee' },
  { key: 'perfect_dodge', label: '完全迴避', abbr: 'Perfect Dodge' },
  { key: 'crit_pct', label: '暴擊率', abbr: 'Critical', suffix: '%' },
  { key: 'crit_shield', label: '暴擊迴避率', abbr: 'Crit Shield', suffix: '%' },
  { key: 'aspd', label: '攻擊速度', abbr: 'Aspd' },
  { key: 'weight', label: '最大負重', abbr: 'Weight' },
  { key: 'hp_regen', label: 'HP 自然恢復', abbr: 'HP Regen' },
  { key: 'mp_regen', label: 'MP 自然恢復', abbr: 'MP Regen' },
  { key: 'cast_reduction_pct', label: '詠唱縮減', abbr: 'Cast-', suffix: '%' },
]

// resists 的 key 名稱由後端決定（狀態異常代碼）；這裡對照 owner 提供表格中出現過的異常，未對到的
// key 原樣顯示（不會擋渲染）。
export const RESIST_LABEL: Record<string, string> = {
  bleed: '出血', sleep: '睡眠', burn: '著火', stun: '暈眩', poison: '中毒',
  dark: '黑暗', silence: '沉默', fear: '恐怖', curse: '詛咒', confusion: '混亂',
}
export function resistLabel(key: string): string { return RESIST_LABEL[key] ?? key }

// ---------------------------------------------------------------------------
// DORPG P5（職業／測試等級／配點技能規則）：職業卡片裝飾用 emoji（純視覺，非後端資料——JobDTO
// 本身已含 name/tagline/description，這裡只補一個好認的圖示，找不到 id 時退回通用圖示）。
export const JOB_EMOJI: Record<string, string> = {
  light_knight: '🗡️',
  archer: '🏹',
  heavy_knight: '🛡️',
  cleric: '✨',
  merchant: '💰',
  mage: '🔮',
}
export function jobEmoji(jobId: string | null | undefined): string {
  return (jobId && JOB_EMOJI[jobId]) || '⚔️'
}

// 技能 kind 中文標籤（見契約 §5 七種詞彙；special＝本輪引擎未實裝，UI 上直接顯示這個字樣而非「特殊」）。
// DORPG P10（CONTRACT §5）新增 taunt→「守護」（重騎士守護系列：挑釁／守護姿態）。
export const SKILL_KIND_LABEL: Record<SkillKind, string> = {
  damage: '傷害',
  heal: '治療',
  shield: '護盾',
  buff: '增益',
  debuff: '減益',
  passive: '被動',
  special: '尚未實裝',
  taunt: '守護',
}

// buff/debuff/passive 的 stat 詞彙中文標籤（見契約 §5 三個 kind 各自的 stat 集合聯集）。
export const SKILL_STAT_LABEL: Record<string, string> = {
  atk_pct: '物攻', matk_pct: '魔攻', def_pct: '物防', mdef_pct: '魔防',
  aspd: '攻速', crit_pct: '暴擊率', crit_dmg_pct: '暴擊傷害', flee: '迴避', hit: '命中',
  hp_regen_pct: 'HP恢復', damage_taken_pct: '受到傷害', hp_max_pct: '最大HP', mp_max_pct: '最大MP',
  perfect_dodge: '完全迴避',
}
export function skillStatLabel(stat: string): string { return SKILL_STAT_LABEL[stat] ?? stat }

/**
 * 把 EffectAtLevel（已依技能等級展開的即時數值）組成一句人看得懂的效果描述，供 CharacterScreen
 * 「目前效果」／「下一級預覽」共用同一顆函式（見契約 §5 各 kind 欄位集合）。純顯示用途，不做任何
 * 戰鬥判斷——實際戰鬥數值一律以 bootstrap 送來的 effect 為準（本函式不參與戰鬥計算）。
 */
export function formatEffectAtLevel(e: EffectAtLevel | null | undefined): string {
  if (!e) return ''
  const targetLabel: Record<string, string> = { enemy: '單體敵人', allEnemies: '全體敵人', self: '自己', ally: '單一隊友', allAllies: '全體隊友' }
  switch (e.kind) {
    case 'damage': {
      const parts = [`威力係數 ${fmtCoef(e.coef)}`]
      if (e.flat) parts.push(`固定 +${fmtCoef(e.flat)}`)
      if (e.hits && e.hits > 1) parts.push(`${e.hits} 段`)
      parts.push(targetLabel[e.target] ?? e.target)
      return parts.join('・')
    }
    case 'heal': {
      const parts = [`治療係數 ${fmtCoef(e.coef)}`]
      if (e.flat) parts.push(`固定 +${fmtCoef(e.flat)}`)
      parts.push(targetLabel[e.target] ?? e.target)
      return parts.join('・')
    }
    case 'shield':
      return `護盾量 ${fmtCoef(e.flat)}${e.duration_ms ? `・持續 ${(e.duration_ms / 1000).toFixed(1)}s` : ''}`
    case 'buff':
    case 'debuff': {
      const sign = (e.value ?? 0) >= 0 ? '+' : ''
      return `${skillStatLabel(e.stat ?? '')} ${sign}${fmtCoef(e.value)}${e.duration_ms ? `・持續 ${(e.duration_ms / 1000).toFixed(1)}s` : ''}・${targetLabel[e.target] ?? e.target}`
    }
    case 'passive': {
      const sign = (e.value ?? 0) >= 0 ? '+' : ''
      // DORPG P10：guard_taunt=true（目前只有「守護本能」hk_c2）額外註明「按防禦即進入守護狀態」，
      // 這是這顆被動唯一會影響戰鬥規則（而不只是數值加成）的地方，值得跟數值放在同一行提醒玩家。
      const guardNote = e.guard_taunt ? '・按防禦即進入守護狀態' : ''
      return `${skillStatLabel(e.stat ?? '')} ${sign}${fmtCoef(e.value)}（常駐）${guardNote}`
    }
    case 'taunt':
      // DORPG P10：taunt 的持續時間／減傷／是否拉怪不在這個扁平結構裡（WIRE 明講是額外的
      // SkillDTO.taunt 物件），呼叫端（CharacterScreen/TavernScreen 的技能列）應改呼叫下面的
      // formatTauntEffect(skill.taunt) 取代這支；這裡只當直接誤呼叫 formatEffectAtLevel(taunt 技能)
      // 時的保底文字，不會出現在正常畫面上。
      return '施放後進入守護狀態（效果見下方）'
    case 'special':
    default:
      return '本輪尚未實裝數值'
  }
}

/**
 * DORPG P10（CONTRACT §3/§4/§5）：taunt 技能（挑釁／守護姿態）的展開效果——獨立於上面
 * formatEffectAtLevel 之外（見 SkillDTO.taunt 型別註解，WIRE 明講是額外一個 taunt 物件，不是塞進
 * effect_at_level 既有欄位），CharacterScreen／TavernScreen 的技能列在 kind==='taunt' 時改呼叫
 * 這支，取代 formatEffectAtLevel(skill.effect_at_level)。
 */
export function formatTauntEffect(t: { duration_ms: number; damage_taken_pct: number; retarget: boolean } | null | undefined): string {
  if (!t) return ''
  const parts = [`持續 ${(t.duration_ms / 1000).toFixed(1)}s`]
  if (t.damage_taken_pct) {
    const sign = t.damage_taken_pct >= 0 ? '+' : ''
    parts.push(`受到傷害 ${sign}${fmtCoef(t.damage_taken_pct)}%`)
  }
  parts.push(t.retarget ? '拉怪（敵人立刻改鎖自己）' : '不拉怪（僅影響之後選目標）')
  return parts.join('・')
}

/**
 * DORPG P10（CONTRACT §1/§3/§5）：角色頁職業卡「職業特性：受到傷害 −15%」一行；目前唯一用得到
 * 的職業特性是 damage_taken_pct（重騎士 -15），沒有這個特性（值為 0/undefined）回傳 null，呼叫端
 * 據此決定要不要渲染這一行。
 */
export function jobTraitLine(traits: { damage_taken_pct?: number } | null | undefined): string | null {
  const v = traits?.damage_taken_pct
  if (!v) return null
  const sign = v > 0 ? '+' : '−'
  return `職業特性：受到傷害 ${sign}${Math.abs(v)}%`
}
function fmtCoef(n: number | undefined): string {
  if (n == null || isNaN(n)) return '0'
  return Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')
}

/** 六職業固定順序（依 sort_order 保底：後端已排序，這裡只在資料異常/尚未載入時當 fallback）。 */
export const JOB_ORDER: string[] = ['light_knight', 'archer', 'heavy_knight', 'cleric', 'merchant', 'mage']
export function sortJobs(jobs: JobDTO[]): JobDTO[] {
  return [...jobs].sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0) || JOB_ORDER.indexOf(a.id) - JOB_ORDER.indexOf(b.id))
}

// ---------------------------------------------------------------------------
// P3（AGI 攻速／DEX 詠唱縮減，審查 dorpg_p3 r5 使用者當面要求）：CharacterScreen 顯示用估算。
//
// GET /rpg/me 目前只回傳 derived（含 aspd 這個「評級數值」），沒有一併帶戰鬥 config（
// battle_attack_cooldown_ms 等係數）——「攻擊速度 150.6」這個原始評級數字對玩家沒有意義，
// 需要換算成「攻擊間隔 X 秒」才看得懂效果，但換算需要的係數只存在戰鬥 config，角色頁拿不到。
// 這裡鏡射 services/api/internal/rpg/config.go DefaultConfig() 與 engine/types.ts
// DEFAULT_BATTLE_CONFIG 的預設值，供角色頁在沒有實際 config 可用時做「預設值下的估算」。
// ⚠️ 若後台在「參數設定→戰鬥」分頁調過這幾個係數，這裡顯示的數字會跟真實戰鬥（吃後端
// bootstrap 送來的真實 config）不同步——這只是給玩家一個「配這麼多 AGI/DEX 大概有多少效果」
// 的大致感覺，不是精算值；要精算需要 /rpg/me 一併回傳這三個數字，或角色頁改打
// GET /rpg/battle/bootstrap 換取真實 config（兩者都不在本輪 FRONTEND 角色可寫清單內，見任務
// 回報建議）。公式與 engine/formulas.ts attackCooldownFor／effectiveCastMs 逐項一致，
// 後台調整這幾個係數的預設值時，這裡要同步修改，否則角色頁的估算會離題。
export const BATTLE_DISPLAY_DEFAULTS = {
  attackCooldownMs: 1500,
  aspdReference: 150,
  attackCooldownMinMs: 700,
  defaultCastMs: 500,
  castMinMs: 120,
}

/** 與 engine/formulas.ts attackCooldownFor 相同公式（毫秒）；僅供 CharacterScreen 顯示用途的預設值估算。 */
export function estimateAttackCooldownMs(aspd: number): number {
  const { attackCooldownMs, aspdReference, attackCooldownMinMs } = BATTLE_DISPLAY_DEFAULTS
  const denom = 200 - aspdReference
  if (denom <= 0) return attackCooldownMs // 防呆同 engine：aspdReference 誤設 ≥200 時不要除以零/負值
  const raw = (attackCooldownMs * (200 - aspd)) / denom
  return Math.min(attackCooldownMs, Math.max(attackCooldownMinMs, raw))
}

/** 與 engine/formulas.ts effectiveCastMs 相同公式（毫秒）；僅供 CharacterScreen 顯示用途的預設值估算。 */
export function estimateCastMs(baseCastMs: number, castReductionPct: number): number {
  const reduced = baseCastMs * (1 - castReductionPct / 100)
  return Math.max(BATTLE_DISPLAY_DEFAULTS.castMinMs, Math.round(reduced))
}

// ---------------------------------------------------------------------------
// DORPG P6（酒館腳本編輯器，見契約 dorpg_p6 CONTRACT.md §3.4）：TavernScreen 的「Max」鈕估算。
//
// 玩家角色頁的 Max 是打 POST /rpg/allocate {mode:"max"}，由伺服器反覆 +1 算到底（契約 dorpg_p5
// §3）。傭兵腳本沒有對應的「allocate」端點——腳本只有 POST /rpg/presets/validate 這個無副作用的
// 純試算端點，一次只回傳「這組完整配置合不合法」，不會幫你算「還能再加幾點」。跟上面
// BATTLE_DISPLAY_DEFAULTS／estimateAttackCooldownMs 同一個理由：TavernScreen 沒有管道拿到後台
// 「配點與技能點」分頁調過的 cost_base／cost_step_every 即時值（那是 RpgConfig，只有 admin token
// 讀得到），這裡鏡射契約 §3 文字寫死的公式與預設值 floor((n−1)/10)+2（n＝即將購買的第 n 點，等於
// 「目前值＋1」）算出「本地估算的 Max」——這只是第一步猜測，不是最終答案。
//
// TavernScreen.tsx 的 maxStat()／skillDelta('max') 真的會把這個估算值送去 validatePreset() 收斂
// （審查修正——舊版註解曾宣稱有這個機制但其實沒實作，現在補上）：若伺服器判定 stat_over_budget／
// stat_over_cap（技能則是 skill_over_budget），就把該項目 −1 再重送一次，最多重試 8 次；8 次都
// 不合法就整個放棄、退回原始值並提示使用者，不會把已知不合法的草稿留在畫面上。之所以需要這個收斂
// 迴圈：本地估算的成本公式是寫死的 2/10（見下面 PRESET_COST_DEFAULTS），一旦後台調過這兩個係數、
// 或其他素質/技能已經在同一次編輯裡吃掉部分預算，估算值就會偏高，必須靠伺服器再驗證一次才能確定
// 真正能加到的最大值。
export const PRESET_COST_DEFAULTS = { costBase: 2, costStepEvery: 10 }

/** 從 currentValue 加到 currentValue+1 這一點的估算花費（見上方檔頭說明，非精算）。 */
export function estimatePresetStatCost(currentValue: number): number {
  const n = currentValue + 1 // 即將購買的第 n 點
  return Math.floor((n - 1) / PRESET_COST_DEFAULTS.costStepEvery) + PRESET_COST_DEFAULTS.costBase
}

/** 在 [currentValue, cap] 內，本地估算「花完 freePoints 最多能加到多少」，供 Max 鈕先做一次樂觀
 *  計算（實際是否合法仍以呼叫端接著打的 validatePreset() 為準，見上方檔頭說明）。 */
export function estimateMaxStatValue(currentValue: number, freePoints: number, cap: number): number {
  let v = currentValue
  let free = freePoints
  while (v < cap) {
    const cost = estimatePresetStatCost(v)
    if (cost > free) break
    free -= cost
    v += 1
  }
  return v
}

// 後台「參數設定」分頁分組欄位 — 逐項對照 RpgConfig 欄位（勿漏改名）。type 省略＝number 輸入框；
// 'json' 是 P2 新增（battle_element_chart 巢狀 map 專用），admin/rpg/page.tsx 的 ConfigTab 用它
// 決定渲染 textarea 而非 <input type="number">，存檔前另外做 JSON.parse 驗證。
// 審查#2 新增 'checkbox'：布林欄位（目前只有 test_level_enabled）用勾選框呈現，見
// admin/rpg/page.tsx ConfigTab 的渲染分支與 buildConfig() 的型別轉換。
export interface ConfigField { key: keyof RpgConfig; label: string; help?: string; type?: 'select' | 'json' | 'checkbox'; options?: { value: string; label: string }[]; step?: number }
export interface ConfigGroup { title: string; fields: ConfigField[] }

export const CONFIG_GROUPS: ConfigGroup[] = [
  {
    title: '基礎規則',
    fields: [
      { key: 'initial_stat', label: '素質初始值' },
      { key: 'initial_free_points', label: '初始可配置點數' },
      { key: 'max_stat', label: '單項素質上限' },
      { key: 'cost_base', label: '配點基礎花費 base_cost' },
      { key: 'cost_step_every', label: '花費每 N 點遞增一次' },
      { key: 'default_weapon_type', label: '預設武器類型（本階段無裝備，用於判定近戰/遠程加成）', type: 'select', options: [{ value: 'melee', label: '近距離' }, { value: 'ranged', label: '遠距離' }] },
    ],
  },
  // DORPG P5：配點/技能點公式改為可調（契約 §3/§4）。initial_stat/initial_free_points/max_stat/
  // cost_base/cost_step_every 仍是上面「基礎規則」既有欄位（單項素質上限、加點成本 floor((n-1)/cost_step_every)+cost_base），
  // 這裡新增的是「總點數隨等級成長」的公式係數，兩組欄位配合才是完整的配點規則。
  {
    title: '配點與技能點（P5）',
    fields: [
      {
        key: 'stat_points_initial',
        label: '配點總數公式初始值：TotalStatPoints(1)（契約預設 48，對齊 RO pre-re statpoint.yml）',
      },
      { key: 'stat_points_step_levels', label: '配點總數公式：每幾級 ⌊(k−1)/此值⌋ 遞增一階（契約預設 5）' },
      { key: 'stat_points_per_level_base', label: '配點總數公式：每級基礎點數（契約預設 3；Lv27 應得 186 點、Lv99 應得 1273 點）' },
      { key: 'skill_points_initial', label: '技能點總數公式初始值：TotalSkillPoints(1)（契約預設 0）' },
      { key: 'skill_points_per_level', label: '技能點總數公式：每級技能點（契約預設 1；Lv27 應得 26 點）' },
      {
        key: 'test_level_enabled', type: 'checkbox',
        label: '測試等級功能開關（正式上線前關閉）——關閉後 PUT /rpg/test-level 一律 403，且既有 test_level 也會被忽略（不是只擋新的設定請求）',
      },
    ],
  },
  {
    title: 'HP／MP',
    fields: [
      { key: 'base_hp', label: '基礎 HP' },
      { key: 'hp_per_base_level', label: '每基本等級 HP' },
      { key: 'base_mp', label: '基礎 MP' },
      { key: 'mp_per_base_level', label: '每基本等級 MP' },
    ],
  },
  {
    title: '攻速／負重',
    fields: [
      { key: 'aspd_base', label: '攻速基礎值' },
      { key: 'aspd_per_agi', label: '每 1 AGI 攻速加成' },
      { key: 'aspd_per_dex', label: '每 1 DEX 攻速加成' },
      { key: 'aspd_cap', label: '攻速上限' },
      { key: 'weight_base', label: '基礎負重' },
      { key: 'weight_per_str', label: '每 1 STR 負重加成' },
    ],
  },
  {
    title: '力量 STR',
    fields: [
      { key: 'str_melee_atk', label: '每 1 點：近戰素質物攻' },
      { key: 'str_equip_atk_pct', label: '每 1 點：裝備物攻 %' },
      { key: 'str_ranged_atk_per', label: '每幾點：遠程素質物攻+1（點數門檻）' },
      {
        key: 'str_tier_coef',
        label: '（P5）整十階梯係數：物攻 += ⌊STR/10⌋² × 此值（全職業統一，弓箭手也吃 STR，已知簡化）',
      },
    ],
  },
  {
    title: '敏捷 AGI',
    fields: [
      { key: 'agi_flee', label: '每 1 點：迴避率' },
      { key: 'agi_def_per', label: '每幾點：物防+1（點數門檻）' },
    ],
  },
  {
    title: '體質 VIT',
    fields: [
      { key: 'vit_hp_pct', label: '每 1 點：最大HP %' },
      { key: 'vit_item_hp_pct', label: '每 1 點：道具HP恢復 %' },
      { key: 'vit_def_per', label: '每幾點：物防+1（點數門檻）' },
      { key: 'vit_mdef_per', label: '每幾點：魔防+1（點數門檻）' },
      { key: 'vit_hp_regen_per', label: '每幾點：HP自然恢復+1（點數門檻）' },
      { key: 'hp_regen_per_max_hp', label: '每多少最大HP：HP自然恢復+1' },
    ],
  },
  {
    title: '靈巧 DEX',
    fields: [
      { key: 'dex_ranged_atk', label: '每 1 點：遠程素質物攻' },
      { key: 'dex_equip_atk_pct', label: '每 1 點：裝備物攻 %' },
      { key: 'dex_hit', label: '每 1 點：命中率' },
      { key: 'dex_melee_atk_per', label: '每幾點：近戰素質物攻+1（點數門檻）' },
      { key: 'dex_matk_per', label: '每幾點：魔攻+1（點數門檻）' },
      { key: 'dex_mdef_per', label: '每幾點：魔防+1（點數門檻）' },
    ],
  },
  {
    title: '智力 INT',
    fields: [
      { key: 'int_matk', label: '每 1 點：魔法攻擊力' },
      { key: 'int_mdef', label: '每 1 點：魔法防禦力' },
      { key: 'int_mp_pct', label: '每 1 點：最大MP %' },
      { key: 'int_item_mp_pct', label: '每 1 點：道具MP恢復 %' },
      { key: 'int_mp_regen_per', label: '每幾點：MP自然恢復+1（點數門檻）' },
      { key: 'int_mp_regen_at_120', label: '達到 120 時：MP自然恢復額外值' },
      { key: 'int_mp_regen_per_after_120', label: '達 120 後每幾點：MP自然恢復+1' },
      { key: 'mp_regen_per_max_mp', label: '每多少最大MP：MP自然恢復+1' },
      { key: 'int_tier_coef', label: '（P5）整十階梯係數：魔攻 += ⌊INT/10⌋² × 此值' },
    ],
  },
  {
    title: '幸運 LUK',
    fields: [
      { key: 'luk_crit', label: '每 1 點：暴擊率' },
      { key: 'luk_atk_per', label: '每幾點：物攻+1（點數門檻）' },
      { key: 'luk_matk_per', label: '每幾點：魔攻+1（點數門檻）' },
      { key: 'luk_hit_per', label: '每幾點：命中+1（點數門檻）' },
      { key: 'luk_flee_per', label: '每幾點：迴避+1（點數門檻）' },
      { key: 'luk_crit_shield_per', label: '每幾點：暴擊迴避+1（點數門檻）' },
      { key: 'luk_perfect_dodge_per', label: '每幾點：完全迴避+1（點數門檻）' },
    ],
  },
  {
    title: '基本等級加成',
    fields: [
      { key: 'lv_hit', label: '每 1 級：命中率' },
      { key: 'lv_flee', label: '每 1 級：迴避率' },
      {
        key: 'lv_def_per',
        label: '每幾級：物防+1（等級門檻，舊版係數——P5 新增下面三個欄位取代等級對「防禦」的線性公式，此欄若已被取代可忽略，以 BACKEND 實際仍在用哪個為準）',
      },
      { key: 'lv_atk_per', label: '每幾級：物攻+1（等級門檻）' },
      { key: 'lv_matk_per', label: '每幾級：魔攻+1（等級門檻）' },
      { key: 'lv_mdef_per', label: '每幾級：魔防+1（等級門檻）' },
      {
        key: 'lv_def_breakpoint',
        label: '（P5）等級防禦二段線性的轉折等級：Lv≤此值走「低段每級」、Lv>此值走「高段每級」（契約預設 50，取代現行 floor(L/2)）',
      },
      { key: 'lv_def_per_low', label: '（P5）轉折等級（含）以下，每級防禦加成（契約預設 0.5）' },
      { key: 'lv_def_per_high', label: '（P5）轉折等級以上，每級防禦加成（契約預設 0.35）' },
    ],
  },
  {
    title: '詠唱縮減／狀態抗性／迴避上限',
    fields: [
      { key: 'cast_unit_dex', label: '每 1 DEX 對應詠唱縮減單位' },
      { key: 'cast_unit_int', label: '每 1 INT 對應詠唱縮減單位' },
      { key: 'cast_pct_per_unit', label: '每單位縮減 %' },
      { key: 'cast_cap_pct', label: '詠唱縮減上限 %' },
      { key: 'resist_pct_per_point', label: '每點狀態抗性 %（AGI/VIT/INT/LUK 共用係數）' },
      { key: 'flee_cap_pct', label: '迴避率上限 %（完全迴避不設上限）' },
    ],
  },
  // DORPG P2：戰鬥內容/縮放/手感參數（契約 dorpg_p2 §3.1、§1 D2）。這批影響「怪很不很硬、多快
  // 打死玩家」，調參前建議先看「戰鬥數據」分頁的實測勝率/時長再回來改這裡（BALANCE 報告見
  // scratchpad/dorpg_p2/BALANCE.md 的建議值與理由）。
  {
    title: '戰鬥',
    fields: [
      {
        key: 'battle_scale_mode', label: '怪物數值縮放模式', type: 'select',
        options: [
          { value: 'level', label: 'level（依遭遇 monster_level 用 RefPlayer(N) 算絕對值，P6 起預設）' },
          { value: 'power', label: 'power（以玩家戰力動態縮放，P2 實作，P6 起保留可切回）' },
          { value: 'fixed', label: 'fixed（直接用 DB 絕對值，仍為預留，未實作）' },
        ],
      },
      { key: 'battle_mob_hits', label: '未蓄氣普攻打死一般怪的目標次數' },
      { key: 'battle_mob_def_ratio', label: '怪物 DEF ÷ 玩家 ATK 比例' },
      { key: 'battle_enemy_dps_ratio', label: '全場敵人合計 DPS ÷ 玩家 MaxHP（每秒，例：0.010≈100 秒打死不防禦的玩家）' },
      { key: 'battle_player_min_atk', label: '玩家 ATK 保底（P2 專用，P3 會取消）' },
      { key: 'battle_player_min_hp', label: '玩家 MaxHP 保底（P2 專用，P3 會取消）' },
      { key: 'battle_attack_cooldown_ms', label: '普攻冷卻（毫秒，AGI 攻速為 0 或後台未調整時的基準冷卻，即「未配任何攻速加成」的手感）' },
      {
        key: 'battle_aspd_reference',
        label: '攻速換算基準點（AGI/DEX 攻速評級等於此值時，攻擊冷卻恰好＝上面的普攻冷卻，即「完全不配 AGI/DEX」的中性表現；只套用在玩家身上；必須嚴格介於 0～200 之間，見下方公式）',
      },
      {
        key: 'battle_attack_cooldown_min_ms',
        label: '玩家攻擊冷卻下限（毫秒，AGI 配再高也不會快過此值；需 >0 且 ≤ 上面的普攻冷卻，否則「配 AGI 變快」會被下限吃掉甚至配了更慢）',
      },
      { key: 'battle_charge_min_ms', label: '蓄氣最短時間（毫秒，需 ≤ 蓄氣全滿時間）' },
      { key: 'battle_charge_full_ms', label: '蓄氣全滿時間（毫秒）' },
      { key: 'battle_charge_max_multiplier', label: '蓄氣全滿傷害倍率' },
      { key: 'battle_guard_multiplier', label: '防禦時受到傷害倍率（0~1）' },
      { key: 'battle_recovery_ms', label: '出手後硬直恢復時間（毫秒）' },
      { key: 'battle_default_cast_ms', label: '技能預設施放時間（毫秒，技能未個別設定時使用）' },
      {
        key: 'battle_cast_min_ms',
        label: '玩家技能施放時間下限（毫秒，DEX/INT 詠唱縮減後不會低於此值；只套用在玩家身上；需 >0 且 ≤ 上面的技能預設施放時間，避免縮到比預設無詠唱技能還誇張）',
      },
      { key: 'battle_escape_judge_ms', label: '逃跑判定動畫時間（毫秒）' },
      { key: 'battle_resolve_delay_ms', label: '戰鬥結算延遲（毫秒，讓最後一擊動畫播完）' },
      { key: 'battle_enemy_act_min_ms', label: '敵人行動間隔下限（毫秒，乘怪物 speed_mult 前）' },
      { key: 'battle_enemy_act_max_ms', label: '敵人行動間隔上限（毫秒，需 ≥ 下限）' },
      { key: 'battle_ally_act_min_ms', label: '隊友 AI 行動間隔下限（毫秒）' },
      { key: 'battle_ally_act_max_ms', label: '隊友 AI 行動間隔上限（毫秒，需 ≥ 下限）' },
      {
        key: 'battle_hit_rate',
        label: '命中率後備值（0~1，1＝必中；只在攻擊方沒有「戰鬥評級」資料時當退路使用——正常情況下命中改由下方「戰鬥評級」機制決定，見 battle_base_miss_pct）',
      },
      {
        key: 'battle_crit_rate',
        label: '全體基礎暴擊率（0~1；P2 起攻守雙方都會加上這個基準值，讓 LUK 配一點就能看到暴擊，非玩家專屬）',
      },
      {
        key: 'battle_crit_multiplier',
        label: '暴擊傷害倍率（舊版固定值——P5 起暴擊改用下面「暴擊傷害浮動倍率」區間隨機抽，此欄前端已不使用，僅供沒套新版時的相容後備）',
      },
      { key: 'battle_exp_preview_per_level', label: '結算畫面「預估經驗」係數（純顯示，不入帳，＝Σ敵人等級×此值）' },
      { key: 'crit_mult_min', label: '（P5）暴擊傷害浮動倍率下界（契約預設 1.75；每次暴擊在下界～上界之間均勻抽，仍會扣防）' },
      { key: 'crit_mult_max', label: '（P5）暴擊傷害浮動倍率上界（契約預設 2.25）' },
      {
        key: 'battle_weakness_bonus_pct',
        label: '（P5）技能屬性命中怪物弱點屬性時的傷害加成 %（契約預設 25；管理者可在下方「屬性相剋表」個別覆寫，含設 0 做免疫）',
      },
      // DORPG P6（契約 §2）：level 模式怪物數值＝RefPlayer(N) 的對應衍生值 × mult（既有 hp_mult 等
      // 隊友/怪物個別倍率）× 這四個「等級模式」比例 × power_scale（沿用既有前後排 slotScale）。
      // 契約原始預設皆為 1.0；2026-09-19 起實際預設改為 hp/atk=3.0、def/mdef=0.4（見下方各欄位
      // label 與 config.go DefaultConfig），六場 power_scale 同時歸 1.0（migration 185）改當
      // 「單場微調」用，怪物強度主要由這四個全域比例決定；六場（Lv10/20/30/40/50/60）的難度曲線
      // 仍主要靠 RefPlayer(N) 隨等級成長本身拉開差距。
      {
        key: 'battle_lvl_hp_ratio',
        label: '（P6）level 模式：怪物 HP ÷ RefPlayer(N).HPMax 的比例（實際預設 3.0，非契約原始 1.0——2026-09-19 依「怪物至少同級玩家 3–4 倍」決策＋滿隊模擬校準，見 config.go DefaultConfig；docs/dorpg/MONSTER_X3_CALIBRATION.md）',
      },
      {
        key: 'battle_lvl_atk_ratio',
        label: '（P6）level 模式：怪物 ATK ÷ RefPlayer(N).ATK 的比例（實際預設 3.0，非契約原始 1.0——2026-09-19 依「怪物至少同級玩家 3–4 倍」決策＋滿隊模擬校準，見 config.go DefaultConfig；docs/dorpg/MONSTER_X3_CALIBRATION.md）',
      },
      {
        key: 'battle_lvl_def_ratio',
        label: '（P6）level 模式：怪物 DEF ÷ RefPlayer(N).DEF 的比例（實際預設 0.4，非契約原始 1.0——線性減防公式下 DEF 若也放大到 3–4 倍會讓玩家幾乎打不到怪，2026-09-19 滿隊模擬證實調低才可玩，見 config.go DefaultConfig；docs/dorpg/MONSTER_X3_CALIBRATION.md）',
      },
      {
        key: 'battle_lvl_mdef_ratio',
        label: '（P6）level 模式：怪物 MDEF ÷ RefPlayer(N).MDEF 的比例（實際預設 0.4，理由同上方 DEF，見 docs/dorpg/MONSTER_X3_CALIBRATION.md）',
      },
    ],
  },
  // P2（暴擊／Miss／無效攻擊）新增：戰鬥評級（命中/迴避/暴擊/暴擊迴避）與屬性相剋表，全部走
  // rpg_config JSON（沒有新增 migration）。玩家/隊友的評級來自既有 RO 素質系統（DEX→命中、
  // AGI→迴避、LUK→暴擊）算出的 Hit/Flee/CritPct/CritShield，這裡只調「怪物沒有個別覆寫時」的
  // 基準值與雙方共用的落空率/暴擊率公式係數。
  {
    title: '戰鬥評級（命中／迴避／暴擊）',
    fields: [
      { key: 'battle_base_miss_pct', label: '基礎落空率（%，clamp 前的起始值）' },
      { key: 'battle_hit_flee_scale', label: '每 1 點 (防守方迴避－攻擊方命中) 差額，增加的落空率（%）' },
      { key: 'battle_miss_min_pct', label: '落空率下限（%，即使命中遠高於迴避也至少會落空這麼多）' },
      {
        key: 'battle_miss_max_pct',
        label: '落空率上限（%，即使迴避遠高於命中也不會高過這個值）⚠️ 設到 100 會讓攻擊永遠落空；不能逃跑的關底場次（見「遭遇」分頁 canEscape）一旦打不到怪就無法脫身，會變成死局，設定前務必確認該場可以逃跑或怪物打得死',
      },
      {
        key: 'battle_monster_hit_base',
        label: '怪物命中基準偏移量（P3 已改語意：不再是絕對值，而是相對「玩家等級基線」的偏移——實際命中＝玩家 Base Lv × 下方每級命中係數 + 此值；配合每級係數讓怪物命中跟著玩家等級同步成長，DEX 配點才會一直有效）',
      },
      {
        key: 'battle_monster_hit_per_level',
        label: '怪物命中隨玩家 Base Lv 增加的斜率（每級 +N；預設 1.0 對齊玩家「基本等級加成」的每級命中——沒有這個係數，怪物命中會跟等級無關，中後期被玩家等級成長甩開，DEX 配點形同虛設）',
      },
      {
        key: 'battle_monster_hit_max',
        label: '怪物命中上限（絕對天花板，必須小於「迴避率上限 flee_cap_pct」預設 95）⚠️ 上面那條命中會隨玩家等級無限成長，但玩家迴避被 flee_cap_pct 硬性封頂——沒有這個上限，Base Lv 93 之後怪物命中就超過玩家可能達到的最高迴避，AGI 配到滿閃避率也會掉回下限、等於完全無效。預設 75，刻意留 20 點投資空間',
      },
      {
        key: 'battle_monster_flee_base',
        label: '怪物迴避基準偏移量（P3 已改語意：不再是絕對值，而是相對「玩家等級基線」的偏移——實際迴避＝(玩家 Base Lv × 下方每級迴避係數 + 此值) × 該怪 speed_mult）',
      },
      {
        key: 'battle_monster_flee_per_level',
        label: '怪物迴避隨玩家 Base Lv 增加的斜率（每級 +N；預設 1.0 對齊玩家「基本等級加成」的每級迴避——沒有這個係數，AGI 配到滿也不可能提高迴避率，見上方 battle_monster_hit_base 語意改寫的理由）',
      },
      { key: 'battle_monster_crit_pct', label: '怪物暴擊率基準（%）' },
      { key: 'battle_monster_crit_shield_base', label: '怪物暴擊迴避基準（實際＝此值 × 該怪 def_mult）' },
      // DORPG P7（契約 §1）：五行＋光暗相剋的三個全域倍率係數。管理者若在下面「屬性相剋表」對
      // 某組（怪物屬性,技能屬性）個別覆寫，該組覆寫值優先於這三個全域係數（ElementMultiplier 的
      // 判斷順序：chart 覆寫 > 這三個係數 > weakElements 取大 > 1）。
      {
        key: 'battle_element_advantage_pct',
        label: '（P7）攻擊屬性剋制怪物屬性時的倍率加成 %（契約預設 25，即該次攻擊 ×1.25）',
      },
      {
        key: 'battle_element_disadvantage_pct',
        label: '（P7）攻擊屬性被怪物屬性剋制時的倍率減損 %（契約預設 −25，即該次攻擊 ×0.75；填正數會變成「被剋反而加成」，請填負數）',
      },
      {
        key: 'battle_element_same_pct',
        label: '（P7）攻擊屬性與怪物屬性相同時的倍率減損 %（契約預設 −25，即 ×0.75；同樣請填負數）',
      },
      {
        key: 'battle_element_chart',
        label: '屬性相剋表（JSON：外層 key＝怪物屬性【中文，如「金」「木」】→ 內層 key＝技能屬性【英文 ElementKind，如 water/fire】→ 數字倍率；0＝完全無效／玩家會看到「無效攻擊」，未列出的屬性或組合＝上面三個全域係數依五行/光暗規則算出的值。刪除某個屬性/element key 現在會真的生效，不會在下次讀取時被預設表復活）',
        type: 'json',
      },
    ],
  },
]

// ---------------------------------------------------------------------------
// DORPG P7（武器系統，見契約 dorpg_p7 CONTRACT.md §2/§3、WIRE.md）：EquipmentScreen（會員）與
// admin/rpg（後台，武器分頁）共用的顯示文案。ATK/MATK 兩個基礎數字兩邊呼叫端都各自有獨立欄位顯示
// （WIRE：「名稱、需 Lv、ATK／MATK、效果摘要」），formatWeaponProfile() 刻意跳過 atk/matk，只描述
// 其餘特性，避免同一個數字被講兩次。

export const ELEMENT_LABEL: Record<RpgElement, string> = {
  metal: '金', wood: '木', water: '水', fire: '火', earth: '土', light: '光', dark: '闇', neutral: '無',
}
export function elementLabel(el: string | null | undefined): string {
  return (el && ELEMENT_LABEL[el as RpgElement]) || el || '無'
}
/** 七屬性（不含 neutral）固定順序，供裝備頁「屬性版七顆 chip」與後台屬性選單使用。 */
export const ELEMENT_ORDER: RpgElement[] = ['metal', 'wood', 'water', 'fire', 'earth', 'light', 'dark']

export const RARITY_LABEL: Record<WeaponRarity, string> = {
  common: '普通', rare: '稀有', epic: '史詩', legendary: '傳說',
}
/** 稀有度標籤底色（全部搭白字——見全站「金底白字」通則；legendary 直接用金底）。 */
export const RARITY_COLOR: Record<WeaponRarity, string> = {
  common: '#7c8a99', rare: '#3a8ff4', epic: '#9b5de5', legendary: 'var(--gold)',
}

const WEAPON_SIZE_LABEL: Record<string, string> = { small: '小型', medium: '中型', large: '大型' }

/**
 * 把 WeaponProfile 組成一句效果摘要，供 EquipmentScreen 武器列表與武器卡共用（WIRE：「效果摘要由
 * rpgMeta 的 formatWeaponProfile() 產生」）。刻意跳過 atk/matk（呼叫端已有獨立欄位顯示基礎數字，
 * 見檔頭說明），只描述其餘特性；缺省／0 值的欄位一律不顯示，避免空泛的「+0%」洗版。純顯示用途，
 * 不做任何戰鬥判斷——實際戰鬥數值一律以 bootstrap 送來的 profile 為準（同 formatEffectAtLevel 的
 * 既有慣例）。
 */
export function formatWeaponProfile(p: WeaponProfile | null | undefined): string {
  if (!p) return ''
  const parts: string[] = []
  if (p.int_bonus) parts.push(`INT+${p.int_bonus}`)
  if (p.mp_pct) parts.push(`MP+${p.mp_pct}%`)
  if (p.atk_pct) parts.push(`物攻+${p.atk_pct}%`)
  if (p.matk_pct) parts.push(`魔攻+${p.matk_pct}%`)
  if (p.def_pct) parts.push(`物防+${p.def_pct}%`)
  if (p.mdef_pct) parts.push(`魔防+${p.mdef_pct}%`)
  if (p.hits && p.hits > 1) {
    const mul = p.hit_mul != null && p.hit_mul !== 1 ? `（每段×${p.hit_mul}）` : ''
    parts.push(`${p.hits} 段攻擊${mul}`)
  }
  if (p.extra_hit_chance_pct) parts.push(`${p.extra_hit_chance_pct}% 機率額外一段`)
  if (p.interval_pct) parts.push(p.interval_pct < 0 ? `攻擊間隔縮短${-p.interval_pct}%` : `攻擊間隔拉長${p.interval_pct}%`)
  if (p.charge_time_mul != null && p.charge_time_mul !== 1) parts.push(`蓄氣時間×${p.charge_time_mul}`)
  if (p.charge_dmg_mul != null && p.charge_dmg_mul !== 1) parts.push(`蓄氣傷害×${p.charge_dmg_mul}`)
  if (p.splash_pct) parts.push(`同排左右濺射${p.splash_pct}%`)
  if (p.size_bonus) {
    for (const k of ['small', 'medium', 'large'] as const) {
      const v = p.size_bonus[k]
      if (v) parts.push(`對${WEAPON_SIZE_LABEL[k]}怪物+${v}%傷害`)
    }
  }
  if (p.crit_pct) parts.push(`暴擊率+${p.crit_pct}`)
  if (p.crit_dmg_pct) parts.push(`暴擊傷害+${p.crit_dmg_pct}%`)
  if (p.flee_bonus) parts.push(`迴避+${p.flee_bonus}`)
  if (p.element_resist_pct) parts.push(`屬性抗性+${p.element_resist_pct}%`)
  if (p.magic_skill_pct) parts.push(`魔法技能效果+${p.magic_skill_pct}%`)
  return parts.join('・')
}

/**
 * DORPG P12（契約 dorpg_p12 CONTRACT.md §1/§3）：武器「類型」層級的前後排加成／貫穿說明——
 * 掛在 WeaponTypeDTO.traits（JSONB，P7 已有此欄，本輪只新增以下四個鍵，其餘既有描述鍵
 * style/special/positioning/size_bonus/interval_pct/…_range 不動、這裡也不讀）：
 *   row_bonus_rear_pct（弓）／row_bonus_front_pct（鈍器）／pierce_chance_pct＋pierce_dmg_pct（槍）。
 * 只讀這四個鍵，缺省或非數字一律當 0（traits 是 Record<string, unknown>，來源是後台 JSON textarea，
 * 不假設型別正確）。純顯示用途，不做戰鬥判斷——實際數值由 bootstrap 的 weapon.profile 四個新欄位
 * （rowBonusFrontPct/rowBonusRearPct/pierceChancePct/pierceDmgPct）帶給引擎。
 */
export function formatWeaponTypeRowBonus(traits: Record<string, unknown> | null | undefined): string {
  if (!traits) return ''
  const num = (key: string): number => {
    const v = traits[key]
    return typeof v === 'number' && isFinite(v) ? v : 0
  }
  const rearPct = num('row_bonus_rear_pct')
  const frontPct = num('row_bonus_front_pct')
  const pierceChancePct = num('pierce_chance_pct')
  const pierceDmgPct = num('pierce_dmg_pct')
  const parts: string[] = []
  if (rearPct) parts.push(`對後排 +${rearPct}%`)
  if (frontPct) parts.push(`對前排 +${frontPct}%`)
  if (pierceChancePct) parts.push(`${pierceChancePct}% 機率貫穿，後排受 ${pierceDmgPct}% 波及`)
  return parts.join('・')
}

// ---------------------------------------------------------------------------
// DORPG P8（防具＋飾品裝備，見契約 dorpg_p8 CONTRACT.md §1/§2/§3、WIRE.md）：EquipmentScreen（會員）
// 與 admin/rpg（後台，防具分頁）共用的顯示文案。

/** 防具品項固定占用的格子（ArmorDTO.slot）中文標籤。 */
export const ARMOR_SLOT_LABEL: Record<ArmorItemSlot, string> = {
  helmet: '頭盔', gloves: '手套', armor: '衣服', legs: '褲裙', boots: '鞋子', accessory: '飾品',
}

/** 八格裝備欄（EquipmentSlot）中文標籤——飾品兩格分開標「飾品1／飾品2」。 */
export const EQUIP_SLOT_LABEL: Record<EquipmentSlot, string> = {
  weapon: '武器', helmet: '頭盔', gloves: '手套', armor: '衣服', legs: '褲裙', boots: '鞋子',
  accessory1: '飾品1', accessory2: '飾品2',
}

/** 八格裝備欄固定顯示順序（契約 §5：武器、頭盔、手套、衣服、褲裙、鞋子、飾品1、飾品2）。 */
export const EQUIP_SLOT_ORDER: EquipmentSlot[] = ['weapon', 'helmet', 'gloves', 'armor', 'legs', 'boots', 'accessory1', 'accessory2']
/** 五個防具格（不含武器／飾品），EquipmentScreen 判斷「目前選的格子是不是防具」時用。 */
export const ARMOR_EQUIP_SLOTS: EquipmentSlot[] = ['helmet', 'gloves', 'armor', 'legs', 'boots']

/**
 * 飾品 18 種效果依契約 §3 原文順序（供 EquipmentScreen 分組列表與後台下拉選單一致排序）。
 * 這 18 個 key 恰好就是 ArmorProfile 扣掉 def 之後的全部欄位——契約設計成「每件飾品恰好只有一個
 * 非零效果欄位」，所以可以直接拿這份順序表逐一掃描找出該件飾品是哪一組效果（見下面
 * accessoryEffectKey()），不必依賴 id/name 命名慣例（防具 seed 由另一個 workflow 產生，這裡只信
 * 任契約保證的 profile 欄位語意）。
 */
export const ACCESSORY_EFFECT_ORDER: (keyof ArmorProfile)[] = [
  'hp_pct', 'mp_pct', 'str', 'vit', 'dex', 'agi', 'int', 'luk',
  'interval_pct', 'crit_pct', 'crit_dmg_pct', 'mp_cost_reduce_pct',
  'hp_regen_pct_per_5s', 'mp_regen_pct_per_5s', 'atk_pct', 'matk_pct',
  'damage_taken_pct', 'element_resist_pct',
]
export const ACCESSORY_EFFECT_LABEL: Record<string, string> = {
  hp_pct: 'HP 上限', mp_pct: 'MP 上限',
  str: '力量 STR', vit: '體質 VIT', dex: '靈巧 DEX', agi: '敏捷 AGI', int: '智力 INT', luk: '幸運 LUK',
  interval_pct: '降低攻擊間隔', crit_pct: '爆擊率', crit_dmg_pct: '爆擊傷害', mp_cost_reduce_pct: '減少 MP 消耗',
  hp_regen_pct_per_5s: '定時恢復 HP', mp_regen_pct_per_5s: '定時恢復 MP',
  atk_pct: '增加 ATK', matk_pct: '增加 MATK', damage_taken_pct: '減少 HP 傷害', element_resist_pct: '屬性傷害降低',
}

/** 找出這件飾品的主效果欄位（見上方 ACCESSORY_EFFECT_ORDER 說明：每件飾品恰好只有一個非零效果
 *  欄位）。找不到非零欄位（理論上不會發生於合法飾品資料，防禦用）回傳 null。 */
export function accessoryEffectKey(p: ArmorProfile | null | undefined): (keyof ArmorProfile) | null {
  if (!p) return null
  for (const k of ACCESSORY_EFFECT_ORDER) {
    if (p[k]) return k
  }
  return null
}

/**
 * 把 ArmorProfile 組成一句效果摘要，供 EquipmentScreen 防具/飾品列表與目前裝備卡共用（同
 * formatWeaponProfile 的既有慣例）。刻意跳過 def（呼叫端已有獨立欄位顯示基礎防禦力，比照武器
 * 跳過 atk/matk 的既有慣例）；缺省／0 值的欄位一律不顯示。純顯示用途，不做任何戰鬥判斷。
 */
export function formatArmorProfile(p: ArmorProfile | null | undefined): string {
  if (!p) return ''
  const parts: string[] = []
  if (p.str) parts.push(`STR+${p.str}`)
  if (p.agi) parts.push(`AGI+${p.agi}`)
  if (p.vit) parts.push(`VIT+${p.vit}`)
  if (p.dex) parts.push(`DEX+${p.dex}`)
  if (p.int) parts.push(`INT+${p.int}`)
  if (p.luk) parts.push(`LUK+${p.luk}`)
  if (p.hp_pct) parts.push(`HP+${p.hp_pct}%`)
  if (p.mp_pct) parts.push(`MP+${p.mp_pct}%`)
  if (p.atk_pct) parts.push(`物攻+${p.atk_pct}%`)
  if (p.matk_pct) parts.push(`魔攻+${p.matk_pct}%`)
  if (p.interval_pct) parts.push(p.interval_pct < 0 ? `攻擊間隔縮短${-p.interval_pct}%` : `攻擊間隔拉長${p.interval_pct}%`)
  if (p.crit_pct) parts.push(`暴擊率+${p.crit_pct}`)
  if (p.crit_dmg_pct) parts.push(`暴擊傷害+${p.crit_dmg_pct}%`)
  if (p.mp_cost_reduce_pct) parts.push(`MP 消耗-${p.mp_cost_reduce_pct}%`)
  if (p.hp_regen_pct_per_5s) parts.push(`每 5 秒回 HP ${p.hp_regen_pct_per_5s}%`)
  if (p.mp_regen_pct_per_5s) parts.push(`每 5 秒回 MP ${p.mp_regen_pct_per_5s}%`)
  if (p.damage_taken_pct) parts.push(`受到傷害${p.damage_taken_pct > 0 ? '+' : ''}${p.damage_taken_pct}%`)
  if (p.element_resist_pct) parts.push(`屬性傷害-${p.element_resist_pct}%`)
  return parts.join('・')
}

/**
 * 把彙總後的 EquipBonusDTO 組成一句加成摘要（「DEF+n、VIT+n…」），供 CharacterScreen 裝備摘要區塊
 * 使用（任務 §3）。跟 formatArmorProfile 不同——這裡不跳過任何欄位（含 def／六素質），因為呼叫端
 * 本身沒有另外顯示這些數字的獨立欄位，此函式就是唯一的顯示位置。
 */
export function formatEquipBonus(b: EquipBonusDTO | null | undefined): string {
  if (!b) return ''
  const parts: string[] = []
  if (b.def) parts.push(`DEF+${b.def}`)
  if (b.str) parts.push(`STR+${b.str}`)
  if (b.agi) parts.push(`AGI+${b.agi}`)
  if (b.vit) parts.push(`VIT+${b.vit}`)
  if (b.dex) parts.push(`DEX+${b.dex}`)
  if (b.int) parts.push(`INT+${b.int}`)
  if (b.luk) parts.push(`LUK+${b.luk}`)
  if (b.hp_pct) parts.push(`HP+${b.hp_pct}%`)
  if (b.mp_pct) parts.push(`MP+${b.mp_pct}%`)
  if (b.atk_pct) parts.push(`物攻+${b.atk_pct}%`)
  if (b.matk_pct) parts.push(`魔攻+${b.matk_pct}%`)
  if (b.interval_pct) parts.push(b.interval_pct < 0 ? `攻擊間隔縮短${-b.interval_pct}%` : `攻擊間隔拉長${b.interval_pct}%`)
  if (b.crit_pct) parts.push(`暴擊率+${b.crit_pct}`)
  if (b.crit_dmg_pct) parts.push(`暴擊傷害+${b.crit_dmg_pct}%`)
  if (b.mp_cost_reduce_pct) parts.push(`MP 消耗-${b.mp_cost_reduce_pct}%`)
  if (b.hp_regen_pct_per_5s) parts.push(`每 5 秒回 HP ${b.hp_regen_pct_per_5s}%`)
  if (b.mp_regen_pct_per_5s) parts.push(`每 5 秒回 MP ${b.mp_regen_pct_per_5s}%`)
  if (b.damage_taken_pct) parts.push(`受到傷害${b.damage_taken_pct > 0 ? '+' : ''}${b.damage_taken_pct}%`)
  if (b.element_resist_pct) parts.push(`屬性傷害-${b.element_resist_pct}%`)
  return parts.join('・')
}

// ---------------------------------------------------------------------------
// DORPG P9（AI 戰鬥策略＋自動戰鬥，見契約 dorpg_p9 CONTRACT.md §1/§2）：本檔只加這份靜態標籤表
// （任務 §0 FRONTEND 所有權：rpgMeta.ts 只加策略標籤），六種 id 固定（引擎 registry 白名單）。
// 這份表是「後端資料尚未到位或呼叫端沒有整包 StrategyDTO」時的顯示備援（例如 CharacterScreen
// 的 /rpg/me 只回 auto_battle.strategy_id，沒有附帶名稱）；有拿到 StrategyDTO[] 的地方
// （TavernScreen 腳本編輯器、戰鬥 HUD 的 AutoBattleBar、後台）一律優先顯示後端給的 name/description
// （後台可調文案），這份表只在拿不到時當備援，不能取代後端資料。
export const STRATEGY_IDS = ['balanced', 'mp_conserve', 'skill_aggressive', 'protect_allies', 'focus_fire', 'element_advantage'] as const
export type StrategyId = (typeof STRATEGY_IDS)[number]
export const STRATEGY_LABEL: Record<string, string> = {
  balanced: '均衡',
  mp_conserve: 'MP 保守使用',
  skill_aggressive: '技能積極使用',
  protect_allies: '保護隊友優先',
  focus_fire: '集中火力',
  element_advantage: '屬性相剋優先',
}
/** 找不到時退回 id 原文（未知 id 理論上不會發生，畢竟引擎也會退回 balanced，這裡純防禦）。 */
export function strategyLabel(id: string | null | undefined): string {
  if (!id) return STRATEGY_LABEL.balanced
  return STRATEGY_LABEL[id] ?? id
}

// ---------------------------------------------------------------------------
// DORPG P11（怪物強度九級，見契約 dorpg_p11 CONTRACT.md §1/§2、WIRE.md）：EncounterPicker（強度挑戰
// 卡片）、BattleStage/BattleScreen（敵人徽章）、admin/rpg（怪物強度分頁／怪物／遭遇表單的 rank
// 下拉）共用同一份九級詞彙表——由弱到強固定 9 個，後端 `GET /rpg/ranks` 與 `rpg_monster_ranks`
// 是唯一真相（label／badge_color 皆可後台調），這裡的 RANK_LABEL／RANK_BADGE_FALLBACK 只在後端
// 尚未送值（舊版回應／後端還沒接上這批欄位）時當顯示備援，不能取代後端資料。
export const RANK_ORDER: RpgRank[] = ['F', 'E', 'D', 'C', 'B', 'A', 'SA', 'S', 'SS']
export const RANK_LABEL: Record<RpgRank, string> = {
  F: 'F級', E: 'E級', D: 'D級', C: 'C級', B: 'B級', A: 'A級', SA: '特A級', S: 'S級', SS: '特S級',
}
/** 找不到（未知 rank 字面值）時原樣顯示，不會擋渲染。 */
export function rankLabel(rank: string | null | undefined): string {
  if (!rank) return ''
  return RANK_LABEL[rank as RpgRank] ?? rank
}
/** 徽章底色備援（後端 badge_color 尚未送值時使用）——由弱到強大致對齊「越強越顯眼」的直覺配色，
 *  非正式規格，後台隨時可用 badge_color 覆寫，這裡的值不影響任何戰鬥計算，純顯示保底。 */
export const RANK_BADGE_FALLBACK: Record<RpgRank, string> = {
  F: '#8d99a8', E: '#4caf7d', D: '#3a8ff4', C: '#9b5de5', B: '#f2994a',
  A: '#e5484d', SA: '#d6409f', S: 'var(--gold, #f3bd62)', SS: '#7b1e3a',
}
export function rankBadgeColor(rank: string | null | undefined, badgeColor: string | null | undefined): string {
  if (badgeColor) return badgeColor
  if (rank && rank in RANK_BADGE_FALLBACK) return RANK_BADGE_FALLBACK[rank as RpgRank]
  return RANK_BADGE_FALLBACK.F
}
/** 強度挑戰卡片「單挑／三隻／五隻」（契約 §1：每級 1/3/5 隻的對戰列表）；非這三種整除數字
 *  （目前規格不會出現，防禦用）直接顯示「N 隻」。 */
export function rankCountLabel(count: number | null | undefined): string {
  if (count === 1) return '單挑'
  if (count === 3) return '三隻'
  if (count === 5) return '五隻'
  return count != null ? `${count} 隻` : ''
}
