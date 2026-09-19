// P9（DORPG_P9 CONTRACT §1/§4/§5、WIRE「引擎」）：玩家自動戰鬥。tick.ts 在隊友 AI 之前呼叫
// advanceAutoBattle(ctx)——跟 advanceAllyAI(ctx, actor) 同一種呼叫慣例，直接對 tick() 正在建構
// 的同一份 ctx 操作，不透過 dispatch()（會遞迴呼到 tick 自己，見 dispatch.ts 檔頭註解）。
// 指令一律經 dispatch.ts 匯出的 applyCommandOnCtx 落地，不繞過冷卻／詠唱／MP 檢查（WIRE 原文）
// ——這裡只負責「決定要送哪個指令」，實際驗證/扣資源/推事件仍是 applyCommandOnCtx 的既有邏輯。
//
// 職責劃分（見 types.ts Decision 型別註解）：decideAction（ai.ts）只回答「要不要治療/上buff/
// 打誰/選哪顆技能」，永遠不回傳 'guard'/'item'；防禦與吃藥是「只對玩家有意義、且必須在呼叫
// decideAction 之前就決定好」的獨立判斷，因此整個寫在這個檔案裡，不混進 ai.ts 的五段優先序。
import { decideAction } from './ai';
import type { Ctx } from './context';
import { applyCommandOnCtx } from './dispatch';
import { NEUTRAL_WEAPON_PROFILE } from './formulas';
import { boolParam, resolveStrategy } from './strategies';
import type { Decision, PartyActor } from './types';

/** 敵人是否有 windup 中、且鎖定這個角色（GUARD_BEGIN/GUARD_END 判斷依據，跟玩家手動防禦讀的
 *  是同一張 ctx.enemyTargets 表）。 */
function isWindupTargeting(ctx: Ctx, actorId: string): boolean {
  return ctx.enemies.some((e) => e.hp > 0 && e.anim === 'windup' && ctx.enemyTargets[e.id] === actorId);
}

function isEnemyTargetId(ctx: Ctx, id: string): boolean {
  return ctx.enemies.some((e) => e.id === id);
}

/** 目標跟目前 ctx.targetId 不同時才補一次 SELECT_TARGET（focus_fire/element_advantage/
 *  protect_allies 這幾種策略可能選出跟玩家手動鎖定不同的目標，見 CONTRACT §4）。 */
function selectEnemyTargetIfNeeded(ctx: Ctx, enemyId: string): void {
  if (ctx.targetId !== enemyId) applyCommandOnCtx(ctx, { type: 'SELECT_TARGET', enemyId });
}

/**
 * 把 decideAction 回傳的 Decision 轉成既有指令，經 applyCommandOnCtx 執行。attack 只發
 * ATTACK_BEGIN（蓄氣→放開跨 tick 進行，見 advanceAutoBattle 的 'charging' 分支，跟玩家手動
 * 按住/放開攻擊鈕同一個節奏，不在同一個 tick 內就把兩個指令都做完）。
 */
function applyDecisionForPlayer(ctx: Ctx, decision: Decision): void {
  switch (decision.kind) {
    case 'attack': {
      if (!decision.targetId || decision.targetId === 'ALL' || decision.targetId === 'ALL_ENEMIES') return;
      selectEnemyTargetIfNeeded(ctx, decision.targetId);
      applyCommandOnCtx(ctx, { type: 'ATTACK_BEGIN' });
      return;
    }
    case 'heal':
    case 'buff':
    case 'damage':
    case 'debuff': {
      if (!decision.skillId) return;
      const targetId = decision.targetId;
      if (targetId && targetId !== 'ALL' && targetId !== 'ALL_ENEMIES' && isEnemyTargetId(ctx, targetId)) {
        selectEnemyTargetIfNeeded(ctx, targetId);
      }
      const cmdTargetId = targetId === 'ALL' || targetId === 'ALL_ENEMIES' ? undefined : targetId;
      applyCommandOnCtx(ctx, { type: 'USE_SKILL', skillId: decision.skillId, targetId: cmdTargetId });
      return;
    }
    default:
      return; // 'wait'/'guard'/'item'——decideAction 對玩家不會產生這些（見型別註解），防呆保留。
  }
}

