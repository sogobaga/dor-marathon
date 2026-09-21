// dispatch(state, cmd, now)：先 tick 到 now（推進計時/AI），再套用玩家指令。純函式，兩段都各自
// 透過 toCtx/fromCtx 操作工作副本，不會動到傳入的 state。
//
// P9（DORPG_P9 CONTRACT §4／WIRE「引擎」）重構：原本單一函式 applyCommand(state, cmd, now) 拆成
// 「純 ctx 變動」的 applyCommandOnCtx(ctx, cmd) ＋ 薄殼 applyCommand(state, cmd, now)。原因：
// autopilot.ts 的玩家自動戰鬥要「把 Decision 轉成既有指令、經 applyCommand 執行，不繞過冷卻／
// 詠唱／MP 檢查」（WIRE 原文），但它是在 tick() 內部（advanceAutoBattle 由 tick 呼叫，見 tick.ts）
// 對同一份正在建構中的 Ctx 操作——如果直接呼叫公開的 dispatch()/舊版 applyCommand()，會重新
// toCtx(state, now) 複製一份新的工作副本並各自獨立 fromCtx 回傳，導致 tick() 手上那份 ctx 跟
// autopilot 產生的結果對不起來（甚至 dispatch()→tick() 會遞迴呼到自己）。拆分後
// applyCommandOnCtx 直接接受呼叫端已經在用的 ctx、直接在上面 mutate（跟 advanceAllyAI／
// advanceEnemyAI 同一種呼叫慣例），autopilot.ts 可以安全重複呼叫，事件/log/seq 都疊加在同一份
// ctx 上，tick() 收尾時一次性 fromCtx 组裝，行為完全一致。
// 這個拆分對 dispatch() 本身的外部行為零改變：applyCommand(state, cmd, now) 原本「toCtx → 跑
// 一個 case → finish(ctx)」的順序，現在是「toCtx → applyCommandOnCtx（做同一件事但不再自己呼叫
// finish） → finish(ctx)」——finish(ctx) 只會被呼叫恰好一次，呼叫時機（case 執行完、下一行）完全
// 相同，equivalent transformation，不影響任何既有斷言。
import type { Skill } from '../types';
import { resolveWeaponAttack } from './combat';
import type { Ctx } from './context';
import { fromCtx, pushEvent, pushLog, toCtx } from './context';
import { effectiveRating } from './effects';
import { attackCooldownFor, chargeMultiplier, combineIntervalPct, effectiveCastMs, effectiveMpCost, floorInt, isTargetBlocked, NEUTRAL_WEAPON_PROFILE } from './formulas';
import { resolveStrategy } from './strategies';
import type { BattleState, Command, PartyActor } from './types';
import { beginResolving, computeVictoryDefeatDraw, tick } from './tick';

/**
 * 每個指令分支結束前的共同出口：玩家自己這一下攻擊/技能就可能是壓垮敵人的最後一擊，
 * 如果只有 tick() 會檢查勝負，直接呼叫 dispatch() 的呼叫端要等「下一次」呼叫才會看到 resolving/ended，
 * 中間會有一個「敵人明明死光了但 phase 還是 active」的過渡態。這裡跟 tick() 共用同一份判定與同一套
 * 「先 resolving 再 ended」流程（審查修復 #1：勝利不能立刻蓋掉死亡動畫）。
 * P9：applyCommand（薄殼）在 applyCommandOnCtx 執行完後呼叫這支，取代原本每個 case 各自呼叫。
 */
function finish(ctx: Ctx): BattleState {
  const outcome = computeVictoryDefeatDraw(ctx);
  if (outcome) return beginResolving(ctx, outcome);
  return fromCtx(ctx, 'active', null);
}

/** P9：原本的 reject(ctx, reason) 改成純粹寫一行 log、不再自己呼叫 finish（呼叫端統一在
 *  applyCommandOnCtx 執行完後由 applyCommand 呼叫一次）。 */
function rejectLog(ctx: Ctx, reason: string): void {
  pushLog(ctx, `[拒絕] ${reason}`);
}

