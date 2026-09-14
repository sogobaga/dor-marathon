// 遊戲化角色數值（RO 素質系統）共用中文標籤／說明 — CharacterScreen（會員）與 admin/rpg（後台）
// 兩處共用，避免文案各自維護、之後改一次兩邊同步。數字係數本身一律吃 RpgConfig／預覽 API 算出的
// 結果，本檔只放「怎麼顯示」，不放任何算式（算式在後端 internal/rpg，前端不重算，見任務決策 D2）。
import type { RpgConfig, RpgDerived, RpgStatKey } from './api'

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

// 後台「參數設定」分頁分組欄位 — 逐項對照 RpgConfig 欄位（勿漏改名）。type 省略＝number 輸入框；
// 'json' 是 P2 新增（battle_element_chart 巢狀 map 專用），admin/rpg/page.tsx 的 ConfigTab 用它
// 決定渲染 textarea 而非 <input type="number">，存檔前另外做 JSON.parse 驗證。
export interface ConfigField { key: keyof RpgConfig; label: string; help?: string; type?: 'select' | 'json'; options?: { value: string; label: string }[]; step?: number }
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
      { key: 'lv_def_per', label: '每幾級：物防+1（等級門檻）' },
      { key: 'lv_atk_per', label: '每幾級：物攻+1（等級門檻）' },
      { key: 'lv_matk_per', label: '每幾級：魔攻+1（等級門檻）' },
      { key: 'lv_mdef_per', label: '每幾級：魔防+1（等級門檻）' },
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
          { value: 'power', label: 'power（以玩家戰力動態縮放，P2 唯一實作）' },
          { value: 'level', label: 'level（依 Base Lv 絕對表，P3 保留分支，P2 選了等同 power 行為）' },
          { value: 'fixed', label: 'fixed（直接用 DB 絕對值，P3 保留分支，P2 選了等同 power 行為）' },
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
      { key: 'battle_crit_multiplier', label: '暴擊傷害倍率' },
      { key: 'battle_exp_preview_per_level', label: '結算畫面「預估經驗」係數（純顯示，不入帳，＝Σ敵人等級×此值）' },
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