/**
 * P9：玩家自動戰鬥。ctx.autoBattle 關閉、玩家不存在／已倒下時整個跳過（跟 advanceAllyAI 對隊友
 * 的早退寫法一致）。四個互斥狀態分支，對齊玩家的 ActorActionState：
 *   - 'guarding'：先決定要不要維持防禦（windup 已結束或 auto_guard 關閉 → GUARD_END）。
 *   - 'charging'：上一次由這裡發起的 ATTACK_BEGIN 蓄力中，蓄滿武器的 chargeTime 才放開。
 *   - 'idle'：可以開始新動作——防禦優先，其次藥水，最後才是 decideAction 的技能/普攻決策。
 *   - 其它（casting/recovering/dead）：這個 tick 不出手，下次再試（跟隊友 AI 的忙碌檢查同精神）。
 * 手動指令插隊（CONTRACT §1）：這支函式完全不檢查「這個 tick 稍後是否有玩家手動指令」——
 * dispatch(state, manualCmd, now) 的執行順序是先 tick()（含這支函式）才套用 manualCmd，若
 * 自動戰鬥已經佔用了這個 tick 的動作（例如剛送出 ATTACK_BEGIN），手動指令會因為
 * player.action 不再是 idle 而被 applyCommandOnCtx 拒絕（一行 log，不會壞），下一個可行動的
 * tick 手動指令自然會成功——不需要額外的鎖或旗標，符合「插隊執行一次、之後 AI 接續」的精神。
 */
export function advanceAutoBattle(ctx: Ctx): void {
  if (!ctx.autoBattle) return;
  const player = ctx.party.find((p) => p.isPlayer);
  if (!player || player.hp <= 0) return;

  const strategy = resolveStrategy(player.strategyId, ctx.cfg.aiStrategies);
  const autoGuard = boolParam(strategy.params, 'auto_guard', false);

  if (player.action === 'guarding') {
    if (!(autoGuard && isWindupTargeting(ctx, player.id))) applyCommandOnCtx(ctx, { type: 'GUARD_END' });
    return;
  }

  if (player.action === 'charging') {
    const weaponProfile: PartyActor['weaponProfile'] = player.weaponProfile ?? NEUTRAL_WEAPON_PROFILE;
    const fullMs = ctx.cfg.chargeMinMs + ctx.cfg.chargeFullMs * weaponProfile.chargeTimeMul;
    const heldMs = ctx.now - (player.chargeStartedAt ?? ctx.now);
    if (heldMs >= fullMs) applyCommandOnCtx(ctx, { type: 'ATTACK_RELEASE' });
    return;
  }

  if (player.action !== 'idle') return;

  if (autoGuard && isWindupTargeting(ctx, player.id)) {
    applyCommandOnCtx(ctx, { type: 'GUARD_BEGIN' });
    return;
  }

  // CONTRACT §4：「HP%<30 且有藥水則用（沿用既有 ITEM 動作）」——只挑 hp 類藥水，revive 用不到
  // （玩家還活著才會走到這裡）。
  const hpPct = player.stats.hpMax > 0 ? (player.hp / player.stats.hpMax) * 100 : 100;
  if (hpPct < 30) {
    const potion = ctx.items.find((i) => i.def.kind === 'hp' && i.quantity > 0);
    if (potion) {
      applyCommandOnCtx(ctx, { type: 'USE_ITEM', itemId: potion.def.id, targetId: player.id });
      return;
    }
  }

  const decision = decideAction(ctx, player, strategy);
  applyDecisionForPlayer(ctx, decision);
}