/**
 * 開始施法：扣 MP、技能進冷卻（規格：「冷卻從施放起算」）、記下 pendingCast 等 tick 到 actionUntil
 * 時結算效果。commitCast 只有 dispatch 的 USE_SKILL 分支會呼叫、actor 一律是玩家（AI 隊友的技能
 * 走 ai.ts 的 resolveSupportSkill/castNow，完全不經過這裡，也不吃 equipmentEffects——見 ai.ts
 * castNow 型別註解）——所以 effectiveCastMs 套用 actor.rating 的 castReductionPct（P3：DEX→詠唱
 * 縮減）與下面 effectiveMpCost 套用 actor.equipmentEffects.mpCostReducePct（P8）在這裡永遠只影響
 * 玩家，不需要另外分支判斷。
 * P8（CONTRACT §2／WIRE「引擎」）：實際扣除的 MP 改用 effectiveMpCost(skill.mpCost,
 * actor.equipmentEffects.mpCostReducePct)——跟下面 USE_SKILL 分支「MP 是否足夠」的檢查呼叫同一支
 * 純函式、同樣的兩個輸入（在同一次指令處理中都不會變），保證兩處算出同一個數字。
 * P9：改成 void（不再自己呼叫 finish，見檔頭註解）。
 */
function commitCast(ctx: Ctx, actor: PartyActor, skill: Skill, targetId: string | 'ALL' | 'ALL_ENEMIES'): void {
  const mpCost = effectiveMpCost(skill.mpCost, actor.equipmentEffects.mpCostReducePct);
  actor.mp = floorInt(actor.mp - mpCost); // P6（CONTRACT §1）：MP 整數不變式，見 formulas.ts floorInt。
  ctx.skillReadyAt[skill.id] = ctx.now + skill.cooldownMs;
  actor.action = 'casting';
  // P5：玩家身上的 buff 可能有 castReductionPct 加成（DEX/INT 效果之外的額外來源），套 effectiveRating
  // 才會反映在施法時間上。
  actor.actionUntil = ctx.now + effectiveCastMs(skill.castMs ?? ctx.cfg.defaultCastMs, effectiveRating(actor.rating, actor.activeEffects), ctx.cfg);
  ctx.pendingCasts[actor.id] = { skillId: skill.id, targetId };
  ctx.targeting = { mode: 'none' };
  pushEvent(ctx, {
    kind: 'skillCast',
    actorId: actor.id,
    skillId: skill.id,
    targetId: targetId === 'ALL' || targetId === 'ALL_ENEMIES' ? null : targetId,
  });
}

/**
 * P9：applyCommand 的核心邏輯，拆出來讓 autopilot.ts 能直接對同一份 ctx 重複呼叫（見檔頭註解）。
 * 純粹 mutate ctx，不回傳值、不判斷勝負——呼叫端（下面的 applyCommand，或 autopilot.ts）各自
 * 決定要不要在之後呼叫 finish(ctx)。
 */
