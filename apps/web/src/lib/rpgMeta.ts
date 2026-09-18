// 遊戲化角色數值（RO 素質系統）共用中文標籤／說明 — CharacterScreen（會員）與 admin/rpg（後台）
// 兩處共用，避免文案各自維護、之後改一次兩邊同步。數字係數本身一律吃 RpgConfig／預覽 API 算出的
// 結果，本檔只放「怎麼顯示」，不放任何算式（算式在後端 internal/rpg，前端不重算，見任務決策 D2）。
import type { EffectAtLevel, JobDTO, RpgConfig, RpgDerived, RpgStatKey, SkillKind } from './api'

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
export const SKILL_KIND_LABEL: Record<SkillKind, string> = {
  damage: '傷害',
  heal: '治療',
  shield: '護盾',
  buff: '增益',
  debuff: '減益',
  passive: '被動',
  special: '尚未實裝',
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
      return `${skillStatLabel(e.stat ?? '')} ${sign}${fmtCoef(e.value)}（常駐）`
    }
    case 'special':
    default:
      return '本輪尚未實裝數值'
  }
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
      // 四個都預設 1.0；四場（Lv10/20/30/40/50/60）的難度曲線主要靠 RefPlayer(N) 隨等級成長本身
      // 拉開差距，這裡是全域再統一調整的旋鈕（例如覺得整體太肉/太脆時只調一個數字）。
      {
        key: 'battle_lvl_hp_ratio',
        label: '（P6）level 模式：怪物 HP ÷ RefPlayer(N).HPMax 的比例（實際預設 0.8，非契約原始 1.0——2026-09-18 依 Lv27 六場模擬校準，見 config.go DefaultConfig）',
      },
      {
        key: 'battle_lvl_atk_ratio',
        label: '（P6）level 模式：怪物 ATK ÷ RefPlayer(N).ATK 的比例（實際預設 1.25，非契約原始 1.0——2026-09-18 依 Lv27 六場模擬校準，見 config.go DefaultConfig）',
      },
      {
        key: 'battle_lvl_def_ratio',
        label: '（P6）level 模式：怪物 DEF ÷ RefPlayer(N).DEF 的比例（契約預設 1.0）',
      },
      {
        key: 'battle_lvl_mdef_ratio',
        label: '（P6）level 模式：怪物 MDEF ÷ RefPlayer(N).MDEF 的比例（契約預設 1.0）',
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
      {
        key: 'battle_element_chart',
        label: '屬性相剋表（JSON：外層 key＝怪物屬性【中文，如「金」「木」】→ 內層 key＝技能屬性【英文 ElementKind，如 water/fire】→ 數字倍率；0＝完全無效／玩家會看到「無效攻擊」，未列出的屬性或組合＝1.0 不相剋不吃虧。刪除某個屬性/element key 現在會真的生效，不會在下次讀取時被預設表復活）',
        type: 'json',
      },
    ],
  },
]
