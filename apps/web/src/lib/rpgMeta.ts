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

// 後台「參數設定」分頁分組欄位 — 逐項對照 RpgConfig 欄位（勿漏改名）。type 省略＝number 輸入框。
export interface ConfigField { key: keyof RpgConfig; label: string; help?: string; type?: 'select'; options?: { value: string; label: string }[]; step?: number }
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
]