export function applyCommandOnCtx(ctx: Ctx, cmd: Command): void {
  const player = ctx.party.find((p) => p.id === ctx.playerId);
  if (!player) return; // 理論上不會發生（playerId 一定指向 party[0]）。

  // 規格：「guarding 中所有其它指令被拒（回同 state，記 log）」——GUARD_END 是唯一的解除手段。
  // 2026-09-19 修復：SET_AUTO_BATTLE 額外豁免（比照下面 dead 分支對 SELECT_TARGET/SET_TRAY 的
  // 既有例外）——切換自動戰鬥開關只是改 ctx.autoBattle/player.strategyId 這兩個「UI/AI 設定」
  // 欄位，不是戰鬥動作，不該被玩家正在防禦（甚至倒下）這種暫時性動作鎖擋住，否則畫面上的
  // AutoBattleBar 開關在這兩種狀態下會完全沒有反應。
  if (player.action === 'guarding' && cmd.type !== 'GUARD_END' && cmd.type !== 'SET_AUTO_BATTLE') {
    rejectLog(ctx, '防禦中，其它指令一律無效');
    return;
  }
  // 規格：「玩家 dead：所有指令拒絕（除 SELECT_TARGET/SET_TRAY）」——SET_AUTO_BATTLE 理由同上。
  if (player.action === 'dead' && cmd.type !== 'SELECT_TARGET' && cmd.type !== 'SET_TRAY' && cmd.type !== 'SET_AUTO_BATTLE') {
    rejectLog(ctx, '玩家已倒下，指令無效');
    return;
  }

  switch (cmd.type) {
    case 'ATTACK_BEGIN': {
      if (player.action !== 'idle') return rejectLog(ctx, '非待命狀態不能開始攻擊');
      if (player.attackReadyAt > ctx.now) return rejectLog(ctx, '攻擊冷卻中');
      const target = ctx.enemies.find((e) => e.id === ctx.targetId && e.hp > 0);
      if (!target) return rejectLog(ctx, '沒有有效目標');
      player.action = 'charging';
      player.chargeStartedAt = ctx.now;
      pushEvent(ctx, { kind: 'chargeStart', actorId: player.id });
      return;
    }

    case 'ATTACK_RELEASE': {
      if (player.action !== 'charging') return rejectLog(ctx, '沒有正在蓄力的攻擊');
      const holdMs = Math.max(0, ctx.now - (player.chargeStartedAt ?? ctx.now));
      // P7（CONTRACT §3 巨劍）：weaponProfile 缺省時全部欄位＝NEUTRAL_WEAPON_PROFILE 中性值，
      // 下面每一行套用武器欄位的算式因此都會退化成 P1～P6 原本的樣子，既有測試不受影響。
      const weaponProfile = player.weaponProfile ?? NEUTRAL_WEAPON_PROFILE;
      const rawChargeMul = chargeMultiplier(holdMs, ctx.cfg, weaponProfile.chargeTimeMul);
      // CONTRACT §3：「蓄氣倍率超出 1 的部分 ×charge_dmg_mul」，即 1+(chargeMul−1)×chargeDmgMul
      // ——無武器（chargeDmgMul=1）時原樣等於 rawChargeMul。
      const chargeMul = 1 + (rawChargeMul - 1) * weaponProfile.chargeDmgMul;
      const charged = holdMs >= ctx.cfg.chargeMinMs;
      if (ctx.targetId) {
        resolveWeaponAttack(ctx, {
          actorId: player.id,
          attackerStats: player.stats,
          attackerEffects: player.activeEffects,
          attackerRating: player.rating,
          weaponVisual: player.weapon,
          weaponProfile,
          targetEnemyId: ctx.targetId,
          chargeMul,
          charged,
        });
      }
      player.action = 'recovering';
      player.actionUntil = ctx.now + ctx.cfg.recoveryMs;
      // P3：AGI→攻速→攻擊冷卻，只套用在玩家身上（隊友的節奏在 ai.ts 用 allyActIntervalMs 排程，
      // 完全不呼叫 attackCooldownFor，不受這裡的改動影響）。P5：套 effectiveRating 讓玩家身上的
      // aspd buff 也能反映在攻擊冷卻上。P7：intervalPct（細劍/長弓/短弓/弩/斧）乘在算出來的冷卻上。
      // P8：武器與裝備（防具/飾品彙總）的 intervalPct 先相加、clamp ≥ −50（combineIntervalPct），
      // 再交給 attackCooldownFor——沒有裝備效果（equipmentEffects.intervalPct=0）時退化成 P7 原樣。
      player.attackReadyAt =
        ctx.now +
        attackCooldownFor(
          effectiveRating(player.rating, player.activeEffects),
          ctx.cfg,
          combineIntervalPct(weaponProfile.intervalPct, player.equipmentEffects.intervalPct),
        );
      player.chargeStartedAt = null;
      return;
    }

    case 'HOLD_CANCEL': {
      // 規格：拖出/失焦/隱藏取消 → 回 idle、不進 CD、不出傷害（attackReadyAt 完全不動）。
      if (player.action !== 'charging') return rejectLog(ctx, '沒有正在蓄力的攻擊可取消');
      player.action = 'idle';
      player.chargeStartedAt = null;
      pushEvent(ctx, { kind: 'chargeCancel', actorId: player.id });
      return;
    }

    case 'GUARD_BEGIN': {
      if (player.action !== 'idle') return rejectLog(ctx, '非待命狀態不能開始防禦');
      player.action = 'guarding';
      player.actionUntil = Number.POSITIVE_INFINITY; // 防禦沒有自動到期，只能靠 GUARD_END 解除。
      return;
    }

    case 'GUARD_END': {
      if (player.action !== 'guarding') return rejectLog(ctx, '目前沒有在防禦');
      player.action = 'idle';
      return;
    }

    case 'SELECT_TARGET': {
      const enemy = ctx.enemies.find((e) => e.id === cmd.enemyId && e.hp > 0);
      if (!enemy) return rejectLog(ctx, '目標不存在或已消滅');
      // P13（DORPG_P13 CONTRACT §2「SELECT_TARGET 指向被阻擋的後排敵人 → 拒絕並回 log」）：玩家
      // 手上沒有武器（player.weaponProfile 為 null）比照 NEUTRAL_WEAPON_PROFILE 視同 melee——
      // 這是唯一會呼叫 SELECT_TARGET 的角色（AI 隊友的目標選取完全不經過這個指令，見 ai.ts），
      // 不需要另外處理「哪個 actor 在選」的分支。
      const reach = (player.weaponProfile ?? NEUTRAL_WEAPON_PROFILE).reach;
      if (isTargetBlocked(ctx.enemies, enemy, reach)) return rejectLog(ctx, '被前排阻擋');
      ctx.targetId = enemy.id;
      pushEvent(ctx, { kind: 'targetChanged', enemyId: enemy.id });
      return;
    }

    case 'SET_TRAY': {
      ctx.trayMode = cmd.mode;
      return;
    }

    case 'USE_SKILL': {
      const skill = ctx.skills.find((s) => s?.id === cmd.skillId) ?? null;
      if (!skill) return rejectLog(ctx, '技能未裝備');
      // P5（CONTRACT §5 special 詞彙）：implemented=false 的技能一律直接拒絕，且發專屬事件
      // 讓 FRONTEND 能跳「尚未實裝」提示，跟其它拒絕只留一行 log 不同——這個檢查刻意放在最前面，
      // 不管冷卻/MP/action 狀態如何，這種技能永遠不能用。
      if (skill.implemented === false) {
        pushEvent(ctx, { kind: 'skillUnavailable', actorId: player.id, skillId: skill.id });
        rejectLog(ctx, `${skill.name} 尚未實裝`);
        return;
      }
      // 防呆：passive 依規則不該出現在技能欄（後端已把它算進玩家 stats），正常情況下不會走到這裡；
      // 萬一離線資料/測試資料誤塞了一顆，明確拒絕比讓它落進下面的 target 判斷、意外套用一次 buff/
      // debuff 邏輯要安全。
      if (skill.kind === 'passive') return rejectLog(ctx, '被動技能不會出現在技能欄，不可主動施放');
      if ((ctx.skillReadyAt[skill.id] ?? 0) > ctx.now) return rejectLog(ctx, '技能冷卻中');
      // P8（CONTRACT §2／WIRE「引擎」）：MP 是否足夠的檢查改用 effectiveMpCost（同一支函式、同樣
      // 兩個輸入也用於 commitCast 的實際扣除，見該函式呼叫端註解），不再直接比對未打折的
      // skill.mpCost——equipmentEffects.mpCostReducePct=0（無裝備）時退化成原本的行為。
      if (player.mp < effectiveMpCost(skill.mpCost, player.equipmentEffects.mpCostReducePct)) return rejectLog(ctx, 'MP 不足');
      if (player.action !== 'idle') return rejectLog(ctx, '非待命狀態不能施放技能');

      if (skill.target === 'ally') {
        // 規格：「需要選隊友的技能：第一次無 targetId → targeting=chooseAlly；再送一次帶 targetId」。
        if (cmd.targetId === undefined) {
          ctx.targeting = { mode: 'chooseAlly', skillId: skill.id };
          return;
        }
        const ally = ctx.party.find((p) => p.id === cmd.targetId);
        if (!ally || ally.hp <= 0) return rejectLog(ctx, '無效的隊友目標'); // 死亡隊友只能靠道具復甦，技能不能選
        commitCast(ctx, player, skill, ally.id);
        return;
      }
      if (skill.target === 'enemy') {
        if (!ctx.targetId || !ctx.enemies.find((e) => e.id === ctx.targetId && e.hp > 0)) {
          return rejectLog(ctx, '沒有有效目標');
        }
        commitCast(ctx, player, skill, ctx.targetId);
        return;
      }
      if (skill.target === 'self') {
        commitCast(ctx, player, skill, player.id);
        return;
      }
      // P5：target='allEnemies'（damage/debuff 專用）跟既有 'allAllies' 對稱，同樣不要求事先選好
      // 目標——資源結算時 (resolveCastEffect/resolveBuffDebuff) 才即時篩選存活敵人。
      if (skill.target === 'allEnemies') {
        commitCast(ctx, player, skill, 'ALL_ENEMIES');
        return;
      }
      commitCast(ctx, player, skill, 'ALL'); // allAllies
      return;
    }

    case 'USE_ITEM': {
      if (player.action !== 'idle') return rejectLog(ctx, '非待命狀態不能使用道具');
      const entry = ctx.items.find((i) => i.def.id === cmd.itemId);
      if (!entry || entry.quantity <= 0) return rejectLog(ctx, '道具數量不足');
      // 規格：「USE_ITEM → 需選隊友（chooseAlly）」，跟技能的兩段式流程對稱。
      if (cmd.targetId === undefined) {
        ctx.targeting = { mode: 'chooseAlly', itemId: entry.def.id };
        return;
      }
      const target = ctx.party.find((p) => p.id === cmd.targetId);
      const isRevive = entry.def.kind === 'revive';
      const validTarget = !!target && (isRevive ? target.hp <= 0 : target.hp > 0);
      if (!validTarget || !target) {
        // 規格：「失敗（無效目標/數量0）→維持items模式」；targeting 清掉讓玩家能重新選，trayMode 不動。
        ctx.targeting = { mode: 'none' };
        rejectLog(ctx, '無效的道具目標');
        return;
      }
      entry.quantity -= 1;
      if (isRevive) {
        // P6（CONTRACT §1）：復活量是「hpMax 的 amount%」，跟 hp_regen_pct 同一種百分比運算，
        // 一併改 Math.round 為 floorInt（向下取整）。
        target.hp = floorInt(target.stats.hpMax * (entry.def.amount / 100));
        target.action = 'idle';
        pushEvent(ctx, { kind: 'actorRevive', actorId: target.id });
      } else if (entry.def.kind === 'hp') {
        target.hp = floorInt(Math.min(target.stats.hpMax, target.hp + entry.def.amount));
      } else {
        target.mp = floorInt(Math.min(target.stats.mpMax, target.mp + entry.def.amount));
      }
      pushEvent(ctx, { kind: 'itemUsed', itemId: entry.def.id, targetId: target.id, amount: entry.def.amount });
      ctx.trayMode = 'skills';
      ctx.targeting = { mode: 'none' };
      player.action = 'recovering';
      player.actionUntil = ctx.now + ctx.cfg.recoveryMs;
      return;
    }

    case 'CANCEL_TARGETING': {
      if (ctx.targeting.mode === 'none') return rejectLog(ctx, '目前沒有在選擇目標');
      ctx.targeting = { mode: 'none' };
      return;
    }

    case 'TRY_ESCAPE': {
      if (player.action !== 'idle') return rejectLog(ctx, '非待命狀態不能嘗試逃跑');
      if (ctx.escape.flow !== 'available') return rejectLog(ctx, '目前無法逃跑');
      ctx.escape.flow = 'judging';
      ctx.escape.judgingUntil = ctx.now + ctx.cfg.escapeJudgeMs;
      ctx.escape.message = '';
      pushEvent(ctx, { kind: 'escapeJudging' });
      return;
    }

    case 'SET_AUTO_BATTLE': {
      // P9（CONTRACT §1／WIRE「引擎」）：本地立即生效，不驗證/不打 API（持久化是前端另外呼叫
      // `PUT /rpg/auto-battle` 的事）。strategyId 一律經 resolveStrategy 正規化——未知 id 存
      // 'balanced'，不會把非法字串留在 PartyActor.strategyId 上讓後續每次 decideAction 都要重新
      // 正規化一次。
      ctx.autoBattle = cmd.enabled;
      player.strategyId = resolveStrategy(cmd.strategyId, ctx.cfg.aiStrategies).id;
      return;
    }

    default: {
      const _exhaustive: never = cmd;
      return _exhaustive;
    }
  }
}

function applyCommand(state: BattleState, cmd: Command, now: number): BattleState {
  if (state.phase !== 'active') return state; // 契約：「ended 後所有指令拒絕」。
  const ctx = toCtx(state, now);
  applyCommandOnCtx(ctx, cmd);
  return finish(ctx);
}

export function dispatch(state: BattleState, cmd: Command, now: number): BattleState {
  const ticked = tick(state, now);
  return applyCommand(ticked, cmd, now);
}
