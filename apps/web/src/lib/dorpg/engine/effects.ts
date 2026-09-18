// P5（CONTRACT §5/§6）：buff/debuff 狀態效果的套用／查詢／到期＋回復清理，以及暴擊倍率浮動抽樣。
// 獨立成一個檔案而不是塞進 formulas.ts：formulas.ts 專注在「單次數值換算」（給什麼輸入、算出什麼
// 輸出，不記得任何東西），這裡處理的是「跨多個 tick 存活的狀態」——需要知道 ctx.now 才能判斷到期、
// 而且會直接 mutate 呼叫端傳入的 activeEffects 陣列（tick/dispatch 走的都是「本次呼叫的工作副本」，
// 見 context.ts toCtx 對 activeEffects 的深拷貝），職責跟 formulas.ts 的純數值函式不同。
import type { ActorStats, BuffDebuffStat, CombatRating } from '../types';
import type { ActiveEffect, BattleConfig } from './types';
import { floorInt } from './formulas';

/** 該屬性目前所有存活效果值加總。「同 stat 不同來源」本來就該疊加——只有「同 stat 同來源」才會在
 *  applyStatusEffect 被合併成一筆，這裡單純加總陣列裡剩下的每一筆，不需要再去重。 */
export function activeStatSum(effects: ActiveEffect[], stat: BuffDebuffStat): number {
  return effects.filter((e) => e.stat === stat).reduce((sum, e) => sum + e.value, 0);
}

/**
 * CONTRACT §5 疊加規則：「同 stat 同來源刷新不疊加」——同一顆技能（sourceSkillId）重複命中同一個
 * stat 直接取代舊的一筆（刷新數值與到期時間，不會變成兩筆疊加）；不同技能即使打在同一個 stat 上，
 * 呼叫端各自呼叫一次 applyStatusEffect 就會各自留一筆、彼此獨立疊加（活得越久的那一筆先到期就先消失，
 * 不會互相干擾對方的到期時間）。
 */
export function applyStatusEffect(target: { activeEffects: ActiveEffect[] }, effect: ActiveEffect): void {
  const idx = target.activeEffects.findIndex((e) => e.stat === effect.stat && e.sourceSkillId === effect.sourceSkillId);
  if (idx >= 0) target.activeEffects[idx] = effect;
  else target.activeEffects.push(effect);
}

/**
 * 到期清除＋hp_regen_pct 定時回復。回復本身透過 onRegen 回呼交給呼叫端執行（tick.ts 才有辦法推
 * heal 事件、封頂 hpMax 等——這裡刻意不 import combat.ts 的 applyHealToTarget，避免 combat.ts↔
 * effects.ts 循環相依，也讓這支函式對 PartyActor/EnemyActor 兩種呼叫端都通用）。
 * while 迴圈保護：tick 的呼叫步距若剛好跨過不只一個 1000ms 回復窗口（例如測試直接 now+=5000ms，
 * 或分頁背景太久沒有 rAF），也要把該回的血一次全部補上，不能只回一次就把 nextTickAt 推到未來、
 * 丟掉中間應該發生的幾次回復。
 */
export function pruneAndRegenEffects(
  now: number,
  actor: { activeEffects: ActiveEffect[]; hp: number; stats: ActorStats },
  onRegen: (amount: number) => void,
  onExpire?: (effect: ActiveEffect) => void,
): void {
  const kept: ActiveEffect[] = [];
  for (const e of actor.activeEffects) {
    if (e.expiresAt <= now) {
      onExpire?.(e); // 到期直接丟棄，不進 kept；呼叫端可選擇性地推 'statusExpired' 事件。
      continue;
    }
    if (e.stat === 'hp_regen_pct' && actor.hp > 0) {
      let next = e.nextTickAt ?? now;
      while (next <= now) {
        // P6（CONTRACT §1）：hp_regen 明講「向下取整」，Math.round 改成 floorInt——負值 value（例如
        // 未來設計出「持續掉血」的 debuff 誤用同一個 stat）floor 會往負無窮取整（扣更多），這正是
        // 契約「無條件捨去」的方向性，不是四捨五入。
        onRegen(floorInt(actor.stats.hpMax * (e.value / 100)));
        next += 1000;
      }
      kept.push({ ...e, nextTickAt: next });
    } else {
      kept.push(e);
    }
  }
  actor.activeEffects = kept;
}

/** 套用 atk_pct/matk_pct/def_pct/mdef_pct 後的即時數值；base 本身永遠不變——effects 到期後自動
 *  回到原值，不需要另外「復原」的簿記，這也是選擇「每次用到才即時算」而不是直接 mutate stats 的原因。 */
export function effectiveStats(base: ActorStats, effects: ActiveEffect[]): ActorStats {
  const mul = (pct: BuffDebuffStat) => 1 + activeStatSum(effects, pct) / 100;
  return {
    // P6（CONTRACT §1）：hpMax/mpMax 本身不會被任何 buff/debuff 詞彙即時改動（BuffDebuffStat 沒有
    // hp_max_pct/mp_max_pct，那是被動技能、由後端算進 base 值），這裡的 floorInt 純粹是防禦——萬一
    // base 本身不知何故帶了小數（例如上游資料層的臨時 bug），effectiveStats 不會把小數繼續傳下去。
    hpMax: floorInt(base.hpMax),
    mpMax: floorInt(base.mpMax),
    atk: Math.round(base.atk * mul('atk_pct')),
    matk: Math.round(base.matk * mul('matk_pct')),
    def: Math.round(base.def * mul('def_pct')),
    mdef: Math.round(base.mdef * mul('mdef_pct')),
  };
}

/** 套用 aspd/crit_pct/flee/hit 後的即時評級；critShield 目前沒有對應的 buff/debuff 詞彙
 *  （CONTRACT §5 沒有列 crit_shield 這個 stat），維持 base 值原樣通過。critDmgPct 同理——
 *  BuffDebuffStat 詞彙表沒有 crit_dmg_pct 這個 stat（它只是 passive 詞彙，由後端 Compute()
 *  算進 Rating 送來，見 CombatRating.critDmgPct 型別註解），這裡單純原樣通過，不疊加任何
 *  buff/debuff。 */
export function effectiveRating(base: CombatRating, effects: ActiveEffect[]): CombatRating {
  return {
    hit: base.hit + activeStatSum(effects, 'hit'),
    flee: base.flee + activeStatSum(effects, 'flee'),
    critPct: base.critPct + activeStatSum(effects, 'crit_pct'),
    critShield: base.critShield,
    aspd: base.aspd + activeStatSum(effects, 'aspd'),
    castReductionPct: base.castReductionPct,
    critDmgPct: base.critDmgPct ?? 0,
  };
}

/**
 * 承受傷害倍率（damage_taken_pct，詞彙表只在 buff 那邊列出——debuff 沒有這個 stat，所以只有隊伍
 * 側會用到這支函式）。
 * P8（DORPG_P8 CONTRACT §2／WIRE「引擎」）：新增可選參數 equipmentDamageTakenPct（預設 0，
 * PartyActor.equipmentEffects.damageTakenPct）——跟 buff 的 damage_taken_pct 相加後，改成 clamp
 * 下限 −60%（契約「與 buff 相加（≥ −60）」）取代舊版「clamp 最終倍率下限 0」的規則：疊加上限從
 * 「理論上可以無限疊到倒扣血邊緣（倍率 0）」收斂成「最多減傷 60%」，賦予裝備與 buff 一個有意義
 * 的疊加天花板。因為 clamp 後的 pct 恆 ≥ −60，換算出的倍率恆 ≥ 0.4，天生就不會變成負數，
 * 不需要再另外套一層 `Math.max(0, ...)`。equipmentDamageTakenPct 缺省 0 時（呼叫端沒有裝備資料，
 * 例如既有測試直接手造 activeEffects 陣列）行為等同「只有 buff 部分」，但 clamp 下限已經從舊版的
 * 「倍率 0」變成「倍率 0.4」——這是契約明講的新規則，不是相容性妥協（見任務回報／verify 腳本
 * 更新後的期望值）。
 */
export function damageTakenMultiplier(effects: ActiveEffect[], equipmentDamageTakenPct = 0): number {
  const sum = equipmentDamageTakenPct + activeStatSum(effects, 'damage_taken_pct');
  return 1 + Math.max(-60, sum) / 100;
}

/**
 * CONTRACT §6：暴擊倍率不再固定 2.0，改成每次暴擊在 [critMultMin, critMultMax] 均勻抽樣。只在真的
 * 判定出暴擊時才呼叫（呼叫端保證，見 combat.ts/ai.ts）——跟其餘只在 isCrit 分支才消耗的 rng() 呼叫
 * 同一個節奏，不會讓「這場戰鬥全程不會暴擊」的既有測試多消耗一次 rng()、打亂它們原本鎖定的序列。
 */
export function rollCritMultiplier(rng: () => number, cfg: BattleConfig): number {
  return cfg.critMultMin + rng() * (cfg.critMultMax - cfg.critMultMin);
}
